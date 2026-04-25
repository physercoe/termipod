package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// newTestServer spins up a Server backed by a temp DB + data root. Used by
// template + spawn tests that need to exercise renderSpawnSpec against real
// storage (journal files, agent rows).
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	cfg := Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Seed the default team so DoSpawn's FK to teams(id) holds. We go
	// direct to SQL to skip the full init() helper; tests that exercise
	// the init CLI path can use a separate fixture.
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		defaultTeamID, "test-team", NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return s, dir
}

func TestRenderSpawnSpec_Basics(t *testing.T) {
	s, _ := newTestServer(t)
	in := spawnIn{
		ChildHandle: "worker-1",
		Kind:        "claude-code",
		SpawnSpec:   "handle: {{handle}}\nkind: {{kind}}\nteam: {{team}}\n",
	}
	got, err := s.renderSpawnSpec(context.Background(), defaultTeamID, in, "@principal")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "handle: worker-1\nkind: claude-code\nteam: " + defaultTeamID + "\n"
	if got != want {
		t.Errorf("render mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderSpawnSpec_MissingVarEmpties(t *testing.T) {
	s, _ := newTestServer(t)
	in := spawnIn{
		ChildHandle: "w", Kind: "k",
		// {{unknown}} has no binding; must expand to empty string so YAML
		// stays parseable instead of being left as a literal placeholder.
		SpawnSpec: "x: [{{unknown}}]\n",
	}
	got, _ := s.renderSpawnSpec(context.Background(), defaultTeamID, in, "")
	if got != "x: []\n" {
		t.Errorf("unknown var: got %q", got)
	}
}

func TestRenderSpawnSpec_JournalFromParent(t *testing.T) {
	s, dataRoot := newTestServer(t)
	ctx := context.Background()

	// Create a parent agent with a handle, then write a journal for it.
	parentID := NewID()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (id, team_id, handle, kind, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		parentID, defaultTeamID, "lead", "claude-code", NowUTC()); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	journalDir := filepath.Join(dataRoot, "agents", "journals", defaultTeamID)
	if err := os.MkdirAll(journalDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, "lead.md"),
		[]byte("## notes\n\nprevious context goes here\n"), 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	in := spawnIn{
		ParentID:    parentID,
		ChildHandle: "helper",
		Kind:        "claude-code",
		SpawnSpec:   "parent: {{parent_handle}}\n---\n{{journal}}\n---\n",
	}
	got, err := s.renderSpawnSpec(ctx, defaultTeamID, in, "@principal")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "parent: lead") {
		t.Errorf("parent_handle not rendered:\n%s", got)
	}
	if !strings.Contains(got, "previous context goes here") {
		t.Errorf("journal body not inlined:\n%s", got)
	}
}

func TestRenderSpawnSpec_NoPlaceholdersShortCircuits(t *testing.T) {
	// If the input has no {{…}} we must return the original string unchanged
	// — the hot path for every spawn that doesn't opt into templating.
	s, _ := newTestServer(t)
	raw := "kind: claude-code\nbackend:\n  cmd: echo hi\n"
	in := spawnIn{ChildHandle: "w", Kind: "k", SpawnSpec: raw}
	got, err := s.renderSpawnSpec(context.Background(), defaultTeamID, in, "@principal")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != raw {
		t.Errorf("no-placeholder fast path mutated input")
	}
}

func TestPrincipalFromScope(t *testing.T) {
	cases := []struct{ scope, want string }{
		{`{"role":"principal","team":"t","handle":"physercoe"}`, "@physercoe"},
		{`{"role":"principal","team":"t"}`, "@principal"},
		{`{"role":"steward"}`, "@steward"},
		{`{"handle":"solo"}`, "@solo"}, // handle wins even without role
		{`{}`, "@principal"},
		{``, "@principal"},
		{`not json`, "@principal"},
	}
	for _, c := range cases {
		got := principalFromScope(c.scope)
		if got != c.want {
			t.Errorf("principalFromScope(%q) = %q, want %q", c.scope, got, c.want)
		}
	}
}

func TestRenderSpawnSpec_PrincipalHandleDottedVar(t *testing.T) {
	// {{principal.handle}} should resolve to the bare handle (no leading
	// `@`) so prompt files can address the user naturally. Prompts that
	// want the role-prefixed form keep using {{principal}}.
	s, _ := newTestServer(t)
	in := spawnIn{
		ChildHandle: "w",
		Kind:        "k",
		SpawnSpec:   "owner: {{principal.handle}}\nrole: {{principal}}\n",
	}
	got, err := s.renderSpawnSpec(context.Background(), defaultTeamID, in, "@physercoe")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "owner: physercoe\nrole: @physercoe\n"
	if got != want {
		t.Errorf("dotted var\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestResolveContextFiles_InlinesPromptFromEmbedded(t *testing.T) {
	// Built-in steward.v1.md ships with the binary. Resolution should
	// read it from the embedded FS, expand {{principal.handle}}, and
	// inline the rendered body under context_files.CLAUDE.md so the
	// host-runner launcher can write it into the agent's workdir.
	s, _ := newTestServer(t)
	rendered := "kind: claude-code\nprompt: steward.v1.md\n"
	vars := map[string]string{"principal.handle": "physercoe"}
	got, err := s.resolveContextFiles(rendered, vars, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(got, "context_files:") {
		t.Fatalf("missing context_files key:\n%s", got)
	}
	if !strings.Contains(got, "CLAUDE.md:") {
		t.Fatalf("missing CLAUDE.md entry:\n%s", got)
	}
	if strings.Contains(got, "{{principal.handle}}") {
		t.Errorf("{{principal.handle}} not expanded:\n%s", got)
	}
	if !strings.Contains(got, "physercoe") {
		t.Errorf("expanded handle not present:\n%s", got)
	}

	// Round-trip parse to confirm host-runner can read it back.
	var parsed struct {
		ContextFiles map[string]string `yaml:"context_files"`
	}
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !strings.Contains(parsed.ContextFiles["CLAUDE.md"], "physercoe") {
		t.Errorf("CLAUDE.md body missing handle: %q",
			parsed.ContextFiles["CLAUDE.md"])
	}
}

func TestResolveContextFiles_DiskOverlayWins(t *testing.T) {
	// User-edited prompts under <dataRoot>/team/templates/prompts beat
	// the embedded built-in. This is the same convention agents/policies
	// follow on first init.
	s, dataRoot := newTestServer(t)
	dir := filepath.Join(dataRoot, "team", "templates", "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	custom := "user-edited prompt for {{principal.handle}}\n"
	if err := os.WriteFile(filepath.Join(dir, "steward.v1.md"),
		[]byte(custom), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	rendered := "prompt: steward.v1.md\n"
	got, err := s.resolveContextFiles(rendered,
		map[string]string{"principal.handle": "alice"}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(got, "user-edited prompt for alice") {
		t.Errorf("disk overlay not used:\n%s", got)
	}
}

func TestResolveContextFiles_AppendsPersonaSeed(t *testing.T) {
	// Mobile bootstrap supplies a free-form persona seed; the hub should
	// concatenate it onto the rendered CLAUDE.md under a labeled section
	// so a human reading the file can tell which lines came from the
	// template vs. the operator.
	s, _ := newTestServer(t)
	rendered := "prompt: steward.v1.md\n"
	got, err := s.resolveContextFiles(rendered,
		map[string]string{"principal.handle": "alice"},
		"You are terse. Always cite line numbers.")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var parsed struct {
		ContextFiles map[string]string `yaml:"context_files"`
	}
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	body := parsed.ContextFiles["CLAUDE.md"]
	if !strings.Contains(body, "# Steward Agent") {
		t.Errorf("template body missing:\n%s", body)
	}
	if !strings.Contains(body, "## Persona override") {
		t.Errorf("override header missing:\n%s", body)
	}
	if !strings.Contains(body, "You are terse") {
		t.Errorf("seed text missing:\n%s", body)
	}
	if strings.Index(body, "# Steward Agent") >
		strings.Index(body, "## Persona override") {
		t.Errorf("override appended in wrong position:\n%s", body)
	}
}

func TestResolveContextFiles_PersonaSeedWithoutPrompt(t *testing.T) {
	// Even when the template has no prompt: field, supplying a seed
	// should still produce a CLAUDE.md so a hand-rolled spawn can
	// author its persona inline.
	s, _ := newTestServer(t)
	got, err := s.resolveContextFiles("kind: x\n", map[string]string{}, "be terse")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var parsed struct {
		ContextFiles map[string]string `yaml:"context_files"`
	}
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !strings.Contains(parsed.ContextFiles["CLAUDE.md"], "be terse") {
		t.Errorf("seed not included:\n%s", parsed.ContextFiles["CLAUDE.md"])
	}
}

func TestResolveContextFiles_NoPromptFieldUnchanged(t *testing.T) {
	s, _ := newTestServer(t)
	in := "backend:\n  cmd: echo hi\n"
	got, err := s.resolveContextFiles(in, map[string]string{}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != in {
		t.Errorf("unchanged spec mutated:\n%s", got)
	}
}

func TestResolveContextFiles_ExplicitOverrideWins(t *testing.T) {
	// If a spec already declares context_files.CLAUDE.md, leave it alone
	// untouched. An operator who hand-rolled the override doesn't want
	// the templated body silently re-merged in.
	s, _ := newTestServer(t)
	in := "prompt: steward.v1.md\ncontext_files:\n  CLAUDE.md: \"my override\"\n"
	got, err := s.resolveContextFiles(in, map[string]string{}, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != in {
		t.Errorf("explicit override mutated:\n%s", got)
	}
}

func TestResolveContextFiles_MissingPromptErrors(t *testing.T) {
	// A template referencing a non-existent prompt is a config bug —
	// surface it loudly rather than silently spawning a contextless agent.
	s, _ := newTestServer(t)
	in := "prompt: does-not-exist.md\n"
	_, err := s.resolveContextFiles(in, map[string]string{}, "")
	if err == nil {
		t.Fatal("want error for missing prompt; got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist.md") {
		t.Errorf("error = %v; want to mention missing prompt name", err)
	}
}

// TestRenderSpawnSpec_FixedPointMCPNamespace verifies that a permission
// flag value embedding {{mcp_namespace}} is recursively expanded. This
// is the wedge that lets templates derive the MCP server name from
// hub.MCPServerName instead of hardcoding "termipod" in three places.
func TestRenderSpawnSpec_FixedPointMCPNamespace(t *testing.T) {
	s, _ := newTestServer(t)
	spec := strings.Join([]string{
		"backend:",
		"  permission_modes:",
		"    prompt: --permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt",
		"  cmd: claude {{permission_flag}}",
		"",
	}, "\n")
	in := spawnIn{
		ChildHandle:    "w",
		Kind:           "claude-code",
		SpawnSpec:      spec,
		PermissionMode: "prompt",
	}
	got, err := s.renderSpawnSpec(context.Background(), defaultTeamID, in, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "claude --permission-prompt-tool mcp__termipod__permission_prompt"
	if !strings.Contains(got, want) {
		t.Errorf("fixed-point expansion did not resolve mcp_namespace:\n%s", got)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("placeholders remained after expansion:\n%s", got)
	}
}

// sanity: the server helper above should not leak a closed DB.
var _ = sql.ErrNoRows

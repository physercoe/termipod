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
	got, err := s.resolveContextFiles(defaultTeamID, rendered, vars, "", "")
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
	got, err := s.resolveContextFiles(defaultTeamID, rendered,
		map[string]string{"principal.handle": "alice"}, "", "")
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
	got, err := s.resolveContextFiles(defaultTeamID, rendered,
		map[string]string{"principal.handle": "alice"},
		"You are terse. Always cite line numbers.", "")
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

func TestResolveContextFiles_AppendsTaskSection(t *testing.T) {
	// ADR-029 W2.6: when a spawn carries an inline task (or a task_id
	// linkage), the rendered task instructions must land in CLAUDE.md
	// under a `## Task` section so the worker reads them on first turn
	// without needing a follow-up a2a.invoke. The section sits AFTER
	// any persona override (persona = who, task = what to do).
	s, _ := newTestServer(t)
	rendered := "prompt: steward.v1.md\n"
	got, err := s.resolveContextFiles(defaultTeamID, rendered,
		map[string]string{"principal.handle": "alice"},
		"You are terse.",
		"# Investigate 502 spike\n\nLook at the last hour of logs and report findings.")
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
	if !strings.Contains(body, "## Task") {
		t.Fatalf("task header missing:\n%s", body)
	}
	if !strings.Contains(body, "Investigate 502 spike") {
		t.Errorf("task title missing:\n%s", body)
	}
	if !strings.Contains(body, "last hour of logs") {
		t.Errorf("task body missing:\n%s", body)
	}
	if strings.Index(body, "## Persona override") > strings.Index(body, "## Task") {
		t.Errorf("task should come after persona override:\n%s", body)
	}
}

func TestResolveContextFiles_TaskWithoutPromptOrSeed(t *testing.T) {
	// A spawn with neither prompt: nor persona seed should still
	// materialize a CLAUDE.md when a task is provided — otherwise the
	// worker boots into a blank workdir and the user's "spawn-for-task"
	// gesture silently dies.
	s, _ := newTestServer(t)
	got, err := s.resolveContextFiles(defaultTeamID, "kind: x\n", map[string]string{}, "",
		"# Just the task title\n")
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
	if !strings.Contains(body, "## Task") {
		t.Fatalf("task header missing:\n%s", body)
	}
	if !strings.Contains(body, "Just the task title") {
		t.Errorf("task content missing:\n%s", body)
	}
}

func TestRenderTaskInstructions_Shapes(t *testing.T) {
	cases := []struct {
		name                  string
		title, body           string
		projectID, taskID     string
		wantEmpty             bool
		wantHas               []string
		wantFooter            bool
	}{
		{"both no IDs", "Investigate 502s", "Look at the logs.", "", "", false,
			[]string{"# Investigate 502s", "Look at the logs."}, false},
		{"title only no IDs", "Investigate 502s", "", "", "", false,
			[]string{"# Investigate 502s"}, false},
		{"body only no IDs", "", "Look at the logs.", "", "", false,
			[]string{"Look at the logs."}, false},
		{"neither", "", "", "", "", true, nil, false},
		{"whitespace only", "   ", "\n\n", "", "", true, nil, false},
		// With IDs the footer renders carrying both literal values + the
		// protocol-not-domain framing that overrides task-body restrictions.
		{"both with IDs", "Investigate 502s", "Look at the logs.",
			"01PROJ", "01TASK", false,
			[]string{
				"# Investigate 502s",
				"Look at the logs.",
				"Task close-out protocol",
				"tasks.complete(",
				"project_id=\"01PROJ\"",
				"task=\"01TASK\"",
				"tasks.update(",
				"status=\"blocked\"",
				"orchestration protocol, not",
				"task.notify",
			}, true},
		// IDs present but one missing → no footer (defensive).
		{"only project_id", "T", "", "01PROJ", "", false,
			[]string{"# T"}, false},
		{"only task_id", "T", "", "", "01TASK", false,
			[]string{"# T"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTaskInstructions(tc.title, tc.body, tc.projectID, tc.taskID)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("want empty; got %q", got)
				}
				return
			}
			for _, w := range tc.wantHas {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in %q", w, got)
				}
			}
			hasFooter := strings.Contains(got, "Task close-out protocol")
			if hasFooter != tc.wantFooter {
				t.Errorf("footer presence = %v; want %v\n---\n%s", hasFooter, tc.wantFooter, got)
			}
		})
	}
}

// Per-engine memory file routing: the persona/task body must land
// under the filename the engine actually opens. Hardcoding CLAUDE.md
// was the original bug — codex/kimi never read it, gemini-cli prefers
// GEMINI.md.
func TestResolveContextFiles_PerEngineMemoryFilename(t *testing.T) {
	cases := []struct {
		name        string
		backendKind string
		wantFile    string
	}{
		{"claude-code → CLAUDE.md", "claude-code", "CLAUDE.md"},
		{"codex → AGENTS.md", "codex", "AGENTS.md"},
		{"kimi-code → AGENTS.md", "kimi-code", "AGENTS.md"},
		{"gemini-cli → GEMINI.md", "gemini-cli", "GEMINI.md"},
		{"empty/unknown → CLAUDE.md (legacy default)", "", "CLAUDE.md"},
		{"future-unknown → CLAUDE.md (legacy default)", "totally-new-engine", "CLAUDE.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			spec := "backend:\n  kind: " + tc.backendKind + "\nprompt: steward.v1.md\n"
			got, err := s.resolveContextFiles(defaultTeamID, spec,
				map[string]string{"principal.handle": "alice"},
				"You are terse.", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			var parsed struct {
				ContextFiles map[string]string `yaml:"context_files"`
			}
			if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, ok := parsed.ContextFiles[tc.wantFile]; !ok {
				t.Errorf("missing %q in context_files: %v", tc.wantFile, parsed.ContextFiles)
			}
			// For non-default kinds we also expect the legacy CLAUDE.md
			// key to NOT be present (otherwise we'd double-write).
			if tc.wantFile != "CLAUDE.md" {
				if _, present := parsed.ContextFiles["CLAUDE.md"]; present {
					t.Errorf("CLAUDE.md present alongside %q — should be one or the other: %v",
						tc.wantFile, parsed.ContextFiles)
				}
			}
		})
	}
}

// Override gate must use the engine-aware filename too — a codex spec
// that hand-rolls AGENTS.md should be respected, not overwritten.
func TestResolveContextFiles_RespectsEngineSpecificOverride(t *testing.T) {
	s, _ := newTestServer(t)
	spec := "backend:\n  kind: codex\nprompt: steward.v1.md\n" +
		"context_files:\n  AGENTS.md: |\n    hand-rolled body\n"
	got, err := s.resolveContextFiles(defaultTeamID, spec, map[string]string{}, "ignored", "ignored task")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.Contains(got, "hand-rolled body") {
		t.Errorf("operator-authored AGENTS.md got overwritten:\n%s", got)
	}
	if strings.Contains(got, "## Persona override") || strings.Contains(got, "## Task") {
		t.Errorf("seed/task leaked into a spec that already provided AGENTS.md:\n%s", got)
	}
}

// Pure-function unit test for the lookup table; keeps the matrix
// visible without needing to spin up a server.
func TestContextFileNameForKind(t *testing.T) {
	cases := map[string]string{
		"claude-code":        "CLAUDE.md",
		"codex":              "AGENTS.md",
		"kimi-code":          "AGENTS.md",
		"gemini-cli":         "GEMINI.md",
		// antigravity reads BOTH AGENTS.md and GEMINI.md (host-verified
		// — both strings present in agy 1.0.1 binary). We hand it the
		// cross-engine AGENTS.md name so the persona stays consistent
		// across agy / codex / kimi without a per-engine fork.
		"antigravity":        "AGENTS.md",
		"":                   "CLAUDE.md",
		"totally-new-engine": "CLAUDE.md",
	}
	for kind, want := range cases {
		t.Run(kind, func(t *testing.T) {
			if got := contextFileNameForKind(kind); got != want {
				t.Errorf("kind=%q → %q; want %q", kind, got, want)
			}
		})
	}
}

func TestResolveContextFiles_PersonaSeedWithoutPrompt(t *testing.T) {
	// Even when the template has no prompt: field, supplying a seed
	// should still produce a CLAUDE.md so a hand-rolled spawn can
	// author its persona inline.
	s, _ := newTestServer(t)
	got, err := s.resolveContextFiles(defaultTeamID, "kind: x\n", map[string]string{}, "be terse", "")
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
	got, err := s.resolveContextFiles(defaultTeamID, in, map[string]string{}, "", "")
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
	got, err := s.resolveContextFiles(defaultTeamID, in, map[string]string{}, "", "")
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
	_, err := s.resolveContextFiles(defaultTeamID, in, map[string]string{}, "", "")
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

// TestBackendVarsFromSpec_EmptyModeDefaultsToSkip locks v1.0.617's fix
// for the worker-can't-write bug: when the caller passes an empty
// PermissionMode (notably the MCP `agents.spawn` path before the schema
// added the field), the helper must rewrite to "skip" so the rendered
// cmd lands `--dangerously-skip-permissions`. The earlier "fall through
// to claude default" behaviour caused stream-json --print to deny
// Write/Edit/Bash with no attention_item to surface, stalling tasks.
func TestBackendVarsFromSpec_EmptyModeDefaultsToSkip(t *testing.T) {
	spec := strings.Join([]string{
		"backend:",
		"  model: claude-opus-4-7",
		"  permission_modes:",
		"    skip: --dangerously-skip-permissions",
		"    prompt: --permission-prompt-tool mcp__{{mcp_namespace}}__permission_prompt",
		"",
	}, "\n")
	model, flag := backendVarsFromSpec(spec, "")
	if model != "claude-opus-4-7" {
		t.Errorf("model = %q; want claude-opus-4-7", model)
	}
	if flag != "--dangerously-skip-permissions" {
		t.Errorf("empty mode → flag = %q; want --dangerously-skip-permissions", flag)
	}

	// Explicit skip continues to resolve to the same flag (no regression).
	_, flag = backendVarsFromSpec(spec, "skip")
	if flag != "--dangerously-skip-permissions" {
		t.Errorf(`explicit "skip" mode → flag = %q; want --dangerously-skip-permissions`, flag)
	}

	// Explicit prompt still works (no regression on the override path).
	_, flag = backendVarsFromSpec(spec, "prompt")
	if !strings.Contains(flag, "--permission-prompt-tool") {
		t.Errorf(`explicit "prompt" mode → flag = %q; want --permission-prompt-tool`, flag)
	}

	// Unknown mode → empty lookup (no skip fallback for typo'd modes;
	// the safety hatch is only for the empty-string default case).
	_, flag = backendVarsFromSpec(spec, "bogus")
	if flag != "" {
		t.Errorf(`unknown mode "bogus" → flag = %q; want ""`, flag)
	}

	// Spec with no permission_modes block → empty, regardless of mode.
	bareSpec := "backend:\n  model: claude-opus-4-7\n"
	_, flag = backendVarsFromSpec(bareSpec, "")
	if flag != "" {
		t.Errorf("spec without permission_modes → flag = %q; want \"\"", flag)
	}
}

// TestRenderSpawnSpec_TemplateIndirectionExpandsModel locks the
// v1.0.624 fix: when the caller passes `spawn_spec_yaml: "template:
// agents.coder"`, the rendered cmd MUST embed the template's
// backend.model (`claude-opus-4-7`), not an empty string. The earlier
// behaviour read backend vars from the pre-merge spec (which only
// had `template:` and no `backend:`), expanded `{{model}}` to "" and
// produced `claude --model --print …`. The claude CLI then
// swallowed `--print` as the model value, the API rejected
// "--print" as a model name, and the worker died on its first turn.
// The fix moves the template merge into buildSpawnVars so var
// extraction sees the same backend block renderSpawnSpec does.
func TestRenderSpawnSpec_TemplateIndirectionExpandsModel(t *testing.T) {
	s, _ := newTestServer(t)
	in := spawnIn{
		ChildHandle: "w",
		Kind:        "coder.v1",
		SpawnSpec:   "template: agents.coder\n",
	}
	rendered, err := s.renderSpawnSpec(context.Background(), defaultTeamID, in, "@p")
	if err != nil {
		t.Fatalf("renderSpawnSpec: %v", err)
	}
	// Model must be substituted from the template's backend.model.
	if !strings.Contains(rendered, "--model claude-opus-4-7") {
		t.Errorf("rendered cmd missing --model claude-opus-4-7:\n%s", rendered)
	}
	// And the broken adjacency (`--model --print`) must not appear.
	if strings.Contains(rendered, "--model --print") {
		t.Errorf("rendered cmd has broken `--model --print` adjacency (empty model var):\n%s", rendered)
	}
	// Default permission mode ("" → "skip") must also resolve so the
	// worker can actually write files — same bug class.
	if !strings.Contains(rendered, "--dangerously-skip-permissions") {
		t.Errorf("rendered cmd missing --dangerously-skip-permissions (empty permission_flag):\n%s", rendered)
	}
}

// TestBuildSpawnVars_BindsProjectID locks the v1.0.625 binding fix.
// {{project_id}} is referenced by steward.research.v1.md's spawn_spec
// examples (`project_id: {{project_id}}`). Pre-v1.0.625 the var was
// unbound → expanded to "" → steward saw malformed `project_id: ` in
// its persona and propagated empty values into worker spawns.
func TestBuildSpawnVars_BindsProjectID(t *testing.T) {
	s, _ := newTestServer(t)
	in := spawnIn{
		ChildHandle: "w",
		Kind:        "coder.v1",
		SpawnSpec:   "backend:\n  model: claude-opus-4-7\n",
		ProjectID:   "01KPROJECT12345",
	}
	vars, err := s.buildSpawnVars(context.Background(), defaultTeamID, in, "@p")
	if err != nil {
		t.Fatalf("buildSpawnVars: %v", err)
	}
	if vars["project_id"] != "01KPROJECT12345" {
		t.Errorf("project_id = %q, want 01KPROJECT12345", vars["project_id"])
	}
	// Unscoped spawn → empty is acceptable; just confirm the key is
	// always present so expandVars never leaves the placeholder behind.
	in.ProjectID = ""
	vars, _ = s.buildSpawnVars(context.Background(), defaultTeamID, in, "@p")
	if _, ok := vars["project_id"]; !ok {
		t.Errorf("project_id key missing for unscoped spawn (would leave {{project_id}} unsubstituted? — no: empty key still expands to empty)")
	}
	if vars["project_id"] != "" {
		t.Errorf("project_id = %q, want empty for unscoped spawn", vars["project_id"])
	}
}

// TestBuildSpawnVars_BindsParentHandleDotted locks the v1.0.625 fix
// for the {{parent.handle}} → "" silent-empty bug. Four bundled
// worker prompts (coder.v1.md, critic.v1.md, lit-reviewer.v1.md,
// paper-writer.v1.md) reference the dotted form. The bound key was
// `parent_handle` (underscore) only, so every "@{{parent.handle}}"
// in those prompts rendered to "@" — the worker's persona never
// learned its steward's handle.
func TestBuildSpawnVars_BindsParentHandleDotted(t *testing.T) {
	s, _ := newTestServer(t)
	parentID := "01KPARENT01"
	_, err := s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at) `+
			`VALUES (?, ?, ?, 'steward.v1', 'running', ?)`,
		parentID, defaultTeamID, "research-steward", "2026-05-17T00:00:00Z")
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	in := spawnIn{
		ChildHandle: "w",
		Kind:        "coder.v1",
		SpawnSpec:   "backend:\n  model: claude-opus-4-7\n",
		ParentID:    parentID,
	}
	vars, err := s.buildSpawnVars(context.Background(), defaultTeamID, in, "@p")
	if err != nil {
		t.Fatalf("buildSpawnVars: %v", err)
	}
	if vars["parent.handle"] != "research-steward" {
		t.Errorf("parent.handle = %q, want research-steward", vars["parent.handle"])
	}
	if vars["parent_handle"] != "research-steward" {
		t.Errorf("parent_handle = %q, want research-steward (back-compat)", vars["parent_handle"])
	}
}

// sanity: the server helper above should not leak a closed DB.
var _ = sql.ErrNoRows

package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// sanity: the server helper above should not leak a closed DB.
var _ = sql.ErrNoRows

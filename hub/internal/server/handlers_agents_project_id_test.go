package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// ADR-025 W1 round-trip + filter tests for agents.project_id.
//
// W1 lands only the read path: schema column + scan + agentOut field +
// optional ?project_id= filter. INSERT here goes through raw SQL because
// the spawn-side YAML parsing that populates project_id lives in W2.

func agentsProjectIDSetup(t *testing.T) (s *Server, token, team string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	tok, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	srv, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	const testTeam = "agents-project-test"
	now := NowUTC()
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		testTeam, testTeam, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	return srv, tok, testTeam
}

func seedProject(t *testing.T, s *Server, team string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, ?, ?, 'goal')`,
		id, team, "proj-"+id, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func seedAgent(t *testing.T, s *Server, team, handle, projectID string) string {
	t.Helper()
	id := NewID()
	if projectID == "" {
		if _, err := s.db.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			VALUES (?, ?, ?, 'claude-code', 'running', ?)`,
			id, team, handle, NowUTC()); err != nil {
			t.Fatalf("seed agent %q: %v", handle, err)
		}
		return id
	}
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status,
		                   project_id, created_at)
		VALUES (?, ?, ?, 'claude-code', 'running', ?, ?)`,
		id, team, handle, projectID, NowUTC()); err != nil {
		t.Fatalf("seed agent %q: %v", handle, err)
	}
	return id
}

func listAgents(t *testing.T, s *Server, token, team, query string) []agentOut {
	t.Helper()
	url := "/v1/teams/" + team + "/agents"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, rr.Code, rr.Body.String())
	}
	var out []agentOut
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestAgentsProjectID_RoundTrip(t *testing.T) {
	srv, tok, team := agentsProjectIDSetup(t)
	proj := seedProject(t, srv, team)
	agentID := seedAgent(t, srv, team, "worker-1", proj)

	out := listAgents(t, srv, tok, team, "")
	if len(out) != 1 {
		t.Fatalf("list: got %d agents, want 1", len(out))
	}
	if out[0].ID != agentID {
		t.Errorf("id=%q want %q", out[0].ID, agentID)
	}
	if out[0].ProjectID != proj {
		t.Errorf("project_id=%q want %q", out[0].ProjectID, proj)
	}

	// GET single agent should also round-trip the column.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+team+"/agents/"+agentID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET agent: %d %s", rr.Code, rr.Body.String())
	}
	var one agentOut
	if err := json.Unmarshal(rr.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if one.ProjectID != proj {
		t.Errorf("single GET project_id=%q want %q", one.ProjectID, proj)
	}
}

func TestAgentsProjectID_NullStaysEmpty(t *testing.T) {
	srv, tok, team := agentsProjectIDSetup(t)
	_ = seedAgent(t, srv, team, "legacy", "")

	out := listAgents(t, srv, tok, team, "")
	if len(out) != 1 {
		t.Fatalf("list: got %d agents, want 1", len(out))
	}
	if out[0].ProjectID != "" {
		t.Errorf("legacy project_id=%q want empty", out[0].ProjectID)
	}
	// omitempty should drop it from the JSON entirely.
	raw, _ := json.Marshal(out[0])
	if got := string(raw); contains(got, `"project_id"`) {
		t.Errorf("legacy agentOut leaked empty project_id field: %s", got)
	}
}

func TestAgentsProjectID_Filter(t *testing.T) {
	srv, tok, team := agentsProjectIDSetup(t)
	projA := seedProject(t, srv, team)
	projB := seedProject(t, srv, team)
	aWorker := seedAgent(t, srv, team, "worker-A", projA)
	bWorker := seedAgent(t, srv, team, "worker-B", projB)
	_ = seedAgent(t, srv, team, "legacy", "")

	// No filter — all three rows.
	all := listAgents(t, srv, tok, team, "")
	if len(all) != 3 {
		t.Fatalf("unfiltered: got %d, want 3", len(all))
	}

	// Filter to projA — only aWorker.
	onlyA := listAgents(t, srv, tok, team, "project_id="+projA)
	if len(onlyA) != 1 || onlyA[0].ID != aWorker {
		t.Fatalf("filter A: got %d rows (%+v), want 1 matching %s",
			len(onlyA), onlyA, aWorker)
	}
	if onlyA[0].ProjectID != projA {
		t.Errorf("filter A: project_id=%q want %q", onlyA[0].ProjectID, projA)
	}

	// Filter to projB — only bWorker.
	onlyB := listAgents(t, srv, tok, team, "project_id="+projB)
	if len(onlyB) != 1 || onlyB[0].ID != bWorker {
		t.Fatalf("filter B: got %d rows (%+v), want 1 matching %s",
			len(onlyB), onlyB, bWorker)
	}
}

func TestAgentsProjectID_ProjectDeleteSetsNull(t *testing.T) {
	srv, tok, team := agentsProjectIDSetup(t)
	proj := seedProject(t, srv, team)
	agentID := seedAgent(t, srv, team, "worker-orphan", proj)

	// Delete the project; ON DELETE SET NULL keeps the agent row but
	// dissolves the project link.
	if _, err := srv.db.Exec(
		`DELETE FROM projects WHERE id = ?`, proj); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	out := listAgents(t, srv, tok, team, "")
	if len(out) != 1 || out[0].ID != agentID {
		t.Fatalf("post-delete list: got %d (%+v), want 1 matching %s",
			len(out), out, agentID)
	}
	if out[0].ProjectID != "" {
		t.Errorf("post-delete project_id=%q want empty (ON DELETE SET NULL)",
			out[0].ProjectID)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

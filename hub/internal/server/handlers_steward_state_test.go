package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// stewardStateSetup spins an isolated hub backed by a tempdir, creates
// a team, a project, and a steward agent linked to that project. Each
// caller can mutate the agent / sessions / spawn / attention / event
// rows to drive a particular state. Returns the server, owner token,
// team, project id, and steward agent id.
func stewardStateSetup(t *testing.T) (
	s *Server, token, team, project, agent string,
) {
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

	const testTeam = "steward-test"
	now := NowUTC()
	if _, err := srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		testTeam, testTeam, now); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	stewardAgent := NewID()
	if _, err := srv.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?, ?, 'steward', 'claude-code', 'running', ?)`,
		stewardAgent, testTeam, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	projectID := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind, steward_agent_id)
		VALUES (?, ?, 'demo', ?, 'goal', ?)`,
		projectID, testTeam, now, stewardAgent); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return srv, tok, testTeam, projectID, stewardAgent
}

func getStewardState(
	t *testing.T, s *Server, token, team, project string,
) stewardStateOut {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+team+"/projects/"+project+"/steward/state", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET steward state: %d %s", rr.Code, rr.Body.String())
	}
	if cc := rr.Result().Header.Get("Cache-Control"); cc != "private, no-cache" {
		t.Errorf("Cache-Control=%q want 'private, no-cache'", cc)
	}
	var out stewardStateOut
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestStewardState_NotSpawnedWhenNoStewardAgent(t *testing.T) {
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

	const testTeam = "no-steward"
	now := NowUTC()
	_, _ = srv.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		testTeam, testTeam, now)
	projectID := NewID()
	_, _ = srv.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, 'demo', ?, 'goal')`, projectID, testTeam, now)

	got := getStewardState(t, srv, tok, testTeam, projectID)
	if got.State != "not-spawned" {
		t.Errorf("state=%q want=not-spawned", got.State)
	}
	if got.AgentID != "" {
		t.Errorf("agent_id=%q want empty", got.AgentID)
	}
}

func TestStewardState_IdleWhenAgentRunningButNothingActive(t *testing.T) {
	srv, tok, team, project, _ := stewardStateSetup(t)
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "idle" {
		t.Errorf("state=%q want=idle", got.State)
	}
}

func TestStewardState_ErrorWhenAgentPaused(t *testing.T) {
	srv, tok, team, project, agent := stewardStateSetup(t)
	if _, err := srv.db.Exec(
		`UPDATE agents SET status='paused' WHERE id = ?`, agent); err != nil {
		t.Fatalf("update agent: %v", err)
	}
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "error" {
		t.Errorf("state=%q want=error", got.State)
	}
}

func TestStewardState_AwaitingDirectorWhenOpenAttention(t *testing.T) {
	srv, tok, team, project, _ := stewardStateSetup(t)
	if _, err := srv.db.Exec(`
		INSERT INTO attention_items
			(id, project_id, scope_kind, scope_id, kind, summary,
			 severity, status, created_at)
		VALUES (?, ?, 'project', ?, 'decision', 'review',
			'minor', 'open', ?)`,
		NewID(), project, project, NowUTC()); err != nil {
		t.Fatalf("seed attention: %v", err)
	}
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "awaiting-director" {
		t.Errorf("state=%q want=awaiting-director", got.State)
	}
}

func TestStewardState_ActiveSessionWinsWhenNoAttention(t *testing.T) {
	srv, tok, team, project, agent := stewardStateSetup(t)
	if _, err := srv.db.Exec(`
		INSERT INTO sessions
			(id, team_id, scope_kind, scope_id, current_agent_id,
			 status, opened_at, last_active_at)
		VALUES (?, ?, 'project', ?, ?, 'open', ?, ?)`,
		NewID(), team, project, agent, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "active-session" {
		t.Errorf("state=%q want=active-session", got.State)
	}
}

func TestStewardState_WorkerDispatchedWhenChildAgentRunning(t *testing.T) {
	srv, tok, team, project, agent := stewardStateSetup(t)
	// Create the worker agent + the spawn row binding it to the steward.
	worker := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status,
		 parent_agent_id, created_at)
		VALUES (?, ?, 'worker-1', 'claude-code', 'running', ?, ?)`,
		worker, team, agent, NowUTC()); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	if _, err := srv.db.Exec(`
		INSERT INTO agent_spawns
			(id, parent_agent_id, child_agent_id, spawn_spec_yaml, spawned_at)
		VALUES (?, ?, ?, '', ?)`,
		NewID(), agent, worker, NowUTC()); err != nil {
		t.Fatalf("seed spawn: %v", err)
	}
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "worker-dispatched" {
		t.Errorf("state=%q want=worker-dispatched", got.State)
	}
}

func TestStewardState_WorkingWhenRecentEvent(t *testing.T) {
	srv, tok, team, project, agent := stewardStateSetup(t)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := srv.db.Exec(`
		INSERT INTO agent_events
			(id, agent_id, seq, ts, kind, producer, payload_json)
		VALUES (?, ?, 1, ?, 'tool.use', 'agent', '{}')`,
		NewID(), agent, now); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "working" {
		t.Errorf("state=%q want=working", got.State)
	}
	if got.CurrentAction == nil || got.CurrentAction.Kind != "tool.use" {
		t.Errorf("current_action=%+v want kind=tool.use", got.CurrentAction)
	}
}

func TestStewardState_HandoffWhenA2AInvokedRecently(t *testing.T) {
	srv, tok, team, project, agent := stewardStateSetup(t)
	now := time.Now().UTC().Format(time.RFC3339)
	payload := `{"to_agent_id":"agent-general","purpose":"team-info"}`
	if _, err := srv.db.Exec(`
		INSERT INTO agent_events
			(id, agent_id, seq, ts, kind, producer, payload_json)
		VALUES (?, ?, 1, ?, 'a2a.invoke', 'agent', ?)`,
		NewID(), agent, now, payload); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	got := getStewardState(t, srv, tok, team, project)
	if got.State != "handoff_in_progress" {
		t.Errorf("state=%q want=handoff_in_progress", got.State)
	}
	if got.Handoff == nil || got.Handoff.ToAgentID != "agent-general" {
		t.Errorf("handoff=%+v want to_agent_id=agent-general", got.Handoff)
	}
	if got.Handoff.Purpose != "team-info" {
		t.Errorf("purpose=%q want=team-info", got.Handoff.Purpose)
	}
}

func TestStewardState_RequiresAuth(t *testing.T) {
	srv, _, team, project, _ := stewardStateSetup(t)
	req := httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+team+"/projects/"+project+"/steward/state", nil)
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want=401", rr.Code)
	}
}

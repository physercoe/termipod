package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// ADR-046 / WS4 — explicit project Start spawns the bound domain steward.
// These drive the REST surface end-to-end (create bound → Start → spawned)
// using the shared project-template harness + a seeded host.

// seedHostForTeam inserts a connected host advertising claude-code so DoSpawn
// resolves a driving mode for the steward template. Mirrors seedHostCaps but
// targets an arbitrary team (the harness team is not defaultTeamID).
func seedHostForTeam(t *testing.T, s *Server, team string) string {
	t.Helper()
	hostID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, ?, 'connected', ?, ?)`,
		hostID, team, "start-host-"+hostID[:6],
		`{"agents":{"claude-code":{"installed":true,"supports":["M1","M2","M4"]}}}`,
		NowUTC()); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return hostID
}

// createBoundProject creates a project bound to the code-migration steward but
// (per ADR-046) not started.
func createBoundProject(t *testing.T, srv *Server, team, tok, name string) projectOut {
	t.Helper()
	return createProject(t, srv, team, tok, map[string]any{
		"name": name, "kind": "goal", "goal": "g",
		"on_create_template_id": "agents.steward.code-migration",
	})
}

func TestStartProject_NoBoundSteward_422(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	// A project with no bound steward cannot be started.
	p := createProject(t, srv, team, tok, map[string]any{"name": "unbound", "kind": "goal"})
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/start", map[string]any{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("start unbound=%d want 422: %s", rr.Code, rr.Body.String())
	}
}

func TestStartProject_NotFound_404(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/nope/start", map[string]any{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("start missing=%d want 404", rr.Code)
	}
}

func TestStartProject_SpawnsBoundSteward(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	seedHostForTeam(t, srv, team)
	p := createBoundProject(t, srv, team, tok, "bound-start")

	// Before Start: bound but not started.
	rr := rcDo(t, srv, tok, http.MethodGet, "/v1/teams/"+team+"/projects/"+p.ID, nil)
	var before projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &before)
	if before.StewardStarted {
		t.Fatal("steward_started=true before Start; want false")
	}

	rr = rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/start", map[string]any{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("start=%d want 201: %s", rr.Code, rr.Body.String())
	}
	var out startProjectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.AgentID == "" || out.AlreadyRan {
		t.Fatalf("start out=%+v; want fresh agent id", out)
	}

	// The spawned steward carries a steward.* kind so the project lookup
	// finds it.
	var kind string
	if err := srv.db.QueryRow(`SELECT kind FROM agents WHERE id = ?`, out.AgentID).Scan(&kind); err != nil {
		t.Fatalf("read agent kind: %v", err)
	}
	if kind != "steward.code-migration" {
		t.Errorf("agent kind = %q; want steward.code-migration", kind)
	}

	// After Start: read reports the steward as started + audit row exists.
	rr = rcDo(t, srv, tok, http.MethodGet, "/v1/teams/"+team+"/projects/"+p.ID, nil)
	var after projectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &after)
	if !after.StewardStarted {
		t.Error("steward_started=false after Start; want true")
	}
	var n int
	_ = srv.db.QueryRow(`
		SELECT COUNT(*) FROM audit_events
		 WHERE action = 'project.started' AND target_id = ?`, p.ID).Scan(&n)
	if n == 0 {
		t.Error("no project.started audit row")
	}
}

func TestStartProject_AlreadyRunning_409(t *testing.T) {
	srv, _, team, tok := newProjectTemplateTestServer(t)
	seedHostForTeam(t, srv, team)
	p := createBoundProject(t, srv, team, tok, "double-start")

	rr := rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/start", map[string]any{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first start=%d want 201: %s", rr.Code, rr.Body.String())
	}
	var first startProjectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &first)

	// Second Start while the steward is live → 409 with the live agent id.
	rr = rcDo(t, srv, tok, http.MethodPost,
		"/v1/teams/"+team+"/projects/"+p.ID+"/start", map[string]any{})
	if rr.Code != http.StatusConflict {
		t.Fatalf("second start=%d want 409: %s", rr.Code, rr.Body.String())
	}
	var second startProjectOut
	_ = json.Unmarshal(rr.Body.Bytes(), &second)
	if !second.AlreadyRan || second.AgentID != first.AgentID {
		t.Errorf("second start out=%+v; want already_running for %s", second, first.AgentID)
	}
}

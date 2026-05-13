package server

import (
	"context"
	"database/sql"
	"testing"
)

// ADR-025 W2: spawn-side project_id round-trip.
//
// Verifies that DoSpawn parses `project_id:` from the rendered spawn
// YAML (canonical site) and falls back to spawnIn.ProjectID (body
// field) when the YAML omits it. The persisted row is what the W1
// read path (handlers_agents_project_id_test.go) surfaces.

func seedProjectInTeam(t *testing.T, s *Server, name string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, ?, ?, 'goal')`,
		id, defaultTeamID, name, NowUTC()); err != nil {
		t.Fatalf("seed project %q: %v", name, err)
	}
	return id
}

func queryAgentProjectID(t *testing.T, s *Server, agentID string) (string, bool) {
	t.Helper()
	var pid sql.NullString
	if err := s.db.QueryRow(
		`SELECT project_id FROM agents WHERE id = ?`, agentID,
	).Scan(&pid); err != nil {
		t.Fatalf("query project_id: %v", err)
	}
	return pid.String, pid.Valid
}

func TestDoSpawn_ProjectID_FromYAML(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-yaml")

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "yaml-worker",
		Kind:        "claude-code",
		SpawnSpec:   "project_id: " + proj + "\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	got, valid := queryAgentProjectID(t, s, out.AgentID)
	if !valid || got != proj {
		t.Fatalf("persisted project_id = %q valid=%v; want %q", got, valid, proj)
	}
}

func TestDoSpawn_ProjectID_FromBodyFallback(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-body")

	// SpawnSpec is just a stub with no project_id key — the body
	// field should propagate as the fallback.
	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "body-worker",
		Kind:        "claude-code",
		ProjectID:   proj,
		SpawnSpec:   "handle: body-worker\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	got, valid := queryAgentProjectID(t, s, out.AgentID)
	if !valid || got != proj {
		t.Fatalf("persisted project_id = %q valid=%v; want %q", got, valid, proj)
	}
}

func TestDoSpawn_ProjectID_YAMLBeatsBody(t *testing.T) {
	s, _ := newTestServer(t)
	projBody := seedProjectInTeam(t, s, "proj-body-loser")
	projYaml := seedProjectInTeam(t, s, "proj-yaml-winner")

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "conflict-worker",
		Kind:        "claude-code",
		ProjectID:   projBody,
		SpawnSpec:   "project_id: " + projYaml + "\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	got, valid := queryAgentProjectID(t, s, out.AgentID)
	if !valid || got != projYaml {
		t.Fatalf("persisted project_id = %q valid=%v; want %q (YAML wins)",
			got, valid, projYaml)
	}
}

func TestDoSpawn_ProjectID_NoneStaysNull(t *testing.T) {
	s, _ := newTestServer(t)

	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "unbound-worker",
		Kind:        "claude-code",
		SpawnSpec:   "handle: unbound-worker\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	got, valid := queryAgentProjectID(t, s, out.AgentID)
	if valid {
		t.Fatalf("persisted project_id = %q valid=true; want NULL", got)
	}
}

// ADR-025 W8 (D5): project-scoped spawns always materialize a
// `scope_kind='project'` session pointing at the new agent, even when
// AutoOpenSession is false. Without this workers couldn't be
// debugged through the standard session viewer.
func TestDoSpawn_ProjectID_AutoOpensProjectSession(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-autosess")

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "auto-session-worker",
		Kind:        "claude-code",
		SpawnSpec:   "project_id: " + proj + "\n",
		// Explicitly false; the project_id binding must override.
		AutoOpenSession: false,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	var (
		sessionScopeKind, sessionScopeID, currentAgentID string
		gotProjectID                                     sql.NullString
	)
	row := s.db.QueryRow(`
		SELECT scope_kind, COALESCE(scope_id, ''), current_agent_id
		  FROM sessions
		 WHERE current_agent_id = ?
		 ORDER BY opened_at DESC LIMIT 1`, out.AgentID)
	if err := row.Scan(&sessionScopeKind, &sessionScopeID, &currentAgentID); err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	_ = s.db.QueryRow(`SELECT project_id FROM agents WHERE id = ?`,
		out.AgentID).Scan(&gotProjectID)
	if sessionScopeKind != "project" {
		t.Errorf("scope_kind=%q; want project", sessionScopeKind)
	}
	if sessionScopeID != proj {
		t.Errorf("scope_id=%q; want %q", sessionScopeID, proj)
	}
	if currentAgentID != out.AgentID {
		t.Errorf("current_agent_id=%q; want %q",
			currentAgentID, out.AgentID)
	}
}

// The auto-open behavior must NOT fire on the session-swap branch:
// a swap rewrites an existing session in-tx, opening a fresh one
// would leave two parallel sessions for the same worker (the old one
// pre-swap + the swap's auto-open).
func TestDoSpawn_ProjectID_NoAutoOpenOnSwap(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-noswap")

	// Seed a pre-existing session pointing at no live agent — the
	// swap path will rewrite this one.
	priorSessionID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO sessions (id, team_id, scope_kind, scope_id,
		 status, opened_at, last_active_at)
		VALUES (?, ?, 'project', ?, 'active', ?, ?)`,
		priorSessionID, defaultTeamID, proj, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	out, _, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "swap-worker",
		Kind:        "claude-code",
		SpawnSpec:   "project_id: " + proj + "\n",
		SessionID:   priorSessionID,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE current_agent_id = ?`,
		out.AgentID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("session count for swap worker=%d; want 1 (no auto-open on swap)", count)
	}
}

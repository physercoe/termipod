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

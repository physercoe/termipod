// ADR-025 W9 gate coverage — exercises the agents.spawn project
// binding check against the canonical truths it enforces:
//
//   - general steward kind blocked outright
//   - project-bound spawn requires caller == project's steward
//   - unbound spawn still flows through (pre-ADR path)
//   - project without a steward rejects (D5 invariant)

package server

import (
	"encoding/json"
	"testing"
)

// seedAgentRow inserts an agent with the given kind + handle, returns id.
func seedAgentRow(t *testing.T, s *Server, team, handle, kind string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, ?, ?, 'running', ?)`,
		id, team, handle, kind, NowUTC()); err != nil {
		t.Fatalf("seed agent %q (%q): %v", handle, kind, err)
	}
	return id
}

// bindProjectSteward sets projects.steward_agent_id for an existing
// project + agent. Mirrors what handleEnsureProjectSteward does after
// a successful spawn.
func bindProjectSteward(t *testing.T, s *Server, projectID, agentID string) {
	t.Helper()
	if _, err := s.db.Exec(
		`UPDATE projects SET steward_agent_id = ? WHERE id = ?`,
		agentID, projectID); err != nil {
		t.Fatalf("bind steward: %v", err)
	}
}

func argsWithProject(t *testing.T, projectID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"child_handle":    "ml-worker",
		"kind":            "claude-code",
		"spawn_spec_yaml": "project_id: " + projectID + "\n",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestAuthorizeAgentsSpawn_PrincipalBypass(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")
	// agentID == "" means principal token; gate must always allow.
	if jerr := s.authorizeAgentsSpawn("", argsWithProject(t, proj)); jerr != nil {
		t.Errorf("principal bypass: gate denied: %s", jerr.Message)
	}
}

func TestAuthorizeAgentsSpawn_GeneralStewardBlocked(t *testing.T) {
	s, _ := newTestServer(t)
	general := seedAgentRow(t, s, defaultTeamID, "@steward", generalStewardKind)

	// With project_id (any value).
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")
	if jerr := s.authorizeAgentsSpawn(general, argsWithProject(t, proj)); jerr == nil {
		t.Errorf("general steward + project_id should be denied")
	}
	// Without project_id — still blocked outright per W9.
	raw, _ := json.Marshal(map[string]any{
		"child_handle":    "ml-worker",
		"kind":            "claude-code",
		"spawn_spec_yaml": "",
	})
	if jerr := s.authorizeAgentsSpawn(general, raw); jerr == nil {
		t.Errorf("general steward (no project) should be denied")
	}
}

func TestAuthorizeAgentsSpawn_ProjectStewardAllowed(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeamID(t, s, defaultTeamID, "owned")
	steward := seedAgentRow(t, s, defaultTeamID, "@steward.owned", "steward.v1")
	bindProjectSteward(t, s, proj, steward)

	if jerr := s.authorizeAgentsSpawn(steward, argsWithProject(t, proj)); jerr != nil {
		t.Errorf("project steward should be allowed: %s", jerr.Message)
	}
}

func TestAuthorizeAgentsSpawn_ForeignStewardDenied(t *testing.T) {
	s, _ := newTestServer(t)
	projOwned := seedProjectInTeamID(t, s, defaultTeamID, "owned")
	projOther := seedProjectInTeamID(t, s, defaultTeamID, "other")
	stewardOwned := seedAgentRow(t, s, defaultTeamID, "@steward.owned", "steward.v1")
	stewardOther := seedAgentRow(t, s, defaultTeamID, "@steward.other", "steward.v1")
	bindProjectSteward(t, s, projOwned, stewardOwned)
	bindProjectSteward(t, s, projOther, stewardOther)

	// stewardOther tries to spawn into projOwned — denied.
	if jerr := s.authorizeAgentsSpawn(stewardOther, argsWithProject(t, projOwned)); jerr == nil {
		t.Errorf("foreign steward should be denied")
	}
}

func TestAuthorizeAgentsSpawn_WorkerDenied(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeamID(t, s, defaultTeamID, "owned")
	steward := seedAgentRow(t, s, defaultTeamID, "@steward.owned", "steward.v1")
	bindProjectSteward(t, s, proj, steward)
	worker := seedAgentRow(t, s, defaultTeamID, "worker-1", "claude-code")

	// Worker tries to spawn into the project — denied (worker.id !=
	// project steward.id). The role manifest also denies this at the
	// allow-list layer; the W9 gate is the second backstop.
	if jerr := s.authorizeAgentsSpawn(worker, argsWithProject(t, proj)); jerr == nil {
		t.Errorf("worker should be denied from project-bound spawn")
	}
}

func TestAuthorizeAgentsSpawn_NoProjectIDFallsThrough(t *testing.T) {
	s, _ := newTestServer(t)
	steward := seedAgentRow(t, s, defaultTeamID, "@steward.unbound", "steward.v1")

	raw, _ := json.Marshal(map[string]any{
		"child_handle":    "free-worker",
		"kind":            "claude-code",
		"spawn_spec_yaml": "name: free-worker\n",
	})
	if jerr := s.authorizeAgentsSpawn(steward, raw); jerr != nil {
		t.Errorf("unbound spawn should fall through: %s", jerr.Message)
	}
}

func TestAuthorizeAgentsSpawn_ProjectWithoutStewardRejected(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeamID(t, s, defaultTeamID, "no-steward")
	// Project exists but steward_agent_id is NULL (no W3 ensure yet).
	caller := seedAgentRow(t, s, defaultTeamID, "@candidate", "steward.v1")

	if jerr := s.authorizeAgentsSpawn(caller, argsWithProject(t, proj)); jerr == nil {
		t.Errorf("project without steward should reject")
	}
}

func TestAuthorizeAgentsSpawn_BodyFieldFallback(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeamID(t, s, defaultTeamID, "owned")
	steward := seedAgentRow(t, s, defaultTeamID, "@steward.owned", "steward.v1")
	bindProjectSteward(t, s, proj, steward)

	// Body field project_id (no YAML) — gate must honour it.
	raw, _ := json.Marshal(map[string]any{
		"child_handle":    "ml-worker",
		"kind":            "claude-code",
		"project_id":      proj,
		"spawn_spec_yaml": "name: ml-worker\n",
	})
	if jerr := s.authorizeAgentsSpawn(steward, raw); jerr != nil {
		t.Errorf("body field project_id: %s", jerr.Message)
	}
	// Cross-check the YAML-wins precedence: YAML names a different
	// project (no steward bound) → gate honours YAML, rejects.
	projOther := seedProjectInTeamID(t, s, defaultTeamID, "other-yaml")
	raw, _ = json.Marshal(map[string]any{
		"child_handle":    "ml-worker",
		"kind":            "claude-code",
		"project_id":      proj, // body says "owned"
		"spawn_spec_yaml": "project_id: " + projOther + "\n", // YAML says "other"
	})
	if jerr := s.authorizeAgentsSpawn(steward, raw); jerr == nil {
		t.Errorf("YAML-wins: gate should evaluate against projOther (no steward) and deny")
	}
}

func TestInjectParentAgentID(t *testing.T) {
	// Empty args / empty parent: no-op.
	if _, ok := injectParentAgentID(nil, "agent_x"); ok {
		t.Error("nil args should not inject")
	}
	if _, ok := injectParentAgentID([]byte(`{}`), ""); ok {
		t.Error("empty parent should not inject")
	}
	// Happy path: missing key → injected.
	in := []byte(`{"child_handle":"w","kind":"claude-code","spawn_spec_yaml":"x"}`)
	out, ok := injectParentAgentID(in, "steward_42")
	if !ok {
		t.Fatal("expected inject on missing key")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("rewritten args parse: %v", err)
	}
	if got["parent_agent_id"] != "steward_42" {
		t.Errorf("parent_agent_id = %v, want steward_42", got["parent_agent_id"])
	}
	if got["child_handle"] != "w" {
		t.Error("existing keys should survive")
	}
	// Caller-supplied wins (REST callers passing a different parent).
	in = []byte(`{"parent_agent_id":"caller_pick","child_handle":"w"}`)
	if _, ok := injectParentAgentID(in, "steward_42"); ok {
		t.Error("non-empty parent should NOT be overwritten")
	}
	// Malformed JSON: no-op (let downstream surface the error).
	if _, ok := injectParentAgentID([]byte(`{not json`), "steward_42"); ok {
		t.Error("malformed args should not inject")
	}
}

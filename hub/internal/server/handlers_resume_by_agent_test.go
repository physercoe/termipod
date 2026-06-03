package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// openSessionForAgent opens a session backed by the agent with a
// worktree + spawn spec so it can be stopped and resumed.
func openSessionForAgent(t *testing.T, s *Server, token, agentID string) sessionOut {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "lifecycle",
			"agent_id":        agentID,
			"worktree_path":   "/tmp/wt/lc",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)
	return ses
}

// TestStopThenResumeByAgent: stop (resumable) leaves the session paused;
// resume-by-agent respawns it as a fresh agent.
func TestStopThenResumeByAgent(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")
	ses := openSessionForAgent(t, s, token, oldAgentID)

	// Stop (the resumable verb).
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/stop", nil)
	if status != http.StatusNoContent {
		t.Fatalf("stop: %d %s", status, body)
	}
	var sesStatus string
	_ = s.db.QueryRow(`SELECT status FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus)
	if sesStatus != "paused" {
		t.Fatalf("after stop session status=%q; want paused", sesStatus)
	}

	// Resume keyed by the agent id.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/resume-session", nil)
	if status != http.StatusOK {
		t.Fatalf("resume-session: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	if newID, _ := resp["new_agent_id"].(string); newID == "" || newID == oldAgentID {
		t.Fatalf("want a fresh agent id, got %q (old=%q)", newID, oldAgentID)
	}
}

// TestTerminateArchivesNotResumable: terminate (permanent) archives the
// session, so resume-by-agent finds nothing paused → 409.
func TestTerminateArchivesNotResumable(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")
	ses := openSessionForAgent(t, s, token, agentID)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/terminate", nil)
	if status != http.StatusNoContent {
		t.Fatalf("terminate: %d %s", status, body)
	}
	var sesStatus string
	_ = s.db.QueryRow(`SELECT status FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus)
	if sesStatus != "archived" {
		t.Fatalf("after terminate session status=%q; want archived", sesStatus)
	}

	// Resume must refuse — terminated work is fork-only, not resumable.
	status, _ = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/resume-session", nil)
	if status != http.StatusConflict {
		t.Fatalf("resume after terminate: want 409, got %d", status)
	}
}

// TestResumeByAgent_NoPausedSession returns 409 when the agent has no
// paused session to bring back.
func TestResumeByAgent_NoPausedSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/resume-session", nil)
	if status != http.StatusConflict {
		t.Fatalf("want 409 for no paused session, got %d", status)
	}
}

// TestResumeKeepsProjectBinding: a resumed agent must inherit the dead
// agent's project_id, else the live continuation hides from the project
// Agents tab while the stale terminated row lingers. Regression guard for
// the v1.0.799 fix (resumePausedSession threads project_id into the spawn).
func TestResumeKeepsProjectBinding(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")

	// Bind the agent to a project (no project_id: in the spawn-spec YAML, so
	// the binding has to be carried by resume itself — the failing path).
	projectID := seedProject(t, s, defaultTeamID)
	if _, err := s.db.Exec(
		`UPDATE agents SET project_id = ? WHERE id = ?`, projectID, oldAgentID); err != nil {
		t.Fatalf("seed project_id: %v", err)
	}

	ses := openSessionForAgent(t, s, token, oldAgentID)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/stop", nil)
	if status != http.StatusNoContent {
		t.Fatalf("stop: %d %s", status, body)
	}

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/resume-session", nil)
	if status != http.StatusOK {
		t.Fatalf("resume-session: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newID, _ := resp["new_agent_id"].(string)
	if newID == "" {
		t.Fatalf("no new_agent_id in resume response: %s", body)
	}

	var gotProject string
	if err := s.db.QueryRow(
		`SELECT COALESCE(project_id, '') FROM agents WHERE id = ?`, newID).Scan(&gotProject); err != nil {
		t.Fatalf("read new agent project_id: %v", err)
	}
	if gotProject != projectID {
		t.Fatalf("resumed agent project_id=%q; want %q", gotProject, projectID)
	}
	// Sanity: the session itself stayed bound to the same agent it resumed.
	var curAgent string
	_ = s.db.QueryRow(`SELECT COALESCE(current_agent_id, '') FROM sessions WHERE id = ?`, ses.ID).Scan(&curAgent)
	if curAgent != newID {
		t.Fatalf("session current_agent_id=%q; want resumed %q", curAgent, newID)
	}
}

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

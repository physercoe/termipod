package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestResumeByAgent_RespawnsTerminatedAgentSession exercises the
// steward-facing inverse of agents.terminate: POST
// /agents/{agent}/resume-session finds the paused session the terminate
// left behind and respawns it as a fresh agent.
func TestResumeByAgent_RespawnsTerminatedAgentSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "resume-by-agent",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/rba",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Terminate the agent (the steward's agents.terminate) — this leaves
	// the session paused.
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "terminated"})

	// Resume keyed by the *agent* id (parity with terminate).
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/resume-session", nil)
	if status != http.StatusOK {
		t.Fatalf("resume-session: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)
	if newAgentID == "" || newAgentID == oldAgentID {
		t.Fatalf("want a fresh agent id, got %q (old=%q)", newAgentID, oldAgentID)
	}

	var sesStatus, sesAgentID string
	_ = s.db.QueryRow(
		`SELECT status, COALESCE(current_agent_id, '')
		   FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus, &sesAgentID)
	if sesStatus != "active" || sesAgentID != newAgentID {
		t.Errorf("session after resume: status=%q agent=%q; want active/%s",
			sesStatus, sesAgentID, newAgentID)
	}
}

// TestResumeByAgent_NoPausedSession returns 409 when the agent has no
// paused session to bring back.
func TestResumeByAgent_NoPausedSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")

	// Agent is live with no paused session.
	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/resume-session", nil)
	if status != http.StatusConflict {
		t.Fatalf("want 409 for no paused session, got %d", status)
	}
}

package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// W2-S1: open → list → get → close round-trip. Verifies status
// transitions and audit_events row gets written for both open
// and close.
func TestSessions_OpenListGetClose(t *testing.T) {
	s, token := newA2ATestServer(t)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "test session", "scope_kind": "team"})
	if status != http.StatusCreated {
		t.Fatalf("open: status=%d body=%s", status, body)
	}
	var opened sessionOut
	if err := json.Unmarshal(body, &opened); err != nil {
		t.Fatalf("decode open: %v", err)
	}
	if opened.ID == "" || opened.Status != "open" {
		t.Fatalf("unexpected open response: %+v", opened)
	}
	if opened.OpenedAt == "" || opened.LastActiveAt == "" {
		t.Errorf("expected timestamps, got %+v", opened)
	}

	// list returns the session
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions?status=open", nil)
	if status != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", status, body)
	}
	var listed []sessionOut
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, ses := range listed {
		if ses.ID == opened.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("opened session %q missing from list of %d", opened.ID, len(listed))
	}

	// get returns the session
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/"+opened.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var got sessionOut
	_ = json.Unmarshal(body, &got)
	if got.ID != opened.ID || got.Title != "test session" {
		t.Errorf("get returned %+v", got)
	}

	// close
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+opened.ID+"/close", nil)
	if status != http.StatusNoContent {
		t.Fatalf("close: status=%d body=%s", status, body)
	}

	// closed sessions are absent from status=open list
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions?status=open", nil)
	var afterOpen []sessionOut
	_ = json.Unmarshal(body, &afterOpen)
	for _, ses := range afterOpen {
		if ses.ID == opened.ID {
			t.Errorf("closed session still in status=open list")
		}
	}

	// audit recorded both open and close
	var openCount, closeCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'session.open' AND target_id = ?`,
		opened.ID,
	).Scan(&openCount)
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'session.close' AND target_id = ?`,
		opened.ID,
	).Scan(&closeCount)
	if openCount != 1 || closeCount != 1 {
		t.Errorf("audit rows: open=%d close=%d (want 1/1)", openCount, closeCount)
	}
}

// Two open sessions sharing a worktree_path violate the partial
// unique index. The schema is the load-bearing guard against
// trampling each other's edits, so a regression here would be a
// silent footgun.
func TestSessions_RejectDuplicateActiveWorktree(t *testing.T) {
	s, token := newA2ATestServer(t)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "first", "worktree_path": "/tmp/wt/A"})
	if status != http.StatusCreated {
		t.Fatalf("first open: status=%d body=%s", status, body)
	}
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "second", "worktree_path": "/tmp/wt/A"})
	if status != http.StatusInternalServerError {
		// SQLite UNIQUE constraint surfaces as 500 today; if we add a
		// 409 path later this test becomes its anchor.
		t.Errorf("second open should fail; got status=%d body=%s",
			status, body)
	}
}

// Stamping: an event posted while a session has current_agent_id =
// agent should land with that session_id in agent_events. The
// resume contract requires this — without it transcripts can't be
// queried by session.
func TestSessions_StampsAgentEvents(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "stamp test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Post an agent event via the public route.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "text",
			"producer": "agent",
			"payload":  map[string]any{"text": "hello"},
		})
	if status != http.StatusCreated {
		t.Fatalf("post event: status=%d body=%s", status, body)
	}

	var stamped string
	_ = s.db.QueryRow(
		`SELECT COALESCE(session_id, '') FROM agent_events
		   WHERE agent_id = ? ORDER BY seq DESC LIMIT 1`,
		agentID,
	).Scan(&stamped)
	if stamped != ses.ID {
		t.Errorf("event session_id = %q; want %q", stamped, ses.ID)
	}

	// last_active_at on the session should have moved past opened_at
	// (or at least equal — depends on clock resolution).
	var lastActive string
	_ = s.db.QueryRow(
		`SELECT last_active_at FROM sessions WHERE id = ?`, ses.ID,
	).Scan(&lastActive)
	if lastActive < ses.OpenedAt {
		t.Errorf("touchSession didn't advance last_active_at: %s < %s",
			lastActive, ses.OpenedAt)
	}
}

// Events posted for an agent that has no session leave session_id
// NULL — pre-sessions agents and orphan events still work.
func TestSessions_NoSession_NullSessionID(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "text",
			"producer": "agent",
			"payload":  map[string]any{"text": "no session"},
		})
	if status != http.StatusCreated {
		t.Fatalf("post event: status=%d body=%s", status, body)
	}

	var nullCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_events
		   WHERE agent_id = ? AND session_id IS NULL`, agentID,
	).Scan(&nullCount)
	if nullCount == 0 {
		t.Errorf("expected at least one NULL-session_id event; got %d", nullCount)
	}
}

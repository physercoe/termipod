package server

import (
	"database/sql"
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

// W2-S3: when an agent the session is pointing at goes to status=
// crashed (the host-runner restart pattern from reconcile.go), the
// session must auto-flip to 'interrupted'. terminated does NOT
// trigger this — terminated is the user's explicit teardown.
func TestSessions_InterruptOnAgentCrash(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// Open a session attached to this agent, with worktree + spec
	// captured so resume could later succeed.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "interrupt test",
			"agent_id":        agentID,
			"worktree_path":   "/tmp/wt/interrupt",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Patch the agent to crashed.
	status, body = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID,
		map[string]any{"status": "crashed"})
	if status != http.StatusNoContent {
		t.Fatalf("patch: %d %s", status, body)
	}

	// Session should be interrupted now.
	var sesStatus string
	_ = s.db.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus)
	if sesStatus != "interrupted" {
		t.Errorf("session status = %q; want interrupted", sesStatus)
	}

	// terminated MUST NOT auto-interrupt — that's a different signal.
	// Reset to 'open' to test.
	if _, err := s.db.Exec(
		`UPDATE sessions SET status='open' WHERE id=?`, ses.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID,
		map[string]any{"status": "terminated"})
	_ = s.db.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus)
	if sesStatus == "interrupted" {
		t.Errorf("terminated should NOT auto-interrupt; got %q", sesStatus)
	}
}

// W2-S3: resume on an interrupted session creates a new agent with
// the same handle/kind/host/worktree, points the session at it, and
// flips status back to open. Transcript continuity is implicit:
// agent_events from the dead agent stay queryable by session_id;
// new events from the resumed agent get the same session_id.
func TestSessions_Resume(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")

	// Open a session with the agent + worktree + spawn_spec captured.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "resume test",
			"agent_id":        oldAgentID,
			"worktree_path":   "/tmp/wt/resume",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Crash the agent → session goes interrupted.
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID,
		map[string]any{"status": "crashed"})

	// Now resume.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	newAgentID, _ := resp["new_agent_id"].(string)
	priorAgentID, _ := resp["prior_agent_id"].(string)
	if newAgentID == "" {
		t.Fatalf("resume returned no new_agent_id: %s", body)
	}
	if newAgentID == oldAgentID {
		t.Errorf("resume reused the same agent_id, want a new one")
	}
	if priorAgentID != oldAgentID {
		t.Errorf("prior_agent_id = %q; want %q", priorAgentID, oldAgentID)
	}

	// Session is now back to open and points at the new agent.
	var sesStatus, sesAgentID string
	_ = s.db.QueryRow(
		`SELECT status, COALESCE(current_agent_id, '')
		   FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus, &sesAgentID)
	if sesStatus != "open" {
		t.Errorf("session status after resume = %q; want open", sesStatus)
	}
	if sesAgentID != newAgentID {
		t.Errorf("session current_agent_id = %q; want %q", sesAgentID, newAgentID)
	}

	// New agent inherited handle + kind + host from the dead one.
	var newHandle, newKind, newHost string
	_ = s.db.QueryRow(
		`SELECT handle, kind, COALESCE(host_id, '')
		   FROM agents WHERE id = ?`, newAgentID).Scan(
		&newHandle, &newKind, &newHost)
	var oldHandle, oldKind, oldHost string
	_ = s.db.QueryRow(
		`SELECT handle, kind, COALESCE(host_id, '')
		   FROM agents WHERE id = ?`, oldAgentID).Scan(
		&oldHandle, &oldKind, &oldHost)
	if newHandle != oldHandle || newKind != oldKind || newHost != oldHost {
		t.Errorf("new agent identity drift: handle=%q/%q kind=%q/%q host=%q/%q",
			newHandle, oldHandle, newKind, oldKind, newHost, oldHost)
	}

	// Audit row recorded.
	var auditCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events
		   WHERE action = 'session.resume' AND target_id = ?`, ses.ID,
	).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("session.resume audit count = %d; want 1", auditCount)
	}
}

// Resume on a still-open session → 409. Sessions that haven't been
// interrupted have nothing to recover from.
func TestSessions_ResumeRefusesOpenSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":           "still open",
			"agent_id":        agentID,
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/resume", nil)
	if status != http.StatusConflict {
		t.Errorf("resume on open session: status=%d body=%s; want 409",
			status, body)
	}
}

// Delete refuses an open session, accepts a closed one, clears
// session_id from the transcript, and absents the row from the
// default list. Deleted-twice is idempotent (204).
func TestSessions_Delete(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "delete test", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Stamp an event so we can assert the session_id gets cleared on delete.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind":     "text",
			"producer": "agent",
			"payload":  map[string]any{"text": "hi"},
		})

	// Open session — delete should refuse with 409.
	status, body = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID, nil)
	if status != http.StatusConflict {
		t.Errorf("delete on open: status=%d body=%s; want 409", status, body)
	}

	// Close it.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/close", nil)

	// Delete now succeeds.
	status, body = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", status, body)
	}

	// session_id on the prior agent_event should be NULL.
	var stamped sql.NullString
	_ = s.db.QueryRow(
		`SELECT session_id FROM agent_events
		   WHERE agent_id = ? ORDER BY seq DESC LIMIT 1`,
		agentID).Scan(&stamped)
	if stamped.Valid && stamped.String != "" {
		t.Errorf("event session_id still %q after delete; want NULL",
			stamped.String)
	}

	// Default list omits deleted.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions", nil)
	var listed []sessionOut
	_ = json.Unmarshal(body, &listed)
	for _, s := range listed {
		if s.ID == ses.ID {
			t.Errorf("deleted session %q still in default list", ses.ID)
		}
	}

	// status=deleted query still surfaces it for ops.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions?status=deleted", nil)
	var deletedList []sessionOut
	_ = json.Unmarshal(body, &deletedList)
	found := false
	for _, s := range deletedList {
		if s.ID == ses.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("?status=deleted didn't return the deleted row")
	}

	// Idempotent re-delete returns 204.
	status, _ = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID, nil)
	if status != http.StatusNoContent {
		t.Errorf("re-delete: status=%d; want 204 idempotent", status)
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

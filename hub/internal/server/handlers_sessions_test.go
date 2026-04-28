package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// W2-S1: create → list → get → archive round-trip. Verifies status
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
	if opened.ID == "" || opened.Status != "active" {
		t.Fatalf("unexpected open response: %+v", opened)
	}
	if opened.OpenedAt == "" || opened.LastActiveAt == "" {
		t.Errorf("expected timestamps, got %+v", opened)
	}

	// list returns the session
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions?status=active", nil)
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

	// archive
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+opened.ID+"/archive", nil)
	if status != http.StatusNoContent {
		t.Fatalf("archive: status=%d body=%s", status, body)
	}

	// archived sessions are absent from status=active list
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions?status=active", nil)
	var afterOpen []sessionOut
	_ = json.Unmarshal(body, &afterOpen)
	for _, ses := range afterOpen {
		if ses.ID == opened.ID {
			t.Errorf("archived session still in status=active list")
		}
	}

	// audit recorded both open and archive
	var openCount, closeCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'session.open' AND target_id = ?`,
		opened.ID,
	).Scan(&openCount)
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'session.archive' AND target_id = ?`,
		opened.ID,
	).Scan(&closeCount)
	if openCount != 1 || closeCount != 1 {
		t.Errorf("audit rows: open=%d archive=%d (want 1/1)", openCount, closeCount)
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
// session must auto-flip to 'paused'. terminated does NOT trigger
// this — terminated is the user's explicit teardown.
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

	// Session should be paused now.
	var sesStatus string
	_ = s.db.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus)
	if sesStatus != "paused" {
		t.Errorf("session status = %q; want paused", sesStatus)
	}

	// terminated MUST NOT auto-pause — that's a different signal.
	// Reset to 'active' to test.
	if _, err := s.db.Exec(
		`UPDATE sessions SET status='active' WHERE id=?`, ses.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID,
		map[string]any{"status": "terminated"})
	_ = s.db.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus)
	if sesStatus == "paused" {
		t.Errorf("terminated should NOT auto-pause; got %q", sesStatus)
	}
}

// W2-S3: resume on a paused session creates a new agent with the
// same handle/kind/host/worktree, points the session at it, and
// flips status back to active. Transcript continuity is implicit:
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

	// Crash the agent → session goes paused.
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

	// Session is now back to active and points at the new agent.
	var sesStatus, sesAgentID string
	_ = s.db.QueryRow(
		`SELECT status, COALESCE(current_agent_id, '')
		   FROM sessions WHERE id = ?`, ses.ID).Scan(&sesStatus, &sesAgentID)
	if sesStatus != "active" {
		t.Errorf("session status after resume = %q; want active", sesStatus)
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

// Resume on a still-active session → 409. Sessions that haven't been
// paused have nothing to recover from.
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
		t.Errorf("resume on active session: status=%d body=%s; want 409",
			status, body)
	}
}

// Delete refuses an active session, accepts an archived one, clears
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

	// Active session — delete should refuse with 409.
	status, body = doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID, nil)
	if status != http.StatusConflict {
		t.Errorf("delete on active: status=%d body=%s; want 409", status, body)
	}

	// Archive it.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/archive", nil)

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

// ADR-009 D4: fork creates a new active session pre-loaded from an
// archived source. Same scope, same team, points at the live
// steward (or a caller-provided agent_id). Refuses non-archived
// sources with 409.
func TestSessions_Fork(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")
	// Mark the seeded agent as a running steward so the auto-resolve
	// fallback path can find it.
	if _, err := s.db.Exec(
		`UPDATE agents SET handle='steward', status='running' WHERE id=?`,
		agentID); err != nil {
		t.Fatalf("promote agent to steward: %v", err)
	}

	// Create a source session, archive it.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":      "source",
			"agent_id":   agentID,
			"scope_kind": "project",
			"scope_id":   "proj-abc",
		})
	if status != http.StatusCreated {
		t.Fatalf("open source: %s", body)
	}
	var src sessionOut
	_ = json.Unmarshal(body, &src)

	// Fork on an active source must 409.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/fork", nil)
	if status != http.StatusConflict {
		t.Errorf("fork on active: status=%d body=%s; want 409", status, body)
	}

	// Archive the source.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/archive", nil)

	// Fork now succeeds.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/fork",
		map[string]any{"agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("fork: status=%d body=%s", status, body)
	}
	var fork map[string]any
	_ = json.Unmarshal(body, &fork)
	newID, _ := fork["session_id"].(string)
	if newID == "" || newID == src.ID {
		t.Fatalf("fork returned session_id=%q (source=%q)", newID, src.ID)
	}

	// New session is active, copies scope, points at the same agent,
	// has no worktree (fork is conversational).
	var (
		nStatus, nScopeKind, nScopeID, nAgent, nWorktree string
	)
	_ = s.db.QueryRow(`
		SELECT status, COALESCE(scope_kind, ''), COALESCE(scope_id, ''),
		       COALESCE(current_agent_id, ''), COALESCE(worktree_path, '')
		  FROM sessions WHERE id = ?`, newID).Scan(
		&nStatus, &nScopeKind, &nScopeID, &nAgent, &nWorktree)
	if nStatus != "active" {
		t.Errorf("fork status=%q; want active", nStatus)
	}
	if nScopeKind != "project" || nScopeID != "proj-abc" {
		t.Errorf("fork scope=%q/%q; want project/proj-abc", nScopeKind, nScopeID)
	}
	if nAgent != agentID {
		t.Errorf("fork agent_id=%q; want %q", nAgent, agentID)
	}
	if nWorktree != "" {
		t.Errorf("fork worktree=%q; want empty", nWorktree)
	}

	// Audit row recorded with source pointer.
	var auditCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events
		   WHERE action = 'session.fork' AND target_id = ?`, newID,
	).Scan(&auditCount)
	if auditCount != 1 {
		t.Errorf("session.fork audit count = %d; want 1", auditCount)
	}

	// Source remains archived (fork doesn't mutate the source).
	var srcStatusAfter string
	_ = s.db.QueryRow(
		`SELECT status FROM sessions WHERE id = ?`, src.ID).Scan(&srcStatusAfter)
	if srcStatusAfter != "archived" {
		t.Errorf("source status after fork = %q; want archived", srcStatusAfter)
	}
}

// Fork without agent_id falls back to the team's live steward when
// one exists. With no live steward and no caller-provided agent_id,
// fork returns 409.
func TestSessions_ForkAutoStewardFallback(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "host-x")
	if _, err := s.db.Exec(
		`UPDATE agents SET handle='steward', status='running' WHERE id=?`,
		agentID); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Source session attached to that steward.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "src", "scope_kind": "team"})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var src sessionOut
	_ = json.Unmarshal(body, &src)
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/archive", nil)

	// Fork with no agent_id → server picks the live steward.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+src.ID+"/fork", nil)
	if status != http.StatusCreated {
		t.Fatalf("fork w/o agent_id: status=%d body=%s", status, body)
	}
	var resp map[string]any
	_ = json.Unmarshal(body, &resp)
	gotAgent, _ := resp["agent_id"].(string)
	if gotAgent == "" {
		t.Errorf("fork did not auto-resolve agent_id: %s", body)
	}
}

// W2 follow-up: a spawn carrying session_id is a session-swap. The
// prior agent terminates inside the same tx, the new agent takes
// the session over with the new spawn_spec, and the transcript
// (queried by session_id) carries forward — the user explicitly
// keeps the conversation while switching engine/model.
func TestSessions_SwapAgentInSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")

	// Open a session attached to the old agent.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{
			"title":    "swap test",
			"agent_id": oldAgentID,
		})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Stamp an event so we can verify transcript continuity.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+oldAgentID+"/events",
		map[string]any{
			"kind": "text", "producer": "agent",
			"payload": map[string]any{"text": "before swap"},
		})

	// Read the old handle so we can swap to the same one (typical
	// "switch model" case keeps handle='steward').
	var oldHandle, oldKind string
	_ = s.db.QueryRow(
		`SELECT handle, kind FROM agents WHERE id = ?`, oldAgentID,
	).Scan(&oldHandle, &oldKind)

	// Spawn-with-session_id: carries the new spec, atomically
	// terminates the prior agent, points session at the new one.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    oldHandle,
			"kind":            oldKind,
			"host_id":         "host-x",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude --new\n",
			"session_id":      ses.ID,
		})
	if status != http.StatusCreated {
		t.Fatalf("swap spawn: status=%d body=%s", status, body)
	}
	var spawnRes spawnOut
	_ = json.Unmarshal(body, &spawnRes)
	newAgentID := spawnRes.AgentID
	if newAgentID == "" || newAgentID == oldAgentID {
		t.Fatalf("expected new agent id; got %q (old %q)",
			newAgentID, oldAgentID)
	}

	// Old agent terminated.
	var oldStatus string
	_ = s.db.QueryRow(
		`SELECT status FROM agents WHERE id = ?`, oldAgentID,
	).Scan(&oldStatus)
	if oldStatus != "terminated" {
		t.Errorf("old agent status = %q; want terminated", oldStatus)
	}

	// Session now points at the new agent, status=active, spec updated.
	var sesAgentID, sesStatus, sesSpec string
	_ = s.db.QueryRow(
		`SELECT COALESCE(current_agent_id, ''), status,
		        COALESCE(spawn_spec_yaml, '')
		   FROM sessions WHERE id = ?`, ses.ID,
	).Scan(&sesAgentID, &sesStatus, &sesSpec)
	if sesAgentID != newAgentID || sesStatus != "active" {
		t.Errorf("session after swap: agent=%q status=%q",
			sesAgentID, sesStatus)
	}
	if !strings.Contains(sesSpec, "claude --new") {
		t.Errorf("session.spawn_spec_yaml did not pick up the new spec; got %q", sesSpec)
	}

	// Stamp an event from the new agent. agent_events query by
	// session_id should now span both agents.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+newAgentID+"/events",
		map[string]any{
			"kind": "text", "producer": "agent",
			"payload": map[string]any{"text": "after swap"},
		})

	var transcriptCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_events WHERE session_id = ?`,
		ses.ID,
	).Scan(&transcriptCount)
	if transcriptCount < 2 {
		t.Errorf("transcript by session_id = %d events; want ≥2 spanning swap",
			transcriptCount)
	}
}

// Long-session UX: cold open must return the newest N events, not
// the oldest N. ?tail=true switches the order; ?before=<seq> walks
// backwards into the older history. Without this, listAgentEvents
// silently truncated big transcripts to their oldest 1000 rows.
func TestAgentEvents_TailAndPaginate(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// Stamp 12 events so we can verify a 5-row tail + a 5-row older page.
	for i := 1; i <= 12; i++ {
		doReq(t, s, token, http.MethodPost,
			"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
			map[string]any{
				"kind": "text", "producer": "agent",
				"payload": map[string]any{"text": fmt.Sprintf("e%d", i)},
			})
	}

	// tail=true → newest 5, DESC order (e12..e8).
	_, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events?tail=true&limit=5",
		nil)
	var page []agentEventOut
	_ = json.Unmarshal(body, &page)
	if len(page) != 5 {
		t.Fatalf("tail page len=%d; want 5", len(page))
	}
	var firstText, lastText map[string]any
	_ = json.Unmarshal(page[0].Payload, &firstText)
	_ = json.Unmarshal(page[4].Payload, &lastText)
	if firstText["text"] != "e12" || lastText["text"] != "e8" {
		t.Errorf("tail order = [%v..%v]; want [e12..e8]",
			firstText["text"], lastText["text"])
	}
	minSeq := page[4].Seq

	// before=minSeq → next-older 5, DESC (e7..e3).
	_, body = doReq(t, s, token, http.MethodGet,
		fmt.Sprintf(
			"/v1/teams/%s/agents/%s/events?before=%d&limit=5",
			defaultTeamID, agentID, minSeq),
		nil)
	var older []agentEventOut
	_ = json.Unmarshal(body, &older)
	if len(older) != 5 {
		t.Fatalf("older page len=%d; want 5", len(older))
	}
	_ = json.Unmarshal(older[0].Payload, &firstText)
	_ = json.Unmarshal(older[4].Payload, &lastText)
	if firstText["text"] != "e7" || lastText["text"] != "e3" {
		t.Errorf("older order = [%v..%v]; want [e7..e3]",
			firstText["text"], lastText["text"])
	}

	// since= keeps existing ASC behavior (used by SSE backfill).
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events?since=10&limit=10",
		nil)
	var asc []agentEventOut
	_ = json.Unmarshal(body, &asc)
	if len(asc) != 2 {
		t.Fatalf("since=10 len=%d; want 2 (e11, e12)", len(asc))
	}
}

// auto_open_session=true on a spawn-with-no-SessionID opens a session
// pointing at the new agent inside the same transaction. The
// multi-steward UX invariant ("every live steward has a session")
// depends on this being atomic — without it, a process crash between
// spawn and openSession would leave an agent-without-session orphan.
func TestSpawn_AutoOpenSession(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Find a host to attach the spawn to. seedChannelAndAgent registers
	// one as a side-effect; reuse it.
	_, _ = seedChannelAndAgent(t, s, "", "host-x")

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":      "research-steward",
			"kind":              "claude-code",
			"host_id":           "host-x",
			"spawn_spec_yaml":   "kind: claude-code\nbackend:\n  cmd: claude\n",
			"auto_open_session": true,
		})
	if status != http.StatusCreated {
		t.Fatalf("spawn: status=%d body=%s", status, body)
	}
	var out spawnOut
	_ = json.Unmarshal(body, &out)
	if out.AgentID == "" {
		t.Fatalf("spawn returned no agent_id: %s", body)
	}

	// Exactly one open session exists pointing at the new agent.
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions
		   WHERE current_agent_id = ? AND status = 'active'`,
		out.AgentID,
	).Scan(&n)
	if n != 1 {
		t.Errorf("auto-open session count = %d; want 1", n)
	}
}

// auto_open_session is ignored when SessionID is set (the swap path
// already updates the named session in-tx). This guards against the
// caller accidentally setting both flags and getting a duplicate
// session.
func TestSpawn_AutoOpenSession_IgnoredOnSwap(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, oldAgentID := seedChannelAndAgent(t, s, "", "host-x")

	// Open a session attached to the existing agent.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "swap test", "agent_id": oldAgentID})
	if status != http.StatusCreated {
		t.Fatalf("open session: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	// Swap with auto_open_session set — the swap should win, no extra
	// session row should appear.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":      "steward",
			"kind":              "claude-code",
			"host_id":           "host-x",
			"spawn_spec_yaml":   "kind: claude-code\nbackend:\n  cmd: claude\n",
			"session_id":        ses.ID,
			"auto_open_session": true,
		})
	if status != http.StatusCreated {
		t.Fatalf("swap spawn: status=%d body=%s", status, body)
	}
	var openCount int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE status = 'active'`).Scan(&openCount)
	if openCount != 1 {
		t.Errorf("open session count after swap = %d; want 1 (auto-open should be ignored when SessionID set)",
			openCount)
	}
}

// PATCH /agents/{id} accepts a handle field and enforces the live-
// handle uniqueness constraint. The multi-steward UX needs this so
// the principal can rename `steward` → `research-steward` without a
// respawn. Collisions surface as 409 with a friendly message rather
// than a generic 500.
func TestPatchAgent_RenameHandle(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// Successful rename.
	status, body := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID,
		map[string]any{"handle": "research-steward"})
	if status != http.StatusNoContent {
		t.Fatalf("rename: status=%d body=%s; want 204", status, body)
	}
	var got string
	_ = s.db.QueryRow(
		`SELECT handle FROM agents WHERE id = ?`, agentID,
	).Scan(&got)
	if got != "research-steward" {
		t.Errorf("handle = %q; want research-steward", got)
	}

	// Empty handle → 400.
	status, _ = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID,
		map[string]any{"handle": ""})
	if status != http.StatusBadRequest {
		t.Errorf("empty handle: status=%d; want 400", status)
	}

	// Collision: spawn a second live agent with a distinct handle,
	// try to rename it onto the first's handle → 409.
	_, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "infra-steward",
			"kind":            "claude-code",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
		})
	var spawned spawnOut
	_ = json.Unmarshal(body, &spawned)
	status, body = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+spawned.AgentID,
		map[string]any{"handle": "research-steward"})
	if status != http.StatusConflict {
		t.Errorf("collision: status=%d body=%s; want 409", status, body)
	}
}

// PATCH /sessions/{id} renames the row. Empty title clears it back
// to NULL so the mobile UI shows "(untitled session)" again.
func TestSessions_PatchRename(t *testing.T) {
	s, token := newA2ATestServer(t)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "old name"})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)

	status, body = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID,
		map[string]any{"title": "new name"})
	if status != http.StatusNoContent {
		t.Fatalf("rename: status=%d body=%s", status, body)
	}
	var got string
	_ = s.db.QueryRow(
		`SELECT COALESCE(title, '') FROM sessions WHERE id = ?`, ses.ID,
	).Scan(&got)
	if got != "new name" {
		t.Errorf("title = %q; want new name", got)
	}

	// Empty title clears it.
	doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID,
		map[string]any{"title": ""})
	var nullable sql.NullString
	_ = s.db.QueryRow(
		`SELECT title FROM sessions WHERE id = ?`, ses.ID,
	).Scan(&nullable)
	if nullable.Valid {
		t.Errorf("title after clear = %q; want NULL", nullable.String)
	}

	// audit row recorded.
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'session.rename' AND target_id = ?`,
		ses.ID,
	).Scan(&n)
	if n != 2 {
		t.Errorf("audit rename rows = %d; want 2 (set + clear)", n)
	}
}

// New-session UX bug fix: when the user closes a session and opens a
// fresh one on the same agent, the AgentFeed must show only the new
// session's events. listAgentEvents?session=<id> is what makes that
// possible — without the filter the prior archived session's transcript
// replays into the "fresh" chat.
func TestAgentEvents_FilterBySession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// First session — stamp two events, close it.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "old", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open A: %s", body)
	}
	var sesA sessionOut
	_ = json.Unmarshal(body, &sesA)
	for _, txt := range []string{"old-1", "old-2"} {
		doReq(t, s, token, http.MethodPost,
			"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
			map[string]any{
				"kind": "text", "producer": "agent",
				"payload": map[string]any{"text": txt},
			})
	}
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesA.ID+"/archive", nil)

	// Second session on the same agent — stamp one event.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "fresh", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open B: %s", body)
	}
	var sesB sessionOut
	_ = json.Unmarshal(body, &sesB)
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events",
		map[string]any{
			"kind": "text", "producer": "agent",
			"payload": map[string]any{"text": "fresh-1"},
		})

	// Unfiltered list returns all 3 — back-compat.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events", nil)
	var all []agentEventOut
	_ = json.Unmarshal(body, &all)
	if len(all) != 3 {
		t.Errorf("unfiltered events = %d; want 3", len(all))
	}

	// Filter by sesB returns just fresh-1.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/events?session="+sesB.ID,
		nil)
	var scoped []agentEventOut
	_ = json.Unmarshal(body, &scoped)
	if len(scoped) != 1 {
		t.Fatalf("session-filtered events = %d; want 1", len(scoped))
	}
	var p map[string]any
	_ = json.Unmarshal(scoped[0].Payload, &p)
	if got, _ := p["text"].(string); got != "fresh-1" {
		t.Errorf("filtered event text = %q; want fresh-1", got)
	}
}

// Spawn with session_id pointing at a deleted session → 409.
func TestSessions_SwapRefusesDeletedSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "", "")

	// Open + close + delete to reach status=deleted.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions",
		map[string]any{"title": "to be deleted", "agent_id": agentID})
	if status != http.StatusCreated {
		t.Fatalf("open: %s", body)
	}
	var ses sessionOut
	_ = json.Unmarshal(body, &ses)
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID+"/archive", nil)
	doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/sessions/"+ses.ID, nil)

	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/agents/spawn",
		map[string]any{
			"child_handle":    "steward",
			"kind":            "claude-code",
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: claude\n",
			"session_id":      ses.ID,
		})
	if status != http.StatusConflict {
		t.Errorf("swap into deleted session: status=%d body=%s; want 409",
			status, body)
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

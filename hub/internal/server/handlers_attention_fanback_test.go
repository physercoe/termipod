package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-030 W11 — propose fan-back via dispatchAttentionReply.
//
// The W4 propose handler already stamps `session_id` on the attention
// row (lookupAgentSession against the calling agent). When /decide
// resolves the row (approve | reject), the W11 allowlist extension
// fires dispatchAttentionReply, which writes an `input.attention_reply`
// agent_event into the requester's session. The event payload carries
// the propose-specific fields (change_kind, decision, executed, reason)
// PLUS a nested ADR-032 envelope ({from, to, kind:report, text, cause,
// thread:{transport:attention, id:<att_id>}}).

// seedSessionForAgent inserts a sessions row pointing at agentID so
// lookupAgentSession (called inside mcpPropose) populates the
// attention row's session_id field.
func seedSessionForAgent(t *testing.T, s *Server, agentID string) string {
	t.Helper()
	id := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO sessions
			(id, team_id, title, scope_kind, current_agent_id, status,
			 opened_at, last_active_at)
		VALUES (?, ?, 'test', 'team', ?, 'active', ?, ?)`,
		id, defaultTeamID, agentID, now, now); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

// readLatestAttentionReply pulls the most recent input.attention_reply
// event for agentID and parses its payload.
func readLatestAttentionReply(t *testing.T, s *Server, agentID string) map[string]any {
	t.Helper()
	var payload string
	if err := s.eventsDB.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.attention_reply'
		 ORDER BY seq DESC LIMIT 1`, agentID).Scan(&payload); err != nil {
		t.Fatalf("read attention_reply event: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return p
}

// 1. Approve fans back with executed + envelope.
func TestFanBack_ProposeApprove_PayloadShape(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "test-task", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	_ = seedSessionForAgent(t, s, agentID)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done", "result_summary": "shipped"},
		"reason":      "done",
	})
	out, _ := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	attID := unwrapMcpResult(t, out)["request_id"].(string)

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal", "reason": "lgtm"})
	if status != 200 {
		t.Fatalf("decide: %d", status)
	}

	p := readLatestAttentionReply(t, s, agentID)
	if p["request_id"] != attID {
		t.Errorf("request_id = %v; want %s", p["request_id"], attID)
	}
	if p["kind"] != "propose" {
		t.Errorf("kind = %v; want propose (attention kind)", p["kind"])
	}
	if p["change_kind"] != "task.set_status" {
		t.Errorf("change_kind = %v; want task.set_status", p["change_kind"])
	}
	if p["decision"] != "approve" {
		t.Errorf("decision = %v; want approve", p["decision"])
	}
	if p["reason"] != "lgtm" {
		t.Errorf("reason = %v; want lgtm", p["reason"])
	}
	if p["executed"] == nil {
		t.Error("executed missing from approve fan-back")
	}
	env, ok := p["envelope"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing or wrong type: %v", p["envelope"])
	}
	if env["kind"] != KindReport {
		t.Errorf("envelope.kind = %v; want %q (closes the propose loop)", env["kind"], KindReport)
	}
	from := env["from"].(map[string]any)
	if from["handle"] != "@principal" {
		t.Errorf("envelope.from.handle = %v; want @principal", from["handle"])
	}
	to := env["to"].(map[string]any)
	if to["agent_id"] != agentID {
		t.Errorf("envelope.to.agent_id = %v; want %s", to["agent_id"], agentID)
	}
	thread := env["thread"].(map[string]any)
	if thread["transport"] != TransportAttention {
		t.Errorf("envelope.thread.transport = %v; want %q", thread["transport"], TransportAttention)
	}
	if thread["id"] != attID {
		t.Errorf("envelope.thread.id = %v; want %s", thread["id"], attID)
	}
	// Text field is human-readable summary.
	if text, _ := env["text"].(string); !strings.Contains(text, "approve") || !strings.Contains(text, "task.set_status") {
		t.Errorf("envelope.text should describe the decision: %q", text)
	}
}

// 2. Reject fans back with reason + envelope; executed absent.
func TestFanBack_ProposeReject_NoExecuted(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "test-task", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	_ = seedSessionForAgent(t, s, agentID)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done"},
	})
	out, _ := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	attID := unwrapMcpResult(t, out)["request_id"].(string)

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "reject", "by": "@principal", "reason": "not yet"})
	if status != 200 {
		t.Fatalf("decide: %d", status)
	}

	p := readLatestAttentionReply(t, s, agentID)
	if p["decision"] != "reject" {
		t.Errorf("decision = %v; want reject", p["decision"])
	}
	if p["reason"] != "not yet" {
		t.Errorf("reason = %v; want not yet", p["reason"])
	}
	if _, has := p["executed"]; has {
		t.Errorf("executed should be absent on reject; got %v", p["executed"])
	}
	env := p["envelope"].(map[string]any)
	if env["kind"] != KindReport {
		t.Errorf("envelope.kind = %v; want %q", env["kind"], KindReport)
	}
	if text, _ := env["text"].(string); !strings.Contains(text, "reject") {
		t.Errorf("envelope.text should describe reject: %q", text)
	}
}

// 3. dry_run does NOT fan back (the preview rides on the
// awaiting_response synchronous return — the row isn't even
// inserted).
func TestFanBack_DryRunNoInsert_NoFanBack(t *testing.T) {
	s, _ := newA2ATestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "test-task", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	_ = seedSessionForAgent(t, s, agentID)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done"},
		"dry_run":     true,
	})
	_, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose dry_run: %v", jerr)
	}
	var n int
	if err := s.eventsDB.QueryRow(`
		SELECT count(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.attention_reply'`, agentID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("dry_run produced %d attention_reply events; want 0", n)
	}
}

// 4. Envelope cause round-trip: the source attention row's `cause`
// column is set, and the fan-back's envelope.cause carries the same
// value. Cause is the lineage pointer to the enclosing task; without
// this hop the directive-trace can't walk past the propose decision.
func TestFanBack_EnvelopeCauseRoundTrip(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "test-task", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	_ = seedSessionForAgent(t, s, agentID)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done"},
	})
	out, _ := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	attID := unwrapMcpResult(t, out)["request_id"].(string)

	// Stamp a cause on the row (the propose handler doesn't populate
	// cause yet — it's set by upstream code that links the propose to
	// the enclosing task/directive). We simulate that step here so the
	// envelope-composition path has something to round-trip.
	causeTaskID := "task-cause-001"
	if _, err := s.db.Exec(
		`UPDATE attention_items SET cause = ? WHERE id = ?`,
		causeTaskID, attID); err != nil {
		// FK constraint: cause references tasks(id). seedTask creates
		// the row already; here we just need a different existing task.
		causeTaskID = taskID
		if _, err := s.db.Exec(
			`UPDATE attention_items SET cause = ? WHERE id = ?`,
			causeTaskID, attID); err != nil {
			t.Fatalf("stamp cause: %v", err)
		}
	}

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})
	if status != 200 {
		t.Fatalf("decide: %d", status)
	}

	p := readLatestAttentionReply(t, s, agentID)
	env := p["envelope"].(map[string]any)
	if env["cause"] != causeTaskID {
		t.Errorf("envelope.cause = %v; want %s (round-tripped from row)", env["cause"], causeTaskID)
	}
}

// 5. Override fans back too: requester sees decision=override +
// executed=rollback so they know the state reverted.
func TestFanBack_Override_FansBackRollback(t *testing.T) {
	s, token := newA2ATestServer(t)
	dir := s.cfg.DataRoot
	reqWithPolicy(t, s, dir, `  task.set_status:
    default_tier: principal
    override_allowed: true`)

	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "test-task", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)
	_ = seedSessionForAgent(t, s, agentID)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done"},
	})
	out, _ := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	attID := unwrapMcpResult(t, out)["request_id"].(string)

	// First decide (approve) → 1 fan-back.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})

	// Override → 2nd fan-back.
	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{
			"decision": "override", "by": "@principal",
			"override": true, "reason": "reverting",
		})

	// Count fan-back events: should be 2 (approve + override).
	var n int
	_ = s.eventsDB.QueryRow(`
		SELECT count(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.attention_reply'`, agentID).Scan(&n)
	if n != 2 {
		t.Errorf("attention_reply count = %d; want 2 (approve + override)", n)
	}
	// Latest is the override fan-back.
	p := readLatestAttentionReply(t, s, agentID)
	if p["decision"] != "override" {
		t.Errorf("latest decision = %v; want override", p["decision"])
	}
	if p["executed"] == nil {
		t.Error("override fan-back missing executed (rollback payload)")
	}
	// executed should report the rollback (status reverted to in_progress).
	exec := p["executed"].(map[string]any)
	if exec["to_status"] != "in_progress" {
		t.Errorf("rollback to_status = %v; want in_progress", exec["to_status"])
	}
}

// 6. Non-propose kind (approval_request) still fan-backs WITHOUT
// change_kind/executed (regression — the extras struct's empty case
// must not break existing attention kinds).
func TestFanBack_LegacyApprovalRequest_NoProposeExtras(t *testing.T) {
	s, token := newA2ATestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}}
	}`)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w-legacy",
		"claude-code", "")
	_ = seedSessionForAgent(t, s, agentID)

	// A non-propose attention kind (approval_request). W1.4 retired the
	// alias dispatcher arm, so decide does not fire an apply here; this
	// verifies the turn-based fan-back composes regardless of dispatch.
	spawnPayload, _ := json.Marshal(map[string]any{
		"child_handle":    "w-spawned",
		"kind":            "claude-code",
		"host_id":         hostID,
		"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: echo x\n",
	})
	attID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle, session_id
		) VALUES (?, NULL, 'team', NULL, 'approval_request',
		          'spawn', 'minor', '[]',
		          ?, 'open', ?, 'agent', 'caller',
		          (SELECT id FROM sessions WHERE current_agent_id = ?))`,
		attID, string(spawnPayload), NowUTC(), agentID); err != nil {
		t.Fatalf("seed legacy att: %v", err)
	}

	doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})

	p := readLatestAttentionReply(t, s, agentID)
	if p["kind"] != "approval_request" {
		t.Errorf("kind = %v; want approval_request", p["kind"])
	}
	// A non-propose attention carries no change_kind (that column is
	// populated only for propose rows), so the fan-back reply must not
	// surface propose-only extras.
	if ck, has := p["change_kind"]; has && ck != "" {
		t.Errorf("approval_request should not carry change_kind; got %v", ck)
	}
	// Envelope is composed regardless of kind.
	env := p["envelope"].(map[string]any)
	if env["kind"] != KindReport {
		t.Errorf("envelope.kind = %v; want %q", env["kind"], KindReport)
	}
}

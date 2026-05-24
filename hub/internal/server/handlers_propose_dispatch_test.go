package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-030 W8 — exercises the /decide handler refactor end-to-end.
// Three input shapes converge on the propose-kind registry, with
// the audit-meta `via` tag as the discriminator:
//
//   - kind="propose" + change_kind="X" → via="propose"
//   - kind="approval_request" + spawnIn payload → via="alias_legacy"
//   - kind="template_proposal" + install payload → via="alias_legacy"
//
// Each shape lands the same row update (executed_json + status) and
// the same per-kind apply audit (with the via tag differing).

// 1. propose(kind="task.set_status") → /decide approve → apply runs,
// task status changes, audit carries via=propose + propose_id, the
// row's executed_json is populated.
func TestDecideDispatcher_Propose_TaskSetStatus_EndToEnd(t *testing.T) {
	s, token := newA2ATestServer(t)
	// Seed: project + task in in_progress + worker agent.
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "review_pr", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)

	// Raise the propose row via mcpPropose.
	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done", "result_summary": "shipped"},
		"reason":      "tests green",
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	attID := payload["request_id"].(string)
	// Status untouched until decide.
	if got := taskStatus(t, s, taskID); got != "in_progress" {
		t.Fatalf("propose mutated status: %q", got)
	}

	// /decide approve.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})
	if status != 200 {
		t.Fatalf("decide: %d body=%s", status, string(body))
	}
	var dec attentionDecideOut
	_ = json.Unmarshal(body, &dec)
	if !dec.Resolved {
		t.Error("expected Resolved=true")
	}
	if len(dec.Executed) == 0 {
		t.Fatal("Executed missing")
	}
	var executed map[string]any
	_ = json.Unmarshal(dec.Executed, &executed)
	if executed["audit_action"] != "task.status" {
		t.Errorf("executed.audit_action = %v; want task.status", executed["audit_action"])
	}

	// Status changed.
	if got := taskStatus(t, s, taskID); got != "done" {
		t.Errorf("status = %q; want done", got)
	}

	// Audit carries via=propose + propose_id back to the attention row.
	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'task.status' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, taskID).Scan(&meta)
	if !strings.Contains(meta, `"via":"propose"`) {
		t.Errorf("audit missing via=propose: %q", meta)
	}
	if !strings.Contains(meta, `"propose_id":"`+attID+`"`) {
		t.Errorf("audit missing propose_id: %q", meta)
	}

	// executed_json mirror lands on the row.
	var execJSON string
	_ = s.db.QueryRow(
		`SELECT COALESCE(executed_json,'') FROM attention_items WHERE id = ?`, attID,
	).Scan(&execJSON)
	if execJSON == "" {
		t.Error("executed_json mirror not persisted")
	}
}

// 2. propose(kind="agent.spawn") via the new verb → spawn via registry,
// audit via=propose.
func TestDecideDispatcher_Propose_AgentSpawn_EndToEnd(t *testing.T) {
	s, token := newA2ATestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}}
	}`)
	proj := seedProject(t, s, defaultTeamID)
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w-propose-spawn",
		"steward.v1", proj)

	args, _ := json.Marshal(map[string]any{
		"kind":       "agent.spawn",
		"target_ref": map[string]any{},
		"change_spec": map[string]any{
			"child_handle":    "new-worker",
			"kind":            "claude-code",
			"host_id":         hostID,
			"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: echo test\n",
		},
		"reason": "need a worker",
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	attID := payload["request_id"].(string)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})
	if status != 200 {
		t.Fatalf("decide: %d body=%s", status, string(body))
	}
	var dec attentionDecideOut
	_ = json.Unmarshal(body, &dec)
	var executed map[string]any
	_ = json.Unmarshal(dec.Executed, &executed)
	if executed["kind"] != "spawn" {
		t.Errorf("executed.kind = %v; want spawn", executed["kind"])
	}
	spawnedID := executed["agent_id"].(string)
	if spawnedID == "" {
		t.Fatal("expected agent_id in executed")
	}

	// Audit carries via=propose.
	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'agent.spawn' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, spawnedID).Scan(&meta)
	if !strings.Contains(meta, `"via":"propose"`) {
		t.Errorf("audit missing via=propose: %q", meta)
	}
	if !strings.Contains(meta, `"propose_id":"`+attID+`"`) {
		t.Errorf("audit missing propose_id: %q", meta)
	}
}

// 3. Legacy approval_request + spawnIn pending_payload → /decide
// approve → dispatcher routes through registry → spawn + audit
// via=alias_legacy. This is the BACKWARD-COMPAT case: the wire shape
// hasn't changed but the audit-meta `via` tag flags it as the
// pre-ADR-030 dispatch hop.
func TestDecideDispatcher_AliasLegacy_ApprovalRequestSpawn(t *testing.T) {
	s, token := newA2ATestServer(t)
	hostID := seedHostCaps(t, s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M1","M2","M4"]}}
	}`)

	// Hand-craft the legacy attention row shape: kind='approval_request'
	// with the spawnIn JSON in pending_payload_json. This mirrors what
	// the pre-ADR-030 MCP `request_approval` plus spawnIn payload
	// flow built up before the dispatcher refactor.
	spawnPayload, _ := json.Marshal(map[string]any{
		"child_handle":    "alias-spawn",
		"kind":            "claude-code",
		"host_id":         hostID,
		"spawn_spec_yaml": "kind: claude-code\nbackend:\n  cmd: echo legacy\n",
	})
	attID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', NULL, 'approval_request',
		          'spawn worker', 'minor', '[]',
		          ?, 'open', ?,
		          'agent', 'legacy-caller')`,
		attID, string(spawnPayload), NowUTC()); err != nil {
		t.Fatalf("seed legacy attention: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})
	if status != 200 {
		t.Fatalf("decide: %d body=%s", status, string(body))
	}
	var dec attentionDecideOut
	_ = json.Unmarshal(body, &dec)
	var executed map[string]any
	_ = json.Unmarshal(dec.Executed, &executed)
	if executed["kind"] != "spawn" {
		t.Errorf("executed.kind = %v; want spawn", executed["kind"])
	}
	spawnedID := executed["agent_id"].(string)

	// Audit via=alias_legacy — the discriminator from the new code.
	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'agent.spawn' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, spawnedID).Scan(&meta)
	if !strings.Contains(meta, `"via":"alias_legacy"`) {
		t.Errorf("audit should carry via=alias_legacy; got %q", meta)
	}
	// propose_id still set (the attention row's id) so consumers can
	// link the audit back to the row even on the legacy path.
	if !strings.Contains(meta, `"propose_id":"`+attID+`"`) {
		t.Errorf("audit missing propose_id: %q", meta)
	}
}

// 4. Legacy template_proposal payload → /decide approve → dispatcher
// routes through registry → install + audit via=alias_legacy.
func TestDecideDispatcher_AliasLegacy_TemplateProposal(t *testing.T) {
	s, token := newA2ATestServer(t)
	// Seed the blob.
	body := []byte("kind: legacy-template\n")
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	seedBlob(t, s, body)

	payloadJSON, _ := json.Marshal(map[string]any{
		"category":    "prompt",
		"name":        "legacy-shape.v1",
		"blob_sha256": sha,
	})
	attID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			pending_payload_json, status, created_at,
			actor_kind, actor_handle
		) VALUES (?, NULL, 'team', NULL, 'template_proposal',
		          'install legacy template', 'minor', '[]',
		          ?, 'open', ?,
		          'agent', 'legacy-proposer')`,
		attID, string(payloadJSON), NowUTC()); err != nil {
		t.Fatalf("seed legacy attention: %v", err)
	}

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "approve", "by": "@principal"})
	if status != 200 {
		t.Fatalf("decide: %d", status)
	}

	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'template.install' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, "prompt/legacy-shape.v1").Scan(&meta)
	if !strings.Contains(meta, `"via":"alias_legacy"`) {
		t.Errorf("audit should carry via=alias_legacy; got %q", meta)
	}
}

// 5. Reject decision: no apply runs, no audit emitted for the kind.
// Existing tests already cover the reject path for legacy shapes;
// here we add a specific propose-shape reject regression so the
// dispatcher refactor doesn't accidentally fire Apply on reject.
func TestDecideDispatcher_Propose_RejectSkipsApply(t *testing.T) {
	s, token := newA2ATestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "review_pr", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)

	args, _ := json.Marshal(map[string]any{
		"kind":        "task.set_status",
		"target_ref":  map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec": map[string]any{"status": "done"},
	})
	out, _ := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	payload := unwrapMcpResult(t, out)
	attID := payload["request_id"].(string)

	beforeAudits := countAudits(t, s)
	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+attID+"/decide",
		map[string]any{"decision": "reject", "by": "@principal", "reason": "not ready"})
	if status != 200 {
		t.Fatalf("decide: %d", status)
	}
	// Task status unchanged.
	if got := taskStatus(t, s, taskID); got != "in_progress" {
		t.Errorf("reject mutated status: %q", got)
	}
	// No task.status audit row written (only the attention.decide audit
	// the decide handler itself emits at the bottom).
	if cnt := countAuditsByAction(t, s, "task.status"); cnt != 0 {
		t.Errorf("reject emitted task.status audit (count=%d)", cnt)
	}
	// Audit count went up by exactly the attention.decide row.
	if after := countAudits(t, s); after != beforeAudits+1 {
		t.Errorf("audit count after reject = %d; want %d (attention.decide only)",
			after, beforeAudits+1)
	}
}

// helper: count audits filtered by action.
func countAuditsByAction(t *testing.T, s *Server, action string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM audit_events WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

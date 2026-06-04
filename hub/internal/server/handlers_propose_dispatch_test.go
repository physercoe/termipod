package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-030 W8 — exercises the /decide handler dispatcher end-to-end.
// An approved propose row converges on the propose-kind registry with
// audit-meta `via="propose"`:
//
//   - kind="propose" + change_kind="X" → via="propose"
//
// (The pre-ADR-030 approval_request / template_proposal alias arms were
// retired in W1.4; the dispatcher now routes only propose rows.) Each
// shape lands the same row update (executed_json + status) and the
// per-kind apply audit.

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

// 3. Reject decision: no apply runs, no audit emitted for the kind.
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

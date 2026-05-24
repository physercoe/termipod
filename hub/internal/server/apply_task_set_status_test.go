package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ADR-030 W7 — apply_task_set_status.go. Mirrors W5/W6 layout against
// the apply function registered at init().

func seedTask(t *testing.T, s *Server, project, title, status string) string {
	t.Helper()
	id := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, body_md, status, priority, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, 'med', ?, ?)`,
		id, project, title, status, now, now); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return id
}

func taskStatus(t *testing.T, s *Server, id string) string {
	t.Helper()
	var st string
	if err := s.db.QueryRow(`SELECT status FROM tasks WHERE id = ?`, id).Scan(&st); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return st
}

func taskCompletedAt(t *testing.T, s *Server, id string) string {
	t.Helper()
	var ca string
	_ = s.db.QueryRow(
		`SELECT COALESCE(completed_at,'') FROM tasks WHERE id = ?`, id).Scan(&ca)
	return ca
}

// 1. Registered at init.
func TestTaskSetStatus_RegisteredAtInit(t *testing.T) {
	pk, ok := LookupProposeKind("task.set_status")
	if !ok {
		t.Fatal("task.set_status not registered at init()")
	}
	if pk.Validate == nil || pk.DryRun == nil || pk.Apply == nil {
		t.Errorf("missing functions: validate=%v dry=%v apply=%v",
			pk.Validate != nil, pk.DryRun != nil, pk.Apply != nil)
	}
}

// 2. Validate — shape + propose-permitted-status set.
func TestTaskSetStatus_Validate(t *testing.T) {
	pk, _ := LookupProposeKind("task.set_status")
	cases := []struct {
		name   string
		target string
		spec   string
		wantOK bool
		wantIn string
	}{
		{"happy done", `{"project_id":"p","task_id":"t"}`,
			`{"status":"done","result_summary":"shipped"}`, true, ""},
		{"happy cancelled", `{"project_id":"p","task_id":"t"}`,
			`{"status":"cancelled"}`, true, ""},
		{"missing project_id", `{"task_id":"t"}`,
			`{"status":"done"}`, false, "project_id"},
		{"missing task_id", `{"project_id":"p"}`,
			`{"status":"done"}`, false, "task_id"},
		{"missing status", `{"project_id":"p","task_id":"t"}`,
			`{}`, false, "status required"},
		{"reject in_progress", `{"project_id":"p","task_id":"t"}`,
			`{"status":"in_progress"}`, false, "not propose-permitted"},
		{"reject blocked", `{"project_id":"p","task_id":"t"}`,
			`{"status":"blocked"}`, false, "not propose-permitted"},
		{"reject todo", `{"project_id":"p","task_id":"t"}`,
			`{"status":"todo"}`, false, "not propose-permitted"},
		{"reject bogus", `{"project_id":"p","task_id":"t"}`,
			`{"status":"shipped"}`, false, "not propose-permitted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pk.Validate(context.Background(), nil,
				json.RawMessage(tc.target), json.RawMessage(tc.spec))
			if tc.wantOK {
				if err != nil {
					t.Errorf("validate: %v; want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want error; got nil")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err %q should contain %q", err.Error(), tc.wantIn)
			}
		})
	}
}

// 3. DryRun returns preview with task title + current status; row
// stays unchanged.
func TestTaskSetStatus_DryRun_Preview(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("task.set_status")
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "research_review", "in_progress")

	target, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": taskID})
	spec, _ := json.Marshal(map[string]any{"status": "done", "result_summary": "shipped"})
	raw, err := pk.DryRun(context.Background(), s, target, spec)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	var preview map[string]any
	_ = json.Unmarshal(raw, &preview)
	if preview["task_title"] != "research_review" {
		t.Errorf("task_title = %v; want research_review", preview["task_title"])
	}
	if preview["from_status"] != "in_progress" {
		t.Errorf("from_status = %v; want in_progress", preview["from_status"])
	}
	if preview["to_status"] != "done" {
		t.Errorf("to_status = %v; want done", preview["to_status"])
	}
	if preview["result_summary"] != "shipped" {
		t.Errorf("result_summary = %v; want shipped", preview["result_summary"])
	}
	if got := taskStatus(t, s, taskID); got != "in_progress" {
		t.Errorf("DryRun mutated status: %q", got)
	}

	// Missing task → error.
	missing, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": "ghost"})
	if _, err := pk.DryRun(context.Background(), s, missing, spec); err == nil {
		t.Error("expected error on missing task")
	}
}

// 4. Apply: in_progress → done with result_summary, stamps
// completed_at, audit carries propose lineage.
func TestTaskSetStatus_Apply_DoneWithResultSummary(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("task.set_status")
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "ship_feature", "in_progress")

	target, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": taskID})
	spec, _ := json.Marshal(map[string]any{"status": "done", "result_summary": "feature shipped to prod"})
	ac := ProposeApplyContext{
		AttentionID: "att-task-1", Team: defaultTeamID,
		AssignedTier: GovTierProjectSteward, DeciderHandle: "@steward.proj",
	}
	raw, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(raw, &executed)
	if executed["audit_action"] != "task.status" {
		t.Errorf("audit_action = %v; want task.status", executed["audit_action"])
	}
	if got := taskStatus(t, s, taskID); got != "done" {
		t.Errorf("status = %q; want done", got)
	}
	if got := taskCompletedAt(t, s, taskID); got == "" {
		t.Error("completed_at not stamped on done")
	}
	// result_summary persisted.
	var rs string
	_ = s.db.QueryRow(`SELECT COALESCE(result_summary,'') FROM tasks WHERE id = ?`, taskID).Scan(&rs)
	if rs != "feature shipped to prod" {
		t.Errorf("result_summary = %q; want feature shipped to prod", rs)
	}

	// Audit lineage.
	var meta string
	if err := s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'task.status' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, taskID).Scan(&meta); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{
		`"via":"propose"`,
		`"propose_id":"att-task-1"`,
		`"by_tier":"project-steward"`,
		`"by_actor":"@steward.proj"`,
		`"from":"in_progress"`,
		`"to":"done"`,
	} {
		if !strings.Contains(meta, want) {
			t.Errorf("audit meta missing %s: %q", want, meta)
		}
	}
}

// 5. Apply: in_progress → cancelled, also stamps completed_at.
func TestTaskSetStatus_Apply_Cancelled(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("task.set_status")
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "spike", "in_progress")

	target, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": taskID})
	spec, _ := json.Marshal(map[string]any{"status": "cancelled"})
	ac := ProposeApplyContext{Team: defaultTeamID, AttentionID: "att-cancel"}
	raw, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(raw, &executed)
	if executed["audit_action"] != "task.status" {
		t.Errorf("audit_action = %v; want task.status", executed["audit_action"])
	}
	if got := taskStatus(t, s, taskID); got != "cancelled" {
		t.Errorf("status = %q; want cancelled", got)
	}
	if got := taskCompletedAt(t, s, taskID); got == "" {
		t.Error("completed_at not stamped on cancelled")
	}
}

// 6. Apply: no-op (current == to) returns executed.no_op, no row
// mutation, no audit.
func TestTaskSetStatus_Apply_NoOp(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("task.set_status")
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "already_done", "done")
	beforeAudits := countAudits(t, s)

	target, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": taskID})
	spec, _ := json.Marshal(map[string]any{"status": "done"})
	ac := ProposeApplyContext{Team: defaultTeamID}
	raw, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var executed map[string]any
	_ = json.Unmarshal(raw, &executed)
	if executed["no_op"] != true {
		t.Errorf("no_op = %v; want true", executed["no_op"])
	}
	if after := countAudits(t, s); after != beforeAudits {
		t.Errorf("no-op apply emitted an audit (count %d → %d)", beforeAudits, after)
	}
}

// 7. Apply: task not found in this project → not-found error.
func TestTaskSetStatus_Apply_NotFound(t *testing.T) {
	s, _ := newTestServer(t)
	pk, _ := LookupProposeKind("task.set_status")
	proj := seedProject(t, s, defaultTeamID)

	target, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": "ghost"})
	spec, _ := json.Marshal(map[string]any{"status": "done"})
	ac := ProposeApplyContext{Team: defaultTeamID, AttentionID: "att-nf"}
	_, err := pk.Apply(context.Background(), s, ac, target, spec)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err %q should say not found", err.Error())
	}
}

// 8. End-to-end: propose → row lands → manual apply → state changes
// + audit links back via propose_id.
func TestTaskSetStatus_EndToEnd(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProject(t, s, defaultTeamID)
	taskID := seedTask(t, s, proj, "review_pr", "in_progress")
	agentID := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", proj)

	args, _ := json.Marshal(map[string]any{
		"kind":           "task.set_status",
		"target_ref":     map[string]any{"project_id": proj, "task_id": taskID},
		"change_spec":    map[string]any{"status": "done", "result_summary": "merged"},
		"reason":         "tests passing",
		"addressee_tier": GovTierProjectSteward,
	})
	out, jerr := s.mcpPropose(context.Background(), defaultTeamID, agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPropose: %v", jerr)
	}
	payload := unwrapMcpResult(t, out)
	attID := payload["request_id"].(string)
	if payload["assigned_tier"] != GovTierProjectSteward {
		t.Errorf("assigned_tier = %v; want project-steward", payload["assigned_tier"])
	}
	// Status unchanged (no apply yet — W8 wires that).
	if got := taskStatus(t, s, taskID); got != "in_progress" {
		t.Errorf("propose mutated status; got %q", got)
	}

	// Manual apply (simulates W8 dispatch on decide-approve).
	pk, _ := LookupProposeKind("task.set_status")
	target, _ := json.Marshal(map[string]any{"project_id": proj, "task_id": taskID})
	spec, _ := json.Marshal(map[string]any{"status": "done", "result_summary": "merged"})
	ac := ProposeApplyContext{
		AttentionID: attID, Team: defaultTeamID,
		AssignedTier: GovTierProjectSteward, DeciderHandle: "@steward.proj",
	}
	if _, err := pk.Apply(context.Background(), s, ac, target, spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := taskStatus(t, s, taskID); got != "done" {
		t.Errorf("status after apply = %q; want done", got)
	}

	var meta string
	_ = s.db.QueryRow(`
		SELECT meta_json FROM audit_events
		 WHERE action = 'task.status' AND target_id = ?
		 ORDER BY ts DESC LIMIT 1`, taskID).Scan(&meta)
	if !strings.Contains(meta, `"propose_id":"`+attID+`"`) {
		t.Errorf("audit should link back to propose row: %q", meta)
	}
}

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// W5 (corrected v1.0.626): in addition to the render-only `task.notify`
// event, the hub emits an `input.text` event with producer="system" so
// the InputRouter dispatches a fresh turn to the steward's engine.
// v1.0.611 W5 originally used kind `input.task_completed` and payload
// field `text`, but no driver handles that kind — every driver's
// switch falls through to `default: unsupported input kind`. The
// steward saw the card but its engine never woke. Corrective fix:
// `input.text` + `body` payload field (drivers' canonical text
// shape). See docs/discussions/validate-at-every-boundary.md §1.

func TestNotifyTaskAssigner_EmitsInputTextEvent_OnDone(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-input-completed")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Survey citation graph")
	if _, err := s.db.Exec(
		`UPDATE tasks SET result_summary = ? WHERE id = ?`,
		"Found 12 cited works; produced gaps section.", taskID,
	); err != nil {
		t.Fatalf("stamp summary: %v", err)
	}

	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "done")

	// Both events should exist for the same agent: render-only
	// task.notify (audit card) AND input.text (driver-deliverable
	// wake event).
	var taskNotifyCount, inputCount int
	if err := s.eventsDB.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'task.notify'`,
		stewardID).Scan(&taskNotifyCount); err != nil {
		t.Fatalf("count task.notify: %v", err)
	}
	if err := s.eventsDB.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'`,
		stewardID).Scan(&inputCount); err != nil {
		t.Fatalf("count input.text: %v", err)
	}
	if taskNotifyCount != 1 {
		t.Errorf("task.notify count = %d; want 1 (render-only audit card)", taskNotifyCount)
	}
	if inputCount != 1 {
		t.Errorf("input.text count = %d; want 1 (driver-deliverable wake)", inputCount)
	}

	// Inspect the input.text payload — body is the field every
	// driver's `case "text"` branch reads.
	var payload string
	if err := s.eventsDB.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Text          string `json:"text"`
		TaskID        string `json:"task_id"`
		TaskTitle     string `json:"task_title"`
		ResultSummary string `json:"result_summary"`
		ToStatus      string `json:"to_status"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.TaskID != taskID {
		t.Errorf("payload task_id = %q; want %q", p.TaskID, taskID)
	}
	if p.TaskTitle != "Survey citation graph" {
		t.Errorf("payload task_title = %q", p.TaskTitle)
	}
	if !strings.Contains(p.Text, "Survey citation graph") {
		t.Errorf("body should name the task: %q", p.Text)
	}
	if !strings.Contains(p.Text, "completed") {
		t.Errorf("body should say 'completed' for done transition: %q", p.Text)
	}
	if !strings.Contains(p.Text, "Found 12 cited") {
		t.Errorf("body should include result_summary: %q", p.Text)
	}
	if !strings.Contains(p.Text, "Decide next step") {
		t.Errorf("body should prompt the steward to act: %q", p.Text)
	}
	if p.ToStatus != "done" {
		t.Errorf("payload to = %q; want done", p.ToStatus)
	}
}

// Verb-correctness for non-done terminal transitions. Pre-v1.0.626
// the body unconditionally said "completed" even for blocked /
// cancelled — misleading the steward LLM about what happened.
func TestNotifyTaskAssigner_BodyUsesCorrectVerb_OnBlocked(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-input-blocked")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Rename project")
	if _, err := s.db.Exec(
		`UPDATE tasks SET result_summary = ? WHERE id = ?`,
		"Hub denied projects.update for worker role.", taskID,
	); err != nil {
		t.Fatalf("stamp summary: %v", err)
	}

	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "blocked")

	var payload string
	if err := s.eventsDB.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Text     string `json:"text"`
		ToStatus string `json:"to_status"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(p.Text, "blocked") {
		t.Errorf("body should say 'blocked' for blocked transition, got %q", p.Text)
	}
	if strings.Contains(p.Text, "completed") {
		t.Errorf("body must not say 'completed' for blocked transition, got %q", p.Text)
	}
	if !strings.Contains(p.Text, "Reason: ") {
		t.Errorf("body should label the summary as 'Reason:' for blocked, got %q", p.Text)
	}
	if p.ToStatus != "blocked" {
		t.Errorf("payload to = %q; want blocked", p.ToStatus)
	}
}

func TestNotifyTaskAssigner_BodyUsesCorrectVerb_OnCancelled(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-input-cancelled")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Defer scope")

	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "cancelled")

	var payload string
	if err := s.eventsDB.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(p.Text, "cancelled") {
		t.Errorf("body should say 'cancelled' for cancelled transition, got %q", p.Text)
	}
	if strings.Contains(p.Text, "completed") {
		t.Errorf("body must not say 'completed' for cancelled, got %q", p.Text)
	}
}

// v1.0.628: auto-derive must NOT overwrite a worker's explicit
// `blocked` declaration when the operator manually terminates the
// agent for cleanup. Pre-bundle, manual stop on a blocked worker
// flipped the task to `cancelled` (or `done` if any summary was
// present) — erasing the worker's verdict and posting a misleading
// "Task X cancelled" wake to the steward. Same protection that
// `cancelled` already had.
func TestDeriveTaskStatus_PreservesBlockedOnManualTerminate(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-preserve-blocked")
	_, taskID := seedAssignerAndTask(t, s, proj, "Try projects.update")
	// Worker explicitly declares blocked via tasks.update.
	if _, err := s.db.Exec(
		`UPDATE tasks SET status = 'blocked', body_md = ? WHERE id = ?`,
		"Hub denied projects.update for worker role.", taskID,
	); err != nil {
		t.Fatalf("set blocked: %v", err)
	}
	// Operator manually stops the worker — find the worker's agent id
	// (assignee_id on the task row) and run auto-derive as
	// stopSessionInternal does.
	var workerID string
	if err := s.db.QueryRow(
		`SELECT assignee_id FROM tasks WHERE id = ?`, taskID).
		Scan(&workerID); err != nil {
		t.Fatalf("find worker: %v", err)
	}
	if err := s.deriveTaskStatusFromAgent(
		context.Background(), defaultTeamID, workerID, "terminated"); err != nil {
		t.Fatalf("derive: %v", err)
	}
	// Task status must STILL be blocked — operator cleanup did not
	// erase the worker's verdict.
	var got string
	if err := s.db.QueryRow(
		`SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&got); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if got != "blocked" {
		t.Errorf("task status = %q after manual terminate; want blocked (operator cleanup must not overwrite worker's explicit verdict)", got)
	}
}

func TestNotifyTaskAssigner_NoInputEvent_ForNonTerminalTransition(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-no-input-on-running")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Probe")

	// Non-terminal transition — should produce neither event.
	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"todo", "in_progress")

	var inputCount int
	if err := s.eventsDB.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'`,
		stewardID).Scan(&inputCount); err != nil {
		t.Fatalf("count input.text: %v", err)
	}
	if inputCount != 0 {
		t.Errorf("input.text should not fire for non-terminal transition; got %d", inputCount)
	}
}

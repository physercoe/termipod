package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// W5: in addition to the render-only `task.notify` event, the hub
// emits an `input.task_completed` event with producer="system" so the
// InputRouter dispatches a fresh turn to the steward's engine. Pre-
// bundle the steward saw the card on mobile but its engine never
// picked up a turn — compose-box-busy stuck. See
// docs/discussions/validate-at-every-boundary.md §1 (steward stuck
// symptom).

func TestNotifyTaskAssigner_EmitsInputTaskCompletedEvent(t *testing.T) {
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
	// task.notify (existing) AND new input.task_completed (W5).
	var taskNotifyCount, inputCount int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'task.notify'`,
		stewardID).Scan(&taskNotifyCount); err != nil {
		t.Fatalf("count task.notify: %v", err)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.task_completed' AND producer = 'system'`,
		stewardID).Scan(&inputCount); err != nil {
		t.Fatalf("count input.task_completed: %v", err)
	}
	if taskNotifyCount != 1 {
		t.Errorf("task.notify count = %d; want 1 (render-only existing path)", taskNotifyCount)
	}
	if inputCount != 1 {
		t.Errorf("input.task_completed count = %d; want 1 (W5 new path)", inputCount)
	}

	// Inspect the input.task_completed payload — it should carry the
	// text body the steward sees as its next-turn input, plus the
	// structured fields the prompt rule references.
	var payload string
	if err := s.db.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.task_completed'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Text          string `json:"text"`
		TaskID        string `json:"task_id"`
		TaskTitle     string `json:"task_title"`
		ResultSummary string `json:"result_summary"`
		To            string `json:"to"`
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
		t.Errorf("text body should name the task: %q", p.Text)
	}
	if !strings.Contains(p.Text, "Found 12 cited") {
		t.Errorf("text body should include result_summary: %q", p.Text)
	}
	if !strings.Contains(p.Text, "Decide next step") {
		t.Errorf("text body should prompt the steward to act: %q", p.Text)
	}
	if p.To != "done" {
		t.Errorf("payload to = %q; want done", p.To)
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
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.task_completed'`,
		stewardID).Scan(&inputCount); err != nil {
		t.Fatalf("count input.task_completed: %v", err)
	}
	if inputCount != 0 {
		t.Errorf("input.task_completed should not fire for non-terminal transition; got %d", inputCount)
	}
}

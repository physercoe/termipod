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
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'task.notify'`,
		stewardID).Scan(&taskNotifyCount); err != nil {
		t.Fatalf("count task.notify: %v", err)
	}
	if err := s.db.QueryRow(`
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
	if err := s.db.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Body          string `json:"body"`
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
	if !strings.Contains(p.Body, "Survey citation graph") {
		t.Errorf("body should name the task: %q", p.Body)
	}
	if !strings.Contains(p.Body, "completed") {
		t.Errorf("body should say 'completed' for done transition: %q", p.Body)
	}
	if !strings.Contains(p.Body, "Found 12 cited") {
		t.Errorf("body should include result_summary: %q", p.Body)
	}
	if !strings.Contains(p.Body, "Decide next step") {
		t.Errorf("body should prompt the steward to act: %q", p.Body)
	}
	if p.To != "done" {
		t.Errorf("payload to = %q; want done", p.To)
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
	if err := s.db.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Body string `json:"body"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(p.Body, "blocked") {
		t.Errorf("body should say 'blocked' for blocked transition, got %q", p.Body)
	}
	if strings.Contains(p.Body, "completed") {
		t.Errorf("body must not say 'completed' for blocked transition, got %q", p.Body)
	}
	if !strings.Contains(p.Body, "Reason: ") {
		t.Errorf("body should label the summary as 'Reason:' for blocked, got %q", p.Body)
	}
	if p.To != "blocked" {
		t.Errorf("payload to = %q; want blocked", p.To)
	}
}

func TestNotifyTaskAssigner_BodyUsesCorrectVerb_OnCancelled(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-input-cancelled")
	stewardID, taskID := seedAssignerAndTask(t, s, proj, "Defer scope")

	s.notifyTaskAssigner(context.Background(), defaultTeamID, taskID,
		"in_progress", "cancelled")

	var payload string
	if err := s.db.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'
		 ORDER BY seq DESC LIMIT 1`,
		stewardID).Scan(&payload); err != nil {
		t.Fatalf("fetch payload: %v", err)
	}
	var p struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(p.Body, "cancelled") {
		t.Errorf("body should say 'cancelled' for cancelled transition, got %q", p.Body)
	}
	if strings.Contains(p.Body, "completed") {
		t.Errorf("body must not say 'completed' for cancelled, got %q", p.Body)
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
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'`,
		stewardID).Scan(&inputCount); err != nil {
		t.Fatalf("count input.text: %v", err)
	}
	if inputCount != 0 {
		t.Errorf("input.text should not fire for non-terminal transition; got %d", inputCount)
	}
}

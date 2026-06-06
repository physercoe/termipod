// apply_task_set_status.go — ADR-030 W7 propose apply function for
// the `task.set_status` governed-action kind.
//
// The legacy REST path is PATCH /v1/teams/{t}/projects/{p}/tasks/{id}
// (handlePatchTask), the MCP path is `tasks.complete` /
// `tasks.update`. Both auto-stamp `completed_at` on the terminal
// `done` / `cancelled` flip and fire `notifyTaskAssigner` so the
// steward who delegated the work hears about it inline (W2.9 of
// ADR-029).
//
// Propose narrows the legal transition set per ADR-029 D-3: only
// `done` and `cancelled` are propose-permitted. `in_progress` and
// `blocked` are auto-derived from agent activity (deriveTaskStatus
// FromAgent runs from the spawn-lifecycle watchers), so proposing
// them would race the auto-derive and confuse the audit timeline.
// The `todo` initial state is set at task-create time and never
// re-entered through propose.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func init() {
	RegisterProposeKind(ProposeKind{
		Kind:     "task.set_status",
		Validate: validateTaskSetStatus,
		DryRun:   dryRunTaskSetStatus,
		Apply:    applyTaskSetStatus,
		Rollback: rollbackTaskSetStatus,
	})
}

// taskSetStatusTarget is the per-kind target_ref shape.
type taskSetStatusTarget struct {
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
}

// taskSetStatusSpec is the per-kind change_spec shape. ResultSummary
// is recommended for `done`; allowed-but-pointless for `cancelled`.
type taskSetStatusSpec struct {
	Status        string `json:"status"`
	ResultSummary string `json:"result_summary,omitempty"`
}

// proposePermittedTaskStatuses is the set propose allows. See file
// header for the auto-derive rationale on in_progress / blocked.
var proposePermittedTaskStatuses = map[string]bool{
	"done":      true,
	"cancelled": true,
}

func parseTaskSetStatus(targetRef, changeSpec json.RawMessage) (taskSetStatusTarget, taskSetStatusSpec, error) {
	var t taskSetStatusTarget
	if len(targetRef) > 0 {
		if err := json.Unmarshal(targetRef, &t); err != nil {
			return t, taskSetStatusSpec{}, fmt.Errorf("target_ref: %w", err)
		}
	}
	if t.ProjectID == "" {
		return t, taskSetStatusSpec{}, errors.New("target_ref.project_id required")
	}
	if t.TaskID == "" {
		return t, taskSetStatusSpec{}, errors.New("target_ref.task_id required")
	}
	var c taskSetStatusSpec
	if len(changeSpec) > 0 {
		if err := json.Unmarshal(changeSpec, &c); err != nil {
			return t, c, fmt.Errorf("change_spec: %w", err)
		}
	}
	if c.Status == "" {
		return t, c, errors.New("change_spec.status required")
	}
	if !proposePermittedTaskStatuses[c.Status] {
		return t, c, fmt.Errorf(
			"change_spec.status %q not propose-permitted (allowed: done, cancelled; "+
				"in_progress and blocked are auto-derived from agent activity per ADR-029 D-3)",
			c.Status)
	}
	return t, c, nil
}

// validateTaskSetStatus is a pure shape check.
func validateTaskSetStatus(_ context.Context, _ *Server, targetRef, changeSpec json.RawMessage) error {
	_, _, err := parseTaskSetStatus(targetRef, changeSpec)
	return err
}

// dryRunTaskSetStatus reads the task's current status + title so the
// preview shows "research_plan_review: in_progress → done" rather
// than just the proposed state.
func dryRunTaskSetStatus(ctx context.Context, s *Server, targetRef, changeSpec json.RawMessage) (json.RawMessage, error) {
	t, c, err := parseTaskSetStatus(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	var fromStatus, title string
	err = s.db.QueryRowContext(ctx,
		`SELECT status, title FROM tasks WHERE id = ? AND project_id = ?`,
		t.TaskID, t.ProjectID).Scan(&fromStatus, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %s not found in project %s", t.TaskID, t.ProjectID)
	}
	if err != nil {
		return nil, err
	}
	preview := map[string]any{
		"task_id":        t.TaskID,
		"task_title":     title,
		"from_status":    fromStatus,
		"to_status":      c.Status,
		"result_summary": c.ResultSummary,
		"no_op":          fromStatus == c.Status,
	}
	return json.Marshal(preview)
}

// applyTaskSetStatus mirrors handlePatchTask's status-flip branch:
// UPDATE status + completed_at (terminal stamp) + result_summary,
// emit `task.status` audit with from→to summary, then call
// notifyTaskAssigner so the steward's session gets the system
// message inline. The propose lineage rides on the audit row's
// meta (`via="propose"`, `by_tier`, `propose_id`); existing
// activity-feed renderers stay unchanged.
//
// No-op (from == to) short-circuits without row touch or audit
// emission — mirrors W5/W6.
func applyTaskSetStatus(
	ctx context.Context, s *Server, ac ProposeApplyContext, targetRef, changeSpec json.RawMessage,
) (json.RawMessage, error) {
	t, c, err := parseTaskSetStatus(targetRef, changeSpec)
	if err != nil {
		return nil, err
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("task.set_status: apply context missing team")
	}

	var fromStatus string
	err = s.db.QueryRowContext(ctx,
		`SELECT status FROM tasks WHERE id = ? AND project_id = ?`,
		t.TaskID, t.ProjectID).Scan(&fromStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task %s not found in project %s", t.TaskID, t.ProjectID)
	}
	if err != nil {
		return nil, fmt.Errorf("read task: %w", err)
	}

	executed := map[string]any{
		"task_id":     t.TaskID,
		"project_id":  t.ProjectID,
		"from_status": fromStatus,
		"to_status":   c.Status,
	}
	if fromStatus == c.Status {
		executed["no_op"] = true
		return json.Marshal(executed)
	}

	// Build the UPDATE. Mirror handlePatchTask: terminal status stamps
	// completed_at; result_summary is stamped when provided (the
	// NULLIF lets the caller omit it without zeroing a prior value
	// set by an earlier apply on the same task — though propose
	// rarely re-applies the same target).
	now := NowUTC()
	q := `UPDATE tasks SET status = ?, updated_at = ?`
	args := []any{c.Status, now}
	// Both done and cancelled are terminal per handlePatchTask:307; we
	// only reach Apply for those two via the Validate check.
	q += `, completed_at = ?`
	args = append(args, now)
	if c.ResultSummary != "" {
		q += `, result_summary = ?`
		args = append(args, c.ResultSummary)
	}
	q += ` WHERE id = ? AND project_id = ?`
	args = append(args, t.TaskID, t.ProjectID)
	if _, err := s.writeDB.ExecContext(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}

	// Audit row mirrors handlePatchTask's task.status emission; meta
	// adds the propose lineage. The summary string uses the same
	// "from → to" shape so the timeline chip renders identically.
	via := ac.ViaOrDefault()
	meta := map[string]any{
		"from":           fromStatus,
		"to":             c.Status,
		"source":         via,
		"via":            via,
		"by_tier":        ac.AssignedTier,
		"propose_id":     ac.AttentionID,
		"result_summary": c.ResultSummary,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "task.status", "task", t.TaskID,
		fromStatus+" → "+c.Status, meta)

	// Up-edge to the steward who delegated the task (ADR-029 W2.9).
	// Best-effort; silently degrades on missing assigner / sleeping
	// session — see task_notify.go.
	s.notifyTaskAssigner(ctx, team, t.TaskID, fromStatus, c.Status)

	executed["audit_action"] = "task.status"
	executed["completed_at"] = now
	return json.Marshal(executed)
}

// rollbackTaskSetStatus reverses a prior done/cancelled apply by
// restoring the prior status. We BYPASS the propose-permitted-status
// check via parseTaskSetStatus because the rollback is restoring an
// externally-derived state (in_progress / blocked / todo) — not
// proposing a new one. Writes the UPDATE directly + clears
// completed_at when restoring to a non-terminal status.
func rollbackTaskSetStatus(
	ctx context.Context, s *Server, ac ProposeApplyContext, originalSpec, originalExecuted json.RawMessage,
) (json.RawMessage, error) {
	var orig struct {
		ProjectID  string `json:"project_id"`
		TaskID     string `json:"task_id"`
		FromStatus string `json:"from_status"`
		ToStatus   string `json:"to_status"`
	}
	if err := json.Unmarshal(originalExecuted, &orig); err != nil {
		return nil, fmt.Errorf("rollback: parse original_executed: %w", err)
	}
	if orig.FromStatus == "" {
		return nil, errors.New("rollback: original_executed missing from_status")
	}
	team := ac.Team
	if team == "" {
		return nil, errors.New("task.set_status rollback: apply context missing team")
	}
	now := NowUTC()
	// Restore status. Clear completed_at when the prior status was
	// non-terminal (anything other than done/cancelled), so the task
	// looks "in-flight" again. Mirrors the legacy PATCH path's
	// completed_at logic.
	q := `UPDATE tasks SET status = ?, updated_at = ?`
	args := []any{orig.FromStatus, now}
	if orig.FromStatus != "done" && orig.FromStatus != "cancelled" {
		q += `, completed_at = NULL`
	}
	q += ` WHERE id = ? AND project_id = ?`
	args = append(args, orig.TaskID, orig.ProjectID)
	if _, err := s.writeDB.ExecContext(ctx, q, args...); err != nil {
		return nil, fmt.Errorf("rollback update: %w", err)
	}

	via := ac.ViaOrDefault()
	meta := map[string]any{
		"from":       orig.ToStatus,
		"to":         orig.FromStatus,
		"source":     via,
		"via":        via,
		"by_tier":    ac.AssignedTier,
		"propose_id": ac.AttentionID,
		"rollback":   true,
	}
	if ac.DeciderHandle != "" {
		meta["by_actor"] = ac.DeciderHandle
	}
	s.recordAudit(ctx, team, "task.status", "task", orig.TaskID,
		orig.ToStatus+" → "+orig.FromStatus+" (rollback)", meta)

	return json.Marshal(map[string]any{
		"task_id":      orig.TaskID,
		"project_id":   orig.ProjectID,
		"from_status":  orig.ToStatus,
		"to_status":    orig.FromStatus,
		"rollback":     true,
		"audit_action": "task.status",
	})
}

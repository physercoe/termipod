package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Tasks are the universal human-reviewable work atom (blueprint §6.1). They
// are created either ad-hoc by users (POST /tasks) or materialized by the
// plan-step executor when a step needs human visibility (human_decision
// gates, agent_spawn launches). The `plan_step_id` link and derived
// `source` field let the UI tell the two apart without another round-trip.

type taskIn struct {
	Title        string `json:"title"`
	BodyMD       string `json:"body_md,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	CreatedByID  string `json:"created_by_id,omitempty"`
	MilestoneID  string `json:"milestone_id,omitempty"`
	Status       string `json:"status,omitempty"`
}

type taskOut struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	Title        string `json:"title"`
	BodyMD       string `json:"body_md"`
	Status       string `json:"status"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	CreatedByID  string `json:"created_by_id,omitempty"`
	MilestoneID  string `json:"milestone_id,omitempty"`
	PlanStepID   string `json:"plan_step_id,omitempty"`
	PlanID       string `json:"plan_id,omitempty"`
	Source       string `json:"source"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// taskSourceFor maps a stored plan_step_id to the derived `source` field.
// Empty link → ad_hoc; otherwise the task was materialized by a plan step.
func taskSourceFor(planStepID string) string {
	if planStepID == "" {
		return "ad_hoc"
	}
	return "plan"
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	var in taskIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title required")
		return
	}
	status := in.Status
	if status == "" {
		status = "todo"
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO tasks (id, project_id, parent_task_id, title, body_md, status,
		                   assignee_id, created_by_id, milestone_id, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?,
		        NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
		id, proj, in.ParentTaskID, in.Title, in.BodyMD, status,
		in.AssigneeID, in.CreatedByID, in.MilestoneID, now, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, taskOut{
		ID: id, ProjectID: proj, ParentTaskID: in.ParentTaskID, Title: in.Title,
		BodyMD: in.BodyMD, Status: status, AssigneeID: in.AssigneeID,
		CreatedByID: in.CreatedByID, MilestoneID: in.MilestoneID,
		Source:    "ad_hoc",
		CreatedAt: now, UpdatedAt: now,
	})
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	status := r.URL.Query().Get("status")
	q := `
		SELECT t.id, t.project_id, COALESCE(t.parent_task_id, ''), t.title, t.body_md, t.status,
		       COALESCE(t.assignee_id, ''), COALESCE(t.created_by_id, ''),
		       COALESCE(t.milestone_id, ''), COALESCE(t.plan_step_id, ''),
		       COALESCE(ps.plan_id, ''),
		       t.created_at, t.updated_at
		FROM tasks t
		LEFT JOIN plan_steps ps ON ps.id = t.plan_step_id
		WHERE t.project_id = ?`
	args := []any{proj}
	if status != "" {
		q += " AND t.status = ?"
		args = append(args, status)
	}
	q += " ORDER BY t.created_at DESC"
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []taskOut{}
	for rows.Next() {
		var t taskOut
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Title, &t.BodyMD,
			&t.Status, &t.AssigneeID, &t.CreatedByID, &t.MilestoneID,
			&t.PlanStepID, &t.PlanID, &t.CreatedAt, &t.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		t.Source = taskSourceFor(t.PlanStepID)
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

type taskPatchIn struct {
	Title      *string `json:"title,omitempty"`
	BodyMD     *string `json:"body_md,omitempty"`
	Status     *string `json:"status,omitempty"`
	AssigneeID *string `json:"assignee_id,omitempty"`
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	var in taskPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	sets, args := []string{}, []any{}
	if in.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *in.Title)
	}
	if in.BodyMD != nil {
		sets = append(sets, "body_md = ?")
		args = append(args, *in.BodyMD)
	}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
	}
	if in.AssigneeID != nil {
		sets = append(sets, "assignee_id = NULLIF(?, '')")
		args = append(args, *in.AssigneeID)
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, NowUTC(), proj, id)
	q := "UPDATE tasks SET " + strings.Join(sets, ", ") + " WHERE project_id = ? AND id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	// Note: closing a plan-linked task manually does NOT cascade into the
	// plan step. Executor-driven transitions own the plan_steps table.
	// The `plan_step_id` link surfaced on the task payload lets the UI warn
	// the user when they are about to manually close a plan-materialized
	// task.
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	var t taskOut
	err := s.db.QueryRowContext(r.Context(), `
		SELECT t.id, t.project_id, COALESCE(t.parent_task_id, ''), t.title, t.body_md, t.status,
		       COALESCE(t.assignee_id, ''), COALESCE(t.created_by_id, ''),
		       COALESCE(t.milestone_id, ''), COALESCE(t.plan_step_id, ''),
		       COALESCE(ps.plan_id, ''),
		       t.created_at, t.updated_at
		FROM tasks t
		LEFT JOIN plan_steps ps ON ps.id = t.plan_step_id
		WHERE t.project_id = ? AND t.id = ?`, proj, id).Scan(
		&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Title, &t.BodyMD,
		&t.Status, &t.AssigneeID, &t.CreatedByID, &t.MilestoneID,
		&t.PlanStepID, &t.PlanID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	t.Source = taskSourceFor(t.PlanStepID)
	writeJSON(w, http.StatusOK, t)
}

// materializePlanStepTask inserts a task row linked to the given plan step
// when the step kind warrants human visibility. Returns the new task id
// (or empty string when no task is created) along with any error. This is
// the single choke-point called by the plan-step create path; keeping all
// of the kind → task policy here means the handler stays simple and the
// test has one place to exercise.
//
// Policy (matches the W2 wedge spec):
//   - human_decision: gate phase. Create a `todo` task so the director has
//     an item to tick through in the Kanban.
//   - agent_spawn:    agent-driven phase. Create an `in_progress` task,
//     assignee = the step's agent_id (if set at spawn time), for visibility.
//   - llm_call / shell / mcp_call: deterministic housekeeping. Skip by
//     default to avoid taskboard noise.
func (s *Server) materializePlanStepTask(
	ctx context.Context,
	tx execer,
	projectID, planStepID, stepKind, stepSpec, agentID string,
) (string, error) {
	var title, taskStatus, assignee string
	switch stepKind {
	case "human_decision":
		title = planStepTitle(stepSpec, "Review plan gate")
		taskStatus = "todo"
	case "agent_spawn":
		title = planStepTitle(stepSpec, "Agent run")
		taskStatus = "in_progress"
		assignee = agentID
	default:
		return "", nil
	}
	taskID := NewID()
	now := NowUTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (id, project_id, title, body_md, status,
		                   assignee_id, plan_step_id, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, NULLIF(?, ''), ?, ?, ?)`,
		taskID, projectID, title, taskStatus, assignee, planStepID, now, now,
	); err != nil {
		return "", err
	}
	return taskID, nil
}

// syncPlanStepTaskStatus keeps any task linked to the given plan step in
// lockstep with the step's lifecycle. Called from the plan-step PATCH path
// when `status` moves. The UI can still override a task status mid-flight;
// the next step transition re-aligns it, which is the intended coupling
// (executor owns the truth).
func (s *Server) syncPlanStepTaskStatus(
	ctx context.Context,
	tx execer,
	planStepID, stepStatus string,
) error {
	taskStatus := planStepStatusToTask(stepStatus)
	if taskStatus == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE tasks SET status = ?, updated_at = ?
		WHERE plan_step_id = ?`,
		taskStatus, NowUTC(), planStepID,
	)
	return err
}

// planStepStatusToTask projects plan-step statuses onto the task status
// vocabulary. Returns empty when a transition should be ignored (e.g. the
// executor re-reports the same status).
func planStepStatusToTask(stepStatus string) string {
	switch stepStatus {
	case "running":
		return "in_progress"
	case "completed":
		return "done"
	case "failed":
		return "blocked"
	case "cancelled", "skipped":
		return "done"
	case "blocked":
		return "blocked"
	case "pending":
		return "todo"
	default:
		return ""
	}
}

// planStepTitle pulls a short human label out of the step's spec_json,
// falling back to the provided default. The spec is an opaque object so we
// just look at a couple of common hints without schema enforcement.
func planStepTitle(specJSON, fallback string) string {
	if specJSON == "" {
		return fallback
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(specJSON), &m); err != nil {
		return fallback
	}
	for _, k := range []string{"title", "prompt", "goal", "review", "name"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return fallback
}

// execer is the minimal subset of *sql.Tx / *sql.DB needed by the task
// materialization helpers; lets callers pass either without a wrapper.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

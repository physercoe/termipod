package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/termipod/hub/internal/auth"
)

// Tasks are the universal human-reviewable work atom (blueprint §6.1). They
// are created either ad-hoc by users (POST /tasks) or materialized by the
// plan-step executor when a step needs human visibility (human_decision
// gates, agent_spawn launches) or by `agents.spawn` itself when the
// caller links a fresh task to the spawn (ADR-029 D-2). The `plan_step_id`
// link and derived `source` field let the UI tell the materialised cases
// apart from user-created ones without another round-trip.
//
// Status vocabulary (no CHECK constraint — enforced in handlers + ADR-029):
//
//   - todo         — created, not yet executing.
//   - in_progress  — flipped on spawn or by explicit update.
//   - blocked      — auto-flip on agent crashed/failed; new spawn unblocks.
//   - done         — auto-flip on agent terminated (any cause, ADR-029 D-3).
//   - cancelled    — terminal, explicit human/steward override only.
//                    Auto-derive never enters or leaves this state.

type taskIn struct {
	Title        string `json:"title"`
	BodyMD       string `json:"body_md,omitempty"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	CreatedByID  string `json:"created_by_id,omitempty"`
	MilestoneID  string `json:"milestone_id,omitempty"`
	Status       string `json:"status,omitempty"`
	Priority     string `json:"priority,omitempty"`
}

type taskOut struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	ParentTaskID string `json:"parent_task_id,omitempty"`
	Title        string `json:"title"`
	BodyMD       string `json:"body_md"`
	Status       string `json:"status"`
	Priority     string `json:"priority"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	CreatedByID  string `json:"created_by_id,omitempty"`
	MilestoneID  string `json:"milestone_id,omitempty"`
	PlanStepID   string `json:"plan_step_id,omitempty"`
	PlanID       string `json:"plan_id,omitempty"`
	Source       string `json:"source"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// ADR-029 W10: denormalized lifecycle + attribution fields so the
	// mobile _TaskTile (W8) doesn't have to N+1-lookup against agents
	// just to render a chip. All four are LEFT-JOIN derived and may be
	// empty when the assignee/assigner agent row is missing or the
	// task hasn't started/completed yet.
	StartedAt       string `json:"started_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	ResultSummary   string `json:"result_summary,omitempty"`
	AssigneeHandle  string `json:"assignee_handle,omitempty"`
	AssigneeStatus  string `json:"assignee_status,omitempty"`
	AssignerHandle  string `json:"assigner_handle,omitempty"`
}

// taskPriorities is the closed enum accepted on create/update. Mirrors
// the CHECK constraint in migration 0021 and the mobile TaskPriority
// enum; new values must be added in all three places.
var taskPriorities = map[string]bool{
	"low":    true,
	"med":    true,
	"high":   true,
	"urgent": true,
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
	priority := in.Priority
	if priority == "" {
		priority = "med"
	}
	if !taskPriorities[priority] {
		writeErr(w, http.StatusBadRequest, "priority must be one of low|med|high|urgent")
		return
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO tasks (id, project_id, parent_task_id, title, body_md, status, priority,
		                   assignee_id, created_by_id, milestone_id, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?,
		        NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
		id, proj, in.ParentTaskID, in.Title, in.BodyMD, status, priority,
		in.AssigneeID, in.CreatedByID, in.MilestoneID, now, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), teamFromProject(r), "task.create", "task", id,
		in.Title, map[string]any{
			"project_id": proj,
			"status":     status,
			"priority":   priority,
			"source":     "ad_hoc",
		})
	writeJSON(w, http.StatusCreated, taskOut{
		ID: id, ProjectID: proj, ParentTaskID: in.ParentTaskID, Title: in.Title,
		BodyMD: in.BodyMD, Status: status, Priority: priority,
		AssigneeID:  in.AssigneeID,
		CreatedByID: in.CreatedByID, MilestoneID: in.MilestoneID,
		Source:    "ad_hoc",
		CreatedAt: now, UpdatedAt: now,
	})
}

// teamFromProject is a chi-URL helper: the task handlers live under
// `/v1/teams/{team}/projects/{project}/tasks/...`, so audit needs the
// team id from the same chi router. Returning the empty string makes
// recordAudit a no-op (team_id is the partition key).
func teamFromProject(r *http.Request) string {
	return chi.URLParam(r, "team")
}

// resolveTaskAuditSource maps the request context's actor to the
// `source` axis the mobile Tasks tab + audit timeline use to colour
// the icon: principal-direct, steward-driven, worker-driven, or
// background/system. Detects steward by `agent.kind` per
// feedback_steward_detection_by_kind_not_handle.
func (s *Server) resolveTaskAuditSource(r *http.Request) string {
	_, actorKind, _ := actorFromContext(r.Context())
	switch actorKind {
	case "principal", "user":
		return "principal"
	case "agent":
		tok, ok := auth.FromContext(r.Context())
		if !ok {
			return "agent"
		}
		var scope struct {
			AgentID string `json:"agent_id"`
		}
		_ = json.Unmarshal([]byte(tok.ScopeJSON), &scope)
		if scope.AgentID == "" {
			return "agent"
		}
		var kind string
		if err := s.db.QueryRowContext(r.Context(),
			`SELECT kind FROM agents WHERE id = ?`, scope.AgentID).Scan(&kind); err == nil {
			if strings.HasPrefix(kind, "steward.") {
				return "steward"
			}
			return "worker"
		}
		return "agent"
	default:
		return "system"
	}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	status := r.URL.Query().Get("status")
	priority := r.URL.Query().Get("priority")
	sortMode := r.URL.Query().Get("sort")
	if priority != "" && !taskPriorities[priority] {
		writeErr(w, http.StatusBadRequest, "priority must be one of low|med|high|urgent")
		return
	}
	q := `
		SELECT t.id, t.project_id, COALESCE(t.parent_task_id, ''), t.title, t.body_md, t.status, t.priority,
		       COALESCE(t.assignee_id, ''), COALESCE(t.created_by_id, ''),
		       COALESCE(t.milestone_id, ''), COALESCE(t.plan_step_id, ''),
		       COALESCE(ps.plan_id, ''),
		       t.created_at, t.updated_at,
		       COALESCE(t.started_at, ''), COALESCE(t.completed_at, ''),
		       COALESCE(t.result_summary, ''),
		       COALESCE(ae.handle, ''), COALESCE(ae.status, ''),
		       COALESCE(ar.handle, '')
		FROM tasks t
		LEFT JOIN plan_steps ps ON ps.id = t.plan_step_id
		LEFT JOIN agents ae ON ae.id = t.assignee_id
		LEFT JOIN agents ar ON ar.id = t.created_by_id
		WHERE t.project_id = ?`
	args := []any{proj}
	if status != "" {
		q += " AND t.status = ?"
		args = append(args, status)
	}
	if priority != "" {
		q += " AND t.priority = ?"
		args = append(args, priority)
	}
	// W3 default sort: urgent → low, then newest-first within a bucket.
	// Callers that explicitly want reverse-chronological (e.g. an
	// "activity-style" view) pass `?sort=updated` to opt out.
	if sortMode == "updated" {
		q += " ORDER BY t.updated_at DESC"
	} else {
		q += ` ORDER BY CASE t.priority
			WHEN 'urgent' THEN 3
			WHEN 'high'   THEN 2
			WHEN 'med'    THEN 1
			WHEN 'low'    THEN 0
			ELSE 1 END DESC, t.updated_at DESC`
	}
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
			&t.Status, &t.Priority, &t.AssigneeID, &t.CreatedByID, &t.MilestoneID,
			&t.PlanStepID, &t.PlanID, &t.CreatedAt, &t.UpdatedAt,
			&t.StartedAt, &t.CompletedAt, &t.ResultSummary,
			&t.AssigneeHandle, &t.AssigneeStatus, &t.AssignerHandle); err != nil {
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
	Priority   *string `json:"priority,omitempty"`
	// ResultSummary lets the closing call (typically `tasks.complete`
	// via MCP, or a manual mobile flip-to-done) record what the worker
	// actually did. Stamped into the existing `tasks.result_summary`
	// column. The W2.9 assigner notification reads this so the steward's
	// session shows the summary inline. Empty/nil leaves the prior
	// value alone; an explicit empty string clears it. ADR-029 D-3 +
	// W2.8.
	ResultSummary *string `json:"result_summary,omitempty"`
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	team := teamFromProject(r)
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	var in taskPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Snapshot the prior status if we're about to change it, so the
	// W4 audit row can record from→to. Done in a single round-trip
	// before the UPDATE; cheap (covering index on project_id+status).
	var priorStatus string
	if in.Status != nil {
		_ = s.db.QueryRowContext(r.Context(),
			`SELECT status FROM tasks WHERE project_id = ? AND id = ?`,
			proj, id).Scan(&priorStatus)
	}
	sets, args := []string{}, []any{}
	changedFields := []string{}
	if in.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, *in.Title)
		changedFields = append(changedFields, "title")
	}
	if in.BodyMD != nil {
		sets = append(sets, "body_md = ?")
		args = append(args, *in.BodyMD)
		changedFields = append(changedFields, "body_md")
	}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
		// Stamp completed_at when the patch lands a terminal status, so
		// the mobile tile can render "done 3m ago" / "cancelled 1h ago"
		// without joining audit. Idempotent: re-stamping just refreshes.
		if *in.Status == "done" || *in.Status == "cancelled" {
			sets = append(sets, "completed_at = ?")
			args = append(args, NowUTC())
		}
	}
	if in.AssigneeID != nil {
		sets = append(sets, "assignee_id = NULLIF(?, '')")
		args = append(args, *in.AssigneeID)
		changedFields = append(changedFields, "assignee_id")
	}
	if in.Priority != nil {
		if !taskPriorities[*in.Priority] {
			writeErr(w, http.StatusBadRequest, "priority must be one of low|med|high|urgent")
			return
		}
		sets = append(sets, "priority = ?")
		args = append(args, *in.Priority)
		changedFields = append(changedFields, "priority")
	}
	if in.ResultSummary != nil {
		sets = append(sets, "result_summary = NULLIF(?, '')")
		args = append(args, *in.ResultSummary)
		changedFields = append(changedFields, "result_summary")
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
	// ADR-029 D-4 W4: split audit. Status change is its own row so the
	// timeline can render the from→to chip; other fields land as a
	// generic `task.update` with the changed_fields list.
	source := s.resolveTaskAuditSource(r)
	if in.Status != nil && *in.Status != priorStatus {
		s.recordAudit(r.Context(), team, "task.status", "task", id,
			priorStatus+" → "+*in.Status,
			map[string]any{
				"from":   priorStatus,
				"to":     *in.Status,
				"source": source,
			})
		// W2.9: when the manual flip lands a terminal state, push a
		// system message into the assigner's chat so the steward
		// doesn't have to poll. notifyTaskAssigner gates on terminal
		// toStatus itself; non-terminal status changes (e.g.
		// todo→in_progress) silently no-op.
		s.notifyTaskAssigner(r.Context(), team, id, priorStatus, *in.Status)
	}
	if len(changedFields) > 0 {
		s.recordAudit(r.Context(), team, "task.update", "task", id,
			"update "+strings.Join(changedFields, ","),
			map[string]any{
				"changed_fields": changedFields,
				"source":         source,
			})
	}
	// Note: closing a plan-linked task manually does NOT cascade into the
	// plan step. Executor-driven transitions own the plan_steps table.
	// The `plan_step_id` link surfaced on the task payload lets the UI warn
	// the user when they are about to manually close a plan-materialized
	// task.
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteTask drops a task row. Per ADR-029 D-7 this is the
// "I created the task in error" escape hatch; the audit trail
// preserves the task.delete row but the task itself is gone. Cf.
// `tasks.update status='cancelled'` which keeps the row.
// ON DELETE SET NULL on agent_spawns.task_id means any spawn that
// drove the task survives the delete with task_id NULL.
func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	team := teamFromProject(r)
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	// Capture the title before the row goes away so the audit summary
	// is readable later. Missing title (NULL or row already gone) is
	// fine — the audit row still resolves via target_id.
	var title string
	_ = s.db.QueryRowContext(r.Context(),
		`SELECT title FROM tasks WHERE project_id = ? AND id = ?`,
		proj, id).Scan(&title)
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM tasks WHERE project_id = ? AND id = ?`, proj, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	s.recordAudit(r.Context(), team, "task.delete", "task", id, title,
		map[string]any{
			"project_id": proj,
			"source":     s.resolveTaskAuditSource(r),
		})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	id := chi.URLParam(r, "task")
	var t taskOut
	err := s.db.QueryRowContext(r.Context(), `
		SELECT t.id, t.project_id, COALESCE(t.parent_task_id, ''), t.title, t.body_md, t.status, t.priority,
		       COALESCE(t.assignee_id, ''), COALESCE(t.created_by_id, ''),
		       COALESCE(t.milestone_id, ''), COALESCE(t.plan_step_id, ''),
		       COALESCE(ps.plan_id, ''),
		       t.created_at, t.updated_at,
		       COALESCE(t.started_at, ''), COALESCE(t.completed_at, ''),
		       COALESCE(t.result_summary, ''),
		       COALESCE(ae.handle, ''), COALESCE(ae.status, ''),
		       COALESCE(ar.handle, '')
		FROM tasks t
		LEFT JOIN plan_steps ps ON ps.id = t.plan_step_id
		LEFT JOIN agents ae ON ae.id = t.assignee_id
		LEFT JOIN agents ar ON ar.id = t.created_by_id
		WHERE t.project_id = ? AND t.id = ?`, proj, id).Scan(
		&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Title, &t.BodyMD,
		&t.Status, &t.Priority, &t.AssigneeID, &t.CreatedByID, &t.MilestoneID,
		&t.PlanStepID, &t.PlanID, &t.CreatedAt, &t.UpdatedAt,
		&t.StartedAt, &t.CompletedAt, &t.ResultSummary,
		&t.AssigneeHandle, &t.AssigneeStatus, &t.AssignerHandle)
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
) (taskID, title string, err error) {
	var taskStatus, assignee string
	switch stepKind {
	case "human_decision":
		title = planStepTitle(stepSpec, "Review plan gate")
		taskStatus = "todo"
	case "agent_spawn":
		title = planStepTitle(stepSpec, "Agent run")
		taskStatus = "in_progress"
		assignee = agentID
	default:
		return "", "", nil
	}
	taskID = NewID()
	now := NowUTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (id, project_id, title, body_md, status,
		                   assignee_id, plan_step_id, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, NULLIF(?, ''), ?, ?, ?)`,
		taskID, projectID, title, taskStatus, assignee, planStepID, now, now,
	); err != nil {
		return "", "", err
	}
	return taskID, title, nil
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

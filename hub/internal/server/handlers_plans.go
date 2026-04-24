package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Plans and plan_steps (blueprint §6.2). Plans are scoped to a project; the
// team URL segment is validated by joining through projects.team_id. Ad-hoc
// plans without a template_id are allowed.

var planStatusValues = map[string]bool{
	"draft":     true,
	"ready":     true,
	"running":   true,
	"completed": true,
	"failed":    true,
	"cancelled": true,
}

var planStepKinds = map[string]bool{
	"agent_spawn":    true,
	"llm_call":       true,
	"shell":          true,
	"mcp_call":       true,
	"human_decision": true,
}

type planIn struct {
	ProjectID  string          `json:"project_id"`
	TemplateID string          `json:"template_id,omitempty"`
	Version    int             `json:"version,omitempty"`
	SpecJSON   json.RawMessage `json:"spec_json,omitempty"`
}

type planOut struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	TemplateID  string          `json:"template_id,omitempty"`
	Version     int             `json:"version"`
	SpecJSON    json.RawMessage `json:"spec_json"`
	Status      string          `json:"status"`
	CreatedAt   string          `json:"created_at"`
	StartedAt   *string         `json:"started_at,omitempty"`
	CompletedAt *string         `json:"completed_at,omitempty"`
}

type planPatchIn struct {
	Status   *string         `json:"status,omitempty"`
	SpecJSON json.RawMessage `json:"spec_json,omitempty"`
}

type planStepIn struct {
	PhaseIdx int             `json:"phase_idx"`
	StepIdx  int             `json:"step_idx"`
	Kind     string          `json:"kind"`
	SpecJSON json.RawMessage `json:"spec_json,omitempty"`
}

type planStepOut struct {
	ID             string          `json:"id"`
	PlanID         string          `json:"plan_id"`
	PhaseIdx       int             `json:"phase_idx"`
	StepIdx        int             `json:"step_idx"`
	Kind           string          `json:"kind"`
	SpecJSON       json.RawMessage `json:"spec_json"`
	Status         string          `json:"status"`
	StartedAt      *string         `json:"started_at,omitempty"`
	CompletedAt    *string         `json:"completed_at,omitempty"`
	InputRefsJSON  json.RawMessage `json:"input_refs_json"`
	OutputRefsJSON json.RawMessage `json:"output_refs_json"`
	AgentID        string          `json:"agent_id,omitempty"`
}

type planStepPatchIn struct {
	Status         *string         `json:"status,omitempty"`
	StartedAt      *string         `json:"started_at,omitempty"`
	CompletedAt    *string         `json:"completed_at,omitempty"`
	InputRefsJSON  json.RawMessage `json:"input_refs_json,omitempty"`
	OutputRefsJSON json.RawMessage `json:"output_refs_json,omitempty"`
	AgentID        *string         `json:"agent_id,omitempty"`
}

// projectBelongsToTeam reports whether the given project exists and is owned
// by the team. Used to reject cross-team access via a crafted plan URL.
func (s *Server) projectBelongsToTeam(r *http.Request, team, project string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COUNT(1) FROM projects WHERE id = ? AND team_id = ?`,
		project, team).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// planProjectForTeam returns the plan's project_id if the plan belongs to a
// project in the given team, else ("", sql.ErrNoRows).
func (s *Server) planProjectForTeam(r *http.Request, team, plan string) (string, error) {
	var projectID string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT p.project_id
		FROM plans p
		JOIN projects pr ON pr.id = p.project_id
		WHERE p.id = ? AND pr.team_id = ?`,
		plan, team).Scan(&projectID)
	return projectID, err
}

func (s *Server) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in planIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ProjectID == "" {
		writeErr(w, http.StatusBadRequest, "project_id required")
		return
	}
	ok, err := s.projectBelongsToTeam(r, team, in.ProjectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	version := in.Version
	if version <= 0 {
		version = 1
	}
	spec := normalizeObjectJSON(in.SpecJSON)
	id := NewID()
	now := NowUTC()
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO plans (id, project_id, template_id, version, spec_json, status, created_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, 'draft', ?)`,
		id, in.ProjectID, in.TemplateID, version, spec, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "plan.create", "plan", id,
		"create plan "+id,
		map[string]any{"project_id": in.ProjectID, "template_id": in.TemplateID, "version": version},
	)
	writeJSON(w, http.StatusCreated, planOut{
		ID: id, ProjectID: in.ProjectID, TemplateID: in.TemplateID,
		Version: version, SpecJSON: json.RawMessage(spec),
		Status: "draft", CreatedAt: now,
	})
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := r.URL.Query().Get("project")
	status := r.URL.Query().Get("status")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	q := `
		SELECT p.id, p.project_id, COALESCE(p.template_id, ''), p.version,
		       p.spec_json, p.status, p.created_at, p.started_at, p.completed_at
		FROM plans p
		JOIN projects pr ON pr.id = p.project_id
		WHERE pr.team_id = ?`
	args := []any{team}
	if project != "" {
		q += " AND p.project_id = ?"
		args = append(args, project)
	}
	if status != "" {
		q += " AND p.status = ?"
		args = append(args, status)
	}
	q += " ORDER BY p.created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []planOut{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	plan := chi.URLParam(r, "plan")
	row := s.db.QueryRowContext(r.Context(), `
		SELECT p.id, p.project_id, COALESCE(p.template_id, ''), p.version,
		       p.spec_json, p.status, p.created_at, p.started_at, p.completed_at
		FROM plans p
		JOIN projects pr ON pr.id = p.project_id
		WHERE p.id = ? AND pr.team_id = ?`, plan, team)
	p, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "plan not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	plan := chi.URLParam(r, "plan")
	var in planPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := s.planProjectForTeam(r, team, plan); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "plan not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	sets, args := []string{}, []any{}
	auditMeta := map[string]any{}
	if in.Status != nil {
		if !planStatusValues[*in.Status] {
			writeErr(w, http.StatusBadRequest, "invalid status")
			return
		}
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
		auditMeta["status"] = *in.Status
		// Stamp lifecycle timestamps when entering terminal transitions.
		switch *in.Status {
		case "running":
			sets = append(sets, "started_at = COALESCE(started_at, ?)")
			args = append(args, NowUTC())
		case "completed", "failed", "cancelled":
			sets = append(sets, "completed_at = COALESCE(completed_at, ?)")
			args = append(args, NowUTC())
		}
	}
	if len(in.SpecJSON) > 0 {
		sets = append(sets, "spec_json = ?")
		args = append(args, normalizeObjectJSON(in.SpecJSON))
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	args = append(args, plan)
	q := "UPDATE plans SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.db.ExecContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "plan not found")
		return
	}
	summary := "update plan " + plan
	if in.Status != nil {
		summary = "update plan " + plan + " \u2192 " + *in.Status
	}
	s.recordAudit(r.Context(), team, "plan.update", "plan", plan, summary, auditMeta)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCreatePlanStep(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	plan := chi.URLParam(r, "plan")
	var in planStepIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !planStepKinds[in.Kind] {
		writeErr(w, http.StatusBadRequest, "invalid kind")
		return
	}
	projectID, err := s.planProjectForTeam(r, team, plan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "plan not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	spec := normalizeObjectJSON(in.SpecJSON)
	id := NewID()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO plan_steps (id, plan_id, phase_idx, step_idx, kind, spec_json, status)
		VALUES (?, ?, ?, ?, ?, ?, 'pending')`,
		id, plan, in.PhaseIdx, in.StepIdx, in.Kind, spec); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// W2: plan steps that need human visibility materialize a task row so
	// the Kanban shows plan work alongside ad-hoc tasks. Deterministic
	// steps (llm_call, shell, mcp_call) skip task creation — see
	// materializePlanStepTask for the policy matrix.
	if _, err := s.materializePlanStepTask(r.Context(), tx, projectID, id, in.Kind, spec, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, planStepOut{
		ID: id, PlanID: plan, PhaseIdx: in.PhaseIdx, StepIdx: in.StepIdx,
		Kind: in.Kind, SpecJSON: json.RawMessage(spec), Status: "pending",
		InputRefsJSON:  json.RawMessage("[]"),
		OutputRefsJSON: json.RawMessage("[]"),
	})
}

func (s *Server) handleListPlanSteps(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	plan := chi.URLParam(r, "plan")
	if _, err := s.planProjectForTeam(r, team, plan); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "plan not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, plan_id, phase_idx, step_idx, kind, spec_json, status,
		       started_at, completed_at, input_refs_json, output_refs_json,
		       COALESCE(agent_id, '')
		FROM plan_steps WHERE plan_id = ?
		ORDER BY phase_idx, step_idx`, plan)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []planStepOut{}
	for rows.Next() {
		step, err := scanPlanStep(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, step)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdatePlanStep(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	plan := chi.URLParam(r, "plan")
	step := chi.URLParam(r, "step")
	var in planStepPatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := s.planProjectForTeam(r, team, plan); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "plan not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	sets, args := []string{}, []any{}
	auditMeta := map[string]any{}
	if in.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *in.Status)
		auditMeta["status"] = *in.Status
	}
	if in.StartedAt != nil {
		sets = append(sets, "started_at = NULLIF(?, '')")
		args = append(args, *in.StartedAt)
	}
	if in.CompletedAt != nil {
		sets = append(sets, "completed_at = NULLIF(?, '')")
		args = append(args, *in.CompletedAt)
	}
	if len(in.InputRefsJSON) > 0 {
		sets = append(sets, "input_refs_json = ?")
		args = append(args, normalizeArrayJSON(in.InputRefsJSON))
	}
	if len(in.OutputRefsJSON) > 0 {
		sets = append(sets, "output_refs_json = ?")
		args = append(args, normalizeArrayJSON(in.OutputRefsJSON))
	}
	if in.AgentID != nil {
		sets = append(sets, "agent_id = NULLIF(?, '')")
		args = append(args, *in.AgentID)
		auditMeta["agent_id"] = *in.AgentID
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no fields to update")
		return
	}
	args = append(args, step, plan)
	q := "UPDATE plan_steps SET " + strings.Join(sets, ", ") + " WHERE id = ? AND plan_id = ?"
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "plan_step not found")
		return
	}
	// W2: keep any linked task in lockstep with the plan step. The executor
	// owns the source of truth; the task row mirrors status so the Kanban
	// reflects reality and propagates the steward assignee once the agent
	// has been spawned.
	if in.Status != nil {
		if err := s.syncPlanStepTaskStatus(r.Context(), tx, step, *in.Status); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if in.AgentID != nil && *in.AgentID != "" {
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE tasks SET assignee_id = ?, updated_at = ?
			WHERE plan_step_id = ?`,
			*in.AgentID, NowUTC(), step); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	summary := "update plan_step " + step
	if in.Status != nil {
		summary = "update plan_step " + step + " \u2192 " + *in.Status
	}
	s.recordAudit(r.Context(), team, "plan_step.update", "plan_step", step, summary, auditMeta)
	w.WriteHeader(http.StatusNoContent)
}

// scanSource matches the Scan signature of both *sql.Row and *sql.Rows so
// the row-extractor helpers can serve both list and get paths.
type scanSource interface {
	Scan(dest ...any) error
}

func scanPlan(src scanSource) (planOut, error) {
	var p planOut
	var spec string
	var started, completed sql.NullString
	if err := src.Scan(
		&p.ID, &p.ProjectID, &p.TemplateID, &p.Version,
		&spec, &p.Status, &p.CreatedAt, &started, &completed,
	); err != nil {
		return p, err
	}
	p.SpecJSON = json.RawMessage(spec)
	if started.Valid {
		p.StartedAt = &started.String
	}
	if completed.Valid {
		p.CompletedAt = &completed.String
	}
	return p, nil
}

func scanPlanStep(src scanSource) (planStepOut, error) {
	var s planStepOut
	var spec, inputRefs, outputRefs string
	var started, completed sql.NullString
	if err := src.Scan(
		&s.ID, &s.PlanID, &s.PhaseIdx, &s.StepIdx, &s.Kind,
		&spec, &s.Status, &started, &completed,
		&inputRefs, &outputRefs, &s.AgentID,
	); err != nil {
		return s, err
	}
	s.SpecJSON = json.RawMessage(spec)
	s.InputRefsJSON = json.RawMessage(inputRefs)
	s.OutputRefsJSON = json.RawMessage(outputRefs)
	if started.Valid {
		s.StartedAt = &started.String
	}
	if completed.Valid {
		s.CompletedAt = &completed.String
	}
	return s, nil
}

// normalizeObjectJSON returns a valid JSON object literal; empty or invalid
// input collapses to "{}" so the column default stays meaningful.
func normalizeObjectJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "{}"
	}
	return string(raw)
}

// normalizeArrayJSON mirrors normalizeObjectJSON for list-shaped columns.
func normalizeArrayJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "[]"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "[]"
	}
	return string(raw)
}

package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Blueprint §6.3: schedules trigger a plan from a template. They never spawn
// agents directly (§7 forbidden pattern). On fire, the scheduler creates a
// plan row with status='ready'; host-runner's plan executor (Phase 1) picks
// it up.

type scheduleIn struct {
	ProjectID      string          `json:"project_id"`
	TemplateID     string          `json:"template_id"`
	TriggerKind    string          `json:"trigger_kind"`
	CronExpr       string          `json:"cron_expr,omitempty"`
	ParametersJSON json.RawMessage `json:"parameters_json,omitempty"`
	Enabled        *bool           `json:"enabled,omitempty"`
}

type scheduleOut struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	TemplateID     string          `json:"template_id"`
	TriggerKind    string          `json:"trigger_kind"`
	CronExpr       string          `json:"cron_expr,omitempty"`
	ParametersJSON json.RawMessage `json:"parameters_json"`
	Enabled        bool            `json:"enabled"`
	NextRunAt      *string         `json:"next_run_at,omitempty"`
	LastRunAt      *string         `json:"last_run_at,omitempty"`
	LastPlanID     *string         `json:"last_plan_id,omitempty"`
	CreatedAt      string          `json:"created_at"`
}

func validTriggerKind(k string) bool {
	return k == "cron" || k == "manual" || k == "on_create"
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in scheduleIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ProjectID == "" || in.TemplateID == "" || in.TriggerKind == "" {
		writeErr(w, http.StatusBadRequest, "project_id, template_id, trigger_kind required")
		return
	}
	if !validTriggerKind(in.TriggerKind) {
		writeErr(w, http.StatusBadRequest, "trigger_kind must be cron|manual|on_create")
		return
	}
	if in.TriggerKind == "cron" && in.CronExpr == "" {
		writeErr(w, http.StatusBadRequest, "cron_expr required when trigger_kind='cron'")
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

	paramsJSON := "{}"
	if len(in.ParametersJSON) > 0 {
		paramsJSON = string(in.ParametersJSON)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	id := NewID()
	now := NowUTC()
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO schedules (
			id, project_id, template_id, trigger_kind, cron_expr,
			parameters_json, enabled, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.ProjectID, in.TemplateID, in.TriggerKind,
		nullIfEmpty(in.CronExpr), paramsJSON, boolToInt(enabled), now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}

	// Only cron schedules attach to the running cron engine; manual and
	// on_create are fired explicitly (by /run endpoint or project create).
	if enabled && in.TriggerKind == "cron" && s.sched != nil {
		if err := s.sched.Register(id, team, in.CronExpr); err != nil {
			_, _ = s.db.ExecContext(r.Context(),
				`DELETE FROM schedules WHERE id = ?`, id)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.recordAudit(r.Context(), team, "schedule.create", "schedule", id,
		"create schedule for project "+in.ProjectID,
		map[string]any{
			"project_id":   in.ProjectID,
			"template_id":  in.TemplateID,
			"trigger_kind": in.TriggerKind,
			"cron_expr":    in.CronExpr,
			"enabled":      enabled,
		},
	)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "created_at": now, "enabled": enabled,
	})
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := r.URL.Query().Get("project")
	args := []any{team}
	q := `SELECT s.id, s.project_id, s.template_id, s.trigger_kind,
	             COALESCE(s.cron_expr, ''), s.parameters_json, s.enabled,
	             s.next_run_at, s.last_run_at, s.last_plan_id, s.created_at
	        FROM schedules s
	        JOIN projects p ON p.id = s.project_id
	       WHERE p.team_id = ?`
	if project != "" {
		q += ` AND s.project_id = ?`
		args = append(args, project)
	}
	q += ` ORDER BY s.created_at`
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []scheduleOut{}
	for rows.Next() {
		var sch scheduleOut
		var params string
		var enabled int
		var nextAt, lastAt, lastPlan sql.NullString
		if err := rows.Scan(
			&sch.ID, &sch.ProjectID, &sch.TemplateID, &sch.TriggerKind,
			&sch.CronExpr, &params, &enabled,
			&nextAt, &lastAt, &lastPlan, &sch.CreatedAt,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		sch.ParametersJSON = json.RawMessage(params)
		sch.Enabled = enabled == 1
		if nextAt.Valid {
			sch.NextRunAt = &nextAt.String
		}
		if lastAt.Valid {
			sch.LastRunAt = &lastAt.String
		}
		if lastPlan.Valid {
			sch.LastPlanID = &lastPlan.String
		}
		out = append(out, sch)
	}
	writeJSON(w, http.StatusOK, out)
}

type schedulePatchIn struct {
	Enabled        *bool           `json:"enabled,omitempty"`
	CronExpr       *string         `json:"cron_expr,omitempty"`
	ParametersJSON json.RawMessage `json:"parameters_json,omitempty"`
}

func (s *Server) handlePatchSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "schedule")
	var in schedulePatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	var (
		triggerKind, cronExpr string
		enabled               int
	)
	err := s.db.QueryRowContext(r.Context(), `
		SELECT s.trigger_kind, COALESCE(s.cron_expr, ''), s.enabled
		  FROM schedules s JOIN projects p ON p.id = s.project_id
		 WHERE s.id = ? AND p.team_id = ?`, id, team).
		Scan(&triggerKind, &cronExpr, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	sets := []string{}
	args := []any{}
	if in.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*in.Enabled))
		enabled = boolToInt(*in.Enabled)
	}
	if in.CronExpr != nil {
		if triggerKind != "cron" {
			writeErr(w, http.StatusBadRequest, "cron_expr only valid for trigger_kind='cron'")
			return
		}
		sets = append(sets, "cron_expr = ?")
		args = append(args, nullIfEmpty(*in.CronExpr))
		cronExpr = *in.CronExpr
	}
	if len(in.ParametersJSON) > 0 {
		sets = append(sets, "parameters_json = ?")
		args = append(args, string(in.ParametersJSON))
	}
	if len(sets) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	args = append(args, id)
	q := `UPDATE schedules SET ` + joinCSV(sets) + ` WHERE id = ?`
	if _, err := s.db.ExecContext(r.Context(), q, args...); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if triggerKind == "cron" && s.sched != nil {
		if enabled == 1 && cronExpr != "" {
			_ = s.sched.Register(id, team, cronExpr)
		} else {
			s.sched.Unregister(id)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "schedule")
	res, err := s.db.ExecContext(r.Context(), `
		DELETE FROM schedules
		 WHERE id = ? AND project_id IN (SELECT id FROM projects WHERE team_id = ?)`,
		id, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	if s.sched != nil {
		s.sched.Unregister(id)
	}
	s.recordAudit(r.Context(), team, "schedule.delete", "schedule", id,
		"delete schedule", nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleRunSchedule manually fires a schedule — equivalent to a cron tick but
// user-initiated. Works for any trigger_kind.
func (s *Server) handleRunSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "schedule")
	var n int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(1) FROM schedules s JOIN projects p ON p.id = s.project_id
		 WHERE s.id = ? AND p.team_id = ?`, id, team).Scan(&n)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	planID, err := s.fireSchedule(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "schedule.run", "schedule", id,
		"run schedule", map[string]any{"plan_id": planID})
	writeJSON(w, http.StatusOK, map[string]any{"plan_id": planID})
}

func joinCSV(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

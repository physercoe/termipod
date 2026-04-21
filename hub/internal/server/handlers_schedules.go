package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type scheduleIn struct {
	Name     string  `json:"name"`
	CronExpr string  `json:"cron_expr"`
	Spawn    spawnIn `json:"spawn"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

type scheduleOut struct {
	ID            string  `json:"id"`
	TeamID        string  `json:"team_id"`
	Name          string  `json:"name"`
	CronExpr      string  `json:"cron_expr"`
	Enabled       bool    `json:"enabled"`
	LastRunAt     *string `json:"last_run_at,omitempty"`
	LastRunStatus string  `json:"last_run_status,omitempty"`
	NextRunAt     *string `json:"next_run_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
	Spawn         json.RawMessage `json:"spawn"`
}

func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in scheduleIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Name == "" || in.CronExpr == "" || in.Spawn.ChildHandle == "" {
		writeErr(w, http.StatusBadRequest, "name, cron_expr, spawn.child_handle required")
		return
	}
	specJSON, _ := json.Marshal(in.Spawn)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO agent_schedules (
			id, team_id, name, cron_expr, spawn_spec_yaml, enabled, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, team, in.Name, in.CronExpr, string(specJSON), boolToInt(enabled), now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if enabled && s.sched != nil {
		if err := s.sched.Register(id, team, in.CronExpr, string(specJSON)); err != nil {
			// Register failure is user-visible: bad cron expression.
			_, _ = s.db.ExecContext(r.Context(),
				`DELETE FROM agent_schedules WHERE id = ?`, id)
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.recordAudit(r.Context(), team, "schedule.create", "schedule", id,
		"create schedule "+in.Name,
		map[string]any{"name": in.Name, "cron_expr": in.CronExpr, "enabled": enabled},
	)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "created_at": now, "enabled": enabled,
	})
}

func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, team_id, name, cron_expr, spawn_spec_yaml, enabled,
		       last_run_at, COALESCE(last_run_status, ''),
		       next_run_at, created_at
		FROM agent_schedules WHERE team_id = ? ORDER BY created_at`, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []scheduleOut{}
	for rows.Next() {
		var sch scheduleOut
		var spec string
		var enabled int
		var lastAt, nextAt sql.NullString
		if err := rows.Scan(&sch.ID, &sch.TeamID, &sch.Name, &sch.CronExpr,
			&spec, &enabled, &lastAt, &sch.LastRunStatus, &nextAt, &sch.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		sch.Spawn = json.RawMessage(spec)
		sch.Enabled = enabled == 1
		if lastAt.Valid {
			sch.LastRunAt = &lastAt.String
		}
		if nextAt.Valid {
			sch.NextRunAt = &nextAt.String
		}
		out = append(out, sch)
	}
	writeJSON(w, http.StatusOK, out)
}

type schedulePatchIn struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (s *Server) handlePatchSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "schedule")
	var in schedulePatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "enabled required")
		return
	}
	var expr, spec string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT cron_expr, spawn_spec_yaml FROM agent_schedules WHERE team_id = ? AND id = ?`,
		team, id).Scan(&expr, &spec)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE agent_schedules SET enabled = ? WHERE team_id = ? AND id = ?`,
		boolToInt(*in.Enabled), team, id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.sched != nil {
		if *in.Enabled {
			_ = s.sched.Register(id, team, expr, spec)
		} else {
			s.sched.Unregister(id)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "schedule")
	var name string
	_ = s.db.QueryRowContext(r.Context(),
		`SELECT name FROM agent_schedules WHERE team_id = ? AND id = ?`,
		team, id).Scan(&name)
	res, err := s.db.ExecContext(r.Context(),
		`DELETE FROM agent_schedules WHERE team_id = ? AND id = ?`, team, id)
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
	summary := "delete schedule"
	if name != "" {
		summary = "delete schedule " + name
	}
	s.recordAudit(r.Context(), team, "schedule.delete", "schedule", id,
		summary, map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Run "extras" digest storage — the trackio sibling tables beside the scalar
// metric curves (config / system_metrics / alerts). Same data-ownership law as
// run_metrics (§4): the hub stores only the compact digest; the host-runner
// reads the local trackio store and PUTs it here.
//
//	PUT/GET /v1/teams/{team}/runs/{run}/config
//	  body: {"config": { ...hyperparameters... }}
//	PUT/GET /v1/teams/{team}/runs/{run}/system_metrics
//	  body: {"metrics":[{"name","points","sample_count","last_step","last_value"}]}
//	  (identical shape to /metrics; x-axis is a sample ordinal, not a step)
//	PUT/GET /v1/teams/{team}/runs/{run}/alerts
//	  body: {"alerts":[{"title","text","level","step","ts","alert_id"}]}

// runInTeam reports whether runID belongs to team (via its project). Shared by
// every run-scoped extras handler so the team-scope check lives in one place.
func (s *Server) runInTeam(r *http.Request, runID, team string) (bool, error) {
	var found string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT r.id FROM runs r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = ? AND p.team_id = ?`, runID, team).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ---- config ----

type runConfigPutIn struct {
	Config json.RawMessage `json:"config"`
}

func (s *Server) handlePutRunConfig(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in runConfigPutIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if len(in.Config) == 0 || !json.Valid(in.Config) {
		writeErr(w, http.StatusBadRequest, "config must be a valid JSON object")
		return
	}

	ok, err := s.runInTeam(r, runID, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	// One row per run — upsert.
	if _, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO run_config (run_id, config_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			config_json = excluded.config_json,
			updated_at  = excluded.updated_at`,
		runID, string(in.Config), NowUTC()); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetRunConfig(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	ok, err := s.runInTeam(r, runID, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	var configJSON, updatedAt string
	err = s.db.QueryRowContext(r.Context(),
		`SELECT config_json, updated_at FROM run_config WHERE run_id = ?`,
		runID).Scan(&configJSON, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// No config logged yet — a clean, empty 200 (not 404; the run exists).
		writeJSON(w, http.StatusOK, map[string]any{"config": nil})
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":     json.RawMessage(configJSON),
		"updated_at": updatedAt,
	})
}

// ---- system metrics (same wire shape as /metrics) ----

func (s *Server) handlePutRunSystemMetrics(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in runMetricsPutIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	for _, m := range in.Metrics {
		if m.Name == "" {
			writeErr(w, http.StatusBadRequest, "metrics[].name required")
			return
		}
		if len(m.Points) == 0 {
			writeErr(w, http.StatusBadRequest, "metrics[].points required")
			return
		}
		if !json.Valid(m.Points) {
			writeErr(w, http.StatusBadRequest, "metrics[].points must be valid JSON")
			return
		}
	}

	ok, err := s.runInTeam(r, runID, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	tx, err := s.writeDB.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM run_system_metrics WHERE run_id = ?`, runID); err != nil {
		s.writeDBErr(w, err)
		return
	}
	now := NowUTC()
	for _, m := range in.Metrics {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO run_system_metrics (id, run_id, metric_name, points_json,
			                                sample_count, last_step, last_value, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			NewID(), runID, m.Name, string(m.Points),
			m.SampleCount, nullableInt64(m.LastStep), nullableFloat64(m.LastValue),
			now); err != nil {
			s.writeDBErr(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.Metrics)})
}

func (s *Server) handleGetRunSystemMetrics(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	ok, err := s.runInTeam(r, runID, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT metric_name, points_json, sample_count, last_step, last_value, updated_at
		FROM run_system_metrics WHERE run_id = ? ORDER BY metric_name`, runID)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()

	out := []metricPointsOut{}
	for rows.Next() {
		var (
			name, pointsJSON, updatedAt string
			sampleCount                 int64
			lastStep                    sql.NullInt64
			lastValue                   sql.NullFloat64
		)
		if err := rows.Scan(&name, &pointsJSON, &sampleCount, &lastStep, &lastValue, &updatedAt); err != nil {
			s.writeDBErr(w, err)
			return
		}
		row := metricPointsOut{
			Name:        name,
			Points:      json.RawMessage(pointsJSON),
			SampleCount: sampleCount,
			UpdatedAt:   updatedAt,
		}
		if lastStep.Valid {
			v := lastStep.Int64
			row.LastStep = &v
		}
		if lastValue.Valid {
			v := lastValue.Float64
			row.LastValue = &v
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- alerts ----

type alertIn struct {
	Title   string `json:"title"`
	Text    string `json:"text,omitempty"`
	Level   string `json:"level,omitempty"`
	Step    *int64 `json:"step,omitempty"`
	TS      string `json:"ts,omitempty"`
	AlertID string `json:"alert_id,omitempty"`
}

type runAlertsPutIn struct {
	Alerts []alertIn `json:"alerts"`
}

type alertOut struct {
	Title     string `json:"title"`
	Text      string `json:"text,omitempty"`
	Level     string `json:"level,omitempty"`
	Step      *int64 `json:"step,omitempty"`
	TS        string `json:"ts,omitempty"`
	AlertID   string `json:"alert_id,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Server) handlePutRunAlerts(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in runAlertsPutIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	for _, a := range in.Alerts {
		if a.Title == "" {
			writeErr(w, http.StatusBadRequest, "alerts[].title required")
			return
		}
	}

	ok, err := s.runInTeam(r, runID, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	tx, err := s.writeDB.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM run_alerts WHERE run_id = ?`, runID); err != nil {
		s.writeDBErr(w, err)
		return
	}
	now := NowUTC()
	for _, a := range in.Alerts {
		level := a.Level
		if level == "" {
			level = "warn"
		}
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO run_alerts (id, run_id, title, body, level, step, ts, alert_id, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			NewID(), runID, a.Title, nullableStr(a.Text), level,
			nullableInt64(a.Step), nullableStr(a.TS), nullableStr(a.AlertID),
			now); err != nil {
			s.writeDBErr(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.Alerts)})
}

func (s *Server) handleGetRunAlerts(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	ok, err := s.runInTeam(r, runID, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT title, body, level, step, ts, alert_id, updated_at
		FROM run_alerts WHERE run_id = ?
		ORDER BY (ts IS NULL), ts ASC, id ASC`, runID)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()

	out := []alertOut{}
	for rows.Next() {
		var (
			title, level, updatedAt string
			body, ts, alertID       sql.NullString
			step                    sql.NullInt64
		)
		if err := rows.Scan(&title, &body, &level, &step, &ts, &alertID, &updatedAt); err != nil {
			s.writeDBErr(w, err)
			return
		}
		row := alertOut{
			Title:     title,
			Text:      body.String,
			Level:     level,
			TS:        ts.String,
			AlertID:   alertID.String,
			UpdatedAt: updatedAt,
		}
		if step.Valid {
			v := step.Int64
			row.Step = &v
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// nullableStr stores "" as SQL NULL so empty optional fields don't round-trip
// as empty strings.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

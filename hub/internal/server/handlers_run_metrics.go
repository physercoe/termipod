package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// P3.1a — Run metric digest storage.
//
// The hub does not own bulk time-series (blueprint §4 data-ownership law).
// Host-runners poll their local trackio endpoint, downsample each curve to
// ≤~100 points, and PUT a compact digest here so the mobile app can render
// a sparkline without fetching the whole series.
//
//   PUT /v1/teams/{team}/runs/{run}/metrics
//     body: {"metrics":[{"name":"loss","points":[[step,value],...],
//                        "sample_count":N,"last_step":N,"last_value":V}, ...]}
//     Replaces the run's entire digest atomically.
//
//   GET /v1/teams/{team}/runs/{run}/metrics
//     Returns all digest rows for the run.

type metricPointsIn struct {
	Name        string          `json:"name"`
	Points      json.RawMessage `json:"points"`
	SampleCount int64           `json:"sample_count"`
	LastStep    *int64          `json:"last_step,omitempty"`
	LastValue   *float64        `json:"last_value,omitempty"`
}

type runMetricsPutIn struct {
	Metrics []metricPointsIn `json:"metrics"`
}

type metricPointsOut struct {
	Name        string          `json:"name"`
	Points      json.RawMessage `json:"points"`
	SampleCount int64           `json:"sample_count"`
	LastStep    *int64          `json:"last_step,omitempty"`
	LastValue   *float64        `json:"last_value,omitempty"`
	UpdatedAt   string          `json:"updated_at"`
}

func (s *Server) handlePutRunMetrics(w http.ResponseWriter, r *http.Request) {
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

	// Confirm run belongs to team via its project.
	var found string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT r.id FROM runs r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = ? AND p.team_id = ?`, runID, team).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM run_metrics WHERE run_id = ?`, runID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := NowUTC()
	for _, m := range in.Metrics {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO run_metrics (id, run_id, metric_name, points_json,
			                         sample_count, last_step, last_value, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			NewID(), runID, m.Name, string(m.Points),
			m.SampleCount, nullableInt64(m.LastStep), nullableFloat64(m.LastValue),
			now); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.Metrics)})
}

func (s *Server) handleGetRunMetrics(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	// Confirm run is team-scoped before returning rows.
	var found string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT r.id FROM runs r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = ? AND p.team_id = ?`, runID, team).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT metric_name, points_json, sample_count, last_step, last_value, updated_at
		FROM run_metrics WHERE run_id = ? ORDER BY metric_name`, runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
			writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func nullableFloat64(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

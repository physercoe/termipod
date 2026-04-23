package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Sweep-summary endpoint — one row per run in a project, carrying the run
// config plus the last value of every tracked metric. Feeds the mobile
// cross-run scatter panel (wandb "parallel coordinates" / sweep-compare
// archetype) on the project detail screen.
//
//   GET /v1/teams/{team}/projects/{project}/sweep-summary
//
// Shape picked to match what the mobile scatter widget needs:
//   - config_json stays a raw string so the client can key off any
//     parameter name (n_embd, optimizer, lr, ...) without the server
//     committing to a schema.
//   - final_metrics is a {metric_name: last_value} map so a reviewer
//     can flip between x/y axes by metric in the UI.
//
// Performance note: scans runs + run_metrics for the whole project in
// two queries, pivots in-memory. Fine for demo-scale sweeps (6 runs).
// If a project ever carries thousands of runs, paginate at the runs
// layer before fanning out to metrics.

type sweepRunOut struct {
	RunID         string             `json:"run_id"`
	Status        string             `json:"status"`
	ConfigJSON    string             `json:"config_json,omitempty"`
	FinalMetrics  map[string]float64 `json:"final_metrics"`
	CreatedAt     string             `json:"created_at"`
	FinishedAt    string             `json:"finished_at,omitempty"`
	TrackioRunURI string             `json:"trackio_run_uri,omitempty"`
}

func (s *Server) handleGetProjectSweepSummary(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	projectID := chi.URLParam(r, "project")

	var projFound string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM projects WHERE id = ? AND team_id = ?`,
		projectID, team).Scan(&projFound)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	runRows, err := s.db.QueryContext(r.Context(), `
		SELECT id, status, config_json, created_at,
		       COALESCE(finished_at, ''), COALESCE(trackio_run_uri, '')
		FROM runs WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer runRows.Close()

	runs := []*sweepRunOut{}
	byID := map[string]*sweepRunOut{}
	for runRows.Next() {
		var (
			id, status, configJSON, createdAt, finishedAt, trackioURI string
		)
		if err := runRows.Scan(&id, &status, &configJSON, &createdAt, &finishedAt, &trackioURI); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Map server 'completed' → UI 'succeeded' at the boundary, same
		// convention as handlers_runs.go _runRowToUI.
		if status == "completed" {
			status = "succeeded"
		}
		row := &sweepRunOut{
			RunID:         id,
			Status:        status,
			ConfigJSON:    configJSON,
			FinalMetrics:  map[string]float64{},
			CreatedAt:     createdAt,
			FinishedAt:    finishedAt,
			TrackioRunURI: trackioURI,
		}
		runs = append(runs, row)
		byID[id] = row
	}
	if err := runRows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if len(runs) > 0 {
		metricRows, err := s.db.QueryContext(r.Context(), `
			SELECT run_id, metric_name, last_value
			FROM run_metrics
			WHERE last_value IS NOT NULL
			  AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			projectID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer metricRows.Close()
		for metricRows.Next() {
			var (
				runID, name string
				lastValue   sql.NullFloat64
			)
			if err := metricRows.Scan(&runID, &name, &lastValue); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !lastValue.Valid {
				continue
			}
			if row, ok := byID[runID]; ok {
				row.FinalMetrics[name] = lastValue.Float64
			}
		}
		if err := metricRows.Err(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// Ensure stable shape when there are zero runs (empty project case —
	// still 200, empty list, never null).
	if runs == nil {
		runs = []*sweepRunOut{}
	}
	writeJSON(w, http.StatusOK, runs)
}

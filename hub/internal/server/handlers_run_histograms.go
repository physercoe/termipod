package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Run histogram digest storage (migration 0018) — wandb/tensorboard
// "Distributions" archetype.
//
// Host-runners bin the raw tensor locally and PUT a compact digest
// (edges + counts) here. Same data-ownership split as run_metrics
// (0014) and run_images (0017): the hub indexes the digest, the host
// keeps the underlying tensor.
//
//   PUT /v1/teams/{team}/runs/{run}/histograms
//     body: {"histograms":[
//       {"name":"grads_hist/layer0","step":100,
//        "buckets":{"edges":[...],"counts":[...]}}, ...
//     ]}
//     Upserts by (run, metric_name, step). PUT replaces the row for
//     that triple; other (name, step) pairs in the run are left alone.
//     Symmetric with run_images' POST-on-each pattern; we prefer PUT
//     here because the body shape matches the one-shot digest upload
//     that wandb-style exporters use.
//
//   GET /v1/teams/{team}/runs/{run}/histograms (?metric=name)
//     Returns every (metric_name, step, buckets_json) row for the run,
//     ordered by metric_name, step. Optional ?metric= filters to one
//     series. Consumer parses buckets_json client-side.

type histBucketsIn struct {
	Edges  json.RawMessage `json:"edges"`
	Counts json.RawMessage `json:"counts"`
}

type histogramIn struct {
	Name    string        `json:"name"`
	Step    int64         `json:"step"`
	Buckets histBucketsIn `json:"buckets"`
}

type runHistogramsPutIn struct {
	Histograms []histogramIn `json:"histograms"`
}

type histogramOut struct {
	Name      string          `json:"name"`
	Step      int64           `json:"step"`
	Buckets   json.RawMessage `json:"buckets"`
	UpdatedAt string          `json:"updated_at"`
}

func (s *Server) handlePutRunHistograms(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in runHistogramsPutIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if len(in.Histograms) == 0 {
		writeErr(w, http.StatusBadRequest, "histograms[] required")
		return
	}
	for _, h := range in.Histograms {
		if h.Name == "" {
			writeErr(w, http.StatusBadRequest, "histograms[].name required")
			return
		}
		if len(h.Buckets.Edges) == 0 || len(h.Buckets.Counts) == 0 {
			writeErr(w, http.StatusBadRequest,
				"histograms[].buckets.{edges,counts} required")
			return
		}
		if !json.Valid(h.Buckets.Edges) || !json.Valid(h.Buckets.Counts) {
			writeErr(w, http.StatusBadRequest,
				"histograms[].buckets.{edges,counts} must be valid JSON arrays")
			return
		}
	}

	// Confirm run belongs to team via its project. Same pattern as
	// handlePutRunMetrics.
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

	now := NowUTC()
	for _, h := range in.Histograms {
		// Serialize buckets back to a canonical object before storing
		// so all rows share the {"edges":..,"counts":..} shape.
		buckets := map[string]json.RawMessage{
			"edges":  h.Buckets.Edges,
			"counts": h.Buckets.Counts,
		}
		blob, err := json.Marshal(buckets)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO run_histograms
				(id, run_id, metric_name, step, buckets_json, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(run_id, metric_name, step) DO UPDATE SET
				buckets_json = excluded.buckets_json,
				updated_at   = excluded.updated_at`,
			NewID(), runID, h.Name, h.Step, string(blob), now); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(in.Histograms)})
}

func (s *Server) handleGetRunHistograms(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")
	metric := r.URL.Query().Get("metric")

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

	var (
		rows *sql.Rows
	)
	if metric != "" {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT metric_name, step, buckets_json, updated_at
			FROM run_histograms
			WHERE run_id = ? AND metric_name = ?
			ORDER BY step`, runID, metric)
	} else {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT metric_name, step, buckets_json, updated_at
			FROM run_histograms
			WHERE run_id = ?
			ORDER BY metric_name, step`, runID)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []histogramOut{}
	for rows.Next() {
		var (
			name, bucketsJSON, updatedAt string
			step                         int64
		)
		if err := rows.Scan(&name, &step, &bucketsJSON, &updatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, histogramOut{
			Name:      name,
			Step:      step,
			Buckets:   json.RawMessage(bucketsJSON),
			UpdatedAt: updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

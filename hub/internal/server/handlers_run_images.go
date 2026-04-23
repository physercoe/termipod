package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Run image series — the wandb/tensorboard "Images" panel equivalent.
//
// Bytes live in the existing content-addressed blobs store; run_images
// rows index them by (run, metric_name, step) so the mobile Run Detail
// screen can pull a compact JSON list and fetch frames lazily via
// /v1/blobs/{sha}.
//
//   POST /v1/teams/{team}/runs/{run}/images
//     body: {"images":[{"metric_name":"samples/generations",
//                       "step":500,"blob_sha":"abc...","caption":"..."}, ...]}
//     Appends rows — idempotent on (run, metric_name, step) via UPSERT so
//     re-uploads from a restarted worker don't duplicate.
//
//   GET /v1/teams/{team}/runs/{run}/images[?metric=samples/generations]
//     Returns rows for the run, ordered by metric_name then step.

type runImageIn struct {
	MetricName string `json:"metric_name"`
	Step       int64  `json:"step"`
	BlobSHA    string `json:"blob_sha"`
	Caption    string `json:"caption,omitempty"`
}

type runImagesPostIn struct {
	Images []runImageIn `json:"images"`
}

type runImageOut struct {
	ID         string `json:"id"`
	MetricName string `json:"metric_name"`
	Step       int64  `json:"step"`
	BlobSHA    string `json:"blob_sha"`
	Caption    string `json:"caption,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func (s *Server) handlePostRunImages(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in runImagesPostIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed body: "+err.Error())
		return
	}
	if len(in.Images) == 0 {
		writeErr(w, http.StatusBadRequest, "images[] required")
		return
	}
	for _, img := range in.Images {
		if img.MetricName == "" {
			writeErr(w, http.StatusBadRequest, "images[].metric_name required")
			return
		}
		if img.BlobSHA == "" {
			writeErr(w, http.StatusBadRequest, "images[].blob_sha required")
			return
		}
	}

	// Run must be team-scoped (via project).
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
	for _, img := range in.Images {
		// UPSERT on the (run, metric_name, step) unique key — workers may
		// re-send the same checkpoint after a crash/resume without creating
		// a duplicate row.
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO run_images (id, run_id, metric_name, step, blob_sha, caption, created_at)
			VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?)
			ON CONFLICT(run_id, metric_name, step) DO UPDATE SET
				blob_sha = excluded.blob_sha,
				caption  = excluded.caption`,
			NewID(), runID, img.MetricName, img.Step, img.BlobSHA, img.Caption, now,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"count": len(in.Images)})
}

func (s *Server) handleGetRunImages(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

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

	q := `SELECT id, metric_name, step, blob_sha, COALESCE(caption, ''), created_at
	      FROM run_images WHERE run_id = ?`
	args := []any{runID}
	if metric := r.URL.Query().Get("metric"); metric != "" {
		q += ` AND metric_name = ?`
		args = append(args, metric)
	}
	q += ` ORDER BY metric_name, step`

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []runImageOut{}
	for rows.Next() {
		var row runImageOut
		if err := rows.Scan(&row.ID, &row.MetricName, &row.Step,
			&row.BlobSHA, &row.Caption, &row.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

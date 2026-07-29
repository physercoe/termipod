package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GET /v1/teams/{team}/runs/{run}/dataset_hint (plan §8, wedge W5).
//
// Reads the run's logged config, sniffs the dataset it names, and — if a
// dataset with that location is already registered in the run's project —
// resolves it to an id. The client turns a resolved hint into a one-click
// "link", and an unresolved one into "register this root first".
//
// Deliberately its own endpoint rather than a field on the run: it needs the
// `run_config` row and a scan of the project's datasets, and GET /runs is on
// every list render. Nothing here writes: linking is PATCH /runs/{run}.

type runDatasetHintOut struct {
	Hint *DatasetHint `json:"hint"`
	// The dataset already registered at the hinted location, or "" when the
	// hint names something this project has never registered.
	DatasetID string `json:"dataset_id,omitempty"`
	// Set when the run is already linked, so a client can tell "nothing
	// proposed because nothing was found" from "nothing proposed because it
	// is already linked".
	LinkedDatasetID string `json:"linked_dataset_id,omitempty"`
}

func (s *Server) handleGetRunDatasetHint(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var projectID, linked, frozenConfig string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT r.project_id, COALESCE(r.dataset_id, ''), COALESCE(r.config_json, '')
		FROM runs r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = ? AND p.team_id = ?`, runID, team).Scan(&projectID, &linked, &frozenConfig)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}

	// The logged config first: it is what the training process actually ran
	// with, where the frozen one is whatever the caller passed at create time
	// and is often empty.
	var logged string
	err = s.db.QueryRowContext(r.Context(),
		`SELECT config_json FROM run_config WHERE run_id = ?`, runID).Scan(&logged)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.writeDBErr(w, err)
		return
	}

	hint := datasetHintFromConfig([]byte(logged))
	if hint == nil {
		hint = datasetHintFromConfig([]byte(frozenConfig))
	}

	out := runDatasetHintOut{Hint: hint, LinkedDatasetID: linked}
	if hint != nil {
		id, err := s.datasetMatchingHint(r, projectID, *hint)
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
		out.DatasetID = id
	}
	writeJSON(w, http.StatusOK, out)
}

// datasetMatchingHint scans the project's datasets for the one a hint names.
//
// A scan, not a query: the repo-id rule is a suffix match on a path whose
// separators vary by host, which SQL cannot express portably, and a project has
// tens of datasets rather than thousands. Deterministic on ties — the first by
// registration — so the same run does not propose a different dataset on each
// load.
func (s *Server) datasetMatchingHint(r *http.Request, projectID string, hint DatasetHint) (string, error) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, root_path, name FROM datasets WHERE project_id = ? ORDER BY registered_at, id`,
		projectID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, rootPath, name string
		if err := rows.Scan(&id, &rootPath, &name); err != nil {
			return "", err
		}
		if datasetMatchesHint(hint, rootPath, name) {
			return id, nil
		}
	}
	return "", rows.Err()
}

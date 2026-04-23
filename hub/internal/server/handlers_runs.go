package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Run status enum — enforced at the handler boundary (the column itself is
// un-checked for forward-compat). Keep in sync with migrations/0006_runs.up.sql.
var validRunStatuses = map[string]struct{}{
	"pending":   {},
	"running":   {},
	"completed": {},
	"failed":    {},
	"cancelled": {},
}

// Terminal statuses accepted by /complete.
var validCompleteStatuses = map[string]struct{}{
	"completed": {},
	"failed":    {},
	"cancelled": {},
}

type runIn struct {
	ProjectID     string          `json:"project_id"`
	AgentID       string          `json:"agent_id,omitempty"`
	ConfigJSON    json.RawMessage `json:"config_json,omitempty"`
	Seed          *int64          `json:"seed,omitempty"`
	ParentRunID   string          `json:"parent_run_id,omitempty"`
	StartedAt     string          `json:"started_at,omitempty"`
	TrackioHostID string          `json:"trackio_host_id,omitempty"`
	TrackioRunURI string          `json:"trackio_run_uri,omitempty"`
}

type runOut struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	AgentID       string `json:"agent_id,omitempty"`
	ConfigJSON    string `json:"config_json,omitempty"`
	Seed          *int64 `json:"seed,omitempty"`
	Status        string `json:"status"`
	StartedAt     string `json:"started_at,omitempty"`
	FinishedAt    string `json:"finished_at,omitempty"`
	TrackioHostID string `json:"trackio_host_id,omitempty"`
	TrackioRunURI string `json:"trackio_run_uri,omitempty"`
	ParentRunID   string `json:"parent_run_id,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type completeRunIn struct {
	Status string `json:"status"`
}

type metricURIIn struct {
	TrackioHostID string `json:"trackio_host_id"`
	TrackioRunURI string `json:"trackio_run_uri"`
}

// TODO: AttachArtifact depends on artifacts table (§6.6). Implement when
// the artifacts primitive lands (separate PR).

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in runIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ProjectID == "" {
		writeErr(w, http.StatusBadRequest, "project_id required")
		return
	}

	// Project must exist in this team.
	var projFound string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM projects WHERE id = ? AND team_id = ?`,
		in.ProjectID, team).Scan(&projFound)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// If agent_id is provided, it must belong to the same team.
	if in.AgentID != "" {
		var agentFound string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT id FROM agents WHERE id = ? AND team_id = ?`,
			in.AgentID, team).Scan(&agentFound)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "agent not found in team")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// If parent_run_id is provided, it must refer to a run in the same project.
	if in.ParentRunID != "" {
		var parentFound string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT id FROM runs WHERE id = ? AND project_id = ?`,
			in.ParentRunID, in.ProjectID).Scan(&parentFound)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "parent_run_id not found in project")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	id := NewID()
	now := NowUTC()
	var configStr string
	if len(in.ConfigJSON) > 0 {
		configStr = string(in.ConfigJSON)
	}

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO runs (
			id, project_id, agent_id, config_json, seed, status,
			started_at, trackio_host_id, trackio_run_uri, parent_run_id,
			created_at
		) VALUES (
			?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, 'pending',
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			?
		)`,
		id, in.ProjectID, in.AgentID, configStr, nullableInt64(in.Seed),
		in.StartedAt, in.TrackioHostID, in.TrackioRunURI, in.ParentRunID,
		now,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordAudit(r.Context(), team, "run.create", "run", id,
		"create run in project "+in.ProjectID,
		map[string]any{"project_id": in.ProjectID, "agent_id": in.AgentID})

	writeJSON(w, http.StatusCreated, runOut{
		ID:            id,
		ProjectID:     in.ProjectID,
		AgentID:       in.AgentID,
		ConfigJSON:    configStr,
		Seed:          in.Seed,
		Status:        "pending",
		StartedAt:     in.StartedAt,
		TrackioHostID: in.TrackioHostID,
		TrackioRunURI: in.TrackioRunURI,
		ParentRunID:   in.ParentRunID,
		CreatedAt:     now,
	})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")

	// Runs are team-scoped via their project. Filter by project via query
	// param (?project=...) and optionally by ?status= and ?agent=.
	q := `
		SELECT r.id, r.project_id, COALESCE(r.agent_id, ''),
		       COALESCE(r.config_json, ''), r.seed, r.status,
		       COALESCE(r.started_at, ''), COALESCE(r.finished_at, ''),
		       COALESCE(r.trackio_host_id, ''), COALESCE(r.trackio_run_uri, ''),
		       COALESCE(r.parent_run_id, ''), r.created_at
		FROM runs r
		JOIN projects p ON p.id = r.project_id
		WHERE p.team_id = ?`
	args := []any{team}

	if project := r.URL.Query().Get("project"); project != "" {
		q += " AND r.project_id = ?"
		args = append(args, project)
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if _, ok := validRunStatuses[status]; !ok {
			writeErr(w, http.StatusBadRequest, "invalid status filter")
			return
		}
		q += " AND r.status = ?"
		args = append(args, status)
	}
	if agent := r.URL.Query().Get("agent"); agent != "" {
		q += " AND r.agent_id = ?"
		args = append(args, agent)
	}
	// ?trackio_host=<hostID> lets the host-runner trackio poller pull only
	// the runs it's responsible for scraping — the hub stores
	// trackio_host_id on the run row, so this is a direct equality match.
	if trackHost := r.URL.Query().Get("trackio_host"); trackHost != "" {
		q += " AND r.trackio_host_id = ?"
		args = append(args, trackHost)
	}
	q += " ORDER BY r.created_at DESC"

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []runOut{}
	for rows.Next() {
		var ro runOut
		var seed sql.NullInt64
		if err := rows.Scan(&ro.ID, &ro.ProjectID, &ro.AgentID,
			&ro.ConfigJSON, &seed, &ro.Status,
			&ro.StartedAt, &ro.FinishedAt,
			&ro.TrackioHostID, &ro.TrackioRunURI,
			&ro.ParentRunID, &ro.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if seed.Valid {
			v := seed.Int64
			ro.Seed = &v
		}
		out = append(out, ro)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var ro runOut
	var seed sql.NullInt64
	err := s.db.QueryRowContext(r.Context(), `
		SELECT r.id, r.project_id, COALESCE(r.agent_id, ''),
		       COALESCE(r.config_json, ''), r.seed, r.status,
		       COALESCE(r.started_at, ''), COALESCE(r.finished_at, ''),
		       COALESCE(r.trackio_host_id, ''), COALESCE(r.trackio_run_uri, ''),
		       COALESCE(r.parent_run_id, ''), r.created_at
		FROM runs r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = ? AND p.team_id = ?`, runID, team).Scan(
		&ro.ID, &ro.ProjectID, &ro.AgentID,
		&ro.ConfigJSON, &seed, &ro.Status,
		&ro.StartedAt, &ro.FinishedAt,
		&ro.TrackioHostID, &ro.TrackioRunURI,
		&ro.ParentRunID, &ro.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if seed.Valid {
		v := seed.Int64
		ro.Seed = &v
	}
	writeJSON(w, http.StatusOK, ro)
}

func (s *Server) handleCompleteRun(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in completeRunIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Status == "" {
		writeErr(w, http.StatusBadRequest, "status required")
		return
	}
	if _, ok := validCompleteStatuses[in.Status]; !ok {
		writeErr(w, http.StatusBadRequest, "status must be one of completed|failed|cancelled")
		return
	}

	now := NowUTC()
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE runs SET status = ?, finished_at = ?
		WHERE id = ? AND project_id IN (SELECT id FROM projects WHERE team_id = ?)`,
		in.Status, now, runID, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	s.recordAudit(r.Context(), team, "run.complete", "run", runID,
		"run "+in.Status, map[string]any{"status": in.Status})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAttachMetricURI(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	runID := chi.URLParam(r, "run")

	var in metricURIIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if in.TrackioHostID == "" || in.TrackioRunURI == "" {
		writeErr(w, http.StatusBadRequest, "trackio_host_id and trackio_run_uri required")
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		UPDATE runs SET trackio_host_id = ?, trackio_run_uri = ?
		WHERE id = ? AND project_id IN (SELECT id FROM projects WHERE team_id = ?)`,
		in.TrackioHostID, in.TrackioRunURI, runID, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "run not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nullableInt64 returns nil for a nil pointer so NULLIF-style SQL writes an
// empty/NULL column. Mirrors nullableInt in handlers_agents.go.
func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

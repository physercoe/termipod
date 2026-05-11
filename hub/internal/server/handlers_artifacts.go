package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Artifact kinds — closed set defined in artifact_kinds.go (W1 of the
// artifact-type-registry plan). See `validArtifactKinds` for the live
// vocabulary and `backfillLegacyArtifactKind` for the pre-W1 mapping.

type artifactIn struct {
	ProjectID       string          `json:"project_id"`
	RunID           string          `json:"run_id,omitempty"`
	Kind            string          `json:"kind"`
	Name            string          `json:"name"`
	URI             string          `json:"uri"`
	SHA256          string          `json:"sha256,omitempty"`
	Size            *int64          `json:"size,omitempty"`
	MIME            string          `json:"mime,omitempty"`
	ProducerAgentID string          `json:"producer_agent_id,omitempty"`
	LineageJSON     json.RawMessage `json:"lineage_json,omitempty"`
}

type artifactOut struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	RunID           string `json:"run_id,omitempty"`
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	URI             string `json:"uri"`
	SHA256          string `json:"sha256,omitempty"`
	Size            *int64 `json:"size,omitempty"`
	MIME            string `json:"mime,omitempty"`
	ProducerAgentID string `json:"producer_agent_id,omitempty"`
	LineageJSON     string `json:"lineage_json,omitempty"`
	CreatedAt       string `json:"created_at"`
}

func (s *Server) handleCreateArtifact(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in artifactIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ProjectID == "" || in.Kind == "" || in.Name == "" || in.URI == "" {
		writeErr(w, http.StatusBadRequest, "project_id, kind, name, uri required")
		return
	}

	// Closed-set validation. Legacy kinds are silently remapped so
	// in-flight MCP clients keep working through one tester cycle; new
	// callers should send a value from `validArtifactKinds` directly.
	if !validArtifactKinds[in.Kind] {
		if mapped, ok := backfillLegacyArtifactKind(in.Kind); ok {
			in.Kind = mapped
		} else {
			writeErr(w, http.StatusBadRequest,
				"unknown artifact kind (see docs/plans/artifact-type-registry.md)")
			return
		}
	}

	// Per-kind body cap for AFM-V1 artifacts. Applied retroactively to
	// code-bundle (Q12 of docs/plans/canvas-viewer.md, 2026-05-11) since
	// no production bundle today comes close. Size is client-reported;
	// the global blob cap still bounds outright abuse.
	if artifactBodyCapped(in.Kind) && in.Size != nil &&
		*in.Size > ArtifactBodyMaxBytes {
		writeErr(w, http.StatusBadRequest, in.Kind+" body exceeds 10 MB cap")
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

	// If run_id is supplied, it must belong to the same project.
	if in.RunID != "" {
		var runProject string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT project_id FROM runs WHERE id = ?`, in.RunID).Scan(&runProject)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "run_id not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if runProject != in.ProjectID {
			writeErr(w, http.StatusBadRequest, "run_id belongs to a different project")
			return
		}
	}

	lineage := ""
	if len(in.LineageJSON) > 0 {
		// Validate shape to keep the column queryable later. We don't
		// enforce any particular schema — just that it parses.
		var tmp any
		if err := json.Unmarshal(in.LineageJSON, &tmp); err != nil {
			writeErr(w, http.StatusBadRequest, "lineage_json must be valid JSON")
			return
		}
		lineage = string(in.LineageJSON)
	}

	id := NewID()
	now := NowUTC()
	var size sql.NullInt64
	if in.Size != nil {
		size = sql.NullInt64{Int64: *in.Size, Valid: true}
	}
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO artifacts (
			id, project_id, run_id, kind, name, uri, sha256, size, mime,
			producer_agent_id, lineage_json, created_at
		) VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''),
		          NULLIF(?, ''), NULLIF(?, ''), ?)`,
		id, in.ProjectID, in.RunID, in.Kind, in.Name, in.URI, in.SHA256, size,
		in.MIME, in.ProducerAgentID, lineage, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	meta := map[string]any{
		"project_id": in.ProjectID,
		"kind":       in.Kind,
	}
	if in.RunID != "" {
		meta["run_id"] = in.RunID
	}
	s.recordAudit(r.Context(), team, "artifact.create", "artifact", id,
		in.Kind+" "+in.Name, meta)

	out := artifactOut{
		ID: id, ProjectID: in.ProjectID, RunID: in.RunID,
		Kind: in.Kind, Name: in.Name, URI: in.URI,
		SHA256: in.SHA256, Size: in.Size, MIME: in.MIME,
		ProducerAgentID: in.ProducerAgentID, LineageJSON: lineage,
		CreatedAt: now,
	}
	writeJSON(w, http.StatusCreated, out)
}

// handleListArtifacts lists artifacts at team scope, filtered by project
// (?project=), run (?run=), and/or kind (?kind=). Newest first. Used by the
// mobile ArtifactsScreen and by any cross-project audit.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	project := r.URL.Query().Get("project")
	run := r.URL.Query().Get("run")
	kind := r.URL.Query().Get("kind")

	// Scope to team by joining on projects. Artifacts are always tied to a
	// project, so project_id → team_id is the authoritative auth check.
	q := `SELECT a.id, a.project_id, a.run_id, a.kind, a.name, a.uri,
	             a.sha256, a.size, a.mime, a.producer_agent_id,
	             a.lineage_json, a.created_at
	      FROM artifacts a
	      JOIN projects p ON p.id = a.project_id
	      WHERE p.team_id = ?`
	args := []any{team}
	if project != "" {
		q += ` AND a.project_id = ?`
		args = append(args, project)
	}
	if run != "" {
		q += ` AND a.run_id = ?`
		args = append(args, run)
	}
	if kind != "" {
		q += ` AND a.kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY a.created_at DESC`

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []artifactOut{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	art := chi.URLParam(r, "artifact")
	row := s.db.QueryRowContext(r.Context(), `
		SELECT a.id, a.project_id, a.run_id, a.kind, a.name, a.uri,
		       a.sha256, a.size, a.mime, a.producer_agent_id,
		       a.lineage_json, a.created_at
		FROM artifacts a
		JOIN projects p ON p.id = a.project_id
		WHERE a.id = ? AND p.team_id = ?`, art, team)
	a, err := scanArtifact(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// scanArtifact accepts *sql.Row or *sql.Rows via the rowScanner interface
// (defined in handlers_agents.go) so the list and get handlers can share
// column plumbing.
func scanArtifact(r rowScanner) (artifactOut, error) {
	var a artifactOut
	var run, sha, mime, producer, lineage sql.NullString
	var size sql.NullInt64
	err := r.Scan(&a.ID, &a.ProjectID, &run, &a.Kind, &a.Name, &a.URI,
		&sha, &size, &mime, &producer, &lineage, &a.CreatedAt)
	if err != nil {
		return a, err
	}
	if run.Valid {
		a.RunID = run.String
	}
	if sha.Valid {
		a.SHA256 = sha.String
	}
	if size.Valid {
		v := size.Int64
		a.Size = &v
	}
	if mime.Valid {
		a.MIME = mime.String
	}
	if producer.Valid {
		a.ProducerAgentID = producer.String
	}
	if lineage.Valid {
		a.LineageJSON = lineage.String
	}
	return a, nil
}

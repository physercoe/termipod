package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type projectIn struct {
	Name      string `json:"name"`
	DocsRoot  string `json:"docs_root,omitempty"`
	ConfigYML string `json:"config_yaml,omitempty"`
}

type projectOut struct {
	ID         string  `json:"id"`
	TeamID     string  `json:"team_id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	DocsRoot   string  `json:"docs_root,omitempty"`
	ConfigYAML string  `json:"config_yaml,omitempty"`
	CreatedAt  string  `json:"created_at"`
	ArchivedAt *string `json:"archived_at,omitempty"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in projectIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO projects (id, team_id, name, config_yaml, docs_root, created_at)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?)`,
		id, team, in.Name, in.ConfigYML, in.DocsRoot, now,
	)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, projectOut{
		ID: id, TeamID: team, Name: in.Name, Status: "active",
		DocsRoot: in.DocsRoot, ConfigYAML: in.ConfigYML, CreatedAt: now,
	})
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, team_id, name, status,
		       COALESCE(docs_root, ''), COALESCE(config_yaml, ''),
		       created_at, archived_at
		FROM projects WHERE team_id = ? ORDER BY created_at DESC`, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []projectOut{}
	for rows.Next() {
		var p projectOut
		var archived sql.NullString
		if err := rows.Scan(&p.ID, &p.TeamID, &p.Name, &p.Status,
			&p.DocsRoot, &p.ConfigYAML, &p.CreatedAt, &archived); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if archived.Valid {
			p.ArchivedAt = &archived.String
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	proj := chi.URLParam(r, "project")
	var p projectOut
	var archived sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, name, status,
		       COALESCE(docs_root, ''), COALESCE(config_yaml, ''),
		       created_at, archived_at
		FROM projects WHERE team_id = ? AND id = ?`, team, proj).Scan(
		&p.ID, &p.TeamID, &p.Name, &p.Status, &p.DocsRoot, &p.ConfigYAML,
		&p.CreatedAt, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if archived.Valid {
		p.ArchivedAt = &archived.String
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleArchiveProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	proj := chi.URLParam(r, "project")
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE projects SET status='archived', archived_at=?
		WHERE team_id = ? AND id = ? AND status != 'archived'`,
		NowUTC(), team, proj)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "project not found or already archived")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

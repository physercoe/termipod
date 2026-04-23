package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type channelIn struct {
	Name string `json:"name"`
}

type channelOut struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	ScopeKind string `json:"scope_kind"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	proj := chi.URLParam(r, "project")
	var in channelIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO channels (id, project_id, scope_kind, name, created_at)
		VALUES (?, ?, 'project', ?, ?)`, id, proj, in.Name, now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "channel.create", "channel", id,
		"create channel #"+in.Name,
		map[string]any{"scope_kind": "project", "project_id": proj})
	writeJSON(w, http.StatusCreated, channelOut{
		ID: id, ProjectID: proj, ScopeKind: "project", Name: in.Name, CreatedAt: now,
	})
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, name, created_at
		FROM channels WHERE project_id = ? ORDER BY created_at`, proj)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []channelOut{}
	for rows.Next() {
		var c channelOut
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	proj := chi.URLParam(r, "project")
	ch := chi.URLParam(r, "channel")
	var c channelOut
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, name, created_at
		FROM channels WHERE project_id = ? AND id = ?`, proj, ch).Scan(
		&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---- team-scope channels ----
//
// Team-scope channels have project_id NULL and scope_kind='team'. They
// carry cross-project traffic (most importantly `#hub-meta`, the principal
// ↔ steward room). Event read/post/stream reuse the project-scope handlers
// — those only consume the channel URL param, never the project param.

func (s *Server) handleCreateTeamChannel(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in channelIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO channels (id, project_id, scope_kind, name, created_at)
		VALUES (?, NULL, 'team', ?, ?)`, id, in.Name, now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "channel.create", "channel", id,
		"create team channel #"+in.Name,
		map[string]any{"scope_kind": "team"})
	writeJSON(w, http.StatusCreated, channelOut{
		ID: id, ScopeKind: "team", Name: in.Name, CreatedAt: now,
	})
}

func (s *Server) handleListTeamChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, name, created_at
		FROM channels WHERE scope_kind = 'team' AND project_id IS NULL
		ORDER BY created_at`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []channelOut{}
	for rows.Next() {
		var c channelOut
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetTeamChannel(w http.ResponseWriter, r *http.Request) {
	ch := chi.URLParam(r, "channel")
	var c channelOut
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, name, created_at
		FROM channels WHERE scope_kind = 'team' AND project_id IS NULL AND id = ?`,
		ch).Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

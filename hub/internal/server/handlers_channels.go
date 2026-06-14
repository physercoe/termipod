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
	_, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO channels (id, team_id, project_id, scope_kind, name, created_at)
		VALUES (?, ?, ?, 'project', ?, ?)`, id, team, proj, in.Name, now)
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
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []channelOut{}
	for rows.Next() {
		var c channelOut
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt); err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
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
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// requireChannelTeam enforces that channelID belongs to the path team
// before any event read / write / stream touches it. The channel event
// routes take a bare `{channel}` id (the post/list/stream handlers are
// shared by the project- and team-scope groups and consume only that
// param), so without this an agent authorized for its own team's path
// (the W1 gate) could still address another team's channel id directly.
// This is the class-level guard for the ADR-037 W6 channel leak, not a
// per-query patch. Returns false (and writes 404, never distinguishing
// "missing" from "foreign") when the channel is absent or owned by
// another team.
func (s *Server) requireChannelTeam(w http.ResponseWriter, r *http.Request, channelID string) bool {
	team := chi.URLParam(r, "team")
	var owner string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT team_id FROM channels WHERE id = ?`, channelID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != team) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return false
	}
	if err != nil {
		s.writeDBErr(w, err)
		return false
	}
	return true
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
	_, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO channels (id, team_id, project_id, scope_kind, name, created_at)
		VALUES (?, ?, NULL, 'team', ?, ?)`, id, team, in.Name, now)
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
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, name, created_at
		FROM channels WHERE scope_kind = 'team' AND project_id IS NULL AND team_id = ?
		ORDER BY created_at`, team)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []channelOut{}
	for rows.Next() {
		var c channelOut
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt); err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetTeamChannel(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	ch := chi.URLParam(r, "channel")
	var c channelOut
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, COALESCE(project_id, ''), scope_kind, name, created_at
		FROM channels WHERE scope_kind = 'team' AND project_id IS NULL AND team_id = ? AND id = ?`,
		team, ch).Scan(&c.ID, &c.ProjectID, &c.ScopeKind, &c.Name, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type hostIn struct {
	Name         string          `json:"name"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

type hostOut struct {
	ID           string          `json:"id"`
	TeamID       string          `json:"team_id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	LastSeenAt   *string         `json:"last_seen_at,omitempty"`
	Capabilities json.RawMessage `json:"capabilities"`
	CreatedAt    string          `json:"created_at"`
}

// handleRegisterHost creates a host record. Host-agents call this on boot
// with their owner token; subsequent heartbeats use the returned host id.
func (s *Server) handleRegisterHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in hostIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	caps := "{}"
	if len(in.Capabilities) > 0 {
		caps = string(in.Capabilities)
	}
	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
		VALUES (?, ?, ?, 'online', ?, ?)`,
		id, team, in.Name, caps, now)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, hostOut{
		ID: id, TeamID: team, Name: in.Name, Status: "online",
		Capabilities: json.RawMessage(caps), CreatedAt: now,
	})
}

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, team_id, name, status, last_seen_at, capabilities_json, created_at
		FROM hosts WHERE team_id = ? ORDER BY created_at`, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []hostOut{}
	for rows.Next() {
		var h hostOut
		var lastSeen sql.NullString
		var caps string
		if err := rows.Scan(&h.ID, &h.TeamID, &h.Name, &h.Status, &lastSeen, &caps, &h.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if lastSeen.Valid {
			h.LastSeenAt = &lastSeen.String
		}
		h.Capabilities = json.RawMessage(caps)
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleHostHeartbeat updates last_seen_at and keeps status = online.
// Called every ~10s by the host-agent loop.
func (s *Server) handleHostHeartbeat(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE hosts SET status='online', last_seen_at = ?
		WHERE team_id = ? AND id = ?`, NowUTC(), team, host)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetHost(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	host := chi.URLParam(r, "host")
	var h hostOut
	var lastSeen sql.NullString
	var caps string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, team_id, name, status, last_seen_at, capabilities_json, created_at
		FROM hosts WHERE team_id = ? AND id = ?`, team, host).Scan(
		&h.ID, &h.TeamID, &h.Name, &h.Status, &lastSeen, &caps, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if lastSeen.Valid {
		h.LastSeenAt = &lastSeen.String
	}
	h.Capabilities = json.RawMessage(caps)
	writeJSON(w, http.StatusOK, h)
}

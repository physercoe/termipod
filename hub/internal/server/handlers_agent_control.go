package server

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Per-agent lifecycle / inspection endpoints that sit on top of the
// host_commands queue. POST /pause and /resume enqueue a command for the
// agent's host; GET /pane returns the most recent cached capture and
// optionally enqueues a fresh one (?refresh=1).

func (s *Server) agentHost(r *http.Request, team, id string) (hostID, paneID string, err error) {
	err = s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(host_id, ''), COALESCE(pane_id, '') FROM agents
		 WHERE team_id = ? AND id = ?`, team, id).Scan(&hostID, &paneID)
	return
}

func (s *Server) handlePauseAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	hostID, paneID, err := s.agentHost(r, team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hostID == "" {
		writeErr(w, http.StatusConflict, "agent has no host yet")
		return
	}
	cmdID, err := s.enqueueHostCommand(r.Context(), hostID, id, "pause",
		map[string]any{"pane_id": paneID})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"command_id": cmdID})
}

func (s *Server) handleResumeAgent(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")
	hostID, paneID, err := s.agentHost(r, team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hostID == "" {
		writeErr(w, http.StatusConflict, "agent has no host yet")
		return
	}
	cmdID, err := s.enqueueHostCommand(r.Context(), hostID, id, "resume",
		map[string]any{"pane_id": paneID})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"command_id": cmdID})
}

// handleGetAgentPane returns the last cached capture. ?refresh=1 additionally
// enqueues a new capture command so the next read reflects the current pane.
func (s *Server) handleGetAgentPane(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "agent")

	var hostID, paneID string
	var lastCapture, lastAt sql.NullString
	err := s.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(host_id, ''), COALESCE(pane_id, ''),
		        last_capture, last_capture_at
		 FROM agents WHERE team_id = ? AND id = ?`, team, id).
		Scan(&hostID, &paneID, &lastCapture, &lastAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if r.URL.Query().Get("refresh") == "1" {
		if hostID == "" {
			writeErr(w, http.StatusConflict, "agent has no host yet")
			return
		}
		if _, err := s.enqueueHostCommand(r.Context(), hostID, id, "capture",
			map[string]any{"pane_id": paneID}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	resp := map[string]any{
		"agent_id": id,
		"pane_id":  paneID,
		"host_id":  hostID,
	}
	if lastCapture.Valid {
		resp["text"] = lastCapture.String
	}
	if lastAt.Valid {
		resp["captured_at"] = lastAt.String
	}
	writeJSON(w, http.StatusOK, resp)
}

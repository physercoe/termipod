package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// An agent's journal is a plain-markdown file on disk: the identity that
// survives respawns (plan §10A). Handle-scoped so a newly-spawned agent
// with the same handle inherits its predecessor's accumulated notes.
//
// Layout: <dataRoot>/agents/journals/<team>/<handle>.md
//
// This is the backing store for the MCP tools `journal_read` /
// `journal_append`. POST is append-only; full rewrites go through the
// filesystem for out-of-band edits.

func (s *Server) journalPath(team, handle string) (string, error) {
	if !safeHandle(handle) {
		return "", errors.New("invalid handle")
	}
	return filepath.Join(s.cfg.DataRoot, "agents", "journals", team, handle+".md"), nil
}

func safeHandle(h string) bool {
	// Handles are user-facing identifiers; keep them filesystem-safe.
	if h == "" || h == "." || h == ".." {
		return false
	}
	for _, c := range h {
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '@', c == '-', c == '_', c == '.':
			// ok
		default:
			return false
		}
	}
	return true
}

func (s *Server) handleReadJournal(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agent := chi.URLParam(r, "agent")

	handle, err := s.lookupAgentHandle(r, team, agent)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		body = nil
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write(body)
}

type journalAppendIn struct {
	Entry  string `json:"entry"`            // markdown content; appended verbatim
	Header string `json:"header,omitempty"` // optional timestamp/section header
}

func (s *Server) handleAppendJournal(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	agent := chi.URLParam(r, "agent")

	var in journalAppendIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Entry == "" {
		writeErr(w, http.StatusBadRequest, "entry required")
		return
	}

	handle, err := s.lookupAgentHandle(r, team, agent)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	path, err := s.journalPath(team, handle)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	now := NowUTC()
	header := in.Header
	if header == "" {
		header = "## " + now
	}
	if _, err := io.WriteString(f, "\n"+header+"\n\n"+in.Entry+"\n"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Cache the handle's last-known journal path on the agent row so the
	// mobile UI can link directly to it without repeating the lookup.
	_, _ = s.db.ExecContext(r.Context(),
		`UPDATE agents SET journal_path = ? WHERE team_id = ? AND id = ?`,
		path, team, agent)

	writeJSON(w, http.StatusCreated, map[string]any{
		"path":       path,
		"appended_at": now,
	})
}

func (s *Server) lookupAgentHandle(r *http.Request, team, agent string) (string, error) {
	var h string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT handle FROM agents WHERE team_id = ? AND id = ?`, team, agent).Scan(&h)
	return h, err
}

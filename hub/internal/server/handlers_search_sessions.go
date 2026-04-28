package server

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleSessionSearch — Phase 1.5c MVP parity gap closer. FTS5
// match over agent event payloads, scoped to the team's sessions.
// Pairs with `agent_events_fts` (migration 0031). Returns a flat
// list of result rows with enough context for the search screen
// to render the row + deep-link into the session at the matching
// event seq.
//
// Query params:
//   q     (required) — FTS5 MATCH expression
//   limit (optional, default 50, max 200)
//
// Result row shape:
//   {
//     event_id, session_id, scope_kind, scope_id, session_title,
//     seq, ts, kind, snippet
//   }
//
// Sessions soft-deleted via the delete handler have their event
// session_id NULLed (handlers_sessions.go:_handleDeleteSession),
// so deleted-session events fall out of this query naturally — the
// LEFT JOIN sees NULL and the WHERE filter on s.team_id rejects.
func (s *Server) handleSessionSearch(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q required")
		return
	}
	limit := 50
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
		    ae.id,
		    COALESCE(ae.session_id, '')      AS session_id,
		    COALESCE(s.scope_kind, '')        AS scope_kind,
		    COALESCE(s.scope_id, '')          AS scope_id,
		    COALESCE(s.title, '')             AS session_title,
		    ae.seq,
		    ae.ts,
		    ae.kind,
		    snippet(agent_events_fts, 1, '<mark>', '</mark>', '…', 16) AS snippet
		FROM agent_events_fts
		JOIN agent_events ae ON ae.id = agent_events_fts.event_id
		JOIN sessions       s  ON ae.session_id = s.id
		WHERE agent_events_fts MATCH ?
		  AND s.team_id = ?
		  AND s.status != 'deleted'
		ORDER BY ae.ts DESC
		LIMIT ?`, q, team, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		var (
			eventID, sessionID, scopeKind, scopeID, title, ts, kind, snip string
			seq                                                            int64
		)
		if err := rows.Scan(
			&eventID, &sessionID, &scopeKind, &scopeID, &title,
			&seq, &ts, &kind, &snip,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, map[string]any{
			"event_id":      eventID,
			"session_id":    sessionID,
			"scope_kind":    scopeKind,
			"scope_id":      scopeID,
			"session_title": title,
			"seq":           seq,
			"ts":            ts,
			"kind":          kind,
			"snippet":       snip,
		})
	}
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

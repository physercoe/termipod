package server

import (
	"net/http"
	"sort"
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
// FTS (agent_events_fts + agent_events) lives in the event store, but the
// team scope + result metadata come from `sessions` in the control store, so
// the old single JOIN is now two steps (ADR-045 D2): resolve the team's
// non-deleted sessions from control — that set doubles as both the result-row
// metadata and the event-store filter (agent_events has no team_id) — then FTS
// MATCH in the event store filtered to those session ids. Sessions soft-deleted
// via the delete handler also have their event session_id NULLed
// (clearSessionFromEvents), so deleted-session events drop out twice over.
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

	// 1. The team's non-deleted sessions (control) — id set + per-session
	//    metadata for the result rows.
	type sessMeta struct{ scopeKind, scopeID, title, hint string }
	metaByID := map[string]sessMeta{}
	var sessIDs []string
	srows, err := s.db.QueryContext(r.Context(), `
		SELECT id, COALESCE(scope_kind, ''), COALESCE(scope_id, ''),
		       COALESCE(title, ''), COALESCE(session_name_hint, '')
		  FROM sessions WHERE team_id = ? AND status != 'deleted'`, team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for srows.Next() {
		var id string
		var m sessMeta
		if err := srows.Scan(&id, &m.scopeKind, &m.scopeID, &m.title, &m.hint); err != nil {
			srows.Close()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		metaByID[id] = m
		sessIDs = append(sessIDs, id)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(sessIDs) == 0 {
		writeJSON(w, http.StatusOK, []map[string]any{})
		return
	}

	// 2. FTS MATCH in the event store, filtered to those sessions. Chunked so
	//    a large team can't exceed SQLite's bound-variable limit; each chunk
	//    is independently top-`limit` by ts, so the global top-`limit` is a
	//    subset of the merged chunk results — exact filter-before-limit.
	type cand struct {
		eventID, sessionID, ts, kind, snip string
		seq                                int64
	}
	var cands []cand
	er, err := s.eventsReader(team)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	const chunk = 900
	for start := 0; start < len(sessIDs); start += chunk {
		end := start + chunk
		if end > len(sessIDs) {
			end = len(sessIDs)
		}
		batch := sessIDs[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+2)
		args = append(args, q)
		for _, id := range batch {
			args = append(args, id)
		}
		args = append(args, limit)
		frows, err := er.QueryContext(r.Context(), `
			SELECT ae.id, COALESCE(ae.session_id, ''), ae.seq, ae.ts, ae.kind,
			       snippet(agent_events_fts, 1, '<mark>', '</mark>', '…', 16)
			  FROM agent_events_fts
			  JOIN agent_events ae ON ae.id = agent_events_fts.event_id
			 WHERE agent_events_fts MATCH ?
			   AND ae.session_id IN (`+ph+`)
			 ORDER BY ae.ts DESC
			 LIMIT ?`, args...)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for frows.Next() {
			var c cand
			if err := frows.Scan(&c.eventID, &c.sessionID, &c.seq, &c.ts, &c.kind, &c.snip); err != nil {
				frows.Close()
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			cands = append(cands, c)
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	// 3. Global top-`limit` by ts DESC (seq DESC tiebreak for stable order
	//    across chunks), then hydrate session metadata from step 1.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].ts != cands[j].ts {
			return cands[i].ts > cands[j].ts
		}
		return cands[i].seq > cands[j].seq
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := []map[string]any{}
	for _, c := range cands {
		m := metaByID[c.sessionID]
		out = append(out, map[string]any{
			"event_id":          c.eventID,
			"session_id":        c.sessionID,
			"scope_kind":        m.scopeKind,
			"scope_id":          m.scopeID,
			"session_title":     m.title,
			"session_name_hint": m.hint,
			"seq":               c.seq,
			"ts":                c.ts,
			"kind":              c.kind,
			"snippet":           c.snip,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

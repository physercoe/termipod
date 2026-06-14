package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/termipod/hub/internal/auth"
)

// handleSearch performs FTS5 match over event text parts (plan §15).
// Returns matching events ordered by received_ts desc.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	// /v1/search sits outside the /v1/teams/{team} group, so there is no
	// path team to gate on — it scopes to the caller's token team
	// instead. Without this the FTS match ran over every team's events
	// and returned other teams' message text to any bearer (ADR-037 G6 /
	// W6). events have no team_id of their own; we reach it by joining
	// channels.team_id (added in 0048). A teamless token fails closed.
	tok, ok := auth.FromContext(r.Context())
	if !ok || tok.ScopeTeam() == "" {
		writeErr(w, http.StatusForbidden, "team-scoped token required")
		return
	}
	team := tok.ScopeTeam()

	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q required")
		return
	}
	limit := 50
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT e.id, e.received_ts, e.channel_id, e.type,
		       COALESCE(e.from_id, ''), e.parts_json
		FROM events_fts f
		JOIN events e ON e.id = f.event_id
		JOIN channels c ON c.id = e.channel_id
		WHERE events_fts MATCH ? AND c.team_id = ?
		ORDER BY e.received_ts DESC
		LIMIT ?`, q, team, limit)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, received, chID, typ, from, parts string
		if err := rows.Scan(&id, &received, &chID, &typ, &from, &parts); err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, map[string]any{
			"id":          id,
			"received_ts": received,
			"channel_id":  chID,
			"type":        typ,
			"from_id":     from,
			"parts":       json.RawMessage(parts),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

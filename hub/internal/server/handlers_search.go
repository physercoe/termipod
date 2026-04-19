package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleSearch performs FTS5 match over event text parts (plan §15).
// Returns matching events ordered by received_ts desc.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
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
		WHERE events_fts MATCH ?
		ORDER BY e.received_ts DESC
		LIMIT ?`, q, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, received, chID, typ, from, parts string
		if err := rows.Scan(&id, &received, &chID, &typ, &from, &parts); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
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

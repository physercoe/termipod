package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// handleListAudit serves GET /v1/teams/{team}/audit. Filters:
//   - action   — exact match on the action string (e.g. "agent.spawn")
//   - since    — ISO-8601 UTC timestamp; returns rows with ts >= since
//   - limit    — max rows, clamped to 500 (default 100)
//
// Rows are ordered ts DESC so the newest actions appear first.
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	q := r.URL.Query()
	action := q.Get("action")
	since := q.Get("since")
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.listAuditEvents(r.Context(), team, action, since, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []AuditRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

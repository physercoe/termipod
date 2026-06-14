package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// handleListAudit serves GET /v1/teams/{team}/audit. Filters:
//   - action     — exact match on the action string (e.g. "agent.spawn")
//   - since      — ISO-8601 UTC timestamp; returns rows with ts >= since
//   - project_id — scope to one project (W2 Activity feed); matches
//     target_kind='project' rows and any row whose meta_json
//     carries a project_id field equal to this value
//   - limit      — max rows, clamped to 500 (default 100)
//   - before     — ISO-8601 UTC timestamp; keyset cursor for backward
//     pagination (ts < before ORDER BY ts DESC)
//   - after      — ISO-8601 UTC timestamp; keyset cursor for forward
//     pagination (ts > after ORDER BY ts ASC)
//
// Rows are ordered ts DESC so the newest actions appear first. When
// before/after are absent the response is byte-identical to the prior
// release — purely additive pagination.
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	q := r.URL.Query()
	action := q.Get("action")
	since := q.Get("since")
	projectID := q.Get("project_id")
	before := q.Get("before")
	after := q.Get("after")
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
	rows, err := s.listAuditEvents(r.Context(), team, action, since, projectID, before, after, limit)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if rows == nil {
		rows = []AuditRow{}
	}
	writeJSON(w, http.StatusOK, rows)
}

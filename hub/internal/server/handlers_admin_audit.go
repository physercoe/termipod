package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

// Owner-scope cross-team audit query (ADR-028 Phase 5 / plan W22+W25).
// GET /v1/teams/{team}/audit is team-scoped and matches `action`
// exactly; the mobile Admin pane needs the fleet-wide view with a
// prefix match (so `host.` catches host.shutdown / host.restart /
// host.update in one query). This endpoint provides that. Read-side:
// it writes no audit row of its own.

// AdminAuditRow is one cross-team audit event. It is AuditRow plus the
// team the event belongs to — the team filter is dropped here, so the
// caller needs the team back to render it.
type AdminAuditRow struct {
	AuditRow
	TeamID string `json:"team_id"`
}

// handleAdminListAudit is GET /v1/admin/audit — owner-scope. Query
// params: action_prefix, target_kind, actor (actor_handle match),
// since (ISO-8601 UTC), limit (default 100, clamped 500). Rows are
// newest-first.
func (s *Server) handleAdminListAudit(w http.ResponseWriter, r *http.Request) {
	if !s.requireOwner(w, r) {
		return
	}
	q := r.URL.Query()
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
	rows, err := s.listAdminAuditEvents(r.Context(), adminAuditFilter{
		actionPrefix: q.Get("action_prefix"),
		targetKind:   q.Get("target_kind"),
		actor:        q.Get("actor"),
		since:        q.Get("since"),
		limit:        limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": rows})
}

// adminAuditFilter is the decoded query for listAdminAuditEvents.
type adminAuditFilter struct {
	actionPrefix string
	targetKind   string
	actor        string
	since        string
	limit        int
}

// listAdminAuditEvents reads audit_events across every team. Empty
// filter fields are skipped. actionPrefix is a left-anchored LIKE so a
// caller can pass "host." and catch the whole verb family.
func (s *Server) listAdminAuditEvents(ctx context.Context, f adminAuditFilter) ([]AdminAuditRow, error) {
	if f.limit <= 0 {
		f.limit = 100
	}
	q := `SELECT id, team_id, ts, actor_kind, COALESCE(actor_handle, ''),
	             action, COALESCE(target_kind, ''), COALESCE(target_id, ''),
	             summary, meta_json
	        FROM audit_events
	       WHERE 1 = 1`
	var args []any
	if f.actionPrefix != "" {
		q += ` AND action LIKE ? || '%'`
		args = append(args, f.actionPrefix)
	}
	if f.targetKind != "" {
		q += ` AND target_kind = ?`
		args = append(args, f.targetKind)
	}
	if f.actor != "" {
		q += ` AND actor_handle = ?`
		args = append(args, f.actor)
	}
	if f.since != "" {
		q += ` AND ts >= ?`
		args = append(args, f.since)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, f.limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminAuditRow{}
	for rows.Next() {
		var ar AdminAuditRow
		var metaJSON string
		if err := rows.Scan(
			&ar.ID, &ar.TeamID, &ar.TS, &ar.ActorKind, &ar.ActorHandle,
			&ar.Action, &ar.TargetKind, &ar.TargetID, &ar.Summary, &metaJSON,
		); err != nil {
			return nil, err
		}
		if metaJSON != "" && metaJSON != "{}" {
			_ = json.Unmarshal([]byte(metaJSON), &ar.Meta)
		}
		out = append(out, ar)
	}
	return out, rows.Err()
}

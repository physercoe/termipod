package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/termipod/hub/internal/auth"
)

// AuditRow is the read shape for GET /v1/teams/{team}/audit.
type AuditRow struct {
	ID           string         `json:"id"`
	TS           string         `json:"ts"`
	ActorKind    string         `json:"actor_kind"`
	ActorHandle  string         `json:"actor_handle,omitempty"`
	Action       string         `json:"action"`
	TargetKind   string         `json:"target_kind,omitempty"`
	TargetID     string         `json:"target_id,omitempty"`
	Summary      string         `json:"summary"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// recordAudit inserts a row into audit_events. Errors are logged at warn
// level and swallowed — missing audit rows must never fail the underlying
// mutation. Actor is resolved from the request context; system callers
// (schedulers, reconstructors) can pass a background context and the row
// lands with actor_kind='system'.
func (s *Server) recordAudit(
	ctx context.Context,
	teamID, action, targetKind, targetID, summary string,
	meta map[string]any,
) {
	if s.db == nil || teamID == "" || action == "" {
		return
	}
	actorTokenID, actorKind, actorHandle := actorFromContext(ctx)
	metaJSON := "{}"
	if len(meta) > 0 {
		b, err := json.Marshal(meta)
		if err == nil {
			metaJSON = string(b)
		}
	}
	var tokenArg any
	if actorTokenID != "" {
		tokenArg = actorTokenID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_events (
			id, team_id, ts, actor_token_id, actor_kind, actor_handle,
			action, target_kind, target_id, summary, meta_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), teamID, NowUTC(), tokenArg, actorKind,
		nullIfEmpty(actorHandle), action,
		nullIfEmpty(targetKind), nullIfEmpty(targetID), summary, metaJSON,
	)
	if err != nil {
		s.log.Warn("audit: insert", "action", action, "err", err)
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// actorFromContext pulls the auth token from the request context and
// derives (token_id, kind, handle). Returns zero-values when the context
// has no token (e.g. scheduler-internal calls).
func actorFromContext(ctx context.Context) (tokenID, kind, handle string) {
	tok, ok := auth.FromContext(ctx)
	if !ok || tok == nil {
		return "", "system", ""
	}
	handle = strings.TrimPrefix(principalFromScope(tok.ScopeJSON), "@")
	return tok.ID, tok.Kind, handle
}

// listAuditEvents reads rows filtered by team/action/since with pagination.
// Callers are expected to pass a reasonable limit — there's no hard cap
// here but the handler clamps to 500.
func (s *Server) listAuditEvents(
	ctx context.Context,
	teamID, action, since string,
	limit int,
) ([]AuditRow, error) {
	if limit <= 0 {
		limit = 100
	}
	args := []any{teamID}
	q := `SELECT id, ts, actor_kind, COALESCE(actor_handle, ''),
	             action, COALESCE(target_kind, ''), COALESCE(target_id, ''),
	             summary, meta_json
	        FROM audit_events
	       WHERE team_id = ?`
	if action != "" {
		q += ` AND action = ?`
		args = append(args, action)
	}
	if since != "" {
		q += ` AND ts >= ?`
		args = append(args, since)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		var metaJSON string
		if err := rows.Scan(
			&r.ID, &r.TS, &r.ActorKind, &r.ActorHandle,
			&r.Action, &r.TargetKind, &r.TargetID,
			&r.Summary, &metaJSON,
		); err != nil {
			return nil, err
		}
		if metaJSON != "" && metaJSON != "{}" {
			_ = json.Unmarshal([]byte(metaJSON), &r.Meta)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Unused in this file but kept to prevent the linter from dropping the
// import if a future refactor removes the only sql use above.
var _ = sql.ErrNoRows

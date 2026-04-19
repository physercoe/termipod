package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Escalator wakes periodically and promotes open attention_items whose tier
// has a configured escalation rule and whose created_at is older than the
// rule's `after` duration. Each promotion:
//   - replaces current_assignees_json with the rule's widen_to list
//   - appends an entry to escalation_history_json
//
// Idempotency: once an item has been escalated (escalation_history non-
// empty), we leave it alone. If the human still hasn't acted after the
// widen, they can resolve it manually; we won't flap assignees.
//
// Runs alongside the cron scheduler and is stopped by the same ctx that
// shuts the HTTP server.
type Escalator struct {
	s    *Server
	log  *slog.Logger
	tick time.Duration

	mu      sync.Mutex
	stopped bool
}

// NewEscalator — tick defaults to 30s when zero. Tests override to ~10ms
// so a single wake-up is enough to observe the side effect.
func NewEscalator(s *Server, log *slog.Logger, tick time.Duration) *Escalator {
	if tick == 0 {
		tick = 30 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Escalator{s: s, log: log, tick: tick}
}

func (e *Escalator) Start(ctx context.Context) {
	go e.loop(ctx)
}

func (e *Escalator) loop(ctx context.Context) {
	t := time.NewTicker(e.tick)
	defer t.Stop()
	// One immediate sweep so a just-started hub picks up any items that
	// crossed their deadline while the process was offline.
	e.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.sweep(ctx)
		}
	}
}

// sweep selects open attention items with a non-null tier, checks each
// against the current policy, and escalates any that have exceeded their
// deadline and not yet been escalated.
func (e *Escalator) sweep(ctx context.Context) {
	if e.s.policy == nil {
		return
	}
	rows, err := e.s.db.QueryContext(ctx, `
		SELECT id, tier, created_at,
		       COALESCE(current_assignees_json, '[]'),
		       COALESCE(escalation_history_json, '[]')
		FROM attention_items
		WHERE status = 'open' AND tier IS NOT NULL`)
	if err != nil {
		e.log.Warn("escalator: select failed", "err", err)
		return
	}
	defer rows.Close()

	type row struct {
		id, tier, createdAt    string
		assignees, history     string
	}
	var items []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.tier, &r.createdAt, &r.assignees, &r.history); err != nil {
			e.log.Warn("escalator: scan failed", "err", err)
			continue
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		e.log.Warn("escalator: row iter failed", "err", err)
		return
	}

	now := time.Now().UTC()
	for _, it := range items {
		rule, ok := e.s.policy.EscalationFor(it.tier)
		if !ok {
			continue
		}
		after, err := time.ParseDuration(rule.After)
		if err != nil {
			// Bad policy value — log once and skip. Changing the YAML will
			// be picked up on the next SIGHUP; no point retrying every tick.
			e.log.Warn("escalator: bad duration in policy",
				"tier", it.tier, "after", rule.After, "err", err)
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, it.createdAt)
		if err != nil {
			continue
		}
		if now.Sub(created) < after {
			continue
		}
		if hasAnyHistory(it.history) {
			continue
		}
		if err := e.apply(ctx, it.id, it.tier, it.assignees, it.history, rule, now); err != nil {
			e.log.Warn("escalator: apply failed", "id", it.id, "err", err)
			continue
		}
		e.log.Info("escalated attention item",
			"id", it.id, "tier", it.tier, "widen_to", rule.WidenTo)
	}
}

// apply mutates one attention_items row: replace assignees, append history
// entry. Done in a single UPDATE so the row can't be observed mid-escalation.
func (e *Escalator) apply(
	ctx context.Context,
	id, tier, assigneesJSON, historyJSON string,
	rule EscalationPolicy,
	now time.Time,
) error {
	var prev []string
	_ = json.Unmarshal([]byte(assigneesJSON), &prev)

	var history []map[string]any
	_ = json.Unmarshal([]byte(historyJSON), &history)
	history = append(history, map[string]any{
		"at":             now.Format(time.RFC3339Nano),
		"tier":           tier,
		"from_assignees": prev,
		"to_assignees":   rule.WidenTo,
		"reason":         "deadline_exceeded",
	})

	newAssignees, err := json.Marshal(rule.WidenTo)
	if err != nil {
		return err
	}
	newHistory, err := json.Marshal(history)
	if err != nil {
		return err
	}
	res, err := e.s.db.ExecContext(ctx, `
		UPDATE attention_items
		SET current_assignees_json = ?, escalation_history_json = ?
		WHERE id = ? AND status = 'open' AND escalation_history_json = ?`,
		string(newAssignees), string(newHistory), id, historyJSON)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Someone else (a decide call, a concurrent escalator) beat us —
		// that's fine; the item is handled.
		return nil
	}
	return nil
}

// hasAnyHistory returns true if the escalation_history JSON has at least
// one entry. Treats malformed JSON as "has history" so a corrupted field
// doesn't get re-escalated every tick.
func hasAnyHistory(js string) bool {
	var h []any
	if err := json.Unmarshal([]byte(js), &h); err != nil {
		return true
	}
	return len(h) > 0
}

// ensure sql import stays used even if future refactors drop the direct
// reference — sweep queries via Server.db which is typed *sql.DB.
var _ = sql.ErrNoRows

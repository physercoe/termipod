package server

import (
	"context"
	"database/sql"
)

// agent_event_insert.go — the single place an agent_events row is born.
//
// Before ADR-042 the `COALESCE(MAX(seq),0)+1` insert idiom was copy-pasted
// across 10 files (13 sites). That duplication is how the per-agent `seq` could
// silently stay the only coordinate even as the session surface grew to need a
// session-unique one. Centralizing the write gives a single, correct
// assignment site for seq today and `session_ordinal` (ADR-042 P1) next.

// agentEventRower is the minimal slice of *sql.DB / *sql.Tx the insert needs,
// so callers can pass either (a plain write or one inside a transaction).
type agentEventRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// agentEventInsert is the per-call shape: everything that varies between the
// insert sites. The id, seq, and ts are assigned by insertAgentEvent.
type agentEventInsert struct {
	AgentID     string
	SessionID   string // "" → stored as NULL (an event without a session)
	Kind        string
	Producer    string // 'agent' | 'user' | 'system' | 'a2a'
	PayloadJSON string
}

// insertAgentEvent writes one agent_events row, assigning a monotonic per-agent
// `seq` and a server `ts`, and returns the generated id, the assigned seq, and
// the ts (callers fan these out onto the SSE bus). SQLite serializes writes, so
// `COALESCE(MAX(seq),0)+1` inside a single statement is race-free against other
// inserts to the same agent; UNIQUE(agent_id, seq) is the backstop. `q` is
// *sql.DB or *sql.Tx.
//
// SessionID is normalized through NULLIF so an empty string becomes a real SQL
// NULL — an event either belongs to a session or it does not; "" is never a
// session id (and ADR-042 P1's session_ordinal is only assigned when session_id
// is non-NULL).
func insertAgentEvent(ctx context.Context, q agentEventRower, ev agentEventInsert) (id string, seq int64, ts string, err error) {
	id = NewID()
	ts = NowUTC()
	err = q.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, NULLIF(?, '')
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, ev.AgentID, ts, ev.Kind, ev.Producer, ev.PayloadJSON, ev.SessionID, ev.AgentID).Scan(&seq)
	return id, seq, ts, err
}

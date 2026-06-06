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

// agentEventInsert is the per-call shape: everything that varies between the
// insert sites. The id, seq, and ts are assigned by insertAgentEvent.
type agentEventInsert struct {
	AgentID   string
	SessionID string // "" → stored as NULL (an event without a session)
	Kind      string
	Producer  string // 'agent' | 'user' | 'system' | 'a2a'
	// ProjectID is normally left "" — insertAgentEvent resolves it from the
	// session's project scope (replacing the agent_events_stamp_project trigger).
	// Set it only to override that resolution.
	ProjectID   string
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
// session id.
//
// session_ordinal (ADR-042) is assigned in the same statement: a dense,
// monotonic coordinate per *session* (COALESCE(MAX(session_ordinal),0)+1 over
// the rows sharing session_id), NULL when the event has no session. Unlike
// `seq` (per-agent), it is unique across the whole session even when the
// session spans multiple agents after a resume — the coordinate the
// session-scoped Insight surface lands on. UNIQUE(session_id, session_ordinal)
// backstops the race, the same way UNIQUE(agent_id, seq) backstops seq.
// The returned sord is the assigned session_ordinal (0 when the event has no
// session). Callers that maintain the per-event digest pass it on so the
// incremental fold records the same ordinal the brute-force fold reads back
// from the column (the incremental==brute invariant, ADR-038).
//
// project_id is resolved here from the session's project scope when the caller
// leaves ev.ProjectID empty — the handler-side replacement for the
// agent_events_stamp_project trigger (dropped in migration 0055 because it read
// sessions, which lives in the control store, so it can't survive agent_events
// moving to its own file — ADR-045 step 4). The sessions read goes through
// s.db (control); the insert through the event-store writer. They need no shared
// transaction: the session is already committed by the time its agent posts
// events. A method (not a free function) so it reaches the control store.
//
// The event-store writer is resolved here from ev.AgentID (ADR-045 P2 — the
// per-team shard the agent belongs to), so every insert site routes to the
// right store without threading a handle. teamForAgent is cached; an
// unresolvable team (no such agent) is returned as an error.
func (s *Server) insertAgentEvent(ctx context.Context, ev agentEventInsert) (id string, seq, sord int64, ts string, err error) {
	w, err := s.eventsWriterForAgent(ctx, ev.AgentID)
	if err != nil {
		return "", 0, 0, "", err
	}
	if ev.ProjectID == "" && ev.SessionID != "" {
		var pid sql.NullString
		_ = s.db.QueryRowContext(ctx,
			`SELECT scope_id FROM sessions WHERE id = ? AND scope_kind = 'project'`,
			ev.SessionID).Scan(&pid)
		ev.ProjectID = pid.String
	}
	id = NewID()
	ts = NowUTC()
	var sordN sql.NullInt64
	err = w.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, session_ordinal, ts, kind, producer, payload_json, session_id, project_id)
		SELECT ?, ?,
		       COALESCE(MAX(seq), 0) + 1,
		       CASE WHEN NULLIF(?, '') IS NULL THEN NULL
		            ELSE COALESCE((SELECT MAX(session_ordinal) FROM agent_events WHERE session_id = ?), 0) + 1
		       END,
		       ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, '')
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq, session_ordinal`,
		id, ev.AgentID, ev.SessionID, ev.SessionID,
		ts, ev.Kind, ev.Producer, ev.PayloadJSON, ev.SessionID, ev.ProjectID, ev.AgentID).Scan(&seq, &sordN)
	return id, seq, sordN.Int64, ts, err
}

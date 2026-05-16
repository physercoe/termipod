package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// notifyA2AReceived posts a kind='a2a.received' producer='system' event
// into the receiving agent's most-recent active session every time an
// A2A relay successfully delivers (status < 400). Closes the gap where
// peer-to-peer messages were audit-only — the receiver only discovered
// them on its next `InputRouter` poll, adding host-runner heartbeat
// latency (up to several seconds) to multi-agent coordination.
//
// The receiving agent's driver subscribes to its `agentBusKey` channel
// and re-renders the chat surface immediately on push, so the message
// preview surfaces inline alongside the actual A2A turn that the host
// runner delivers a moment later. The event is informational — it
// records that an inbound A2A message was received and gives the
// receiver's UI a hook to badge the session.
//
// Best-effort:
//   - no live session for the receiver → skip (audit row remains)
//   - DB errors → logged at warn
//   - empty body → still push (the event records arrival; consumers
//     can decide how to render an empty preview)
//
// fromHandle / fromAgentID may be empty when the peer's bearer didn't
// resolve to a known agent (e.g. an external A2A caller). Mirror the
// audit row's "actor_kind='peer'" semantic — body still says "A2A
// message received" with whatever attribution was available.
func (s *Server) notifyA2AReceived(ctx context.Context, recvAgentID string, body []byte, fromHandle, fromAgentID string) {
	if recvAgentID == "" {
		return
	}
	var teamID string
	err := s.db.QueryRowContext(ctx,
		`SELECT team_id FROM agents WHERE id = ?`, recvAgentID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		s.log.Warn("notify a2a received: team lookup",
			"recv_agent_id", recvAgentID, "err", err)
		return
	}
	var sessionID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		  FROM sessions
		 WHERE team_id = ? AND current_agent_id = ? AND status = 'active'
		 ORDER BY last_active_at DESC
		 LIMIT 1`, teamID, recvAgentID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) || sessionID == "" {
		return
	}
	if err != nil {
		s.log.Warn("notify a2a received: session lookup",
			"recv_agent_id", recvAgentID, "err", err)
		return
	}

	preview := previewA2ABody(body)
	rendered := a2aNotifyBody(fromHandle, fromAgentID, preview)
	payload := map[string]any{
		"from_handle":   fromHandle,
		"from_agent_id": fromAgentID,
		"preview":       preview,
		"body":          rendered,
	}
	payloadBytes, _ := json.Marshal(payload)
	id := NewID()
	ts := NowUTC()
	var seq int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, 'a2a.received', 'system', ?, ?
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, recvAgentID, ts, string(payloadBytes), sessionID, recvAgentID).
		Scan(&seq)
	if err != nil {
		s.log.Warn("notify a2a received: insert event",
			"recv_agent_id", recvAgentID, "err", err)
		return
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(recvAgentID), map[string]any{
		"id":         id,
		"agent_id":   recvAgentID,
		"seq":        seq,
		"ts":         ts,
		"kind":       "a2a.received",
		"producer":   "system",
		"payload":    json.RawMessage(payloadBytes),
		"session_id": sessionID,
	})
}

// a2aNotifyBody formats the inline chat preview. "A2A from @sender:
// <text>" when sender is known; "A2A peer message: <text>" otherwise.
// Empty preview produces "A2A from @sender." with no trailing colon.
func a2aNotifyBody(fromHandle, fromAgentID, preview string) string {
	var b strings.Builder
	b.WriteString("A2A ")
	switch {
	case fromHandle != "":
		b.WriteString("from @")
		b.WriteString(strings.TrimPrefix(fromHandle, "@"))
	case fromAgentID != "":
		b.WriteString("from `")
		b.WriteString(fromAgentID)
		b.WriteString("`")
	default:
		b.WriteString("peer message")
	}
	preview = strings.TrimSpace(preview)
	if preview != "" {
		b.WriteString(": ")
		b.WriteString(preview)
	} else {
		b.WriteString(".")
	}
	return b.String()
}

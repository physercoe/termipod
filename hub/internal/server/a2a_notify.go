package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// notifyA2ASent posts a kind='a2a.sent' producer='system' event into the
// SENDING agent's most-recent active session every time an A2A relay
// successfully delivers (status < 400). Closes the gap where the sender
// (e.g. the general steward calling a2a.invoke) had no in-chat trace of
// what it just dispatched — the MCP call returns the receiver's reply
// synchronously, but the sender's session showed nothing for the
// outbound turn.
//
// The receiver does NOT need a sibling notification: the host-runner's
// a2aHubDispatcher already POSTs the message body to the hub as an
// input.text producer='a2a' event, so the receiver sees the actual A2A
// turn render in its chat as a real user message. A receiver-side
// banner on top of that would just double-render the same content.
//
// Best-effort:
//   - sender unknown (peer call without a forwarded bearer) → skip
//     (audit row remains the only trace; the sender isn't ours to ping)
//   - no live session for the sender → skip
//   - DB errors → logged at warn
//
// recvHandle / recvAgentID identify the destination for the rendered
// chat preview. When recvHandle is empty (legacy / un-handle'd agent),
// the body falls back to the agent_id.
func (s *Server) notifyA2ASent(ctx context.Context, senderAgentID string, body []byte, recvHandle, recvAgentID string) {
	if senderAgentID == "" {
		return
	}
	var teamID string
	err := s.db.QueryRowContext(ctx,
		`SELECT team_id FROM agents WHERE id = ?`, senderAgentID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		s.log.Warn("notify a2a sent: team lookup",
			"sender_agent_id", senderAgentID, "err", err)
		return
	}
	var sessionID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		  FROM sessions
		 WHERE team_id = ? AND current_agent_id = ? AND status = 'active'
		 ORDER BY last_active_at DESC
		 LIMIT 1`, teamID, senderAgentID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) || sessionID == "" {
		return
	}
	if err != nil {
		s.log.Warn("notify a2a sent: session lookup",
			"sender_agent_id", senderAgentID, "err", err)
		return
	}

	preview := previewA2ABody(body)
	rendered := a2aSentBody(recvHandle, recvAgentID, preview)
	payload := map[string]any{
		"to_handle":   recvHandle,
		"to_agent_id": recvAgentID,
		"preview":     preview,
		"body":        rendered,
	}
	payloadBytes, _ := json.Marshal(payload)
	id := NewID()
	ts := NowUTC()
	var seq int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, 'a2a.sent', 'system', ?, ?
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, senderAgentID, ts, string(payloadBytes), sessionID, senderAgentID).
		Scan(&seq)
	if err != nil {
		s.log.Warn("notify a2a sent: insert event",
			"sender_agent_id", senderAgentID, "err", err)
		return
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(senderAgentID), map[string]any{
		"id":         id,
		"agent_id":   senderAgentID,
		"seq":        seq,
		"ts":         ts,
		"kind":       "a2a.sent",
		"producer":   "system",
		"payload":    json.RawMessage(payloadBytes),
		"session_id": sessionID,
	})
}

// a2aSentBody formats the inline chat preview for the sender's session.
// "→ A2A to @receiver: <text>" when handle known; "→ A2A to `<id>`: <text>"
// when only the agent_id is known. Empty preview → "→ A2A to @receiver."
// with no trailing colon.
func a2aSentBody(recvHandle, recvAgentID, preview string) string {
	var b strings.Builder
	b.WriteString("→ A2A to ")
	switch {
	case recvHandle != "":
		b.WriteString("@")
		b.WriteString(strings.TrimPrefix(recvHandle, "@"))
	case recvAgentID != "":
		b.WriteString("`")
		b.WriteString(recvAgentID)
		b.WriteString("`")
	default:
		b.WriteString("peer")
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

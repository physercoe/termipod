package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// notifyRunOwner posts a kind='run.notify' producer='system' event into
// the owning agent's most-recent active session when a run reaches a
// terminal status (completed / failed / cancelled). Closes the gap
// where ML engineers running sweeps had no push signal — `runs.update
// status='completed'` wrote audit only.
//
// "Owning agent" is `runs.agent_id` — the worker that registered the
// run via `runs.create`. The worker may have async-waited on trackio
// digests and wants to know its sweep finished; the steward learns
// downstream via `task.notify` when the worker closes out the task it
// was assigned (W2.9).
//
// Best-effort. Silent degrade on:
//   - non-terminal `toStatus`
//   - NULL `runs.agent_id` (no owner to push to)
//   - no live session for the agent
//   - DB errors (logged at warn)
//
// Companion to notifyTaskAssigner. Mirrors the wire shape so mobile
// renders both event kinds consistently.
func (s *Server) notifyRunOwner(ctx context.Context, team, runID, toStatus string) {
	switch toStatus {
	case "completed", "failed", "cancelled":
	default:
		return
	}
	var (
		agentID   sql.NullString
		projectID string
		startedAt sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(agent_id, ''), project_id, COALESCE(started_at, '')
		  FROM runs WHERE id = ?`, runID).
		Scan(&agentID, &projectID, &startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		s.log.Warn("notify run owner: run lookup",
			"run_id", runID, "err", err)
		return
	}
	if !agentID.Valid || agentID.String == "" {
		return // standalone run with no owner; nothing to push.
	}
	var sessionID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		  FROM sessions
		 WHERE team_id = ? AND current_agent_id = ? AND status = 'active'
		 ORDER BY last_active_at DESC
		 LIMIT 1`, team, agentID.String).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) || sessionID == "" {
		return // owner has no live chat to deliver into.
	}
	if err != nil {
		s.log.Warn("notify run owner: session lookup",
			"agent_id", agentID.String, "err", err)
		return
	}
	body := runNotifyBody(runID, toStatus, startedAt.String)
	payload := map[string]any{
		"run_id":     runID,
		"project_id": projectID,
		"status":     toStatus,
		"started_at": startedAt.String,
		"body":       body,
	}
	payloadBytes, _ := json.Marshal(payload)
	id := NewID()
	ts := NowUTC()
	var seq int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, 'run.notify', 'system', ?, ?
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, agentID.String, ts, string(payloadBytes), sessionID, agentID.String).
		Scan(&seq)
	if err != nil {
		s.log.Warn("notify run owner: insert event",
			"agent_id", agentID.String, "err", err)
		return
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(agentID.String), map[string]any{
		"id":         id,
		"agent_id":   agentID.String,
		"seq":        seq,
		"ts":         ts,
		"kind":       "run.notify",
		"producer":   "system",
		"payload":    json.RawMessage(payloadBytes),
		"session_id": sessionID,
	})
}

// runNotifyBody formats the inline chat line the agent sees. Short and
// scannable: "Run abc12345 completed." or "Run abc12345 failed."
// Followed by started_at when available so the agent can correlate to
// its earlier `runs.create` call.
func runNotifyBody(runID, toStatus, startedAt string) string {
	var b strings.Builder
	b.WriteString("Run `")
	if len(runID) > 12 {
		b.WriteString(runID[:8])
	} else {
		b.WriteString(runID)
	}
	b.WriteString("` ")
	b.WriteString(toStatus)
	b.WriteString(".")
	startedAt = strings.TrimSpace(startedAt)
	if startedAt != "" {
		b.WriteString(" (started ")
		b.WriteString(startedAt)
		b.WriteString(")")
	}
	return b.String()
}

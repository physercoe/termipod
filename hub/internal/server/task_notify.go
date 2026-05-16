package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// notifyTaskAssigner posts a system-attributed message into the
// assigner's most-recent active session when a task transitions to a
// terminal status (done / blocked / cancelled). This is the W2.9
// up-edge of the task primitive: workers update task state, and the
// steward who delegated the work hears about it inline in chat
// without polling.
//
// Best-effort. Silently degrades on:
//   - non-terminal `toStatus` (auto-derive may run on every agent flip)
//   - missing/NULL `created_by_id` (principal-direct task; principal
//     already sees the change via the mobile UI it triggered)
//   - no active session for the assigner (steward sleeps; the audit
//     row still lands and a chat re-open shows the catch-up)
//   - DB errors (logged at warn)
//
// Callers: handlePatchTask (after the audit) and
// deriveTaskStatusFromAgent (after its UPDATE).
func (s *Server) notifyTaskAssigner(ctx context.Context, team, taskID, fromStatus, toStatus string) {
	switch toStatus {
	case "done", "blocked", "cancelled":
	default:
		return
	}
	var (
		assignerID sql.NullString
		title      string
		summary    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(created_by_id, ''), title, COALESCE(result_summary, '')
		  FROM tasks WHERE id = ?`, taskID).
		Scan(&assignerID, &title, &summary)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		s.log.Warn("notify assigner: task lookup",
			"task_id", taskID, "err", err)
		return
	}
	if !assignerID.Valid || assignerID.String == "" {
		return // principal-direct task; nothing to push.
	}
	// Find the assigner's most-recent active session for the team.
	var sessionID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id
		  FROM sessions
		 WHERE team_id = ? AND current_agent_id = ? AND status = 'active'
		 ORDER BY last_active_at DESC
		 LIMIT 1`, team, assignerID.String).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) || sessionID == "" {
		return // no live chat to deliver into.
	}
	if err != nil {
		s.log.Warn("notify assigner: session lookup",
			"assigner_id", assignerID.String, "err", err)
		return
	}

	body := taskNotifyBody(title, fromStatus, toStatus, summary.String)
	payload := map[string]any{
		"task_id":        taskID,
		"title":          title,
		"from":           fromStatus,
		"to":             toStatus,
		"result_summary": summary.String,
		"body":           body,
	}
	payloadBytes, _ := json.Marshal(payload)
	id := NewID()
	ts := NowUTC()
	var seq int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, 'task.notify', 'system', ?, ?
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, assignerID.String, ts, string(payloadBytes), sessionID, assignerID.String).
		Scan(&seq)
	if err != nil {
		s.log.Warn("notify assigner: insert event",
			"assigner_id", assignerID.String, "err", err)
		return
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(assignerID.String), map[string]any{
		"id":         id,
		"agent_id":   assignerID.String,
		"seq":        seq,
		"ts":         ts,
		"kind":       "task.notify",
		"producer":   "system",
		"payload":    json.RawMessage(payloadBytes),
		"session_id": sessionID,
	})
}

// taskNotifyBody formats the inline chat message the steward sees. The
// shape mirrors how a human would summarise the change: title, state
// arrow, and the worker's optional summary on a new line.
func taskNotifyBody(title, from, to, summary string) string {
	var b strings.Builder
	b.WriteString("Task ")
	if title != "" {
		b.WriteString("**")
		b.WriteString(title)
		b.WriteString("** ")
	}
	if from != "" {
		b.WriteString(from)
		b.WriteString(" → ")
	}
	b.WriteString(to)
	b.WriteString(".")
	summary = strings.TrimSpace(summary)
	if summary != "" {
		b.WriteString("\n\n")
		b.WriteString(summary)
	}
	return b.String()
}

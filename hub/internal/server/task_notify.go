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
	// ADR-034 D-5: the PostDirectiveOutcome hook gates a directive's
	// close — it flags a bare-relay close on a root task. Runs before
	// the assigner-delivery short-circuit so a principal-direct directive
	// is checked too.
	if toStatus == "done" {
		s.onPostDirectiveOutcome(ctx, taskID, summary.String)
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
	id, seq, _, ts, err := insertAgentEvent(ctx, s.eventsWriteDB, agentEventInsert{
		AgentID:     assignerID.String,
		SessionID:   sessionID,
		Kind:        "task.notify",
		Producer:    "system",
		PayloadJSON: string(payloadBytes),
	})
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

	// W5 (corrected v1.0.626): also emit an `input.text` event so the
	// InputRouter dispatches a fresh turn to the steward's engine.
	// Pre-W5 the steward saw the card on mobile but its engine never
	// received it — InputRouter filtered system-producer events. W5
	// (v1.0.611) added the allowlist and emitted `input.task_completed`
	// — but no driver handles that kind. Every driver's switch
	// (driver_appserver.go, driver_pane.go, driver_exec_resume.go,
	// claude_code/sendkeys.go) falls through to `default:` and returns
	// "unsupported input kind". Same single-boundary validation gap
	// as v1.0.619: the DB-level unit test passed; the driver-dispatch
	// path was never exercised end-to-end.
	//
	// Corrective fix: use `input.text` with the canonical `body`
	// payload field every driver's text branch already reads. The
	// task_id / task_title / from / to sidecar fields stay for
	// inspection; the body is the actual text delivered to the
	// engine. For blocked / cancelled transitions the body reflects
	// the real status — previously the W5 body always said
	// "completed" regardless of toStatus.
	//
	// Audit trail unaffected: the `task.notify` event (first INSERT
	// above) is still the render-only card mobile shows on the
	// steward feed. The `input.text` event is purely the wake
	// mechanism; producer=system + the sidecar fields preserve the
	// origin for debug/audit.
	// ADR-032: the wake event carries the message envelope as its flat
	// payload — a system-from notification, caused by the task. The status
	// sidecars are renamed from/to → from_status/to_status so they don't
	// collide with the envelope's from/to endpoint objects.
	inputBody := taskOutcomeInputBody(title, summary.String, toStatus)
	env := composeMessage(
		systemEndpoint(), s.endpointForAgent(ctx, assignerID.String),
		KindNotification, inputBody, taskID,
		MessageThread{Transport: TransportSession, ID: sessionID})
	if ae := s.admitEnvelope(ctx, env, false); ae != nil {
		// Hub-composed — a failure here is a programming error. The
		// render-only task.notify card already landed; skip the wake.
		s.log.Error("task notify: envelope admission failed",
			"stage", ae.Stage, "reason", ae.Reason, "task_id", taskID)
		return
	}
	inputPayload := env.PayloadMap()
	inputPayload["task_id"] = taskID
	inputPayload["task_title"] = title
	inputPayload["result_summary"] = summary.String
	inputPayload["from_status"] = fromStatus
	inputPayload["to_status"] = toStatus
	inputBytes, _ := json.Marshal(inputPayload)
	inputID, inputSeq, _, inputTS, err := insertAgentEvent(ctx, s.eventsWriteDB, agentEventInsert{
		AgentID:     assignerID.String,
		SessionID:   sessionID,
		Kind:        "input.text",
		Producer:    "system",
		PayloadJSON: string(inputBytes),
	})
	if err != nil {
		// Best-effort: render-only path already fired. Log and move on.
		s.log.Warn("notify assigner: insert input.text (task outcome)",
			"assigner_id", assignerID.String, "err", err)
		return
	}
	s.bus.Publish(agentBusKey(assignerID.String), map[string]any{
		"id":         inputID,
		"agent_id":   assignerID.String,
		"seq":        inputSeq,
		"ts":         inputTS,
		"kind":       "input.text",
		"producer":   "system",
		"payload":    json.RawMessage(inputBytes),
		"session_id": sessionID,
	})
}

// taskOutcomeInputBody is the short input.text body the steward
// receives after a worker transitions a delegated task to a terminal
// status. Minimal imperative; steward decides next step via natural
// reasoning. The verb reflects toStatus so blocked / cancelled
// tasks read accurately (pre-v1.0.626 always said "completed").
func taskOutcomeInputBody(title, summary, toStatus string) string {
	var b strings.Builder
	b.WriteString("Task ")
	if title != "" {
		b.WriteString("'")
		b.WriteString(title)
		b.WriteString("' ")
	}
	switch toStatus {
	case "done":
		b.WriteString("completed.")
	case "blocked":
		b.WriteString("blocked.")
	case "cancelled":
		b.WriteString("cancelled.")
	default:
		// Defensive: notifyTaskAssigner already gates on these three.
		b.WriteString("status=")
		b.WriteString(toStatus)
		b.WriteString(".")
	}
	summary = strings.TrimSpace(summary)
	if summary != "" {
		switch toStatus {
		case "blocked":
			b.WriteString(" Reason: ")
		default:
			b.WriteString(" Result: ")
		}
		b.WriteString(summary)
	}
	b.WriteString(" Decide next step.")
	return b.String()
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

package server

import (
	"context"
	"encoding/json"
	"time"
)

// The loop-closure reconcile sweep (ADR-034 D-2 / D-3 / D-4).
//
// A single hub-server goroutine ticks every loopSweepInterval, scans the
// open loop-entities, and reconciles each against its per-hop deadlines:
// an inactivity breach escalates the stall one level up the chain; an
// absolute-cap breach terminates the entity `timed_out`. Deadline state
// lives in persisted columns (B1), so a hub restart loses nothing — the
// next tick re-derives. This is the reconcile-loop pattern, modelled on
// host_sweep.go; it fires whether or not anyone is watching, which is
// the structural guarantee against a silently stalled hop.

const (
	// loopSweepInterval must be ≪ the smallest deadline so detection lag
	// is at most one tick (ADR-034 D-3).
	loopSweepInterval = 45 * time.Second

	// Default per-hop budgets. ADR-034 D-2 says these come from the agent
	// family / template; calibration is post-MVP — these are the bundled
	// defaults a loop-entity is stamped with on first sight.
	loopInactivityBudget  = 20 * time.Minute
	loopAbsoluteCapBudget = 2 * time.Hour
)

// loopTSFormat is a fixed-width UTC timestamp layout. Unlike RFC3339Nano
// (which trims trailing fractional zeros) it is safe to compare
// lexically — every loop-deadline column is written and compared in this
// one format, so the sweep never needs to parse a time.
const loopTSFormat = "2006-01-02T15:04:05.000000000Z07:00"

func loopTS(t time.Time) string { return t.UTC().Format(loopTSFormat) }

// loopTable returns the backing table for a loop-entity source. The
// source is hub-internal (set by openLoopEntities), never user input —
// safe to interpolate.
func loopTable(source string) string {
	if source == LoopSourceQuestion {
		return "attention_items"
	}
	return "tasks"
}

func loopTargetKind(e LoopEntity) string {
	if e.Source == LoopSourceQuestion {
		return "attention_item"
	}
	return "task"
}

// runLoopSweep ticks the reconcile sweep until ctx is cancelled.
func (s *Server) runLoopSweep(ctx context.Context) {
	t := time.NewTicker(loopSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepLoopOnce(ctx)
		}
	}
}

// sweepLoopOnce reconciles every open loop-entity against its deadlines.
func (s *Server) sweepLoopOnce(ctx context.Context) {
	ents, err := s.openLoopEntities(ctx)
	if err != nil {
		s.log.Warn("loop sweep: list open entities failed", "err", err)
		return
	}
	now := loopTS(time.Now().UTC())
	for _, e := range ents {
		// An entity not yet stamped with deadlines is stamped on first
		// sight and reconciled from the next tick on.
		if e.OpenedAt == "" {
			s.stampLoopDeadlines(ctx, e)
			continue
		}
		// A parked hop (a blocked task awaiting a human) is not a
		// stalled hop — its deadlines pause (ADR-034 D-2).
		if isLoopParked(e) {
			continue
		}
		if e.AbsoluteCap != "" && now > e.AbsoluteCap {
			s.terminateLoopTimedOut(ctx, e)
			continue
		}
		if e.InactivityDeadline != "" && now > e.InactivityDeadline {
			s.escalateStall(ctx, e)
		}
	}
}

// isLoopParked reports whether an entity's deadlines should pause. A
// blocked task is parked awaiting intervention; a question is always a
// live loop-entity (an unanswered question is precisely a stall).
func isLoopParked(e LoopEntity) bool {
	return e.Source == LoopSourceTask && e.State == "blocked"
}

// stampLoopDeadlines stamps an entity's per-hop deadline columns on first
// sight — opened/last-progress now, the inactivity and absolute-cap
// deadlines at now + the default budgets.
func (s *Server) stampLoopDeadlines(ctx context.Context, e LoopEntity) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE `+loopTable(e.Source)+`
		   SET opened_at = ?, last_progress_at = ?,
		       inactivity_deadline = ?, absolute_cap = ?
		 WHERE id = ?`,
		loopTS(now), loopTS(now),
		loopTS(now.Add(loopInactivityBudget)), loopTS(now.Add(loopAbsoluteCapBudget)),
		e.ID)
	if err != nil {
		s.log.Warn("loop sweep: stamp deadlines failed", "id", e.ID, "err", err)
	}
}

// bumpLoopProgress slides an open task's inactivity deadline forward — it
// is alive even if slow as long as it is producing events. Called when
// an agent event lands for the entity's assignee. Best-effort.
func (s *Server) bumpLoopProgress(ctx context.Context, agentID string) {
	if agentID == "" {
		return
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		   SET last_progress_at = ?, inactivity_deadline = ?, escalation_state = 'none'
		 WHERE assignee_id = ? AND status NOT IN ('done', 'cancelled')
		   AND opened_at IS NOT NULL`,
		loopTS(now), loopTS(now.Add(loopInactivityBudget)), agentID)
	if err != nil {
		s.log.Warn("loop sweep: bump progress failed", "agent", agentID, "err", err)
	}
}

// escalateStall advances a stalled entity's escalation one level — the
// no-silent-sink guarantee (ADR-034 D-4). Idempotent: once at the
// principal level it never re-fires, bounding notification volume. The
// inactivity deadline is pushed forward a budget so the next level fires
// after another window rather than on the next tick.
func (s *Server) escalateStall(ctx context.Context, e LoopEntity) {
	var next string
	switch e.EscalationState {
	case EscalationNone:
		next = EscalationSteward
	case EscalationSteward:
		next = EscalationPrincipal
	default:
		return // already escalated to the principal — nothing further.
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE `+loopTable(e.Source)+`
		   SET escalation_state = ?, inactivity_deadline = ?
		 WHERE id = ?`,
		next, loopTS(time.Now().UTC().Add(loopInactivityBudget)), e.ID)
	if err != nil {
		s.log.Warn("loop sweep: escalate failed", "id", e.ID, "err", err)
		return
	}
	s.recordAudit(ctx, s.teamForLoopEntity(ctx, e), "loop.stall_escalated",
		loopTargetKind(e), e.ID,
		"loop-entity stalled — escalated to "+next,
		map[string]any{"source": e.Source, "level": next, "project_id": e.ProjectID})
	// A worker stall (none → steward) wakes the steward who owns it; a
	// steward stall (steward → principal) surfaces to the principal via
	// the audit row above and the directive trace (B4).
	if next == EscalationSteward {
		s.wakeStewardForStall(ctx, e)
	}
}

// terminateLoopTimedOut closes an entity that breached its absolute cap
// with terminal_reason=timed_out (ADR-034 D-6). A task moves to the
// cancelled umbrella status; a question is resolved.
func (s *Server) terminateLoopTimedOut(ctx context.Context, e LoopEntity) {
	var err error
	if e.Source == LoopSourceQuestion {
		_, err = s.db.ExecContext(ctx, `
			UPDATE attention_items
			   SET status = 'resolved', terminal_reason = ?, resolved_at = ?
			 WHERE id = ?`, TerminalTimedOut, NowUTC(), e.ID)
	} else {
		_, err = s.db.ExecContext(ctx, `
			UPDATE tasks
			   SET status = 'cancelled', terminal_reason = ?,
			       completed_at = ?, updated_at = ?
			 WHERE id = ?`, TerminalTimedOut, NowUTC(), NowUTC(), e.ID)
	}
	if err != nil {
		s.log.Warn("loop sweep: timed-out termination failed", "id", e.ID, "err", err)
		return
	}
	s.recordAudit(ctx, s.teamForLoopEntity(ctx, e), "loop.timed_out",
		loopTargetKind(e), e.ID, "loop-entity exceeded its absolute cap",
		map[string]any{"source": e.Source, "project_id": e.ProjectID})
}

// teamForLoopEntity resolves the team that owns a loop-entity's project,
// for the audit row. Returns "" when it can't be resolved (recordAudit
// then no-ops).
func (s *Server) teamForLoopEntity(ctx context.Context, e LoopEntity) string {
	if e.ProjectID == "" {
		return ""
	}
	var team string
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(team_id, '') FROM projects WHERE id = ?`, e.ProjectID).Scan(&team)
	return team
}

// wakeStewardForStall delivers a system notification envelope into the
// steward who owns the stalled entity (its created_by), so the steward's
// engine wakes and can re-drive or reassign. Best-effort.
func (s *Server) wakeStewardForStall(ctx context.Context, e LoopEntity) {
	if e.CreatedByID == "" {
		return
	}
	sessionID := s.lookupSessionForAgent(ctx, e.CreatedByID)
	text := "A loop-entity you own has stalled — no progress past its deadline. " +
		"Check on it, then re-drive or reassign it."
	env := composeMessage(systemEndpoint(), s.endpointForAgent(ctx, e.CreatedByID),
		KindNotification, text, e.ID,
		MessageThread{Transport: TransportSession, ID: sessionID})
	if ae := s.admitEnvelope(ctx, env, false); ae != nil {
		s.log.Warn("loop sweep: stall notification rejected",
			"stage", ae.Stage, "reason", ae.Reason)
		return
	}
	payload, _ := json.Marshal(env.PayloadMap())
	id := NewID()
	ts := NowUTC()
	var seq int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, 'input.text', 'system', ?, NULLIF(?, '')
		  FROM agent_events WHERE agent_id = ?
		RETURNING seq`,
		id, e.CreatedByID, ts, string(payload), sessionID, e.CreatedByID).Scan(&seq)
	if err != nil {
		s.log.Warn("loop sweep: stall notification insert failed", "id", e.ID, "err", err)
		return
	}
	s.touchSession(ctx, sessionID)
	s.bus.Publish(agentBusKey(e.CreatedByID), map[string]any{
		"id": id, "agent_id": e.CreatedByID, "seq": seq, "ts": ts,
		"kind": "input.text", "producer": "system",
		"payload": json.RawMessage(payload), "session_id": sessionID,
	})
}

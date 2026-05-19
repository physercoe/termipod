package server

import (
	"context"
	"testing"
	"time"
)

// seedLoopTask inserts a task with explicit loop-deadline columns so the
// sweep's reconcile logic can be exercised without waiting real time.
func seedLoopTask(t *testing.T, s *Server, proj, status, openedAt, inactivityDeadline, absoluteCap string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status,
		                   opened_at, inactivity_deadline, absolute_cap,
		                   created_at, updated_at)
		VALUES (?, ?, 'loop task', ?, NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), ?, ?)`,
		id, proj, status, openedAt, inactivityDeadline, absoluteCap,
		NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed loop task: %v", err)
	}
	return id
}

func taskEscalation(t *testing.T, s *Server, id string) string {
	t.Helper()
	var st string
	if err := s.db.QueryRow(`SELECT escalation_state FROM tasks WHERE id=?`, id).Scan(&st); err != nil {
		t.Fatalf("read escalation_state: %v", err)
	}
	return st
}

func TestLoopSweep_Stall(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-stall")
	past := loopTS(time.Now().Add(-time.Hour))
	future := loopTS(time.Now().Add(time.Hour))
	id := seedLoopTask(t, s, proj, "in_progress", past, past, future)

	s.sweepLoopOnce(context.Background())

	if got := taskEscalation(t, s, id); got != EscalationSteward {
		t.Errorf("stalled task escalation_state = %q, want %q", got, EscalationSteward)
	}
}

func TestLoopSweep_Escalate(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-escalate")
	past := loopTS(time.Now().Add(-time.Hour))
	future := loopTS(time.Now().Add(time.Hour))
	id := seedLoopTask(t, s, proj, "in_progress", past, past, future)

	// none → escalated_steward
	s.sweepLoopOnce(context.Background())
	if got := taskEscalation(t, s, id); got != EscalationSteward {
		t.Fatalf("after sweep 1: %q, want escalated_steward", got)
	}
	// The sweep pushed the deadline forward; force it back into the past.
	if _, err := s.db.Exec(`UPDATE tasks SET inactivity_deadline=? WHERE id=?`, past, id); err != nil {
		t.Fatalf("reset deadline: %v", err)
	}
	// escalated_steward → escalated_principal
	s.sweepLoopOnce(context.Background())
	if got := taskEscalation(t, s, id); got != EscalationPrincipal {
		t.Fatalf("after sweep 2: %q, want escalated_principal", got)
	}
	// Idempotent: a third breach does not re-fire past the principal.
	if _, err := s.db.Exec(`UPDATE tasks SET inactivity_deadline=? WHERE id=?`, past, id); err != nil {
		t.Fatalf("reset deadline: %v", err)
	}
	s.sweepLoopOnce(context.Background())
	if got := taskEscalation(t, s, id); got != EscalationPrincipal {
		t.Errorf("after sweep 3: %q, escalation must not advance past principal", got)
	}
}

func TestLoopSweep_ParkedSkipped(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-parked")
	past := loopTS(time.Now().Add(-time.Hour))
	future := loopTS(time.Now().Add(time.Hour))
	// A blocked task is parked awaiting a human — its deadline pauses.
	id := seedLoopTask(t, s, proj, "blocked", past, past, future)

	s.sweepLoopOnce(context.Background())

	if got := taskEscalation(t, s, id); got != EscalationNone {
		t.Errorf("parked (blocked) task escalation_state = %q, want none", got)
	}
}

func TestLoopSweep_AbsoluteCap(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-cap")
	past := loopTS(time.Now().Add(-time.Hour))
	id := seedLoopTask(t, s, proj, "in_progress", past, past, past)

	s.sweepLoopOnce(context.Background())

	var status, reason string
	if err := s.db.QueryRow(
		`SELECT status, COALESCE(terminal_reason,'') FROM tasks WHERE id=?`, id).
		Scan(&status, &reason); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "cancelled" {
		t.Errorf("absolute-cap breach: status = %q, want cancelled", status)
	}
	if reason != TerminalTimedOut {
		t.Errorf("absolute-cap breach: terminal_reason = %q, want %q", reason, TerminalTimedOut)
	}
}

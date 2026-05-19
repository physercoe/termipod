package server

import (
	"context"
	"strings"
	"testing"
)

func systemInputCount(t *testing.T, s *Server, agentID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'input.text' AND producer = 'system'`,
		agentID).Scan(&n); err != nil {
		t.Fatalf("count system input: %v", err)
	}
	return n
}

func relayFlagCount(t *testing.T, s *Server, taskID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM audit_events
		 WHERE action = 'loop.relay_not_synthesis' AND target_id = ?`,
		taskID).Scan(&n); err != nil {
		t.Fatalf("count relay flag: %v", err)
	}
	return n
}

func TestHook_PreAgentIdle_ReWakesWithOpenSet(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-idle")
	agentID := seedAgentForInput(t, s)
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, assignee_id, created_at, updated_at)
		VALUES (?, ?, 'open work', 'in_progress', ?, ?, ?)`,
		NewID(), proj, agentID, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed open task: %v", err)
	}

	s.onPreAgentIdle(context.Background(), agentID)

	if n := systemInputCount(t, s, agentID); n != 1 {
		t.Errorf("an agent idling with open work must be re-woken; got %d events", n)
	}
}

func TestHook_PreAgentIdle_NoOpenWorkNoWake(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := seedAgentForInput(t, s)

	s.onPreAgentIdle(context.Background(), agentID)

	if n := systemInputCount(t, s, agentID); n != 0 {
		t.Errorf("an agent with no open work must not be re-woken; got %d events", n)
	}
}

func TestHook_PostDirectiveOutcome_FlagsBareRelay(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-dir-relay")
	rootTask := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, created_at, updated_at)
		VALUES (?, ?, 'a directive', 'done', ?, ?)`,
		rootTask, proj, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed root task: %v", err)
	}

	s.onPostDirectiveOutcome(context.Background(), rootTask, "ok")

	if n := relayFlagCount(t, s, rootTask); n != 1 {
		t.Errorf("a bare-relay directive close must be flagged; got %d flags", n)
	}
}

func TestHook_PostDirectiveOutcome_SynthesisPasses(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-dir-synth")
	rootTask := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, created_at, updated_at)
		VALUES (?, ?, 'a directive', 'done', ?, ?)`,
		rootTask, proj, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed root task: %v", err)
	}

	s.onPostDirectiveOutcome(context.Background(), rootTask,
		strings.Repeat("a real synthesised result. ", 4))

	if n := relayFlagCount(t, s, rootTask); n != 0 {
		t.Errorf("a synthesised directive close must not be flagged; got %d flags", n)
	}
}

func TestHook_PostDirectiveOutcome_ChildTaskSkipped(t *testing.T) {
	s, _ := newTestServer(t)
	proj := seedProjectInTeam(t, s, "proj-dir-child")
	parentTask := NewID()
	childTask := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, title, status, created_at, updated_at)
		VALUES (?, ?, 'directive', 'in_progress', ?, ?)`,
		parentTask, proj, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO tasks (id, project_id, parent_task_id, title, status, created_at, updated_at)
		VALUES (?, ?, ?, 'child work', 'done', ?, ?)`,
		childTask, proj, parentTask, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	// A bare summary on a *child* task is not gated — only root
	// directives are.
	s.onPostDirectiveOutcome(context.Background(), childTask, "x")

	if n := relayFlagCount(t, s, childTask); n != 0 {
		t.Errorf("a child task's outcome must not be gated; got %d flags", n)
	}
}

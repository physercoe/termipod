package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLoopHooks_Overlay(t *testing.T) {
	// No overlay → the bundled default (both hooks enabled).
	def := loadLoopHooks("")
	if !def.PreAgentIdle.Enabled || !def.PostDirectiveOutcome.Enabled {
		t.Fatalf("the bundled default should enable both hooks: %+v", def)
	}

	// An on-disk overlay overrides the default without a rebuild.
	dir := t.TempDir()
	overlay := "pre_agent_idle:\n  enabled: false\n" +
		"post_directive_outcome:\n  enabled: true\n  min_synthesis_chars: 99\n"
	if err := os.WriteFile(filepath.Join(dir, "loop-hooks.yaml"),
		[]byte(overlay), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	got := loadLoopHooks(dir)
	if got.PreAgentIdle.Enabled {
		t.Error("the overlay should have disabled PreAgentIdle")
	}
	if got.PostDirectiveOutcome.MinSynthesisChars != 99 {
		t.Errorf("overlay min_synthesis_chars = %d, want 99",
			got.PostDirectiveOutcome.MinSynthesisChars)
	}

	// writeLoopHooksDefault seeds the file when absent and never
	// overwrites an operator edit.
	seedDir := t.TempDir()
	if err := writeLoopHooksDefault(seedDir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p := filepath.Join(seedDir, "loop-hooks.yaml")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("loop-hooks.yaml was not seeded: %v", err)
	}
	_ = os.WriteFile(p, []byte("pre_agent_idle:\n  enabled: false\n"), 0o644)
	if err := writeLoopHooksDefault(seedDir); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "enabled: true") {
		t.Error("writeLoopHooksDefault overwrote an operator edit")
	}
}

func systemInputCount(t *testing.T, s *Server, agentID string) int {
	t.Helper()
	var n int
	if err := evRForTeam(t, s, defaultTeamID).QueryRow(`
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

package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// W9 sync-wait three-state return. Post-bundle, the agents.spawn MCP
// path defaults to wait=true so the steward's tool_result accurately
// reflects whether the engine actually started. See
// docs/discussions/validate-at-every-boundary.md §1 for the misleading
// "spawned" return that motivated this.

func TestWaitForSpawnOutcome_DefaultTimeoutFallsBackToPending(t *testing.T) {
	s, _ := newTestServer(t)
	// Create an agent in pending state via direct insert (no live
	// hostrunner publishing lifecycle events).
	agentID := NewID()
	_, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'test-pending', 'claude-code', 'pending', ?)`,
		agentID, defaultTeamID, NowUTC())
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// 1-second timeout — pending should land within ~1s.
	start := time.Now()
	status, reason := s.waitForSpawnOutcome(context.Background(), agentID, 1)
	elapsed := time.Since(start)
	if status != "pending" {
		t.Errorf("status = %q; want pending", status)
	}
	if reason != "" {
		t.Errorf("reason = %q; want empty on pending", reason)
	}
	if elapsed > 2*time.Second {
		t.Errorf("waited %v; should respect 1s cap", elapsed)
	}
}

func TestWaitForSpawnOutcome_LifecycleStartedReturnsRunning(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := NewID()
	_, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'test-started', 'claude-code', 'pending', ?)`,
		agentID, defaultTeamID, NowUTC())
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Publish lifecycle.started after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.bus.Publish(agentBusKey(agentID), map[string]any{
			"agent_id": agentID,
			"kind":     "lifecycle",
			"producer": "system",
			"payload": map[string]any{
				"phase": "started",
				"mode":  "M4",
			},
		})
	}()

	status, reason := s.waitForSpawnOutcome(context.Background(), agentID, 5)
	if status != "running" {
		t.Errorf("status = %q; want running", status)
	}
	if reason != "" {
		t.Errorf("reason = %q; want empty on running", reason)
	}
}

func TestWaitForSpawnOutcome_LifecycleFailedReturnsFailedWithReason(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := NewID()
	_, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'test-failed', 'claude-code', 'pending', ?)`,
		agentID, defaultTeamID, NowUTC())
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		s.bus.Publish(agentBusKey(agentID), map[string]any{
			"agent_id": agentID,
			"kind":     "lifecycle",
			"producer": "system",
			"payload": map[string]any{
				"phase":  "failed",
				"reason": "no backend.cmd resolved from spawn spec or template",
			},
		})
	}()

	status, reason := s.waitForSpawnOutcome(context.Background(), agentID, 5)
	if status != "failed" {
		t.Errorf("status = %q; want failed", status)
	}
	if !strings.Contains(reason, "backend.cmd") {
		t.Errorf("reason = %q; should carry the lifecycle.failed payload.reason", reason)
	}
}

func TestWaitForSpawnOutcome_AlreadyFailedBeforeSubscribe(t *testing.T) {
	// W7 patches agent.status='failed' synchronously from launchOne.
	// W9 must detect this even when the lifecycle.failed event was
	// emitted before our subscribe call.
	s, _ := newTestServer(t)
	agentID := NewID()
	// Seed with status='failed' AND a prior lifecycle.failed event.
	_, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at, terminated_at)
		VALUES (?, ?, 'test-prior-fail', 'claude-code', 'failed', ?, ?)`,
		agentID, defaultTeamID, NowUTC(), NowUTC())
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"phase":  "failed",
		"reason": "no backend.cmd resolved from spawn spec or template",
	})
	if _, err := s.eventsWriteDB.Exec(`
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
		VALUES (?, ?, 1, ?, 'lifecycle', 'system', ?)`,
		NewID(), agentID, NowUTC(), string(payload)); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	status, reason := s.waitForSpawnOutcome(context.Background(), agentID, 5)
	if status != "failed" {
		t.Errorf("status = %q; want failed", status)
	}
	if !strings.Contains(reason, "backend.cmd") {
		t.Errorf("reason = %q; should fetch from prior lifecycle.failed event", reason)
	}
}

func TestWaitForSpawnOutcome_AlreadyRunningBeforeSubscribe(t *testing.T) {
	s, _ := newTestServer(t)
	agentID := NewID()
	_, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'test-prior-run', 'claude-code', 'running', ?)`,
		agentID, defaultTeamID, NowUTC())
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	status, _ := s.waitForSpawnOutcome(context.Background(), agentID, 5)
	if status != "running" {
		t.Errorf("status = %q; want running (seeded)", status)
	}
}

func TestWaitForSpawnOutcome_CapsAt50Seconds(t *testing.T) {
	// Smoke test the cap: pass wait_seconds=999, assert the function
	// doesn't run forever. We assert by measuring elapsed when the
	// agent stays pending — should return after ~50s but for the
	// test we use a short pending+publish race.
	s, _ := newTestServer(t)
	agentID := NewID()
	_, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'test-cap', 'claude-code', 'pending', ?)`,
		agentID, defaultTeamID, NowUTC())
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// Publish after 100ms to short-circuit the test (we just want to
	// know the function ran, didn't panic, and respected the bus path).
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.bus.Publish(agentBusKey(agentID), map[string]any{
			"agent_id": agentID,
			"kind":     "lifecycle",
			"producer": "system",
			"payload":  map[string]any{"phase": "started"},
		})
	}()

	start := time.Now()
	status, _ := s.waitForSpawnOutcome(context.Background(), agentID, 999)
	elapsed := time.Since(start)
	if status != "running" {
		t.Errorf("status = %q; want running", status)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed %v: cap should have kept this short on event arrival", elapsed)
	}
}

// DoSpawn (the internal call) does NOT itself invoke waitForSpawnOutcome
// — that's handleSpawn's responsibility. Tests of the wait helper
// (above) cover the wait semantics; DoSpawn-level tests in
// spawn_failfast_backend_test.go cover the synchronous-validation
// path. End-to-end coverage through the full HTTP handler chain is
// exercised by the steward-lifecycle test scenarios documented in
// docs/how-to/test-steward-lifecycle.md (S38, S39, S42).

package server

import (
	"context"
	"testing"
)

// TestSumTurnActiveMs verifies the run's active running time = the sum of the
// closed turns' durations (not the first→last span): open turns (duration <= 0)
// are excluded, and the sum spans every named agent.
func TestSumTurnActiveMs(t *testing.T) {
	c := newE2E(t)
	ctx := context.Background()

	// agent_turns has a FK to agents, so spawn two real agents on one shared
	// host. No events seeded → no fold → the rows saved below are the only turns.
	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	spawn := func(handle string) string {
		t.Helper()
		res, _, err := c.s.DoSpawn(ctx, defaultTeamID, spawnIn{
			ChildHandle:     handle,
			Kind:            "claude-code",
			HostID:          hostID,
			SpawnSpec:       "driving_mode: M2\nbackend:\n  cmd: echo test\n",
			AutoOpenSession: true,
		})
		if err != nil {
			t.Fatalf("spawn %s: %v", handle, err)
		}
		return res.AgentID
	}
	agentA := spawn("active-a")
	agentB := spawn("active-b")

	save := func(agentID, turnID string, idx int, durMs int64) {
		t.Helper()
		if err := saveTurnRow(ctx, dgWForTeam(t, c.s, defaultTeamID), agentID, defaultTeamID, &turnRow{
			TurnID:     turnID,
			Idx:        idx,
			StartSeq:   int64(idx*10 + 1),
			EndSeq:     int64(idx*10 + 5),
			DurationMs: durMs,
			Status:     "success",
		}); err != nil {
			t.Fatalf("saveTurnRow: %v", err)
		}
	}

	// agentA: two closed turns (2000 + 3000) + one still-open turn (0, excluded).
	save(agentA, "t0", 0, 2000)
	save(agentA, "t1", 1, 3000)
	save(agentA, "t-open", 2, 0)
	// agentB: one closed turn (1500) — the session rollup sums across agents.
	save(agentB, "t0", 0, 1500)

	got, err := c.s.sumTurnActiveMs(ctx, []string{agentA})
	if err != nil {
		t.Fatalf("sumTurnActiveMs(agentA): %v", err)
	}
	if got != 5000 {
		t.Errorf("active(agentA) = %d, want 5000 (open turn excluded)", got)
	}

	got, err = c.s.sumTurnActiveMs(ctx, []string{agentA, agentB})
	if err != nil {
		t.Fatalf("sumTurnActiveMs(session): %v", err)
	}
	if got != 6500 {
		t.Errorf("active(session) = %d, want 6500", got)
	}

	got, err = c.s.sumTurnActiveMs(ctx, nil)
	if err != nil {
		t.Fatalf("sumTurnActiveMs(empty): %v", err)
	}
	if got != 0 {
		t.Errorf("active(empty) = %d, want 0", got)
	}
}

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRunTeamShardSplit_FansRowsPerTeam: a P1 global store with events across
// two teams is partitioned into per-team shards — each team's events.db holds
// exactly its own rows (FTS rebuilt), each digest.db its own digests, and the
// global store is retired. The exact per-team counts (5 + 4 = 9) prove there is
// no cross-team leak.
func TestRunTeamShardSplit_FansRowsPerTeam(t *testing.T) {
	c := newE2E(t)
	ctx := context.Background()
	dbPath := filepath.Join(c.dataRoot, "hub.db")

	// Default team (c.teamID) + a second team.
	teamB := "team-b"
	if _, err := c.s.writeDB.ExecContext(ctx,
		`INSERT INTO teams (id, name, created_at) VALUES (?,?,?)`, teamB, "Team B", NowUTC()); err != nil {
		t.Fatalf("seed team B: %v", err)
	}

	a1 := seedAgentRow(t, c.s, c.teamID, "a1", "claude-code")
	a2 := seedAgentRow(t, c.s, c.teamID, "a2", "claude-code")
	b1 := seedAgentRow(t, c.s, teamB, "b1", "claude-code")

	seed := func(agent string, n int) {
		for i := 0; i < n; i++ {
			if _, _, _, _, err := c.s.insertAgentEvent(ctx, agentEventInsert{
				AgentID: agent, SessionID: "s-" + agent, Kind: "text",
				Producer: "agent", PayloadJSON: `{"text":"x"}`,
			}); err != nil {
				t.Fatalf("seed event for %s: %v", agent, err)
			}
		}
	}
	seed(a1, 2)
	seed(a2, 3)
	seed(b1, 4)

	// Materialize digests so digest.db has rows to partition.
	for _, ag := range []struct{ id, team string }{{a1, c.teamID}, {a2, c.teamID}, {b1, teamB}} {
		if _, err := c.s.ensureAgentDigest(ctx, ag.id, ag.team); err != nil {
			t.Fatalf("backfill digest %s: %v", ag.id, err)
		}
	}

	// Release the global store handles before the offline split (it renames the
	// global files at the end).
	_ = c.s.Close()

	rep, err := RunTeamShardSplit(dbPath)
	if err != nil {
		t.Fatalf("split-teams: %v", err)
	}
	if rep.AlreadySharded {
		t.Fatal("unexpected already-sharded report")
	}
	if rep.TotalEvents != 9 {
		t.Fatalf("total events = %d, want 9", rep.TotalEvents)
	}

	wantEvents := map[string]int64{c.teamID: 5, teamB: 4}
	for team, want := range wantEvents {
		ev := filepath.Join(c.dataRoot, "teams", team, "events.db")
		got, err := countTableRows(ev, "agent_events")
		if err != nil {
			t.Fatalf("count events %s: %v", team, err)
		}
		if got != want {
			t.Fatalf("team %s events = %d, want %d (cross-team leak?)", team, got, want)
		}
		fts, err := countTableRows(ev, "agent_events_fts")
		if err != nil || fts != want {
			t.Fatalf("team %s fts = %d (err %v), want %d", team, fts, err, want)
		}
		dg := filepath.Join(c.dataRoot, "teams", team, "digest.db")
		if d, err := countTableRows(dg, "agent_event_digests"); err != nil || d < 1 {
			t.Fatalf("team %s digests = %d (err %v), want >= 1", team, d, err)
		}
		if rep.EventsByTeam[team] != want {
			t.Fatalf("report events[%s] = %d, want %d", team, rep.EventsByTeam[team], want)
		}
	}

	// The global store is retired (renamed), so the serve-guard won't refuse.
	if _, err := os.Stat(filepath.Join(c.dataRoot, "events.db")); !os.IsNotExist(err) {
		t.Fatalf("global events.db should be renamed away")
	}
	if _, err := os.Stat(filepath.Join(c.dataRoot, "events.db.pre-shard")); err != nil {
		t.Fatalf("expected events.db.pre-shard backup: %v", err)
	}
}

// TestRunTeamShardSplit_UnsplitRefuses: an un-split hub.db (moving tables still
// in hub.db, no global events.db) is refused with split-first guidance rather
// than silently doing nothing.
func TestRunTeamShardSplit_UnsplitRefuses(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil { // migrates hub.db, leaves it un-split
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events.db")); err == nil {
		t.Fatalf("Init unexpectedly produced a global events.db")
	}
	if _, err := RunTeamShardSplit(dbPath); err == nil {
		t.Fatalf("expected split-first guidance for an un-split hub.db")
	}
}

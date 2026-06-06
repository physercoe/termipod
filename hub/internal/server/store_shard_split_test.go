package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunTeamShardSplit_FansRowsPerTeam: a P1 GLOBAL store (the upgrade source —
// one events.db / digest.db beside hub.db) with events across two teams is
// partitioned into per-team shards — each team's events.db holds exactly its own
// rows (FTS rebuilt), each digest.db its own digests, and the global store is
// retired. The exact per-team counts (5 + 4 = 9) prove there is no cross-team
// leak.
//
// The fixture is built OFFLINE (no New() — under P2, New() writes per-team
// directly, so there'd be nothing to migrate): seed the moving tables IN hub.db,
// then RunStoreSplit (the P1 split) to produce the global store RunTeamShardSplit
// upgrades.
func TestRunTeamShardSplit_FansRowsPerTeam(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	teamA, teamB := defaultTeamID, "team-b"

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	now := NowUTC()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed (%s): %v", q, err)
		}
	}
	mustExec(`INSERT INTO teams (id, name, created_at) VALUES (?,?,?)`, teamA, "default", now)
	mustExec(`INSERT INTO teams (id, name, created_at) VALUES (?,?,?)`, teamB, "Team B", now)
	agents := []struct {
		id, team string
		n        int
	}{{"a1", teamA, 2}, {"a2", teamA, 3}, {"b1", teamB, 4}}
	seq := 0
	for _, ag := range agents {
		mustExec(`INSERT INTO agents (id, team_id, handle, kind, created_at) VALUES (?,?,?,?,?)`,
			ag.id, ag.team, ag.id, "claude-code", now)
		for i := 0; i < ag.n; i++ {
			seq++
			mustExec(`INSERT INTO agent_events
				(id, agent_id, seq, ts, kind, producer, payload_json, session_id)
				VALUES (?,?,?,?,?,?,?,?)`,
				NewID(), ag.id, i+1, now, "text", "agent", `{"text":"x"}`, "s-"+ag.id)
		}
		// A minimal digest row per agent so digest.db has rows to partition.
		mustExec(`INSERT INTO agent_event_digests (agent_id, team_id, updated_at) VALUES (?,?,?)`,
			ag.id, ag.team, now)
	}
	db.Close()

	// P1 split: relocate the moving tables out of hub.db into the global
	// events.db / digest.db that RunTeamShardSplit upgrades.
	if _, err := RunStoreSplit(dbPath); err != nil {
		t.Fatalf("P1 split: %v", err)
	}

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

	wantEvents := map[string]int64{teamA: 5, teamB: 4}
	for team, want := range wantEvents {
		ev := filepath.Join(dir, "teams", team, "events.db")
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
		dg := filepath.Join(dir, "teams", team, "digest.db")
		if d, err := countTableRows(dg, "agent_event_digests"); err != nil || d < 1 {
			t.Fatalf("team %s digests = %d (err %v), want >= 1", team, d, err)
		}
		if rep.EventsByTeam[team] != want {
			t.Fatalf("report events[%s] = %d, want %d", team, rep.EventsByTeam[team], want)
		}
	}

	// The global store is retired (renamed), so the serve-guard won't refuse.
	if _, err := os.Stat(filepath.Join(dir, "events.db")); !os.IsNotExist(err) {
		t.Fatalf("global events.db should be renamed away")
	}
	if _, err := os.Stat(filepath.Join(dir, "events.db.pre-shard")); err != nil {
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

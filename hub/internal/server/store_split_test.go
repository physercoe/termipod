package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestSplitStores_RelocatesMovingTables drives the offline physical split end
// to end: seed a realistic run (events + a folded digest + turns) into a
// combined hub.db, run splitStores, and assert the moving tables — with their
// rows and a rebuilt FTS index — now live in events.db / digest.db and are gone
// from hub.db.
func TestSplitStores_RelocatesMovingTables(t *testing.T) {
	c := newE2E(t)
	ctx := context.Background()
	agentID, _ := seedVectorRun(t, c)
	// Materialize the digest + turn rows so digest.db has data to move.
	if _, err := c.s.ensureAgentDigest(ctx, agentID, defaultTeamID); err != nil {
		t.Fatalf("ensureAgentDigest: %v", err)
	}

	count := func(db *sql.DB, table string) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	wantEvents := count(c.s.db, "agent_events")
	wantDigests := count(c.s.db, "agent_event_digests")
	wantTurns := count(c.s.db, "agent_turns")
	if wantEvents == 0 || wantDigests == 0 || wantTurns == 0 {
		t.Fatalf("fixture must seed all moving tables; got events=%d digests=%d turns=%d",
			wantEvents, wantDigests, wantTurns)
	}

	hubPath := filepath.Join(c.dataRoot, "hub.db")
	eventsPath := filepath.Join(c.dataRoot, "events.db")
	digestPath := filepath.Join(c.dataRoot, "digest.db")

	// The split is offline — release the server's file handles first.
	if err := c.s.Close(); err != nil {
		t.Fatalf("close server: %v", err)
	}

	// Guard sees the un-split, populated control DB.
	ctl, err := sql.Open("sqlite", dsnFKOff(hubPath))
	if err != nil {
		t.Fatal(err)
	}
	if has, err := controlHasMovingTables(ctl); err != nil || !has {
		t.Fatalf("controlHasMovingTables=%v err=%v, want true", has, err)
	}
	if n, err := movingTableRowCount(ctl); err != nil || n == 0 {
		t.Fatalf("movingTableRowCount=%d err=%v, want >0", n, err)
	}
	ctl.Close()

	if err := splitStores(hubPath, eventsPath, digestPath); err != nil {
		t.Fatalf("splitStores: %v", err)
	}

	// events.db: rows copied + FTS index rebuilt (one fts row per event).
	ev, err := ensureEventsStore(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(ev, "agent_events"); got != wantEvents {
		t.Errorf("events.db agent_events = %d, want %d", got, wantEvents)
	}
	if got := count(ev, "agent_events_fts"); got != wantEvents {
		t.Errorf("events.db agent_events_fts = %d, want %d (FTS backfill incomplete)", got, wantEvents)
	}
	ev.Close()

	// digest.db: digests + turns copied.
	dg, err := ensureDigestStore(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(dg, "agent_event_digests"); got != wantDigests {
		t.Errorf("digest.db agent_event_digests = %d, want %d", got, wantDigests)
	}
	if got := count(dg, "agent_turns"); got != wantTurns {
		t.Errorf("digest.db agent_turns = %d, want %d", got, wantTurns)
	}
	dg.Close()

	// hub.db no longer holds the moving tables — the serve-guard now reads
	// "already split".
	ctl2, err := sql.Open("sqlite", dsnFKOff(hubPath))
	if err != nil {
		t.Fatal(err)
	}
	defer ctl2.Close()
	if has, err := controlHasMovingTables(ctl2); err != nil || has {
		t.Fatalf("controlHasMovingTables after split=%v err=%v, want false", has, err)
	}
}

// TestSplitStores_EmptyFreshInstall covers the zero-data path: a freshly
// migrated hub.db (no agents, empty moving tables) splits cleanly into empty
// event/digest stores — the auto-split case New() takes on first boot.
func TestSplitStores_EmptyFreshInstall(t *testing.T) {
	dir := t.TempDir()
	hubPath := filepath.Join(dir, "hub.db")
	db, err := OpenDB(hubPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if n, err := movingTableRowCount(db); err != nil || n != 0 {
		t.Fatalf("fresh movingTableRowCount=%d err=%v, want 0", n, err)
	}
	db.Close()

	eventsPath := filepath.Join(dir, "events.db")
	digestPath := filepath.Join(dir, "digest.db")
	if err := splitStores(hubPath, eventsPath, digestPath); err != nil {
		t.Fatalf("splitStores (empty): %v", err)
	}

	ev, err := ensureEventsStore(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Close()
	var n int64
	if err := ev.QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("fresh events.db count=%d err=%v, want 0", n, err)
	}
}

package server

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestSplitStores_RelocatesMovingTables drives the offline physical split end
// to end: build a combined hub.db (OpenDB migrates the full schema), seed the
// moving tables directly, run splitStores, and assert the rows — with a rebuilt
// FTS index — now live in events.db / digest.db and are gone from hub.db. Seeds
// through a foreign-keys-off connection so it needn't materialize agents rows.
func TestSplitStores_RelocatesMovingTables(t *testing.T) {
	dir := t.TempDir()
	hubPath := filepath.Join(dir, "hub.db")
	eventsPath := filepath.Join(dir, "events.db")
	digestPath := filepath.Join(dir, "digest.db")

	db, err := OpenDB(hubPath) // migrate → combined schema (all tables in hub.db)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	db.Close()

	seed, err := sql.Open("sqlite", dsnFKOff(hubPath))
	if err != nil {
		t.Fatal(err)
	}
	const ts = "2026-06-06T00:00:00Z"
	// Two events (the FTS insert trigger populates agent_events_fts as we go).
	if _, err := seed.Exec(`INSERT INTO agent_events
		(id, agent_id, seq, session_ordinal, ts, kind, producer, payload_json, session_id, project_id)
		VALUES ('e1','a1',1,1,?,'text','agent','{"body":"hello"}','s1',NULL),
		       ('e2','a1',2,2,?,'text','agent','{"body":"world"}','s1',NULL)`, ts, ts); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := seed.Exec(`INSERT INTO agent_event_digests
		(agent_id, team_id, updated_at, watermark_seq, event_count)
		VALUES ('a1','team-1',?,2,2)`, ts); err != nil {
		t.Fatalf("seed digest: %v", err)
	}
	if _, err := seed.Exec(`INSERT INTO agent_turns
		(agent_id, turn_id, team_id, idx, start_seq, start_ts, session_id)
		VALUES ('a1','t1','team-1',0,1,?,'s1')`, ts); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	seed.Close()

	count := func(db *sql.DB, table string) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	const wantEvents, wantDigests, wantTurns = 2, 1, 1

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

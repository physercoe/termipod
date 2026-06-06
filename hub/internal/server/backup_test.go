package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Backup → Restore round-trip: a session's content must survive the
// archive cycle byte-identical, the team/ and blobs/ subtrees must be
// preserved, and Restore on the restored DB must succeed (migrations
// run cleanly through OpenDB).
func TestBackup_RestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, "hub.db")

	// Seed a tiny but realistic state: one session with two events,
	// plus a team/ template + a blobs/ blob so we exercise the full
	// archive surface, not just the DB.
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("init src db: %v", err)
	}
	// teams + agents come first because sessions FKs reference them
	// (ON DELETE CASCADE / SET NULL respectively).
	if _, err := db.Exec(`
		INSERT INTO teams (id, name, created_at)
		VALUES ('default', 'default', '2026-04-27T00:00:00Z')`); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, created_at)
		VALUES ('a1', 'default', 'steward', 'claude-code', '2026-04-27T00:00:00Z')`); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (id, team_id, title, scope_kind, current_agent_id, status, opened_at, last_active_at)
		VALUES ('s1', 'default', 'demo', 'team', 'a1', 'active', '2026-04-27T00:00:00Z', '2026-04-27T00:00:00Z')`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	for i, txt := range []string{"hello", "world"} {
		if _, err := db.Exec(`
			INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
			VALUES (?, 'a1', ?, '2026-04-27T00:00:00Z', 'text', 'agent', ?, 's1')`,
			"e"+string(rune('0'+i)), i+1, `{"text":"`+txt+`"}`); err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}
	db.Close()

	// team/ + blobs/ subtrees the archive should round-trip too.
	teamFile := filepath.Join(src, "team", "templates", "demo.yaml")
	if err := os.MkdirAll(filepath.Dir(teamFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(teamFile, []byte("kind: demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	blobFile := filepath.Join(src, "blobs", "ab", "cd", "abcd1234")
	if err := os.MkdirAll(filepath.Dir(blobFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobFile, []byte("blob payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Backup → archive on a separate tempdir so we don't conflate src/dst.
	archive := filepath.Join(t.TempDir(), "hub.tar.gz")
	if err := Backup(context.Background(), dbPath, src, archive); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if stat, err := os.Stat(archive); err != nil || stat.Size() == 0 {
		t.Fatalf("archive missing or empty: %v size=%v", err, stat.Size())
	}

	// Restore into a fresh dir + verify content lined up.
	dst := t.TempDir()
	if err := Restore(context.Background(), archive, dst, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rdb, err := OpenDB(filepath.Join(dst, "hub.db"))
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer rdb.Close()
	var n int
	if err := rdb.QueryRow(
		`SELECT COUNT(*) FROM agent_events WHERE session_id = 's1'`).Scan(&n); err != nil {
		t.Fatalf("count restored events: %v", err)
	}
	if n != 2 {
		t.Errorf("restored event count = %d; want 2", n)
	}
	got, err := os.ReadFile(filepath.Join(dst, "team", "templates", "demo.yaml"))
	if err != nil || string(got) != "kind: demo\n" {
		t.Errorf("team/templates/demo.yaml mismatch: %q err=%v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dst, "blobs", "ab", "cd", "abcd1234"))
	if err != nil || string(got) != "blob payload" {
		t.Errorf("blobs roundtrip mismatch: %q err=%v", got, err)
	}
}

// A split deployment (ADR-045 P1) keeps the event + digest stores in their own
// files; Backup must snapshot all three and Restore must put them back, or a
// restored hub loses its whole transcript history. Build a combined DB, split
// it, then round-trip and assert the events survive in the restored events.db.
func TestBackup_RestoreRoundTrip_Split(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, "hub.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("init src db: %v", err)
	}
	db.Close()

	// Seed two events + a digest + a turn through a foreign-keys-off connection
	// (no need to materialize agents), then split into events.db / digest.db.
	seed, err := sql.Open("sqlite", dsnFKOff(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	const ts = "2026-06-06T00:00:00Z"
	if _, err := seed.Exec(`INSERT INTO agent_events
		(id, agent_id, seq, session_ordinal, ts, kind, producer, payload_json, session_id)
		VALUES ('e1','a1',1,1,?,'text','agent','{"text":"hello"}','s1'),
		       ('e2','a1',2,2,?,'text','agent','{"text":"world"}','s1')`, ts, ts); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := seed.Exec(`INSERT INTO agent_event_digests (agent_id, team_id, updated_at, event_count)
		VALUES ('a1','default',?,2)`, ts); err != nil {
		t.Fatalf("seed digest: %v", err)
	}
	seed.Close()

	eventsPath, digestPath := storePathsFor(dbPath)
	if err := splitStores(dbPath, eventsPath, digestPath); err != nil {
		t.Fatalf("split: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "hub.tar.gz")
	if err := Backup(context.Background(), dbPath, src, archive); err != nil {
		t.Fatalf("backup: %v", err)
	}

	dst := t.TempDir()
	if err := Restore(context.Background(), archive, dst, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	// The restored event store must hold both events.
	rev, err := sql.Open("sqlite", dsnFKOff(filepath.Join(dst, "events.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer rev.Close()
	var n int
	if err := rev.QueryRow(`SELECT COUNT(*) FROM agent_events WHERE session_id = 's1'`).Scan(&n); err != nil {
		t.Fatalf("count restored events: %v", err)
	}
	if n != 2 {
		t.Errorf("restored events.db event count = %d; want 2", n)
	}
	// And the restored hub.db must NOT carry the moving tables (it was split).
	rhub, err := sql.Open("sqlite", dsnFKOff(filepath.Join(dst, "hub.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer rhub.Close()
	if has, err := controlHasMovingTables(rhub); err != nil || has {
		t.Fatalf("restored hub.db still has moving tables=%v err=%v", has, err)
	}
}

// A per-team sharded deployment (ADR-045 P2) keeps each team's event + digest
// store under teams/<team>/. Backup must VACUUM-INTO-snapshot every shard and
// Restore must put them back at teams/<team>/, with each team's rows intact and
// no cross-team leak — or a restored hub loses its transcript history.
func TestBackup_RestoreRoundTrip_PerTeamShards(t *testing.T) {
	src := t.TempDir()
	dbPath := filepath.Join(src, "hub.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("init src db: %v", err)
	}
	db.Close()

	const ts = "2026-06-06T00:00:00Z"
	mkShard := func(team string, n int) {
		dir := filepath.Join(src, "teams", team)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", team, err)
		}
		ev, err := ensureEventsStore(filepath.Join(dir, "events.db"))
		if err != nil {
			t.Fatalf("events store %s: %v", team, err)
		}
		for i := 0; i < n; i++ {
			if _, err := ev.Exec(`INSERT INTO agent_events
				(id, agent_id, seq, ts, kind, producer, payload_json, session_id)
				VALUES (?,?,?,?,?,?,?,?)`,
				NewID(), "a-"+team, i+1, ts, "text", "agent", `{"text":"x"}`, "s-"+team); err != nil {
				t.Fatalf("seed event %s: %v", team, err)
			}
		}
		ev.Close()
		dg, err := ensureDigestStore(filepath.Join(dir, "digest.db"))
		if err != nil {
			t.Fatalf("digest store %s: %v", team, err)
		}
		dg.Close()
	}
	want := map[string]int{"default": 2, "team-b": 4}
	for team, n := range want {
		mkShard(team, n)
	}

	archive := filepath.Join(t.TempDir(), "hub.tar.gz")
	if err := Backup(context.Background(), dbPath, src, archive); err != nil {
		t.Fatalf("backup: %v", err)
	}

	dst := t.TempDir()
	if err := Restore(context.Background(), archive, dst, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for team, n := range want {
		p := filepath.Join(dst, "teams", team, "events.db")
		rev, err := sql.Open("sqlite", dsnFKOff(p))
		if err != nil {
			t.Fatalf("open restored %s: %v", team, err)
		}
		var got int
		if err := rev.QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&got); err != nil {
			rev.Close()
			t.Fatalf("count restored %s: %v", team, err)
		}
		rev.Close()
		if got != n {
			t.Errorf("team %s restored events = %d; want %d (cross-team leak?)", team, got, n)
		}
	}
}

// Restore refuses to overwrite a non-empty data root unless force=true.
// This is the load-bearing safety check — clobbering a half-built hub
// because the operator dragged the wrong path is exactly what backup is
// supposed to prevent in the first place.
func TestRestore_RefusesNonEmpty(t *testing.T) {
	// Build a one-event archive to use as source.
	src := t.TempDir()
	dbPath := filepath.Join(src, "hub.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("init src db: %v", err)
	}
	db.Close()
	archive := filepath.Join(t.TempDir(), "hub.tar.gz")
	if err := Backup(context.Background(), dbPath, src, archive); err != nil {
		t.Fatalf("backup: %v", err)
	}

	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "in-progress.txt"),
		[]byte("don't clobber me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), archive, dst, false); !errors.Is(err, ErrDataRootNotEmpty) {
		t.Fatalf("restore on non-empty: err=%v; want ErrDataRootNotEmpty", err)
	}
	// --force overrides the guard.
	if err := Restore(context.Background(), archive, dst, true); err != nil {
		t.Fatalf("restore --force: %v", err)
	}
}

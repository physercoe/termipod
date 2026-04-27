package server

import (
	"context"
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
		VALUES ('s1', 'default', 'demo', 'team', 'a1', 'open', '2026-04-27T00:00:00Z', '2026-04-27T00:00:00Z')`); err != nil {
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

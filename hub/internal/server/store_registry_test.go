package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTeamStores_LazyOpenCreatesFilesAndSchema: get() on a fresh team creates
// the per-team directory + both store files with their schema, and the handles
// can actually round-trip a row.
func TestTeamStores_LazyOpenCreatesFilesAndSchema(t *testing.T) {
	root := t.TempDir()
	r := newTeamStores(root, 0)
	defer r.closeAll()

	h, err := r.get("acme")
	if err != nil {
		t.Fatalf("get(acme): %v", err)
	}
	for _, p := range []string{
		filepath.Join(root, "teams", "acme", "events.db"),
		filepath.Join(root, "teams", "acme", "digest.db"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}

	// events.db round-trip through the writer + reader pools.
	if _, err := h.eventsW.Exec(
		`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
		 VALUES ('e1','a1',1,'2026-01-01T00:00:00Z','text','agent','{}')`); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var n int
	if err := h.eventsR.QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events read: n=%d err=%v", n, err)
	}
	// FTS trigger fired on insert.
	if err := h.eventsR.QueryRow(`SELECT COUNT(*) FROM agent_events_fts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("fts read: n=%d err=%v", n, err)
	}
	// digest.db schema present.
	if _, err := h.digestW.Exec(
		`INSERT INTO agent_event_digests (agent_id, team_id, updated_at) VALUES ('a1','acme','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert digest: %v", err)
	}
	if err := h.digestR.QueryRow(`SELECT COUNT(*) FROM agent_turns`).Scan(&n); err != nil {
		t.Fatalf("turns table missing: %v", err)
	}
}

// TestTeamStores_SameTeamSameHandles: repeated get() returns the cached handle
// set (one writer pool per team — never a second).
func TestTeamStores_SameTeamSameHandles(t *testing.T) {
	r := newTeamStores(t.TempDir(), 0)
	defer r.closeAll()
	a, err := r.get("t1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expected the same cached handle set for t1")
	}
	if r.openCount() != 1 {
		t.Fatalf("openCount=%d want 1", r.openCount())
	}
}

// TestTeamStores_DistinctTeamsDistinctFiles: different teams get isolated files
// and handles.
func TestTeamStores_DistinctTeamsDistinctFiles(t *testing.T) {
	root := t.TempDir()
	r := newTeamStores(root, 0)
	defer r.closeAll()
	h1, err := r.get("alpha")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := r.get("beta")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatalf("distinct teams must not share handles")
	}
	// A row written to alpha must not be visible in beta's file.
	if _, err := h1.eventsW.Exec(
		`INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
		 VALUES ('e1','a1',1,'2026-01-01T00:00:00Z','text','agent','{}')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h2.eventsR.QueryRow(`SELECT COUNT(*) FROM agent_events`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("beta should be empty: n=%d err=%v", n, err)
	}
}

// TestTeamStores_LRUEviction: with a cap of 2, opening a third team evicts the
// least-recently-used one and closes its pools.
func TestTeamStores_LRUEviction(t *testing.T) {
	r := newTeamStores(t.TempDir(), 2)
	defer r.closeAll()
	h1, _ := r.get("t1")
	if _, err := r.get("t2"); err != nil {
		t.Fatal(err)
	}
	// Touch t1 so t2 becomes the LRU victim, not t1.
	if _, err := r.get("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.get("t3"); err != nil { // over cap -> evict t2
		t.Fatal(err)
	}
	if got := r.openCount(); got != 2 {
		t.Fatalf("openCount=%d want 2 (cap)", got)
	}
	// t1 was touched last among the first two, so it survives and its pool is
	// still usable.
	if err := h1.eventsR.Ping(); err != nil {
		t.Fatalf("t1 should still be open: %v", err)
	}
	// Re-getting an evicted team reopens a fresh handle set.
	again, err := r.get("t2")
	if err != nil {
		t.Fatalf("reopen evicted t2: %v", err)
	}
	if err := again.eventsR.Ping(); err != nil {
		t.Fatalf("reopened t2 unusable: %v", err)
	}
}

// TestTeamStores_InvalidTeamRejected: a non-slug team id (path traversal) is
// refused before any file is touched.
func TestTeamStores_InvalidTeamRejected(t *testing.T) {
	r := newTeamStores(t.TempDir(), 0)
	defer r.closeAll()
	for _, bad := range []string{"", "../etc", "a/b", "Team", "x.y"} {
		if _, err := r.get(bad); err == nil {
			t.Fatalf("get(%q) should be rejected", bad)
		}
	}
	if r.openCount() != 0 {
		t.Fatalf("rejected ids must not open anything: openCount=%d", r.openCount())
	}
}

// TestTeamStores_CloseAll: closeAll empties the registry and closes pools.
func TestTeamStores_CloseAll(t *testing.T) {
	r := newTeamStores(t.TempDir(), 0)
	h, _ := r.get("t1")
	r.closeAll()
	if r.openCount() != 0 {
		t.Fatalf("openCount=%d after closeAll want 0", r.openCount())
	}
	if err := h.eventsR.Ping(); err == nil {
		t.Fatalf("pool should be closed after closeAll")
	}
}

package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// maintTestServer is a Server with only the fields maintainStore touches (the
// logger) — the maintenance pass needs no DB wiring beyond the handle passed in.
func maintTestServer() *Server {
	return &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// freshEventsWriter opens a brand-new per-team events shard through the runtime
// path (openStorePool writer + ensureEventsSchema), so it is born
// auto_vacuum=INCREMENTAL exactly as a real shard is.
func freshEventsWriter(t *testing.T) (*sql.DB, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "events.db")
	w, err := openStorePool(p, true, "")
	if err != nil {
		t.Fatalf("openStorePool: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := ensureEventsSchema(w); err != nil {
		t.Fatalf("ensureEventsSchema: %v", err)
	}
	return w, p
}

// insertEvents bulk-inserts n agent_events rows with a payload of ~payloadBytes,
// in one transaction.
func insertEvents(t *testing.T, db *sql.DB, n, payloadBytes int) {
	t.Helper()
	payload := `{"t":"` + strings.Repeat("x", payloadBytes) + `"}`
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO agent_events
		(id, agent_id, seq, ts, kind, producer, payload_json)
		VALUES (?, 'a1', ?, ?, 'text', 'agent', ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("e%d", i), i, fmt.Sprintf("ts%d", i), payload); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func pragmaInt(t *testing.T, db *sql.DB, p string) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA " + p).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", p, err)
	}
	return v
}

// TestNewShardIsIncrementalAutoVacuum pins ADR-045 D4: a shard created through
// the runtime writer path is auto_vacuum=INCREMENTAL (mode 2).
func TestNewShardIsIncrementalAutoVacuum(t *testing.T) {
	w, _ := freshEventsWriter(t)
	if av := pragmaInt(t, w, "auto_vacuum"); av != 2 {
		t.Fatalf("auto_vacuum = %d, want 2 (INCREMENTAL)", av)
	}
}

// TestMaintainStoreReclaimsFreePages: after a bulk delete leaves a large
// freelist, one maintenance pass returns pages to the OS (page_count shrinks,
// freelist shrinks), bounded by the per-pass cap.
func TestMaintainStoreReclaimsFreePages(t *testing.T) {
	w, _ := freshEventsWriter(t)
	insertEvents(t, w, 3000, 1200)
	if _, err := w.Exec(`DELETE FROM agent_events WHERE seq < 2950`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	flBefore := pragmaInt(t, w, "freelist_count")
	pcBefore := pragmaInt(t, w, "page_count")
	if flBefore <= storeVacuumWatermarkPages {
		t.Fatalf("setup: freelist %d not above watermark %d — test can't exercise reclaim",
			flBefore, storeVacuumWatermarkPages)
	}

	maintTestServer().maintainStore(context.Background(), w, "test/events.db")

	flAfter := pragmaInt(t, w, "freelist_count")
	pcAfter := pragmaInt(t, w, "page_count")
	if pcAfter >= pcBefore {
		t.Errorf("page_count did not shrink: before=%d after=%d", pcBefore, pcAfter)
	}
	if flAfter >= flBefore {
		t.Errorf("freelist did not shrink: before=%d after=%d", flBefore, flAfter)
	}
	// Per-pass cap honored: no single pass reclaims more than the bound.
	if reclaimed := flBefore - flAfter; reclaimed > storeVacuumMaxPagesPerPass {
		t.Errorf("reclaimed %d pages in one pass, exceeds cap %d", reclaimed, storeVacuumMaxPagesPerPass)
	}
}

// TestMaintainStoreNoReclaimBelowThreshold: a store with little free space keeps
// its high-water mark (the hysteresis gate refuses to thrash).
func TestMaintainStoreNoReclaimBelowThreshold(t *testing.T) {
	w, _ := freshEventsWriter(t)
	insertEvents(t, w, 200, 400) // no deletes → freelist ~0
	pcBefore := pragmaInt(t, w, "page_count")

	maintTestServer().maintainStore(context.Background(), w, "test/events.db")

	if pcAfter := pragmaInt(t, w, "page_count"); pcAfter != pcBefore {
		t.Errorf("page_count changed despite tiny freelist: before=%d after=%d", pcBefore, pcAfter)
	}
}

// TestMaintainStoreSafeOnNonIncremental: on an auto_vacuum=NONE store (hub.db /
// legacy shards), the pass runs without error and incremental_vacuum is a no-op
// (the file is not rewritten).
func TestMaintainStoreSafeOnNonIncremental(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.db")
	db, err := ensureEventsStore(p) // dsnFKOff → no auto_vacuum → mode NONE
	if err != nil {
		t.Fatalf("ensureEventsStore: %v", err)
	}
	defer db.Close()
	if av := pragmaInt(t, db, "auto_vacuum"); av != 0 {
		t.Fatalf("precondition: auto_vacuum = %d, want 0 (NONE)", av)
	}
	insertEvents(t, db, 2000, 1200)
	if _, err := db.Exec(`DELETE FROM agent_events WHERE seq < 1950`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	pcBefore := pragmaInt(t, db, "page_count")

	maintTestServer().maintainStore(context.Background(), db, "legacy/events.db")

	if pcAfter := pragmaInt(t, db, "page_count"); pcAfter != pcBefore {
		t.Errorf("page_count changed on NONE store (incremental_vacuum should no-op): before=%d after=%d",
			pcBefore, pcAfter)
	}
}

// TestMaintainStoreTruncatesWAL: the checkpoint(TRUNCATE) step shrinks the -wal
// sidecar back toward zero.
func TestMaintainStoreTruncatesWAL(t *testing.T) {
	w, p := freshEventsWriter(t)
	insertEvents(t, w, 3000, 1200) // grow the WAL
	walPath := p + "-wal"
	fi, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat -wal: %v", err)
	}
	walBefore := fi.Size()
	if walBefore == 0 {
		t.Skip("WAL already empty before checkpoint (auto-checkpoint raced) — nothing to assert")
	}

	maintTestServer().maintainStore(context.Background(), w, "test/events.db")

	fi, err = os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat -wal after: %v", err)
	}
	if fi.Size() >= walBefore {
		t.Errorf("WAL not truncated: before=%d after=%d", walBefore, fi.Size())
	}
}

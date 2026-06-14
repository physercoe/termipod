package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// store_maintenance.go — ADR-045 D4: automated WAL checkpointing + bounded
// incremental reclamation across the per-team shards.
//
// Two distinct storage-hygiene problems, two distinct mechanisms (do not
// conflate them):
//
//   - WAL growth. SQLite's auto-checkpoint can only reset the WAL up to the
//     oldest LIVE reader's snapshot. The hub holds long-lived SSE readers, so a
//     continuous reader + continuous firehose write keeps the checkpoint from
//     ever reaching the WAL head and it grows without bound. Fixed by a periodic
//     wal_checkpoint(TRUNCATE) — NOT by VACUUM.
//   - Free pages never returned to the OS. After a retention/refold delete, freed
//     pages sit on the freelist (reused by future inserts, but the file stays at
//     its high-water mark). Returned to the OS by incremental auto-vacuum in
//     bounded chunks — NOT by a full VACUUM, whose ~2× disk + global write lock
//     for an O(DB-size) duration is hostile to a small always-on VPS.
//
// New shards are created auto_vacuum=INCREMENTAL (store_split.go / openStorePool),
// so incremental_vacuum here actually returns pages; on hub.db and pre-D4 shards
// (auto_vacuum=NONE) incremental_vacuum is a documented no-op, so the same pass
// is safe everywhere. Full VACUUM stays the operator-only `hub-server db vacuum`.

const (
	// storeVacuumWatermarkPages is the freelist slack kept after a reclamation
	// pass (~512 KiB at the 4 KiB default page size). Reclaiming to zero on a
	// still-active firehose would just hand back pages the next insert
	// re-allocates — this watermark is the hysteresis floor.
	storeVacuumWatermarkPages = 128
	// storeVacuumFreeFracPct gates a pass on the freelist being a meaningful
	// fraction of the file, so a near-full file with a few free pages is left
	// alone (its high-water mark IS its working set).
	storeVacuumFreeFracPct = 25
	// storeVacuumMaxPagesPerPass bounds how many pages one pass returns (~8 MiB),
	// so the short incremental_vacuum transaction never holds the write lock long
	// even on a store with a huge freelist; the next tick continues draining.
	storeVacuumMaxPagesPerPass = 2048
)

// maintTarget is one store-file writer pool the maintenance loop operates on,
// with a human label for logs.
type maintTarget struct {
	label string
	db    *sql.DB
}

// storeMaintenanceInterval is the loop cadence, operator-tunable via
// HUB_STORE_MAINTENANCE_INTERVAL (a Go duration, e.g. "2m", "10m").
func storeMaintenanceInterval() time.Duration {
	d := 5 * time.Minute
	if v := os.Getenv("HUB_STORE_MAINTENANCE_INTERVAL"); v != "" {
		if p, err := time.ParseDuration(v); err == nil && p > 0 {
			d = p
		}
	}
	return d
}

// runStoreMaintenance is the maintenance loop (ADR-045 D4). It runs until ctx is
// cancelled. Started from Start() unless HUB_STORE_MAINTENANCE_DISABLE is set.
func (s *Server) runStoreMaintenance(ctx context.Context) {
	ticker := time.NewTicker(storeMaintenanceInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maintainStores(ctx)
		}
	}
}

// maintainStores runs one maintenance pass over hub.db and every currently-open
// team shard writer. Cold (unopened/evicted) teams are skipped on purpose: they
// take no writes, and SQLite checkpoints a file when its last connection closes,
// so an evicted team's WAL is already truncated.
func (s *Server) maintainStores(ctx context.Context) {
	if s.writeDB != nil {
		s.maintainStore(ctx, s.writeDB, "hub.db")
	}
	if s.stores != nil {
		for _, t := range s.stores.maintenanceTargets() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			s.maintainStore(ctx, t.db, t.label)
		}
	}
}

// maintainStore checkpoints one store's WAL, then conditionally returns free
// pages to the OS. All operations run on the store's single-connection WRITER
// pool, so they serialize with the store's other writes rather than racing them.
// Every failure is best-effort and logged at debug — a missed pass is retried on
// the next tick, never an error to a caller.
func (s *Server) maintainStore(ctx context.Context, db *sql.DB, label string) {
	// 1. Truncate the WAL back into the main file. TRUNCATE blocks for
	//    busy_timeout if a reader pins an old snapshot, then returns busy=1
	//    (not an error) — we just try again next tick.
	var busy, walFrames, checkpointed int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &walFrames, &checkpointed); err != nil {
		s.log.Debug("store maintenance: checkpoint failed", "store", label, "err", err)
	} else if busy != 0 {
		s.log.Debug("store maintenance: checkpoint busy (reader pinned)", "store", label)
	}

	// 2. Reclaim free pages with hysteresis. incremental_vacuum is a no-op when
	//    the store is not auto_vacuum=INCREMENTAL, so this is safe on hub.db and
	//    pre-D4 shards (it simply returns nothing there).
	var freelist, pageCount int
	if err := db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freelist); err != nil {
		s.log.Debug("store maintenance: freelist_count failed", "store", label, "err", err)
		return
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		s.log.Debug("store maintenance: page_count failed", "store", label, "err", err)
		return
	}
	if pageCount <= 0 || freelist <= storeVacuumWatermarkPages {
		return
	}
	if freelist*100 < pageCount*storeVacuumFreeFracPct {
		return // free space below the fraction gate — leave the high-water mark
	}
	n := freelist - storeVacuumWatermarkPages
	if n > storeVacuumMaxPagesPerPass {
		n = storeVacuumMaxPagesPerPass
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, n)); err != nil {
		s.log.Debug("store maintenance: incremental_vacuum failed",
			"store", label, "pages", n, "err", err)
		return
	}
	s.log.Debug("store maintenance: reclaimed pages",
		"store", label, "pages", n, "freelist_before", freelist, "page_count", pageCount)
}

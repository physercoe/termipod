package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	hub "github.com/termipod/hub"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

// SQLite connection pragmas (hub-scaling Tier-1 tuning, ADR-045). Two profiles:
//
//   - pragmaCommon goes on EVERY pool — WAL + synchronous=NORMAL +
//     busy_timeout, plus temp_store=MEMORY (temp b-trees for ORDER BY / GROUP BY
//     / FTS merges in RAM) and a 256 MiB mmap (shared OS page cache, not heap —
//     fewer read() syscalls). Both are cheap and safe to fan out.
//   - pragmaWriterCache adds a large 64 MiB per-connection page cache, applied
//     ONLY to the single-connection writer pools (one control writer + one
//     events + one digest writer per open team — a bounded set). It is kept OFF
//     the UNCAPPED reader pools on purpose: cache_size is per-connection, so a
//     big reader cache would multiply across many concurrent readers and exhaust
//     RAM on a small VPS. Measured ~+20% ingest throughput at writer saturation
//     (≥800 concurrent agents, internal/server/load_test.go), a wash below it.
const pragmaCommon = "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(2)&_pragma=mmap_size(268435456)"

// pragmaStoreAutoVacuum requests INCREMENTAL auto-vacuum (mode 2) for the
// per-team event/digest shards (ADR-045 D4). auto_vacuum is a database-level
// property fixed when the first table is created, so it MUST ride on the
// schema-creating writer connection at first open (openStorePool); on an
// already-populated file the pragma is a harmless no-op (the mode is set, and
// changing it would require a full VACUUM — that path is the operator-only
// `hub-server db vacuum`). It is deliberately kept OFF hub.db (control), which
// has low delete volume and keeps plain freelist reuse.
const pragmaStoreAutoVacuum = "&_pragma=auto_vacuum(2)"

// pragmaWriterCache is resolved once at startup. Default 64 MiB; the size (in
// KiB) is operator-tunable per VPS via HUB_SQLITE_WRITER_CACHE_KB (also used to
// sweep the value in load tests). Applied to the SINGLE global control writer
// (hub.db, OpenWriterDB) — there is exactly one, so its cache is unbounded by
// team count. The PER-TEAM writer pools use perTeamWriterCachePragma instead,
// which divides a global budget across the open-store cap (see below).
var pragmaWriterCache = writerCachePragma()

// writerCacheKB is the per-connection writer cache size in KiB — the default
// (64 MiB) and the operator override. It doubles as the per-pool CEILING for
// the budget-divided per-team caches.
func writerCacheKB() int {
	kb := 65536
	if v := os.Getenv("HUB_SQLITE_WRITER_CACHE_KB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			kb = n
		}
	}
	return kb
}

func writerCachePragma() string {
	return fmt.Sprintf("&_pragma=cache_size(-%d)", writerCacheKB())
}

// perTeamWriterCachePragma sizes the page cache for ONE per-team writer pool so
// that the aggregate writer cache across every open team stays bounded by RAM,
// regardless of team count. The earlier design put a flat 64 MiB on every
// per-team writer pool; with 2 writer pools per team (events.db + digest.db)
// and up to HUB_MAX_OPEN_TEAM_STORES open teams, that product was unbounded —
// a measured hazard on a 2 GB VPS (scaling_probe_test.go Probe C).
//
// Instead a single budget (HUB_SQLITE_WRITER_CACHE_BUDGET_MB, default 256 MiB)
// is divided across the maximum number of concurrently-open per-team writer
// pools (2 × maxOpen), then clamped to [1 MiB, writerCacheKB()]. So:
//
//   - the aggregate per-team writer cache never exceeds ~the budget;
//   - fewer teams (a lower cap) ⇒ a larger cache per pool, same total — the
//     operator trades breadth for depth by tuning HUB_MAX_OPEN_TEAM_STORES;
//   - the global control writer (hub.db) keeps its full writerCacheKB() cache
//     and is NOT counted here (it is a single pool).
//
// To restore the old flat-64 MiB behaviour, raise the budget (e.g. set
// HUB_SQLITE_WRITER_CACHE_BUDGET_MB high enough that budget/(2·maxOpen) ≥ 64
// MiB) — the clamp to writerCacheKB() then governs.
func perTeamWriterCachePragma(maxOpen int) string {
	budgetMB := 256
	if v := os.Getenv("HUB_SQLITE_WRITER_CACHE_BUDGET_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			budgetMB = n
		}
	}
	if maxOpen < 1 {
		maxOpen = 1
	}
	const floorKB = 1024 // 1 MiB — keep the cache useful even at a large cap
	perKB := budgetMB * 1024 / (2 * maxOpen)
	if perKB < floorKB {
		perKB = floorKB
	}
	if ceil := writerCacheKB(); perKB > ceil {
		perKB = ceil
	}
	return fmt.Sprintf("&_pragma=cache_size(-%d)", perKB)
}

// maxReadConns returns the generous reader-pool connection cap (default 64),
// overridable via HUB_DB_MAX_READ_CONNS. This is an FD-exhaustion safety
// bound, NOT a throughput cap — keep it generous.
func maxReadConns() int {
	n := 64
	if v := os.Getenv("HUB_DB_MAX_READ_CONNS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return n
}

// OpenDB opens the SQLite database at path and runs pending migrations.
// Callers must Close the returned *sql.DB.
func OpenDB(path string) (*sql.DB, error) {
	// Migrations run on a dedicated connection pool with foreign-key
	// enforcement OFF. SQLite's recommended pattern for table-recreate
	// migrations (lang_altertable.html §7) requires FKs disabled so that
	// DROP TABLE doesn't fire ON DELETE CASCADE / SET NULL against
	// dependent rows. PRAGMA foreign_keys is a no-op inside a transaction,
	// so we set it via DSN and use a separate sql.DB that's discarded
	// after migrations finish.
	migrateDSN := path + "?_pragma=foreign_keys(0)&" + pragmaCommon
	migrateDB, err := sql.Open("sqlite", migrateDSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite (migrations): %w", err)
	}
	if err := migrateDB.Ping(); err != nil {
		migrateDB.Close()
		return nil, fmt.Errorf("ping sqlite (migrations): %w", err)
	}
	if err := runMigrations(migrateDB); err != nil {
		migrateDB.Close()
		return nil, err
	}
	if err := migrateDB.Close(); err != nil {
		return nil, fmt.Errorf("close migrations db: %w", err)
	}

	// The general/reader pool gets pragmaCommon WITHOUT the big writer cache
	// (cache_size is per-connection — see the const doc). It carries a generous
	// FD-safety cap (see maxReadConns).
	dsn := path + "?_pragma=foreign_keys(1)&" + pragmaCommon
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}

// OpenWriterDB opens the dedicated single-writer connection pool against an
// already-migrated database. It runs NO migrations (OpenDB owns schema). The
// caller caps it to one connection (New()) so every write serializes through
// one lane and queues in Go rather than colliding on SQLite's write lock —
// see New() for the read/write split and
// docs/discussions/hub-scaling-storage-and-concurrency.md §6.
func OpenWriterDB(path string) (*sql.DB, error) {
	// Single-connection writer pool (bounded) → gets the big writer cache.
	dsn := path + "?_pragma=foreign_keys(1)&" + pragmaCommon + pragmaWriterCache
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite (writer): %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite (writer): %w", err)
	}
	return db, nil
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(hub.MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	drv, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("migrations driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}

	// Auto-recover from a dirty migration state. golang-migrate marks a
	// version dirty when its tx was rolled back; the schema is back at
	// version-1 but the bookkeeping row still says "in progress" and Up()
	// refuses to proceed. Force the bookkeeping back so the (now fixed)
	// migration can re-run on the next Up() below.
	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		prev := int(version) - 1
		slog.Warn("migration dirty state detected; forcing back",
			"from", version, "to", prev)
		if err := m.Force(prev); err != nil {
			return fmt.Errorf("recover dirty migration version %d: %w", version, err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

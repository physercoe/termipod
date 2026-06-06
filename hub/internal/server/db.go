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

// pragmaWriterCache is resolved once at startup. Default 64 MiB; the size (in
// KiB) is operator-tunable per VPS via HUB_SQLITE_WRITER_CACHE_KB (also used to
// sweep the value in load tests). Applied only to the bounded single-conn writer
// pools, never the uncapped readers — see the cache_size note above.
var pragmaWriterCache = writerCachePragma()

func writerCachePragma() string {
	kb := 65536
	if v := os.Getenv("HUB_SQLITE_WRITER_CACHE_KB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			kb = n
		}
	}
	return fmt.Sprintf("&_pragma=cache_size(-%d)", kb)
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

	// The general/reader pool is uncapped, so it gets pragmaCommon WITHOUT the
	// big writer cache (cache_size is per-connection — see the const doc).
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

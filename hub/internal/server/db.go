package server

import (
	"database/sql"
	"fmt"
	"log/slog"

	hub "github.com/termipod/hub"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

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
	migrateDSN := path + "?_pragma=foreign_keys(0)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
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

	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
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

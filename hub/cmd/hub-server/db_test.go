package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/server"
)

func TestVacuumStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := server.Init(dir, dbPath); err != nil {
		t.Fatalf("server.Init: %v", err)
	}
	res, err := vacuumStats(dbPath)
	if err != nil {
		t.Fatalf("vacuumStats: %v", err)
	}
	if res.BeforeBytes <= 0 || res.AfterBytes <= 0 {
		t.Errorf("expected non-zero sizes either side, got %+v", res)
	}
}

func TestVacuumStats_MissingDB(t *testing.T) {
	if _, err := vacuumStats(filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("expected an error for a missing database file")
	}
}

// vacuumTeamStores no-ops on a data root without a teams/ dir, and VACUUMs each
// per-team shard that does exist (ADR-045 P2).
func TestVacuumTeamStores(t *testing.T) {
	dir := t.TempDir()

	// No teams/ dir → nothing to do, no error.
	if reclaimed, files, err := vacuumTeamStores(dir); err != nil || files != 0 || reclaimed != 0 {
		t.Fatalf("empty data root: reclaimed=%d files=%d err=%v", reclaimed, files, err)
	}

	// One team with an events.db shard (no digest.db) → exactly one file vacuumed.
	shardDir := filepath.Join(dir, "teams", "default")
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(shardDir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES ('a'), ('b')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, files, err := vacuumTeamStores(dir)
	if err != nil {
		t.Fatalf("vacuumTeamStores: %v", err)
	}
	if files != 1 {
		t.Errorf("vacuumed files = %d, want 1 (events.db present, digest.db absent)", files)
	}
}

func TestSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := server.Init(dir, dbPath); err != nil {
		t.Fatalf("server.Init: %v", err)
	}
	version, dirty, err := schemaVersion(dbPath)
	if err != nil {
		t.Fatalf("schemaVersion: %v", err)
	}
	if dirty {
		t.Error("a freshly-initialised schema must not be dirty")
	}
	if version <= 0 {
		t.Errorf("version = %d, want > 0", version)
	}
}

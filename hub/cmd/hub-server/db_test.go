package main

import (
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

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	hub "github.com/termipod/hub"
	"github.com/termipod/hub/internal/auth"
)

const defaultTeamID = "default"

// Init prepares a fresh hub data root: creates directory layout, opens DB,
// runs migrations, ensures the default team, seeds built-in templates,
// and issues a fresh owner token. Returns the owner token (one-time).
func Init(dataRoot, dbPath string) (string, error) {
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return "", fmt.Errorf("mkdir data root: %w", err)
	}
	for _, sub := range []string{"projects", "blobs", "agents", "team/templates/agents", "team/templates/prompts", "team/templates/policies"} {
		if err := os.MkdirAll(filepath.Join(dataRoot, sub), 0o700); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}

	db, err := OpenDB(dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()

	ctx := context.Background()
	if err := ensureTeam(ctx, db, defaultTeamID, "default"); err != nil {
		return "", err
	}
	if err := ensureTeamChannel(ctx, db, "hub-meta"); err != nil {
		return "", err
	}
	if err := writeBuiltinTemplates(dataRoot); err != nil {
		return "", err
	}

	// Issue a fresh owner token. One-time display; only hash is stored.
	plaintext := auth.NewToken()
	scope, _ := json.Marshal(map[string]any{"team": defaultTeamID, "role": "principal"})
	if err := auth.InsertToken(ctx, db, "owner", string(scope), plaintext, NewID(), NowUTC()); err != nil {
		return "", fmt.Errorf("insert owner token: %w", err)
	}
	return plaintext, nil
}

// ensureTeamChannel creates a team-scope (project_id NULL) channel if missing.
// #hub-meta is the principal ↔ steward room; it must exist before the steward
// can be spawned.
func ensureTeamChannel(ctx context.Context, db *sql.DB, name string) error {
	var existing string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM channels WHERE scope_kind = 'team' AND project_id IS NULL AND name = ?`,
		name).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO channels (id, project_id, scope_kind, name, created_at)
		VALUES (?, NULL, 'team', ?, ?)`, NewID(), name, NowUTC())
	return err
}

func ensureTeam(ctx context.Context, db *sql.DB, id, name string) error {
	var existing string
	err := db.QueryRowContext(ctx, `SELECT id FROM teams WHERE id = ?`, id).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		id, name, NowUTC())
	return err
}

// writeBuiltinTemplates copies embedded templates into the data root on first
// init. Subsequent inits never overwrite — the user's edits win.
func writeBuiltinTemplates(dataRoot string) error {
	base := filepath.Join(dataRoot, "team")
	return fs.WalkDir(hub.TemplatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := path
		if rel == "templates" {
			return nil
		}
		target := filepath.Join(base, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if _, err := os.Stat(target); err == nil {
			return nil // never overwrite existing user edits
		}
		data, err := fs.ReadFile(hub.TemplatesFS, path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

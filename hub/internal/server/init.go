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
	if err := seedBuiltinProjectTemplates(ctx, db); err != nil {
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

// seedBuiltinProjectTemplates inserts the built-in project-template rows
// (blueprint §6.1: is_template=1 project rows that serve as instantiation
// blueprints for concrete runs). Idempotent via UNIQUE(team_id, name) +
// INSERT OR IGNORE — user edits to the row survive re-init.
//
// The `ablation-sweep` template is the research-demo entry point: the user
// instantiates it with concrete parameters, and the steward decomposes the
// sweep per the recipe in prompts/steward.v1.md.
func seedBuiltinProjectTemplates(ctx context.Context, db *sql.DB) error {
	type projectTemplate struct {
		name          string
		kind          string
		goal          string
		parameters    map[string]any
		onCreateTmpl  string
	}
	templates := []projectTemplate{
		{
			name: "ablation-sweep",
			kind: "goal",
			goal: "Run an ablation sweep over model sizes and optimizers on the target training repo. " +
				"Default: nanoGPT-Shakespeare, AdamW vs Lion, n_embd {128,256,384}, 1000 iters.",
			parameters: map[string]any{
				"model_sizes": []int{128, 256, 384},
				"optimizers":  []string{"adamw", "lion"},
				"iters":       1000,
			},
			onCreateTmpl: "agents.steward",
		},
		{
			// write-memo: a lightweight "quick brief" template. The steward
			// reads any named context docs, drafts a Goal / Findings / Open
			// questions memo via documents.create, and requests review so it
			// surfaces in the principal's Inbox. No agents spawned, no hosts
			// touched — entirely hub-local, so it's a cheap ad-hoc wedge
			// alongside the ablation sweep.
			name: "write-memo",
			kind: "goal",
			goal: "Draft a short memo on {topic}. Read any context docs in context_doc_ids, " +
				"structure as Goal / Findings / Open questions, and request review when done.",
			parameters: map[string]any{
				"topic":            "",
				"context_doc_ids":  []string{},
				"length":           "short",
			},
			onCreateTmpl: "agents.steward",
		},
	}
	for _, t := range templates {
		params, err := json.Marshal(t.parameters)
		if err != nil {
			return fmt.Errorf("marshal parameters for %s: %w", t.name, err)
		}
		_, err = db.ExecContext(ctx, `
			INSERT OR IGNORE INTO projects
				(id, team_id, name, status, config_yaml, created_at,
				 goal, kind, is_template, parameters_json, on_create_template_id)
			VALUES (?, ?, ?, 'active', '', ?, ?, ?, 1, ?, ?)`,
			NewID(), defaultTeamID, t.name, NowUTC(),
			t.goal, t.kind, string(params), t.onCreateTmpl)
		if err != nil {
			return fmt.Errorf("insert project template %s: %w", t.name, err)
		}
	}
	return nil
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

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	hub "github.com/termipod/hub"
	"github.com/termipod/hub/internal/auth"
	"gopkg.in/yaml.v3"
)

const defaultTeamID = "default"

// Init prepares a fresh hub data root: creates directory layout, opens DB,
// runs migrations, ensures the default team, seeds built-in templates,
// and issues a fresh owner token. Returns the owner token (one-time).
func Init(dataRoot, dbPath string) (string, error) {
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		return "", fmt.Errorf("mkdir data root: %w", err)
	}
	for _, sub := range []string{"projects", "blobs", "agents", "team/templates/agents", "team/templates/prompts", "team/templates/policies", "team/templates/projects"} {
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
	// Copy embedded templates to disk first so user-authored YAMLs in
	// team/templates/projects/ (if any) are visible to the seeder.
	if err := writeBuiltinTemplates(dataRoot); err != nil {
		return "", err
	}
	if err := seedBuiltinProjectTemplates(ctx, db, dataRoot); err != nil {
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

// projectTemplateDoc mirrors a templates/projects/*.yaml file. The hub
// is domain-agnostic — it does not know the difference between
// `ablation-sweep` and `write-memo`, and it should not. To register
// another template, drop a YAML file with this shape into
// `hub/templates/projects/` (for built-ins shipped with the binary) or
// into `<dataRoot>/team/templates/projects/` (for user-authored ones,
// picked up on next Init).
type projectTemplateDoc struct {
	Name               string         `yaml:"name"`
	Kind               string         `yaml:"kind"`
	Goal               string         `yaml:"goal"`
	Parameters         map[string]any `yaml:"parameters"`
	OnCreateTemplateID string         `yaml:"on_create_template_id"`
	// OverviewWidget picks the pluggable hero region under the portfolio
	// header on Project Detail → Overview (A+B chassis, IA §6.2).
	// Empty / unknown → defaults to overviewWidgetDefault at resolve time.
	OverviewWidget string `yaml:"overview_widget"`
	// Phases is the ordered phase set declared by the template (D1).
	// Empty (the existing case for every shipped template) keeps the
	// project lifecycle-disabled — projects.phase stays NULL and the
	// mobile UI falls back to the pre-lifecycle Overview. The full
	// per-phase deliverable / criterion / section spec lands in W7;
	// W1 only consumes the phase list.
	Phases []string `yaml:"phases"`
}

// overviewWidgetDefault is returned when a template doesn't specify one
// or specifies an unknown value. Project Detail's mobile registry maps
// this name to the default task/milestone list hero.
const overviewWidgetDefault = "task_milestone_list"

// validOverviewWidgets is the closed set of pluggable hero kinds shipped
// by W4 (plus the W6 workspace default). Unknown values log and fall
// back to overviewWidgetDefault; the mobile registry enforces the same
// enum on render.
var validOverviewWidgets = map[string]bool{
	"task_milestone_list": true,
	"recent_artifacts":    true,
	"children_status":     true,
	"recent_firings_list": true,
	// W7 — research-template heroes (A6 §2 + §3-§7).
	// `portfolio_header` was retired in v1.0.501 — it was a no-op
	// pointer at the chassis-A header above. Templates that named
	// it as project-level default now fall through to
	// overviewWidgetDefault here, which the mobile mirrors.
	// `sweep_compare` was retired in v1.0.506 — the multi-series
	// metric-chart embedded by experiment_dash subsumes the
	// cross-run scatter use case. See plans/multi-run-experiment-phase.md.
	"idea_conversation": true,
	"deliverable_focus": true,
	"experiment_dash":   true,
	"paper_acceptance":  true,
}

// normalizeOverviewWidget returns the widget name to expose on the wire.
// Unknown values degrade to the default rather than 500'ing a project
// read — the hub is domain-agnostic and a stray enum value shouldn't
// brick the UI.
func normalizeOverviewWidget(v string) string {
	if v == "" {
		return overviewWidgetDefault
	}
	if validOverviewWidgets[v] {
		return v
	}
	return overviewWidgetDefault
}

// seedBuiltinProjectTemplates inserts one is_template=1 projects row
// per YAML file under templates/projects/ (blueprint §6.1). The set of
// templates is data, not code: the hub walks the embedded FS + any
// user-authored additions in <dataRoot>/team/templates/projects/.
// Idempotent via UNIQUE(team_id, name) + INSERT OR IGNORE — user edits
// to the DB row survive re-init.
func seedBuiltinProjectTemplates(ctx context.Context, db *sql.DB, dataRoot string) error {
	docs, err := loadProjectTemplates(dataRoot)
	if err != nil {
		return err
	}
	for _, t := range docs {
		if t.Name == "" {
			return fmt.Errorf("project template missing name")
		}
		params := t.Parameters
		if params == nil {
			params = map[string]any{}
		}
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal parameters for %s: %w", t.Name, err)
		}
		_, err = db.ExecContext(ctx, `
			INSERT OR IGNORE INTO projects
				(id, team_id, name, status, config_yaml, created_at,
				 goal, kind, is_template, parameters_json, on_create_template_id)
			VALUES (?, ?, ?, 'active', '', ?, ?, ?, 1, ?, ?)`,
			NewID(), defaultTeamID, t.Name, NowUTC(),
			t.Goal, t.Kind, string(paramsJSON), t.OnCreateTemplateID)
		if err != nil {
			return fmt.Errorf("insert project template %s: %w", t.Name, err)
		}
	}
	return nil
}

// loadProjectTemplates walks templates/projects/*.yaml in the embedded
// FS and, on top of that, <dataRoot>/team/templates/projects/*.yaml if
// the directory exists. Disk entries override embedded entries by name
// so a user-edited copy of a built-in template takes precedence without
// needing to delete the embedded original.
func loadProjectTemplates(dataRoot string) ([]projectTemplateDoc, error) {
	byName := map[string]projectTemplateDoc{}
	order := []string{}

	add := func(name string, doc projectTemplateDoc) {
		if _, seen := byName[name]; !seen {
			order = append(order, name)
		}
		byName[name] = doc
	}

	walkErr := fs.WalkDir(hub.TemplatesFS, "templates/projects", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := fs.ReadFile(hub.TemplatesFS, path)
		if err != nil {
			return err
		}
		var doc projectTemplateDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if doc.OverviewWidget != "" && !validOverviewWidgets[doc.OverviewWidget] {
			slog.Warn("unknown overview_widget, falling back to default",
				"template", doc.Name, "overview_widget", doc.OverviewWidget,
				"default", overviewWidgetDefault)
			doc.OverviewWidget = ""
		}
		add(doc.Name, doc)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	diskDir := filepath.Join(dataRoot, "team", "templates", "projects")
	if entries, err := os.ReadDir(diskDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(diskDir, e.Name()))
			if err != nil {
				return nil, err
			}
			var doc projectTemplateDoc
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
			}
			if doc.OverviewWidget != "" && !validOverviewWidgets[doc.OverviewWidget] {
				slog.Warn("unknown overview_widget, falling back to default",
					"template", doc.Name, "overview_widget", doc.OverviewWidget,
					"default", overviewWidgetDefault)
				doc.OverviewWidget = ""
			}
			add(doc.Name, doc)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	out := make([]projectTemplateDoc, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out, nil
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

package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestInit_SeedsAblationSweepTemplate confirms the built-in project template
// lands in the DB on first init with the expected fields (blueprint §6.1:
// is_template=1 rows parameterize steward decomposition).
func TestInit_SeedsAblationSweepTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var (
		kind, goal, paramsJSON, onCreate string
		isTemplate                       int
	)
	err = db.QueryRowContext(context.Background(), `
		SELECT kind, goal, is_template, parameters_json, on_create_template_id
		FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, "ablation-sweep").
		Scan(&kind, &goal, &isTemplate, &paramsJSON, &onCreate)
	if err != nil {
		t.Fatalf("lookup ablation-sweep template: %v", err)
	}

	if isTemplate != 1 {
		t.Errorf("is_template = %d, want 1", isTemplate)
	}
	if kind != "goal" {
		t.Errorf("kind = %q, want goal", kind)
	}
	if onCreate != "agents.steward" {
		t.Errorf("on_create_template_id = %q, want agents.steward", onCreate)
	}
	if goal == "" {
		t.Errorf("goal is empty")
	}

	var params map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
		t.Fatalf("parse parameters_json %q: %v", paramsJSON, err)
	}
	for _, k := range []string{"model_sizes", "optimizers", "iters"} {
		if _, ok := params[k]; !ok {
			t.Errorf("parameters_json missing %q: %s", k, paramsJSON)
		}
	}
}

// TestInit_SeedsWriteMemoTemplate confirms the write-memo template is
// seeded alongside ablation-sweep with the expected parameter keys
// (topic, context_doc_ids, length).
func TestInit_SeedsWriteMemoTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var (
		isTemplate     int
		goal, params   string
		onCreate, kind string
	)
	err = db.QueryRowContext(context.Background(), `
		SELECT kind, goal, is_template, parameters_json, on_create_template_id
		FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, "write-memo").
		Scan(&kind, &goal, &isTemplate, &params, &onCreate)
	if err != nil {
		t.Fatalf("lookup write-memo template: %v", err)
	}
	if isTemplate != 1 {
		t.Errorf("is_template = %d, want 1", isTemplate)
	}
	if onCreate != "agents.steward" {
		t.Errorf("on_create = %q, want agents.steward", onCreate)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		t.Fatalf("parse parameters_json %q: %v", params, err)
	}
	for _, k := range []string{"topic", "context_doc_ids", "length"} {
		if _, ok := p[k]; !ok {
			t.Errorf("write-memo parameters missing %q: %s", k, params)
		}
	}
}

// TestInit_SeedsBenchmarkComparisonTemplate confirms the
// benchmark-comparison template lands with its expected parameter keys.
func TestInit_SeedsBenchmarkComparisonTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var params string
	if err := db.QueryRowContext(context.Background(), `
		SELECT parameters_json FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, "benchmark-comparison").Scan(&params); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{"models", "benchmark", "samples", "headline_metric"} {
		if _, ok := p[k]; !ok {
			t.Errorf("missing key %q in %s", k, params)
		}
	}
}

// TestInit_SeedsReproducePaperTemplate confirms the reproduce-paper
// template lands with its expected parameter keys.
func TestInit_SeedsReproducePaperTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var params string
	if err := db.QueryRowContext(context.Background(), `
		SELECT parameters_json FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, "reproduce-paper").Scan(&params); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{"paper_arxiv_id", "repo_url", "target_metric", "tolerance_pct"} {
		if _, ok := p[k]; !ok {
			t.Errorf("missing key %q in %s", k, params)
		}
	}
}

// TestInit_SeedsUserAuthoredTemplate confirms a YAML dropped into
// <dataRoot>/team/templates/projects/ is seeded alongside the built-ins
// on the next Init. Templates are data, so the loader must not be
// bound to the embedded set.
func TestInit_SeedsUserAuthoredTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	// First init materializes the templates dir.
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	// Drop a user-authored template. The goal is deliberately non-ML so
	// the test proves the hub has no opinion about the domain.
	yaml := `name: onboard-newhire
kind: goal
goal: "Walk {name} through day-one setup."
parameters:
  name: ""
  team: ""
on_create_template_id: agents.steward
`
	path := filepath.Join(dir, "team", "templates", "projects", "onboard-newhire.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write user template: %v", err)
	}
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var params, kind string
	var isTemplate int
	err = db.QueryRowContext(context.Background(), `
		SELECT kind, is_template, parameters_json FROM projects
		WHERE team_id = ? AND name = ?`,
		defaultTeamID, "onboard-newhire").Scan(&kind, &isTemplate, &params)
	if err != nil {
		t.Fatalf("lookup user template: %v", err)
	}
	if isTemplate != 1 {
		t.Errorf("is_template = %d, want 1", isTemplate)
	}
	if kind != "goal" {
		t.Errorf("kind = %q, want goal", kind)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		t.Fatalf("parse parameters_json %q: %v", params, err)
	}
	for _, k := range []string{"name", "team"} {
		if _, ok := p[k]; !ok {
			t.Errorf("user template parameters missing %q", k)
		}
	}
}

// TestInit_SeedIsIdempotent confirms re-running Init on the same data root
// does not duplicate the template row or error out.
func TestInit_SeedIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hub.db")
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	for _, name := range []string{"ablation-sweep", "write-memo", "benchmark-comparison", "reproduce-paper"} {
		var count int
		if err := db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM projects WHERE team_id = ? AND name = ?`,
			defaultTeamID, name).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 1 {
			t.Errorf("%s row count = %d after re-init, want 1", name, count)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
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

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, "ablation-sweep").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("ablation-sweep row count = %d after re-init, want 1", count)
	}
}

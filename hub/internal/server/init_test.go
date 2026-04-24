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

// TestLoadProjectTemplates_OverviewWidget confirms the YAML-declared
// overview_widget round-trips for valid values, defaults to empty (→
// task_milestone_list at resolve time) when missing, and is normalized
// to empty for unknown values (with a warning logged — W4 A+B chassis).
func TestLoadProjectTemplates_OverviewWidget(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, filepath.Join(dir, "hub.db")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Drop user-authored templates exercising each code path.
	userYAMLs := map[string]string{
		"ow-known.yaml": `name: ow-known
kind: goal
goal: "k"
overview_widget: recent_artifacts
on_create_template_id: agents.steward
`,
		"ow-unknown.yaml": `name: ow-unknown
kind: goal
goal: "u"
overview_widget: not_a_real_widget
on_create_template_id: agents.steward
`,
		"ow-missing.yaml": `name: ow-missing
kind: goal
goal: "m"
on_create_template_id: agents.steward
`,
	}
	for fname, body := range userYAMLs {
		path := filepath.Join(dir, "team", "templates", "projects", fname)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", fname, err)
		}
	}

	docs, err := loadProjectTemplates(dir)
	if err != nil {
		t.Fatalf("loadProjectTemplates: %v", err)
	}
	byName := map[string]projectTemplateDoc{}
	for _, d := range docs {
		byName[d.Name] = d
	}

	// Known value survives.
	if got := byName["ow-known"].OverviewWidget; got != "recent_artifacts" {
		t.Errorf("ow-known OverviewWidget = %q, want recent_artifacts", got)
	}
	// Unknown is scrubbed to "" so normalize → default.
	if got := byName["ow-unknown"].OverviewWidget; got != "" {
		t.Errorf("ow-unknown OverviewWidget = %q, want \"\" (normalized from unknown)", got)
	}
	if got := normalizeOverviewWidget(byName["ow-unknown"].OverviewWidget); got != overviewWidgetDefault {
		t.Errorf("normalized unknown = %q, want %q", got, overviewWidgetDefault)
	}
	// Missing declares "" which normalize → default.
	if got := byName["ow-missing"].OverviewWidget; got != "" {
		t.Errorf("ow-missing OverviewWidget = %q, want \"\"", got)
	}
	if got := normalizeOverviewWidget(byName["ow-missing"].OverviewWidget); got != overviewWidgetDefault {
		t.Errorf("normalized missing = %q, want %q", got, overviewWidgetDefault)
	}

	// Built-in templates ship with the expected hero kinds.
	wantBuiltin := map[string]string{
		"ablation-sweep":       "sweep_compare",
		"benchmark-comparison": "sweep_compare",
		"reproduce-paper":      "recent_artifacts",
		"write-memo":           "task_milestone_list",
	}
	for name, want := range wantBuiltin {
		got := normalizeOverviewWidget(byName[name].OverviewWidget)
		if got != want {
			t.Errorf("built-in %s OverviewWidget = %q, want %q", name, got, want)
		}
	}
}

// TestNormalizeOverviewWidget_Fallback is the unit-level guarantee the
// mobile registry relies on: empty and unknown both → default.
func TestNormalizeOverviewWidget_Fallback(t *testing.T) {
	cases := map[string]string{
		"":                    overviewWidgetDefault,
		"task_milestone_list": "task_milestone_list",
		"sweep_compare":       "sweep_compare",
		"recent_artifacts":    "recent_artifacts",
		"children_status":     "children_status",
		"recent_firings_list": "recent_firings_list",
		"not_a_widget":        overviewWidgetDefault,
		"SWEEP_COMPARE":       overviewWidgetDefault, // case-sensitive by design
	}
	for in, want := range cases {
		if got := normalizeOverviewWidget(in); got != want {
			t.Errorf("normalizeOverviewWidget(%q) = %q, want %q", in, got, want)
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

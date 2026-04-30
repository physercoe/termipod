// seed_demo_lifecycle_test.go — coverage for `seed-demo --shape
// lifecycle` (W6). Verifies the seed inserts the expected row
// shapes so the mobile UI renders without running phases live.

package server

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func TestSeedLifecycleDemo_InsertsExpectedShape(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	res, err := SeedLifecycleDemo(ctx, db)
	if err != nil {
		t.Fatalf("SeedLifecycleDemo: %v", err)
	}
	if res.Skipped {
		t.Fatal("first call: Skipped=true unexpected")
	}

	// Project exists with the expected name + template_id.
	var projName, templateID, paramsJSON string
	if err := db.QueryRowContext(ctx,
		`SELECT name, COALESCE(template_id,''), COALESCE(parameters_json,'{}')
		 FROM projects WHERE id = ?`, res.ProjectID).
		Scan(&projName, &templateID, &paramsJSON); err != nil {
		t.Fatalf("project lookup: %v", err)
	}
	if projName != lifecycleDemoProjectName {
		t.Errorf("project name=%q; want %q", projName, lifecycleDemoProjectName)
	}
	if templateID != "research-project.v1" {
		t.Errorf("project template_id=%q; want research-project.v1", templateID)
	}
	if !strings.Contains(paramsJSON, `"idea"`) {
		t.Errorf("parameters_json missing idea: %s", paramsJSON)
	}

	// Plan exists with 5 phases in spec_json + status running.
	var planSpec, planStatus string
	if err := db.QueryRowContext(ctx,
		`SELECT spec_json, status FROM plans WHERE id = ?`, res.PlanID).
		Scan(&planSpec, &planStatus); err != nil {
		t.Fatalf("plan lookup: %v", err)
	}
	if planStatus != "running" {
		t.Errorf("plan status=%q; want running", planStatus)
	}
	// Note: Go's json.Marshal escapes `&` as `&` by default, so
	// the literal "Method & Code" doesn't appear verbatim — search
	// for the JSON-encoded form for that phase.
	expected := []string{"Bootstrap", "Lit Review", `Method & Code`, "Experiment", "Paper"}
	for _, phase := range expected {
		if !strings.Contains(planSpec, phase) {
			t.Errorf("plan spec missing phase %q: %s", phase, planSpec)
		}
	}

	// Plan steps: 5 rows, 0+1 completed, 2 in_progress, 3+4 pending.
	wantStatus := map[int]string{
		0: "completed",
		1: "completed",
		2: "in_progress",
		3: "pending",
		4: "pending",
	}
	rows, err := db.QueryContext(ctx,
		`SELECT phase_idx, status FROM plan_steps WHERE plan_id = ?
		 ORDER BY phase_idx`, res.PlanID)
	if err != nil {
		t.Fatalf("plan_steps query: %v", err)
	}
	defer rows.Close()
	gotPhases := map[int]string{}
	for rows.Next() {
		var phase int
		var status string
		if err := rows.Scan(&phase, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		gotPhases[phase] = status
	}
	for p, want := range wantStatus {
		if got := gotPhases[p]; got != want {
			t.Errorf("phase %d status=%q; want %q", p, got, want)
		}
	}

	// Steward agent exists with kind=steward.research.v1, role
	// derives to steward via the manifest.
	var stewardKind string
	if err := db.QueryRowContext(ctx,
		`SELECT kind FROM agents WHERE id = ?`, res.StewardAgentID).
		Scan(&stewardKind); err != nil {
		t.Fatalf("steward lookup: %v", err)
	}
	if stewardKind != "steward.research.v1" {
		t.Errorf("steward kind=%q; want steward.research.v1", stewardKind)
	}

	// Coder agent exists with parent_agent_id = steward.
	var coderKind, coderParent string
	if err := db.QueryRowContext(ctx,
		`SELECT kind, COALESCE(parent_agent_id,'') FROM agents WHERE id = ?`,
		res.CoderAgentID).Scan(&coderKind, &coderParent); err != nil {
		t.Fatalf("coder lookup: %v", err)
	}
	if coderKind != "coder.v1" {
		t.Errorf("coder kind=%q; want coder.v1", coderKind)
	}
	if coderParent != res.StewardAgentID {
		t.Errorf("coder parent=%q; want %q", coderParent, res.StewardAgentID)
	}

	// Documents: lit-review and method draft.
	var litTitle, methodTitle string
	if err := db.QueryRowContext(ctx,
		`SELECT title FROM documents WHERE id = ?`, res.LitReviewDocID).
		Scan(&litTitle); err != nil {
		t.Fatalf("lit-review doc lookup: %v", err)
	}
	if !strings.Contains(litTitle, "Lit review") {
		t.Errorf("lit-review title=%q", litTitle)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT title FROM documents WHERE id = ?`, res.MethodDocID).
		Scan(&methodTitle); err != nil {
		t.Fatalf("method doc lookup: %v", err)
	}
	if !strings.Contains(methodTitle, "Method") {
		t.Errorf("method title=%q", methodTitle)
	}

	// Attention item: open select kind on this project.
	var attStatus, attKind string
	if err := db.QueryRowContext(ctx,
		`SELECT status, kind FROM attention_items WHERE id = ?`, res.AttentionID).
		Scan(&attStatus, &attKind); err != nil {
		t.Fatalf("attention lookup: %v", err)
	}
	if attStatus != "open" || attKind != "select" {
		t.Errorf("attention status=%q kind=%q; want open/select", attStatus, attKind)
	}
}

func TestSeedLifecycleDemo_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	first, err := SeedLifecycleDemo(ctx, db)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	second, err := SeedLifecycleDemo(ctx, db)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if !second.Skipped {
		t.Errorf("second call: Skipped=false; want true")
	}
	if second.ProjectID != first.ProjectID {
		t.Errorf("second call returned different project id: %s vs %s",
			second.ProjectID, first.ProjectID)
	}
}

func TestResetLifecycleDemo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	// Reset on empty DB → no-op, returns deleted=false.
	if deleted, err := ResetLifecycleDemo(ctx, db); err != nil || deleted {
		t.Fatalf("empty reset: deleted=%v err=%v", deleted, err)
	}

	// Seed, then reset, then verify project gone.
	first, err := SeedLifecycleDemo(ctx, db)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	deleted, err := ResetLifecycleDemo(ctx, db)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !deleted {
		t.Error("reset: deleted=false after seed")
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM projects WHERE id = ?`, first.ProjectID).
		Scan(&count); err != nil {
		t.Fatalf("post-reset project count: %v", err)
	}
	if count != 0 {
		t.Errorf("project still exists after reset (count=%d)", count)
	}
	// Plan should be gone too (CASCADE plus our explicit delete).
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM plans WHERE id = ?`, first.PlanID).
		Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatalf("post-reset plan count: %v", err)
	}
	if count != 0 {
		t.Errorf("plan still exists after reset (count=%d)", count)
	}
	// Re-seed succeeds (proves reset cleaned thoroughly).
	if _, err := SeedLifecycleDemo(ctx, db); err != nil {
		t.Fatalf("re-seed after reset: %v", err)
	}
}

// TestLifecycleSeed_BundledPlanTemplate verifies the bundled
// research-project.v1.yaml file is reachable via the embed.FS so
// stewards (and tests against template-loading) can read it.
func TestLifecycleSeed_BundledPlanTemplate(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	if _, err := Init(dir, dbPath); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// writeBuiltinTemplates ran inside New(); the plan template
	// should have been copied to the team overlay.
	body, err := s.loadBuiltinAgentTemplate("steward.research.v1.yaml")
	if err != nil {
		t.Fatalf("seed agent template missing: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("seed agent body empty")
	}

	// Verify the plan file is on disk under team/templates/plans/.
	overlayPath := dir + "/team/templates/plans/research-project.v1.yaml"
	if _, err := os.Stat(overlayPath); err != nil {
		t.Errorf("plan template not copied to overlay at %s: %v", overlayPath, err)
	}
}

package server

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSeedDemo_InsertsExpectedRows covers the happy path: a fresh hub DB
// gets one demo project, 6 runs (3 sizes × 2 optimizers), a matching
// run_metrics row per run, one briefing document, one pending review,
// and one open attention_item.
func TestSeedDemo_InsertsExpectedRows(t *testing.T) {
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

	ctx := context.Background()
	res, err := SeedDemo(ctx, db)
	if err != nil {
		t.Fatalf("SeedDemo: %v", err)
	}
	if res.Skipped {
		t.Fatalf("first call reported Skipped=true; want fresh insert")
	}
	if res.ProjectID == "" {
		t.Fatalf("ProjectID empty")
	}
	if len(res.RunIDs) != 6 {
		t.Errorf("RunIDs len = %d, want 6", len(res.RunIDs))
	}

	// Row-count assertions — keeps the test honest about scope.
	cases := []struct {
		label string
		query string
		args  []any
		want  int
	}{
		{"projects",
			`SELECT COUNT(*) FROM projects WHERE team_id = ? AND name = ? AND is_template = 0`,
			[]any{defaultTeamID, demoProjectName}, 1},
		{"runs",
			`SELECT COUNT(*) FROM runs WHERE project_id = ? AND status = 'completed'`,
			[]any{res.ProjectID}, 6},
		{"run_metrics",
			`SELECT COUNT(*) FROM run_metrics WHERE metric_name = 'loss' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 6},
		{"documents",
			`SELECT COUNT(*) FROM documents WHERE project_id = ? AND kind = 'memo'`,
			[]any{res.ProjectID}, 1},
		{"reviews_pending",
			`SELECT COUNT(*) FROM reviews WHERE project_id = ? AND state = 'pending'`,
			[]any{res.ProjectID}, 1},
		{"attention_open",
			`SELECT COUNT(*) FROM attention_items WHERE project_id = ? AND status = 'open'`,
			[]any{res.ProjectID}, 1},
	}
	for _, c := range cases {
		var n int
		if err := db.QueryRowContext(ctx, c.query, c.args...).Scan(&n); err != nil {
			t.Errorf("%s count: %v", c.label, err)
			continue
		}
		if n != c.want {
			t.Errorf("%s count = %d, want %d", c.label, n, c.want)
		}
	}

	// Each run_metrics row should carry a 100-point curve with a sensible
	// last_value (monotonically-ish approaching the per-run floor).
	var minPts, maxPts int
	if err := db.QueryRowContext(ctx,
		`SELECT MIN(sample_count), MAX(sample_count) FROM run_metrics
		 WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
		res.ProjectID).Scan(&minPts, &maxPts); err != nil {
		t.Fatalf("sample_count bounds: %v", err)
	}
	if minPts != 100 || maxPts != 100 {
		t.Errorf("sample_count range = [%d,%d], want [100,100]", minPts, maxPts)
	}
}

// TestSeedDemo_IsIdempotent re-runs the seeder and asserts the second call
// touches nothing and returns Skipped=true with the same ProjectID.
func TestSeedDemo_IsIdempotent(t *testing.T) {
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
	ctx := context.Background()

	first, err := SeedDemo(ctx, db)
	if err != nil {
		t.Fatalf("first SeedDemo: %v", err)
	}
	if first.Skipped {
		t.Fatalf("first call reported Skipped=true")
	}

	second, err := SeedDemo(ctx, db)
	if err != nil {
		t.Fatalf("second SeedDemo: %v", err)
	}
	if !second.Skipped {
		t.Errorf("second call Skipped = false, want true")
	}
	if second.ProjectID != first.ProjectID {
		t.Errorf("second ProjectID = %q, want %q", second.ProjectID, first.ProjectID)
	}

	// Re-running must not create extra run/document/review rows.
	var runs, docs, reviews int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE project_id = ?`, first.ProjectID).Scan(&runs)
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM documents WHERE project_id = ?`, first.ProjectID).Scan(&docs)
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reviews WHERE project_id = ?`, first.ProjectID).Scan(&reviews)
	if runs != 6 || docs != 1 || reviews != 1 {
		t.Errorf("row counts after 2nd call: runs=%d docs=%d reviews=%d; want 6/1/1",
			runs, docs, reviews)
	}
}

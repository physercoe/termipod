package server

import (
	"context"
	"os"
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
	res, err := SeedDemo(ctx, db, dir)
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
	if res.ImageCount != 18 {
		t.Errorf("ImageCount = %d, want 18", res.ImageCount)
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
			// 23 series per run × 6 runs. See synthRunCurves for the list:
			// loss/{train,val}, learning_rate, grad_norm,
			// throughput/tokens_per_sec, smooth/{train_raw,train_ema},
			// sys/{gpu_util,gpu_mem,cpu_util}, weights_dist/p{5,25,50,75,95},
			// eval/{perplexity,bleu,accuracy}, grokking/success_rate,
			// grads/{layer0..3}.
			`SELECT COUNT(*) FROM run_metrics WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 138},
		{"run_metrics_loss_train",
			`SELECT COUNT(*) FROM run_metrics WHERE metric_name = 'loss/train' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 6},
		{"run_metrics_loss_val",
			`SELECT COUNT(*) FROM run_metrics WHERE metric_name = 'loss/val' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 6},
		{"run_metrics_weights_dist",
			// Five percentile series per run × 6 runs.
			`SELECT COUNT(*) FROM run_metrics WHERE metric_name LIKE 'weights_dist/%' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 30},
		{"run_metrics_sys",
			`SELECT COUNT(*) FROM run_metrics WHERE metric_name LIKE 'sys/%' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 18},
		{"documents",
			`SELECT COUNT(*) FROM documents WHERE project_id = ? AND kind = 'memo'`,
			[]any{res.ProjectID}, 1},
		{"sample_documents",
			// One text-sample document per run.
			`SELECT COUNT(*) FROM documents WHERE project_id = ? AND kind = 'sample'`,
			[]any{res.ProjectID}, 6},
		{"reviews_pending",
			`SELECT COUNT(*) FROM reviews WHERE project_id = ? AND state = 'pending'`,
			[]any{res.ProjectID}, 1},
		{"attention_open",
			`SELECT COUNT(*) FROM attention_items WHERE project_id = ? AND status = 'open'`,
			[]any{res.ProjectID}, 1},
		{"run_images",
			// 3 checkpoint PNGs per run × 6 runs = 18.
			`SELECT COUNT(*) FROM run_images WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 18},
		{"run_images_generations",
			`SELECT COUNT(*) FROM run_images WHERE metric_name = 'samples/generations' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 18},
		{"run_histograms",
			// 2 histograms × 4 checkpoints × 6 runs = 48.
			`SELECT COUNT(*) FROM run_histograms WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 48},
		{"run_histograms_grads",
			`SELECT COUNT(*) FROM run_histograms WHERE metric_name = 'grads_hist/layer0' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 24},
		{"run_histograms_weights",
			`SELECT COUNT(*) FROM run_histograms WHERE metric_name = 'weights_hist/all' AND run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			[]any{res.ProjectID}, 24},
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

	// Seeded attention_item should carry the steward as actor so the mobile
	// StewardBadge lights up without heuristics (migration 0016).
	var actorKind, actorHandle string
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(actor_kind, ''), COALESCE(actor_handle, '')
		 FROM attention_items WHERE id = ?`, res.Attention).
		Scan(&actorKind, &actorHandle); err != nil {
		t.Fatalf("read attention actor: %v", err)
	}
	if actorKind != "agent" || actorHandle != "steward" {
		t.Errorf("attention actor = (%q, %q), want (agent, steward)",
			actorKind, actorHandle)
	}

	// Dense curves should carry 100 points; sparse eval curves should
	// carry 10. Min=10 (eval/*), max=100 (everything else).
	var minPts, maxPts int
	if err := db.QueryRowContext(ctx,
		`SELECT MIN(sample_count), MAX(sample_count) FROM run_metrics
		 WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
		res.ProjectID).Scan(&minPts, &maxPts); err != nil {
		t.Fatalf("sample_count bounds: %v", err)
	}
	if minPts != 10 || maxPts != 100 {
		t.Errorf("sample_count range = [%d,%d], want [10,100]", minPts, maxPts)
	}

	// Spot-check one row from each new family — if these exist at all,
	// the metric is wired; curve shape is covered by the row counts.
	newFamilies := []string{
		"eval/perplexity", "eval/bleu", "eval/accuracy",
		"grokking/success_rate",
		"grads/layer0", "grads/layer3",
	}
	for _, name := range newFamilies {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM run_metrics
			 WHERE metric_name = ? AND
			       run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
			name, res.ProjectID).Scan(&n); err != nil {
			t.Errorf("count %s: %v", name, err)
			continue
		}
		if n != 6 {
			t.Errorf("%s present in %d runs, want 6", name, n)
		}
	}

	// Every run_images row must point at a blobs table entry and the
	// corresponding file on disk must exist — the mobile client loads
	// PNGs via GET /v1/blobs/{sha}, which hits the disk path.
	imgRows, err := db.QueryContext(ctx, `
		SELECT ri.blob_sha, b.scope_path, b.size, b.mime
		FROM run_images ri
		JOIN blobs b ON b.sha256 = ri.blob_sha
		WHERE ri.run_id IN (SELECT id FROM runs WHERE project_id = ?)`,
		res.ProjectID)
	if err != nil {
		t.Fatalf("join run_images+blobs: %v", err)
	}
	defer imgRows.Close()
	var checked int
	for imgRows.Next() {
		var sha, path, mime string
		var size int64
		if err := imgRows.Scan(&sha, &path, &size, &mime); err != nil {
			t.Errorf("scan run_images row: %v", err)
			continue
		}
		if mime != "image/png" {
			t.Errorf("blob %s mime = %q, want image/png", sha, mime)
		}
		if size <= 0 {
			t.Errorf("blob %s size = %d, want >0", sha, size)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("blob %s on disk: %v", sha, err)
		}
		checked++
	}
	if checked != 18 {
		t.Errorf("checked %d run_images+blobs rows, want 18", checked)
	}
}

// TestResetDemo_WipesAndAllowsReSeed covers the -reset path: seed once,
// call ResetDemo, seed again; final state must match a single seed with
// a fresh project id.
func TestResetDemo_WipesAndAllowsReSeed(t *testing.T) {
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

	first, err := SeedDemo(ctx, db, dir)
	if err != nil {
		t.Fatalf("first SeedDemo: %v", err)
	}

	deleted, err := ResetDemo(ctx, db)
	if err != nil {
		t.Fatalf("ResetDemo: %v", err)
	}
	if !deleted {
		t.Fatalf("ResetDemo reported deleted=false after seed; want true")
	}

	// After reset: no rows should reference the old project id.
	tables := []struct {
		label string
		query string
	}{
		{"projects", `SELECT COUNT(*) FROM projects WHERE id = ?`},
		{"runs", `SELECT COUNT(*) FROM runs WHERE project_id = ?`},
		{"documents", `SELECT COUNT(*) FROM documents WHERE project_id = ?`},
		{"reviews", `SELECT COUNT(*) FROM reviews WHERE project_id = ?`},
		{"attention_items", `SELECT COUNT(*) FROM attention_items WHERE project_id = ?`},
		{"run_metrics (cascade)", `SELECT COUNT(*) FROM run_metrics
			WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`},
		{"run_images (cascade)", `SELECT COUNT(*) FROM run_images
			WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`},
		{"run_histograms (cascade)", `SELECT COUNT(*) FROM run_histograms
			WHERE run_id IN (SELECT id FROM runs WHERE project_id = ?)`},
	}
	for _, c := range tables {
		var n int
		if err := db.QueryRowContext(ctx, c.query, first.ProjectID).Scan(&n); err != nil {
			t.Errorf("post-reset count %s: %v", c.label, err)
			continue
		}
		if n != 0 {
			t.Errorf("post-reset %s count = %d, want 0", c.label, n)
		}
	}

	// Second seed must succeed and mint a new project id.
	second, err := SeedDemo(ctx, db, dir)
	if err != nil {
		t.Fatalf("second SeedDemo: %v", err)
	}
	if second.Skipped {
		t.Fatalf("second SeedDemo after reset reported Skipped=true; want fresh insert")
	}
	if second.ProjectID == first.ProjectID {
		t.Errorf("post-reset project id = %q (same as pre-reset); want a new id",
			second.ProjectID)
	}

	// And ResetDemo on an empty hub should no-op.
	if _, err := ResetDemo(ctx, db); err != nil {
		t.Fatalf("second ResetDemo: %v", err)
	}
	if _, err := ResetDemo(ctx, db); err != nil {
		t.Fatalf("ResetDemo no-op after reset: %v", err)
	}
	emptyDeleted, err := (func() (bool, error) {
		// run a third reset to confirm no-op semantics
		return ResetDemo(ctx, db)
	})()
	if err != nil {
		t.Fatalf("third ResetDemo: %v", err)
	}
	// After two resets the demo is gone; third reset should report
	// deleted=false (nothing to wipe).
	if emptyDeleted {
		t.Errorf("ResetDemo on empty demo reported deleted=true; want false")
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

	first, err := SeedDemo(ctx, db, dir)
	if err != nil {
		t.Fatalf("first SeedDemo: %v", err)
	}
	if first.Skipped {
		t.Fatalf("first call reported Skipped=true")
	}

	second, err := SeedDemo(ctx, db, dir)
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
	// Documents = 1 memo + 6 sample docs = 7.
	var runs, docs, reviews int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE project_id = ?`, first.ProjectID).Scan(&runs)
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM documents WHERE project_id = ?`, first.ProjectID).Scan(&docs)
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reviews WHERE project_id = ?`, first.ProjectID).Scan(&reviews)
	if runs != 6 || docs != 7 || reviews != 1 {
		t.Errorf("row counts after 2nd call: runs=%d docs=%d reviews=%d; want 6/7/1",
			runs, docs, reviews)
	}
}

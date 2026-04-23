package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
)

// SeedDemoResult summarizes what seed-demo inserted (or skipped) so the
// CLI can print a useful one-liner and callers can assert in tests.
type SeedDemoResult struct {
	ProjectID  string
	RunIDs     []string
	DocumentID string
	ReviewID   string
	Attention  string
	Skipped    bool // true when the demo project already existed
}

// demoProjectName is the concrete (non-template) project row seed-demo
// lands. Distinct from the `ablation-sweep` template so the two can
// coexist in the Projects tab — the template seeds in init.go as
// is_template=1; this seeds is_template=0 as a ready-to-view project.
const demoProjectName = "ablation-sweep-demo"

// SeedDemo fills a fresh hub with the state a reviewer needs to *see* the
// research demo (blueprint §9 P4) on the phone without actually running
// nanoGPT on a GPU. It writes:
//
//   - 1 project (name "ablation-sweep-demo") with parameters_json set to the
//     same {model_sizes, optimizers, iters} shape as the ablation-sweep
//     template.
//   - len(model_sizes) * len(optimizers) run rows, status=completed, with
//     synthetic trackio_run_uri values so the mobile Run Detail screen can
//     link out even though no actual trackio process is involved.
//   - One run_metrics row per run with a "loss" curve: 100 (step, value)
//     pairs following exponential decay + small noise. Final loss
//     depends on (size, optimizer) so different runs visibly differ on
//     the sparkline.
//   - One briefing document (markdown memo) + one pending review against it.
//   - One open attention_item (decision — nightly budget approval) so the
//     Inbox has something to approve.
//
// Idempotent: if a project named demoProjectName already exists in team
// defaultTeamID, returns Skipped=true without touching anything.
func SeedDemo(ctx context.Context, db *sql.DB) (*SeedDemoResult, error) {
	if db == nil {
		return nil, fmt.Errorf("SeedDemo: nil db")
	}
	// Idempotency check.
	var existingID string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, demoProjectName).Scan(&existingID)
	if err == nil {
		return &SeedDemoResult{ProjectID: existingID, Skipped: true}, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check existing demo project: %w", err)
	}

	// Ensure default team exists (normally seeded by Init, but SeedDemo may
	// run against a hub that was migrated by a test harness without Init).
	if err := ensureTeam(ctx, db, defaultTeamID, "default"); err != nil {
		return nil, fmt.Errorf("ensure team: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	res := &SeedDemoResult{}
	now := NowUTC()

	sizes := []int{128, 256, 384}
	optimizers := []string{"adamw", "lion"}
	iters := 1000
	params := map[string]any{
		"model_sizes": sizes,
		"optimizers":  optimizers,
		"iters":       iters,
	}
	paramsJSON, _ := json.Marshal(params)

	res.ProjectID = NewID()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO projects
			(id, team_id, name, status, config_yaml, created_at,
			 goal, kind, is_template, parameters_json)
		VALUES (?, ?, ?, 'active', '', ?, ?, 'goal', 0, ?)`,
		res.ProjectID, defaultTeamID, demoProjectName, now,
		"nanoGPT-Shakespeare ablation sweep (seeded demo state; no real training ran)",
		string(paramsJSON))
	if err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}

	rng := rand.New(rand.NewSource(1)) // deterministic for reproducible demo state
	for _, size := range sizes {
		for _, opt := range optimizers {
			runID := NewID()
			res.RunIDs = append(res.RunIDs, runID)
			uri := fmt.Sprintf("trackio://ablation-sweep-demo/size%d-%s", size, opt)
			configBlob, _ := json.Marshal(map[string]any{
				"n_embd":    size,
				"optimizer": opt,
				"iters":     iters,
			})
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO runs (
					id, project_id, config_json, seed, status,
					started_at, finished_at, trackio_run_uri,
					created_at
				) VALUES (?, ?, ?, 42, 'completed', ?, ?, ?, ?)`,
				runID, res.ProjectID, string(configBlob), now, now, uri, now,
			); err != nil {
				return nil, fmt.Errorf("insert run size=%d opt=%s: %w", size, opt, err)
			}

			points, lastStep, lastVal := synthLossCurve(rng, size, opt, iters, 100)
			pointsJSON, _ := json.Marshal(points)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO run_metrics (
					id, run_id, metric_name, points_json, sample_count,
					last_step, last_value, updated_at
				) VALUES (?, ?, 'loss', ?, ?, ?, ?, ?)`,
				NewID(), runID, string(pointsJSON), len(points), lastStep, lastVal, now,
			); err != nil {
				return nil, fmt.Errorf("insert run_metrics: %w", err)
			}
		}
	}

	// Briefing document — markdown memo summarizing the sweep.
	res.DocumentID = NewID()
	memo := buildDemoMemo(sizes, optimizers, iters)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO documents (
			id, project_id, kind, title, version, content_inline, created_at
		) VALUES (?, ?, 'memo', ?, 1, ?, ?)`,
		res.DocumentID, res.ProjectID,
		"Overnight briefing — ablation sweep",
		memo, now,
	); err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}

	res.ReviewID = NewID()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO reviews (
			id, project_id, target_kind, target_id, state,
			comment, created_at
		) VALUES (?, ?, 'document', ?, 'pending', ?, ?)`,
		res.ReviewID, res.ProjectID, res.DocumentID,
		"Briefing agent posted overnight summary — requesting steward sign-off.",
		now,
	); err != nil {
		return nil, fmt.Errorf("insert review: %w", err)
	}

	// One open attention item — decision (minor) so the Inbox has teeth.
	res.Attention = NewID()
	assignees, _ := json.Marshal([]string{"@steward"})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json,
			decisions_json, escalation_history_json,
			status, created_at
		) VALUES (?, ?, 'project', ?, 'decision',
		          ?, 'minor', ?,
		          '[]', '[]',
		          'open', ?)`,
		res.Attention, res.ProjectID, res.ProjectID,
		"Approve nightly sweep budget ($2/run × 6 runs)?",
		string(assignees), now,
	); err != nil {
		return nil, fmt.Errorf("insert attention: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// synthLossCurve generates a plausible training-loss curve for a (size,
// optimizer) pair. Shape: exponential decay from ~4.0 towards a floor
// that depends on the config, with per-step noise proportional to the
// residual so early-training noise dominates. Returns points (as
// [][2]any for JSON shape compatibility), the last step, and the last
// value so callers don't have to recompute them.
func synthLossCurve(rng *rand.Rand, size int, optimizer string, iters, points int) ([][2]any, int64, float64) {
	// Floor encodes "bigger model + lion > smaller model + adamw" so the
	// sparkline visibly differs across runs.
	floor := 2.2
	switch size {
	case 256:
		floor -= 0.3
	case 384:
		floor -= 0.55
	}
	if optimizer == "lion" {
		floor -= 0.15
	}
	// Decay rate also varies a bit — lion converges slightly faster.
	tau := float64(iters) / 4.0
	if optimizer == "lion" {
		tau *= 0.85
	}
	start := 4.0

	out := make([][2]any, 0, points)
	stride := int64(iters / points)
	if stride < 1 {
		stride = 1
	}
	var step int64
	var last float64
	for i := 0; i < points; i++ {
		step = int64(i) * stride
		clean := floor + (start-floor)*math.Exp(-float64(step)/tau)
		noise := (rng.Float64() - 0.5) * 0.12 * (clean - floor + 0.05)
		v := clean + noise
		if v < 0 {
			v = 0
		}
		out = append(out, [2]any{step, roundTo(v, 4)})
		last = v
	}
	return out, step, roundTo(last, 4)
}

func roundTo(v float64, places int) float64 {
	m := math.Pow(10, float64(places))
	return math.Round(v*m) / m
}

func buildDemoMemo(sizes []int, optimizers []string, iters int) string {
	return "# Overnight briefing — ablation sweep\n\n" +
		"**Goal:** Ablate nanoGPT-Shakespeare across model sizes and optimizers.\n\n" +
		fmt.Sprintf("**What ran:** %d runs (sizes=%v × optimizers=%v, iters=%d).\n\n",
			len(sizes)*len(optimizers), sizes, optimizers, iters) +
		"**Takeaway:** Lion beats AdamW at every size; the 384-embd + Lion run\n" +
		"reaches the lowest final loss by a visible margin. Smaller models\n" +
		"plateau well above 2.0 regardless of optimizer.\n\n" +
		"**Caveats:** Seeded from synthetic data for UI testing — no real\n" +
		"training happened. Numbers will change once a GPU host is attached\n" +
		"and a real trackio-producing worker runs the sweep.\n\n" +
		"**Plots:** See the Run Detail screen for each run's loss sparkline.\n"
}

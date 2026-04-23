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

			curves := synthRunCurves(rng, size, opt, iters, 100)
			for _, c := range curves {
				pointsJSON, _ := json.Marshal(c.points)
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO run_metrics (
						id, run_id, metric_name, points_json, sample_count,
						last_step, last_value, updated_at
					) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
					NewID(), runID, c.name, string(pointsJSON),
					len(c.points), c.lastStep, c.lastValue, now,
				); err != nil {
					return nil, fmt.Errorf("insert run_metrics %s: %w", c.name, err)
				}
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

// demoCurve is one seeded metric family for a single run.
type demoCurve struct {
	name      string
	points    [][2]any
	lastStep  int64
	lastValue float64
}

// synthRunCurves emits the five metric families the mobile Run Detail
// screen renders as separate sparklines, shaped so the ablation story is
// visible in the data:
//
//   - train_loss / val_loss: exponential decay; val tracks train with a
//     small gap that widens slightly at the end (mild overfit).
//   - learning_rate: linear warmup → cosine decay, identical across runs
//     (acts as a control curve).
//   - grad_norm: starts high, decays roughly like a noisy 1/sqrt(step).
//   - tokens_per_sec: approximately constant with jitter; lower for
//     bigger models (compute scales).
//
// Same deterministic rng is shared across all curves so the seed=1
// reproducible-demo guarantee still holds.
func synthRunCurves(rng *rand.Rand, size int, optimizer string, iters, points int) []demoCurve {
	trainPts, trainStep, trainLast := synthLossCurve(rng, size, optimizer, iters, points)

	// val_loss: same decay shape, slightly higher floor (overfit gap) and
	// a bit more noise so the two curves are visually distinguishable
	// when overlaid. Offset grows with step so late-training divergence
	// is visible.
	valPts := make([][2]any, len(trainPts))
	var valLast float64
	for i, p := range trainPts {
		step := p[0].(int64)
		tv := p[1].(float64)
		frac := float64(step) / float64(iters)
		gap := 0.04 + 0.18*frac // widens from +0.04 to +0.22
		noise := (rng.Float64() - 0.5) * 0.06
		v := tv + gap + noise
		valPts[i] = [2]any{step, roundTo(v, 4)}
		valLast = v
	}

	// learning_rate: linear warmup for first 10% then cosine decay to 0.
	// Peak depends weakly on optimizer (lion runs at slightly lower lr).
	peakLR := 6e-4
	if optimizer == "lion" {
		peakLR = 3e-4
	}
	warmup := int64(iters) / 10
	lrPts := make([][2]any, len(trainPts))
	var lrLast float64
	for i, p := range trainPts {
		step := p[0].(int64)
		var lr float64
		if step < warmup {
			lr = peakLR * float64(step) / float64(warmup)
		} else {
			t := float64(step-warmup) / float64(int64(iters)-warmup)
			lr = peakLR * 0.5 * (1 + math.Cos(math.Pi*t))
		}
		lrPts[i] = [2]any{step, roundTo(lr, 6)}
		lrLast = lr
	}

	// grad_norm: noisy 1/sqrt(step), starts at ~3.0, clamps to 0.1 floor.
	gnPts := make([][2]any, len(trainPts))
	var gnLast float64
	for i, p := range trainPts {
		step := p[0].(int64)
		base := 3.0 / math.Sqrt(float64(step+1))
		if base < 0.1 {
			base = 0.1
		}
		v := base + (rng.Float64()-0.5)*0.4*base
		if v < 0.05 {
			v = 0.05
		}
		gnPts[i] = [2]any{step, roundTo(v, 4)}
		gnLast = v
	}

	// tokens_per_sec: baseline throughput inversely proportional to model
	// size (big model = fewer tok/s); jitter ±5%. Lion is a touch slower
	// than adamw per step — barely visible in the curve.
	baseTps := 18000.0
	switch size {
	case 256:
		baseTps = 12000.0
	case 384:
		baseTps = 7500.0
	}
	if optimizer == "lion" {
		baseTps *= 0.95
	}
	tpsPts := make([][2]any, len(trainPts))
	var tpsLast float64
	for i, p := range trainPts {
		step := p[0].(int64)
		v := baseTps + (rng.Float64()-0.5)*0.1*baseTps
		tpsPts[i] = [2]any{step, roundTo(v, 1)}
		tpsLast = v
	}

	// Metric names follow the wandb/tensorboard "<group>/<series>"
	// convention so the mobile UI can group curves that share a y-axis.
	// loss/{train,val} overlay on one chart; optim and throughput stand
	// alone with one series each.
	return []demoCurve{
		{"loss/train", trainPts, trainStep, trainLast},
		{"loss/val", valPts, trainStep, roundTo(valLast, 4)},
		{"optim/learning_rate", lrPts, trainStep, roundTo(lrLast, 6)},
		{"optim/grad_norm", gnPts, trainStep, roundTo(gnLast, 4)},
		{"throughput/tokens_per_sec", tpsPts, trainStep, roundTo(tpsLast, 1)},
	}
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
		"**Plots:** Each run carries three chart groups on the Run Detail\n" +
		"screen — `loss/{train,val}` overlaid, `optim/{learning_rate," +
		"grad_norm}`, and `throughput/tokens_per_sec`. The val/train gap\n" +
		"widens slightly over training; throughput drops as model size\n" +
		"grows, as expected.\n"
}

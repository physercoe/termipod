package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
)

// SeedDemoResult summarizes what seed-demo inserted (or skipped) so the
// CLI can print a useful one-liner and callers can assert in tests.
type SeedDemoResult struct {
	ProjectID  string
	RunIDs     []string
	DocumentID string
	ReviewID   string
	Attention  string
	ImageCount int  // total run_images rows inserted (0 when dataRoot is empty)
	Skipped    bool // true when the demo project already existed
	Reset      bool // true when the prior demo rows were deleted before re-inserting
}

// ResetDemo deletes the ablation-sweep-demo project and everything
// downstream (runs, run_metrics, documents, reviews, attention_items).
// Safe to call when no demo exists — returns with deleted=false.
//
// Needed because the mock data shape evolves (new metric families, new
// plot archetypes) but SeedDemo is skip-on-exists, so a pre-existing
// row on a reviewer's hub masks new seed content. Reviewers call
// `seed-demo -reset` to wipe and re-insert against the current code.
//
// Kept transactional: either the whole demo disappears or nothing does.
// Runs on reviewer hubs where other real projects + team state must
// survive — the WHERE clauses all key on project_id, never dropping
// non-demo data.
func ResetDemo(ctx context.Context, db *sql.DB) (deleted bool, err error) {
	if db == nil {
		return false, fmt.Errorf("ResetDemo: nil db")
	}
	var projectID string
	err = db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE team_id = ? AND name = ?`,
		defaultTeamID, demoProjectName).Scan(&projectID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up demo project: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Child tables with no FK cascade back to projects (see 0006_runs,
	// 0007_documents_reviews): clear explicitly. run_metrics (0014) and
	// run_images (0017) cascade from runs, so deleting runs is enough
	// for those. Blob files on disk are left behind — they're content-
	// addressed and harmless; a re-seed with identical inputs dedups.
	steps := []struct {
		label string
		query string
	}{
		{"documents", `DELETE FROM documents WHERE project_id = ?`},
		{"reviews", `DELETE FROM reviews WHERE project_id = ?`},
		{"runs", `DELETE FROM runs WHERE project_id = ?`},
		// attention_items + tasks cascade via projects.id FK, but
		// delete explicitly so the delete plan is readable and the
		// step list documents what the demo actually owns.
		{"attention_items", `DELETE FROM attention_items WHERE project_id = ?`},
		{"tasks", `DELETE FROM tasks WHERE project_id = ?`},
		{"projects", `DELETE FROM projects WHERE id = ?`},
	}
	for _, s := range steps {
		if _, err := tx.ExecContext(ctx, s.query, projectID); err != nil {
			return false, fmt.Errorf("reset %s: %w", s.label, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
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
//   - Fifteen run_metrics rows per run, one per metric family, each with
//     100 (step, value) pairs. Families cover the dominant wandb/
//     tensorboard plot archetypes (single scalar, multi-series overlay,
//     percentile band). See synthRunCurves for the full list.
//   - One text-sample document per run (kind='sample') carrying
//     nanoGPT-Shakespeare-style generations at three checkpoints.
//   - One briefing document (markdown memo) + one pending review against it.
//   - One open attention_item (decision — nightly budget approval) so the
//     Inbox has something to approve.
//
// Idempotent: if a project named demoProjectName already exists in team
// defaultTeamID, returns Skipped=true without touching anything.
//
// dataRoot is the hub's on-disk data directory (same one used for the
// content-addressed blobs store). When non-empty, seed-demo also writes
// 3 placeholder PNG checkpoints per run into run_images so the mobile
// Run Detail screen can exercise the image-panel archetype. Empty
// dataRoot skips image seeding — useful in unit tests that don't need
// filesystem side effects.
func SeedDemo(ctx context.Context, db *sql.DB, dataRoot string) (*SeedDemoResult, error) {
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

			// Per-run image series (plot archetype: wandb-style "Images"
			// panel). Three placeholder PNG checkpoints at steps 0, mid,
			// final — content visibly evolves with training progress so
			// the reviewer can scrub the slider and *see* a change.
			if dataRoot != "" {
				checkpoints := []int64{0, int64(iters) / 2, int64(iters) - 1}
				for _, step := range checkpoints {
					pngBytes := drawCheckpointPNG(step, int64(iters), size, opt)
					sha, err := insertDemoBlob(ctx, tx, dataRoot, pngBytes, "image/png", now)
					if err != nil {
						return nil, fmt.Errorf("insert demo blob: %w", err)
					}
					caption := fmt.Sprintf("step %d · size=%d %s", step, size, opt)
					if _, err := tx.ExecContext(ctx, `
						INSERT INTO run_images (
							id, run_id, metric_name, step, blob_sha,
							caption, created_at
						) VALUES (?, ?, 'samples/generations', ?, ?, ?, ?)`,
						NewID(), runID, step, sha, caption, now,
					); err != nil {
						return nil, fmt.Errorf("insert run_images: %w", err)
					}
					res.ImageCount++
				}
			}

			// Per-run text-sample document (plot type 7: text-sample panel).
			// Mirrors what a nanoGPT-Shakespeare worker would upload as
			// checkpoint generations. Stored with kind='sample' so the
			// mobile UI can filter these out of the main memo/report stream.
			sampleID := NewID()
			sampleMD := buildDemoSample(size, opt)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO documents (
					id, project_id, kind, title, version, content_inline, created_at
				) VALUES (?, ?, 'sample', ?, 1, ?, ?)`,
				sampleID, res.ProjectID,
				fmt.Sprintf("Shakespeare samples — size=%d %s", size, opt),
				sampleMD, now,
			); err != nil {
				return nil, fmt.Errorf("insert sample document: %w", err)
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
			status, created_at,
			actor_kind, actor_handle
		) VALUES (?, ?, 'project', ?, 'decision',
		          ?, 'minor', ?,
		          '[]', '[]',
		          'open', ?,
		          'agent', 'steward')`,
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

// synthRunCurves emits the metric families the mobile Run Detail screen
// renders, shaped so the ablation story is visible in the data and so each
// of the dominant wandb/tensorboard plot archetypes is represented at least
// once:
//
//   Type 1 (single scalar):
//     - learning_rate, grad_norm, throughput/tokens_per_sec
//
//   Type 2 (multi-series overlay, shared y-axis):
//     - loss/{train,val}                 — train vs val loss
//     - smooth/{train_raw,train_ema}     — raw curve vs EMA-smoothed
//     - sys/{gpu_util,gpu_mem,cpu_util}  — three system metrics (%)
//
//   Type 3 (percentile band / distribution over time):
//     - weights_dist/{p5,p25,p50,p75,p95} — weight-magnitude quantiles,
//       all five overlaid so the UI sees a band thickening over training.
//
//   Type 4 (sparse eval metrics — points every ~N steps, not every step):
//     - eval/{perplexity,bleu,accuracy} — three eval metrics logged at
//       10 checkpoints across training. Visually distinct from dense
//       curves (each tile shows ~10 vertices, not 100).
//
//   Type 5 (non-monotone / phase transition):
//     - grokking/success_rate — flat near zero for the first ~60% of
//       training, then a sharp sigmoid ramp to near 1.0. Mirrors the
//       canonical "grokking" shape.
//
//   Type 6 (per-layer overlay):
//     - grads/{layer0,layer1,layer2,layer3} — layer-wise gradient
//       norms, four series on one tile. Early-layer grads decay
//       faster than late-layer grads, so the lines visibly diverge.
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

	// smooth/train_{raw,ema}: raw training loss + an EMA-smoothed companion
	// so the UI can show the "jittery-raw + tidy-smoothed" overlay that is
	// ubiquitous on wandb dashboards. Uses trainPts as the source.
	smoothRawPts := make([][2]any, len(trainPts))
	smoothEMAPts := make([][2]any, len(trainPts))
	var ema float64
	const alpha = 0.15 // EMA weight on new sample
	var smoothRawLast, smoothEMALast float64
	for i, p := range trainPts {
		step := p[0].(int64)
		tv := p[1].(float64)
		// Inject extra per-step jitter on top of trainPts so the raw
		// curve looks visibly noisier than the already-smoothed loss/train.
		raw := tv + (rng.Float64()-0.5)*0.25
		if i == 0 {
			ema = raw
		} else {
			ema = alpha*raw + (1-alpha)*ema
		}
		smoothRawPts[i] = [2]any{step, roundTo(raw, 4)}
		smoothEMAPts[i] = [2]any{step, roundTo(ema, 4)}
		smoothRawLast = raw
		smoothEMALast = ema
	}

	// sys/{gpu_util,gpu_mem,cpu_util}: three system-utilization curves in
	// percent (0-100), noisy and roughly flat once training reaches steady
	// state. Lets the UI demo a three-series overlay with shared y-axis.
	gpuUtilPts := make([][2]any, len(trainPts))
	gpuMemPts := make([][2]any, len(trainPts))
	cpuUtilPts := make([][2]any, len(trainPts))
	var gpuUtilLast, gpuMemLast, cpuUtilLast float64
	// Steady-state targets depend weakly on model size.
	gpuUtilBase := 78.0
	gpuMemBase := 62.0
	cpuUtilBase := 22.0
	switch size {
	case 256:
		gpuUtilBase, gpuMemBase = 86.0, 74.0
	case 384:
		gpuUtilBase, gpuMemBase = 93.0, 88.0
	}
	for i, p := range trainPts {
		step := p[0].(int64)
		// Linear ramp for the first ~5% of training (warm-up), then steady.
		frac := float64(step) / float64(iters)
		ramp := math.Min(1.0, frac/0.05)
		gu := gpuUtilBase*ramp + (rng.Float64()-0.5)*6
		gm := gpuMemBase*ramp + (rng.Float64()-0.5)*4
		cu := cpuUtilBase + (rng.Float64()-0.5)*8
		if gu < 0 {
			gu = 0
		}
		if gu > 100 {
			gu = 100
		}
		if gm < 0 {
			gm = 0
		}
		if gm > 100 {
			gm = 100
		}
		if cu < 0 {
			cu = 0
		}
		gpuUtilPts[i] = [2]any{step, roundTo(gu, 2)}
		gpuMemPts[i] = [2]any{step, roundTo(gm, 2)}
		cpuUtilPts[i] = [2]any{step, roundTo(cu, 2)}
		gpuUtilLast, gpuMemLast, cpuUtilLast = gu, gm, cu
	}

	// weights_dist/p{5,25,50,75,95}: synthetic per-step quantiles of the
	// absolute weight distribution, drifting outward as the model trains
	// (a widening band is what you typically see on wandb histograms-over-
	// time). All five share a y-axis so the UI can overlay them and the
	// human eye reads the band thickness at each step.
	pTags := []string{"p5", "p25", "p50", "p75", "p95"}
	// Relative offsets from p50 (in units of "std"); scaled by a growing
	// spread factor so the band widens with step.
	pOffsets := []float64{-1.6, -0.7, 0.0, 0.7, 1.6}
	pPts := make([][][2]any, len(pTags))
	pLast := make([]float64, len(pTags))
	for k := range pTags {
		pPts[k] = make([][2]any, len(trainPts))
	}
	for i, p := range trainPts {
		step := p[0].(int64)
		frac := float64(step) / float64(iters)
		center := 0.02 + 0.06*frac    // mean |w| drifts up over training
		spread := 0.008 + 0.022*frac  // std of |w| grows ~3x
		for k, off := range pOffsets {
			v := center + off*spread + (rng.Float64()-0.5)*0.002
			if v < 0 {
				v = 0
			}
			pPts[k][i] = [2]any{step, roundTo(v, 5)}
			pLast[k] = v
		}
	}

	// eval/{perplexity,bleu,accuracy}: sparse checkpoints (10 points across
	// `iters`). Perplexity = exp(val_loss-ish); bleu drifts up from 0.05 to
	// a plateau near 0.35; accuracy ramps with a slight per-optimizer gap.
	// Renders as three overlaid curves with visibly few vertices — the
	// "sparse eval" archetype every real training run produces.
	const evalPoints = 10
	evalStride := int64(iters / evalPoints)
	if evalStride < 1 {
		evalStride = 1
	}
	evalPplPts := make([][2]any, 0, evalPoints)
	evalBleuPts := make([][2]any, 0, evalPoints)
	evalAccPts := make([][2]any, 0, evalPoints)
	var pplLast, bleuLast, accLast float64
	accCap := 0.82
	if optimizer == "lion" {
		accCap = 0.86
	}
	if size == 384 {
		accCap += 0.03
	}
	for i := 0; i < evalPoints; i++ {
		step := int64(i) * evalStride
		frac := float64(step) / float64(iters)
		// Perplexity decays from ~60 towards ~8–15 depending on size/opt.
		pplFloor := 14.0 - float64(size-128)/256.0*4.0
		if optimizer == "lion" {
			pplFloor -= 1.5
		}
		ppl := pplFloor + (60.0-pplFloor)*math.Exp(-3.0*frac) +
			(rng.Float64()-0.5)*1.2
		// BLEU ramps from 0.04 to ~0.3–0.38.
		bleu := 0.04 + (0.34+(accCap-0.82)*0.1)*(1-math.Exp(-2.5*frac)) +
			(rng.Float64()-0.5)*0.02
		// Accuracy ramps to accCap.
		acc := accCap*(1-math.Exp(-2.0*frac)) + (rng.Float64()-0.5)*0.015
		evalPplPts = append(evalPplPts, [2]any{step, roundTo(ppl, 3)})
		evalBleuPts = append(evalBleuPts, [2]any{step, roundTo(bleu, 4)})
		evalAccPts = append(evalAccPts, [2]any{step, roundTo(acc, 4)})
		pplLast, bleuLast, accLast = ppl, bleu, acc
	}

	// grokking/success_rate: canonical phase-transition shape. Flat at
	// ~0.05 for the first 60% of training, then a sharp logistic ramp to
	// ~0.95. Uses the same step grid as trainPts so it overlays cleanly.
	grokPts := make([][2]any, len(trainPts))
	var grokLast float64
	// Lion "groks" ~10% earlier than adamw; 384 model groks ~5% earlier
	// than 128 because the inductive-capacity arc favours bigger models.
	transitionFrac := 0.62
	if optimizer == "lion" {
		transitionFrac -= 0.08
	}
	if size == 384 {
		transitionFrac -= 0.04
	}
	for i, p := range trainPts {
		step := p[0].(int64)
		frac := float64(step) / float64(iters)
		// Logistic: k=25 gives a sharp jump spanning ~15% of training.
		s := 1.0 / (1.0 + math.Exp(-25.0*(frac-transitionFrac)))
		v := 0.04 + 0.93*s + (rng.Float64()-0.5)*0.02
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		grokPts[i] = [2]any{step, roundTo(v, 4)}
		grokLast = v
	}

	// grads/{layer0..3}: per-layer gradient norms. Layer 0 (input) has
	// the smallest grads and decays fastest; layer 3 (output) has larger
	// grads and decays more slowly. Uses a 1/sqrt decay similar to the
	// top-level grad_norm but scaled per layer.
	layerScales := []float64{0.6, 1.0, 1.4, 2.0} // layer0..layer3
	layerTaus := []float64{0.8, 1.0, 1.2, 1.5}   // layer0..layer3
	layerPts := make([][][2]any, len(layerScales))
	layerLasts := make([]float64, len(layerScales))
	for k := range layerScales {
		layerPts[k] = make([][2]any, len(trainPts))
	}
	for i, p := range trainPts {
		step := p[0].(int64)
		for k, scale := range layerScales {
			base := scale * math.Pow(float64(step+1), -0.5*layerTaus[k])
			if base < 0.02 {
				base = 0.02
			}
			v := base + (rng.Float64()-0.5)*0.2*base
			if v < 0.01 {
				v = 0.01
			}
			layerPts[k][i] = [2]any{step, roundTo(v, 4)}
			layerLasts[k] = v
		}
	}

	// Final step across every curve is the same as train_loss.
	curves := []demoCurve{
		{"loss/train", trainPts, trainStep, trainLast},
		{"loss/val", valPts, trainStep, roundTo(valLast, 4)},
		// learning_rate & grad_norm kept top-level (no `/`) because their
		// y-scales disagree wildly (6e-4 vs ~3.0), so overlaying would look
		// awful. They render as one tile each on the mobile UI.
		{"learning_rate", lrPts, trainStep, roundTo(lrLast, 6)},
		{"grad_norm", gnPts, trainStep, roundTo(gnLast, 4)},
		{"throughput/tokens_per_sec", tpsPts, trainStep, roundTo(tpsLast, 1)},
		{"smooth/train_raw", smoothRawPts, trainStep, roundTo(smoothRawLast, 4)},
		{"smooth/train_ema", smoothEMAPts, trainStep, roundTo(smoothEMALast, 4)},
		{"sys/gpu_util", gpuUtilPts, trainStep, roundTo(gpuUtilLast, 2)},
		{"sys/gpu_mem", gpuMemPts, trainStep, roundTo(gpuMemLast, 2)},
		{"sys/cpu_util", cpuUtilPts, trainStep, roundTo(cpuUtilLast, 2)},
	}
	for k, tag := range pTags {
		curves = append(curves, demoCurve{
			name:      "weights_dist/" + tag,
			points:    pPts[k],
			lastStep:  trainStep,
			lastValue: roundTo(pLast[k], 5),
		})
	}
	// Sparse eval group — lastStep taken from the last point written, not
	// trainStep, so the summary line stamps where eval actually stopped.
	evalLastStep := int64((evalPoints - 1)) * evalStride
	curves = append(curves,
		demoCurve{"eval/perplexity", evalPplPts, evalLastStep, roundTo(pplLast, 3)},
		demoCurve{"eval/bleu", evalBleuPts, evalLastStep, roundTo(bleuLast, 4)},
		demoCurve{"eval/accuracy", evalAccPts, evalLastStep, roundTo(accLast, 4)},
		demoCurve{"grokking/success_rate", grokPts, trainStep, roundTo(grokLast, 4)},
	)
	for k := range layerScales {
		curves = append(curves, demoCurve{
			name:      fmt.Sprintf("grads/layer%d", k),
			points:    layerPts[k],
			lastStep:  trainStep,
			lastValue: roundTo(layerLasts[k], 4),
		})
	}
	return curves
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

// insertDemoBlob writes bytes to the content-addressed blob store rooted
// at dataRoot (same layout the real POST /v1/blobs handler uses) and
// upserts the blobs table row. Safe to call multiple times with the
// same bytes — disk write is skipped when the file already exists and
// the INSERT is `OR IGNORE`.
func insertDemoBlob(ctx context.Context, tx *sql.Tx, dataRoot string, data []byte, mime string, now string) (string, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	path := filepath.Join(dataRoot, "blobs", sha[:2], sha[2:4], sha)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO blobs (sha256, scope_path, size, mime, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		sha, path, len(data), mime, now); err != nil {
		return "", err
	}
	return sha, nil
}

// drawCheckpointPNG renders a 64x64 PNG that visibly evolves with
// training progress — early steps look noisy, late steps show a clean
// diagonal wave. (size, optimizer) perturb the color ramp slightly so
// different runs produce different blobs (blob-dedup still kicks in on
// identical inputs, which is the right behaviour).
//
// Deterministic: same (step, iters, size, optimizer) always yields the
// same bytes, so re-seeding finds identical blobs and the INSERT OR
// IGNORE paths stay quiet.
func drawCheckpointPNG(step, iters int64, size int, optimizer string) []byte {
	const w, h = 64, 64
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	frac := float64(step) / float64(iters-1)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	// Noise seed depends on everything so runs diverge but a given cell
	// in (step, size, opt) space is reproducible.
	seed := step*1000 + int64(size)*7
	if optimizer == "lion" {
		seed += 3
	}
	rng := rand.New(rand.NewSource(seed))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Diagonal wave — representing the "structure" the model has
			// learnt. Strength grows with training fraction.
			structVal := 0.5 + 0.5*math.Sin(float64(x+y)/6.0)
			noise := rng.Float64()
			v := (1-frac)*noise + frac*structVal

			// Color ramp: early training leans green/amber, late training
			// leans blue/violet. Optimizer adds a subtle hue shift.
			rCh := uint8(frac * 200)
			gCh := uint8(v*200 + 40)
			bCh := uint8((1-frac)*80 + v*140)
			if optimizer == "lion" {
				bCh = uint8(math.Min(255, float64(bCh)+30))
			}
			img.Set(x, y, color.RGBA{rCh, gCh, bCh, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
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
		"**Plots:** Each run carries the dominant wandb/tensorboard plot\n" +
		"archetypes on the Run Detail screen:\n\n" +
		"- `loss/{train,val}` — overlaid multi-series (val gap widens)\n" +
		"- `smooth/{train_raw,train_ema}` — raw vs EMA-smoothed overlay\n" +
		"- `sys/{gpu_util,gpu_mem,cpu_util}` — three system metrics (%)\n" +
		"- `weights_dist/p{5,25,50,75,95}` — weight-magnitude percentile band\n" +
		"- `eval/{perplexity,bleu,accuracy}` — sparse eval (10 checkpoints)\n" +
		"- `grokking/success_rate` — phase-transition curve (flat → sharp ramp)\n" +
		"- `grads/{layer0..3}` — per-layer gradient norms\n" +
		"- `learning_rate`, `grad_norm` — single-series scalars\n" +
		"- `throughput/tokens_per_sec` — single-series scalar\n" +
		"- `samples/generations` (image series) — three PNG checkpoints\n" +
		"  per run, scrubbable via the step slider\n\n" +
		"Each run also carries a `kind='sample'` document with Shakespeare-\n" +
		"style generations captured at checkpoints — the text-sample panel\n" +
		"archetype.\n"
}

// buildDemoSample returns a markdown document imitating nanoGPT-Shakespeare
// generations at three training checkpoints. The text gets progressively
// more coherent at later steps so the reviewer can *see* learning happening
// in the sample panel itself, not just on the loss curve.
func buildDemoSample(size int, optimizer string) string {
	return fmt.Sprintf(
		"# Shakespeare samples — size=%d optimizer=%s\n\n"+
			"Generations captured at three checkpoints during training. Prompt: `ROMEO:`\n\n"+
			"---\n\n"+
			"## Step 100 (early — mostly noise)\n\n"+
			"```\n"+
			"ROMEO: thet hreo whonh. tesed tho ie soulnt the\n"+
			"         thy, whod hanes thee; med hal rt\n"+
			"         withe spoour, thin fath thit,\n"+
			"         thonk nold hau\n"+
			"```\n\n"+
			"## Step 500 (mid — shape of language, words not quite right)\n\n"+
			"```\n"+
			"ROMEO: Good morrow, noble lord. What fares thy heart?\n"+
			"         I pray you, speak, and let my sorrow end,\n"+
			"         For every hour that passes without word\n"+
			"         Doth weigh upon this breast as heavy stone.\n"+
			"```\n\n"+
			"## Step 990 (final — recognisably Shakespearean pastiche)\n\n"+
			"```\n"+
			"ROMEO: O, speak again, bright angel! for thou art\n"+
			"         As glorious to this night, being o'er my head,\n"+
			"         As is a winged messenger of heaven\n"+
			"         Unto the white-upturned wondering eyes\n"+
			"         Of mortals that fall back to gaze on him.\n"+
			"```\n\n"+
			"---\n\n"+
			"_Synthetic samples — no real training ran. Replace with true\n"+
			"worker output once a GPU host is attached._\n",
		size, optimizer,
	)
}

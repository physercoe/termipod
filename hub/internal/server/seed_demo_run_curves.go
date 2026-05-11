// Run-metric curve synthesisers used by the lifecycle seed to populate
// run_metrics for each completed run. Carried over from the legacy
// seed_demo.go when --shape ablation was retired in v1.0.507
// (plans/multi-run-experiment-phase.md, W4). The lifecycle seed is the
// only caller today; the curves are deterministic given (size,
// optimizer, iters) so re-seeding produces the same project state.
package server

import (
	"fmt"
	"math"
	"math/rand"
)

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

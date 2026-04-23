package main

import (
	"math"
	"math/rand"
)

// curveConfig shapes the synthetic loss curve. Floor depends on Size and
// Optimizer, decay rate on Optimizer. Kept tiny on purpose — this is a
// UI fixture, not a model simulator.
type curveConfig struct {
	Size      int
	Optimizer string
	Iters     int
}

// curveFor returns the (floor, tau, start) triple for a given config.
// The curve is loss(step) = floor + (start-floor) * exp(-step/tau) with
// per-step noise proportional to the residual, so early-training jitter
// dominates the sparkline shape (matches what a reviewer expects to see).
func curveFor(c curveConfig) (floor, tau, start float64) {
	floor = 2.2
	switch c.Size {
	case 256:
		floor -= 0.3
	case 384:
		floor -= 0.55
	}
	if c.Optimizer == "lion" {
		floor -= 0.15
	}
	tau = float64(c.Iters) / 4.0
	if c.Optimizer == "lion" {
		tau *= 0.85
	}
	start = 4.0
	return
}

// nextLoss computes loss at step given the curve parameters and a
// fresh RNG draw. Clamped at zero so a deep noise sample can't flip
// the sign.
func nextLoss(rng *rand.Rand, floor, tau, start float64, step int64) float64 {
	clean := floor + (start-floor)*math.Exp(-float64(step)/tau)
	noise := (rng.Float64() - 0.5) * 0.12 * (clean - floor + 0.05)
	v := clean + noise
	if v < 0 {
		v = 0
	}
	return v
}

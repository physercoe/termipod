package main

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/metrics"
	"github.com/termipod/hub/internal/hostrunner/trackio"
	"github.com/termipod/hub/internal/hostrunner/wandb"
)

// TestTrackioRoundTrip writes a trackio SQLite file with mock-trainer and
// reads it back through the real trackio.Reader. Catches schema drift:
// if the writer and reader disagree on table/column shape, this test
// fails.
func TestTrackioRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := curveConfig{Size: 384, Optimizer: "lion", Iters: 200}
	rng := rand.New(rand.NewSource(1))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx := context.Background()
	uri, err := writeTrackio(ctx, dir, "demo-proj", "run-1", cfg, rng, 0, log)
	if err != nil {
		t.Fatalf("writeTrackio: %v", err)
	}
	want := "trackio://demo-proj/run-1"
	if uri != want {
		t.Errorf("uri = %q, want %q", uri, want)
	}

	// Read through the real host-runner reader.
	series, err := trackio.ReadRun(ctx, trackio.ProjectDBPath(dir, "demo-proj"), "run-1")
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss, ok := series["loss"]
	if !ok {
		t.Fatalf("no loss series in output; got keys: %v", mapKeys(series))
	}
	if len(loss) != cfg.Iters {
		t.Errorf("len(loss) = %d, want %d", len(loss), cfg.Iters)
	}

	// Curve should descend: the last sample should be well below the first.
	// Noise can flip adjacent samples, so compare endpoints.
	if len(loss) >= 2 && loss[len(loss)-1].Value >= loss[0].Value {
		t.Errorf("curve did not descend: first=%v last=%v",
			loss[0].Value, loss[len(loss)-1].Value)
	}
}

// TestWandbRoundTrip mirrors TestTrackioRoundTrip for the wandb offline
// JSONL format — same rationale.
func TestWandbRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := curveConfig{Size: 128, Optimizer: "adamw", Iters: 150}
	rng := rand.New(rand.NewSource(2))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx := context.Background()
	uri, err := writeWandb(ctx, dir, "demo-proj", "run-xyz", cfg, rng, 0, log)
	if err != nil {
		t.Fatalf("writeWandb: %v", err)
	}
	want := "wandb://demo-proj/run-xyz"
	if uri != want {
		t.Errorf("uri = %q, want %q", uri, want)
	}

	series, err := wandb.ReadRun(ctx, wandb.RunHistoryPath(dir, "run-xyz"))
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	loss, ok := series["loss"]
	if !ok {
		t.Fatalf("no loss series in output; got keys: %v", mapKeys(series))
	}
	if len(loss) != cfg.Iters {
		t.Errorf("len(loss) = %d, want %d", len(loss), cfg.Iters)
	}
	if len(loss) >= 2 && loss[len(loss)-1].Value >= loss[0].Value {
		t.Errorf("curve did not descend: first=%v last=%v",
			loss[0].Value, loss[len(loss)-1].Value)
	}
}

// TestCurveShape covers the (size, optimizer) → floor ordering that the
// demo memo claims: bigger + lion beats everything.
func TestCurveShape(t *testing.T) {
	cases := []struct {
		name string
		cfg  curveConfig
	}{
		{"128-adamw", curveConfig{Size: 128, Optimizer: "adamw", Iters: 1000}},
		{"384-lion", curveConfig{Size: 384, Optimizer: "lion", Iters: 1000}},
	}
	floors := map[string]float64{}
	for _, c := range cases {
		floor, _, _ := curveFor(c.cfg)
		floors[c.name] = floor
	}
	if floors["384-lion"] >= floors["128-adamw"] {
		t.Errorf("expected 384-lion floor (%v) < 128-adamw floor (%v)",
			floors["384-lion"], floors["128-adamw"])
	}
}

func mapKeys(m map[string]metrics.Series) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

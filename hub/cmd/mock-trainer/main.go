// mock-trainer — write a realistic training-loss curve to a
// trackio or wandb-offline store without running a real model on a GPU.
//
// Purpose: dress-rehearsal tool for the research-demo pipeline (blueprint
// §9 P4). Pairs with `hub-server seed-demo` to exercise the full mobile
// path (trackio / wandb file → host-runner poll → hub digest → mobile
// sparkline) end-to-end on any laptop. No GPU, no real training, just
// the exact on-disk formats the host-runner's metrics.Readers consume.
//
// Two vendor backends are supported:
//
//	--vendor=trackio  → writes <dir>/<project>.db (trackio schema)
//	--vendor=wandb    → writes <dir>/<run>/files/wandb-history.jsonl
//
// On success prints the vendor URI (trackio://… or wandb://…) so it can
// be dropped into runs.trackio_run_uri on the hub.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"time"
)

func main() {
	var (
		vendor     = flag.String("vendor", "trackio", "metrics vendor: trackio|wandb")
		dir        = flag.String("dir", "", "output directory (trackio root, or wandb root). Required.")
		project    = flag.String("project", "mock-trainer-demo", "project name (trackio DB filename, or wandb top-level dir)")
		run        = flag.String("run", "mock-run-1", "run name (trackio run_name, or wandb run-dir)")
		size       = flag.Int("size", 256, "synthetic model size (shapes the curve)")
		optimizer  = flag.String("optimizer", "adamw", "synthetic optimizer (shapes the curve): adamw|lion")
		iters      = flag.Int("iters", 1000, "total training steps to simulate")
		intervalMs = flag.Int("interval-ms", 0, "sleep between step writes (simulates real training time; 0 = instant)")
		seed       = flag.Int64("seed", 42, "RNG seed")
	)
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "--dir is required")
		flag.Usage()
		os.Exit(2)
	}
	if *iters <= 0 {
		fmt.Fprintln(os.Stderr, "--iters must be > 0")
		os.Exit(2)
	}

	curve := curveConfig{
		Size:      *size,
		Optimizer: *optimizer,
		Iters:     *iters,
	}
	rng := rand.New(rand.NewSource(*seed))
	interval := time.Duration(*intervalMs) * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		uri string
		err error
	)
	switch *vendor {
	case "trackio":
		uri, err = writeTrackio(ctx, *dir, *project, *run, curve, rng, interval, log)
	case "wandb":
		uri, err = writeWandb(ctx, *dir, *project, *run, curve, rng, interval, log)
	default:
		fmt.Fprintf(os.Stderr, "unknown --vendor %q (want trackio|wandb)\n", *vendor)
		os.Exit(2)
	}
	if err != nil {
		log.Error("mock-trainer failed", "err", err, "vendor", *vendor)
		os.Exit(1)
	}
	fmt.Printf("mock-trainer: wrote %d steps to vendor=%s.\n  uri: %s\n", *iters, *vendor, uri)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. On the host, point host-runner at --%s-dir=%s\n", *vendor, *dir)
	fmt.Printf("  2. POST a run to the hub with trackio_run_uri=%s\n", uri)
	fmt.Printf("  3. Watch the mobile sparkline populate as host-runner digests the curve.\n")
}

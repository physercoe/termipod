package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// writeWandb writes an offline wandb-history.jsonl file under
// <root>/<run>/files/wandb-history.jsonl. One JSON object per line with
// {"_step": N, "_timestamp": …, "loss": …}. Matches the format
// host-runner's wandb reader expects (see hub/internal/hostrunner/wandb/reader.go).
// Returns the canonical wandb:// URI for the new run.
//
// Note: `project` is the wandb-project slug that goes into the URI. It
// is not realized as a directory — the wandb reader's URI layout is
// <root>/<run-dir>/files/wandb-history.jsonl, with <run-dir> being the
// run identifier (what users expect to paste). Project is a logical
// grouping that lives in the URI only.
func writeWandb(ctx context.Context, root, project, run string, cfg curveConfig, rng *rand.Rand, interval time.Duration, log *slog.Logger) (string, error) {
	runDir := filepath.Join(root, run, "files")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", runDir, err)
	}
	path := filepath.Join(runDir, "wandb-history.jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	floor, tau, start := curveFor(cfg)
	enc := json.NewEncoder(f)
	for step := int64(0); step < int64(cfg.Iters); step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		v := nextLoss(rng, floor, tau, start, step)
		obj := map[string]any{
			"_step":      step,
			"_timestamp": time.Now().Unix(),
			"loss":       v,
		}
		if err := enc.Encode(obj); err != nil {
			return "", fmt.Errorf("encode step %d: %w", step, err)
		}
		if interval > 0 {
			if step%100 == 0 {
				log.Info("mock-trainer step", "step", step, "loss", v)
			}
			time.Sleep(interval)
		}
	}
	return fmt.Sprintf("wandb://%s/%s", project, run), nil
}

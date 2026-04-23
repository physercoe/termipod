package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// writeTrackio writes `iters` rows into a trackio-schema SQLite file at
// <root>/<project>.db. The schema matches the one host-runner's trackio
// reader expects (see hub/internal/hostrunner/trackio/reader.go).
// Returns the canonical trackio_run_uri for the new run.
func writeTrackio(ctx context.Context, root, project, run string, cfg curveConfig, rng *rand.Rand, interval time.Duration, log *slog.Logger) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", root, err)
	}
	dbPath := filepath.Join(root, project+".db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	// Matches docs.huggingface.co/trackio/storage_schema and the columns
	// host-runner queries. IF NOT EXISTS lets the same file accumulate
	// runs across mock-trainer invocations.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT,
			run_name TEXT,
			step INTEGER,
			metrics TEXT
		)`); err != nil {
		return "", fmt.Errorf("create metrics: %w", err)
	}

	stmt, err := db.PrepareContext(ctx,
		`INSERT INTO metrics (timestamp, run_name, step, metrics) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return "", fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	floor, tau, start := curveFor(cfg)
	for step := int64(0); step < int64(cfg.Iters); step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		v := nextLoss(rng, floor, tau, start, step)
		payload, _ := json.Marshal(map[string]any{"loss": v})
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := stmt.ExecContext(ctx, ts, run, step, string(payload)); err != nil {
			return "", fmt.Errorf("insert step %d: %w", step, err)
		}
		if interval > 0 {
			if step%100 == 0 {
				log.Info("mock-trainer step", "step", step, "loss", v)
			}
			time.Sleep(interval)
		}
	}
	return fmt.Sprintf("trackio://%s/%s", project, run), nil
}

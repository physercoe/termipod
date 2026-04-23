package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/wandb"
)

// seedWandbHistory writes a minimal wandb offline-run history file under
// {root}/{runDir}/files/wandb-history.jsonl with one row per (step, value)
// logged as metric "loss". Helper for host-runner level tests — mirrors
// mustSeedTrackio in trackio_poll_seed_test.go.
func seedWandbHistory(t *testing.T, root, runDir string, points [][2]any) {
	t.Helper()
	filesDir := filepath.Join(root, runDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(filesDir, "wandb-history.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, p := range points {
		if err := enc.Encode(map[string]any{"_step": p[0], "loss": p[1]}); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
}

func TestWandbTick_PushesDigestForMatchingRun(t *testing.T) {
	dir := t.TempDir()
	runDir := "run-20260423_123456-abc123"
	seedWandbHistory(t, dir, runDir, [][2]any{
		{int64(0), 2.5},
		{int64(50), 1.8},
		{int64(100), 1.23},
	})

	fake := &fakeHub{
		runs: []Run{{
			ID:            "run-42",
			TrackioHostID: "host-x",
			TrackioRunURI: "wandb://nano/" + runDir,
			Status:        "running",
		}},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:         NewClient(srv.URL, "t", "default"),
		HostID:         "host-x",
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), wandb.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.lastFilter != "host-x" {
		t.Errorf("server saw trackio_host=%q, want host-x", fake.lastFilter)
	}
	got := fake.puts["run-42"]
	if len(got) != 1 || got[0].Name != "loss" {
		t.Fatalf("puts[run-42] = %+v, want one loss series", got)
	}
	if got[0].SampleCount != 3 {
		t.Errorf("sample_count = %d, want 3", got[0].SampleCount)
	}
	if got[0].LastStep == nil || *got[0].LastStep != 100 {
		t.Errorf("last_step = %v, want 100", got[0].LastStep)
	}
	if got[0].LastValue == nil || *got[0].LastValue != 1.23 {
		t.Errorf("last_value = %v, want 1.23", got[0].LastValue)
	}
	if len(got[0].Points) != 3 {
		t.Errorf("points len = %d, want 3 (under max)", len(got[0].Points))
	}
}

func TestWandbTick_SkipsRunsWithoutWandbScheme(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeHub{
		runs: []Run{
			{ID: "run-trackio", TrackioHostID: "host-x", TrackioRunURI: "trackio://p/r", Status: "running"},
			{ID: "run-empty", TrackioHostID: "host-x", TrackioRunURI: "", Status: "running"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:         NewClient(srv.URL, "t", "default"),
		HostID:         "host-x",
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), wandb.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 {
		t.Errorf("put %d runs, want 0 (non-wandb schemes must be skipped)", len(fake.puts))
	}
}

func TestWandbTick_SkipsRunsWithEmptySeries(t *testing.T) {
	dir := t.TempDir()
	// No history file seeded — ReadRun returns empty, tick should not PUT.
	fake := &fakeHub{
		runs: []Run{
			{ID: "run-1", TrackioHostID: "host-x", TrackioRunURI: "wandb://nano/run-missing"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:         NewClient(srv.URL, "t", "default"),
		HostID:         "host-x",
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), wandb.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 {
		t.Errorf("put %d runs, want 0 (empty series should not PUT)", len(fake.puts))
	}
}

func TestWandbTick_DownsamplesToMaxPoints(t *testing.T) {
	dir := t.TempDir()
	runDir := "run-big"
	// 500 steps — should collapse to 100 with endpoints preserved.
	points := make([][2]any, 500)
	for i := range points {
		points[i] = [2]any{int64(i), float64(i) * 0.01}
	}
	seedWandbHistory(t, dir, runDir, points)

	fake := &fakeHub{
		runs: []Run{{
			ID:            "run-big",
			TrackioHostID: "host-x",
			TrackioRunURI: "wandb://nano/" + runDir,
			Status:        "running",
		}},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client:         NewClient(srv.URL, "t", "default"),
		HostID:         "host-x",
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), wandb.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	got := fake.puts["run-big"]
	if len(got) != 1 {
		t.Fatalf("puts = %+v, want one series", got)
	}
	if len(got[0].Points) > 100 {
		t.Errorf("points len = %d, want <=100 after downsample", len(got[0].Points))
	}
	if got[0].SampleCount != 500 {
		t.Errorf("sample_count = %d, want 500 (pre-downsample count)", got[0].SampleCount)
	}
}

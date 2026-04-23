package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/termipod/hub/internal/hostrunner/trackio"
)

type fakeHub struct {
	mu         sync.Mutex
	runs       []Run
	puts       map[string][]MetricPoints // runID -> uploaded digest
	lastFilter string                    // whatever ?trackio_host= was requested
}

func (f *fakeHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/teams/", func(w http.ResponseWriter, r *http.Request) {
		// /v1/teams/{team}/runs?trackio_host=...
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs") {
			f.mu.Lock()
			f.lastFilter = r.URL.Query().Get("trackio_host")
			out := f.runs
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		// /v1/teams/{team}/runs/{runID}/metrics
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/runs/") &&
			strings.HasSuffix(r.URL.Path, "/metrics") {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			// ["v1","teams","{team}","runs","{runID}","metrics"]
			if len(parts) < 6 {
				http.Error(w, "bad path", http.StatusBadRequest)
				return
			}
			runID := parts[4]
			body, _ := io.ReadAll(r.Body)
			var in struct {
				Metrics []MetricPoints `json:"metrics"`
			}
			_ = json.Unmarshal(body, &in)
			f.mu.Lock()
			if f.puts == nil {
				f.puts = map[string][]MetricPoints{}
			}
			f.puts[runID] = in.Metrics
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"count": 1}`))
			return
		}
		http.Error(w, "unhandled: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	})
	return mux
}

func TestTrackioTick_PushesDigestForMatchingRun(t *testing.T) {
	// Seed a synthetic trackio DB on disk.
	dir := t.TempDir()
	path := trackio.ProjectDBPath(dir, "nano")
	_ = path // keep name consistent with reader_test helper
	// Reuse the test helper from the trackio package by building a DB manually.
	// The reader's test covers schema creation; here we use its public API to
	// populate one run's worth of steps.
	mustSeedTrackio(t, dir, "nano", "run-a", [][2]any{
		{int64(0), 2.5},
		{int64(50), 1.8},
		{int64(100), 1.23},
	})

	// Fake hub that advertises one matching run.
	fake := &fakeHub{
		runs: []Run{{
			ID:            "run-42",
			TrackioHostID: "host-x",
			TrackioRunURI: "trackio://nano/run-a",
			Status:        "running",
		}},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client: NewClient(srv.URL, "t", "default"),
		HostID: "host-x",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), trackio.New(dir), 100)

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

func TestTrackioTick_SkipsRunsWithoutURI(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeHub{
		runs: []Run{
			{ID: "run-no-uri", TrackioHostID: "host-x", TrackioRunURI: "", Status: "running"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client: NewClient(srv.URL, "t", "default"),
		HostID: "host-x",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), trackio.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 {
		t.Errorf("put %d runs, want 0", len(fake.puts))
	}
}

func TestTrackioTick_SkipsRunsWithEmptySeries(t *testing.T) {
	dir := t.TempDir()
	// No DB file created — ReadRun returns empty, tick should not PUT.
	fake := &fakeHub{
		runs: []Run{
			{ID: "run-1", TrackioHostID: "host-x", TrackioRunURI: "trackio://nano/run-a"},
		},
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	r := &Runner{
		Client: NewClient(srv.URL, "t", "default"),
		HostID: "host-x",
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r.metricsTick(context.Background(), trackio.New(dir), 100)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 {
		t.Errorf("put %d runs, want 0 (empty series should not PUT)", len(fake.puts))
	}
}

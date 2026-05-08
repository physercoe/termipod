package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/termipod/hub/internal/agentfamilies"
)

// TestLaunchOne_ExecResumeDriver_PatchesStatusRunning pins the
// regression where a paneless driver (gemini-cli ADR-013) left the
// agent stuck in `pending` because tickReconcile gates its tmux-derived
// transitions on a non-empty PaneID. launchM2 returns PaneID="" for
// exec-per-turn families, so launchOne must patch status="running"
// directly when a driver is wired but no pane was created.
func TestLaunchOne_ExecResumeDriver_PatchesStatusRunning(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Stage a fake gemini binary so exec.LookPath in launchM2 succeeds.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "gemini")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake gemini: %v", err)
	}

	type recordedPatch struct {
		AgentID string
		Body    AgentPatch
	}
	var (
		mu      sync.Mutex
		patches []recordedPatch
	)

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// PATCH /v1/teams/{team}/agents/{id}
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/agents/") {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
			// ["v1","teams","{team}","agents","{id}"]
			if len(parts) < 5 {
				http.Error(w, "bad path", http.StatusBadRequest)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var p AgentPatch
			_ = json.Unmarshal(body, &p)
			mu.Lock()
			patches = append(patches, recordedPatch{AgentID: parts[4], Body: p})
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Lifecycle events from the driver Start() call.
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/events") {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "unhandled: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(hub.Close)

	r := &Runner{
		Client:    NewClient(hub.URL, "tok", "default"),
		HostID:    "host-x",
		Launcher:  StubLauncher{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers:   map[string]Driver{},
		tailers:   map[string]*Tailer{},
		worktrees: map[string]WorktreeSpec{},
		panes:     map[string]paneState{},
		templates: &agentTemplates{},
	}
	r.agentPoster = r.Client
	r.inputs = NewInputRouter(r.Client, r.Log)

	// Sanity: the gemini-cli family must be registered, otherwise the
	// launchM2 branch will reject and we'd be testing the wrong path.
	if _, ok := agentfamilies.ByName("gemini-cli"); !ok {
		t.Fatal("gemini-cli family not in registry; family loader regressed")
	}

	sp := Spawn{
		ChildID: "agent-gemini-1",
		Handle:  "gemini-steward",
		Kind:    "gemini-cli",
		Mode:    "M2",
		SpawnSpec: "backend:\n" +
			"  cmd: " + fakeBin + "\n" +
			"  default_workdir: " + homeDir + "\n",
	}

	r.launchOne(context.Background(), sp)

	// Tear down the driver we just created so the test doesn't leak
	// the goroutines exec-resume opens.
	if d, ok := r.drivers[sp.ChildID]; ok {
		d.Stop()
	}

	mu.Lock()
	defer mu.Unlock()
	var sawRunning bool
	for _, p := range patches {
		if p.AgentID != sp.ChildID {
			continue
		}
		if p.Body.Status != nil && *p.Body.Status == "running" {
			sawRunning = true
		}
		if p.Body.PaneID != nil && *p.Body.PaneID != "" {
			t.Errorf("unexpected pane_id patch for paneless driver: %q", *p.Body.PaneID)
		}
	}
	if !sawRunning {
		t.Fatalf("expected PATCH status=running for paneless driver; got patches=%+v", patches)
	}
}

// stubDriver is a Driver that records Start/Stop calls. Used by the
// paneless terminate test to verify the registry-based teardown path.
type stubDriver struct {
	mu      sync.Mutex
	started bool
	stopped bool
}

func (s *stubDriver) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	return nil
}

func (s *stubDriver) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

func (s *stubDriver) wasStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// TestTerminatePane_PanelessDriverStopsViaRegistry pins the bug where
// terminating a paneless agent (M2 ExecResumeDriver, M1 ACPDriver post
// tail-pane failure) returned `terminate: pane_id required` and left
// the live process running. The fix routes paneless terminate through
// stopDriver so the driver-owned process is killed via Driver.Stop().
func TestTerminatePane_PanelessDriverStopsViaRegistry(t *testing.T) {
	r := &Runner{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers:   map[string]Driver{},
		tailers:   map[string]*Tailer{},
		worktrees: map[string]WorktreeSpec{},
		panes:     map[string]paneState{},
	}
	r.inputs = NewInputRouter(nil, r.Log)
	stub := &stubDriver{}
	r.drivers["agent-paneless"] = stub

	cmd := HostCommand{
		ID:      "cmd-1",
		AgentID: "agent-paneless",
		Kind:    "terminate",
		Args:    json.RawMessage(`{}`),
	}
	if err := r.terminatePane(context.Background(), cmd); err != nil {
		t.Fatalf("terminatePane: %v", err)
	}
	if !stub.wasStopped() {
		t.Error("driver Stop was not called")
	}
	if _, ok := r.drivers["agent-paneless"]; ok {
		t.Error("driver still in registry after terminate")
	}
}

// TestTerminatePane_PanelessNoDriverIsNoop locks the "agent already
// stopped or living on another host" path: with pane_id absent and no
// driver registered, terminate returns nil so the hub-side terminate
// command flips to done. Returning an error here would loop the
// command back to pending and spin forever.
func TestTerminatePane_PanelessNoDriverIsNoop(t *testing.T) {
	r := &Runner{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers:   map[string]Driver{},
		tailers:   map[string]*Tailer{},
		worktrees: map[string]WorktreeSpec{},
		panes:     map[string]paneState{},
	}
	r.inputs = NewInputRouter(nil, r.Log)
	cmd := HostCommand{
		ID:      "cmd-2",
		AgentID: "agent-already-gone",
		Kind:    "terminate",
		Args:    json.RawMessage(`{}`),
	}
	if err := r.terminatePane(context.Background(), cmd); err != nil {
		t.Errorf("terminatePane: %v; want nil for already-stopped paneless agent", err)
	}
}

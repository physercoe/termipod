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

// TestParseSpec_FallbackModes pins the runtime fallback ladder: spec
// YAML carries fallback_modes, host-runner parses it so a launch failure
// at a higher mode (M1 ACP handshake stall, M2 stdio start crash) lands
// on the next-best mode rather than straight on M4. Without this field
// the dispatch loop has no way to honor the template's preferred order.
func TestParseSpec_FallbackModes(t *testing.T) {
	yaml := "driving_mode: M1\n" +
		"fallback_modes: [M2, M4]\n" +
		"backend:\n  cmd: gemini --acp\n"
	spec, err := ParseSpec(yaml)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if len(spec.FallbackModes) != 2 || spec.FallbackModes[0] != "M2" || spec.FallbackModes[1] != "M4" {
		t.Errorf("FallbackModes = %v; want [M2 M4]", spec.FallbackModes)
	}
}

// TestTerminatePane_PanedDriverAlsoStopped pins v1.0.450's fix: M1/M2
// land here with BOTH a tmux pane (cosmetic tail-F display) AND a
// registered driver (the live engine subprocess). The pre-fix path
// only ran kill-pane and never called the driver's Closer, so the
// "[host-runner] M1 stopped at ..." farewell line was never written
// and lifecycle.stopped never posted. The fix routes through
// stopDriver before the kill-pane (paneless and paned share the same
// teardown sequence now).
//
// We deliberately don't try to assert the kill-pane shell call (would
// require a tmux fixture). The driver-stop assertion is the key
// invariant; missing the kill-pane in test scaffolding is harmless
// because runTmux returns an error which terminatePane swallows when
// the pane is already gone.
func TestTerminatePane_PanedDriverAlsoStopped(t *testing.T) {
	r := &Runner{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers:   map[string]Driver{},
		tailers:   map[string]*Tailer{},
		worktrees: map[string]WorktreeSpec{},
		panes:     map[string]paneState{},
	}
	r.inputs = NewInputRouter(nil, r.Log)
	stub := &stubDriver{}
	r.drivers["agent-paned"] = stub

	cmd := HostCommand{
		ID:      "cmd-paned",
		AgentID: "agent-paned",
		Kind:    "terminate",
		// Non-existent pane id so kill-pane fails harmlessly with
		// "can't find pane" — terminatePane swallows that case.
		Args: json.RawMessage(`{"pane_id":"%nonexistent"}`),
	}
	_ = r.terminatePane(context.Background(), cmd) // ignore tmux err

	if !stub.wasStopped() {
		t.Error("driver Stop was not called when terminate had both pane_id and agent_id — Closer's farewell line never reaches the cosmetic log")
	}
	if _, ok := r.drivers["agent-paned"]; ok {
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

// TestLaunchOne_RefusesEmptyBackendCmd pins the W7 refusal. Pre-bundle
// the M4 PaneDriver fallback at runner.go's `cmd == ""` branch invoked
// `a.Launcher.Launch(ctx, sp)` — the launcher default placeholder
// (interactive bash) — which then accepted keystroke-pumped task
// prompt input. Post-W7 this branch refuses to launch, marks the
// agent failed via PATCH, and creates no tmux pane. See
// docs/discussions/validate-at-every-boundary.md §1.
func TestLaunchOne_RefusesEmptyBackendCmd(t *testing.T) {
	type recordedPatch struct {
		AgentID string
		Body    AgentPatch
	}
	var (
		mu      sync.Mutex
		patches []recordedPatch
	)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/agents/") {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
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
		http.Error(w, "unhandled", http.StatusNotFound)
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

	sp := Spawn{
		ChildID: "agent-no-cmd",
		Handle:  "no-cmd-worker",
		Kind:    "claude-code",
		Mode:    "M4",
		// SpawnSpec has no backend.cmd at all; templates index also
		// returns "" for unknown kind. This is the W7 trigger condition.
		SpawnSpec: "driving_mode: M4\n",
	}

	r.launchOne(context.Background(), sp)

	mu.Lock()
	defer mu.Unlock()
	var sawFailed bool
	for _, p := range patches {
		if p.AgentID == sp.ChildID && p.Body.Status != nil && *p.Body.Status == "failed" {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Errorf("W7 refusal failed: expected PATCH status=failed; got patches=%+v", patches)
	}
	if _, ok := r.drivers[sp.ChildID]; ok {
		t.Error("W7 refusal failed: driver should not be registered after refuse-to-launch")
	}
}

// TestLaunchOne_SkipsWhenDriverAlreadyRegistered pins the W3 dedup
// guard. The respawn-loop bug in the coder.v1 incident (v1.0.619)
// reproduced as follows: a malformed spawn fell through to the
// launcher placeholder (interactive bash), the engine never produced
// output, the reconciler couldn't flip status pending → running, and
// the next tickPoll re-saw the spawn in pending and re-fired
// launchOne — creating a fresh tmux pane every interval. The guard
// returns immediately when a.drivers[ChildID] already exists; this
// test asserts no PATCH or driver registration happens on the second
// call.
func TestLaunchOne_SkipsWhenDriverAlreadyRegistered(t *testing.T) {
	var (
		mu          sync.Mutex
		patchCount  int
		eventCount  int
	)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/agents/") {
			mu.Lock()
			patchCount++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/events") {
			mu.Lock()
			eventCount++
			mu.Unlock()
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "unhandled", http.StatusNotFound)
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

	// Pre-register a driver as if a prior launchOne already ran.
	stub := &stubDriver{}
	r.drivers["agent-dup"] = stub

	sp := Spawn{
		ChildID: "agent-dup",
		Handle:  "dup-worker",
		Kind:    "claude-code",
		Mode:    "M4",
		// SpawnSpec doesn't matter; the guard fires before we look at it.
		SpawnSpec: "backend:\n  cmd: echo test\n",
	}

	r.launchOne(context.Background(), sp)

	mu.Lock()
	defer mu.Unlock()
	if patchCount != 0 {
		t.Errorf("dedup guard failed: got %d PATCH calls, want 0", patchCount)
	}
	if eventCount != 0 {
		t.Errorf("dedup guard failed: got %d event posts, want 0", eventCount)
	}
	if stub.wasStopped() {
		t.Error("pre-existing driver should not have been touched by skipped launchOne")
	}
}

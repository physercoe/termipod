package hostrunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchM2_GeminiFamily_WiresExecResumeDriver pins the slice-3
// dispatch: family=gemini-cli routes to the ExecResumeDriver, no
// long-running process is spawned (the spawner is untouched), and
// the resolved bin / workdir / Yolo are threaded through correctly.
//
// We do *not* exercise an actual turn here — the driver's own tests
// (TestExecResumeDriver_*) cover that with a fake CommandBuilder.
// This test focuses on the launchM2 wiring: type, fields, and the
// "we did not spawn a placeholder anchor" property that ADR-013 D7
// commits to.
func TestLaunchM2_GeminiFamily_WiresExecResumeDriver(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Stage a fake `gemini` binary that exec.LookPath will resolve
	// (LookPath returns absolute paths verbatim when the file exists
	// and is executable). We never actually invoke it — the
	// production CommandBuilder isn't called in this test because we
	// don't drive an Input call.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "gemini")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake gemini: %v", err)
	}

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:gemini-steward.0"}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID: "agent-gemini-1",
		Handle:  "gemini-steward",
		Kind:    "gemini-cli",
		SpawnSpec: "backend:\n" +
			"  cmd: " + fakeBin + "\n" +
			"  default_workdir: ~/hub-work\n",
		Mode: "M2",
	}

	res, err := launchM2(context.Background(), M2LaunchConfig{
		Spawn:    sp,
		Launcher: launcher,
		Client:   poster,
		Spawner:  spawner,
		LogDir:   logDir,
	})
	if err != nil {
		t.Fatalf("launchM2: %v", err)
	}
	defer res.Driver.Stop()

	// Driver dispatch: gemini must produce *ExecResumeDriver.
	drv, ok := res.Driver.(*ExecResumeDriver)
	if !ok {
		t.Fatalf("res.Driver: want *ExecResumeDriver, got %T", res.Driver)
	}

	if drv.Bin != fakeBin {
		t.Errorf("Bin = %q; want %q", drv.Bin, fakeBin)
	}
	wantWD := filepath.Join(homeDir, "hub-work")
	if drv.Workdir != wantWD {
		t.Errorf("Workdir = %q; want %q", drv.Workdir, wantWD)
	}
	if !drv.Yolo {
		t.Error("Yolo = false; want true (ADR-013 D4 — gemini stewards default to --yolo)")
	}
	if drv.FrameProfile == nil {
		t.Error("FrameProfile is nil — gemini-cli profile didn't load through dispatch")
	}
	if drv.CommandBuilder == nil {
		t.Error("CommandBuilder is nil — production wiring didn't set ExecCommandBuilder")
	}

	// Trust env: gemini-cli@0.41+ rejects headless turns from untrusted
	// folders (overrides --yolo back to "default" and exits before any
	// stream-json output). Hub-work is the agent's operating dir, so
	// GEMINI_CLI_TRUST_WORKSPACE=true must be in the spawn env or the
	// driver will run gemini and get nothing back.
	var sawTrust bool
	for _, kv := range drv.Env {
		if kv == "GEMINI_CLI_TRUST_WORKSPACE=true" {
			sawTrust = true
			break
		}
	}
	if !sawTrust {
		t.Error("GEMINI_CLI_TRUST_WORKSPACE=true missing from drv.Env; gemini-cli@0.41 will refuse headless turns from untrusted folders")
	}

	// Pane / log: ADR-013 D7 — exec-per-turn doesn't anchor a pane
	// since there's no long-running stdout to tail. Spawner must NOT
	// have been called.
	if spawner.cmd != "" {
		t.Errorf("spawner.cmd = %q; want empty (gemini doesn't spawn a long-running anchor)", spawner.cmd)
	}
	if res.PaneID != "" {
		t.Errorf("PaneID = %q; want empty for gemini exec-per-turn", res.PaneID)
	}
	if res.LogPath != "" {
		t.Errorf("LogPath = %q; want empty for gemini exec-per-turn", res.LogPath)
	}
}

// TestLaunchM2_GeminiFamily_RejectsMissingBin pins the safety check:
// when neither spec.Backend.Cmd nor PATH resolves a gemini binary, the
// launcher must fail rather than silently constructing a driver that
// would explode at first Input.
func TestLaunchM2_GeminiFamily_RejectsMissingBin(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Strip PATH so `gemini` cannot be resolved.
	t.Setenv("PATH", "")

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: ""}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID:   "agent-gemini-2",
		Kind:      "gemini-cli",
		SpawnSpec: "backend:\n  cmd: gemini\n  default_workdir: ~/x\n",
		Mode:      "M2",
	}

	_, err := launchM2(context.Background(), M2LaunchConfig{
		Spawn:    sp,
		Launcher: launcher,
		Client:   poster,
		Spawner:  spawner,
		LogDir:   logDir,
	})
	if err == nil {
		t.Fatal("launchM2 should fail when gemini bin is unresolvable")
	}
	if !strings.Contains(err.Error(), "gemini bin") {
		t.Errorf("error missing 'gemini bin' context: %v", err)
	}
}

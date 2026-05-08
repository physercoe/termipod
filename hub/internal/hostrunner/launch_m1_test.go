package hostrunner

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestLaunchM1_WiresACPDriverAndPane verifies the full M1 path:
// host-runner spawns the engine via the ProcSpawner, the fakeACPAgent
// (driving the other end of the stdio pipes) responds to initialize
// + session/new, and launchM1 returns an ACPDriver pointing at a real
// pane. This is the wiring contract that makes `gemini --acp` →
// long-running daemon → tail-anchored pane work end-to-end.
func TestLaunchM1_WiresACPDriverAndPane(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:gemini-acp.0"}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID: "agent-acp-1",
		Handle:  "gemini-acp",
		Kind:    "gemini-cli",
		Mode:    "M1",
		SpawnSpec: "backend:\n" +
			"  cmd: gemini --acp\n" +
			"  default_workdir: " + homeDir + "\n",
	}

	// launchM1 calls drv.Start which performs the handshake before
	// returning. Run launchM1 in a goroutine so we can drive the
	// fake agent on the other end concurrently.
	type result struct {
		res M1LaunchResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, e := launchM1(context.Background(), M1LaunchConfig{
			Spawn:    sp,
			Launcher: launcher,
			Client:   poster,
			Spawner:  spawner,
			LogDir:   logDir,
		})
		done <- result{r, e}
	}()

	// Wait for the spawner to be exercised.
	deadline := time.After(2 * time.Second)
	for spawner.child == nil {
		select {
		case <-deadline:
			t.Fatal("spawner never invoked")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Drive the fakeACPAgent on the child end of the pipes. It reads
	// what the driver wrote (spawner.input) and writes back into the
	// driver (spawner.child).
	agent := newFakeACPAgent(t, spawner.input, spawner.child, "sess-acp-launchm1")
	go agent.serve()

	// Wait for handshake to complete + launchM1 to return.
	select {
	case <-time.After(3 * time.Second):
		t.Fatal("launchM1 did not return")
	case r := <-done:
		if r.err != nil {
			t.Fatalf("launchM1: %v", r.err)
		}
		if _, ok := r.res.Driver.(*ACPDriver); !ok {
			t.Fatalf("Driver: want *ACPDriver, got %T", r.res.Driver)
		}
		if r.res.PaneID != "hub-agents:gemini-acp.0" {
			t.Errorf("PaneID = %q; want hub-agents:gemini-acp.0", r.res.PaneID)
		}
		if r.res.LogPath == "" {
			t.Error("LogPath is empty; M1 should write a log file the tail pane mirrors")
		}
		// Tear down the driver so the goroutines unwind before the test
		// ends — otherwise the readLoop sits on the closed pipe.
		r.res.Driver.Stop()
	}

	// The launcher must have been pointed at the log file via tail -F.
	if !strings.Contains(launcher.gotCmd, "tail -F") ||
		!strings.Contains(launcher.gotCmd, "termipod-agent-agent-acp-1.log") {
		t.Errorf("launcher saw cmd=%q; want a tail -F on the agent's log path", launcher.gotCmd)
	}

	// Spawner must have been invoked with the workdir-prefixed command.
	if !strings.Contains(spawner.cmd, "gemini --acp") {
		t.Errorf("spawner.cmd = %q; want it to contain `gemini --acp`", spawner.cmd)
	}
	if !strings.Contains(spawner.cmd, "cd ") {
		t.Errorf("spawner.cmd = %q; want a leading `cd <workdir>`", spawner.cmd)
	}
	// gemini-cli@0.41+ rejects headless mode (--acp included) from an
	// untrusted folder: the binary exits before producing any
	// JSON-RPC output, ACP initialize times out, and we fall back to
	// M2/M4. Inline GEMINI_CLI_TRUST_WORKSPACE=true into the bash -c
	// command so the trust gate clears.
	if !strings.Contains(spawner.cmd, "GEMINI_CLI_TRUST_WORKSPACE=true") {
		t.Errorf("spawner.cmd = %q; want a leading GEMINI_CLI_TRUST_WORKSPACE=true env so gemini-cli@0.41 doesn't refuse the launch", spawner.cmd)
	}

	// Lifecycle event must report mode=M1 (driver_acp.go emits this on
	// successful handshake — proves the ACP path ran end-to-end).
	var sawStarted bool
	for _, ev := range poster.snapshot() {
		if ev.Kind != "lifecycle" {
			continue
		}
		if phase, _ := ev.Payload["phase"].(string); phase != "started" {
			continue
		}
		if mode, _ := ev.Payload["mode"].(string); mode == "M1" {
			sawStarted = true
		}
	}
	if !sawStarted {
		t.Errorf("never saw lifecycle started/M1 event; events = %+v", poster.snapshot())
	}
}

// TestLaunchM1_ErrorsWhenBackendCmdMissing locks the precondition
// guard. M1 launch needs a real cmd to spawn — without one the
// resolver should fail clean, not stand up half a daemon.
func TestLaunchM1_ErrorsWhenBackendCmdMissing(t *testing.T) {
	sp := Spawn{
		ChildID:   "agent-no-cmd",
		Mode:      "M1",
		SpawnSpec: "backend:\n  default_workdir: /tmp\n",
	}
	_, err := launchM1(context.Background(), M1LaunchConfig{
		Spawn:    sp,
		Launcher: &recordingLauncher{},
		Client:   &fakePoster{},
		Spawner:  newFakeProcSpawner(),
		LogDir:   t.TempDir(),
	})
	if err == nil {
		t.Fatal("launchM1 should fail when backend.cmd is empty")
	}
	if !strings.Contains(err.Error(), "backend.cmd") {
		t.Errorf("error missing 'backend.cmd' context: %v", err)
	}
}


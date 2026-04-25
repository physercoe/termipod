package hostrunner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProcSpawner returns the wire ends of an io.Pipe so a test can play the
// role of the child process. Writes to `child` end up in launch_m2's tee ->
// log + driver; reads from `input` would be the child seeing stdin (we don't
// exercise input here).
type fakeProcSpawner struct {
	cmd       string
	child     *io.PipeWriter // test writes here to simulate child stdout
	input     *io.PipeReader // test reads here to see what host-runner wrote
	killed    chan struct{}
}

func newFakeProcSpawner() *fakeProcSpawner {
	return &fakeProcSpawner{killed: make(chan struct{})}
}

func (f *fakeProcSpawner) Spawn(_ context.Context, command string) (io.ReadCloser, io.WriteCloser, func(), error) {
	f.cmd = command
	outR, outW := io.Pipe()
	inR, inW := io.Pipe()
	f.child = outW
	f.input = inR
	kill := func() {
		select {
		case <-f.killed:
		default:
			close(f.killed)
		}
		_ = outW.Close()
		_ = inR.Close()
	}
	return outR, inW, kill, nil
}

// recordingLauncher captures the command and pane target so the test can
// assert on the tail -F invocation without running real tmux.
type recordingLauncher struct {
	gotCmd string
	pane   string
}

func (r *recordingLauncher) Launch(_ context.Context, _ Spawn) (string, error) {
	return r.pane, nil
}

func (r *recordingLauncher) LaunchCmd(_ context.Context, _ Spawn, cmd string) (string, error) {
	r.gotCmd = cmd
	return r.pane, nil
}

func TestLaunchM2_TeesToLogAndStartsDriver(t *testing.T) {
	logDir := t.TempDir()
	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:w1.0"}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID:   "agent-m2x",
		Handle:    "w1",
		Kind:      "claude-code",
		SpawnSpec: "backend:\n  cmd: fake-agent --stream-json\n",
		Mode:      "M2",
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

	if res.PaneID != "hub-agents:w1.0" {
		t.Fatalf("PaneID = %q; want hub-agents:w1.0", res.PaneID)
	}
	if res.Driver == nil {
		t.Fatal("Driver is nil")
	}
	if !strings.Contains(res.LogPath, "termipod-agent-agent-m2x.log") {
		t.Fatalf("LogPath = %q; want contains termipod-agent-agent-m2x.log", res.LogPath)
	}
	if spawner.cmd != "fake-agent --stream-json" {
		t.Fatalf("spawner.cmd = %q; want 'fake-agent --stream-json'", spawner.cmd)
	}
	if !strings.HasPrefix(launcher.gotCmd, "tail -F ") {
		t.Fatalf("launcher.gotCmd = %q; want tail -F …", launcher.gotCmd)
	}
	if !strings.Contains(launcher.gotCmd, res.LogPath) {
		t.Fatalf("launcher.gotCmd = %q; want to include log path %q", launcher.gotCmd, res.LogPath)
	}

	// Feed one stream-json frame so we can observe both the log file and
	// the driver's translation.
	frame := `{"type":"system","subtype":"init","session_id":"s1","model":"m","tools":["Read"]}` + "\n"
	if _, err := spawner.child.Write([]byte(frame)); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	// Driver should have emitted lifecycle.started + session.init.
	evs := poster.wait(t, 2, 2*time.Second)
	if evs[0].Kind != "lifecycle" || evs[0].Payload["phase"] != "started" {
		t.Fatalf("evs[0] = %+v; want lifecycle.started", evs[0])
	}
	if evs[1].Kind != "session.init" || evs[1].Payload["session_id"] != "s1" {
		t.Fatalf("evs[1] = %+v; want session.init s1", evs[1])
	}

	// Log file should mirror what the child wrote. Wait briefly for the
	// tee writer (driver reads → TeeReader writes on Read).
	deadline := time.Now().Add(time.Second)
	var logged []byte
	for time.Now().Before(deadline) {
		b, rerr := os.ReadFile(res.LogPath)
		if rerr == nil && strings.Contains(string(b), `"session_id":"s1"`) {
			logged = b
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(string(logged), `"session_id":"s1"`) {
		t.Fatalf("log file did not receive frame; contents=%q", string(logged))
	}

	// Stop should kill the process and tear the driver down cleanly.
	res.Driver.Stop()
	select {
	case <-spawner.killed:
	case <-time.After(time.Second):
		t.Fatal("Stop did not invoke kill on the child process")
	}
}

func TestLaunchM2_WrapsCommandWithWorkdir(t *testing.T) {
	logDir := t.TempDir()
	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:w2.0"}
	poster := &fakePoster{}

	// Pin HOME so ~ expansion is deterministic.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	sp := Spawn{
		ChildID: "agent-wd",
		Handle:  "w2",
		Kind:    "claude-code",
		SpawnSpec: "backend:\n" +
			"  cmd: claude --print\n" +
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

	wantPrefix := "cd '" + filepath.Join(homeDir, "hub-work") + "' && "
	if !strings.HasPrefix(spawner.cmd, wantPrefix) {
		t.Fatalf("spawner.cmd = %q; want prefix %q", spawner.cmd, wantPrefix)
	}
	if !strings.HasSuffix(spawner.cmd, "claude --print") {
		t.Fatalf("spawner.cmd = %q; want suffix 'claude --print'", spawner.cmd)
	}

	res.Driver.Stop()
}

func TestLaunchM2_ErrorsWhenBackendCmdMissing(t *testing.T) {
	_, err := launchM2(context.Background(), M2LaunchConfig{
		Spawn:    Spawn{ChildID: "a1", SpawnSpec: ""}, // no backend.cmd
		Launcher: StubLauncher{Log: slog.Default()},
		Client:   &fakePoster{},
		Spawner:  newFakeProcSpawner(),
		LogDir:   t.TempDir(),
	})
	if err == nil {
		t.Fatal("want error for missing backend.cmd; got nil")
	}
	if !strings.Contains(err.Error(), "backend.cmd") {
		t.Fatalf("err = %v; want mention of backend.cmd", err)
	}
}

func TestLaunchM2_TolerantOfPaneLaunchFailure(t *testing.T) {
	// Launcher failures should degrade gracefully — driver still works.
	logDir := t.TempDir()
	spawner := newFakeProcSpawner()
	poster := &fakePoster{}

	res, err := launchM2(context.Background(), M2LaunchConfig{
		Spawn:    Spawn{ChildID: "a2", Handle: "w", SpawnSpec: "backend:\n  cmd: cmd\n"},
		Launcher: failingLauncher{},
		Client:   poster,
		Spawner:  spawner,
		LogDir:   logDir,
	})
	if err != nil {
		t.Fatalf("launchM2: %v", err)
	}
	if res.PaneID != "" {
		t.Fatalf("PaneID = %q; want empty on pane failure", res.PaneID)
	}
	if res.Driver == nil {
		t.Fatal("Driver is nil but launchM2 returned no error")
	}
	// Log file should exist in the requested dir.
	if filepath.Dir(res.LogPath) != logDir {
		t.Fatalf("LogPath dir = %q; want %q", filepath.Dir(res.LogPath), logDir)
	}
	res.Driver.Stop()
}

type failingLauncher struct{}

func (failingLauncher) Launch(_ context.Context, _ Spawn) (string, error) {
	return "", io.ErrClosedPipe
}
func (failingLauncher) LaunchCmd(_ context.Context, _ Spawn, _ string) (string, error) {
	return "", io.ErrClosedPipe
}

func TestLaunchM2_WritesContextFilesIntoWorkdir(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:s.0"}
	poster := &fakePoster{}

	// Inline a CLAUDE.md via context_files: the launcher must write it
	// into the workdir before the agent process starts so Claude Code
	// reads it on init. The spec uses default_workdir to resolve where.
	sp := Spawn{
		ChildID: "agent-cf",
		Handle:  "s",
		Kind:    "claude-code",
		SpawnSpec: "backend:\n" +
			"  cmd: fake-agent\n" +
			"  default_workdir: ~/hub-work\n" +
			"context_files:\n" +
			"  CLAUDE.md: |\n" +
			"    hello from steward\n",
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

	wantPath := filepath.Join(homeDir, "hub-work", "CLAUDE.md")
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(body), "hello from steward") {
		t.Errorf("CLAUDE.md = %q; want contains 'hello from steward'", string(body))
	}
}

func TestLaunchM2_ContextFilesWithoutWorkdirFails(t *testing.T) {
	// Writing context_files without a workdir would land the file in
	// host-runner's own cwd, which leaks the agent's persona into the
	// wrong tree. Reject the spawn instead.
	logDir := t.TempDir()
	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: ""}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID: "agent-no-wd",
		Handle:  "s",
		Kind:    "claude-code",
		SpawnSpec: "backend:\n" +
			"  cmd: fake-agent\n" +
			"context_files:\n" +
			"  CLAUDE.md: hi\n",
	}

	_, err := launchM2(context.Background(), M2LaunchConfig{
		Spawn:    sp,
		Launcher: launcher,
		Client:   poster,
		Spawner:  spawner,
		LogDir:   logDir,
	})
	if err == nil {
		t.Fatal("want error when context_files set without default_workdir; got nil")
	}
	if !strings.Contains(err.Error(), "default_workdir") {
		t.Fatalf("err = %v; want mention of default_workdir", err)
	}
}

func TestSafeContextFileName(t *testing.T) {
	cases := map[string]bool{
		"CLAUDE.md":            true,
		"docs/howto.md":        true,
		".mcp.json":            false, // hidden
		"":                     false,
		"/abs":                 false,
		"../escape":            false,
		"sub/../etc/passwd":    false,
		"ok/sub/file":          true,
		"with\\backslash":      false,
		"trailing/":            false,
	}
	for in, want := range cases {
		if got := safeContextFileName(in); got != want {
			t.Errorf("safeContextFileName(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestShellEscape(t *testing.T) {
	cases := map[string]string{
		"/tmp/x.log":         "'/tmp/x.log'",
		"/tmp/it's a log":    "'/tmp/it'\\''s a log'",
		"'starts-with-quote": "''\\''starts-with-quote'",
	}
	for in, want := range cases {
		if got := shellEscape(in); got != want {
			t.Errorf("shellEscape(%q) = %q; want %q", in, got, want)
		}
	}
}

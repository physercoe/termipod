// Structured-mode launch (M2) on host-runner — blueprint §5.3.1.
//
// Unlike M4, where tmux owns the PTY and the agent process is a child
// of that pane, M2 means host-runner owns the process directly so it
// can speak the agent's native JSON-line protocol on stdio. The pane
// still exists (read-mostly display channel — blueprint §5.3.1 calls
// out that the user must still be able to "Enter pane"), but it only
// runs `tail -f <log>` against a file the driver mirrors output into.
//
// This file deliberately does not wire input (user → agent) yet — that
// plumbing belongs with the SSE input subscription and lands in a
// follow-up. Here we only pull stdout into the hub's event stream.
package hostrunner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ProcSpawner is the narrow dependency we inject so tests can stand in a
// fake process without wrestling with real exec, signals, and pipes.
// Implementations return an io.ReadCloser on the child's combined
// stdout+stderr, an io.WriteCloser on its stdin, and a Kill func the
// driver's Stop will invoke.
type ProcSpawner interface {
	Spawn(ctx context.Context, command string) (stdout io.ReadCloser, stdin io.WriteCloser, kill func(), err error)
}

// RealProcSpawner runs the command under `bash -c`, capturing stdout +
// stderr together so the log file (and thus the pane tail) matches what
// would appear on a terminal. Callers needing a distinct stderr stream
// can build a more elaborate spawner later.
type RealProcSpawner struct{}

func (RealProcSpawner) Spawn(ctx context.Context, command string) (io.ReadCloser, io.WriteCloser, func(), error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, err
	}
	// StdoutPipe set cmd.Stdout to the write end of a pipe; point stderr
	// at the same sink so both streams land in the log.
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	kill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	return stdout, stdin, kill, nil
}

// M2LaunchConfig carries everything launchM2 needs, grouped so the
// per-call argument list stays short. LogDir defaults to the process's
// temp dir; Spawner defaults to RealProcSpawner.
type M2LaunchConfig struct {
	Spawn    Spawn
	Launcher Launcher
	Client   AgentEventPoster
	Spawner  ProcSpawner
	LogDir   string
}

// M2LaunchResult is what launchM2 hands back to runner.go so it can keep
// its bookkeeping (pane id, driver handle) the same shape across modes.
type M2LaunchResult struct {
	PaneID  string
	Driver  *StdioDriver
	LogPath string
}

// launchM2 wires an agent in structured-stdio mode: spawn the binary,
// tee its stdout to a log file that a `tail -f` pane renders, and hand
// the same stdout to a StdioDriver that translates stream-json into
// agent_events.
func launchM2(ctx context.Context, cfg M2LaunchConfig) (M2LaunchResult, error) {
	if cfg.Spawner == nil {
		cfg.Spawner = RealProcSpawner{}
	}
	if cfg.LogDir == "" {
		cfg.LogDir = os.TempDir()
	}
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return M2LaunchResult{}, fmt.Errorf("mkdir log dir: %w", err)
	}

	spec, _ := ParseSpec(cfg.Spawn.SpawnSpec)
	command := spec.Backend.Cmd
	if command == "" {
		return M2LaunchResult{}, fmt.Errorf("M2 launch: backend.cmd is empty in spawn spec")
	}

	logPath := filepath.Join(cfg.LogDir, "termipod-agent-"+cfg.Spawn.ChildID+".log")
	// Truncate any stale log from a prior spawn with this id so the tail
	// pane doesn't replay ancient output on reconnect.
	logFile, err := os.Create(logPath)
	if err != nil {
		return M2LaunchResult{}, fmt.Errorf("create log: %w", err)
	}

	stdout, stdin, kill, err := cfg.Spawner.Spawn(ctx, command)
	if err != nil {
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return M2LaunchResult{}, fmt.Errorf("spawn: %w", err)
	}

	// Tee the child's stdout through the log file so the pane `tail -f`
	// and the driver see the same bytes. TeeReader writes synchronously
	// on each Read — if the disk stalls, so does the driver. That's
	// acceptable: the log writing is local, bounded, and tiny.
	teed := io.TeeReader(stdout, logFile)

	// The pane is cosmetic in M2; if the launcher doesn't support
	// LaunchCmd (or it fails) the driver still works — we just lose the
	// Enter-pane affordance. Tolerate that rather than aborting.
	paneCmd := fmt.Sprintf("tail -F %s", shellEscape(logPath))
	pane, paneErr := cfg.Launcher.LaunchCmd(ctx, cfg.Spawn, paneCmd)
	if paneErr != nil {
		pane = "" // non-fatal; driver owns the real stream
	}

	drv := &StdioDriver{
		AgentID: cfg.Spawn.ChildID,
		Poster:  cfg.Client,
		Stdout:  teed,
		Closer: func() {
			kill()
			_ = stdin.Close()
			_ = stdout.Close()
			_ = logFile.Close()
		},
	}
	if err := drv.Start(ctx); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = logFile.Close()
		kill()
		return M2LaunchResult{}, fmt.Errorf("driver start: %w", err)
	}

	return M2LaunchResult{PaneID: pane, Driver: drv, LogPath: logPath}, nil
}

// shellEscape wraps a path in single quotes, escaping any embedded
// single quote. The log path is host-runner–controlled so this is
// belt-and-braces, but a pane command crafted by `bash -c` deserves
// some care in case someone later lets spec fields flow in.
func shellEscape(s string) string {
	return "'" + escapeSingleQuotes(s) + "'"
}

func escapeSingleQuotes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, "'\\''"...)
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

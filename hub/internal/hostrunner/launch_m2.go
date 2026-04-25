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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	hub "github.com/termipod/hub"
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
	// HubURL is the base URL host-runner uses to talk to the hub. It's
	// written into the agent's `.mcp.json` together with the spawn's
	// per-agent token so the spawned claude process can resolve
	// `mcp__termipod__*` tools. Optional — when empty (or when the
	// spawn carries no token) `.mcp.json` is not written and the agent
	// runs without hub MCP access.
	HubURL string
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
	// `cd <workdir> && <cmd>` so the agent process inherits the right cwd.
	// We expand ~ ourselves rather than relying on bash's tilde expansion
	// because the launcher passes the command through `bash -c "..."` and
	// we want the failure mode to be a clean Go error if HOME is unset.
	expandedWorkdir := ""
	if wd := spec.Backend.DefaultWorkdir; wd != "" {
		expanded, err := expandHome(wd)
		if err != nil {
			return M2LaunchResult{}, fmt.Errorf("expand default_workdir %q: %w", wd, err)
		}
		expandedWorkdir = expanded
		command = fmt.Sprintf("cd %s && %s", shellEscape(expanded), command)
	}

	// Materialize context_files (CLAUDE.md, etc.) into the workdir so
	// Claude Code reads its persona on startup. We only do this when a
	// workdir is set — without one we'd be writing into an unknown cwd
	// (likely the host-runner's own dir), which leaks the agent's
	// configuration into the wrong tree. Failure here is fatal: an agent
	// missing its CLAUDE.md will behave very differently from one with it.
	if len(spec.ContextFiles) > 0 {
		if expandedWorkdir == "" {
			return M2LaunchResult{}, fmt.Errorf("context_files set but backend.default_workdir is empty")
		}
		if err := writeContextFiles(expandedWorkdir, spec.ContextFiles); err != nil {
			return M2LaunchResult{}, fmt.Errorf("write context_files: %w", err)
		}
	}

	// Materialize .mcp.json with the per-agent bearer so claude-code
	// can speak the hub's MCP endpoint (and therefore resolve the
	// `mcp__termipod__permission_prompt` tool referenced by the
	// steward template's --permission-prompt-tool flag). Skipped when
	// the spawn carries no token or the launcher has no hub URL: the
	// agent still runs, just without hub-mediated tool gating. We
	// write directly (not via writeContextFiles) so the dotfile is
	// kept out of spec.context_files — secrets shouldn't ride in the
	// spawn_spec_yaml that's persisted on the hub.
	if cfg.Spawn.MCPToken != "" && cfg.HubURL != "" {
		if expandedWorkdir == "" {
			return M2LaunchResult{}, fmt.Errorf("mcp_token set but backend.default_workdir is empty")
		}
		if err := writeMCPConfig(expandedWorkdir, cfg.HubURL, cfg.Spawn.MCPToken); err != nil {
			return M2LaunchResult{}, fmt.Errorf("write .mcp.json: %w", err)
		}
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
		Stdin:   stdin,
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

// writeContextFiles materializes per-agent files (CLAUDE.md, .mcp.json,
// etc.) into the workdir before launch. Keys must be simple filenames or
// shallow forward-slash relative paths — anything starting with `/`,
// containing `..`, or backtracking out of the workdir is rejected so a
// hostile spawn_spec can't write into /etc.
func writeContextFiles(workdir string, files map[string]string) error {
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}
	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		return err
	}
	for name, content := range files {
		if !safeContextFileName(name) {
			return fmt.Errorf("invalid context_files key %q", name)
		}
		target := filepath.Join(absWorkdir, name)
		// Belt-and-braces: ensure the resolved path stays inside workdir
		// even after Join's cleanup.
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(absTarget, absWorkdir+string(os.PathSeparator)) && absTarget != absWorkdir {
			return fmt.Errorf("context_files key %q escapes workdir", name)
		}
		if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", name, err)
		}
		if err := os.WriteFile(absTarget, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// writeMCPConfig writes a `.mcp.json` into the agent workdir that
// points claude-code at hub-mcp-bridge as the stdio transport for
// MCP tools. The server name comes from hub.MCPServerName so it stays
// in lockstep with the {{mcp_namespace}} template render var; the
// steward template's --permission-prompt-tool flag must reference the
// same namespace, e.g. mcp__termipod__permission_prompt.
//
// Token handling: claude-code reads `.mcp.json` from disk and treats
// it as plaintext config. We write 0o600 so it isn't world-readable
// on shared hosts. Re-running this overwrites — host-runner is
// idempotent across spawns and a stale token must not linger.
func writeMCPConfig(workdir, hubURL, token string) error {
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			hub.MCPServerName: map[string]any{
				"command": "hub-mcp-bridge",
				"env": map[string]string{
					"HUB_URL":   hubURL,
					"HUB_TOKEN": token,
				},
			},
		},
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(workdir, ".mcp.json")
	return os.WriteFile(target, body, 0o600)
}

func safeContextFileName(n string) bool {
	if n == "" || strings.HasPrefix(n, "/") || strings.HasPrefix(n, ".") || strings.Contains(n, `\`) {
		return false
	}
	for _, part := range strings.Split(n, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// expandHome resolves a leading `~` or `~/` against $HOME. We do this
// in Go (instead of letting bash do it inside `bash -c`) so a missing
// HOME yields a structured error here rather than the agent process
// silently starting in `/`.
func expandHome(p string) (string, error) {
	if p == "" || (p[0] != '~') {
		return p, nil
	}
	if len(p) > 1 && p[1] != '/' {
		// `~user` form — not supported. Return as-is rather than guess.
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("HOME unresolved")
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

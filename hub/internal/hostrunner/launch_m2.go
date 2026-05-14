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
	"syscall"
	"time"

	hub "github.com/termipod/hub"
	"github.com/termipod/hub/internal/agentfamilies"
)

// ProcSpawner is the narrow dependency we inject so tests can stand in a
// fake process without wrestling with real exec, signals, and pipes.
// Implementations return an io.ReadCloser on the child's combined
// stdout+stderr, an io.WriteCloser on its stdin, and a Kill func the
// driver's Stop will invoke.
type ProcSpawner interface {
	Spawn(ctx context.Context, command string) (stdout io.ReadCloser, stdin io.WriteCloser, kill func(), err error)
	// SpawnWithStderr returns the child's stdout and stderr as separate
	// streams. M1 (ACP) launch uses this so non-JSON stderr lines (auth
	// diagnostics, ripgrep warnings, etc.) don't pollute the JSON-RPC
	// frame parser the driver runs over stdout. M2 still uses Spawn —
	// stream-json drivers tolerate stderr garbage on stdout and the
	// merged stream gives a richer pane tail.
	SpawnWithStderr(ctx context.Context, command string) (stdout io.ReadCloser, stderr io.ReadCloser, stdin io.WriteCloser, kill func(), err error)
}

// RealProcSpawner runs the command under `bash -c`. Spawn merges
// stderr into stdout (M2 path); SpawnWithStderr keeps them separate
// (M1 path) so the ACP driver's frame parser only sees clean JSON-RPC.
type RealProcSpawner struct{}

func (RealProcSpawner) Spawn(ctx context.Context, command string) (io.ReadCloser, io.WriteCloser, func(), error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	// Run bash + its descendants in a fresh process group so kill()
	// reaches the engine the bash invocation actually exec'd. Without
	// Setpgid, `cmd.Process.Kill()` SIGKILLs only the bash parent; the
	// gemini-cli (or any) child inherits init as its parent and stays
	// alive holding per-user file locks (~/.gemini/oauth_creds.json,
	// settings.json singleton). The next M1 launch then stalls at
	// `initialize` — the new daemon contends with the orphan and times
	// out at HandshakeTimeout (90s), forcing an M2 fallback with a
	// brand-new session_id.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	kill := killProcessGroup(cmd)
	return stdout, stdin, kill, nil
}

func (RealProcSpawner) SpawnWithStderr(ctx context.Context, command string) (io.ReadCloser, io.ReadCloser, io.WriteCloser, func(), error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	// See Spawn() for why the process group matters. M1 spawn relies on
	// this so a hung gemini-cli daemon dies on Stop() instead of leaking
	// into the next launch's `initialize` window.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, nil, nil, nil, err
	}
	kill := killProcessGroup(cmd)
	return stdout, stderr, stdin, kill, nil
}

// killProcessGroup returns a closer that SIGKILLs the entire process
// group rooted at cmd. Negative pid in syscall.Kill targets the process
// group leader's group; we also call cmd.Process.Kill() as a belt-and-
// suspenders fallback for the (rare) case where Setpgid silently failed
// at Start time. Idempotent: repeated calls after the group is dead
// return ESRCH which we ignore.
func killProcessGroup(cmd *exec.Cmd) func() {
	return func() {
		if cmd == nil || cmd.Process == nil {
			return
		}
		// pgid == pid because we set Setpgid=true at Start; the kernel
		// makes the child its own pgrp leader.
		pid := cmd.Process.Pid
		if pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		_ = cmd.Process.Kill()
	}
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
// Driver is the interface type so launchM2 can return either the
// stream-json StdioDriver (claude-code, gemini-cli) or the JSON-RPC
// AppServerDriver (codex, ADR-012) without runner.go caring.
type M2LaunchResult struct {
	PaneID  string
	Driver  Driver
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
	//
	// Workdir resolution (ADR-025 W6):
	//   1. spec.Backend.DefaultWorkdir   — explicit template field wins.
	//   2. cfg.Spawn.ProjectID set        — derive ~/hub-work/<pid8>/<handle>
	//      so workers in the same project share a folder root and sibling
	//      handles don't collide across projects.
	//   3. neither                        — empty, command runs from
	//      host-runner's cwd (legacy single-host demo path).
	wd := spec.Backend.DefaultWorkdir
	if wd == "" && cfg.Spawn.ProjectID != "" {
		pid := cfg.Spawn.ProjectID
		if len(pid) > 8 {
			pid = pid[:8]
		}
		handle := cfg.Spawn.Handle
		if handle == "" {
			handle = cfg.Spawn.ChildID
		}
		wd = filepath.Join("~", "hub-work", pid, handle)
	}
	expandedWorkdir := ""
	if wd != "" {
		expanded, err := expandHome(wd)
		if err != nil {
			return M2LaunchResult{}, fmt.Errorf("expand workdir %q: %w", wd, err)
		}
		// Ensure the directory exists — derivation builds a new path the
		// first time a worker lands in this project, and writeContextFiles
		// / writeMCPConfig below would fail otherwise. 0o755 matches the
		// downstream calls so a re-launch is idempotent.
		if err := os.MkdirAll(expanded, 0o755); err != nil {
			return M2LaunchResult{}, fmt.Errorf("mkdir workdir %q: %w", expanded, err)
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

	// Materialize the engine's MCP config with the per-agent bearer
	// so the spawned process can speak the hub's MCP endpoint (and
	// therefore resolve `mcp__termipod__permission_prompt` etc).
	// Skipped when the spawn carries no token or the launcher has no
	// hub URL: the agent still runs, just without hub-mediated tool
	// gating. We write directly (not via writeContextFiles) so the
	// dotfile is kept out of spec.context_files — secrets shouldn't
	// ride in the spawn_spec_yaml that's persisted on the hub.
	//
	// Per-family format: claude-code reads `.mcp.json` (project-local
	// JSON); codex reads `.codex/config.toml` per ADR-012 D5. Other
	// families fall through to the JSON shape until they ship their
	// own materializer.
	if cfg.Spawn.MCPToken != "" && cfg.HubURL != "" {
		if expandedWorkdir == "" {
			return M2LaunchResult{}, fmt.Errorf("mcp_token set but backend.default_workdir is empty")
		}
		if err := writeMCPConfigForFamily(
			cfg.Spawn.Kind, expandedWorkdir, cfg.HubURL, cfg.Spawn.MCPToken,
		); err != nil {
			return M2LaunchResult{}, fmt.Errorf("write mcp config: %w", err)
		}
	}

	logPath := filepath.Join(cfg.LogDir, "termipod-agent-"+cfg.Spawn.ChildID+".log")
	// Truncate any stale log from a prior spawn with this id so the tail
	// pane doesn't replay ancient output on reconnect.
	logFile, err := os.Create(logPath)
	if err != nil {
		return M2LaunchResult{}, fmt.Errorf("create log: %w", err)
	}

	// gemini-cli is exec-per-turn-with-resume (ADR-013): no
	// long-running child process to spawn here, no stdout to tee,
	// no pane to attach. The driver itself spawns
	// `gemini -p <text> --output-format stream-json [--resume UUID]`
	// per Input call. Branch out before the persistent-spawn machinery
	// so we don't burn a placeholder process to satisfy a flow shape
	// gemini doesn't fit.
	if cfg.Spawn.Kind == "gemini-cli" {
		// Don't remove the log: the runtime fallback ladder
		// (runner.launchOne) reaches us only when a prior M1 launch
		// failed, and that M1 launcher already attached a tmux pane
		// running `tail -F <logPath>`. Removing the file makes that
		// orphan pane print "tail: file inaccessible" forever. Keep
		// the file alive and append a one-line mode-switch notice so
		// the pane stays readable and the operator can tell what
		// happened. When M2 is the primary mode (no prior M1, no
		// orphan pane) the file is just an inert empty marker.
		fmt.Fprintf(logFile,
			"[host-runner] M1 unavailable; falling back to M2 (gemini-exec-resume). Per-turn output flows through agent_events; nothing to stream here.\n")
		_ = logFile.Close()
		fam, ok := agentfamilies.ByName("gemini-cli")
		if !ok {
			return M2LaunchResult{}, fmt.Errorf("gemini-cli family missing from registry")
		}
		bin := strings.TrimSpace(spec.Backend.Cmd)
		if bin == "" {
			bin = fam.Bin
		}
		// The same spec is sometimes reused across modes via the
		// runtime fallback ladder (runner.launchOne walks primary →
		// fallback_modes → M4 with one spec). M1's cmd typically
		// carries argv ("gemini --acp"); for M2 we need only the
		// binary token because the ExecResumeDriver constructs its
		// own argv. Without this trim, exec.LookPath looks for a file
		// literally called "gemini --acp" with the space and the M2
		// fallback fails with a confusing PATH error.
		if i := strings.IndexAny(bin, " \t"); i > 0 {
			bin = bin[:i]
		}
		resolved, lookErr := exec.LookPath(bin)
		if lookErr != nil {
			return M2LaunchResult{}, fmt.Errorf("gemini bin %q: %w", bin, lookErr)
		}
		// gemini-cli@0.41+ gates headless turns behind a trusted-folders
		// list; an untrusted workdir overrides --yolo back to "default"
		// and the binary exits before producing any stream-json frames.
		// Hub-work is intentionally the agent's operating dir, so opt
		// into trust for it explicitly. Prepended to os.Environ() so a
		// host-runner-level override (operator unsetting it) still wins.
		geminiEnv := append([]string{"GEMINI_CLI_TRUST_WORKSPACE=true"}, os.Environ()...)
		drv := &ExecResumeDriver{
			AgentID:        cfg.Spawn.ChildID,
			Handle:         cfg.Spawn.Handle,
			Poster:         cfg.Client,
			Bin:            resolved,
			Workdir:        expandedWorkdir,
			Env:            geminiEnv,
			Yolo:           true, // ADR-013 D4 — see steward template
			FrameProfile:   fam.FrameProfile,
			CommandBuilder: ExecCommandBuilder(expandedWorkdir, geminiEnv),
		}
		if err := drv.Start(ctx); err != nil {
			return M2LaunchResult{}, fmt.Errorf("exec-resume start: %w", err)
		}
		return M2LaunchResult{Driver: drv}, nil
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

	// Pull the family's frame_translator policy + profile so the driver
	// can dispatch via the data-driven path when configured. Unknown
	// kinds (gemini-cli before its profile lands) leave both fields
	// zero → translate() falls through to legacyTranslate. ADR-010
	// Phase 1.6: default is legacy until canary holds.
	var frameTranslator string
	var frameProfile *agentfamilies.FrameProfile
	var familyName string
	if fam, ok := agentfamilies.ByName(cfg.Spawn.Kind); ok {
		frameTranslator = fam.FrameTranslator
		frameProfile = fam.FrameProfile
		familyName = fam.Family
	}

	closer := func() {
		// Final notice for the cosmetic tail pane — see launch_m1's
		// equivalent for rationale.
		_, _ = fmt.Fprintf(logFile, "\n[host-runner] M2 stopped at %s\n",
			time.Now().UTC().Format(time.RFC3339))
		kill()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = logFile.Close()
	}

	// Per-family driver dispatch (ADR-012 D1). Codex's app-server
	// speaks JSON-RPC, not stream-json — same line-delimited stdio,
	// different framing — so it gets its own driver. Everything else
	// uses StdioDriver with the frame-profile + legacy translators
	// from ADR-010.
	var drv Driver
	if familyName == "codex" {
		// AttentionPoster is the same Client object — only the codex
		// driver uses the bridge today, but the dependency stays
		// narrow (PostAttention only) so future drivers can opt in.
		// AttentionPoster is non-nil only when cfg.Client itself is a
		// real Client; tests pass nil and the driver's bridge path
		// falls through to auto-decline.
		var attention AttentionPoster
		if c, ok := cfg.Client.(AttentionPoster); ok {
			attention = c
		}
		drv = &AppServerDriver{
			AgentID:      cfg.Spawn.ChildID,
			Handle:       cfg.Spawn.Handle,
			Poster:       cfg.Client,
			Attention:    attention,
			Stdout:       teed,
			Stdin:        stdin,
			FrameProfile: frameProfile,
			Closer:       closer,
		}
	} else {
		drv = &StdioDriver{
			AgentID:         cfg.Spawn.ChildID,
			Poster:          cfg.Client,
			Stdout:          teed,
			Stdin:           stdin,
			FrameTranslator: frameTranslator,
			FrameProfile:    frameProfile,
			Closer:          closer,
		}
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
// registers the single MCP server the spawned agent talks to:
//
//	termipod (hub.MCPServerName) — the hub's in-process MCP catalog
//	reached through hub-mcp-bridge → /mcp/<token>. Exposes the union of
//	the narrow surface (gates, attention, post_excerpt, journal,
//	orchestrator-worker primitives) AND the rich-authority surface
//	(projects, plans, runs, documents, reviews, agents.spawn, a2a.invoke,
//	channels.post_event, schedules.*, tasks.*) — the latter is wired
//	in-process by mcp_authority.go via a chi-router HTTP transport, so
//	the bridge alone reaches everything that used to require a second
//	hub-mcp-server daemon.
//
// Plaintext config at 0o600 — re-running overwrites; host-runner is
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

// writeMCPConfigForFamily picks the right materializer by family.
// Claude-code (the default) writes .mcp.json; codex writes
// .codex/config.toml per ADR-012 D5; gemini writes
// .gemini/settings.json per ADR-013 D5.
//
// Spawn templates are responsible for pointing the engine at this
// config. Codex picks up `.codex/config.toml` only from "trusted
// projects" by default, so the codex spawn template sets
// `CODEX_HOME=<workdir>/.codex` in the launch env; that bypasses
// the trust-list and keeps each spawn's MCP scope isolated to its
// own workdir. Gemini reads project-scoped `<workdir>/.gemini/
// settings.json` automatically — no equivalent gate to bypass.
func writeMCPConfigForFamily(family, workdir, hubURL, token string) error {
	switch family {
	case "codex":
		return writeCodexMCPConfig(workdir, hubURL, token)
	case "gemini-cli":
		return writeGeminiMCPConfig(workdir, hubURL, token)
	case "kimi-code":
		return writeKimiMCPConfig(workdir, hubURL, token)
	default:
		return writeMCPConfig(workdir, hubURL, token)
	}
}

// writeKimiMCPConfig emits kimi-code's JSON form at
// <workdir>/.kimi/mcp.json (ADR-026 D5). The kimi-cli `--mcp-config-file`
// flag is top-level, repeatable, and defaults to ~/.kimi/mcp.json; we
// pin per-spawn isolation by writing into the agent's workdir and
// splicing --mcp-config-file <path> into the cmd at launch (see
// launch_m1.go).
//
// We deep-merge with the operator's existing ~/.kimi/mcp.json so that
// any MCP servers they configured on the host pass through unchanged:
// kimi sees both the operator-configured servers AND the per-spawn
// `termipod` entry pointing at hub-mcp-bridge. The operator's
// `~/.kimi/config.toml` is untouched — its `[services.moonshot_search]`
// API key stays where they put it.
//
// Wire shape mirrors gemini's settings.json and claude's .mcp.json:
//
//	{
//	  "mcpServers": {
//	    "termipod": {
//	      "command": "hub-mcp-bridge",
//	      "env": { "HUB_URL": "<url>", "HUB_TOKEN": "<token>" }
//	    },
//	    ...operator entries pass through...
//	  }
//	}
//
// File mode 0o600; .kimi directory mode 0o700. Malformed operator
// mcp.json fails the merge loud — we don't silently clobber a file
// the operator may be relying on for non-termipod MCP servers.
func writeKimiMCPConfig(workdir, hubURL, token string) error {
	dir := filepath.Join(workdir, ".kimi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir .kimi: %w", err)
	}

	// Start with the operator's existing ~/.kimi/mcp.json contents
	// (if any). Missing file → empty seed. Parse failure → fail loud
	// rather than silently overwrite — operators using non-termipod
	// MCP servers via the same file should see a clear error, not
	// quietly lose them.
	cfg := map[string]any{"mcpServers": map[string]any{}}
	if home, err := os.UserHomeDir(); err == nil {
		operatorPath := filepath.Join(home, ".kimi", "mcp.json")
		if data, rerr := os.ReadFile(operatorPath); rerr == nil {
			var existing map[string]any
			if jerr := json.Unmarshal(data, &existing); jerr != nil {
				return fmt.Errorf("parse %s: %w", operatorPath, jerr)
			}
			// Preserve everything outside mcpServers (kimi-cli may
			// honor sibling keys in future versions). Merge in
			// existing mcpServers entries.
			for k, v := range existing {
				cfg[k] = v
			}
			if existingServers, ok := existing["mcpServers"].(map[string]any); ok {
				cfg["mcpServers"] = existingServers
			} else {
				cfg["mcpServers"] = map[string]any{}
			}
		}
	}

	// Splice in (or replace) the termipod entry. Replace-not-skip is
	// intentional: a previous spawn might have written a stale
	// HUB_TOKEN, and the operator should never have hand-edited a
	// `termipod` entry into their own mcp.json.
	servers := cfg["mcpServers"].(map[string]any)
	servers[hub.MCPServerName] = map[string]any{
		"command": "hub-mcp-bridge",
		"env": map[string]string{
			"HUB_URL":   hubURL,
			"HUB_TOKEN": token,
		},
	}
	cfg["mcpServers"] = servers

	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "mcp.json")
	return os.WriteFile(target, body, 0o600)
}

// writeGeminiMCPConfig emits gemini-cli's JSON form at
// <workdir>/.gemini/settings.json (ADR-013 D5):
//
//	{
//	  "mcpServers": {
//	    "termipod": {
//	      "command": "hub-mcp-bridge",
//	      "env": { "HUB_URL": "<url>", "HUB_TOKEN": "<token>" }
//	    }
//	  }
//	}
//
// Wire shape matches claude's .mcp.json exactly because gemini-cli's
// settings.json mcpServers schema accepts the same stdio
// command+env transport. Different file location (project-scoped
// .gemini/ rather than top-level dotfile), same hub-mcp-bridge on
// the other end. File mode 0o600; .gemini directory mode 0o700.
func writeGeminiMCPConfig(workdir, hubURL, token string) error {
	dir := filepath.Join(workdir, ".gemini")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir .gemini: %w", err)
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
	target := filepath.Join(dir, "settings.json")
	return os.WriteFile(target, body, 0o600)
}

// writeCodexMCPConfig emits codex's TOML form:
//
//	[mcp_servers.termipod]
//	command = "hub-mcp-bridge"
//
//	[mcp_servers.termipod.env]
//	HUB_URL = "<url>"
//	HUB_TOKEN = "<token>"
//
// We hand-format rather than depend on a TOML library — the shape is
// fixed, the values are the only variables, and adding a runtime dep
// (BurntSushi/toml or pelletier/go-toml) for two stanzas isn't
// proportionate. If the format ever grows arrays-of-tables or other
// TOML niceties, that calculus changes.
//
// File location: <workdir>/.codex/config.toml. Codex normally reads
// from $CODEX_HOME (default ~/.codex) — the spawn template overrides
// CODEX_HOME at launch time so the engine reads our project-scoped
// file without touching the user's global codex config.
//
// Auth caveat: codex stores its login/API-key credentials in
// $CODEX_HOME/auth.json (written by `codex login`). When we redirect
// CODEX_HOME at spawn we lose visibility of that file, and codex
// falls back to "no auth" → 401 Unauthorized on the OpenAI websocket.
// To keep "works in terminal → works under host-runner" parity, copy
// the user's auth.json into the spawn-scoped CODEX_HOME alongside the
// MCP config. Best-effort: a missing/unreadable source is logged but
// does not fail the spawn, so a fully-headless deployment with auth
// in env vars still works.
func writeCodexMCPConfig(workdir, hubURL, token string) error {
	dir := filepath.Join(workdir, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir .codex: %w", err)
	}
	body := codexConfigTOML(hub.MCPServerName, hubURL, token)
	target := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		return err
	}
	copyCodexAuthInto(dir)
	return nil
}

// copyCodexAuthInto best-effort-copies the user's codex auth.json into
// the spawn-scoped CODEX_HOME. Returns silently on any error: callers
// must not depend on auth.json being present (some users deploy with
// $OPENAI_API_KEY in env, which codex resolves without auth.json).
//
// Source resolution mirrors codex's own:
//  1. $CODEX_HOME (the *outer* host-runner process env, before we
//     redirect it for the child)
//  2. $HOME/.codex
//
// Mode is forced to 0o600 — auth.json holds bearer tokens.
func copyCodexAuthInto(dstDir string) {
	src := userCodexAuthPath()
	if src == "" {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dstDir, "auth.json"), data, 0o600)
}

// userCodexAuthPath resolves the user's codex auth.json location at
// the time host-runner is running, before we override CODEX_HOME for
// any child. Returns "" when neither $CODEX_HOME nor $HOME is set.
func userCodexAuthPath() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

// codexConfigTOML produces the literal config.toml bytes. Factored
// out for direct unit-testing of the wire shape; the file-write
// layer is exercised via writeCodexMCPConfig + a temp dir.
//
// approval_policy: codex's default ("on-request") gates every MCP
// tool call behind a wrapper elicitation ("Allow the termipod MCP
// server to run tool X?"). For our deployment that wrapper is
// strictly redundant — termipod's request_select / request_approval
// / request_help tools each post their own attention_items row, so
// the principal already sees the decision through the hub. Setting
// "never" skips codex's wrapper gate AND its shell-command /
// file-change approval gates. The hub is the trust boundary; codex
// running inside it doesn't need a second checkpoint. Operators who
// want codex's local gates back can override via the
// `TERMIPOD_CODEX_APPROVAL_POLICY` env var on the host-runner
// process before spawn.
func codexConfigTOML(serverName, hubURL, token string) string {
	policy := codexApprovalPolicy()
	return "" +
		"# Generated by termipod host-runner. Re-spawning overwrites this file.\n" +
		"approval_policy = " + tomlString(policy) + "\n" +
		"\n" +
		"[mcp_servers." + tomlBareKey(serverName) + "]\n" +
		"command = " + tomlString("hub-mcp-bridge") + "\n" +
		"\n" +
		"[mcp_servers." + tomlBareKey(serverName) + ".env]\n" +
		"HUB_URL = " + tomlString(hubURL) + "\n" +
		"HUB_TOKEN = " + tomlString(token) + "\n"
}

// codexApprovalPolicy returns the value to write into config.toml's
// top-level approval_policy field. Defaults to "never" — see
// codexConfigTOML for the rationale. Override via
// TERMIPOD_CODEX_APPROVAL_POLICY (accepted: "untrusted",
// "on-request", "never"; "on-failure" is upstream-deprecated and
// rejected here so we don't paper over a bad default). Unknown
// values fall back to "never" with no error — config validation is
// codex's job, and we don't want a typo in the env to break spawn.
func codexApprovalPolicy() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("TERMIPOD_CODEX_APPROVAL_POLICY")))
	switch v {
	case "untrusted", "on-request", "never":
		return v
	}
	return "never"
}

// tomlBareKey is permissive — it expects the caller to feed it a
// known-safe identifier (hub.MCPServerName is a compile-time
// constant). Quoted-key syntax is left for a future expansion if
// the constant ever needs spaces or punctuation.
func tomlBareKey(s string) string { return s }

// tomlString writes a TOML basic string (double-quoted) with the
// minimum escapes needed for our values: backslash, double quote,
// and the control characters TOML disallows in basic strings.
// Hub URLs are ASCII; tokens are URL-safe base64 — neither hits the
// edges, but we escape correctly anyway so a future change to token
// shape doesn't silently corrupt the file.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				// TOML disallows raw control bytes in basic strings.
				// Hex-escape per the spec.
				const hex = "0123456789ABCDEF"
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
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

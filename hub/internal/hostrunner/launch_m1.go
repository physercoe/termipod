// Structured-mode launch (M1, ACP) on host-runner — blueprint §5.3.1.
//
// M1 spawns the engine in Zed's Agent Client Protocol mode: long-running
// stdio JSON-RPC daemon, hub-side ACPDriver speaks `initialize` →
// `session/new` → `session/prompt`, agent emits `session/update`
// notifications back. The wire shape is similar to codex's app-server
// (M2/codex path) — line-delimited JSON over stdin/stdout — but the
// method names follow ACP, not codex's bespoke item-stream RPC.
//
// gemini-cli's `--acp` flag is the first concrete user (replacing the
// exec-per-turn-with-resume path that ADR-013 originally chose, before
// gemini's ACP support stabilised). Future engines that ship ACP daemon
// modes (claude-code SDK, etc.) drop into this same path with no Go
// diff — only a steward-template `cmd:` change to invoke their ACP flag.
//
// Most of the launch machinery (workdir / context_files / MCP config /
// log + tail pane) is identical to launchM2; the only divergent piece
// is the driver dispatch at the end. We deliberately keep the two
// functions parallel rather than merging them — the mode boundary is
// where the protocol contract changes, and inlining keeps each
// function's preconditions readable on their own.
package hostrunner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/termipod/hub/internal/agentfamilies"
)

// M1LaunchConfig mirrors M2LaunchConfig — same dependencies (process
// spawner, tmux launcher for the cosmetic pane, hub event poster, MCP
// hub URL). Kept as a separate type so future M1-specific options
// (e.g. ACP capability negotiation tweaks) don't bleed into the M2
// surface.
type M1LaunchConfig struct {
	Spawn    Spawn
	Launcher Launcher
	Client   AgentEventPoster
	Spawner  ProcSpawner
	LogDir   string
	HubURL   string
}

// M1LaunchResult — same shape as M2LaunchResult so runner.go's mode
// dispatch can record pane id / driver / log path uniformly.
type M1LaunchResult struct {
	PaneID  string
	Driver  Driver
	LogPath string
}

// launchM1 spawns the engine in ACP mode and wires ACPDriver to its
// stdio. On handshake failure (e.g. the engine doesn't actually
// support `--acp`), the caller is expected to fall back to M2/M4.
func launchM1(ctx context.Context, cfg M1LaunchConfig) (M1LaunchResult, error) {
	if cfg.Spawner == nil {
		cfg.Spawner = RealProcSpawner{}
	}
	if cfg.LogDir == "" {
		cfg.LogDir = os.TempDir()
	}
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		return M1LaunchResult{}, fmt.Errorf("mkdir log dir: %w", err)
	}

	spec, _ := ParseSpec(cfg.Spawn.SpawnSpec)
	command := spec.Backend.Cmd
	if command == "" {
		return M1LaunchResult{}, fmt.Errorf("M1 launch: backend.cmd is empty in spawn spec")
	}

	// gemini-cli@0.41+ refuses headless launch (incl. --acp) from an
	// untrusted folder. Inline-export the trust opt-in into the bash -c
	// command — this is the M1 mirror of the geminiEnv hook in launch_m2.
	// Codex / claude-code don't read this var so it's a safe no-op for
	// other families; we still gate on Kind to keep env clean. We also
	// splice --skip-trust into the cmd if the operator hasn't already
	// added it — protects against env stripping by pid 1 / sudo.
	if cfg.Spawn.Kind == "gemini-cli" {
		if !strings.Contains(command, "--skip-trust") {
			command = strings.Replace(command, "gemini ", "gemini --skip-trust ", 1)
		}
		command = "GEMINI_CLI_TRUST_WORKSPACE=true " + command
	}

	expandedWorkdir := ""
	if wd := spec.Backend.DefaultWorkdir; wd != "" {
		expanded, err := expandHome(wd)
		if err != nil {
			return M1LaunchResult{}, fmt.Errorf("expand default_workdir %q: %w", wd, err)
		}
		expandedWorkdir = expanded
		command = fmt.Sprintf("cd %s && %s", shellEscape(expanded), command)
	}

	if len(spec.ContextFiles) > 0 {
		if expandedWorkdir == "" {
			return M1LaunchResult{}, fmt.Errorf("context_files set but backend.default_workdir is empty")
		}
		if err := writeContextFiles(expandedWorkdir, spec.ContextFiles); err != nil {
			return M1LaunchResult{}, fmt.Errorf("write context_files: %w", err)
		}
	}

	// MCP config materialization is the same as M2 — gemini's `--acp`
	// mode still reads `<workdir>/.gemini/settings.json` for its
	// mcpServers list, so writing the per-spawn settings file gives
	// the agent hub-mediated MCP access regardless of which mode
	// drives the JSON-RPC layer. ACPDriver itself sends an empty
	// mcpServers in its session/new — gemini honors the file-level
	// config, which is what we want for per-spawn isolation.
	if cfg.Spawn.MCPToken != "" && cfg.HubURL != "" {
		if expandedWorkdir == "" {
			return M1LaunchResult{}, fmt.Errorf("mcp_token set but backend.default_workdir is empty")
		}
		if err := writeMCPConfigForFamily(
			cfg.Spawn.Kind, expandedWorkdir, cfg.HubURL, cfg.Spawn.MCPToken,
		); err != nil {
			return M1LaunchResult{}, fmt.Errorf("write mcp config: %w", err)
		}
		// kimi-code does NOT auto-discover the per-spawn mcp.json the
		// way gemini-cli auto-discovers <workdir>/.gemini/settings.json
		// from a cwd-matching trust gate. Kimi's `--mcp-config-file`
		// flag is the only sanctioned injection path, and its default
		// (~/.kimi/mcp.json) points at the operator's home dir — which
		// our writeKimiMCPConfig has already merged into the per-spawn
		// copy. Splice the flag into the argv between `kimi` and the
		// next top-level flag so the per-spawn file wins. Idempotent
		// against operator templates that already specify the flag.
		if cfg.Spawn.Kind == "kimi-code" && !strings.Contains(command, "--mcp-config-file") {
			mcpPath := filepath.Join(expandedWorkdir, ".kimi", "mcp.json")
			command = strings.Replace(command, "kimi ",
				"kimi --mcp-config-file "+shellEscape(mcpPath)+" ", 1)
		}
	}

	logPath := filepath.Join(cfg.LogDir, "termipod-agent-"+cfg.Spawn.ChildID+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return M1LaunchResult{}, fmt.Errorf("create log: %w", err)
	}

	// Stderr lands in a sibling file so a hung-on-auth daemon's
	// diagnostic line (e.g. gemini's "Opening authentication page in
	// your browser…" or keychain unreachable) is persisted somewhere
	// the operator can grep, instead of being lost. Splitting the
	// streams also keeps the JSON-RPC frame parser on stdout clean —
	// any non-JSON line on stderr would otherwise break it.
	errLogPath := strings.TrimSuffix(logPath, ".log") + "-err.log"
	errLogFile, err := os.Create(errLogPath)
	if err != nil {
		_ = logFile.Close()
		_ = os.Remove(logPath)
		return M1LaunchResult{}, fmt.Errorf("create err log: %w", err)
	}

	// Bidirectional JSON-RPC trace — what the driver SENT (out) plus
	// what the agent SENT BACK (in), each wrapped as a JSONL record
	// with a UTC timestamp + direction tag. The plain `*.log` only
	// captures gemini's stdout, so without this file we have no record
	// of session/prompt etc. ever leaving host-runner. Operators can
	// `jq` the file or grep `"dir":"out"` to see every outbound frame.
	rpcLogPath := strings.TrimSuffix(logPath, ".log") + "-rpc.jsonl"
	rpcLogFile, err := os.Create(rpcLogPath)
	if err != nil {
		_ = logFile.Close()
		_ = errLogFile.Close()
		_ = os.Remove(logPath)
		_ = os.Remove(errLogPath)
		return M1LaunchResult{}, fmt.Errorf("create rpc log: %w", err)
	}

	stdout, stderr, stdin, kill, err := cfg.Spawner.SpawnWithStderr(ctx, command)
	if err != nil {
		_ = logFile.Close()
		_ = errLogFile.Close()
		_ = rpcLogFile.Close()
		_ = os.Remove(logPath)
		_ = os.Remove(errLogPath)
		_ = os.Remove(rpcLogPath)
		return M1LaunchResult{}, fmt.Errorf("spawn: %w", err)
	}

	// Tee child's stdout into the log file so the cosmetic tail pane
	// renders the same bytes the driver consumes. ACP traffic is raw
	// JSON-RPC, so the pane shows ndjson — useful for debugging but
	// not a polished read.
	teed := io.TeeReader(stdout, logFile)

	// Pump stderr into its sibling log on a background goroutine. The
	// driver doesn't read stderr, so this copy runs until the child
	// closes its stderr (process exit) or the closer fires.
	if stderr != nil {
		go func() {
			_, _ = io.Copy(errLogFile, stderr)
		}()
	}

	paneCmd := fmt.Sprintf("tail -F %s", shellEscape(logPath))
	pane, paneErr := cfg.Launcher.LaunchCmd(ctx, cfg.Spawn, paneCmd)
	if paneErr != nil {
		pane = "" // non-fatal; driver owns the real stream
	}

	closer := func() {
		// Final notice for the cosmetic tail pane before we tear
		// everything down — without this the tmux pane's tail just
		// stops emitting bytes when the engine exits, leaving the
		// operator unsure whether the stop succeeded. Written before
		// kill() so the bytes flush while the process still owns its
		// stdout fds; potential interleaving with engine output is
		// harmless because user-initiated stop happens during idle.
		_, _ = fmt.Fprintf(logFile, "\n[host-runner] M1 stopped at %s\n",
			time.Now().UTC().Format(time.RFC3339))
		kill()
		_ = stdin.Close()
		_ = stdout.Close()
		if stderr != nil {
			_ = stderr.Close()
		}
		_ = logFile.Close()
		_ = errLogFile.Close()
		_ = rpcLogFile.Close()
	}

	// ADR-021 W1.4: resolve the ACP authentication methodId.
	// Precedence: spec override (steward template) → family default
	// (agent_families.yaml) → "" (driver picks first non-interactive
	// or fails with attention_request).
	authMethod := spec.AuthMethod
	if authMethod == "" {
		if fam, ok := agentfamilies.ByName(cfg.Spawn.Kind); ok {
			authMethod = fam.DefaultAuthMethod
		}
	}

	drv := &ACPDriver{
		AgentID: cfg.Spawn.ChildID,
		Poster:  cfg.Client,
		Stdin:   stdin,
		Stdout:  teed,
		RPCLog:  rpcLogFile,
		Closer:  closer,
		// ADR-021 W1.2: when the hub spliced a captured cursor into the
		// rendered spec (handlers_sessions.handleResumeSession), thread
		// it through to ACPDriver so Start can call session/load instead
		// of session/new. Empty for cold-starts; harmless field on the
		// driver in either case.
		ResumeSessionID: spec.ResumeSessionID,
		AuthMethod:      authMethod,
	}
	if err := drv.Start(ctx); err != nil {
		// Start covers initialize + session/new — if either fails the
		// engine doesn't actually speak ACP (or doesn't speak our
		// protocol version). Tear the process down and let the caller
		// fall back to M2.
		_ = stdin.Close()
		_ = stdout.Close()
		if stderr != nil {
			_ = stderr.Close()
		}
		_ = logFile.Close()
		_ = errLogFile.Close()
		_ = rpcLogFile.Close()
		kill()
		return M1LaunchResult{}, fmt.Errorf("acp start: %w", err)
	}

	return M1LaunchResult{PaneID: pane, Driver: drv, LogPath: logPath}, nil
}

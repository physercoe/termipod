// host-runner — bridges the hub to backend CLI processes running on this box.
//
//	host-runner run --hub https://hub.example.com --token <host> --name <hostname>
//
// On first run it registers this host with the hub; subsequent runs pass
// --host-id to skip registration. Launches spawned agents into tmux panes
// on behalf of the hub — not an agent itself (no row in the agents table).
//
// host-runner is also a busybox-style multicall binary: when invoked
// under the basename `hub-mcp-bridge` (typically via a symlink in
// /usr/local/bin), or via the explicit `mcp-bridge` subcommand, it
// runs the stdio↔HTTP shim that claude-code spawns from `.mcp.json`.
// One install covers both roles.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/termipod/hub/internal/hostrunner"
	"github.com/termipod/hub/internal/mcpbridge"
)

func main() {
	// Multicall: the binary routes to a different entry point when
	// invoked under the hub-mcp-bridge basename (typically a symlink
	// at /usr/local/bin). Spawned agents reference the friendly name
	// in .mcp.json so claude-code Just Works without a second install.
	switch filepath.Base(os.Args[0]) {
	case "hub-mcp-bridge":
		// stdio↔HTTP shim → hub /mcp/<token> → in-process MCP catalog
		// (gates, attention, post_excerpt, journal, the orchestrator-
		// worker primitives, AND the rich-authority surface — projects,
		// plans, runs, agents.spawn, schedules, channels, a2a.invoke,
		// … — wired in-process by mcp_authority.go). One bridge entry
		// in .mcp.json reaches everything; no second daemon needed.
		os.Exit(mcpbridge.Run(os.Args[1:]))
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		runDaemon(os.Args[2:])
	case "register":
		runRegister(os.Args[2:])
	case "mcp-bridge":
		os.Exit(mcpbridge.Run(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `host-runner <command> [flags]

Commands:
  register     Register this host with the hub, print host_id.
  run          Run the daemon: heartbeat + poll pending spawns + launch.
  mcp-bridge   stdio↔HTTP shim used by spawned agents (claude-code et al.)
               via .mcp.json. Also reachable by symlinking the binary as
               hub-mcp-bridge for back-compat with older spawn configs.`)
}

func runRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	hub := fs.String("hub", "http://127.0.0.1:8443", "hub base URL")
	token := fs.String("token", "", "bearer token")
	team := fs.String("team", "default", "team id")
	name := fs.String("name", hostname(), "host display name")
	_ = fs.Parse(args)

	if *token == "" {
		die("--token required")
	}
	c := hostrunner.NewClient(*hub, *token, *team)
	id, err := c.RegisterHost(context.Background(), *name, nil)
	if err != nil {
		die("register: " + err.Error())
	}
	fmt.Println(id)
}

func runDaemon(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	hub := fs.String("hub", "http://127.0.0.1:8443", "hub base URL")
	token := fs.String("token", "", "bearer token")
	team := fs.String("team", "default", "team id")
	name := fs.String("name", hostname(), "host display name")
	hostID := fs.String("host-id", "", "known host id (skips registration)")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory for runner state (caches host_id across restarts)")
	launcher := fs.String("launcher", "tmux", "launcher kind: stub|tmux")
	session := fs.String("tmux-session", "hub-agents", "tmux session name (tmux launcher)")
	backendCmd := fs.String("backend-cmd", "", "command to run in each pane (tmux launcher); empty = built-in placeholder")
	a2aAddr := fs.String("a2a-addr", "", "bind address for the A2A server (e.g. :8801); empty disables")
	a2aPublicURL := fs.String("a2a-public-url", "", "base URL advertised in agent-cards; falls back to request Host header")
	egressProxyAddr := fs.String("egress-proxy-addr", hostrunner.DefaultEgressProxyAddr,
		"bind address for the in-process reverse proxy that masks the hub URL from spawned agents; "+
			"agents see http://<this addr>/ in their .mcp.json instead of --hub. "+
			"Empty disables the proxy and .mcp.json carries the real hub URL.")
	trackioDir := fs.String("trackio-dir", "", "trackio root dir (default: $TRACKIO_DIR or ~/.cache/huggingface/trackio); empty disables the metric-digest poller")
	wandbDir := fs.String("wandb-dir", "", "wandb offline-run root dir (contains run-*/files/wandb-history.jsonl); empty disables the wandb metric-digest poller")
	tbDir := fs.String("tb-dir", "", "TensorBoard root logdir; each run's tfevents files live under <tb-dir>/<run-path>. Empty disables the TensorBoard metric-digest poller")
	_ = fs.Parse(args)

	if *token == "" {
		die("--token required")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// One-shot audit at startup: which backend-CLI auth env vars are
	// actually visible to host-runner? Spawned claude/codex/gemini
	// inherit os.Environ() through exec.Command, so what host-runner
	// sees here is what the agent will see. If a value is missing the
	// agent will report "not logged in" — and operators have asked
	// repeatedly how to verify this without instrumenting the spawn.
	auditAuthEnv(log)

	var lnch hostrunner.Launcher
	switch *launcher {
	case "stub":
		lnch = hostrunner.StubLauncher{Log: log}
	case "tmux":
		lnch = hostrunner.NewTmuxLauncher(*session, *backendCmd, log)
	default:
		die("unknown --launcher: " + *launcher)
	}

	r := &hostrunner.Runner{
		Client:          hostrunner.NewClient(*hub, *token, *team),
		HostName:        *name,
		HostID:          *hostID,
		Launcher:        lnch,
		Log:             log,
		StateDir:        *stateDir,
		A2AAddr:         *a2aAddr,
		A2APublicURL:    *a2aPublicURL,
		EgressProxyAddr: *egressProxyAddr,
		TrackioDir:      *trackioDir,
		WandbDir:        *wandbDir,
		TensorBoardDir:  *tbDir,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := r.Start(ctx); err != nil {
		log.Error("host-runner exited", "err", err)
		os.Exit(1)
	}
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "host-unknown"
	}
	return h
}

// defaultStateDir resolves ~/.cache/termipod/host-runner for XDG-style
// caching. Returns "" if HOME is unset — the runner treats an empty
// state-dir as "don't persist," which still works (server UPSERT handles
// the re-register).
func defaultStateDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "termipod", "host-runner")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "termipod", "host-runner")
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// auditAuthEnv logs whether each backend-CLI auth env var is visible
// to host-runner (which is exactly what the spawned agent will inherit).
// Values are NEVER logged — only present/absent — so this is safe to
// leave on by default and to share in bug reports.
//
// Also reports HOME because misconfigured systemd / sudo can leave it
// empty or pointing at root, which silently breaks credential lookup
// for engines that read ~/.claude/credentials.json or ~/.codex/auth.json.
func auditAuthEnv(log *slog.Logger) {
	keys := []string{
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY",
		"GEMINI_API_KEY",
		"GOOGLE_API_KEY",
		"CODEX_HOME",
		"CLAUDE_CONFIG",
	}
	pairs := make([]any, 0, len(keys)*2+2)
	for _, k := range keys {
		state := "absent"
		if os.Getenv(k) != "" {
			state = "present"
		}
		pairs = append(pairs, k, state)
	}
	pairs = append(pairs, "HOME", os.Getenv("HOME"))
	log.Info("auth-env-audit", pairs...)
}

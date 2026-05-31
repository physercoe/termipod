// host-runner — bridges the hub to backend CLI processes running on this box.
//
//	host-runner run --hub https://hub.example.com --token <host> --name <hostname>
//
// On first run it registers this host with the hub; subsequent runs pass
// --host-id to skip registration. Launches spawned agents into tmux panes
// on behalf of the hub — not an agent itself (no row in the agents table).
//
// host-runner is also a busybox-style multicall binary. Two stdio
// shims live alongside the daemon, both invoked by spawned agents
// from their `.mcp.json` files:
//
//   - `hub-mcp-bridge` basename (or `mcp-bridge` subcommand): stdio ↔
//     HTTP shim to the hub via the egress proxy. Used by every spawn
//     for `mcp__termipod__*` tools.
//   - `mcp-uds-stdio` subcommand: stdio ↔ UDS shim to the per-spawn
//     host-runner gateway. Used only by claude-code M4 LocalLogTail
//     spawns for `mcp__termipod-host__hook_*` tools (ADR-027 W5c).
//
// One install covers all roles.
//
// Exit-code contract (ADR-028 D-2). The systemd unit shipped at
// hub/deploy/systemd/termipod-host@.service uses Restart=on-failure,
// which means systemd respawns only on NON-ZERO exits. host-runner's
// callers rely on this split:
//
//   - exit 0   — true shutdown (`host.shutdown` verb). Systemd does
//     NOT respawn. Operator brings the host back manually
//     with `systemctl start termipod-host@<id>`.
//   - exit 75  — bounce (EX_TEMPFAIL). Systemd respawns with whatever
//     binary is now at the install path (used by Phase 2
//     `host.update` and Phase 3 `host.restart`).
//   - exit 1+  — failure path. Systemd respawns the same binary.
//
// Keep these contracts stable: the orchestrator (`hub-server
// shutdown-all`, future `update-all`, `restart-all`) and operator
// docs (docs/how-to/install-host-runner.md) both depend on them.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/termipod/hub/internal/buildinfo"
	"github.com/termipod/hub/internal/hookfire"
	"github.com/termipod/hub/internal/hostrunner"
	"github.com/termipod/hub/internal/hostrunner/trackio"
	"github.com/termipod/hub/internal/mcpbridge"
	"github.com/termipod/hub/internal/mcpudsbridge"
	"github.com/termipod/hub/internal/selfupdate"
	"github.com/termipod/hub/internal/statusfire"
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
	case "mcp-uds-stdio":
		os.Exit(mcpudsbridge.Run(os.Args[2:]))
	case "hook-fire":
		os.Exit(hookfire.Run(os.Args[2:]))
	case "status-fire":
		// ADR-036 W1 — stdin (claude-code statusLine JSON) → UDS
		// gateway `status_line` tool → stdout (one line for claude
		// to render). Best-effort: transport failures degrade to a
		// quiet default line, not a non-zero exit, because the cadence
		// (~10s) makes loud failures intolerable in the TUI.
		os.Exit(statusfire.Run(os.Args[2:]))
	case "self-update":
		runSelfUpdate(os.Args[2:])
	case "doctor":
		runHostDoctor(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	case "-v", "--version", "version":
		printVersion()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func printVersion() {
	fmt.Printf("host-runner %s\n", buildinfo.Version)
	if buildinfo.Commit != "" {
		mod := ""
		if buildinfo.Modified {
			mod = " (dirty)"
		}
		fmt.Printf("  commit: %s%s\n", buildinfo.Commit, mod)
	}
	if buildinfo.BuildTime != "" {
		fmt.Printf("  built:  %s\n", buildinfo.BuildTime)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `host-runner <command> [flags]

Commands:
  register       Register this host with the hub, print host_id.
  run            Run the daemon: heartbeat + poll pending spawns + launch.
  mcp-bridge     stdio↔HTTP shim used by spawned agents (claude-code et al.)
                 via .mcp.json. Also reachable by symlinking the binary as
                 hub-mcp-bridge for back-compat with older spawn configs.
  mcp-uds-stdio  stdio↔UDS shim into the per-spawn host-runner MCP gateway.
                 Used by claude-code M4 LocalLogTail spawns to reach the
                 mcp__termipod-host__hook_* tools (ADR-027). Reads
                 --socket / MCP_UDS_SOCKET.
  hook-fire      One-shot stdin → UDS → stdout bridge for claude-code's
                 settings.local.json hook entries (ADR-027 W6, rebuilt
                 in v1.0.659). Reads the hook event payload from stdin,
                 wraps it as a JSON-RPC tools/call for the corresponding
                 hook_<event> handler on the gateway, writes the
                 response object to stdout. Flags: --socket --event.
  status-fire    One-shot stdin → UDS bridge for claude-code's
                 settings.local.json statusLine entry (ADR-036 W1).
                 Reads the structured statusLine JSON from stdin, posts
                 it to the gateway's status_line tool, prints one line
                 to stdout for claude to render. Best-effort: transport
                 failures degrade quietly so the ~10s cadence doesn't
                 leak errors into the TUI. Flags: --socket [--wrap].
  self-update    Fetch a release from GitHub, verify SHA256, replace this
                 binary, and exit 75 so the supervisor respawns it
                 (ADR-028). Flags: --version / --channel / --upstream-repo
                 / --install-path / --dry-run.
  doctor         Host-side preflight: HOME, hub reachable, token valid,
                 engines on PATH, scratch dir writable. Exits 1 on any
                 red. Flags: --hub / --token / --team / --json.`)
}

// runSelfUpdate fetches a release of host-runner from GitHub, verifies
// it against the release SHA256SUMS, and atomically replaces this
// binary on disk (ADR-028 D-4 / plan W6). On success it exits 75 so
// the systemd supervisor respawns with the new binary; on any failure
// it exits 1 — a generic failure that still respawns the SAME binary,
// so the host never goes dark.
func runSelfUpdate(args []string) {
	fs := flag.NewFlagSet("self-update", flag.ExitOnError)
	version := fs.String("version", "", "explicit release tag to install (e.g. v1.0.634-alpha); overrides --channel")
	channel := fs.String("channel", "stable", "release channel when --version is unset: stable|alpha")
	repo := fs.String("upstream-repo", selfupdate.DefaultRepo, "GitHub owner/name to fetch releases from")
	installPath := fs.String("install-path", "", "file to replace (default: this binary's resolved path)")
	dryRun := fs.Bool("dry-run", false, "resolve and report the target release without downloading or replacing")
	_ = fs.Parse(args)

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	res, err := selfupdate.Run(context.Background(), selfupdate.Options{
		Binary:      "host-runner",
		Repo:        *repo,
		Channel:     *channel,
		Version:     *version,
		InstallPath: *installPath,
		DryRun:      *dryRun,
		Log:         log,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "self-update failed: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		fmt.Printf("self-update (dry run): host-runner %s -> %s [no changes made]\n",
			res.FromVersion, res.ToVersion)
		return
	}
	fmt.Printf("self-update: host-runner %s -> %s installed at %s\n",
		res.FromVersion, res.ToVersion, res.InstallPath)
	fmt.Println("exiting 75 so the supervisor respawns with the new binary")
	os.Exit(75)
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
	// A2A defaults to a loopback auto-pick port so agents on the host are
	// reachable via `a2a.invoke` out of the box. The hub-side card rewrite
	// (handlers_a2a.go::rewriteCardURL) overrides `url` to its public relay
	// regardless of what we bind, so a 127.0.0.1 bind works fine behind NAT.
	// Pass `--a2a-addr=disabled` to suppress (and skip the A2A subsystem
	// entirely — the prior empty-string semantics).
	a2aAddr := fs.String("a2a-addr", "127.0.0.1:0",
		"bind address for the A2A server (e.g. :8801, 127.0.0.1:0 for auto-pick); "+
			"'disabled' suppresses the A2A server + directory publish")
	a2aPublicURL := fs.String("a2a-public-url", "", "base URL advertised in agent-cards; falls back to request Host header")
	egressProxyAddr := fs.String("egress-proxy-addr", hostrunner.DefaultEgressProxyAddr,
		"bind address for the in-process reverse proxy that masks the hub URL from spawned agents; "+
			"agents see http://<this addr>/ in their .mcp.json instead of --hub. "+
			"Empty disables the proxy and .mcp.json carries the real hub URL.")
	trackioDir := fs.String("trackio-dir", "", "trackio root dir; empty falls back to $TRACKIO_DIR then ~/.cache/huggingface/trackio (trackio's own default), so the metric-digest poller is ON by default. Pass --no-trackio to disable.")
	noTrackio := fs.Bool("no-trackio", false, "disable the trackio metric-digest poller even when a default trackio dir resolves")
	wandbDir := fs.String("wandb-dir", "", "wandb offline-run root dir (contains run-*/files/wandb-history.jsonl); empty disables the wandb metric-digest poller")
	tbDir := fs.String("tb-dir", "", "TensorBoard root logdir; each run's tfevents files live under <tb-dir>/<run-path>. Empty disables the TensorBoard metric-digest poller")
	_ = fs.Parse(args)

	// Trackio poller is on by default: when --trackio-dir is unset, fall
	// back to trackio's own default location (matching the documented
	// behaviour and the flag help) instead of silently disabling the
	// poller. Reading a non-existent default dir is a cheap no-op — the
	// reader returns empty until a worker logs — so defaulting on costs
	// nothing on hosts that never run training. --no-trackio is the
	// explicit opt-out. (wandb/TensorBoard stay opt-in: they have no
	// universal default location.)
	if *noTrackio {
		*trackioDir = ""
	} else if *trackioDir == "" {
		*trackioDir = trackio.DefaultDir()
	}

	// v1.0.665 — log level toggle. Default is Info (slog's default for
	// nil opts); set HOSTRUNNER_LOG_LEVEL=debug to see every Debug
	// breadcrumb, including the adapter's per-event "posted" line that
	// confirms claude-code JSONL frames reached the hub. Use "warn" or
	// "error" to quiet a noisy host. Unknown values fall through to
	// Info so a typo doesn't silence the runner entirely.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: resolveLogLevel(os.Getenv("HOSTRUNNER_LOG_LEVEL")),
	}))

	// Prefer a bearer persisted by a prior host.token_rotate verb
	// (ADR-028 W20) over the --token flag — otherwise a restart would
	// re-authenticate with the now-revoked token and the host would go
	// dark. Either source is sufficient; only an empty result is fatal.
	effectiveToken, rotated := hostrunner.ResolveBearerToken(*stateDir, *hub, *team, *name, *token)
	if effectiveToken == "" {
		die("--token required (or a token persisted from a prior rotation)")
	}
	if rotated {
		log.Info("using rotated host token from state dir", "state_dir", *stateDir)
	}

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

	// Translate the explicit opt-out sentinel into the empty string the
	// Runner's existing `if A2AAddr != ""` gate already understands, so
	// the on/off plumbing inside hostrunner stays unchanged.
	resolvedA2AAddr := *a2aAddr
	if resolvedA2AAddr == "disabled" {
		resolvedA2AAddr = ""
		log.Warn("a2a server disabled by --a2a-addr=disabled; " +
			"agents on this host will not be reachable via a2a.invoke")
	} else {
		log.Info("a2a server enabled", "addr", resolvedA2AAddr)
	}

	r := &hostrunner.Runner{
		Client:          hostrunner.NewClient(*hub, effectiveToken, *team),
		HostName:        *name,
		HostID:          *hostID,
		Launcher:        lnch,
		Log:             log,
		StateDir:        *stateDir,
		A2AAddr:         resolvedA2AAddr,
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

// resolveLogLevel maps a HOSTRUNNER_LOG_LEVEL env value to a slog.Level.
// Recognises debug / info / warn / error (case-insensitive). Anything
// else, including empty, returns slog.LevelInfo so a typo never makes
// the host go silent.
func resolveLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
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

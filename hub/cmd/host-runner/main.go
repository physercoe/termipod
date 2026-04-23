// host-runner — bridges the hub to backend CLI processes running on this box.
//
//	host-runner run --hub https://hub.example.com --token <host> --name <hostname>
//
// On first run it registers this host with the hub; subsequent runs pass
// --host-id to skip registration. Launches spawned agents into tmux panes
// on behalf of the hub — not an agent itself (no row in the agents table).
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
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		runDaemon(os.Args[2:])
	case "register":
		runRegister(os.Args[2:])
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
  register   Register this host with the hub, print host_id.
  run        Run the daemon: heartbeat + poll pending spawns + launch.`)
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
	_ = fs.Parse(args)

	if *token == "" {
		die("--token required")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

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
		Client:       hostrunner.NewClient(*hub, *token, *team),
		HostName:     *name,
		HostID:       *hostID,
		Launcher:     lnch,
		Log:          log,
		StateDir:     *stateDir,
		A2AAddr:      *a2aAddr,
		A2APublicURL: *a2aPublicURL,
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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

// adminHostRow mirrors server.AdminHostRow — the Flutter-app rule of
// "no typed classes for hub entities" doesn't bind a Go CLI, but a
// local struct keeps the decode honest.
type adminHostRow struct {
	HostID          string `json:"host_id"`
	TeamID          string `json:"team_id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	Live            bool   `json:"live"`
	LastSeenAt      string `json:"last_seen_at"`
	RunnerCommit    string `json:"runner_commit"`
	RunnerBuildTime string `json:"runner_build_time"`
	Pinged          bool   `json:"pinged"`
	Version         string `json:"version"`
	PingMS          int64  `json:"ping_ms"`
	PingError       string `json:"ping_error"`
}

// runHosts dispatches `hub-server hosts <ls|ping>` (ADR-028 plan W15).
func runHosts(args []string, log *slog.Logger) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hub-server hosts <ls|ping> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "ls":
		runHostsLs(args[1:], log)
	case "ping":
		runHostsPing(args[1:], log)
	default:
		fmt.Fprintf(os.Stderr, "unknown hosts subcommand: %s\n", args[0])
		os.Exit(2)
	}
}

// runHostsLs implements `hub-server hosts ls`: the registered fleet
// with heartbeat liveness and the runner build captured at register
// time. With --ping it also round-trips host.ping at each live host
// to surface the version actually running now.
func runHostsLs(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("hosts ls", flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(), "hub base URL (HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "owner-scope bearer token (HUB_TOKEN env)")
	ping := fs.Bool("ping", false, "round-trip host.ping at each live host to fetch its running version")
	asJSON := fs.Bool("json", false, "emit the host list as JSON")
	_ = fs.Parse(args)
	_ = log

	path := "/v1/admin/hosts"
	if *ping {
		path += "?ping=1"
	}
	var out struct {
		Hosts []adminHostRow `json:"hosts"`
	}
	if err := adminCall(http.MethodGet, *hubURL, path, *token, nil, &out); err != nil {
		fmt.Fprintf(os.Stderr, "hosts ls: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		b, _ := json.MarshalIndent(out.Hosts, "", "  ")
		fmt.Println(string(b))
		return
	}
	if len(out.Hosts) == 0 {
		fmt.Println("hosts ls: no hosts registered.")
		return
	}
	fmt.Printf("%-24s %-14s %-16s %-5s %-9s %s\n",
		"host_id", "team", "name", "live", "commit", "version / last_seen")
	for _, h := range out.Hosts {
		live := "no"
		if h.Live {
			live = "yes"
		}
		tail := h.LastSeenAt
		if h.Pinged {
			switch {
			case h.PingError != "":
				tail = "ping error: " + h.PingError
			case h.Version != "":
				tail = fmt.Sprintf("%s (%dms)", h.Version, h.PingMS)
			}
		}
		fmt.Printf("%-24s %-14s %-16s %-5s %-9s %s\n",
			h.HostID, h.TeamID, h.Name, live, shortCommit(h.RunnerCommit), tail)
	}
}

// runHostsPing implements `hub-server hosts ping <host-id>`: a single
// host.ping round-trip confirming the tunnel works end-to-end and
// reporting the host-runner build on the far side.
func runHostsPing(args []string, log *slog.Logger) {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintln(os.Stderr, "usage: hub-server hosts ping <host-id> [flags]")
		os.Exit(2)
	}
	hostID := args[0]
	fs := flag.NewFlagSet("hosts ping", flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(), "hub base URL (HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "owner-scope bearer token (HUB_TOKEN env)")
	asJSON := fs.Bool("json", false, "emit the ping result as JSON")
	_ = fs.Parse(args[1:])
	_ = log

	var out struct {
		HostID string `json:"host_id"`
		Ping   struct {
			OK        bool   `json:"ok"`
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			BuildTime string `json:"build_time"`
			PingMS    int64  `json:"ping_ms"`
			Error     string `json:"error"`
		} `json:"ping"`
	}
	if err := adminCall(http.MethodPost, *hubURL,
		"/v1/admin/hosts/"+hostID+"/ping", *token, nil, &out); err != nil {
		fmt.Fprintf(os.Stderr, "hosts ping: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		if !out.Ping.OK {
			os.Exit(1)
		}
		return
	}
	if !out.Ping.OK {
		fmt.Fprintf(os.Stderr, "hosts ping: %s did not answer — %s\n", hostID, out.Ping.Error)
		os.Exit(1)
	}
	fmt.Printf("%s — host-runner %s (%dms round-trip)\n",
		hostID, out.Ping.Version, out.Ping.PingMS)
	if out.Ping.Commit != "" {
		fmt.Printf("  commit: %s\n", shortCommit(out.Ping.Commit))
	}
	if out.Ping.BuildTime != "" {
		fmt.Printf("  built:  %s\n", out.Ping.BuildTime)
	}
}

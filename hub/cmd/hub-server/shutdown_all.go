package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// runShutdownAll fires host.shutdown across the fleet — each
// host-runner exits 0 and, per ADR-028 D-2, stays DOWN until the
// operator runs `systemctl start termipod-host@<id>`.
func runShutdownAll(args []string, log *slog.Logger) {
	runFleetStop(args, log, "shutdown-all")
}

// runFleetStop is the shared thin CLI wrapper behind shutdown-all and
// restart-all. The orchestration runs hub-side (it owns the in-memory
// tunnel queue); this binary only authenticates, POSTs to
// /v1/admin/fleet/<verb>, prints the per-host outcome, and exits 0
// unless at least one host errored.
//
// Auth: an owner-scope bearer token is required. Pass via --token or
// the HUB_TOKEN env var. The CLI never reads it from disk to avoid the
// "I shipped my token into git" antipattern.
//
// ADR-028 D-2: hub-server itself stays up either way. shutdown-all
// leaves hosts DOWN; restart-all's exit-75 has systemd respawn them
// with the same binary. Sessions left at status=paused are resumable
// via the existing /v1/teams/{team}/sessions/{id}/resume route.
func runFleetStop(args []string, log *slog.Logger, name string) {
	op := strings.TrimSuffix(name, "-all") // "shutdown" | "restart"
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(),
		"hub base URL (HUB_URL env). Defaults to http://127.0.0.1:8443.")
	token := fs.String("token", os.Getenv("HUB_TOKEN"),
		"owner-scope bearer token (HUB_TOKEN env).")
	noWait := fs.Bool("no-wait", false,
		"skip the 60s per-host ack timeout; fire-and-move-on. Useful when "+
			"a host is unresponsive and you want the rest of the fleet to "+
			"proceed without blocking on it.")
	forceKill := fs.Bool("force-kill", false,
		"SIGKILL each agent's pane instead of SIGTERM+grace. Use when a "+
			"session is stuck and the host-runner terminate handler can't "+
			"clean it up via the normal path.")
	reason := fs.String("reason", "operator-initiated",
		"audit-log reason; appears on session.stop + host."+op+" rows.")
	_ = fs.Parse(args)

	if *token == "" {
		fmt.Fprintf(os.Stderr,
			"%s: an owner-scope bearer is required (pass --token or set HUB_TOKEN).\n", name)
		os.Exit(2)
	}

	body, _ := json.Marshal(map[string]any{
		"no_wait":    *noWait,
		"force_kill": *forceKill,
		"reason":     *reason,
	})
	// Timeout caps the whole orchestration at 5 minutes — 60s ack × N
	// hosts is the dominant cost, with a buffer for the synchronous
	// stopSessionInternal pass per host.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, *hubURL+"/v1/admin/fleet/"+op, bytes.NewReader(body))
	if err != nil {
		log.Error("build request", "err", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error("call hub", "err", err, "url", req.URL.String())
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr,
			"%s: hub returned %d: %s\n", name, resp.StatusCode, string(b))
		os.Exit(1)
	}
	var out struct {
		Hosts []struct {
			HostID          string `json:"host_id"`
			TeamID          string `json:"team_id"`
			HostName        string `json:"host_name"`
			SessionsStopped int    `json:"sessions_stopped"`
			Acked           bool   `json:"acked"`
			Error           string `json:"error"`
		} `json:"hosts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Error("decode response", "err", err)
		os.Exit(1)
	}

	if len(out.Hosts) == 0 {
		fmt.Printf("%s: no live hosts (none heartbeated in the last 5 minutes).\n", name)
		return
	}
	failures := 0
	fmt.Printf("%-24s %-16s %-10s %-6s %s\n",
		"host_id", "team", "sessions", "acked", "error")
	for _, h := range out.Hosts {
		ack := "no"
		if h.Acked {
			ack = "yes"
		}
		if h.Error != "" {
			failures++
		}
		fmt.Printf("%-24s %-16s %-10d %-6s %s\n",
			h.HostID, h.TeamID, h.SessionsStopped, ack, h.Error)
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr,
			"%s: %d host(s) errored — sessions are paused (resumable) but the "+
				"host.%s verb may not have landed; check journald.\n",
			name, failures, op)
		os.Exit(1)
	}
}

// defaultHubURL prefers HUB_URL; falls back to the default --listen
// from `hub-server serve`. Keep this in sync with runServe's default.
func defaultHubURL() string {
	if v := os.Getenv("HUB_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8443"
}

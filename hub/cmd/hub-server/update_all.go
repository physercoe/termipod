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
	"time"
)

// runUpdateAll is the thin CLI wrapper around
// POST /v1/admin/fleet/update. The orchestration runs hub-side; this
// binary authenticates, POSTs, prints the per-host outcome, and exits
// 0 unless at least one host errored.
//
// --target selects the scope: "hosts" fans host.update across the
// fleet; "hub" bounces only the hub daemon; "both" (default) does the
// hosts then the hub. The hub self-update runs asynchronously inside
// the daemon (it must replace its own binary and exit 75), so this
// command reports the host outcomes plus a note about the hub —
// confirm the hub with `hub-server version` once it has respawned.
func runUpdateAll(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("update-all", flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(),
		"hub base URL (HUB_URL env). Defaults to http://127.0.0.1:8443.")
	token := fs.String("token", os.Getenv("HUB_TOKEN"),
		"owner-scope bearer token (HUB_TOKEN env).")
	target := fs.String("target", "both",
		"update scope: hosts | hub | both.")
	version := fs.String("version", "",
		"explicit release tag to install fleet-wide; overrides --channel.")
	channel := fs.String("channel", "stable",
		"release channel when --version is unset: stable | alpha.")
	upstreamRepo := fs.String("upstream-repo", "",
		"GitHub owner/name to fetch releases from (default: physercoe/termipod).")
	dryRun := fs.Bool("dry-run", false,
		"report what would be updated without firing any verb or replacing a binary.")
	reason := fs.String("reason", "operator-initiated",
		"audit-log reason; appears on host.update rows.")
	_ = fs.Parse(args)

	if *token == "" {
		fmt.Fprintln(os.Stderr,
			"update-all: an owner-scope bearer is required (pass --token or set HUB_TOKEN).")
		os.Exit(2)
	}
	switch *target {
	case "hosts", "hub", "both":
	default:
		fmt.Fprintln(os.Stderr, "update-all: --target must be hosts|hub|both.")
		os.Exit(2)
	}

	body, _ := json.Marshal(map[string]any{
		"target":        *target,
		"version":       *version,
		"channel":       *channel,
		"upstream_repo": *upstreamRepo,
		"dry_run":       *dryRun,
		"reason":        *reason,
	})
	// host.update blocks per host for the length of a download; budget
	// generously so a slow link doesn't cut the orchestration short.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost, *hubURL+"/v1/admin/fleet/update", bytes.NewReader(body))
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
			"update-all: hub returned %d: %s\n", resp.StatusCode, string(b))
		os.Exit(1)
	}
	var out struct {
		Target string `json:"target"`
		DryRun bool   `json:"dry_run"`
		Hosts  []struct {
			HostID      string `json:"host_id"`
			TeamID      string `json:"team_id"`
			FromVersion string `json:"from_version"`
			ToVersion   string `json:"to_version"`
			Acked       bool   `json:"acked"`
			WouldUpdate bool   `json:"would_update"`
			Error       string `json:"error"`
		} `json:"hosts"`
		HubNote string `json:"hub_note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Error("decode response", "err", err)
		os.Exit(1)
	}

	failures := 0
	if len(out.Hosts) == 0 && (*target == "hosts" || *target == "both") {
		fmt.Println("update-all: no live hosts (none heartbeated in the last 5 minutes).")
	} else if len(out.Hosts) > 0 {
		fmt.Printf("%-24s %-16s %-14s %-14s %-6s %s\n",
			"host_id", "team", "from", "to", "acked", "error")
		for _, h := range out.Hosts {
			ack := "no"
			if h.Acked {
				ack = "yes"
			}
			if h.WouldUpdate {
				ack = "would"
			}
			if h.Error != "" {
				failures++
			}
			fmt.Printf("%-24s %-16s %-14s %-14s %-6s %s\n",
				h.HostID, h.TeamID, dashIfEmpty(h.FromVersion), dashIfEmpty(h.ToVersion), ack, h.Error)
		}
	}
	if out.HubNote != "" {
		fmt.Println("hub: " + out.HubNote)
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr,
			"update-all: %d host(s) errored — check journald; the hub bounce was skipped.\n",
			failures)
		os.Exit(1)
	}
}

// dashIfEmpty renders an empty version cell as "-" for table alignment.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

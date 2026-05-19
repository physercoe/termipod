package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/termipod/hub/internal/buildinfo"
)

// runVersion implements `hub-server version` (ADR-028 plan W14): report
// the release tag plus the git revision and build time embedded by the
// Go toolchain. With --remote it also fans the read-side host.ping verb
// across the live fleet (via GET /v1/admin/hosts?ping=1) so the
// operator sees, in one shot, whether every host matches the hub.
func runVersion(args []string) {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the build info as JSON")
	remote := fs.Bool("remote", false, "also report the version each live host-runner is running")
	hubURL := fs.String("hub-url", defaultHubURL(), "hub base URL when --remote (HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "owner-scope bearer when --remote (HUB_TOKEN env)")
	_ = fs.Parse(args)

	hub := map[string]any{"version": buildinfo.Version}
	if buildinfo.Commit != "" {
		hub["commit"] = buildinfo.Commit
		hub["modified"] = buildinfo.Modified
	}
	if buildinfo.BuildTime != "" {
		hub["build_time"] = buildinfo.BuildTime
	}

	var hosts []adminHostRow
	if *remote {
		var out struct {
			Hosts []adminHostRow `json:"hosts"`
		}
		if err := adminCall(http.MethodGet, *hubURL,
			"/v1/admin/hosts?ping=1", *token, nil, &out); err != nil {
			fmt.Fprintf(os.Stderr, "version --remote: %v\n", err)
			os.Exit(1)
		}
		hosts = out.Hosts
	}

	if *asJSON {
		payload := map[string]any{"hub": hub}
		if *remote {
			payload["hosts"] = hosts
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Println(string(b))
		return
	}

	fmt.Printf("hub-server %s\n", buildinfo.Version)
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
	if !*remote {
		return
	}

	fmt.Println()
	if len(hosts) == 0 {
		fmt.Println("fleet: no hosts registered.")
		return
	}
	drift := 0
	fmt.Printf("%-24s %-14s %-5s %s\n", "host_id", "team", "live", "version")
	for _, h := range hosts {
		live := "no"
		if h.Live {
			live = "yes"
		}
		ver := h.Version
		switch {
		case !h.Live:
			ver = "(offline — last commit " + shortCommit(h.RunnerCommit) + ")"
		case h.PingError != "":
			ver = "ping error: " + h.PingError
		case ver != "" && ver != buildinfo.Version:
			ver += "  ← drift"
			drift++
		}
		fmt.Printf("%-24s %-14s %-5s %s\n", h.HostID, h.TeamID, live, ver)
	}
	if drift > 0 {
		fmt.Fprintf(os.Stderr,
			"\nversion --remote: %d host(s) differ from the hub (%s) — "+
				"consider `hub-server update-all`\n", drift, buildinfo.Version)
		os.Exit(1)
	}
}

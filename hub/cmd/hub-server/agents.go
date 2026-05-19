package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// adminAgentRow mirrors server.AdminAgentRow for the CLI decode.
type adminAgentRow struct {
	AgentID string `json:"agent_id"`
	TeamID  string `json:"team_id"`
	Handle  string `json:"handle"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	HostID  string `json:"host_id"`
}

// runAgents dispatches `hub-server agents <ls|kill>` (ADR-028 plan W17).
func runAgents(args []string, log *slog.Logger) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: hub-server agents <ls|kill> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "ls":
		runAgentsLs(args[1:], log)
	case "kill":
		runAgentsKill(args[1:], log)
	default:
		fmt.Fprintf(os.Stderr, "unknown agents subcommand: %s\n", args[0])
		os.Exit(2)
	}
}

// runAgentsLs lists live agents across the fleet; --all includes the
// terminal ones.
func runAgentsLs(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("agents ls", flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(), "hub base URL (HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "owner-scope bearer token (HUB_TOKEN env)")
	all := fs.Bool("all", false, "include terminated / crashed / failed / archived agents")
	asJSON := fs.Bool("json", false, "emit the agent list as JSON")
	_ = fs.Parse(args)
	_ = log

	path := "/v1/admin/agents"
	if *all {
		path += "?all=1"
	}
	var out struct {
		Agents []adminAgentRow `json:"agents"`
	}
	if err := adminCall(http.MethodGet, *hubURL, path, *token, nil, &out); err != nil {
		fmt.Fprintf(os.Stderr, "agents ls: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(out.Agents, "", "  ")
		fmt.Println(string(b))
		return
	}
	if len(out.Agents) == 0 {
		fmt.Println("agents ls: no agents.")
		return
	}
	fmt.Printf("%-24s %-14s %-18s %-14s %-11s %s\n",
		"agent_id", "team", "handle", "kind", "status", "host_id")
	for _, a := range out.Agents {
		fmt.Printf("%-24s %-14s %-18s %-14s %-11s %s\n",
			a.AgentID, a.TeamID, a.Handle, a.Kind, a.Status, a.HostID)
	}
}

// runAgentsKill terminates one agent (`kill <id>`) or every live agent
// (`kill --all`). Each kill is a separate owner-authenticated POST so
// the per-agent audit trail matches a mobile-driven stop.
func runAgentsKill(args []string, log *slog.Logger) {
	// Accept the agent id as a leading positional so `kill <id> --json`
	// parses; flag.Parse would otherwise stop at the first non-flag.
	var posID string
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		posID, flagArgs = args[0], args[1:]
	}
	fs := flag.NewFlagSet("agents kill", flag.ExitOnError)
	hubURL := fs.String("hub-url", defaultHubURL(), "hub base URL (HUB_URL env)")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "owner-scope bearer token (HUB_TOKEN env)")
	all := fs.Bool("all", false, "kill every live agent across the fleet")
	asJSON := fs.Bool("json", false, "emit the result as JSON")
	_ = fs.Parse(flagArgs)
	_ = log

	var targets []string
	switch {
	case *all && posID != "":
		fmt.Fprintln(os.Stderr, "agents kill: pass either --all or an agent id, not both")
		os.Exit(2)
	case *all:
		var out struct {
			Agents []adminAgentRow `json:"agents"`
		}
		if err := adminCall(http.MethodGet, *hubURL, "/v1/admin/agents", *token, nil, &out); err != nil {
			fmt.Fprintf(os.Stderr, "agents kill --all: %v\n", err)
			os.Exit(1)
		}
		for _, a := range out.Agents {
			targets = append(targets, a.AgentID)
		}
	case posID != "":
		targets = []string{posID}
	default:
		fmt.Fprintln(os.Stderr, "usage: hub-server agents kill <agent-id> | --all  [flags]")
		os.Exit(2)
	}
	if len(targets) == 0 {
		fmt.Println("agents kill: no live agents to kill.")
		return
	}

	type killResult struct {
		AgentID string `json:"agent_id"`
		Handle  string `json:"handle"`
		Killed  bool   `json:"killed"`
		Already string `json:"already,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	results := make([]killResult, 0, len(targets))
	failures := 0
	for _, id := range targets {
		kr := killResult{AgentID: id}
		var resp struct {
			Handle  string `json:"handle"`
			Killed  bool   `json:"killed"`
			Already string `json:"already"`
		}
		if err := adminCall(http.MethodPost, *hubURL,
			"/v1/admin/agents/"+id+"/kill", *token, nil, &resp); err != nil {
			kr.Error = err.Error()
			failures++
		} else {
			kr.Handle, kr.Killed, kr.Already = resp.Handle, resp.Killed, resp.Already
		}
		results = append(results, kr)
	}

	if *asJSON {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(b))
	} else {
		for _, r := range results {
			switch {
			case r.Error != "":
				fmt.Printf("  %s — error: %s\n", r.AgentID, r.Error)
			case r.Killed:
				fmt.Printf("  %s (%s) — killed\n", r.AgentID, r.Handle)
			default:
				fmt.Printf("  %s (%s) — already %s, skipped\n", r.AgentID, r.Handle, r.Already)
			}
		}
	}
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "agents kill: %d of %d failed\n", failures, len(targets))
		os.Exit(1)
	}
}

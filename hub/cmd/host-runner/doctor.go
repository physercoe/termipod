package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/termipod/hub/internal/hostrunner"
)

// hostCheck is one `host-runner doctor` preflight result. OK=false rows
// carry a Hint with the remediation step.
type hostCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// runHostDoctor implements `host-runner doctor` (ADR-028 plan W21): a
// host-side preflight an operator runs before `host-runner run` to catch
// the misconfigurations that otherwise surface as a dead agent hours
// later — missing engine binaries, an unreachable hub, a stale token, an
// empty HOME that breaks credential lookup.
//
// It prints green/red per check with a remediation hint and exits 1 if
// any check is red, so it composes into CI / provisioning scripts.
func runHostDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	hub := fs.String("hub", "http://127.0.0.1:8443", "hub base URL to probe")
	token := fs.String("token", os.Getenv("HUB_TOKEN"), "host bearer token (HUB_TOKEN env)")
	team := fs.String("team", "default", "team id the host is registered in")
	asJSON := fs.Bool("json", false, "emit the check results as a JSON array")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	checks := []hostCheck{
		checkHomeSet(),
		checkHubReachable(ctx, *hub),
		checkHostToken(ctx, *hub, *team, *token),
		checkEngines(ctx),
		checkScratchWritable(),
	}
	emitHostChecks("host-runner doctor", checks, *asJSON)
}

// checkHomeSet verifies HOME is populated. A misconfigured systemd / sudo
// invocation can leave it empty, which silently breaks credential lookup
// for engines that read ~/.claude/credentials.json or ~/.codex/auth.json.
func checkHomeSet() hostCheck {
	const name = "HOME set"
	if h := os.Getenv("HOME"); h != "" {
		return hostCheck{Name: name, OK: true, Detail: h}
	}
	return hostCheck{Name: name, OK: false, Detail: "HOME is empty",
		Hint: "set HOME in the systemd unit — engines read ~/.claude and ~/.codex for credentials"}
}

// checkHubReachable confirms the hub answers /v1/_info at the given URL.
func checkHubReachable(ctx context.Context, hub string) hostCheck {
	const name = "hub reachable"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hub+"/v1/_info", nil)
	if err != nil {
		return hostCheck{Name: name, OK: false, Detail: err.Error()}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return hostCheck{Name: name, OK: false, Detail: err.Error(),
			Hint: "is the hub running and is --hub correct? (" + hub + ")"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return hostCheck{Name: name, OK: false,
			Detail: fmt.Sprintf("GET /v1/_info returned %d", resp.StatusCode),
			Hint:   "the URL answers but is not a hub — check --hub"}
	}
	return hostCheck{Name: name, OK: true, Detail: hub}
}

// checkHostToken probes an authenticated endpoint to confirm the bearer
// is accepted by the hub for this team.
func checkHostToken(ctx context.Context, hub, team, token string) hostCheck {
	const name = "host token valid"
	if token == "" {
		return hostCheck{Name: name, OK: false, Detail: "no token provided",
			Hint: "pass --token or set HUB_TOKEN to a host-scope bearer"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		hub+"/v1/teams/"+team+"/hosts/", nil)
	if err != nil {
		return hostCheck{Name: name, OK: false, Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return hostCheck{Name: name, OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return hostCheck{Name: name, OK: true, Detail: "accepted in team " + team}
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return hostCheck{Name: name, OK: false,
			Detail: fmt.Sprintf("hub rejected the token (%d)", resp.StatusCode),
			Hint:   "reissue with `hub-server tokens issue --kind host`"}
	default:
		return hostCheck{Name: name, OK: false,
			Detail: fmt.Sprintf("unexpected status %d", resp.StatusCode)}
	}
}

// checkEngines probes the embedded agent-family registry for installed
// engine CLIs. A host with no engine on PATH can register but never
// spawn anything, so zero-present is the failure condition.
func checkEngines(ctx context.Context) hostCheck {
	const name = "engines on PATH"
	caps := hostrunner.ProbeCapabilities(ctx)
	var present, absent []string
	for fam, c := range caps.Agents {
		if c.Installed {
			present = append(present, fam)
		} else {
			absent = append(absent, fam)
		}
	}
	sort.Strings(present)
	sort.Strings(absent)
	if len(present) == 0 {
		return hostCheck{Name: name, OK: false, Detail: "no engine CLI found on PATH",
			Hint: "install at least one of claude / codex / gemini / kimi"}
	}
	detail := "present: " + strings.Join(present, ", ")
	if len(absent) > 0 {
		detail += "; absent: " + strings.Join(absent, ", ")
	}
	return hostCheck{Name: name, OK: true, Detail: detail}
}

// checkScratchWritable confirms host-runner can create per-task workdirs
// under ~/hub-work — the launcher fallback root for project-bound spawns.
func checkScratchWritable() hostCheck {
	const name = "scratch dir writable"
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return hostCheck{Name: name, OK: false, Detail: "cannot resolve home directory",
			Hint: "set HOME so per-task workdirs have a root"}
	}
	dir := filepath.Join(home, "hub-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return hostCheck{Name: name, OK: false, Detail: err.Error(),
			Hint: "host-runner writes per-task workdirs under " + dir}
	}
	probe := filepath.Join(dir, ".doctor-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return hostCheck{Name: name, OK: false, Detail: err.Error()}
	}
	_ = os.Remove(probe)
	return hostCheck{Name: name, OK: true, Detail: dir}
}

// emitHostChecks renders the check list (plain text or JSON) and exits 1
// when any check is red.
func emitHostChecks(title string, checks []hostCheck, asJSON bool) {
	failed := 0
	for _, c := range checks {
		if !c.OK {
			failed++
		}
	}
	if asJSON {
		b, _ := json.MarshalIndent(checks, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("%s\n\n", title)
		for _, c := range checks {
			mark := "PASS"
			if !c.OK {
				mark = "FAIL"
			}
			line := fmt.Sprintf("  [%s] %s", mark, c.Name)
			if c.Detail != "" {
				line += " — " + c.Detail
			}
			fmt.Println(line)
			if !c.OK && c.Hint != "" {
				fmt.Printf("         hint: %s\n", c.Hint)
			}
		}
		fmt.Println()
		if failed == 0 {
			fmt.Printf("doctor: all %d check(s) passed\n", len(checks))
		} else {
			fmt.Fprintf(os.Stderr, "doctor: %d of %d check(s) failed\n", failed, len(checks))
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

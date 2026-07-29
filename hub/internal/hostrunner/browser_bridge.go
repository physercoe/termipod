package hostrunner

// Browser-bridge MCP injection (docs/plans/desktop-agent-browser-bridge.md,
// W1+W2). When the TermiPod desktop's "agent browser bridge" toggle is on, the
// Electron main process runs a loopback MCP server and publishes
// ~/.termipod/browser-bridge.json. At spawn time we ADD a second, optional
// mcpServers entry — `termipod-browser`, a stdio relay (node
// browser_bridge_stdio.mjs) — beside the hub entry so agents on THIS host
// get the bridge's browser_* tools. The file's presence + a live pid is the
// same-host proof: a remote host has no file, so nothing is injected there.
// The token rides env (never argv), same rule as the hub bridge.
//
// W2 scope split: the discovery file carries TWO per-run tokens. Every spawn
// gets the READ token (TP_BROWSER_SCOPE=read); a spawn whose spec sets
// `browser_bridge: true` gets the ACTION token (TP_BROWSER_SCOPE=action),
// unlocking browser_navigate/click/type/… desktop-side. Either way the
// spawn's agent id rides TP_BROWSER_AGENT_ID → the relay forwards it as
// x-tp-agent-id so the desktop's audit trail attributes every action call
// to the calling agent (hub agent_events mirror).

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

// browserBridgeDiscovery mirrors the JSON the desktop writes at
// ~/.termipod/browser-bridge.json (desktop/electron/src/browserbridge.ts).
// ActionToken is empty on discovery files written by a W1-era desktop —
// treated as read-only (opted-in spawns degrade to read scope).
type browserBridgeDiscovery struct {
	URL         string `json:"url"`
	Token       string `json:"token"`
	ActionToken string `json:"action_token"`
	PID         int    `json:"pid"`
	StartedAt   string `json:"started_at"`
	AppVersion  string `json:"app_version"`
	BridgePath  string `json:"bridge_path"`
}

// browserBridgeRequest is one spawn's bridge injection intent: whether the
// spec opted into action scope (`browser_bridge: true`) and the agent id the
// desktop's audit trail should attribute action calls to. The zero value is
// the W1 behavior: read scope, unknown agent.
type browserBridgeRequest struct {
	optIn   bool
	agentID string
}

// resolve picks the token/scope pair for this spawn against the validated
// discovery. The action token is handed out only when the spawn opted in AND
// the desktop wrote one (a W1-era discovery file has none — degrade to read
// with a log note, never fail the spawn).
func (r browserBridgeRequest) resolve(d *browserBridgeDiscovery) (token, scope string) {
	if r.optIn {
		if d.ActionToken != "" {
			return d.ActionToken, "action"
		}
		slog.Info("browser bridge: spawn opted into action scope but discovery has no action_token (W1-era desktop?); injecting read scope")
	}
	return d.Token, "read"
}

// browserBridgeDiscoveryPath is the well-known location both sides share.
func browserBridgeDiscoveryPath(home string) string {
	return filepath.Join(home, ".termipod", "browser-bridge.json")
}

// pidAlive reports whether pid refers to a live process. Unix: signal 0
// (EPERM still means "alive", just owned by someone else). Windows: a dead
// pid fails OpenProcess inside FindProcess, so reaching the GOOS check at
// all means the pid exists — Signal(0) isn't supported there.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// readBrowserBridgeDiscovery returns the live local bridge's discovery info,
// or nil when the bridge isn't running on this host: no file, malformed
// contents, a dead pid (app crashed without cleanup — the stale file is
// removed so we don't stat it on every spawn), a non-loopback URL, or a
// bridge_path that no longer exists (app moved/updated).
func readBrowserBridgeDiscovery(home string) *browserBridgeDiscovery {
	if home == "" {
		return nil
	}
	p := browserBridgeDiscoveryPath(home)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var d browserBridgeDiscovery
	if err := json.Unmarshal(data, &d); err != nil {
		return nil
	}
	if d.URL == "" || d.Token == "" || d.BridgePath == "" || d.PID <= 0 {
		return nil
	}
	// The desktop only ever binds loopback; refuse anything else so a
	// malformed (or planted) file can't aim agent traffic (and the bearer
	// token) at a network peer. PARSED, not prefix-matched: in
	// "http://127.0.0.1:8080@evil.com/mcp" everything before the '@' is
	// userinfo and the real host is evil.com — a prefix check passes it.
	// Userinfo is refused outright; the desktop never writes one.
	u, err := url.Parse(d.URL)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return nil
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return nil
	}
	if !pidAlive(d.PID) {
		_ = os.Remove(p)
		return nil
	}
	if _, err := os.Stat(d.BridgePath); err != nil {
		return nil
	}
	return &d
}

// browserBridgeDiscoveryForUser resolves the current user's discovery file.
func browserBridgeDiscoveryForUser() *browserBridgeDiscovery {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return readBrowserBridgeDiscovery(home)
}

// browserBridgeAvailable returns the validated discovery when the desktop
// bridge is live on this host AND node is on PATH to run the stdio relay
// (the relay is a plain-node script; codex being a native binary means node
// is NOT implied by the fleet). nil otherwise. Skipping is never a failure
// — the spawn proceeds without browser tools, with a note in the runner log.
func browserBridgeAvailable() *browserBridgeDiscovery {
	d := browserBridgeDiscoveryForUser()
	if d == nil {
		return nil
	}
	if _, err := exec.LookPath("node"); err != nil {
		slog.Info("browser bridge: discovery present but node not on PATH; skipping MCP injection")
		return nil
	}
	return d
}

// browserBridgeMCPServer returns the additive `termipod-browser` mcpServers
// entry for the JSON-shaped engine configs (claude/kimi-code-ts/gemini), or
// nil when browserBridgeAvailable is nil. The token rides env (never argv),
// same rule as the hub bridge. An optional browserBridgeRequest carries the
// spawn's action-scope opt-in and agent id (W2); omitted = read scope,
// anonymous agent.
func browserBridgeMCPServer(req ...browserBridgeRequest) map[string]any {
	d := browserBridgeAvailable()
	if d == nil {
		return nil
	}
	var r browserBridgeRequest
	if len(req) > 0 {
		r = req[0]
	}
	token, scope := r.resolve(d)
	return map[string]any{
		"command": "node",
		"args":    []string{d.BridgePath},
		"env": map[string]string{
			"TP_BROWSER_URL":      d.URL,
			"TP_BROWSER_TOKEN":    token,
			"TP_BROWSER_SCOPE":    scope,
			"TP_BROWSER_AGENT_ID": r.agentID,
		},
	}
}

// codexBrowserBridgeTOML appends the codex TOML stanzas for the bridge:
//
//	[mcp_servers.termipod-browser]
//	command = "node"
//	args = ["<bridge_path>"]
//
//	[mcp_servers.termipod-browser.env]
//	TP_BROWSER_URL = "<url>"
//	TP_BROWSER_TOKEN = "<token>"
//	TP_BROWSER_SCOPE = "read"
//	TP_BROWSER_AGENT_ID = "<agent>"
//
// "termipod-browser" is a legal TOML bare key (dashes allowed), matching the
// JSON families' server name. Token in env, never argv.
func codexBrowserBridgeTOML(d *browserBridgeDiscovery, req ...browserBridgeRequest) string {
	var r browserBridgeRequest
	if len(req) > 0 {
		r = req[0]
	}
	token, scope := r.resolve(d)
	return "" +
		"\n[mcp_servers.termipod-browser]\n" +
		"command = " + tomlString("node") + "\n" +
		"args = [" + tomlString(d.BridgePath) + "]\n" +
		"\n[mcp_servers.termipod-browser.env]\n" +
		"TP_BROWSER_URL = " + tomlString(d.URL) + "\n" +
		"TP_BROWSER_TOKEN = " + tomlString(token) + "\n" +
		"TP_BROWSER_SCOPE = " + tomlString(scope) + "\n" +
		"TP_BROWSER_AGENT_ID = " + tomlString(r.agentID) + "\n"
}

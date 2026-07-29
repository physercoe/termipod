package hostrunner

// Browser-bridge MCP injection (docs/plans/desktop-agent-browser-bridge.md,
// W1). When the TermiPod desktop's "agent browser bridge" toggle is on, the
// Electron main process runs a loopback MCP server and publishes
// ~/.termipod/browser-bridge.json. At spawn time we ADD a second, optional
// mcpServers entry — `termipod-browser`, a stdio relay (node
// browser_bridge_stdio.mjs) — beside the hub entry so agents on THIS host
// get the bridge's read-only browser_* tools. The file's presence + a live
// pid is the same-host proof: a remote host has no file, so nothing is
// injected there. The token rides env (never argv), same rule as the hub
// bridge. W1 injects read scope only (TP_BROWSER_SCOPE=read; action tools
// are W2 behind a per-spawn opt-in).

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// browserBridgeDiscovery mirrors the JSON the desktop writes at
// ~/.termipod/browser-bridge.json (desktop/electron/src/browserbridge.ts).
type browserBridgeDiscovery struct {
	URL        string `json:"url"`
	Token      string `json:"token"`
	PID        int    `json:"pid"`
	StartedAt  string `json:"started_at"`
	AppVersion string `json:"app_version"`
	BridgePath string `json:"bridge_path"`
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
	// malformed (or planted) file can't aim agent traffic at a network peer.
	if !strings.HasPrefix(d.URL, "http://127.0.0.1:") && !strings.HasPrefix(d.URL, "http://localhost:") {
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
// same rule as the hub bridge.
func browserBridgeMCPServer() map[string]any {
	d := browserBridgeAvailable()
	if d == nil {
		return nil
	}
	return map[string]any{
		"command": "node",
		"args":    []string{d.BridgePath},
		"env": map[string]string{
			"TP_BROWSER_URL":   d.URL,
			"TP_BROWSER_TOKEN": d.Token,
			"TP_BROWSER_SCOPE": "read",
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
//
// "termipod-browser" is a legal TOML bare key (dashes allowed), matching the
// JSON families' server name. Token in env, never argv.
func codexBrowserBridgeTOML(d *browserBridgeDiscovery) string {
	return "" +
		"\n[mcp_servers.termipod-browser]\n" +
		"command = " + tomlString("node") + "\n" +
		"args = [" + tomlString(d.BridgePath) + "]\n" +
		"\n[mcp_servers.termipod-browser.env]\n" +
		"TP_BROWSER_URL = " + tomlString(d.URL) + "\n" +
		"TP_BROWSER_TOKEN = " + tomlString(d.Token) + "\n" +
		"TP_BROWSER_SCOPE = " + tomlString("read") + "\n"
}

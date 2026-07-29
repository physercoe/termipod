package hostrunner

// Tests for the browser-bridge discovery reader + MCP injection (plan W1).
// The matrix that matters: discovery present/absent/stale × node on/off PATH
// × engine family (claude JSON / kimi-code-ts JSON / gemini JSON / codex
// TOML) — and that the token always rides env, never argv.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupBridgeHome lays down ~/.termipod/browser-bridge.json under a temp
// home pointing at a real (fake-content) stdio relay file, plus a `node`
// shim on PATH when withNode. Returns the home and the discovery written.
func setupBridgeHome(t *testing.T, withNode bool) (string, browserBridgeDiscovery) {
	t.Helper()
	home := t.TempDir()
	script := filepath.Join(home, "browser_bridge_stdio.mjs")
	if err := os.WriteFile(script, []byte("// fake relay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := browserBridgeDiscovery{
		URL:        "http://127.0.0.1:47321/mcp",
		Token:      "tok-browser-test",
		PID:        os.Getpid(), // this test process — alive by construction
		StartedAt:  "2026-07-29T00:00:00Z",
		AppVersion: "0.0.0-test",
		BridgePath: script,
	}
	writeDiscovery(t, home, d)

	binDir := t.TempDir()
	if withNode {
		name := "node"
		if runtime.GOOS == "windows" {
			name = "node.exe"
		}
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("rem shim\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", home)
	return home, d
}

func writeDiscovery(t *testing.T, home string, d browserBridgeDiscovery) {
	t.Helper()
	dir := filepath.Join(home, ".termipod")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "browser-bridge.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadBrowserBridgeDiscovery(t *testing.T) {
	t.Run("absent file", func(t *testing.T) {
		if got := readBrowserBridgeDiscovery(t.TempDir()); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})

	t.Run("valid + live pid", func(t *testing.T) {
		home, want := setupBridgeHome(t, false)
		got := readBrowserBridgeDiscovery(home)
		if got == nil {
			t.Fatal("want discovery, got nil")
		}
		if got.URL != want.URL || got.Token != want.Token || got.BridgePath != want.BridgePath {
			t.Errorf("fields mismatch: got %+v want %+v", got, want)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		home := t.TempDir()
		dir := filepath.Join(home, ".termipod")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "browser-bridge.json"), []byte("{nope"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readBrowserBridgeDiscovery(home); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})

	t.Run("non-loopback URL refused", func(t *testing.T) {
		home, d := setupBridgeHome(t, false)
		d.URL = "http://192.0.2.10:47321/mcp" // a network peer — never inject
		writeDiscovery(t, home, d)
		if got := readBrowserBridgeDiscovery(home); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})

	t.Run("dead pid is stale: nil + file removed", func(t *testing.T) {
		home, d := setupBridgeHome(t, false)
		d.PID = 1<<31 - 2 // beyond every platform's pid space
		writeDiscovery(t, home, d)
		if got := readBrowserBridgeDiscovery(home); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
		if _, err := os.Stat(browserBridgeDiscoveryPath(home)); !os.IsNotExist(err) {
			t.Errorf("stale discovery file should be removed, stat err = %v", err)
		}
	})

	t.Run("missing bridge script", func(t *testing.T) {
		home, d := setupBridgeHome(t, false)
		d.BridgePath = filepath.Join(home, "gone.mjs")
		writeDiscovery(t, home, d)
		if got := readBrowserBridgeDiscovery(home); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})
}

// familyConfigPath is where each family's MCP config lands under the workdir.
func familyConfigPath(t *testing.T, family, workdir string) string {
	t.Helper()
	switch family {
	case "kimi-code-ts":
		return filepath.Join(workdir, ".kimi-code", "mcp.json")
	case "gemini-cli":
		return filepath.Join(workdir, ".gemini", "settings.json")
	case "codex":
		return filepath.Join(workdir, ".codex", "config.toml")
	default: // claude-code + fallthrough
		return filepath.Join(workdir, ".mcp.json")
	}
}

func TestBrowserBridgeInjectionMatrix(t *testing.T) {
	families := []string{"claude-code", "kimi-code-ts", "gemini-cli", "codex"}

	t.Run("injected for all families when bridge live + node present", func(t *testing.T) {
		for _, family := range families {
			t.Run(family, func(t *testing.T) {
				_, d := setupBridgeHome(t, true)
				workdir := t.TempDir()
				if err := writeMCPConfigForFamily(family, workdir, "https://hub.example/", "tok-hub"); err != nil {
					t.Fatalf("writeMCPConfigForFamily: %v", err)
				}
				body, err := os.ReadFile(familyConfigPath(t, family, workdir))
				if err != nil {
					t.Fatal(err)
				}
				if family == "codex" {
					text := string(body)
					if !strings.Contains(text, "[mcp_servers.termipod-browser]") {
						t.Errorf("codex config missing termipod-browser stanza:\n%s", text)
					}
					if !strings.Contains(text, "TP_BROWSER_TOKEN") || !strings.Contains(text, d.BridgePath) {
						t.Errorf("codex config missing env token or bridge path:\n%s", text)
					}
					return
				}
				var parsed map[string]any
				if err := json.Unmarshal(body, &parsed); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, body)
				}
				servers := parsed["mcpServers"].(map[string]any)
				bb, ok := servers["termipod-browser"].(map[string]any)
				if !ok {
					t.Fatalf("termipod-browser entry missing; servers = %v", servers)
				}
				if bb["command"] != "node" {
					t.Errorf("command = %v; want node", bb["command"])
				}
				args, ok := bb["args"].([]any)
				if !ok || len(args) != 1 || args[0] != d.BridgePath {
					t.Errorf("args = %v; want [%s]", bb["args"], d.BridgePath)
				}
				env, ok := bb["env"].(map[string]any)
				if !ok {
					t.Fatalf("env missing or wrong shape: %v", bb["env"])
				}
				if env["TP_BROWSER_URL"] != d.URL || env["TP_BROWSER_TOKEN"] != d.Token || env["TP_BROWSER_SCOPE"] != "read" {
					t.Errorf("env = %v; want URL/token/scope=read", env)
				}
				// The token must ride env, never argv — the argv of an MCP
				// server is visible to any process listing.
				for _, a := range args {
					if strings.Contains(a.(string), d.Token) {
						t.Errorf("token leaked into args: %v", args)
					}
				}
				// The hub entry must be untouched by the additive splice.
				if servers["termipod"] == nil {
					t.Error("hub termipod entry clobbered by the splice")
				}
			})
		}
	})

	t.Run("absent without discovery file", func(t *testing.T) {
		binDir := t.TempDir() // node on PATH, no discovery
		name := "node"
		if runtime.GOOS == "windows" {
			name = "node.exe"
		}
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("rem\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		t.Setenv("HOME", t.TempDir())
		for _, family := range families {
			workdir := t.TempDir()
			if err := writeMCPConfigForFamily(family, workdir, "https://hub.example/", "tok-hub"); err != nil {
				t.Fatalf("%s: %v", family, err)
			}
			body, err := os.ReadFile(familyConfigPath(t, family, workdir))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "termipod-browser") {
				t.Errorf("%s: bridge entry present without discovery file:\n%s", family, body)
			}
		}
	})

	t.Run("absent when node is not on PATH", func(t *testing.T) {
		setupBridgeHome(t, false) // discovery live, PATH = empty dir
		for _, family := range families {
			workdir := t.TempDir()
			if err := writeMCPConfigForFamily(family, workdir, "https://hub.example/", "tok-hub"); err != nil {
				t.Fatalf("%s: %v", family, err)
			}
			body, err := os.ReadFile(familyConfigPath(t, family, workdir))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "termipod-browser") {
				t.Errorf("%s: bridge entry present without node:\n%s", family, body)
			}
		}
	})

	t.Run("stale discovery (dead pid) is not injected", func(t *testing.T) {
		home, d := setupBridgeHome(t, true)
		d.PID = 1<<31 - 2
		writeDiscovery(t, home, d)
		workdir := t.TempDir()
		if err := writeMCPConfigForFamily("claude-code", workdir, "https://hub.example/", "tok-hub"); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(workdir, ".mcp.json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "termipod-browser") {
			t.Errorf("bridge entry present with dead pid:\n%s", body)
		}
	})
}

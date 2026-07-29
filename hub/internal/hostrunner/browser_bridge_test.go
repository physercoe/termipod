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
		for _, u := range []string{
			"http://192.0.2.10:47321/mcp",          // a network peer — never inject
			"http://127.0.0.1:8080@evil.com/mcp",   // userinfo trick: host is evil.com, not loopback
			"http://localhost:@evil.com/mcp",       // same, localhost spelling
			"http://localhost.evil.com:47321/mcp",  // loopback-lookalike hostname
			"https://127.0.0.1:47321/mcp",          // the desktop only ever writes http
			"http://user:pass@127.0.0.1:47321/mcp", // userinfo the desktop never writes
		} {
			home, d := setupBridgeHome(t, false)
			d.URL = u
			writeDiscovery(t, home, d)
			if got := readBrowserBridgeDiscovery(home); got != nil {
				t.Fatalf("URL %q: want nil, got %+v", u, got)
			}
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

// W2: the spawn-spec opt-in selects the action-scoped token; the agent id
// always rides TP_BROWSER_AGENT_ID for the desktop's audit attribution.
func TestBrowserBridgeActionScope(t *testing.T) {
	withActionToken := func(t *testing.T, home string, d browserBridgeDiscovery) browserBridgeDiscovery {
		t.Helper()
		d.ActionToken = "tok-browser-action"
		writeDiscovery(t, home, d)
		return d
	}

	// envOf writes the family config and returns the injected bridge env.
	envOf := func(t *testing.T, family string, req browserBridgeRequest) map[string]string {
		t.Helper()
		workdir := t.TempDir()
		if err := writeMCPConfigForFamily(family, workdir, "https://hub.example/", "tok-hub", req); err != nil {
			t.Fatalf("writeMCPConfigForFamily: %v", err)
		}
		body, err := os.ReadFile(familyConfigPath(t, family, workdir))
		if err != nil {
			t.Fatal(err)
		}
		if family == "codex" {
			text := string(body)
			out := map[string]string{}
			for _, key := range []string{"TP_BROWSER_URL", "TP_BROWSER_TOKEN", "TP_BROWSER_SCOPE", "TP_BROWSER_AGENT_ID"} {
				for _, line := range strings.Split(text, "\n") {
					if strings.HasPrefix(line, key+" = ") {
						out[key] = strings.Trim(strings.TrimPrefix(line, key+" = "), `"`)
					}
				}
			}
			if out["TP_BROWSER_TOKEN"] == "" {
				t.Fatalf("codex bridge env unparsable:\n%s", text)
			}
			return out
		}
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, body)
		}
		bb := parsed["mcpServers"].(map[string]any)["termipod-browser"].(map[string]any)
		out := map[string]string{}
		for k, v := range bb["env"].(map[string]any) {
			out[k] = v.(string)
		}
		return out
	}

	for _, family := range []string{"claude-code", "kimi-code-ts", "gemini-cli", "codex"} {
		t.Run(family+" opt-in gets the action token", func(t *testing.T) {
			home, d := setupBridgeHome(t, true)
			d = withActionToken(t, home, d)
			env := envOf(t, family, browserBridgeRequest{optIn: true, agentID: "agent-123"})
			if env["TP_BROWSER_TOKEN"] != d.ActionToken {
				t.Errorf("token = %q; want the action token", env["TP_BROWSER_TOKEN"])
			}
			if env["TP_BROWSER_SCOPE"] != "action" {
				t.Errorf("scope = %q; want action", env["TP_BROWSER_SCOPE"])
			}
			if env["TP_BROWSER_AGENT_ID"] != "agent-123" {
				t.Errorf("agent id = %q; want agent-123", env["TP_BROWSER_AGENT_ID"])
			}
		})

		t.Run(family+" default stays read scope", func(t *testing.T) {
			home, d := setupBridgeHome(t, true)
			d = withActionToken(t, home, d)
			env := envOf(t, family, browserBridgeRequest{})
			if env["TP_BROWSER_TOKEN"] != d.Token || env["TP_BROWSER_SCOPE"] != "read" {
				t.Errorf("env = %v; want read token + scope=read", env)
			}
		})

		t.Run(family+" opt-in with a W1-era discovery degrades to read", func(t *testing.T) {
			setupBridgeHome(t, true) // no action_token written
			env := envOf(t, family, browserBridgeRequest{optIn: true, agentID: "agent-123"})
			if env["TP_BROWSER_SCOPE"] != "read" {
				t.Errorf("scope = %q; want read (no action_token in discovery)", env["TP_BROWSER_SCOPE"])
			}
		})
	}
}

// W2: the spawn_spec_yaml field parses through.
func TestParseSpecBrowserBridge(t *testing.T) {
	spec, err := ParseSpec("backend:\n  cmd: claude\nbrowser_bridge: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.BrowserBridge {
		t.Error("browser_bridge: true did not parse")
	}
	spec, err = ParseSpec("backend:\n  cmd: claude\n")
	if err != nil {
		t.Fatal(err)
	}
	if spec.BrowserBridge {
		t.Error("browser_bridge should default to false")
	}
}

package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLaunchM2_CodexFamily_WiresAppServerDriver pins the slice-6
// integration: family=codex routes to the AppServerDriver, the
// MCP config materializes as TOML at .codex/config.toml (slice 5),
// no .mcp.json gets written, and the JSON-RPC handshake actually
// fires once the driver starts. A fake codex stand-in services the
// initialize + thread/start calls so launchM2 can complete without
// a real binary on $PATH.
func TestLaunchM2_CodexFamily_WiresAppServerDriver(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:codex-steward.0"}
	poster := &fakePoster{}

	// Drive the codex side: read JSON-RPC requests off the input
	// pipe and write canned responses to the child stdout pipe so
	// the AppServerDriver handshake doesn't hang.
	go fakeCodexAppServer(t, spawner)

	sp := Spawn{
		ChildID: "agent-codex-1",
		Handle:  "codex-steward",
		Kind:    "codex",
		SpawnSpec: "backend:\n" +
			"  cmd: CODEX_HOME=.codex codex app-server --listen stdio://\n" +
			"  default_workdir: ~/hub-work\n",
		Mode:     "M2",
		MCPToken: "tok-codex-test",
	}

	res, err := launchM2(context.Background(), M2LaunchConfig{
		Spawn:    sp,
		Launcher: launcher,
		Client:   poster,
		Spawner:  spawner,
		LogDir:   logDir,
		HubURL:   "https://hub.example/mcp/",
	})
	if err != nil {
		t.Fatalf("launchM2: %v", err)
	}
	defer res.Driver.Stop()

	// Driver dispatch: codex must produce *AppServerDriver, not
	// StdioDriver. A type assertion keeps this honest under
	// future refactors that flatten the dispatch elsewhere.
	if _, ok := res.Driver.(*AppServerDriver); !ok {
		t.Fatalf("res.Driver: want *AppServerDriver, got %T", res.Driver)
	}

	// MCP config: slice-5 dispatcher must write the TOML form into
	// <workdir>/.codex/config.toml — *not* .mcp.json.
	codexCfg := filepath.Join(homeDir, "hub-work", ".codex", "config.toml")
	body, err := os.ReadFile(codexCfg)
	if err != nil {
		t.Fatalf("read .codex/config.toml: %v", err)
	}
	bodyStr := string(body)
	for _, want := range []string{
		"[mcp_servers.termipod]",
		`command = "hub-mcp-bridge"`,
		`HUB_URL = "https://hub.example/mcp/"`,
		`HUB_TOKEN = "tok-codex-test"`,
	} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("config.toml missing %q\n--- contents ---\n%s", want, bodyStr)
		}
	}
	if _, err := os.Stat(filepath.Join(homeDir, "hub-work", ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should NOT exist for codex spawns; stat err = %v", err)
	}

	// Spawn command must carry CODEX_HOME so the engine reads our
	// project-scoped config.toml without consulting its
	// trusted-projects gate. Wrapped by the launcher in
	// `cd <workdir> && <cmd>`, the assertion is on the substring.
	if !strings.Contains(spawner.cmd, "CODEX_HOME=.codex") {
		t.Errorf("spawn command missing CODEX_HOME bypass; cmd = %q", spawner.cmd)
	}
	if !strings.Contains(spawner.cmd, "codex app-server --listen stdio://") {
		t.Errorf("spawn command not invoking app-server in stdio mode; cmd = %q", spawner.cmd)
	}

	// Handshake completed → the driver should have a non-empty
	// thread id captured from the fake server's response. This
	// proves the JSON-RPC layer is talking through the launchM2
	// pipes the same way the standalone driver test does, just
	// wired up by the production launcher path.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tid := res.Driver.(*AppServerDriver).ThreadID(); tid != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if tid := res.Driver.(*AppServerDriver).ThreadID(); tid == "" {
		t.Errorf("handshake did not capture thread id within 2s")
	}
}

// fakeCodexAppServer plays the codex side of the JSON-RPC connection
// for launch_m2 tests. Reads requests off the spawner's input pipe
// (what host-runner wrote to the child's stdin) and writes canned
// responses + a thread/started notification back through the child
// stdout pipe. Mirrors the inline server in driver_appserver_test
// but the launchM2 fakeProcSpawner uses io.Pipe ends laid out
// differently from newPipePair, hence the small duplicate.
func fakeCodexAppServer(t *testing.T, spawner *fakeProcSpawner) {
	// Wait for the spawner to have wired its pipes. The Spawn call
	// in launchM2 is what populates spawner.child / spawner.input;
	// poll briefly so the goroutine doesn't race the launcher.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (spawner.child == nil || spawner.input == nil) {
		time.Sleep(5 * time.Millisecond)
	}
	if spawner.child == nil || spawner.input == nil {
		return
	}
	dec := json.NewDecoder(spawner.input)
	enc := json.NewEncoder(spawner.child)
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			return
		}
		method, _ := req["method"].(string)
		// Notifications (no id) need no response — just consume.
		idRaw, hasID := req["id"]
		if !hasID {
			continue
		}
		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "1.0"}
		case "thread/start":
			result = map[string]any{
				"thread": map[string]any{
					"id":            "thr_codex_smoke",
					"createdAt":     "2026-04-29T10:00:00Z",
					"modelProvider": "gpt-5.4",
				},
			}
		default:
			// Echo a generic empty result so unknown calls don't
			// stall the driver while a slice-7 expansion lands.
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      idRaw,
			"result":  result,
		})
	}
}

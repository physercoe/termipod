package hostrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWriteKimiTSMCPConfig_FreshWorkdir asserts the file-system shape
// when the workdir has no existing .kimi-code/mcp.json: the per-spawn
// <workdir>/.kimi-code/mcp.json contains exactly one server (termipod)
// pointing at hub-mcp-bridge, file 0o600 inside dir 0o700. ADR-054 D3.
// Unlike the Python-line writer (writeKimiMCPConfig), there is NO
// home-file merge — the TS engine loads ~/.kimi-code/mcp.json itself
// underneath the project level, so the operator's file stays untouched
// and unconsulted here.
func TestWriteKimiTSMCPConfig_FreshWorkdir(t *testing.T) {
	workdir := t.TempDir()

	if err := writeKimiTSMCPConfig(workdir, "https://hub.example/mcp/", "tok-kimi-ts-fresh"); err != nil {
		t.Fatalf("writeKimiTSMCPConfig: %v", err)
	}

	target := filepath.Join(workdir, ".kimi-code", "mcp.json")
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, body)
	}
	servers := parsed["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Errorf("fresh workdir should have exactly 1 server, got %d: %v", len(servers), servers)
	}
	tp := servers["termipod"].(map[string]any)
	if tp["command"] != "hub-mcp-bridge" {
		t.Errorf("command = %v; want hub-mcp-bridge", tp["command"])
	}
	env := tp["env"].(map[string]any)
	if env["HUB_URL"] != "https://hub.example/mcp/" {
		t.Errorf("HUB_URL = %v", env["HUB_URL"])
	}
	if env["HUB_TOKEN"] != "tok-kimi-ts-fresh" {
		t.Errorf("HUB_TOKEN = %v", env["HUB_TOKEN"])
	}

	dirInfo, _ := os.Stat(filepath.Join(workdir, ".kimi-code"))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf(".kimi-code dir perm = %o; want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, _ := os.Stat(target)
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("mcp.json perm = %o; want 0600", fileInfo.Mode().Perm())
	}
}

// TestWriteKimiTSMCPConfig_PreservesProjectServers asserts the merge
// branch: a pre-existing project-level <workdir>/.kimi-code/mcp.json
// (the workdir may be a real repo the operator pre-configured) keeps
// its own MCP servers and sibling top-level keys unchanged alongside
// the spliced termipod entry.
func TestWriteKimiTSMCPConfig_PreservesProjectServers(t *testing.T) {
	workdir := t.TempDir()

	// Stage an existing project config with one custom MCP server +
	// a sibling top-level key that should pass through unchanged.
	projectPath := filepath.Join(workdir, ".kimi-code", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o700); err != nil {
		t.Fatalf("mkdir project .kimi-code: %v", err)
	}
	projectCfg := []byte(`{
  "mcpServers": {
    "brave-search": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-brave-search"],
      "env": {"BRAVE_API_KEY": "operator-key"}
    }
  },
  "operator_only_field": "should pass through"
}`)
	if err := os.WriteFile(projectPath, projectCfg, 0o600); err != nil {
		t.Fatalf("seed project mcp.json: %v", err)
	}

	if err := writeKimiTSMCPConfig(workdir, "https://hub.example/mcp/", "tok-merge"); err != nil {
		t.Fatalf("writeKimiTSMCPConfig: %v", err)
	}

	body, _ := os.ReadFile(filepath.Join(workdir, ".kimi-code", "mcp.json"))
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, body)
	}
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["brave-search"]; !ok {
		t.Error("project's brave-search MCP server was lost in the merge")
	}
	if _, ok := servers["termipod"]; !ok {
		t.Error("termipod MCP server was not added")
	}
	if parsed["operator_only_field"] != "should pass through" {
		t.Errorf("project's sibling key was lost: %v", parsed["operator_only_field"])
	}
	brave := servers["brave-search"].(map[string]any)
	if brave["command"] != "npx" {
		t.Errorf("project brave-search.command mutated: %v", brave["command"])
	}
	braveEnv := brave["env"].(map[string]any)
	if braveEnv["BRAVE_API_KEY"] != "operator-key" {
		t.Errorf("project brave-search.env.BRAVE_API_KEY mutated: %v", braveEnv["BRAVE_API_KEY"])
	}
}

// TestWriteKimiTSMCPConfig_MalformedProjectFails asserts the loud-fail
// branch: a corrupted project-level <workdir>/.kimi-code/mcp.json must
// NOT be silently overwritten — the operator needs to know their file
// is broken so they can fix it. (Silently clobbering is the failure
// mode this test defends against.) The error must name the path, and
// the file on disk must still hold the original bytes afterwards.
func TestWriteKimiTSMCPConfig_MalformedProjectFails(t *testing.T) {
	workdir := t.TempDir()

	projectPath := filepath.Join(workdir, ".kimi-code", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(projectPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("seed bad mcp.json: %v", err)
	}

	err := writeKimiTSMCPConfig(workdir, "https://hub.example/mcp/", "tok")
	if err == nil {
		t.Fatal("expected error for malformed project mcp.json, got nil")
	}
	if !strings.Contains(err.Error(), projectPath) {
		t.Errorf("error text should reference the file path %s: %v", projectPath, err)
	}

	// The malformed file must NOT have been overwritten — partial
	// writes here would also be a footgun.
	body, rerr := os.ReadFile(projectPath)
	if rerr != nil {
		t.Fatalf("project mcp.json missing after parse failure: %v", rerr)
	}
	if string(body) != "{not-json" {
		t.Errorf("project mcp.json was overwritten despite parse failure: %q", body)
	}
}

// TestWriteKimiTSMCPConfig_TermipodReplacesStaleEntry asserts that a
// project-level `termipod` entry (e.g. left behind by a previous
// spawn) gets replaced rather than appended-to. The hub's HUB_TOKEN is
// per-spawn; a previous value is never the right answer for the next
// spawn.
func TestWriteKimiTSMCPConfig_TermipodReplacesStaleEntry(t *testing.T) {
	workdir := t.TempDir()

	projectPath := filepath.Join(workdir, ".kimi-code", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := []byte(`{
  "mcpServers": {
    "termipod": {"command": "old-bridge", "env": {"HUB_TOKEN": "stale-token"}}
  }
}`)
	if err := os.WriteFile(projectPath, stale, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeKimiTSMCPConfig(workdir, "https://hub.example/mcp/", "fresh-token"); err != nil {
		t.Fatalf("writeKimiTSMCPConfig: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(workdir, ".kimi-code", "mcp.json"))
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	tp := parsed["mcpServers"].(map[string]any)["termipod"].(map[string]any)
	if tp["command"] != "hub-mcp-bridge" {
		t.Errorf("termipod.command = %v; want hub-mcp-bridge", tp["command"])
	}
	if tp["env"].(map[string]any)["HUB_TOKEN"] != "fresh-token" {
		t.Errorf("HUB_TOKEN = %v; want fresh-token", tp["env"].(map[string]any)["HUB_TOKEN"])
	}
}

// TestLaunchM1_KimiTSWritesProjectConfigNoFlagSplice closes the
// launch-side loop for the TypeScript kimi line — the inverse of
// TestLaunchM1_KimiSplicesMCPConfigFlag. When launch_m1 is given a
// kimi-code-ts spawn carrying an MCPToken, the per-spawn
// .kimi-code/mcp.json gets materialized in the workdir AND the cmd
// that spawns the engine is left untouched: the TS build removed the
// Python line's --mcp-config-file flag and auto-discovers the
// project-level file, so any argv splice would fail the spawn loud
// (unknown flag).
func TestLaunchM1_KimiTSWritesProjectConfigNoFlagSplice(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:kimi-ts-acp.0"}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID: "agent-kimi-ts-1",
		Handle:  "kimi-ts-acp",
		Kind:    "kimi-code-ts",
		Mode:    "M1",
		SpawnSpec: "backend:\n" +
			"  cmd: kimi --yolo acp\n" +
			"  default_workdir: " + homeDir + "\n",
		MCPToken: "tok-kimi-ts-launch",
	}

	type result struct {
		res M1LaunchResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, e := launchM1(context.Background(), M1LaunchConfig{
			Spawn:    sp,
			Launcher: launcher,
			Client:   poster,
			Spawner:  spawner,
			LogDir:   logDir,
			HubURL:   "https://hub.example/mcp/",
		})
		done <- result{r, e}
	}()

	// Wait for the spawner to be exercised; drive the fake ACP agent
	// on the child end so launch_m1 actually returns.
	spawner.waitReady(t)
	agent := newFakeACPAgent(t, spawner.input, spawner.child, "sess-kimi-ts-launchm1")
	go agent.serve()

	select {
	case <-time.After(3 * time.Second):
		t.Fatal("launchM1 did not return")
	case r := <-done:
		if r.err != nil {
			t.Fatalf("launchM1: %v", r.err)
		}
		defer r.res.Driver.Stop()
	}

	// The spawner must have seen the kimi binary invoked WITHOUT any
	// --mcp-config-file splice — kimi-code-ts auto-discovers the
	// project-level file; the flag doesn't exist on this build.
	if strings.Contains(spawner.cmd, "--mcp-config-file") {
		t.Errorf("spawner.cmd = %q; want NO --mcp-config-file splice for kimi-code-ts", spawner.cmd)
	}
	if !strings.Contains(spawner.cmd, "kimi --yolo acp") {
		t.Errorf("spawner.cmd = %q; want the template cmd `kimi --yolo acp` verbatim", spawner.cmd)
	}

	// And the per-spawn project-level file must actually exist in the
	// workdir the engine cd's into.
	mcpPath := filepath.Join(homeDir, ".kimi-code", "mcp.json")
	if _, err := os.Stat(mcpPath); err != nil {
		t.Errorf(".kimi-code/mcp.json not materialized at %s: %v", mcpPath, err)
	}
}

// TestWriteMCPConfigForFamily_KimiTSDispatch pins the dispatcher
// branch: family=kimi-code-ts routes to writeKimiTSMCPConfig
// (.kimi-code/mcp.json), not to any other engine's materializer — in
// particular not to the Python line's .kimi/mcp.json.
func TestWriteMCPConfigForFamily_KimiTSDispatch(t *testing.T) {
	workdir := t.TempDir()
	if err := writeMCPConfigForFamily("kimi-code-ts", workdir, "https://hub.example/", "tok"); err != nil {
		t.Fatalf("writeMCPConfigForFamily(kimi-code-ts): %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".kimi-code", "mcp.json")); err != nil {
		t.Errorf(".kimi-code/mcp.json should exist for kimi-code-ts: %v", err)
	}
	// Sibling engine paths must NOT exist.
	for _, leak := range []string{
		filepath.Join(workdir, ".kimi", "mcp.json"),
		filepath.Join(workdir, ".mcp.json"),
		filepath.Join(workdir, ".gemini", "settings.json"),
		filepath.Join(workdir, ".codex", "config.toml"),
	} {
		if _, err := os.Stat(leak); !os.IsNotExist(err) {
			t.Errorf("%s should not exist for kimi-code-ts (cross-engine leak)", leak)
		}
	}
}

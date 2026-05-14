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

// TestWriteKimiMCPConfig_FreshHost asserts the file-system shape when
// the operator has no existing ~/.kimi/mcp.json: per-spawn
// <workdir>/.kimi/mcp.json contains exactly one server (termipod)
// pointing at hub-mcp-bridge, file 0o600 inside dir 0o700. ADR-026 D5.
func TestWriteKimiMCPConfig_FreshHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	if err := writeKimiMCPConfig(workdir, "https://hub.example/mcp/", "tok-kimi-fresh"); err != nil {
		t.Fatalf("writeKimiMCPConfig: %v", err)
	}

	target := filepath.Join(workdir, ".kimi", "mcp.json")
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
		t.Errorf("fresh host should have exactly 1 server, got %d: %v", len(servers), servers)
	}
	tp := servers["termipod"].(map[string]any)
	if tp["command"] != "hub-mcp-bridge" {
		t.Errorf("command = %v; want hub-mcp-bridge", tp["command"])
	}
	env := tp["env"].(map[string]any)
	if env["HUB_URL"] != "https://hub.example/mcp/" {
		t.Errorf("HUB_URL = %v", env["HUB_URL"])
	}
	if env["HUB_TOKEN"] != "tok-kimi-fresh" {
		t.Errorf("HUB_TOKEN = %v", env["HUB_TOKEN"])
	}

	dirInfo, _ := os.Stat(filepath.Join(workdir, ".kimi"))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf(".kimi dir perm = %o; want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, _ := os.Stat(target)
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("mcp.json perm = %o; want 0600", fileInfo.Mode().Perm())
	}
}

// TestWriteKimiMCPConfig_PreservesOperatorServers asserts the merge
// branch: an operator-configured ~/.kimi/mcp.json with their own MCP
// server passes through unchanged into the per-spawn copy alongside
// the termipod entry. The operator never sees their MCP setup
// silently disappear when termipod spawns a kimi steward.
func TestWriteKimiMCPConfig_PreservesOperatorServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	// Stage an existing operator config with one custom MCP server +
	// a sibling top-level key that should pass through unchanged.
	operatorPath := filepath.Join(home, ".kimi", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(operatorPath), 0o700); err != nil {
		t.Fatalf("mkdir operator .kimi: %v", err)
	}
	operatorCfg := []byte(`{
  "mcpServers": {
    "brave-search": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-brave-search"],
      "env": {"BRAVE_API_KEY": "operator-key"}
    }
  },
  "operator_only_field": "should pass through"
}`)
	if err := os.WriteFile(operatorPath, operatorCfg, 0o600); err != nil {
		t.Fatalf("seed operator mcp.json: %v", err)
	}

	if err := writeKimiMCPConfig(workdir, "https://hub.example/mcp/", "tok-merge"); err != nil {
		t.Fatalf("writeKimiMCPConfig: %v", err)
	}

	body, _ := os.ReadFile(filepath.Join(workdir, ".kimi", "mcp.json"))
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, body)
	}
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["brave-search"]; !ok {
		t.Error("operator's brave-search MCP server was lost in the merge")
	}
	if _, ok := servers["termipod"]; !ok {
		t.Error("termipod MCP server was not added")
	}
	if parsed["operator_only_field"] != "should pass through" {
		t.Errorf("operator's sibling key was lost: %v", parsed["operator_only_field"])
	}
	brave := servers["brave-search"].(map[string]any)
	if brave["command"] != "npx" {
		t.Errorf("operator brave-search.command mutated: %v", brave["command"])
	}
	braveEnv := brave["env"].(map[string]any)
	if braveEnv["BRAVE_API_KEY"] != "operator-key" {
		t.Errorf("operator brave-search.env.BRAVE_API_KEY mutated: %v", braveEnv["BRAVE_API_KEY"])
	}
}

// TestWriteKimiMCPConfig_MalformedOperatorFails asserts the loud-fail
// branch: a corrupted operator ~/.kimi/mcp.json must NOT be silently
// overwritten — the operator needs to know their file is broken so
// they can fix it. (Silently clobbering is the failure mode this test
// defends against.)
func TestWriteKimiMCPConfig_MalformedOperatorFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	operatorPath := filepath.Join(home, ".kimi", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(operatorPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(operatorPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("seed bad mcp.json: %v", err)
	}

	err := writeKimiMCPConfig(workdir, "https://hub.example/mcp/", "tok")
	if err == nil {
		t.Fatal("expected error for malformed operator mcp.json, got nil")
	}
	if !strings.Contains(err.Error(), "mcp.json") {
		t.Errorf("error text should reference the file: %v", err)
	}

	// The per-spawn file must NOT have been written when the parse
	// failed — partial writes here would also be a footgun.
	if _, statErr := os.Stat(filepath.Join(workdir, ".kimi", "mcp.json")); !os.IsNotExist(statErr) {
		t.Errorf("per-spawn mcp.json should NOT exist after parse failure; stat err = %v", statErr)
	}
}

// TestWriteKimiMCPConfig_TerminpodReplacesStaleEntry asserts that an
// operator who manually added a `termipod` entry (or a previous spawn
// left a stale token) gets it replaced rather than appended-to. The
// hub's HUB_TOKEN is per-spawn; a previous value is never the right
// answer for the next spawn.
func TestWriteKimiMCPConfig_TerminpodReplacesStaleEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()

	operatorPath := filepath.Join(home, ".kimi", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(operatorPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := []byte(`{
  "mcpServers": {
    "termipod": {"command": "old-bridge", "env": {"HUB_TOKEN": "stale-token"}}
  }
}`)
	if err := os.WriteFile(operatorPath, stale, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := writeKimiMCPConfig(workdir, "https://hub.example/mcp/", "fresh-token"); err != nil {
		t.Fatalf("writeKimiMCPConfig: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(workdir, ".kimi", "mcp.json"))
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

// TestLaunchM1_KimiSplicesMCPConfigFlag closes the launch-side loop:
// when launch_m1 is given a kimi-code spawn carrying an MCPToken, the
// per-spawn .kimi/mcp.json gets materialized AND the cmd that
// actually spawns the engine carries `--mcp-config-file <path>`
// between the binary name and the next top-level flag. Without the
// argv splice, kimi-cli would fall back to ~/.kimi/mcp.json (the
// operator's file) and the hub MCP server would never get injected.
func TestLaunchM1_KimiSplicesMCPConfigFlag(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: "hub-agents:kimi-acp.0"}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID: "agent-kimi-1",
		Handle:  "kimi-acp",
		Kind:    "kimi-code",
		Mode:    "M1",
		SpawnSpec: "backend:\n" +
			"  cmd: kimi --yolo --thinking acp\n" +
			"  default_workdir: " + homeDir + "\n",
		MCPToken: "tok-kimi-launch",
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
	deadline := time.After(2 * time.Second)
	for spawner.child == nil {
		select {
		case <-deadline:
			t.Fatal("spawner never invoked")
		case <-time.After(5 * time.Millisecond):
		}
	}
	agent := newFakeACPAgent(t, spawner.input, spawner.child, "sess-kimi-launchm1")
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

	// The spawner must have seen the kimi binary invoked with the
	// --mcp-config-file flag spliced between `kimi` and `--yolo`.
	if !strings.Contains(spawner.cmd, "--mcp-config-file") {
		t.Errorf("spawner.cmd = %q; want --mcp-config-file spliced in argv", spawner.cmd)
	}
	// Splice point matters: --mcp-config-file must precede --yolo so
	// it's parsed as a top-level flag (kimi's option parser treats
	// arguments after the subcommand as subcommand args, not global).
	mcpIdx := strings.Index(spawner.cmd, "--mcp-config-file")
	yoloIdx := strings.Index(spawner.cmd, "--yolo")
	acpIdx := strings.Index(spawner.cmd, " acp")
	if mcpIdx < 0 || yoloIdx < 0 || acpIdx < 0 {
		t.Fatalf("spawner.cmd missing expected flags: %q", spawner.cmd)
	}
	if !(mcpIdx < yoloIdx && yoloIdx < acpIdx) {
		t.Errorf("flag order in spawner.cmd = %q; want --mcp-config-file < --yolo < acp", spawner.cmd)
	}

	// And the per-spawn file must actually exist at the path the
	// flag points to.
	mcpPath := filepath.Join(homeDir, ".kimi", "mcp.json")
	if _, err := os.Stat(mcpPath); err != nil {
		t.Errorf(".kimi/mcp.json not materialized at %s: %v", mcpPath, err)
	}
}

// TestWriteMCPConfigForFamily_KimiDispatch pins the dispatcher branch:
// family=kimi-code routes to writeKimiMCPConfig (.kimi/mcp.json), not
// to any other engine's materializer.
func TestWriteMCPConfigForFamily_KimiDispatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdir := t.TempDir()
	if err := writeMCPConfigForFamily("kimi-code", workdir, "https://hub.example/", "tok"); err != nil {
		t.Fatalf("writeMCPConfigForFamily(kimi-code): %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".kimi", "mcp.json")); err != nil {
		t.Errorf(".kimi/mcp.json should exist for kimi-code: %v", err)
	}
	// Sibling engine paths must NOT exist.
	for _, leak := range []string{
		filepath.Join(workdir, ".mcp.json"),
		filepath.Join(workdir, ".gemini", "settings.json"),
		filepath.Join(workdir, ".codex", "config.toml"),
	} {
		if _, err := os.Stat(leak); !os.IsNotExist(err) {
			t.Errorf("%s should not exist for kimi-code (cross-engine leak)", leak)
		}
	}
}

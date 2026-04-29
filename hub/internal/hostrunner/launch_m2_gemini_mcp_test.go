package hostrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteGeminiMCPConfig pins the slice-5 wire shape for ADR-013 D5:
// .gemini/settings.json with mcpServers.<name>.{command,env}, file
// mode 0o600 inside a 0o700 .gemini directory. Wire shape mirrors
// claude's .mcp.json so hub-mcp-bridge stays vendor-neutral.
func TestWriteGeminiMCPConfig(t *testing.T) {
	workdir := t.TempDir()
	if err := writeGeminiMCPConfig(workdir, "https://hub.example/mcp/", "tok-gemini-test"); err != nil {
		t.Fatalf("writeGeminiMCPConfig: %v", err)
	}

	// File must be at <workdir>/.gemini/settings.json — matching what
	// gemini-cli reads when launched with cwd=<workdir>.
	target := filepath.Join(workdir, ".gemini", "settings.json")
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n--- contents ---\n%s", err, body)
	}

	servers, ok := parsed["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("settings.json: mcpServers not a map; got %T", parsed["mcpServers"])
	}
	tp, ok := servers["termipod"].(map[string]any)
	if !ok {
		t.Fatalf("settings.json: mcpServers.termipod missing or wrong shape; got %v", servers)
	}
	if tp["command"] != "hub-mcp-bridge" {
		t.Errorf("command = %v; want hub-mcp-bridge", tp["command"])
	}
	env, ok := tp["env"].(map[string]any)
	if !ok {
		t.Fatalf("env not a map; got %T", tp["env"])
	}
	if env["HUB_URL"] != "https://hub.example/mcp/" {
		t.Errorf("HUB_URL = %v", env["HUB_URL"])
	}
	if env["HUB_TOKEN"] != "tok-gemini-test" {
		t.Errorf("HUB_TOKEN = %v", env["HUB_TOKEN"])
	}

	// Perm checks: dir 0o700, file 0o600 — secrets must not leak via
	// other-user-readable bits when the workdir lives on a shared
	// filesystem.
	dirInfo, err := os.Stat(filepath.Join(workdir, ".gemini"))
	if err != nil {
		t.Fatalf("stat .gemini: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf(".gemini dir perm = %o; want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat settings.json: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("settings.json perm = %o; want 0600", fileInfo.Mode().Perm())
	}
}

// TestWriteGeminiMCPConfig_TokenWithSpecialChars covers a token whose
// shape includes characters JSON requires escaping. Uses the same
// adversarial input as the codex test for parity. We expect the
// JSON encoder to escape correctly so a future token-shape change
// doesn't silently corrupt the file.
func TestWriteGeminiMCPConfig_TokenWithSpecialChars(t *testing.T) {
	workdir := t.TempDir()
	tricky := "tok-\"with quotes\"-and-\\backslashes-\nand-newlines"
	if err := writeGeminiMCPConfig(workdir, "https://hub.example/mcp/", tricky); err != nil {
		t.Fatalf("writeGeminiMCPConfig: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(workdir, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON with adversarial token: %v\n%s", err, body)
	}
	got := parsed["mcpServers"].(map[string]any)["termipod"].(map[string]any)["env"].(map[string]any)["HUB_TOKEN"]
	if got != tricky {
		t.Errorf("HUB_TOKEN round-trip = %q; want %q", got, tricky)
	}
}

// TestWriteMCPConfigForFamily_GeminiDispatch pins the dispatcher
// branch — family=gemini-cli must route to writeGeminiMCPConfig
// (.gemini/settings.json), not to writeMCPConfig (.mcp.json) or
// writeCodexMCPConfig (.codex/config.toml). Defends against a future
// refactor that flattens the dispatch and accidentally picks the
// wrong materializer for one engine.
func TestWriteMCPConfigForFamily_GeminiDispatch(t *testing.T) {
	workdir := t.TempDir()
	if err := writeMCPConfigForFamily("gemini-cli", workdir, "https://hub.example/", "tok"); err != nil {
		t.Fatalf("writeMCPConfigForFamily(gemini-cli): %v", err)
	}

	// Gemini path must exist.
	if _, err := os.Stat(filepath.Join(workdir, ".gemini", "settings.json")); err != nil {
		t.Errorf(".gemini/settings.json should exist for gemini-cli: %v", err)
	}
	// Other paths must NOT exist — strict isolation per family.
	if _, err := os.Stat(filepath.Join(workdir, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should not exist for gemini-cli (claude format leaked); err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf(".codex/config.toml should not exist for gemini-cli (codex format leaked); err = %v", err)
	}
}

// TestLaunchM2_GeminiFamily_WritesSettingsJSON closes the loop end-to-end:
// the slice-3 launch path must call into writeGeminiMCPConfig when
// the spawn carries a token + hubURL, just like codex's wire-up does.
// Catches a wiring regression where launchM2 forgets to materialize
// the MCP config for gemini even though writeMCPConfigForFamily knows
// how.
func TestLaunchM2_GeminiFamily_WritesSettingsJSON(t *testing.T) {
	logDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Stage a fake gemini bin so launch_m2's exec.LookPath succeeds.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "gemini")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake gemini: %v", err)
	}

	spawner := newFakeProcSpawner()
	launcher := &recordingLauncher{pane: ""}
	poster := &fakePoster{}

	sp := Spawn{
		ChildID: "agent-gemini-mcp",
		Kind:    "gemini-cli",
		SpawnSpec: "backend:\n" +
			"  cmd: " + fakeBin + "\n" +
			"  default_workdir: ~/hub-work\n",
		Mode:     "M2",
		MCPToken: "tok-launch-gemini",
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

	target := filepath.Join(homeDir, "hub-work", ".gemini", "settings.json")
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("settings.json not materialized at %s: %v", target, err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("settings.json invalid JSON: %v\n%s", err, body)
	}
	servers := parsed["mcpServers"].(map[string]any)
	tp := servers["termipod"].(map[string]any)
	env := tp["env"].(map[string]any)
	if env["HUB_TOKEN"] != "tok-launch-gemini" {
		t.Errorf("HUB_TOKEN = %v; want tok-launch-gemini", env["HUB_TOKEN"])
	}

	// And the wrong-engine paths must NOT have been written.
	if _, err := os.Stat(filepath.Join(homeDir, "hub-work", ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should NOT exist for gemini spawns; err = %v", err)
	}
}

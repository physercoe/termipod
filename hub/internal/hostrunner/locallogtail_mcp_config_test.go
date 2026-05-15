package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub"
)

func TestWriteMCPConfigClaudeCodeM4_DualServer(t *testing.T) {
	dir := t.TempDir()
	if err := writeMCPConfigClaudeCodeM4(
		dir, "http://127.0.0.1:41825", "tok-abc",
		"/tmp/termipod-agent-XYZ.sock", "/usr/local/bin/host-runner",
	); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse: %v (body=%s)", err, body)
	}
	authority, ok := parsed.MCPServers[hub.MCPServerName]
	if !ok {
		t.Fatalf(".mcpServers[%q] missing; got keys: %v",
			hub.MCPServerName, mapKeys(parsed.MCPServers))
	}
	if authority.Command != "hub-mcp-bridge" {
		t.Errorf("authority cmd = %q, want hub-mcp-bridge", authority.Command)
	}
	if authority.Env["HUB_URL"] != "http://127.0.0.1:41825" {
		t.Errorf("authority HUB_URL = %q", authority.Env["HUB_URL"])
	}
	if authority.Env["HUB_TOKEN"] != "tok-abc" {
		t.Errorf("authority HUB_TOKEN = %q", authority.Env["HUB_TOKEN"])
	}

	hostLocal, ok := parsed.MCPServers[hub.MCPServerNameHost]
	if !ok {
		t.Fatalf(".mcpServers[%q] missing; got keys: %v",
			hub.MCPServerNameHost, mapKeys(parsed.MCPServers))
	}
	if hostLocal.Command != "/usr/local/bin/host-runner" {
		t.Errorf("host-local cmd = %q", hostLocal.Command)
	}
	want := []string{"mcp-uds-stdio", "--socket", "/tmp/termipod-agent-XYZ.sock"}
	if len(hostLocal.Args) != len(want) {
		t.Fatalf("host-local args = %v, want %v", hostLocal.Args, want)
	}
	for i := range want {
		if hostLocal.Args[i] != want[i] {
			t.Errorf("host-local args[%d] = %q, want %q", i, hostLocal.Args[i], want[i])
		}
	}
	if hostLocal.Env != nil {
		t.Errorf("host-local entry should not carry env (no hub secrets needed); got %v", hostLocal.Env)
	}
}

func TestWriteMCPConfigClaudeCodeM4_DefaultHostRunnerExe(t *testing.T) {
	dir := t.TempDir()
	if err := writeMCPConfigClaudeCodeM4(
		dir, "http://127.0.0.1:41825", "tok",
		"/tmp/s.sock", "",
	); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	var parsed struct {
		MCPServers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	_ = json.Unmarshal(body, &parsed)
	if parsed.MCPServers[hub.MCPServerNameHost].Command != "host-runner" {
		t.Errorf("default host-runner cmd = %q, want host-runner",
			parsed.MCPServers[hub.MCPServerNameHost].Command)
	}
}

func TestWriteMCPConfigClaudeCodeM4_FileMode(t *testing.T) {
	dir := t.TempDir()
	if err := writeMCPConfigClaudeCodeM4(dir, "u", "t", "/s.sock", "h"); err != nil {
		t.Fatalf("write: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// On Linux the umask may further restrict but we explicitly write
	// 0o600; verify the mode bits we control.
	if mode := st.Mode().Perm() & 0o077; mode != 0 {
		t.Errorf("group/other bits set: mode = %o", st.Mode().Perm())
	}
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

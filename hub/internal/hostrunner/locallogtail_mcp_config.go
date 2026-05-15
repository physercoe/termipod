// .mcp.json writer for claude-code M4 LocalLogTail spawns — ADR-027 W5d.
//
// Two-server config: `termipod` keeps the existing hub-mcp-bridge entry
// (`permission_prompt`, `delegate`, `request_*`, all hub-authority tools)
// while `termipod-host` adds a sibling entry that dials the per-spawn
// host-runner UDS gateway via `host-runner mcp-uds-stdio --socket
// <path>`. claude-code resolves `mcp__termipod-host__hook_*` against
// the second entry; the LocalLogTailDriver therefore gets first read of
// every hook payload before any hub round-trip.
//
// Every other spawn path (M1, M2, M4 for non-claude families) keeps the
// single-server `writeMCPConfig` — no behaviour change. This file is
// pure addition; W7 wires the call.
package hostrunner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/termipod/hub"
)

// writeMCPConfigClaudeCodeM4 materializes a dual-server `.mcp.json` at
// `<workdir>/.mcp.json` for the claude-code M4 LocalLogTail path.
//
//   - termipod:      `hub-mcp-bridge`, env carries HUB_URL/HUB_TOKEN.
//     Unchanged from writeMCPConfig — preserves the existing approval
//     channel and authority surface.
//   - termipod-host: `<hostRunnerExe> mcp-uds-stdio --socket <udsPath>`.
//     The shim dials the host-runner gateway StartGateway opened for
//     this spawn (see ADR-027 W5a wiring).
//
// hostRunnerExe is the path or basename to invoke for the shim. When
// empty we default to "host-runner" so a PATH lookup picks up the
// installed daemon binary; tests pass an explicit fake.
//
// File mode 0o600 — re-running overwrites, secrets stay readable only
// by the spawned agent's uid.
func writeMCPConfigClaudeCodeM4(workdir, hubURL, token, udsPath, hostRunnerExe string) error {
	if hostRunnerExe == "" {
		hostRunnerExe = "host-runner"
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			hub.MCPServerName: map[string]any{
				"command": "hub-mcp-bridge",
				"env": map[string]string{
					"HUB_URL":   hubURL,
					"HUB_TOKEN": token,
				},
			},
			hub.MCPServerNameHost: map[string]any{
				"command": hostRunnerExe,
				"args":    []string{"mcp-uds-stdio", "--socket", udsPath},
			},
		},
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(workdir, ".mcp.json")
	return os.WriteFile(target, body, 0o600)
}

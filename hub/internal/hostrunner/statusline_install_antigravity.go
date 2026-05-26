// statusLine install for Antigravity (`agy`) — G1 wedge of the
// antigravity statusLine research (docs/discussions/antigravity-
// statusline-research.md §6 G1).
//
// Mirrors `installClaudeStatusLine` in hooks_install.go (the claude-code
// ADR-036 W1 install) but targets a different file:
//
//   - claude-code: <workdir>/.claude/settings.local.json (per-workdir;
//     each spawn has its own .claude dir).
//   - antigravity: ~/.gemini/antigravity-cli/settings.json (host-global;
//     all agy processes for this operator share the file).
//
// The global-vs-per-workdir distinction is fine for the cadence-based
// statusLine model because agy caches the `statusLine.command` value
// at process boot — once an agy process is running, it never re-reads
// settings.json (host-verified via the `/statusline delete` requires
// "reset" string in the binary). So the install→spawn ordering on each
// new spawn — `installAntigravityStatusLine` writes the file → tmux
// launch boots agy → agy caches OUR command — gives each spawn its own
// effective statusLine, even on a shared file. Concurrent spawns each
// see the most recent write at their boot moment, which is what we
// want.
//
// Wrap-and-passthrough preserves operator-set statusLine: if the
// operator (or agy's `/statusline <cmd>` slash command, surface verified
// in the binary strings dump) set a command before us, our install
// records it under `_termipod_wrapped_command` and the status-fire
// shim's `--wrap` flag invokes it after posting telemetry to the
// gateway (operator's stdout becomes the rendered status text;
// telemetry is additive).
package hostrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// installAntigravityStatusLine merges a `statusLine` block into agy's
// host-global ~/.gemini/antigravity-cli/settings.json pointing at the
// host-runner `status-fire` shim against the per-spawn UDS gateway.
//
// Idempotent + wrap-and-passthrough (same shape as installClaudeStatusLine
// in hooks_install.go):
//
//   - If no prior statusLine block exists, we write ours with the
//     `_termipod_managed: true` marker and `enabled: true`.
//
//   - If a prior statusLine block exists AND it's already marked
//     `_termipod_managed: true`, we just update the `command` line
//     (the UDS path is per-spawn so each spawn re-points it).
//
//   - If a prior statusLine block exists and is NOT marked managed,
//     it's an OPERATOR config we must respect. We wrap-and-passthrough:
//     record the operator's `command` under `_termipod_wrapped_command`
//     on our marker block, and the shim's `--wrap <cmd>` flag invokes
//     it after posting telemetry. Operator config stays visible;
//     telemetry is additive (ADR-036 D1).
//
// Other settings.json keys (enableTelemetry, trustedWorkspaces, future
// agy additions) are preserved verbatim — `preTrustWorkspaceAntigravity`
// writes to the same file and the two functions must compose without
// stepping on each other's keys.
//
// agy's `statusLine` block also carries an `enabled: bool` field (host-
// verified — the binary ships `{"type":"","command":"","enabled":true}`
// as the empty default). We always set `enabled: true` alongside our
// command since installing a disabled hook is silently broken; the
// operator can use `/statusline off` if they want to disable us
// without uninstalling.
func installAntigravityStatusLine(udsSocket, hostRunnerExe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve HOME: %w", err)
	}
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir settings dir: %w", err)
	}

	settings := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &settings); err != nil {
				return fmt.Errorf("parse existing %s: %w", target, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", target, err)
	}

	// Determine the operator-wrapped command, if any (same logic as
	// installClaudeStatusLine).
	var wrappedCmd string
	if prior, ok := settings["statusLine"].(map[string]any); ok {
		managed, _ := prior[termipodManagedKey].(bool)
		if !managed {
			// Operator config — capture so the shim can passthrough.
			// agy stores the same {type, command, enabled} shape we
			// write, so we only need to wrap the `command` string.
			if priorCmd, _ := prior["command"].(string); priorCmd != "" {
				wrappedCmd = priorCmd
			}
		} else {
			// Already managed — preserve any prior wrapped command we
			// recorded (operator might have set it before we ever
			// touched the file).
			if priorWrap, _ := prior["_termipod_wrapped_command"].(string); priorWrap != "" {
				wrappedCmd = priorWrap
			}
		}
	}

	cmd := fmt.Sprintf("%s status-fire --socket %s",
		hostRunnerExe, shellQuote(udsSocket))
	if wrappedCmd != "" {
		cmd = fmt.Sprintf("%s --wrap %s", cmd, shellQuote(wrappedCmd))
	}

	block := map[string]any{
		"type":             "command",
		"command":          cmd,
		"enabled":          true, // agy-specific: explicit on/off toggle
		termipodManagedKey: true,
	}
	if wrappedCmd != "" {
		block["_termipod_wrapped_command"] = wrappedCmd
	}
	settings["statusLine"] = block

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(target, body, 0o600)
}

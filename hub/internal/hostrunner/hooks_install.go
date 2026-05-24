// settings.local.json hook installer — ADR-027 W6, rebuilt v1.0.659.
//
// Writes the 9 ADR-027 hook entries into `<workdir>/.claude/settings.local.json`
// so claude-code routes hook events through the host-runner UDS gateway.
//
// v1.0.659 rebuild — root-cause note. The original W6 emitted entries
// of shape `{"type": "mcp_tool", "tool": "mcp__termipod-host__hook_*"}`
// on the assumption that claude-code's hook schema would accept MCP
// tool references directly. claude-code's actual schema only supports
// `{"type": "command", "command": "<shell string>"}`, so every M4
// LocalLogTail spawn since v1.0.592 boot-errored with "Expected
// string, but received undefined" on first run — the entire file
// failed validation and claude refused to start. v1.0.659 routes
// around that by emitting `type: "command"` entries that invoke a
// host-runner stdio shim (`host-runner hook-fire --socket <uds>
// --event <Event>`) which forwards to the SAME gateway tools.
//
// The file may already exist (operator-supplied `.permissions.allow`
// rules, per-project model overrides, etc.); we merge by appending our
// matcher blocks under each event rather than overwriting, so user
// config survives. Other top-level keys (permissions, model,
// statusLine, …) are not touched. Any prior _termipod_managed matcher
// block — including the broken `type: "mcp_tool"` ones from
// pre-v1.0.659 spawns — is stripped before we append our fresh block,
// so a stale workdir self-heals on next spawn.
//
// Atomic write: marshalled JSON goes to a sibling temp file first and
// is rename(2)'d over the target. A crash partway through never leaves
// a half-written settings.local.json.
//
// Identification: every matcher block we own carries a stable
// "_termipod_managed": true marker. A future host-runner teardown pass
// (out of scope for MVP) can drop only the marked blocks and keep
// operator-authored hook entries intact.
package hostrunner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// claudeHookEvent enumerates the 9 events ADR-027 installs hooks for,
// paired with the local tool name on the host-runner UDS gateway and
// the per-event timeout in seconds. Timeouts mirror plan §5.C:
//   - PreCompact gets 300s because compaction approval needs human
//     decision time.
//   - PreToolUse gets 30s for the AskUserQuestion parked branch (other
//     PreToolUse calls return {} immediately, so the budget is unused).
//   - Everything else returns {} immediately and 5s is sufficient
//     transport headroom.
//
// Timeouts here cap the `mcp_tool` transport call; the actual park
// deadline is enforced host-runner-side in the gateway handler (plan
// §8 hook_park_default_ms = 60_000 ms for PreToolUse/PreCompact).
var claudeHookEvents = []struct {
	event   string // claude-code event name
	tool    string // local handler name on the host-runner UDS gateway
	timeout int    // seconds
}{
	{"PreToolUse", "hook_pre_tool_use", 30},
	{"PostToolUse", "hook_post_tool_use", 5},
	{"Notification", "hook_notification", 5},
	{"PreCompact", "hook_pre_compact", 300},
	{"Stop", "hook_stop", 5},
	{"SubagentStop", "hook_subagent_stop", 5},
	{"UserPromptSubmit", "hook_user_prompt", 5},
	{"SessionStart", "hook_session_start", 5},
	{"SessionEnd", "hook_session_end", 5},
}

// termipodManagedKey is the marker host-runner stamps on every matcher
// block it owns. A future teardown can find these to remove only the
// blocks we inserted, leaving operator-authored entries alone.
const termipodManagedKey = "_termipod_managed"

// preEnabledMcpServers is the set of MCP server names host-runner
// pre-grants in `<workdir>/.claude/settings.local.json` so claude-code
// doesn't open with its "Do you want to enable the <server> MCP
// server?" confirm dialog for every server in `<workdir>/.mcp.json`.
//
// Empirical confirmation: after a one-time interactive accept, claude
// writes `enabledMcpjsonServers: ["termipod","termipod-host"]` into
// this file itself, so pre-writing the same field is observationally
// equivalent to the user having already clicked through.
//
// Both names must stay in lockstep with writeMCPConfigClaudeCodeM4 —
// if a third server is ever added there, it also needs an entry here
// or its first-spawn UX regresses to a dialog. Kept as a constant slice
// rather than re-deriving from the writer to make that lockstep
// explicit at code-review time.
var preEnabledMcpServers = []string{"termipod", "termipod-host"}

// installClaudeHooks merges the 9 ADR-027 hook entries into
// `<workdir>/.claude/settings.local.json`. The workdir must already
// exist (we mkdir-p the `.claude` subdir). If the settings file is
// absent we create it with just our hooks block; if present we parse
// it, augment `.hooks`, and atomically rewrite.
//
// hookFireExe + udsSocket parameterise the spawned shim command:
//   - hookFireExe is the absolute path of the host-runner binary
//     (so the command works even if PATH doesn't carry it). Call
//     with "host-runner" to keep the basename-resolution semantics.
//   - udsSocket is the per-spawn UDS path the gateway is listening
//     on; baked into each hook command so claude doesn't need to
//     know the path itself.
//
// Idempotency: re-running for the same workdir does NOT duplicate
// entries — for every event we drop any prior matcher block whose
// `_termipod_managed` marker is true before appending the fresh one.
// Pre-v1.0.659 entries (type: "mcp_tool") carry the same marker, so
// the stale-workdir self-heal kicks in on the first v1.0.659 spawn.
func installClaudeHooks(workdir, hookFireExe, udsSocket string) error {
	claudeDir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("mkdir .claude: %w", err)
	}
	target := filepath.Join(claudeDir, "settings.local.json")

	settings := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &settings); err != nil {
				return fmt.Errorf("parse existing %s: %w", target, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", target, err)
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}

	for _, e := range claudeHookEvents {
		cmd := fmt.Sprintf("%s hook-fire --socket %s --event %s",
			hookFireExe, shellQuote(udsSocket), e.event)
		hooks[e.event] = appendTermipodMatcher(hooks[e.event], cmd, hookFireExe, e.timeout)
	}

	// Pre-grant termipod + termipod-host so the per-server MCP confirm
	// dialog doesn't fire on first attach. Merge with any prior value
	// the operator set: keep their entries, add ours if missing,
	// dedupe. Drop any disabled entries we manage (so a user who
	// previously denied us via the dialog doesn't get permanently
	// locked out the next time the workdir is re-spawned).
	settings["enabledMcpjsonServers"] = mergeEnabledMcpServers(settings["enabledMcpjsonServers"])
	if disabled, ok := settings["disabledMcpjsonServers"]; ok {
		settings["disabledMcpjsonServers"] = removeManagedFromDisabled(disabled)
	}

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(target, body, 0o644)
}

// appendTermipodMatcher returns the prior list of matcher blocks for
// an event, stripped of any termipod-managed entries, plus our fresh
// matcher block on the end. A nil/empty/non-array prior value is
// treated as "no existing config" and replaced by a single-element
// array containing only our block.
//
// The matcher value is the empty string per claude-code's hook schema:
// "" matches all tools, equivalent to the deprecated "*" form. We
// emitted "*" pre-v1.0.659; both still work but "" matches the
// canonical example from claude's hook docs / error messages.
//
// Idempotency notes:
//   - First-line identifier is the `_termipod_managed: true` marker
//     on the matcher object — preserved across our own writes.
//   - Backup identifier (v1.0.663): the inner hooks[].command begins
//     with `<hookFireExe> hook-fire `. Claude-code rewrites this file
//     when the operator clicks through MCP / permission-mode dialogs
//     (writes `enabledMcpjsonServers`, etc.) and drops unknown keys
//     like our marker. Without the backup match, the next spawn
//     appends a SECOND entry pointing at the new UDS socket while
//     leaving the prior (now-dead-socket) one in place — every hook
//     event fires twice, and the first invocation always fails
//     because its socket no longer exists. Detected on the dev box
//     v1.0.662 smoke: every Stop/PreToolUse/etc landed as TWO
//     attachment rows in the JSONL.
func appendTermipodMatcher(prior any, command, hookFireExe string, timeout int) []any {
	out := []any{}
	if arr, ok := prior.([]any); ok {
		for _, b := range arr {
			m, ok := b.(map[string]any)
			if !ok {
				out = append(out, b)
				continue
			}
			if managed, _ := m[termipodManagedKey].(bool); managed {
				continue
			}
			if isManagedByCommandShape(m, hookFireExe) {
				continue
			}
			out = append(out, b)
		}
	}
	out = append(out, map[string]any{
		"matcher":          "",
		termipodManagedKey: true,
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": command,
			"timeout": timeout,
		}},
	})
	return out
}

// isManagedByCommandShape returns true when a matcher block's inner
// hooks[].command starts with `<hookFireExe> hook-fire ` — the exact
// shape host-runner installs. Used as a backup identifier when the
// `_termipod_managed: true` marker has been stripped (claude rewrites
// settings.local.json without preserving keys it doesn't know about,
// e.g. when the operator clicks through the MCP enable dialog).
func isManagedByCommandShape(m map[string]any, hookFireExe string) bool {
	hooks, _ := m["hooks"].([]any)
	if len(hooks) == 0 {
		return false
	}
	// Any one hook entry matching is enough — the matcher block is
	// ours by composition (operator-authored blocks would have their
	// own command).
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := hm["type"].(string)
		if typ != "command" {
			continue
		}
		cmd, _ := hm["command"].(string)
		// Match by `<exe> hook-fire ` prefix — covers any UDS path
		// or event suffix. Use a space after `hook-fire` so a future
		// `hook-fire-debug` doesn't accidentally match.
		if cmd == "" {
			continue
		}
		prefix := hookFireExe + " hook-fire "
		if len(cmd) >= len(prefix) && cmd[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// mergeEnabledMcpServers folds preEnabledMcpServers into the prior
// `enabledMcpjsonServers` value, preserving anything the operator
// added. Returns a []any (JSON-marshalable as an array) so the
// `settings` map round-trips cleanly through encoding/json without
// type-juggling at the call site.
func mergeEnabledMcpServers(prior any) []any {
	seen := map[string]bool{}
	out := []any{}
	if arr, ok := prior.([]any); ok {
		for _, v := range arr {
			s, ok := v.(string)
			if !ok || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range preEnabledMcpServers {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// removeManagedFromDisabled strips our managed server names from a
// `disabledMcpjsonServers` value while preserving operator-disabled
// entries. Returns a []any for the same round-trip reason as
// mergeEnabledMcpServers.
func removeManagedFromDisabled(prior any) []any {
	managed := map[string]bool{}
	for _, s := range preEnabledMcpServers {
		managed[s] = true
	}
	out := []any{}
	arr, ok := prior.([]any)
	if !ok {
		return out
	}
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			out = append(out, v)
			continue
		}
		if managed[s] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// (shell-quoting of the UDS path goes through markers.go:shellQuote;
// claude-code parses each `command` with POSIX sh, so any whitespace
// or special chars in the path would otherwise break the argv split.)

// installClaudeStatusLine merges a `statusLine` block into
// `<workdir>/.claude/settings.local.json` pointing at the host-runner
// `status-fire` shim against the per-spawn UDS gateway (ADR-036 W1).
//
// Idempotent + wrap-and-passthrough:
//
//  - If no prior statusLine block exists, we write ours with the
//    `_termipod_managed: true` marker.
//
//  - If a prior statusLine block exists AND it's already marked
//    `_termipod_managed: true`, we just update the `command` line
//    (the UDS path may have changed across spawns).
//
//  - If a prior statusLine block exists and is NOT marked managed,
//    it's an OPERATOR config we must respect. We wrap-and-passthrough:
//    record the operator's `command` under `_termipod_wrapped_command`
//    on our marker block, and the shim's `--wrap <cmd>` flag invokes
//    it after posting telemetry to the gateway (the operator's stdout
//    becomes the rendered status text). Operator config stays visible;
//    telemetry is additive (ADR-036 D1).
//
// hostRunnerExe + udsSocket parameterise the spawned shim command,
// same shape as installClaudeHooks above. Atomic write via
// atomicWriteFile so the file is never observed half-written.
func installClaudeStatusLine(workdir, hostRunnerExe, udsSocket string) error {
	claudeDir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("mkdir .claude: %w", err)
	}
	target := filepath.Join(claudeDir, "settings.local.json")

	settings := map[string]any{}
	if raw, err := os.ReadFile(target); err == nil {
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &settings); err != nil {
				return fmt.Errorf("parse existing %s: %w", target, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", target, err)
	}

	// Determine the operator-wrapped command, if any. We only wrap a
	// prior block we didn't author; an already-managed block just
	// gets re-stamped with the current UDS path.
	var wrappedCmd string
	if prior, ok := settings["statusLine"].(map[string]any); ok {
		managed, _ := prior[termipodManagedKey].(bool)
		if !managed {
			// Operator config — record their `command` so the shim can
			// passthrough. We don't try to parse out their type/etc;
			// `type: "command"` is the only supported value today and
			// that's what we re-emit.
			if priorCmd, _ := prior["command"].(string); priorCmd != "" {
				wrappedCmd = priorCmd
			}
		} else {
			// Already managed — preserve any prior wrapped command we
			// recorded on a previous spawn (operator might have set it
			// before we ever touched the file, and a re-install
			// shouldn't drop the wrapper).
			if priorWrap, _ := prior["_termipod_wrapped_command"].(string); priorWrap != "" {
				wrappedCmd = priorWrap
			}
		}
	}

	cmd := fmt.Sprintf("%s status-fire --socket %s",
		hostRunnerExe, shellQuote(udsSocket))
	if wrappedCmd != "" {
		// shellQuote handles single-quote escaping for paths; the wrap
		// value is an arbitrary shell command, so it gets the same
		// treatment (claude-code parses `command` with POSIX sh).
		cmd = fmt.Sprintf("%s --wrap %s", cmd, shellQuote(wrappedCmd))
	}

	block := map[string]any{
		"type":             "command",
		"command":          cmd,
		termipodManagedKey: true,
	}
	if wrappedCmd != "" {
		// Preserve the wrapped command so a re-install (which may pass
		// us no operator context) doesn't lose it.
		block["_termipod_wrapped_command"] = wrappedCmd
	}
	settings["statusLine"] = block

	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(target, body, 0o644)
}

// atomicWriteFile writes body to <target>.<pid>.tmp then renames over
// target. The rename is atomic on POSIX filesystems even across
// crashes, so a half-written settings.local.json is never observable.
func atomicWriteFile(target string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.%d.tmp",
		filepath.Base(target), os.Getpid()))
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

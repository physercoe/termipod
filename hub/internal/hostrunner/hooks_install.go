// settings.local.json hook installer — ADR-027 W6.
//
// Writes the 9 ADR-027 hook entries into `<workdir>/.claude/settings.local.json`
// so claude-code routes hook events through the host-runner UDS gateway
// (`mcp__termipod-host__hook_*`, served by mcp_gateway.go W5b). The file
// may already exist (operator-supplied `.permissions.allow` rules,
// per-project model overrides, etc.); we merge by appending our matcher
// blocks under each event rather than overwriting, so user config
// survives. Other top-level keys (permissions, model, statusLine, …)
// are not touched.
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

	"github.com/termipod/hub"
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

// installClaudeHooks merges the 9 ADR-027 hook entries into
// `<workdir>/.claude/settings.local.json`. The workdir must already
// exist (we mkdir-p the `.claude` subdir). If the settings file is
// absent we create it with just our hooks block; if present we parse
// it, augment `.hooks`, and atomically rewrite.
//
// Idempotency: re-running for the same workdir does NOT duplicate
// entries — for every event we drop any prior matcher block whose
// `_termipod_managed` marker is true before appending the fresh one.
func installClaudeHooks(workdir string) error {
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
		hooks[e.event] = appendTermipodMatcher(hooks[e.event], e.tool, e.timeout)
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
func appendTermipodMatcher(prior any, tool string, timeout int) []any {
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
			out = append(out, b)
		}
	}
	out = append(out, map[string]any{
		"matcher":          "*",
		termipodManagedKey: true,
		"hooks": []any{map[string]any{
			"type":    "mcp_tool",
			"tool":    fmt.Sprintf("mcp__%s__%s", hub.MCPServerNameHost, tool),
			"timeout": timeout,
		}},
	})
	return out
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

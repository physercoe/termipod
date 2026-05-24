package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test fixtures — the same path string everywhere so the substring
// assertions below stay short.
const (
	testHookFireExe = "host-runner"
	testUDSPath     = "/tmp/termipod-agent-abc.sock"
)

type parsedHookSettings struct {
	Permissions            any            `json:"permissions,omitempty"`
	Model                  any            `json:"model,omitempty"`
	StatusLine             any            `json:"statusLine,omitempty"`
	Hooks                  map[string]any `json:"hooks"`
	EnabledMcpjsonServers  []string       `json:"enabledMcpjsonServers,omitempty"`
	DisabledMcpjsonServers []string       `json:"disabledMcpjsonServers,omitempty"`
}

func readSettings(t *testing.T, path string) parsedHookSettings {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var p parsedHookSettings
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("parse: %v (body=%s)", err, raw)
	}
	return p
}

// extractCommands returns the `command` strings from every termipod-
// managed hook entry under `event`. Replaces the pre-v1.0.659 helper
// that pulled `tool` strings — that field no longer exists on the
// type:"command" hook shape.
func extractCommands(t *testing.T, hooks map[string]any, event string) []string {
	t.Helper()
	arr, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var cmds []string
	for _, b := range arr {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		hooksArr, _ := m["hooks"].([]any)
		for _, h := range hooksArr {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); cmd != "" {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// Hook entries MUST serialise to claude-code's documented schema:
// {matcher, hooks[]} where each hooks[] element is {type:"command",
// command:<string>, timeout?:<int>}. Pre-v1.0.659 wrote type:"mcp_tool"
// + tool:<...> which claude-code rejected with "Expected string, but
// received undefined" at first start.
func TestInstallClaudeHooks_NewFile_ValidSchema(t *testing.T) {
	workdir := t.TempDir()
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	if len(p.Hooks) != 9 {
		t.Errorf("hook events = %d, want 9 (got keys %v)", len(p.Hooks), mapKeysAny(p.Hooks))
	}

	// Every termipod-managed matcher block must obey claude-code's
	// schema: matcher present (string), hooks[] present (array), and
	// each element MUST have type="command" + command:<string>.
	for event, raw := range p.Hooks {
		arr, _ := raw.([]any)
		for i, b := range arr {
			m, _ := b.(map[string]any)
			if m == nil {
				t.Errorf("%s[%d] not an object: %v", event, i, b)
				continue
			}
			if _, hasMatcher := m["matcher"].(string); !hasMatcher {
				t.Errorf("%s[%d] missing string `matcher`: %v", event, i, m)
			}
			hs, _ := m["hooks"].([]any)
			if len(hs) == 0 {
				t.Errorf("%s[%d] empty `hooks` array", event, i)
				continue
			}
			for j, h := range hs {
				hm, _ := h.(map[string]any)
				if t_, _ := hm["type"].(string); t_ != "command" {
					t.Errorf("%s[%d].hooks[%d] type = %q, want \"command\"", event, i, j, t_)
				}
				if cmd, _ := hm["command"].(string); cmd == "" {
					t.Errorf("%s[%d].hooks[%d] empty `command`: %v", event, i, j, hm)
				}
			}
		}
	}

	// The PreToolUse command should reference our shim + UDS path +
	// the right event name. Substring asserts (don't lock the exact
	// arg order, which could legitimately change).
	cmds := extractCommands(t, p.Hooks, "PreToolUse")
	if len(cmds) != 1 {
		t.Fatalf("PreToolUse commands = %d, want 1; got %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "host-runner hook-fire") {
		t.Errorf("PreToolUse command missing shim invocation: %q", cmds[0])
	}
	if !strings.Contains(cmds[0], "--event PreToolUse") {
		t.Errorf("PreToolUse command missing --event PreToolUse: %q", cmds[0])
	}
	if !strings.Contains(cmds[0], testUDSPath) {
		t.Errorf("PreToolUse command missing UDS path %q: %q", testUDSPath, cmds[0])
	}
}

func TestInstallClaudeHooks_PreservesOperatorKeys(t *testing.T) {
	workdir := t.TempDir()
	claudeDir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{
  "permissions": {"allow": ["Bash(git push *)"]},
  "model": "claude-sonnet-4-5",
  "statusLine": {"type": "command", "command": "echo hi"}
}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), existing, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(claudeDir, "settings.local.json"))
	perms, _ := p.Permissions.(map[string]any)
	if perms == nil || len(perms) == 0 {
		t.Errorf("permissions key lost; got %v", p.Permissions)
	}
	if p.Model != "claude-sonnet-4-5" {
		t.Errorf("model key lost; got %v", p.Model)
	}
	if p.StatusLine == nil {
		t.Errorf("statusLine key lost")
	}
	if len(p.Hooks) != 9 {
		t.Errorf("hooks not installed alongside operator keys; got %d events", len(p.Hooks))
	}
}

func TestInstallClaudeHooks_PreservesUserHooksInSameEvent(t *testing.T) {
	workdir := t.TempDir()
	claudeDir := filepath.Join(workdir, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	existing := []byte(`{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{"type": "command", "command": "echo user-hook"}]
    }]
  }
}`)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), existing, 0o644)

	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(claudeDir, "settings.local.json"))
	arr, ok := p.Hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatalf("PreToolUse not array; got %T", p.Hooks["PreToolUse"])
	}
	if len(arr) != 2 {
		t.Fatalf("PreToolUse blocks = %d, want 2 (user + termipod); got %v", len(arr), arr)
	}
	// User hook (no termipod marker) must still be present.
	userM, _ := arr[0].(map[string]any)
	if userM == nil || userM["matcher"] != "Bash" {
		t.Errorf("user hook displaced; arr[0] = %v", arr[0])
	}
	// Our hook is the appended one with _termipod_managed=true.
	ourM, _ := arr[1].(map[string]any)
	if ourM == nil {
		t.Fatalf("our hook missing")
	}
	if managed, _ := ourM[termipodManagedKey].(bool); !managed {
		t.Errorf("appended block missing %s marker: %v", termipodManagedKey, ourM)
	}
}

// Stale workdirs from pre-v1.0.659 spawns hold the invalid `type:
// "mcp_tool"` entries that broke claude's schema validator. The next
// v1.0.659 spawn MUST self-heal by stripping the prior _termipod_managed
// blocks before appending fresh `type: "command"` ones — no leftover
// from the old shape.
func TestInstallClaudeHooks_SelfHealsStaleMcpToolEntries(t *testing.T) {
	workdir := t.TempDir()
	claudeDir := filepath.Join(workdir, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	// Simulate a pre-v1.0.659 settings.local.json with the broken
	// mcp_tool hook entry that claude refuses to validate.
	stale := []byte(`{
  "hooks": {
    "Stop": [{
      "matcher": "*",
      "_termipod_managed": true,
      "hooks": [{"type": "mcp_tool", "tool": "mcp__termipod-host__hook_stop", "timeout": 5}]
    }]
  }
}`)
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), stale, 0o644)

	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(claudeDir, "settings.local.json"))
	arr, _ := p.Hooks["Stop"].([]any)
	if len(arr) != 1 {
		t.Fatalf("Stop matcher blocks = %d, want 1 (the stale block must be stripped, fresh appended): %v",
			len(arr), arr)
	}
	m, _ := arr[0].(map[string]any)
	hs, _ := m["hooks"].([]any)
	if len(hs) != 1 {
		t.Fatalf("Stop hooks[] = %d, want 1: %v", len(hs), hs)
	}
	h, _ := hs[0].(map[string]any)
	if h["type"] != "command" {
		t.Errorf("Stop hook type = %v, want \"command\" (stale mcp_tool not stripped)", h["type"])
	}
	if _, hasTool := h["tool"]; hasTool {
		t.Errorf("Stop hook still carries pre-v1.0.659 `tool` field: %v", h)
	}
}

func TestInstallClaudeHooks_IsIdempotent(t *testing.T) {
	workdir := t.TempDir()
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("second install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	// Each event should still have exactly one matcher block.
	for event, raw := range p.Hooks {
		arr, _ := raw.([]any)
		if len(arr) != 1 {
			t.Errorf("event %s has %d blocks after re-install, want 1", event, len(arr))
		}
	}
}

func TestInstallClaudeHooks_TimeoutPerEvent(t *testing.T) {
	workdir := t.TempDir()
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	got := map[string]float64{}
	for _, e := range claudeHookEvents {
		arr, _ := p.Hooks[e.event].([]any)
		if len(arr) == 0 {
			continue
		}
		m, _ := arr[0].(map[string]any)
		hs, _ := m["hooks"].([]any)
		if len(hs) == 0 {
			continue
		}
		h, _ := hs[0].(map[string]any)
		got[e.event], _ = h["timeout"].(float64) // json unmarshals numbers as float64
	}
	if got["PreCompact"] != 300 {
		t.Errorf("PreCompact timeout = %v, want 300", got["PreCompact"])
	}
	if got["PreToolUse"] != 30 {
		t.Errorf("PreToolUse timeout = %v, want 30", got["PreToolUse"])
	}
	if got["Stop"] != 5 {
		t.Errorf("Stop timeout = %v, want 5", got["Stop"])
	}
}

func mapKeysAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// v1.0.661 — pre-grant `enabledMcpjsonServers` so a fresh workdir
// spawn doesn't open with the per-server MCP confirm dialog (one
// click per server, no mobile affordance to dismiss). The hook
// installer is the right place: it already owns settings.local.json
// for the same workdir, and the field IS valid in that file
// (verified against ~/.claude.json — claude writes the same key
// there when the operator clicks "yes" interactively).
func TestInstallClaudeHooks_PreGrantsMcpServers_FreshWorkdir(t *testing.T) {
	workdir := t.TempDir()
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	want := map[string]bool{"termipod": true, "termipod-host": true}
	got := map[string]bool{}
	for _, s := range p.EnabledMcpjsonServers {
		got[s] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("enabledMcpjsonServers missing %q; got %v", k, p.EnabledMcpjsonServers)
		}
	}
}

// Idempotency: a second install MUST NOT duplicate entries.
func TestInstallClaudeHooks_PreGrantsMcpServers_NoDuplicates(t *testing.T) {
	workdir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	if len(p.EnabledMcpjsonServers) != 2 {
		t.Errorf("enabledMcpjsonServers grew on repeat install: %v", p.EnabledMcpjsonServers)
	}
}

// Operator pre-existing entries MUST survive — we add ours, we don't
// overwrite.
func TestInstallClaudeHooks_PreGrantsMcpServers_PreservesOperator(t *testing.T) {
	workdir := t.TempDir()
	claudeDir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{"enabledMcpjsonServers":["my-private-mcp","another"]}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), existing, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	got := map[string]bool{}
	for _, s := range p.EnabledMcpjsonServers {
		got[s] = true
	}
	for _, want := range []string{"my-private-mcp", "another", "termipod", "termipod-host"} {
		if !got[want] {
			t.Errorf("merged list missing %q; got %v", want, p.EnabledMcpjsonServers)
		}
	}
}

// If a prior dialog landed our servers in the disabled list (operator
// clicked "no" once), the installer must lift the block on the next
// spawn — otherwise the workdir is permanently broken until manual
// edit.
func TestInstallClaudeHooks_PreGrantsMcpServers_LiftsPriorDeny(t *testing.T) {
	workdir := t.TempDir()
	claudeDir := filepath.Join(workdir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{"disabledMcpjsonServers":["termipod","operator-blocked"]}`)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), existing, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installClaudeHooks(workdir, testHookFireExe, testUDSPath); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	for _, s := range p.DisabledMcpjsonServers {
		if s == "termipod" || s == "termipod-host" {
			t.Errorf("our managed server %q still in disabledMcpjsonServers: %v", s, p.DisabledMcpjsonServers)
		}
	}
	// Operator-blocked entries that we don't manage must stay.
	gotOperator := false
	for _, s := range p.DisabledMcpjsonServers {
		if s == "operator-blocked" {
			gotOperator = true
		}
	}
	if !gotOperator {
		t.Errorf("operator-blocked entry dropped from disabledMcpjsonServers: %v", p.DisabledMcpjsonServers)
	}
}

// The merge helpers must round-trip JSON values cleanly — encoding a
// []any of strings emits a JSON array of strings (not numeric indices
// or a stringified map). Locks the surface against accidental
// any→string type drift.
func TestMergeEnabledMcpServers_JSONEncodable(t *testing.T) {
	merged := mergeEnabledMcpServers([]any{"alpha"})
	raw, err := json.Marshal(map[string]any{"enabledMcpjsonServers": merged})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `"alpha"`
	if !strings.Contains(string(raw), want) {
		t.Errorf("encoded list does not contain %s: %s", want, raw)
	}
}

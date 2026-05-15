package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/termipod/hub"
)

type parsedHookSettings struct {
	Permissions any            `json:"permissions,omitempty"`
	Model       any            `json:"model,omitempty"`
	StatusLine  any            `json:"statusLine,omitempty"`
	Hooks       map[string]any `json:"hooks"`
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

func extractToolNames(t *testing.T, hooks map[string]any, event string) []string {
	t.Helper()
	arr, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, b := range arr {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		hooksArr, _ := m["hooks"].([]any)
		for _, h := range hooksArr {
			hm, _ := h.(map[string]any)
			if name, _ := hm["tool"].(string); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func TestInstallClaudeHooks_NewFile(t *testing.T) {
	workdir := t.TempDir()
	if err := installClaudeHooks(workdir); err != nil {
		t.Fatalf("install: %v", err)
	}
	p := readSettings(t, filepath.Join(workdir, ".claude", "settings.local.json"))
	if len(p.Hooks) != 9 {
		t.Errorf("hook events = %d, want 9 (got keys %v)", len(p.Hooks), mapKeysAny(p.Hooks))
	}
	want := "mcp__" + hub.MCPServerNameHost + "__hook_pre_tool_use"
	got := extractToolNames(t, p.Hooks, "PreToolUse")
	if len(got) != 1 || got[0] != want {
		t.Errorf("PreToolUse tools = %v, want [%s]", got, want)
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
	if err := installClaudeHooks(workdir); err != nil {
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

	if err := installClaudeHooks(workdir); err != nil {
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

func TestInstallClaudeHooks_IsIdempotent(t *testing.T) {
	workdir := t.TempDir()
	if err := installClaudeHooks(workdir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := installClaudeHooks(workdir); err != nil {
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
	if err := installClaudeHooks(workdir); err != nil {
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

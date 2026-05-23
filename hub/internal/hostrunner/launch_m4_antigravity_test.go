package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// preTrustWorkspaceAntigravity must append a workdir to the
// trustedWorkspaces list in agy's settings.json, preserving any other
// keys (enableTelemetry, statusLine, future additions) and deduplicating
// so repeat launches don't grow the list.
func TestPreTrustWorkspaceAntigravity_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")

	// Existing settings — must be preserved untouched.
	initial := map[string]any{
		"enableTelemetry": false,
		"statusLine": map[string]any{
			"type":    "",
			"command": "",
			"enabled": true,
		},
		"trustedWorkspaces": []any{"/home/ubuntu/agytest"},
	}
	mustWriteJSON(t, settingsPath, initial)

	workdir := "/home/ubuntu/hub-work/antigravity"
	if err := preTrustWorkspaceAntigravity(workdir); err != nil {
		t.Fatalf("first call: %v", err)
	}

	got := mustReadJSON(t, settingsPath)
	wantList := []any{"/home/ubuntu/agytest", "/home/ubuntu/hub-work/antigravity"}
	if !reflect.DeepEqual(got["trustedWorkspaces"], wantList) {
		t.Errorf("trustedWorkspaces = %v; want %v", got["trustedWorkspaces"], wantList)
	}
	if got["enableTelemetry"] != false {
		t.Errorf("enableTelemetry lost: got %v", got["enableTelemetry"])
	}
	if _, ok := got["statusLine"].(map[string]any); !ok {
		t.Errorf("statusLine lost or wrong type: %v", got["statusLine"])
	}

	// Second call must be a no-op on the list (idempotent re-spawn).
	if err := preTrustWorkspaceAntigravity(workdir); err != nil {
		t.Fatalf("second call: %v", err)
	}
	got2 := mustReadJSON(t, settingsPath)
	if !reflect.DeepEqual(got2["trustedWorkspaces"], wantList) {
		t.Errorf("after re-call trustedWorkspaces = %v; want %v", got2["trustedWorkspaces"], wantList)
	}
}

// A missing settings.json (fresh box) is treated as the empty config —
// the function creates the file with just the trustedWorkspaces entry.
func TestPreTrustWorkspaceAntigravity_FreshBox(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := "/home/ubuntu/hub-work/antigravity"
	if err := preTrustWorkspaceAntigravity(workdir); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	got := mustReadJSON(t, filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	if !reflect.DeepEqual(got["trustedWorkspaces"], []any{workdir}) {
		t.Errorf("trustedWorkspaces = %v; want [%q]", got["trustedWorkspaces"], workdir)
	}
}

// A workdir with a trailing slash / relative-style path must dedupe
// against the clean form (agy stores the cleaned absolute path).
func TestPreTrustWorkspaceAntigravity_DedupesClean(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	mustWriteJSON(t, settingsPath, map[string]any{
		"trustedWorkspaces": []any{"/home/ubuntu/hub-work/antigravity"},
	})

	// Same path, trailing slash.
	if err := preTrustWorkspaceAntigravity("/home/ubuntu/hub-work/antigravity/"); err != nil {
		t.Fatal(err)
	}
	got := mustReadJSON(t, settingsPath)
	if l, ok := got["trustedWorkspaces"].([]any); !ok || len(l) != 1 {
		t.Errorf("expected single-element list after dedup; got %v", got["trustedWorkspaces"])
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

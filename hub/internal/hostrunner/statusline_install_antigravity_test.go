package hostrunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Sets HOME to t.TempDir() for the duration of the test so the
// install function targets a writable per-test path rather than the
// operator's real ~/.gemini directory.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// G1: a fresh install (no prior settings.json) writes a managed block
// pointing at host-runner status-fire with the per-spawn UDS path.
// Other settings keys don't exist yet so this is the cleanest path.
func TestInstallAntigravityStatusLine_FreshInstall(t *testing.T) {
	home := withTempHome(t)
	if err := installAntigravityStatusLine("/run/termipod/agy-abc.sock", "/usr/local/bin/host-runner"); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := readJSON(t, filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
	sl, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing from settings: %v", got)
	}
	if want := "command"; sl["type"] != want {
		t.Errorf("type = %v; want %v", sl["type"], want)
	}
	// shellQuote always single-quotes (POSIX literal); the quoting is
	// load-bearing for paths with shell metachars so we test the
	// verbatim emitted shape, not a "would parse the same" check.
	wantCmd := "/usr/local/bin/host-runner status-fire --socket '/run/termipod/agy-abc.sock'"
	if sl["command"] != wantCmd {
		t.Errorf("command = %v; want %v", sl["command"], wantCmd)
	}
	if sl["enabled"] != true {
		t.Errorf("enabled = %v; want true", sl["enabled"])
	}
	if sl["_termipod_managed"] != true {
		t.Errorf("missing _termipod_managed marker: %v", sl)
	}
	if _, present := sl["_termipod_wrapped_command"]; present {
		t.Errorf("unexpected wrapped command on fresh install: %v", sl)
	}
}

// G1: re-install over a prior MANAGED block updates `command` (the UDS
// path may change across spawns) but doesn't lose the marker or
// re-wrap anything.
func TestInstallAntigravityStatusLine_ReinstallManaged(t *testing.T) {
	home := withTempHome(t)
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	priorOK := map[string]any{
		"enableTelemetry":   false,
		"trustedWorkspaces": []any{"/home/op/agytest"},
		"statusLine": map[string]any{
			"type":              "command",
			"command":           "/old/host-runner status-fire --socket /run/termipod/agy-old.sock",
			"enabled":           true,
			"_termipod_managed": true,
		},
	}
	writeJSON(t, target, priorOK)

	if err := installAntigravityStatusLine("/run/termipod/agy-NEW.sock", "/new/host-runner"); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	got := readJSON(t, target)
	sl := got["statusLine"].(map[string]any)
	wantCmd := "/new/host-runner status-fire --socket '/run/termipod/agy-NEW.sock'"
	if sl["command"] != wantCmd {
		t.Errorf("command = %v; want %v", sl["command"], wantCmd)
	}
	// Sibling keys preserved.
	if got["enableTelemetry"] != false {
		t.Errorf("enableTelemetry lost: %v", got)
	}
	tw, _ := got["trustedWorkspaces"].([]any)
	if len(tw) != 1 || tw[0] != "/home/op/agytest" {
		t.Errorf("trustedWorkspaces lost: %v", got)
	}
}

// G1 (wrap-and-passthrough): an operator-set statusLine (no managed
// marker) gets wrapped under _termipod_wrapped_command and the shim's
// --wrap flag is added so operator stdout still renders.
func TestInstallAntigravityStatusLine_WrapsOperatorCommand(t *testing.T) {
	home := withTempHome(t)
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	operator := map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/opt/operator/my-statusline.sh",
			"enabled": true,
			// No _termipod_managed marker → operator config.
		},
	}
	writeJSON(t, target, operator)

	if err := installAntigravityStatusLine("/run/termipod/agy-X.sock", "/usr/local/bin/host-runner"); err != nil {
		t.Fatalf("install: %v", err)
	}
	got := readJSON(t, target)
	sl := got["statusLine"].(map[string]any)

	if sl["_termipod_managed"] != true {
		t.Errorf("missing managed marker after wrap: %v", sl)
	}
	wantWrap := "/opt/operator/my-statusline.sh"
	if sl["_termipod_wrapped_command"] != wantWrap {
		t.Errorf("wrapped command = %v; want %v", sl["_termipod_wrapped_command"], wantWrap)
	}
	wantCmd := "/usr/local/bin/host-runner status-fire --socket '/run/termipod/agy-X.sock' --wrap '/opt/operator/my-statusline.sh'"
	if sl["command"] != wantCmd {
		t.Errorf("command = %q; want %q", sl["command"], wantCmd)
	}
}

// G1: a previously-managed install that recorded a wrapped operator
// command preserves the wrap across re-install (operator might have
// removed their entry before we re-installed, but we shouldn't drop
// the wrapper if the marker says we wrapped one).
func TestInstallAntigravityStatusLine_PreservesPriorWrap(t *testing.T) {
	home := withTempHome(t)
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	prior := map[string]any{
		"statusLine": map[string]any{
			"type":                      "command",
			"command":                   "/old/host-runner status-fire --socket /run/termipod/agy-old.sock --wrap /opt/operator/my-statusline.sh",
			"enabled":                   true,
			"_termipod_managed":         true,
			"_termipod_wrapped_command": "/opt/operator/my-statusline.sh",
		},
	}
	writeJSON(t, target, prior)

	if err := installAntigravityStatusLine("/run/termipod/agy-Y.sock", "/usr/local/bin/host-runner"); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	got := readJSON(t, target)
	sl := got["statusLine"].(map[string]any)
	if sl["_termipod_wrapped_command"] != "/opt/operator/my-statusline.sh" {
		t.Errorf("wrap dropped on reinstall: %v", sl)
	}
	wantCmd := "/usr/local/bin/host-runner status-fire --socket '/run/termipod/agy-Y.sock' --wrap '/opt/operator/my-statusline.sh'"
	if sl["command"] != wantCmd {
		t.Errorf("command = %q; want %q", sl["command"], wantCmd)
	}
}

// G1 + sibling-compose: installAntigravityStatusLine and
// preTrustWorkspaceAntigravity both write to the same settings.json
// — they must not clobber each other's keys. Compose order: trust
// first, then statusLine, then trust again (idempotent).
func TestInstallAntigravityStatusLine_ComposesWithPreTrust(t *testing.T) {
	home := withTempHome(t)
	target := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")

	if err := preTrustWorkspaceAntigravity("/home/op/agytest"); err != nil {
		t.Fatalf("pre-trust: %v", err)
	}
	if err := installAntigravityStatusLine("/run/termipod/agy-Z.sock", "/usr/local/bin/host-runner"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := preTrustWorkspaceAntigravity("/home/op/agytest2"); err != nil {
		t.Fatalf("pre-trust 2: %v", err)
	}
	got := readJSON(t, target)
	// statusLine survived second pre-trust.
	if _, ok := got["statusLine"].(map[string]any); !ok {
		t.Errorf("statusLine lost after subsequent pre-trust: %v", got)
	}
	tw, _ := got["trustedWorkspaces"].([]any)
	if len(tw) != 2 {
		t.Errorf("trustedWorkspaces count = %d; want 2 (%v)", len(tw), tw)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return out
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

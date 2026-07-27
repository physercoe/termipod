package hostrunner

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Exercises the setup-script launch injection (env-profiles plan, E1c): the
// newline-free command fragment spliced into every driving mode's launch
// string, and the synchronous one-shot runner the gemini exec path uses.

func TestSetupScriptPrefix(t *testing.T) {
	// Empty script → no-op, no file.
	pre, path, err := setupScriptPrefix("", "fail", "agent1")
	if err != nil || pre != "" || path != "" {
		t.Fatalf("empty: pre=%q path=%q err=%v", pre, path, err)
	}

	// Fail-closed (default): `bash '<path>' && ` and the file holds the script.
	pre, path, err = setupScriptPrefix("echo hi\nmkdir -p out", "fail", "agent1")
	if err != nil {
		t.Fatalf("fail policy: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if !strings.HasPrefix(pre, "bash ") || !strings.HasSuffix(pre, " && ") {
		t.Fatalf("fail prefix shape: %q", pre)
	}
	if strings.Contains(pre, "\n") {
		t.Fatalf("prefix must be newline-free (safe for tmux/send-keys): %q", pre)
	}
	body, _ := os.ReadFile(path)
	if string(body) != "echo hi\nmkdir -p out" {
		t.Fatalf("script file content: %q", string(body))
	}

	// Continue: wrapped so a non-zero exit doesn't break the && chain.
	pre, path2, err := setupScriptPrefix("false", "continue", "agent1")
	if err != nil {
		t.Fatalf("continue policy: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path2) })
	if !strings.HasPrefix(pre, "{ bash ") || !strings.Contains(pre, "|| true; } && ") {
		t.Fatalf("continue prefix shape: %q", pre)
	}
}

func TestRunSetupScriptOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Success: writes a marker into the workdir.
	if err := runSetupScriptOnce(ctx, "touch marker", "fail", dir, os.Environ(), "g1"); err != nil {
		t.Fatalf("success run: %v", err)
	}
	if _, err := os.Stat(dir + "/marker"); err != nil {
		t.Fatalf("setup script did not run in workdir: %v", err)
	}

	// Env is passed through to the script.
	if err := runSetupScriptOnce(ctx, `test "$TP_TEST" = ok`, "fail", dir,
		append(os.Environ(), "TP_TEST=ok"), "g1"); err != nil {
		t.Fatalf("env not passed to setup script: %v", err)
	}

	// Fail-closed: a non-zero exit is surfaced.
	if err := runSetupScriptOnce(ctx, "exit 3", "fail", dir, os.Environ(), "g1"); err == nil {
		t.Fatalf("fail policy should surface non-zero exit")
	}
	// Continue: the same failure is swallowed.
	if err := runSetupScriptOnce(ctx, "exit 3", "continue", dir, os.Environ(), "g1"); err != nil {
		t.Fatalf("continue policy should swallow failure: %v", err)
	}
	// Empty script is a no-op.
	if err := runSetupScriptOnce(ctx, "", "fail", dir, os.Environ(), "g1"); err != nil {
		t.Fatalf("empty script: %v", err)
	}
}

func TestSanitizeTempTag(t *testing.T) {
	if got := sanitizeTempTag("../../etc/passwd"); strings.ContainsAny(got, "/.") {
		t.Fatalf("path chars survived: %q", got)
	}
	if got := sanitizeTempTag(""); got != "agent" {
		t.Fatalf("empty tag fallback: %q", got)
	}
	if got := sanitizeTempTag("abc-123_X"); got != "abc-123_X" {
		t.Fatalf("safe tag mangled: %q", got)
	}
}

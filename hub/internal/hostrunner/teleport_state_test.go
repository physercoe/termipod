package hostrunner

import (
	"os"
	"path/filepath"
	"testing"

	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
)

// writeClaudeSession creates a claude-code session JSONL at the resolver-derived
// path for (home, workdir, sessionID) and returns its absolute path.
func writeClaudeSession(t *testing.T, home, workdir, sessionID, content string) string {
	t.Helper()
	dir := claudecode.ProjectDirFor(home, workdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPackRestoreEngineState_ClaudeRemapsAcrossHosts(t *testing.T) {
	const sessionID = "11111111-2222-3333-4444-555555555555"
	const content = `{"type":"user","text":"hi"}` + "\n" + `{"type":"assistant","text":"hello"}` + "\n"

	// Source host: home + workdir A.
	srcHome := t.TempDir()
	srcWorkdir := "/home/ubuntu/proj-a/wt-worker-1"
	writeClaudeSession(t, srcHome, srcWorkdir, sessionID, content)

	bundle, err := packEngineState("claude-code", srcHome, srcWorkdir, sessionID)
	if err != nil {
		t.Fatalf("packEngineState: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("empty bundle")
	}

	// Target host: a DIFFERENT home + workdir. Restore must place the file at
	// the target's resolver-derived path (different slug), not the source's.
	tgtHome := t.TempDir()
	tgtWorkdir := "/data/agents/proj-a/wt-worker-1" // different cwd → different slug
	if err := restoreEngineState("claude-code", tgtHome, tgtWorkdir, sessionID, bundle); err != nil {
		t.Fatalf("restoreEngineState: %v", err)
	}

	want := filepath.Join(claudecode.ProjectDirFor(tgtHome, tgtWorkdir), sessionID+".jsonl")
	got, rerr := os.ReadFile(want)
	if rerr != nil {
		t.Fatalf("restored file not at expected target path %s: %v", want, rerr)
	}
	if string(got) != content {
		t.Fatalf("content mismatch after teleport:\n got %q\nwant %q", got, content)
	}
	// And it must NOT have leaked into a source-slug path under the target home.
	leak := filepath.Join(claudecode.ProjectDirFor(tgtHome, srcWorkdir), sessionID+".jsonl")
	if _, err := os.Stat(leak); err == nil {
		t.Fatalf("engine state leaked to source-slug path %s", leak)
	}
}

func TestPackEngineState_MissingSessionFails(t *testing.T) {
	home := t.TempDir()
	if _, err := packEngineState("claude-code", home, "/w/d", "no-such-session"); err == nil {
		t.Fatal("expected error for missing session id")
	}
	// A real id whose file doesn't exist also fails.
	if _, err := packEngineState("claude-code", home, "/w/d", "deadbeef"); err == nil {
		t.Fatal("expected error for missing session file")
	}
}

func TestEngineState_UnsupportedEngine(t *testing.T) {
	if _, err := packEngineState("codex", t.TempDir(), "/w/d", "s"); err == nil {
		t.Fatal("expected unsupported-engine error on pack")
	}
	if err := restoreEngineState("kimi-code-ts", t.TempDir(), "/w/d", "s", nil); err == nil {
		t.Fatal("expected unsupported-engine error on restore")
	}
}

func TestRestoreEngineState_OverwritesExisting(t *testing.T) {
	const sessionID = "aaaa"
	srcHome := t.TempDir()
	srcWorkdir := "/src/wt"
	writeClaudeSession(t, srcHome, srcWorkdir, sessionID, "new\n")
	bundle, err := packEngineState("claude-code", srcHome, srcWorkdir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	tgtHome := t.TempDir()
	tgtWorkdir := "/tgt/wt"
	// Pre-existing stale file at the target path.
	writeClaudeSession(t, tgtHome, tgtWorkdir, sessionID, "stale\n")
	if err := restoreEngineState("claude-code", tgtHome, tgtWorkdir, sessionID, bundle); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claudecode.ProjectDirFor(tgtHome, tgtWorkdir), sessionID+".jsonl"))
	if string(got) != "new\n" {
		t.Fatalf("expected overwrite, got %q", got)
	}
}

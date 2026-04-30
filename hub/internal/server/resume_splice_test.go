package server

import (
	"strings"
	"testing"
)

// TestSpliceClaudeResume_AddsFlagAfterBin pins the load-bearing
// shape: when sessions.engine_session_id is captured, the resume
// handler must splice `--resume <id>` into backend.cmd directly after
// the `claude` token. Position matters less for claude's flag parser
// than for human grep — placing it right after the bin makes the
// resume cursor obvious in agent_spawns.spawn_spec_yaml audit rows.
func TestSpliceClaudeResume_AddsFlagAfterBin(t *testing.T) {
	in := `template: agents.steward
backend:
  kind: claude-code
  cmd: "claude --model claude-opus-4-7 --print --output-format stream-json --input-format stream-json --verbose --dangerously-skip-permissions"
  default_workdir: ~/hub-work
`
	out := spliceClaudeResume(in, "abc-123")
	if out == in {
		t.Fatalf("expected cmd rewrite; got unchanged spec")
	}
	if !strings.Contains(out, "claude --resume abc-123 --model") {
		t.Errorf("expected `claude --resume abc-123 --model` prefix; got:\n%s", out)
	}
	// Other claude flags must survive the rewrite.
	for _, want := range []string{
		"--print", "--output-format stream-json",
		"--input-format stream-json", "--verbose",
		"--dangerously-skip-permissions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rewrite dropped %q from cmd:\n%s", want, out)
		}
	}
}

// TestSpliceClaudeResume_Idempotent — a second pass with the same id
// must be a no-op. handleResumeSession reads the *original* spec from
// `sessions.spawn_spec_yaml` (which the handler never writes back), so
// in practice we always splice on a clean cmd. The idempotence check
// guards against a future code path that decides to persist the
// spliced spec — without it, repeated resumes would balloon the cmd
// with duplicate `--resume` pairs.
func TestSpliceClaudeResume_Idempotent(t *testing.T) {
	in := `backend:
  cmd: "claude --resume abc-123 --model claude-opus-4-7 --print"
`
	out := spliceClaudeResume(in, "abc-123")
	if strings.Count(out, "--resume") != 1 {
		t.Errorf("expected exactly one --resume, got %d:\n%s",
			strings.Count(out, "--resume"), out)
	}
	if !strings.Contains(out, "--resume abc-123") {
		t.Errorf("lost the resume flag entirely:\n%s", out)
	}
}

// TestSpliceClaudeResume_ReplacesPriorID covers the "operator
// hand-edited a template with a stale --resume" edge case. The
// captured engineSessionID always wins.
func TestSpliceClaudeResume_ReplacesPriorID(t *testing.T) {
	in := `backend:
  cmd: "claude --resume OLD-ID --model claude-opus-4-7"
`
	out := spliceClaudeResume(in, "NEW-ID")
	if strings.Contains(out, "OLD-ID") {
		t.Errorf("prior id leaked through:\n%s", out)
	}
	if !strings.Contains(out, "--resume NEW-ID") {
		t.Errorf("new id missing:\n%s", out)
	}
	if strings.Count(out, "--resume") != 1 {
		t.Errorf("expected exactly one --resume, got %d:\n%s",
			strings.Count(out, "--resume"), out)
	}
}

// TestSpliceClaudeResume_LeavesNonClaudeAlone — the splice helper is
// claude-specific by design. handleResumeSession already gates on
// kind=claude-code, but the helper itself also short-circuits so a
// future caller that forgets the gate doesn't corrupt a codex/gemini
// cmd. (Codex resumes via thread/resume RPC; gemini via its own driver
// argv splice — both are pinned in ADR-012/013, neither uses the YAML
// spec splice path.)
func TestSpliceClaudeResume_LeavesNonClaudeAlone(t *testing.T) {
	in := `backend:
  kind: gemini-cli
  cmd: "gemini"
`
	out := spliceClaudeResume(in, "abc-123")
	if out != in {
		t.Errorf("non-claude cmd was rewritten:\n--- in ---\n%s\n--- out ---\n%s", in, out)
	}
}

// TestSpliceClaudeResume_EmptyInputs — both empty spec and empty id
// must short-circuit. Empty spec is the pre-resume legacy session
// branch; empty id is the never-saw-session.init case.
func TestSpliceClaudeResume_EmptyInputs(t *testing.T) {
	if got := spliceClaudeResume("", "abc"); got != "" {
		t.Errorf("expected empty spec passthrough, got %q", got)
	}
	in := `backend:
  cmd: "claude --model X"
`
	if got := spliceClaudeResume(in, ""); got != in {
		t.Errorf("empty id should be no-op; got rewrite:\n%s", got)
	}
}

// TestSpliceClaudeResume_MalformedYAML — when the YAML doesn't parse,
// fall through to the cold-start cmd rather than 500-ing the resume
// handler. The user gets a fresh session, the same outcome they had
// pre-ADR-014.
func TestSpliceClaudeResume_MalformedYAML(t *testing.T) {
	in := "this: is: not: valid: yaml: : :"
	if got := spliceClaudeResume(in, "abc"); got != in {
		t.Errorf("malformed yaml should pass through; got:\n%s", got)
	}
}

// TestSpliceClaudeResume_NoBackendCmd — yaml parses but lacks the key
// we want to mutate. Pass through unchanged so the spawn proceeds with
// whatever shape the spec actually had.
func TestSpliceClaudeResume_NoBackendCmd(t *testing.T) {
	in := `worktree:
  repo: owner/proj
`
	if got := spliceClaudeResume(in, "abc"); got != in {
		t.Errorf("missing backend.cmd should pass through; got:\n%s", got)
	}
}

// TestRewriteClaudeResumeFlag_AbsolutePath — some operators install
// claude at a non-standard path and template the absolute binary.
// isClaudeBin recognises trailing `/claude` so the splice still
// applies. Without this, an operator using e.g. `/opt/claude/bin/claude
// ...` would silently skip the resume rewrite.
func TestRewriteClaudeResumeFlag_AbsolutePath(t *testing.T) {
	got, ok := rewriteClaudeResumeFlag("/opt/claude/bin/claude --print", "id-1")
	if !ok {
		t.Fatalf("absolute path claude bin not recognised")
	}
	if !strings.Contains(got, "/opt/claude/bin/claude --resume id-1 --print") {
		t.Errorf("unexpected rewrite: %q", got)
	}
}

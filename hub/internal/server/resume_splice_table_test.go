package server

import (
	"strings"
	"testing"

	"github.com/termipod/hub/internal/resumerecipes"
)

// Tests for the table-driven splice (pane-state-manifests plan, N1). The
// pre-existing tests in resume_splice_test.go pin the claude and agy
// behaviour that must not change; these cover the dispatch itself, the
// families that used to live in two hand-copied switches, and the shell
// hardening the table brought with it.

func specWithCmd(cmd string) string {
	return "backend:\n  kind: x\n  cmd: \"" + cmd + "\"\n"
}

// TestSpliceResume_DispatchesEveryFamilyTheHubKnows is the anti-drift test.
// The two switches this replaced had already diverged; this asserts each
// family gets the mechanism its recipe row declares, from the ONE entry point
// both call sites now use.
func TestSpliceResume_DispatchesEveryFamilyTheHubKnows(t *testing.T) {
	cases := []struct {
		family    string
		cmd       string
		wantInCmd string // argv families: expected substring in backend.cmd
		wantField bool   // protocol families: expect top-level resume_session_id
	}{
		{family: "claude-code", cmd: "claude --model x", wantInCmd: "claude --resume sess-1 --model x"},
		{family: "antigravity", cmd: "agy --dangerously-skip-permissions", wantInCmd: "agy --conversation sess-1"},
		{family: "codex", cmd: "codex", wantField: true},
		{family: "gemini-cli", cmd: "gemini --acp", wantField: true},
		{family: "kimi-code-ts", cmd: "kimi --yolo", wantField: true},
		{family: "kimi-code", cmd: "kimi", wantField: true},
	}
	for _, tc := range cases {
		got := spliceResume(specWithCmd(tc.cmd), tc.family, "sess-1")
		if tc.wantInCmd != "" && !strings.Contains(got, tc.wantInCmd) {
			t.Errorf("%s: cmd missing %q\n--- got ---\n%s", tc.family, tc.wantInCmd, got)
		}
		hasField := strings.Contains(got, "resume_session_id: sess-1")
		if tc.wantField && !hasField {
			t.Errorf("%s: expected the protocol-level resume_session_id field\n--- got ---\n%s", tc.family, got)
		}
		if !tc.wantField && hasField {
			t.Errorf("%s: argv family must not gain resume_session_id\n--- got ---\n%s", tc.family, got)
		}
	}
}

// TestSpliceResume_AntigravityReachesBothCallSites is the regression for the
// divergence found while writing N1: respawn_with_spec_mutation's copy of the
// switch never had an antigravity arm. Latent (flagForField gates first), but
// it would have become a silent cold-start the moment antigravity gained a
// mode/model flag. Both call sites now share this function, so one test covers
// both.
func TestSpliceResume_AntigravityReachesBothCallSites(t *testing.T) {
	got := spliceResume(specWithCmd("agy --dangerously-skip-permissions"), "antigravity", "conv-9")
	if !strings.Contains(got, "agy --conversation conv-9") {
		t.Fatalf("antigravity not spliced:\n%s", got)
	}
	// And the wrapper the old spec-mutation path would have called agrees.
	if spliceAntigravityResume(specWithCmd("agy"), "conv-9") == specWithCmd("agy") {
		t.Error("spliceAntigravityResume made no change")
	}
}

func TestSpliceResume_UnknownFamilyLeavesSpecAlone(t *testing.T) {
	in := specWithCmd("someengine --flag")
	if got := spliceResume(in, "not-a-family", "sess-1"); got != in {
		t.Errorf("unknown family rewrote the spec:\n%s", got)
	}
	// An engine we have a RECIPE for but no family row must also be refused —
	// a recipe is not permission to rewrite our spawn specs.
	if got := spliceResume(specWithCmd("droid --x"), "droid", "sess-1"); got != specWithCmd("droid --x") {
		t.Errorf("recipe-only engine was spliced as if it were a family:\n%s", got)
	}
}

// --- shell hardening --------------------------------------------------------

// TestSpliceResume_HostileSessionIDIsQuotedInTheCmd is the end-to-end version
// of the injection test. engine_session_id is copied verbatim from the
// engine's own session.init payload (handlers_sessions.go captureEngineSessionID
// validates nothing), and backend.cmd is handed to tmux, which runs it through
// a shell. Before the table this value was joined in unquoted.
func TestSpliceResume_HostileSessionIDIsQuotedInTheCmd(t *testing.T) {
	hostile := "abc; touch /tmp/pwned"
	got := spliceResume(specWithCmd("claude --model x"), "claude-code", hostile)
	if strings.Contains(got, "--resume abc; touch") {
		t.Fatalf("session id escaped its shell word:\n%s", got)
	}
	if !strings.Contains(got, `--resume 'abc; touch /tmp/pwned'`) {
		t.Errorf("expected the value single-quoted:\n%s", got)
	}
}

// TestSpliceResume_UnquotableSessionIDRefusesRatherThanCorrupts — a control
// character cannot be made safe by quoting, and a newline in backend.cmd would
// end the command line entirely. Refuse and cold-start, which is what every
// other failure in this path already does.
func TestSpliceResume_UnquotableSessionIDRefusesRatherThanCorrupts(t *testing.T) {
	in := specWithCmd("claude --model x")
	for _, bad := range []string{"abc\nrm -rf /", "abc\x00d", strings.Repeat("x", resumerecipes.MaxSessionIDLen+1)} {
		if got := spliceResume(in, "claude-code", bad); got != in {
			t.Errorf("unquotable id %q was spliced:\n%s", bad, got)
		}
	}
}

// TestSpliceResume_OrdinaryIDStaysUnquoted pins the no-behaviour-change half:
// every id that was already safe renders exactly as it did before N1, so
// adopting the table did not churn a single live spawn spec.
func TestSpliceResume_OrdinaryIDStaysUnquoted(t *testing.T) {
	got := spliceResume(specWithCmd("claude --model x"), "claude-code",
		"550e8400-e29b-41d4-a716-446655440000")
	if !strings.Contains(got, "--resume 550e8400-e29b-41d4-a716-446655440000 --model x") {
		t.Errorf("ordinary uuid was altered:\n%s", got)
	}
	if strings.Contains(got, "'550e8400") {
		t.Errorf("ordinary uuid was needlessly quoted:\n%s", got)
	}
}

// --- generalized rewriter ---------------------------------------------------

// TestRewriteResumeFlag_FlagEqualsStyleQuotesOnlyTheValue — copilot and omp
// use `--resume=<id>`. The flag prefix must stay outside the quotes or the
// engine stops recognising it as that flag. No family maps to these today;
// the test exists so the shape is right before one does.
func TestRewriteResumeFlag_FlagEqualsStyleQuotesOnlyTheValue(t *testing.T) {
	e, ok := resumerecipes.MustLoad().EngineByID("copilot")
	if !ok {
		t.Fatal("copilot recipe missing")
	}
	ref, err := resumerecipes.NewID("a b; c")
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	got, ok := rewriteResumeFlag("copilot --verbose", e, ref)
	if !ok {
		t.Fatal("rewrite refused a copilot cmd")
	}
	if !strings.Contains(got, `--resume='a b; c'`) {
		t.Errorf("got %q, want the flag prefix outside the quotes", got)
	}
}

// TestRewriteResumeFlag_SubcommandStyleRefuses — `codex resume <id>` is a
// different invocation, not a flag to add to an existing one. Splicing a verb
// into a flag-bearing cmd would produce something codex rejects.
func TestRewriteResumeFlag_SubcommandStyleRefuses(t *testing.T) {
	e, _ := resumerecipes.MustLoad().EngineByID("codex")
	ref, _ := resumerecipes.NewID("t-1")
	if _, ok := rewriteResumeFlag("codex --model x", e, ref); ok {
		t.Error("subcommand-style recipe was spliced into an existing cmd")
	}
}

// TestRewriteResumeFlag_FindsBinAfterAPrefix — a cmd may lead with
// `cd <workdir> && <bin>`. agy's rewriter already scanned for the token;
// claude's required it first. Unifying on the scan is why this is asserted
// for both.
func TestRewriteResumeFlag_FindsBinAfterAPrefix(t *testing.T) {
	tbl := resumerecipes.MustLoad()
	ref, _ := resumerecipes.NewID("s-1")
	for _, tc := range []struct{ engine, cmd, want string }{
		{"claude", "cd /w && claude --model x", "claude --resume s-1 --model x"},
		{"agy", "cd /w && agy --yolo", "agy --conversation s-1 --yolo"},
		{"claude", "/opt/claude/bin/claude --print", "/opt/claude/bin/claude --resume s-1 --print"},
	} {
		e, _ := tbl.EngineByID(tc.engine)
		got, ok := rewriteResumeFlag(tc.cmd, e, ref)
		if !ok {
			t.Errorf("%s: refused %q", tc.engine, tc.cmd)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want it to contain %q", tc.engine, got, tc.want)
		}
	}
}

// TestRewriteResumeFlag_WorkdirNamedAfterEngineIsNotTheBin — the bin scan
// suffix-matches path tokens, and specs commonly lead with `cd <workdir> &&`.
// A workdir that happens to be named after the engine must not take the flag:
// `cd /w/claude --resume <id>` is not a resume, it is a spawn that dies on
// `cd: too many arguments`.
func TestRewriteResumeFlag_WorkdirNamedAfterEngineIsNotTheBin(t *testing.T) {
	tbl := resumerecipes.MustLoad()
	ref, _ := resumerecipes.NewID("s-1")
	for _, tc := range []struct{ engine, cmd, want string }{
		{"claude", "cd /home/u/projects/claude && claude --model x",
			"cd /home/u/projects/claude && claude --resume s-1 --model x"},
		{"agy", "cd ~/hub-work/agy && agy --yolo",
			"cd ~/hub-work/agy && agy --conversation s-1 --yolo"},
	} {
		e, _ := tbl.EngineByID(tc.engine)
		got, ok := rewriteResumeFlag(tc.cmd, e, ref)
		if !ok || got != tc.want {
			t.Errorf("%s: got (%q, %v), want %q", tc.engine, got, ok, tc.want)
		}
	}
	// When the ONLY match is cd's operand there is no invocation to splice —
	// refuse rather than corrupt the cd.
	e, _ := tbl.EngineByID("claude")
	in := "cd /home/u/projects/claude && make run"
	if got, ok := rewriteResumeFlag(in, e, ref); ok || got != in {
		t.Errorf("cd-operand-only cmd was rewritten: (%q, %v)", got, ok)
	}
}

func TestRewriteResumeFlag_StripsPriorFlagBeforeSplicing(t *testing.T) {
	e, _ := resumerecipes.MustLoad().EngineByID("claude")
	ref, _ := resumerecipes.NewID("new")
	got, ok := rewriteResumeFlag("claude --resume old --model x", e, ref)
	if !ok {
		t.Fatal("refused")
	}
	if strings.Contains(got, "old") {
		t.Errorf("prior id survived: %q", got)
	}
	if strings.Count(got, "--resume") != 1 {
		t.Errorf("expected exactly one --resume: %q", got)
	}
}

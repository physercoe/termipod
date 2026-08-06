package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEncodeProjectDir_MatchesObservedSlugs(t *testing.T) {
	// Slugs observed under ~/.claude/projects/ on the dev box. Every
	// non-alphanumeric character becomes `-`, and the leading `-` is
	// just the leading `/` getting the same treatment.
	cases := map[string]string{
		"/home/ubuntu/mux-pod":  "-home-ubuntu-mux-pod",
		"/home/ubuntu":          "-home-ubuntu",
		"/home/ubuntu/hub-work": "-home-ubuntu-hub-work",
		"/tmp/foo":              "-tmp-foo",
		"/home/ubuntu/proj/":    "-home-ubuntu-proj", // trailing slash cleaned
		"/home//ubuntu//proj":   "-home-ubuntu-proj", // doubles cleaned
	}
	for in, want := range cases {
		if got := EncodeProjectDir(in); got != want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// The regression pin for the rule this file got wrong until 2026-08-05.
// Not a hypothetical: this exact pair was produced by running a real
// claude session in the probe directory and reading back the directory
// claude created for it. `_` and `.` both collapse to `-`, so a workdir
// like `my_project` or `repo.git` resolved — under the old
// separators-only rule — to a directory claude never creates, and the
// tail waited on it forever without an error.
//
// Every slug we had observed before that probe (all of them under
// /home/ubuntu/<plain-name>) is satisfied by BOTH rules, which is
// exactly why the wrong one survived: the sample could not discriminate.
// Keep a case here that can.
func TestEncodeProjectDir_ReplacesEveryNonAlphanumeric(t *testing.T) {
	const probed = "/tmp/claude-1000/-home-ubuntu-mux-pod/" +
		"a31ebb3e-4c8a-4fbd-9567-aa085280f2a9/scratchpad/enc_probe.v1"
	const want = "-tmp-claude-1000--home-ubuntu-mux-pod-" +
		"a31ebb3e-4c8a-4fbd-9567-aa085280f2a9-scratchpad-enc-probe-v1"
	if got := EncodeProjectDir(probed); got != want {
		t.Errorf("EncodeProjectDir(%q) =\n  %q\nwant\n  %q", probed, got, want)
	}

	// The discriminating characters on their own, so a failure says
	// which one regressed rather than just "the long string differs".
	for in, w := range map[string]string{
		"/w/my_project": "-w-my-project",
		"/w/repo.git":   "-w-repo-git",
		"/w/v1.2.3":     "-w-v1-2-3",
		"/w/a b":        "-w-a-b",
		"/w/x@y":        "-w-x-y",
	} {
		if got := EncodeProjectDir(in); got != w {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", in, got, w)
		}
	}
}

func TestProjectDirFor_AssemblesPath(t *testing.T) {
	got := ProjectDirFor("/home/alice", "/home/alice/proj")
	want := "/home/alice/.claude/projects/-home-alice-proj"
	if got != want {
		t.Errorf("ProjectDirFor = %q, want %q", got, want)
	}
}

// ProjectDirFor is the ENV-FREE form — teleport depends on that, because
// it resolves a source host and a target host neither of which is
// described by this process's environment. $CLAUDE_CONFIG_DIR must not
// leak into it.
func TestProjectDirFor_IgnoresConfigHomeEnvVar(t *testing.T) {
	t.Setenv(ConfigHomeEnvVar, "/elsewhere/.claude-work")
	got := ProjectDirFor("/home/alice", "/home/alice/proj")
	want := "/home/alice/.claude/projects/-home-alice-proj"
	if got != want {
		t.Errorf("ProjectDirFor leaked the env var: got %q, want %q", got, want)
	}
}

func TestProjectDirIn_UsesGivenRoot(t *testing.T) {
	got := ProjectDirIn("/home/alice/.claude-work", "/home/alice/proj")
	want := "/home/alice/.claude-work/projects/-home-alice-proj"
	if got != want {
		t.Errorf("ProjectDirIn = %q, want %q", got, want)
	}
}

func TestConfigHome_PrefersEnvVar(t *testing.T) {
	t.Setenv(ConfigHomeEnvVar, "/home/alice/.claude-work")
	got, err := ConfigHome()
	if err != nil {
		t.Fatalf("ConfigHome: %v", err)
	}
	if got != "/home/alice/.claude-work" {
		t.Errorf("ConfigHome = %q, want the env var's value", got)
	}
}

func TestConfigHome_FallsBackToHome(t *testing.T) {
	t.Setenv(ConfigHomeEnvVar, "")
	t.Setenv("HOME", "/home/bob")
	got, err := ConfigHome()
	if err != nil {
		t.Fatalf("ConfigHome: %v", err)
	}
	if got != "/home/bob/.claude" {
		t.Errorf("ConfigHome = %q, want /home/bob/.claude", got)
	}
}

// The precedence that matters in production: an env-profile value is
// exported into the CHILD's environment only, so host-runner's own
// $CLAUDE_CONFIG_DIR must lose to it. A tail that consulted only
// os.Getenv would resolve a different directory than the child writes
// to — silently, since neither path errors.
func TestResolveConfigHome_SpawnOverrideBeatsProcessEnv(t *testing.T) {
	t.Setenv(ConfigHomeEnvVar, "/host/runner/root")

	if got := ResolveConfigHome("/spawn/root", "/home/carol"); got != "/spawn/root" {
		t.Errorf("spawn override lost: got %q", got)
	}
	if got := ResolveConfigHome("", "/home/carol"); got != "/host/runner/root" {
		t.Errorf("process env ignored: got %q", got)
	}
	if got := ResolveConfigHome("   ", "/home/carol"); got != "/host/runner/root" {
		t.Errorf("blank override should not win: got %q", got)
	}

	t.Setenv(ConfigHomeEnvVar, "")
	if got := ResolveConfigHome("", "/home/carol"); got != "/home/carol/.claude" {
		t.Errorf("home fallback: got %q", got)
	}
}

func TestResolveLatest_PicksNewest(t *testing.T) {
	dir := t.TempDir()
	// Write three files with increasing mtimes; the middle one is
	// NOT a .jsonl and must be skipped.
	mustWrite(t, filepath.Join(dir, "old.jsonl"), "old\n")
	time.Sleep(10 * time.Millisecond)
	mustWrite(t, filepath.Join(dir, "ignored.tmp"), "junk\n")
	time.Sleep(10 * time.Millisecond)
	mustWrite(t, filepath.Join(dir, "new.jsonl"), "new\n")

	path, _, err := ResolveLatest(dir)
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	if filepath.Base(path) != "new.jsonl" {
		t.Errorf("picked %s, want new.jsonl", filepath.Base(path))
	}
}

func TestResolveLatest_NoSessionWhenDirMissing(t *testing.T) {
	_, _, err := ResolveLatest("/this/path/does/not/exist")
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}

func TestResolveLatest_NoSessionWhenDirEmpty(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ResolveLatest(dir)
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v on empty dir, want ErrNoSession", err)
	}
}

// v1.0.661 — stale-JSONL cutoff. ResolveLatestSince(minMtime) must
// ignore JSONLs whose mtime is at or before minMtime. Without this,
// a prior interactive `claude` session in the same workdir gets
// latched onto and its transcript replays into the fresh agent's
// feed (the `/exit` slash-command bleed-through symptom).
func TestResolveLatestSince_IgnoresStaleJSONL(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "stale.jsonl"), "old\n")
	// Cutoff is "now"; the file we just wrote sits at-or-before it.
	cutoff := time.Now()
	time.Sleep(20 * time.Millisecond)
	mustWrite(t, filepath.Join(dir, "fresh.jsonl"), "new\n")

	path, _, err := ResolveLatestSince(dir, cutoff)
	if err != nil {
		t.Fatalf("ResolveLatestSince: %v", err)
	}
	if filepath.Base(path) != "fresh.jsonl" {
		t.Errorf("picked %s, want fresh.jsonl (stale ignored)", filepath.Base(path))
	}

	// Same dir, no fresh file: cutoff must reject the stale one and
	// surface ErrNoSession so WaitForSessionSince keeps polling.
	dir2 := t.TempDir()
	mustWrite(t, filepath.Join(dir2, "stale.jsonl"), "old\n")
	cutoff2 := time.Now()
	_, _, err = ResolveLatestSince(dir2, cutoff2)
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v with only stale jsonl, want ErrNoSession", err)
	}
}

// Zero minMtime must behave identically to the old ResolveLatest —
// tests + non-adapter callers shouldn't change behaviour.
func TestResolveLatestSince_ZeroCutoffMatchesResolveLatest(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "old.jsonl"), "old\n")
	time.Sleep(10 * time.Millisecond)
	mustWrite(t, filepath.Join(dir, "new.jsonl"), "new\n")

	a, _, err := ResolveLatest(dir)
	if err != nil {
		t.Fatalf("ResolveLatest: %v", err)
	}
	b, _, err := ResolveLatestSince(dir, time.Time{})
	if err != nil {
		t.Fatalf("ResolveLatestSince(zero): %v", err)
	}
	if a != b {
		t.Errorf("zero-cutoff result %s != ResolveLatest result %s", b, a)
	}
}

func TestResolveLatest_NoSessionWhenOnlyNonJsonl(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "settings.json"), "{}\n")
	mustWrite(t, filepath.Join(dir, "scratch.tmp"), "x\n")
	_, _, err := ResolveLatest(dir)
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v with no .jsonl files, want ErrNoSession", err)
	}
}

func TestWaitForSession_ReturnsImmediatelyIfPresent(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "live.jsonl"), "hi\n")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	path, err := WaitForSession(ctx, dir, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForSession: %v", err)
	}
	if filepath.Base(path) != "live.jsonl" {
		t.Errorf("got %s, want live.jsonl", path)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("WaitForSession took %v with file already present; want <200ms", elapsed)
	}
}

func TestWaitForSession_PicksUpFileAfterPoll(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(120 * time.Millisecond)
		mustWrite(t, filepath.Join(dir, "appeared.jsonl"), "boot\n")
	}()

	path, err := WaitForSession(ctx, dir, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForSession: %v", err)
	}
	if filepath.Base(path) != "appeared.jsonl" {
		t.Errorf("got %s, want appeared.jsonl", path)
	}
}

func TestWaitForSession_TimesOut(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := WaitForSession(ctx, dir, 25*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForSession returned nil error on timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want wrapping DeadlineExceeded", err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

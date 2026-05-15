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
	// Slugs observed under ~/.claude/projects/ on the dev box; the
	// encoding is literal `/` → `-` plus a leading `-` because the
	// path starts with `/`.
	cases := map[string]string{
		"/home/ubuntu/mux-pod":   "-home-ubuntu-mux-pod",
		"/home/ubuntu":           "-home-ubuntu",
		"/home/ubuntu/hub-work":  "-home-ubuntu-hub-work",
		"/tmp/foo":               "-tmp-foo",
		"/home/ubuntu/proj/":     "-home-ubuntu-proj", // trailing slash cleaned
		"/home//ubuntu//proj":    "-home-ubuntu-proj", // doubles cleaned
	}
	for in, want := range cases {
		if got := EncodeProjectDir(in); got != want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", in, got, want)
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

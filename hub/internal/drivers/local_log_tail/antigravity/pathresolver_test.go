package antigravity

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// New-brain-dir-since-launch is the primary signal. agy stopped writing
// last_conversations.json for project workdirs (host-verified 2026-05-23
// W11 smoke), so this resolver path must succeed without the cache.
func TestWaitForConversation_NewBrainDirSinceWinsWithStaleCache(t *testing.T) {
	home := t.TempDir()

	// Seed a stale cache that DOESN'T contain our workdir — mirrors the
	// state we found on the smoke box (May-22 entries only, our workdir
	// missing).
	cacheDir := filepath.Join(StoreDir(home), "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheBody, _ := json.Marshal(map[string]string{
		"/some/other/workdir": "old-conv-from-yesterday",
	})
	if err := os.WriteFile(filepath.Join(cacheDir, "last_conversations.json"), cacheBody, 0o600); err != nil {
		t.Fatal(err)
	}

	// Seed a PRE-launch brain dir — must NOT be picked up (sibling spawn
	// or a previous run of agy on the same host).
	brainRoot := filepath.Join(StoreDir(home), "brain")
	preID := "pre-launch-sibling"
	if err := os.MkdirAll(filepath.Join(brainRoot, preID), 0o755); err != nil {
		t.Fatal(err)
	}
	// Stamp its mtime well in the past.
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(filepath.Join(brainRoot, preID), past, past); err != nil {
		t.Fatal(err)
	}

	launch := time.Now()

	// Race: schedule the post-launch brain dir to appear after a beat.
	ourID := "conv-from-this-spawn"
	go func() {
		time.Sleep(50 * time.Millisecond)
		dir := filepath.Join(brainRoot, ourID)
		_ = os.MkdirAll(dir, 0o755)
		now := time.Now()
		_ = os.Chtimes(dir, now, now)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := WaitForConversation(ctx, home, "/home/ubuntu/hub-work/antigravity",
		25*time.Millisecond, false, launch)
	if err != nil {
		t.Fatalf("WaitForConversation: %v", err)
	}
	if got != ourID {
		t.Errorf("got %q; want %q (pre-launch sibling %q must not win)", got, ourID, preID)
	}
}

// Legacy: when the cache still works (casual non-project workdir), the
// cache entry resolves directly without needing a brain dir to appear.
func TestWaitForConversation_LegacyCacheStillWorks(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(StoreDir(home), "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workdir := "/tmp/some-casual-workdir"
	wantID := "cached-conv-id"
	cacheBody, _ := json.Marshal(map[string]string{workdir: wantID})
	if err := os.WriteFile(filepath.Join(cacheDir, "last_conversations.json"), cacheBody, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err := WaitForConversation(ctx, home, workdir, 10*time.Millisecond, false, time.Now())
	if err != nil {
		t.Fatalf("WaitForConversation: %v", err)
	}
	if got != wantID {
		t.Errorf("got %q; want %q", got, wantID)
	}
}

// newestBrainSince ignores pre-`since` directories — a launch from
// thirty seconds ago does not pick up a directory from an hour ago.
func TestNewestBrainSince_IgnoresOlderDirs(t *testing.T) {
	home := t.TempDir()
	brainRoot := filepath.Join(StoreDir(home), "brain")
	if err := os.MkdirAll(brainRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(brainRoot, "old-conv")
	newer := filepath.Join(brainRoot, "newer-conv")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newer, 0o755); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour)
	recent := time.Now()
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, recent, recent); err != nil {
		t.Fatal(err)
	}

	// since = 30 minutes ago — should pick newer only.
	since := time.Now().Add(-30 * time.Minute)
	got, ok := newestBrainSince(home, since)
	if !ok {
		t.Fatal("newestBrainSince returned false")
	}
	if got != "newer-conv" {
		t.Errorf("got %q; want %q", got, "newer-conv")
	}

	// since = 1 second ago — no eligible dirs.
	got2, ok2 := newestBrainSince(home, time.Now().Add(1*time.Second))
	if ok2 {
		t.Errorf("expected no match in the future; got %q", got2)
	}
}

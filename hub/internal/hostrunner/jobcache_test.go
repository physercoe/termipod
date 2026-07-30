package hostrunner

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/termipod/hub/internal/hostjobs"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// stageJobDir writes a job directory of roughly n bytes and back-dates
// everything in it, so eviction sees a deterministic recency order.
func stageJobDir(t *testing.T, root, kind, id string, n int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, kind, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	f := filepath.Join(dir, "artifact.rrd")
	if err := os.WriteFile(f, make([]byte, n), 0o600); err != nil {
		t.Fatalf("write %s: %v", f, err)
	}
	when := time.Now().Add(-age)
	// The file first, then the directory: writing the file bumps the dir mtime.
	if err := os.Chtimes(f, when, when); err != nil {
		t.Fatalf("chtimes file: %v", err)
	}
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatalf("chtimes dir: %v", err)
	}
	return dir
}

func TestJobCache_EvictsColdestJobDirsFirst(t *testing.T) {
	root := t.TempDir()
	c := &jobCache{Root: root, Cap: 250, Pin: time.Nanosecond, Log: quietLog()}

	cold := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "cold", 100, 3*time.Hour)
	mid := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "mid", 100, 2*time.Hour)
	warm := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "warm", 100, time.Hour)

	if freed := c.evict(); freed != 100 {
		t.Fatalf("freed %d bytes, want exactly the coldest dir's 100", freed)
	}
	if _, err := os.Stat(cold); !os.IsNotExist(err) {
		t.Fatalf("coldest dir survived: %v", err)
	}
	for _, keep := range []string{mid, warm} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("evicted past the cap: %s is gone (%v)", keep, err)
		}
	}
}

// The unit of eviction is a job's whole directory. Deleting one file out of a
// job's output leaves an artifact whose path the hub still advertises as valid.
func TestJobCache_EvictsWholeJobDirNotIndividualFiles(t *testing.T) {
	root := t.TempDir()
	c := &jobCache{Root: root, Cap: 10, Pin: time.Nanosecond, Log: quietLog()}

	dir := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "multi", 100, 2*time.Hour)
	side := filepath.Join(dir, "export.log")
	if err := os.WriteFile(side, []byte("lines"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	when := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(side, when, when)
	_ = os.Chtimes(dir, when, when)

	c.evict()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("job dir partially evicted; dir still present: %v", err)
	}
}

// Recently-written directories are pinned, which is how a running job and a
// just-reported artifact are both protected under one rule. Staying over cap is
// the deliberate trade.
func TestJobCache_PinWindowProtectsRecentDirsEvenOverCap(t *testing.T) {
	root := t.TempDir()
	c := &jobCache{Root: root, Cap: 10, Pin: time.Hour, Log: quietLog()}

	fresh := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "inflight", 500, time.Second)

	if freed := c.evict(); freed != 0 {
		t.Fatalf("freed %d bytes from a pinned dir, want 0", freed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("an in-flight job's dir was evicted: %v", err)
	}
}

func TestJobCache_TouchKeepsADirFromBeingEvicted(t *testing.T) {
	root := t.TempDir()
	c := &jobCache{Root: root, Cap: 150, Pin: time.Minute, Log: quietLog()}

	older := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "older", 100, 3*time.Hour)
	newer := stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "newer", 100, 2*time.Hour)

	// A use the cache was told about: `older` is now the warm one.
	c.touchJobDir(older)

	c.evict()

	if _, err := os.Stat(older); err != nil {
		t.Fatalf("a touched dir was evicted: %v", err)
	}
	if _, err := os.Stat(newer); !os.IsNotExist(err) {
		t.Fatalf("the now-coldest dir survived: %v", err)
	}
}

// The cache shares a parent with the host-runner's state dir. Deleting anything
// it does not recognise is how a cache eats data it did not create.
func TestJobCache_LeavesUnrecognisedEntriesAlone(t *testing.T) {
	root := t.TempDir()
	c := &jobCache{Root: root, Cap: 1, Pin: time.Nanosecond, Log: quietLog()}

	stageJobDir(t, root, hostjobs.KindDatasetExportRRD, "job", 100, 2*time.Hour)
	stray := filepath.Join(root, hostjobs.KindDatasetExportRRD, "stray.txt")
	if err := os.WriteFile(stray, []byte("not mine"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	c.evict()

	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("a stray file was deleted: %v", err)
	}
}

func TestJobCache_JobDirRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	c := &jobCache{Root: root, Log: quietLog()}

	cases := []struct{ kind, id string }{
		{"../../etc", "x"},
		{"kind", "../../../tmp/pwned"},
		{"kind/nested", "x"},
		{"", "x"},
		{"kind", ""},
		{"..", "x"},
	}
	for _, c2 := range cases {
		if _, err := c.jobDir(c2.kind, c2.id); err == nil {
			t.Fatalf("jobDir(%q, %q) was accepted", c2.kind, c2.id)
		}
	}
	if _, err := c.jobDir(hostjobs.KindDatasetExportRRD, "cmd-01_ok.v2"); err != nil {
		t.Fatalf("a legitimate kind/id was rejected: %v", err)
	}
}

// An unconfigured cache must refuse rather than pick a fallback location:
// writing multi-GB artifacts somewhere unexpected is worse than failing the job.
func TestJobCache_UnconfiguredRootRefuses(t *testing.T) {
	var c *jobCache
	if _, err := c.jobDir("k", "i"); err == nil {
		t.Fatal("a nil cache handed out a directory")
	}
	empty := &jobCache{Log: quietLog()}
	if _, err := empty.jobDir("k", "i"); err == nil {
		t.Fatal("a rootless cache handed out a directory")
	}
	if freed := empty.evict(); freed != 0 {
		t.Fatalf("a rootless cache evicted %d bytes", freed)
	}
}

func TestJobCache_EmptyRootIsNotAnError(t *testing.T) {
	c := &jobCache{Root: filepath.Join(t.TempDir(), "never-created"), Log: quietLog()}
	if freed := c.evict(); freed != 0 {
		t.Fatalf("freed %d from a nonexistent root", freed)
	}
}

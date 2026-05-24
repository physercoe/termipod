package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EncodeProjectDir mirrors claude-code's on-disk encoding for cwd
// slugs: every path-separator is replaced with `-`. Empirically
// observed via `ls ~/.claude/projects/`:
//
//	/home/ubuntu/mux-pod    →    -home-ubuntu-mux-pod
//	/tmp/foo                →    -tmp-foo
//
// claude-code resolves project session files under
// `~/.claude/projects/<slug>/<session-uuid>.jsonl`. Cleans the
// input (no trailing slash, collapses doubles) before encoding so
// `/home/ubuntu/proj/` and `/home/ubuntu/proj` produce the same
// slug.
func EncodeProjectDir(cwd string) string {
	cwd = filepath.Clean(cwd)
	return strings.ReplaceAll(cwd, string(filepath.Separator), "-")
}

// ProjectDirFor returns the absolute path of claude-code's per-cwd
// session directory: `<homeDir>/.claude/projects/<encoded-cwd>`.
// homeDir is typically `os.UserHomeDir()`; tests pass an explicit
// temp dir.
func ProjectDirFor(homeDir, cwd string) string {
	return filepath.Join(homeDir, ".claude", "projects", EncodeProjectDir(cwd))
}

// ResolveLatest returns the absolute path of the newest `.jsonl`
// file in projectDir by mtime, plus its mtime. Returns
// (ErrNoSession) if the directory is missing or contains no
// `.jsonl` files. Returns any other filesystem error as-is.
//
// claude-code may write hidden tmp files alongside the session
// JSONL during compaction; we filter to `.jsonl` extension to
// avoid latching on those.
func ResolveLatest(projectDir string) (path string, mtime time.Time, err error) {
	return ResolveLatestSince(projectDir, time.Time{})
}

// ResolveLatestSince is ResolveLatest but only considers JSONL files
// whose mtime is strictly after `minMtime`. Used by the adapter on
// fresh-spawn attach to ignore stale transcripts from a previous
// interactive `claude` session in the same workdir — without the
// cutoff, our reader latches on whichever JSONL claude touched most
// recently, and a manual operator session that contained `/exit` or
// other slash-command transcripts gets replayed into the new agent's
// feed. agy hit the same class of bug and fixed it at v1.0.645
// ("brain-dir-since-launch" resolver).
//
// A zero minMtime disables the cutoff (equivalent to ResolveLatest).
func ResolveLatestSince(projectDir string, minMtime time.Time) (path string, mtime time.Time, err error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", time.Time{}, ErrNoSession
		}
		return "", time.Time{}, err
	}
	var bestPath string
	var bestT time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		if !minMtime.IsZero() && !info.ModTime().After(minMtime) {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestT) {
			bestPath = filepath.Join(projectDir, e.Name())
			bestT = info.ModTime()
		}
	}
	if bestPath == "" {
		return "", time.Time{}, ErrNoSession
	}
	return bestPath, bestT, nil
}

// WaitForSession blocks until a `.jsonl` file appears in projectDir
// or the context fires. Returns the file's absolute path on success.
// pollEvery controls the poll cadence; zero defaults to 250ms.
// Useful when host-runner spawns claude-code and wants to start
// tailing as soon as the session file materializes — the file
// doesn't exist until claude has produced its first event, typically
// well under a second.
//
// Equivalent to WaitForSessionSince(ctx, projectDir, pollEvery,
// time.Time{}).
func WaitForSession(ctx context.Context, projectDir string, pollEvery time.Duration) (string, error) {
	return WaitForSessionSince(ctx, projectDir, pollEvery, time.Time{})
}

// WaitForSessionSince is WaitForSession but only returns when a JSONL
// whose mtime is strictly after `minMtime` is present. The adapter
// passes its construction time so a stale JSONL from a prior `claude`
// session in the same workdir is never latched onto.
func WaitForSessionSince(ctx context.Context, projectDir string, pollEvery time.Duration, minMtime time.Time) (string, error) {
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}
	// Check once immediately so a spawn that beat us to the punch
	// returns without waiting one full poll interval.
	if path, _, err := ResolveLatestSince(projectDir, minMtime); err == nil {
		return path, nil
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for claude-code session in %s: %w",
				projectDir, ctx.Err())
		case <-t.C:
			if path, _, err := ResolveLatestSince(projectDir, minMtime); err == nil {
				return path, nil
			}
		}
	}
}

// ErrNoSession is returned by ResolveLatest when projectDir is
// missing or empty. Sentinel so callers can distinguish "not yet
// available" from filesystem errors.
var ErrNoSession = errNoSession{}

type errNoSession struct{}

func (errNoSession) Error() string { return "no claude-code session jsonl found" }

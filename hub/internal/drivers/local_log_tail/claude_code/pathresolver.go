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
func WaitForSession(ctx context.Context, projectDir string, pollEvery time.Duration) (string, error) {
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}
	// Check once immediately so a spawn that beat us to the punch
	// returns without waiting one full poll interval.
	if path, _, err := ResolveLatest(projectDir); err == nil {
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
			if path, _, err := ResolveLatest(projectDir); err == nil {
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

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
// slugs: EVERY non-alphanumeric character becomes `-`, not just the
// path separator. Anthropic's session docs state the rule ("the
// absolute working directory with every non-alphanumeric character
// replaced by `-`"), and it is verified on-host (2026-08-05) by
// running a session in a probe directory and reading back the
// directory claude created:
//
//	/home/ubuntu/mux-pod           →    -home-ubuntu-mux-pod
//	/tmp/foo                       →    -tmp-foo
//	/tmp/scratchpad/enc_probe.v1   →    -tmp-scratchpad-enc-probe-v1
//
// The third pair is the one that matters: `_` and `.` both collapse.
// Until that probe this function replaced separators only, which is
// indistinguishable from the real rule on every slug we had ever
// observed — all of them held nothing but separators and
// alphanumerics — and wrong for any workdir containing `_` or `.`
// (`my_project`, `repo.git`, `v1.2`). It failed silent: the tail
// waited forever on a directory claude would never create, and
// teleport packed an engine-state bundle with nothing in it.
//
// Non-ASCII is the one case still unverified. We replace any rune
// outside [A-Za-z0-9], which matches a JS `replace(/[^a-zA-Z0-9]/g,
// '-')` over BMP code points (claude-code is TypeScript) but not
// necessarily over surrogate pairs. Re-probe before trusting this for
// a workdir with non-ASCII characters in it.
//
// claude-code resolves project session files under
// `<config-home>/projects/<slug>/<session-uuid>.jsonl`. Cleans the
// input (no trailing slash, collapses doubles) before encoding so
// `/home/ubuntu/proj/` and `/home/ubuntu/proj` produce the same
// slug.
func EncodeProjectDir(cwd string) string {
	cwd = filepath.Clean(cwd)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, cwd)
}

// ConfigHomeEnvVar is the environment variable claude-code reads to
// relocate its entire config home — sessions, settings, credentials
// and the `claude daemon` roster all move with it. Anthropic's
// settings page documents neither the variable nor its blast radius
// (anthropics/claude-code#33430 asks for that), but the Agent SDK's
// session docs are explicit that transcripts live under
// `$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/` when it is set.
//
// Worth knowing who this is for: the variable's dominant real-world
// use is running two claude.ai accounts (work + personal) side by
// side, so ignoring it breaks **subscription** users. An API-key user
// has no reason to set it, which is why reading only the SDK docs —
// where it reads as a hosting detail — understates the blast radius.
const ConfigHomeEnvVar = "CLAUDE_CONFIG_DIR"

// ConfigHomeFor is the env-free form: claude-code's config home for a
// GIVEN home directory, with no environment consulted. Teleport keys
// everything off an explicit host home (source/target), never off this
// process's env, so it needs this form — the same split kimi's
// StoreHome/StoreHomeFor pair makes for the same reason.
func ConfigHomeFor(home string) string {
	return filepath.Join(home, ".claude")
}

// ConfigHome resolves THIS process's config home: $CLAUDE_CONFIG_DIR
// when set, else `<home>/.claude`.
func ConfigHome() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(ConfigHomeEnvVar)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve HOME: %w", err)
	}
	return ConfigHomeFor(home), nil
}

// ResolveConfigHome picks the config home a spawned claude child will
// ACTUALLY use, in the order the child itself resolves it:
//
//  1. `spawnOverride` — the value the spawn exports into the child's
//     environment (env-profile vars are exported ahead of the command,
//     `launch_m4_locallogtail.go`). Host-runner's own environment never
//     sees this, so a tail that consulted only os.Getenv would resolve
//     a different directory than the child writes to.
//  2. host-runner's own `$CLAUDE_CONFIG_DIR`, which the child inherits
//     when the spawn doesn't override it.
//  3. `<home>/.claude`.
func ResolveConfigHome(spawnOverride, home string) string {
	if dir := strings.TrimSpace(spawnOverride); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv(ConfigHomeEnvVar)); dir != "" {
		return dir
	}
	return ConfigHomeFor(home)
}

// ProjectDirIn returns claude-code's per-cwd session directory under an
// already-resolved config home: `<configHome>/projects/<encoded-cwd>`.
// Callers that know which root the child uses (the M4 launcher, via
// ResolveConfigHome) should prefer this over ProjectDirFor.
func ProjectDirIn(configHome, cwd string) string {
	return filepath.Join(configHome, "projects", EncodeProjectDir(cwd))
}

// ProjectDirFor returns the absolute path of claude-code's per-cwd
// session directory for a given home, ignoring $CLAUDE_CONFIG_DIR:
// `<homeDir>/.claude/projects/<encoded-cwd>`. This is the env-free
// form — see ConfigHomeFor for why teleport wants it.
func ProjectDirFor(homeDir, cwd string) string {
	return ProjectDirIn(ConfigHomeFor(homeDir), cwd)
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

// Package antigravity is the Antigravity-CLI (`agy`) plug-in for the
// LocalLogTailDriver (ADR-035 / ADR-027 D9). It implements
// locallogtail.Adapter for an engine that has neither ACP (M1) nor
// --output-format (M2), so M4 LocalLogTail is the only driving mode.
//
// It mirrors the claude_code leaf set but with two genuinely different
// leaves, because agy's on-disk session log differs structurally from
// claude-code's:
//
//   - pathresolver (this file): the session id is a conversationId agy
//     mints itself, recorded in a workspace→id cache rather than encoded
//     from the cwd. We look it up (claude-code derives the dir from the
//     cwd slug).
//   - reader: the transcript is a rewritten *snapshot* keyed by
//     step_index — steps go RUNNING→DONE in place — not an append-only
//     log, so the shared tail-from-offset Tailer does not apply
//     (see reader.go).
//
// Everything host-verified on agy 1.0.1; see ADR-035 for the probes.
package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StoreDir is agy's per-user state root: <home>/.gemini/antigravity-cli.
// Everything else (the workspace→conversation cache, the per-conversation
// brain dirs) hangs off this.
func StoreDir(homeDir string) string {
	return filepath.Join(homeDir, ".gemini", "antigravity-cli")
}

// cacheFile is the workspace-abspath → conversationId map agy writes
// after it mints a conversation. Verified shape (agy 1.0.1):
//
//	{ "/home/ubuntu/agytest": "04c40a15-…", "/tmp": "9adcd869-…" }
func cacheFile(homeDir string) string {
	return filepath.Join(StoreDir(homeDir), "cache", "last_conversations.json")
}

// TranscriptPath is the watch-and-diff oracle for one conversation:
// <store>/brain/<conversationId>/.system_generated/logs/transcript_full.jsonl.
func TranscriptPath(homeDir, conversationID string) string {
	return filepath.Join(StoreDir(homeDir), "brain", conversationID,
		".system_generated", "logs", "transcript_full.jsonl")
}

// ConversationIDForWorkdir returns the conversationId agy associated with
// workdir, reading the workspace→id cache. The workdir is filepath.Clean'd
// before lookup so a trailing slash doesn't miss. Returns ("", false) when
// the cache is missing, unparseable, or has no entry for workdir yet —
// callers treat that as "agy hasn't minted the conversation; keep polling".
func ConversationIDForWorkdir(homeDir, workdir string) (string, bool) {
	b, err := os.ReadFile(cacheFile(homeDir))
	if err != nil {
		return "", false
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return "", false
	}
	id, ok := m[filepath.Clean(workdir)]
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// WaitForConversation blocks until agy mints a conversationId for our
// spawn, or the context fires. pollEvery defaults to 250ms.
//
// Signal: a `brain/<convId>/` directory whose mtime is strictly after
// `since`. agy creates this dir the instant it mints a conversation in
// response to the first user message — reliable regardless of which
// internal persistence mechanism agy is using, and scoped by `since` so
// pre-existing brain dirs (from earlier spawns, sibling agy procs, or a
// lazy flush of an old session that races our launch) can never
// mis-resolve a fresh spawn.
//
// `since` MUST be the moment of `agy` launch. Passing the zero value
// disables the signal entirely (Wait will block until ctx fires).
//
// The legacy `last_conversations.json` cache used to be a secondary
// fallback here, but the v1.0.645 W11 smoke caught it mis-resolving
// every fresh spawn against a prior conversation: agy writes that cache
// LAZILY on graceful exit (the diagnosis that said "agy doesn't write
// it for project workdirs" was wrong — agy writes it later, when the
// process shuts down). So a fresh spawn that fires after a prior exit
// flushed the cache will hit a workdir→old-conv-id mapping and resume
// the wrong session. Dropped in v1.0.646.
//
// `workdir` is retained in the signature for the error message; the
// resolver itself doesn't read it.
//
// newestBrainFallback (opt-in) is the after-timeout last resort: when
// set, after the ctx deadline the resolver returns the absolute newest
// brain dir, ignoring `since`. Safe only for callers that can guarantee
// single-spawn isolation (per-spawn HOME). Off by default.
func WaitForConversation(ctx context.Context, homeDir, workdir string, pollEvery time.Duration, newestBrainFallback bool, since time.Time) (string, error) {
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}
	tryOnce := func() (string, bool) {
		if since.IsZero() {
			return "", false
		}
		return newestBrainSince(homeDir, since)
	}
	if id, ok := tryOnce(); ok {
		return id, nil
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if newestBrainFallback {
				if id, err := newestBrainID(homeDir); err == nil {
					return id, nil
				}
			}
			return "", fmt.Errorf("waiting for agy conversation id for %s: %w", workdir, ctx.Err())
		case <-t.C:
			if id, ok := tryOnce(); ok {
				return id, nil
			}
		}
	}
}

// newestBrainSince returns the basename of the most-recently-modified
// brain/*/ directory whose mtime is strictly after `since`, or ("",
// false) if no such directory exists. Mirrors newestBrainID but scoped
// by time so concurrent spawns or pre-existing dirs don't cross-talk.
func newestBrainSince(homeDir string, since time.Time) (string, bool) {
	brainDir := filepath.Join(StoreDir(homeDir), "brain")
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return "", false
	}
	var bestID string
	var bestT time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if !info.ModTime().After(since) {
			continue
		}
		if bestID == "" || info.ModTime().After(bestT) {
			bestID = e.Name()
			bestT = info.ModTime()
		}
	}
	return bestID, bestID != ""
}

// WaitForTranscript blocks until the transcript_full.jsonl for conversationID
// exists (agy creates it lazily on the first step), or the context fires.
// Returns the absolute path on success.
func WaitForTranscript(ctx context.Context, homeDir, conversationID string, pollEvery time.Duration) (string, error) {
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}
	path := TranscriptPath(homeDir, conversationID)
	if fileExists(path) {
		return path, nil
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for agy transcript %s: %w", path, ctx.Err())
		case <-t.C:
			if fileExists(path) {
				return path, nil
			}
		}
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// newestBrainID returns the conversationId of the most recently modified
// brain/*/ directory. Used only as WaitForConversation's opt-in fallback.
func newestBrainID(homeDir string) (string, error) {
	brainDir := filepath.Join(StoreDir(homeDir), "brain")
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return "", err
	}
	var bestID string
	var bestT time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if bestID == "" || info.ModTime().After(bestT) {
			bestID = e.Name()
			bestT = info.ModTime()
		}
	}
	if bestID == "" {
		return "", fmt.Errorf("no agy brain dirs under %s", brainDir)
	}
	return bestID, nil
}

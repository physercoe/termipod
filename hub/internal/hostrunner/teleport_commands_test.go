package hostrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudecode "github.com/termipod/hub/internal/drivers/local_log_tail/claude_code"
)

// memBlobStore is an in-memory content-addressed handoff.BlobStore, standing in
// for the hub blob API so the pack→unpack round-trip runs without a hub.
type memBlobStore struct{ m map[string][]byte }

func newMemBlobStore() *memBlobStore { return &memBlobStore{m: map[string][]byte{}} }

func (s *memBlobStore) Put(_ context.Context, body []byte, _ string) (string, error) {
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	cp := make([]byte, len(body))
	copy(cp, body)
	s.m[sha] = cp
	return sha, nil
}

func (s *memBlobStore) Get(_ context.Context, sha string) ([]byte, error) {
	b, ok := s.m[sha]
	if !ok {
		return nil, fmt.Errorf("blob not found: %s", sha)
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

// TestTeleportHandoffRoundTrip exercises the full source→target handoff core
// across two HETEROGENEOUS hosts — DIFFERENT home directories with the same
// relative layout (ADR-057 D-6). The source's worktree + repo live under
// srcHome; pack rewrites them home-relative (`~/…`); the target re-anchors them
// under its own tgtHome. Wires git (#417) + engine-state (#418) + transport
// (#416) via the command cores.
func TestTeleportHandoffRoundTrip(t *testing.T) {
	const sessionID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	const transcript = `{"type":"user","text":"continue on the gpu box"}` + "\n"

	// Source "host": home with the shared remote, repo and worktree under it.
	srcHome := t.TempDir()
	remote, srcRepo, srcWt, branch := makeSharedRemoteWorkspaceUnder(t, srcHome)
	writeClaudeSession(t, srcHome, srcWt, sessionID, transcript)

	store := newMemBlobStore()
	packRes, err := runHandoffPack(context.Background(), store, srcHome, handoffPackArgs{
		Engine:          "claude-code",
		WorktreePath:    srcWt,
		Repo:            srcRepo,
		Branch:          branch,
		Remote:          "origin",
		EngineSessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("runHandoffPack: %v", err)
	}
	if packRes.HeadSHA == "" || packRes.ManifestSHA == "" {
		t.Fatalf("pack result incomplete: %+v", packRes)
	}
	if packRes.Branch != branch {
		t.Fatalf("pack result branch: got %q want %q", packRes.Branch, branch)
	}
	// The source paths must have been rewritten home-relative for the target.
	if packRes.PortableWorktreePath != "~/wt" || packRes.PortableRepo != "~/src" {
		t.Fatalf("portable paths: got worktree=%q repo=%q want ~/wt, ~/src",
			packRes.PortableWorktreePath, packRes.PortableRepo)
	}

	// Target "host": a DIFFERENT home with the repo cloned at the SAME relative
	// path (~/src). No home is shared with the source.
	tgtHome := t.TempDir()
	tgit(t, tgtHome, "clone", remote, filepath.Join(tgtHome, "src"))

	unpackRes, err := runHandoffUnpack(context.Background(), store, tgtHome, handoffUnpackArgs{
		Engine:          "claude-code",
		Repo:            packRes.PortableRepo,         // "~/src"
		WorktreePath:    packRes.PortableWorktreePath, // "~/wt"
		Branch:          branch,
		Remote:          "origin",
		ExpectHead:      packRes.HeadSHA,
		EngineSessionID: sessionID,
		ManifestSHA:     packRes.ManifestSHA,
	})
	if err != nil {
		t.Fatalf("runHandoffUnpack: %v", err)
	}
	// The target worktree path re-anchored under tgtHome (NOT srcHome).
	wantWt := filepath.Join(tgtHome, "wt")
	if unpackRes.WorktreePath != wantWt {
		t.Fatalf("target worktree path: got %s want %s", unpackRes.WorktreePath, wantWt)
	}

	// 1) Worktree WIP the source hadn't committed is on the target.
	if got, _ := os.ReadFile(filepath.Join(wantWt, "wip.txt")); string(got) != "in progress\n" {
		t.Fatalf("worktree WIP not handed off: %q", got)
	}
	// 2) Engine transcript landed at the TARGET's resolver path (target home +
	//    target worktree → target slug), with the source's content.
	tgtJSONL := filepath.Join(claudecode.ProjectDirFor(tgtHome, wantWt), sessionID+".jsonl")
	got, rerr := os.ReadFile(tgtJSONL)
	if rerr != nil {
		t.Fatalf("engine transcript not restored on target: %v", rerr)
	}
	if string(got) != transcript {
		t.Fatalf("transcript mismatch: got %q want %q", got, transcript)
	}
}

func TestTeleportHandoffPack_RejectsBadArgs(t *testing.T) {
	if _, err := runHandoffPack(context.Background(), newMemBlobStore(), t.TempDir(), handoffPackArgs{}); err == nil {
		t.Fatal("expected error on empty pack args")
	}
}

func TestTeleportHandoffUnpack_RejectsBadArgs(t *testing.T) {
	if _, err := runHandoffUnpack(context.Background(), newMemBlobStore(), t.TempDir(), handoffUnpackArgs{Engine: "claude-code"}); err == nil {
		t.Fatal("expected error on incomplete unpack args")
	}
	// Pack refuses an empty session id; the restore must be as strict — an
	// empty id would write the engine state at a nonsense path and the resume
	// would cold-start while the command reports success.
	if _, err := runHandoffUnpack(context.Background(), newMemBlobStore(), t.TempDir(), handoffUnpackArgs{
		Engine: "claude-code", Repo: "r", WorktreePath: "w", Branch: "b", ManifestSHA: "m",
	}); err == nil || !strings.Contains(err.Error(), "engine_session_id") {
		t.Fatalf("expected engine_session_id refusal, got %v", err)
	}
}

// A tampered manifest fails the transport integrity check, so unpack refuses
// rather than restoring corrupt engine state.
func TestTeleportHandoffUnpack_DetectsCorruptBundle(t *testing.T) {
	const sessionID = "cccc"
	remote, _, srcWt, branch := makeSharedRemoteWorkspace(t)
	srcHome := t.TempDir()
	writeClaudeSession(t, srcHome, srcWt, sessionID, "data\n")
	store := newMemBlobStore()
	packRes, err := runHandoffPack(context.Background(), store, srcHome, handoffPackArgs{
		Engine: "claude-code", WorktreePath: srcWt, Branch: branch, Remote: "origin", EngineSessionID: sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt one stored part's bytes (same key, different content).
	for k, v := range store.m {
		if k == packRes.ManifestSHA {
			continue
		}
		tampered := make([]byte, len(v))
		copy(tampered, v)
		if len(tampered) > 0 {
			tampered[0] ^= 0xff
		}
		store.m[k] = tampered
		break
	}
	troot := t.TempDir()
	tgtRepo := filepath.Join(troot, "tgt")
	tgit(t, troot, "clone", remote, tgtRepo)
	_, err = runHandoffUnpack(context.Background(), store, t.TempDir(), handoffUnpackArgs{
		Engine: "claude-code", Repo: tgtRepo, WorktreePath: filepath.Join(troot, "wt"),
		Branch: branch, Remote: "origin", ExpectHead: packRes.HeadSHA,
		EngineSessionID: sessionID, ManifestSHA: packRes.ManifestSHA,
	})
	if err == nil {
		t.Fatal("expected corrupt-bundle detection")
	}
}

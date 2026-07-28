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

// TestTeleportHandoffRoundTrip exercises the full source→target handoff core:
// git WIP snapshot + push, engine-state chunked upload, then on the target a
// git fetch+worktree add, chunked download, and engine-state restore — all
// across two "hosts" (distinct homes + repo clones) sharing one bare remote and
// one blob store. This is the T1a integration: git (#417) + engine-state (#418)
// + transport (#416) wired by the command cores.
func TestTeleportHandoffRoundTrip(t *testing.T) {
	const sessionID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
	const transcript = `{"type":"user","text":"continue on the gpu box"}` + "\n"

	remote, _, srcWt, branch := makeSharedRemoteWorkspace(t)

	// Source host HOME + its claude session state for the worktree cwd.
	srcHome := t.TempDir()
	writeClaudeSession(t, srcHome, srcWt, sessionID, transcript)

	store := newMemBlobStore()

	packRes, err := runHandoffPack(context.Background(), store, srcHome, handoffPackArgs{
		Engine:          "claude-code",
		WorktreePath:    srcWt,
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

	// Target host: fresh clone of the shared remote + its own HOME + worktree
	// path. Different home + worktree ⇒ the engine state must remap.
	troot := t.TempDir()
	tgtRepo := filepath.Join(troot, "tgt")
	tgit(t, troot, "clone", remote, tgtRepo)
	tgtHome := t.TempDir()
	tgtWt := filepath.Join(troot, "wt-target")

	unpackRes, err := runHandoffUnpack(context.Background(), store, tgtHome, handoffUnpackArgs{
		Engine:          "claude-code",
		Repo:            tgtRepo,
		WorktreePath:    tgtWt,
		Branch:          branch,
		Remote:          "origin",
		ExpectHead:      packRes.HeadSHA,
		EngineSessionID: sessionID,
		ManifestSHA:     packRes.ManifestSHA,
	})
	if err != nil {
		t.Fatalf("runHandoffUnpack: %v", err)
	}
	if unpackRes.WorktreePath != tgtWt {
		t.Fatalf("unpack worktree path: got %s want %s", unpackRes.WorktreePath, tgtWt)
	}

	// 1) Worktree files (incl. the WIP the source hadn't committed) are on the
	//    target.
	if got, _ := os.ReadFile(filepath.Join(tgtWt, "wip.txt")); string(got) != "in progress\n" {
		t.Fatalf("worktree WIP not handed off: %q", got)
	}
	// 2) Engine transcript landed at the TARGET's resolver path (target slug),
	//    with the source's content.
	tgtJSONL := filepath.Join(claudecode.ProjectDirFor(tgtHome, tgtWt), sessionID+".jsonl")
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

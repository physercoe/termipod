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
	kimicode "github.com/termipod/hub/internal/drivers/local_log_tail/kimi_code"
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

// TestTeleportHandoffRoundTrip_NonWorktree exercises the T2a path: a session
// with NO git worktree, whose working directory itself moves as a tar bundle
// (alongside the engine state), across two heterogeneous homes. No git is
// touched; the target re-anchors the portable workdir under its own home.
func TestTeleportHandoffRoundTrip_NonWorktree(t *testing.T) {
	const sessionID = "aaaa1111-2222-3333-4444-555566667777"
	const transcript = `{"type":"user","text":"non-worktree teleport"}` + "\n"

	srcHome := t.TempDir()
	srcWorkdir := filepath.Join(srcHome, "scratch")
	if err := os.MkdirAll(filepath.Join(srcWorkdir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcWorkdir, "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcWorkdir, "data", "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeClaudeSession(t, srcHome, srcWorkdir, sessionID, transcript)

	store := newMemBlobStore()
	packRes, err := runHandoffPack(context.Background(), store, srcHome, handoffPackArgs{
		Engine:          "claude-code",
		Workdir:         "~/scratch",
		EngineSessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("runHandoffPack: %v", err)
	}
	if packRes.ManifestSHA == "" || packRes.WorkdirManifestSHA == "" {
		t.Fatalf("non-worktree pack incomplete: %+v", packRes)
	}
	if packRes.PortableWorkdirPath != "~/scratch" {
		t.Fatalf("portable workdir: got %q want ~/scratch", packRes.PortableWorkdirPath)
	}
	// A non-worktree pack must not touch git.
	if packRes.HeadSHA != "" || packRes.Branch != "" {
		t.Fatalf("non-worktree pack should not touch git: %+v", packRes)
	}

	tgtHome := t.TempDir()
	unpackRes, err := runHandoffUnpack(context.Background(), store, tgtHome, handoffUnpackArgs{
		Engine:             "claude-code",
		Workdir:            packRes.PortableWorkdirPath, // "~/scratch"
		EngineSessionID:    sessionID,
		ManifestSHA:        packRes.ManifestSHA,
		WorkdirManifestSHA: packRes.WorkdirManifestSHA,
	})
	if err != nil {
		t.Fatalf("runHandoffUnpack: %v", err)
	}
	wantWorkdir := filepath.Join(tgtHome, "scratch")
	if unpackRes.Workdir != wantWorkdir {
		t.Fatalf("target workdir: got %s want %s", unpackRes.Workdir, wantWorkdir)
	}
	if unpackRes.WorktreePath != "" {
		t.Fatalf("non-worktree unpack should report no worktree path, got %q", unpackRes.WorktreePath)
	}
	// 1) Workdir tree restored under the target home.
	if got, _ := os.ReadFile(filepath.Join(wantWorkdir, "main.py")); string(got) != "print('hi')\n" {
		t.Fatalf("workdir file not restored: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(wantWorkdir, "data", "notes.md")); string(got) != "# notes\n" {
		t.Fatalf("nested workdir file not restored: %q", got)
	}
	// 2) Engine transcript at the TARGET resolver path (target home + target workdir).
	tgtJSONL := filepath.Join(claudecode.ProjectDirFor(tgtHome, wantWorkdir), sessionID+".jsonl")
	if got, rerr := os.ReadFile(tgtJSONL); rerr != nil || string(got) != transcript {
		t.Fatalf("engine transcript not restored: err=%v got=%q", rerr, got)
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

// TestTeleportHandoffRoundTrip_Kimi mirrors TestTeleportHandoffRoundTrip for
// kimi-code-ts (ticket #429): same heterogeneous-host handoff, but the engine
// state is kimi's session TREE with its cwd→wd_* remap and the state.json /
// workspaces.json fixups the target's resume needs.
func TestTeleportHandoffRoundTrip_Kimi(t *testing.T) {
	const sessionID = "session_77777777-8888-9999-aaaa-bbbbbbbbbbbb"

	srcHome := t.TempDir()
	remote, srcRepo, srcWt, branch := makeSharedRemoteWorkspaceUnder(t, srcHome)
	writeKimiSession(t, srcHome, srcWt, sessionID)

	store := newMemBlobStore()
	packRes, err := runHandoffPack(context.Background(), store, srcHome, handoffPackArgs{
		Engine:          "kimi-code-ts",
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

	tgtHome := t.TempDir()
	tgit(t, tgtHome, "clone", remote, filepath.Join(tgtHome, "src"))

	unpackRes, err := runHandoffUnpack(context.Background(), store, tgtHome, handoffUnpackArgs{
		Engine:          "kimi-code-ts",
		Repo:            packRes.PortableRepo,
		WorktreePath:    packRes.PortableWorktreePath,
		Branch:          branch,
		Remote:          "origin",
		ExpectHead:      packRes.HeadSHA,
		EngineSessionID: sessionID,
		ManifestSHA:     packRes.ManifestSHA,
	})
	if err != nil {
		t.Fatalf("runHandoffUnpack: %v", err)
	}
	wantWt := filepath.Join(tgtHome, "wt")
	if unpackRes.WorktreePath != wantWt {
		t.Fatalf("target worktree path: got %s want %s", unpackRes.WorktreePath, wantWt)
	}

	// The session tree landed under the TARGET's wd_* with state.json
	// rewritten to the target worktree — kimi will resume it there.
	tgtStore := kimicode.StoreHomeFor(tgtHome)
	wdID, lerr := kimicode.LookupWorkspaceID(tgtStore, wantWt)
	if lerr != nil {
		t.Fatalf("target workspaces.json not synthesized: %v", lerr)
	}
	tgtSessionDir := filepath.Join(tgtStore, "sessions", wdID, sessionID)
	wire, rerr := os.ReadFile(filepath.Join(tgtSessionDir, "agents", "main", "wire.jsonl"))
	if rerr != nil {
		t.Fatalf("wire log not restored on target: %v", rerr)
	}
	if !strings.Contains(string(wire), `"protocol_version"`) {
		t.Fatalf("wire log content mismatch: %q", wire)
	}
	stRaw, rerr := os.ReadFile(filepath.Join(tgtSessionDir, "state.json"))
	if rerr != nil {
		t.Fatalf("state.json not restored: %v", rerr)
	}
	if !strings.Contains(string(stRaw), kimicode.ResolveWorkdirRoot(wantWt)) {
		t.Fatalf("state.json workDir not rewritten to target: %s", stRaw)
	}
}

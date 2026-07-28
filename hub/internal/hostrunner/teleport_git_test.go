package hostrunner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// git helper for tests: run a git command in cwd, fail the test on error.
func tgit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), cwd, args...)
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), cwd, err, out)
	}
	return out
}

// makeSharedRemoteWorkspace builds a bare "shared remote" plus a source clone
// with one committed file on a session branch and a worktree carrying WIP —
// the state a teleport source starts from. Returns (remote, sourceRepo,
// worktreePath, branch).
func makeSharedRemoteWorkspace(t *testing.T) (remote, srcRepo, wtPath, branch string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	tgit(t, root, "init", "--bare", remote)

	srcRepo = filepath.Join(root, "src")
	tgit(t, root, "clone", remote, srcRepo)
	// Deterministic identity for the seed commit.
	tgit(t, srcRepo, "config", "user.name", "seed")
	tgit(t, srcRepo, "config", "user.email", "seed@t.local")
	if err := os.WriteFile(filepath.Join(srcRepo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgit(t, srcRepo, "add", "-A")
	tgit(t, srcRepo, "commit", "-m", "base")
	tgit(t, srcRepo, "push", "origin", "HEAD:refs/heads/main")

	branch = "hub/worker-1"
	wtPath = filepath.Join(root, "wt")
	// Create the session worktree on the branch (as a spawn would).
	created, err := EnsureWorktree(context.Background(), WorktreeSpec{Repo: srcRepo, Path: wtPath, Branch: branch})
	if err != nil || !created {
		t.Fatalf("EnsureWorktree: created=%v err=%v", created, err)
	}
	// Agent writes some WIP (uncommitted).
	if err := os.WriteFile(filepath.Join(wtPath, "wip.txt"), []byte("in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return remote, srcRepo, wtPath, branch
}

func TestGitCommitAndPush_SnapshotsWIP(t *testing.T) {
	remote, _, wtPath, branch := makeSharedRemoteWorkspace(t)

	head, err := gitCommitAndPush(context.Background(), wtPath, branch, "origin")
	if err != nil {
		t.Fatalf("gitCommitAndPush: %v", err)
	}
	if head == "" {
		t.Fatal("empty head")
	}
	// The remote now carries the branch at exactly this head.
	remoteHead := strings.TrimSpace(tgit(t, remote, "rev-parse", "refs/heads/"+branch))
	if remoteHead != head {
		t.Fatalf("remote head %s != pushed head %s", remoteHead, head)
	}
	// The WIP is now committed (worktree clean).
	if isDirty(context.Background(), wtPath) {
		t.Fatal("worktree still dirty after commit+push")
	}
}

func TestGitCommitAndPush_CleanWorktree(t *testing.T) {
	remote, _, wtPath, branch := makeSharedRemoteWorkspace(t)
	// Commit the WIP first so the worktree is clean going into the handoff.
	tgit(t, wtPath, "config", "user.name", "a")
	tgit(t, wtPath, "config", "user.email", "a@t.local")
	tgit(t, wtPath, "add", "-A")
	tgit(t, wtPath, "commit", "-m", "already committed")
	before := strings.TrimSpace(tgit(t, wtPath, "rev-parse", "HEAD"))

	head, err := gitCommitAndPush(context.Background(), wtPath, branch, "origin")
	if err != nil {
		t.Fatalf("gitCommitAndPush: %v", err)
	}
	if head != before {
		t.Fatalf("clean worktree should not add a commit: head %s != %s", head, before)
	}
	if got := strings.TrimSpace(tgit(t, remote, "rev-parse", "refs/heads/"+branch)); got != head {
		t.Fatalf("remote head %s != %s", got, head)
	}
}

func TestGitFetchAndAddWorktree_TargetReceivesWIP(t *testing.T) {
	remote, _, wtPath, branch := makeSharedRemoteWorkspace(t)
	head, err := gitCommitAndPush(context.Background(), wtPath, branch, "origin")
	if err != nil {
		t.Fatalf("source push: %v", err)
	}

	// Target host: a fresh clone of the same shared remote.
	troot := t.TempDir()
	tgtRepo := filepath.Join(troot, "tgt")
	tgit(t, troot, "clone", remote, tgtRepo)
	tgtWt := filepath.Join(troot, "wt")

	if err := gitFetchAndAddWorktree(context.Background(), tgtRepo, tgtWt, branch, "origin", head); err != nil {
		t.Fatalf("gitFetchAndAddWorktree: %v", err)
	}
	// The WIP file the source produced is present on the target worktree.
	got, err := os.ReadFile(filepath.Join(tgtWt, "wip.txt"))
	if err != nil {
		t.Fatalf("read wip on target: %v", err)
	}
	if string(got) != "in progress\n" {
		t.Fatalf("wip content mismatch: %q", got)
	}
	if h := strings.TrimSpace(tgit(t, tgtWt, "rev-parse", "HEAD")); h != head {
		t.Fatalf("target head %s != source head %s", h, head)
	}
}

func TestGitFetchAndAddWorktree_HeadMismatchFails(t *testing.T) {
	remote, _, wtPath, branch := makeSharedRemoteWorkspace(t)
	if _, err := gitCommitAndPush(context.Background(), wtPath, branch, "origin"); err != nil {
		t.Fatalf("source push: %v", err)
	}
	troot := t.TempDir()
	tgtRepo := filepath.Join(troot, "tgt")
	tgit(t, troot, "clone", remote, tgtRepo)
	tgtWt := filepath.Join(troot, "wt")

	badHead := "0000000000000000000000000000000000000000"
	if err := gitFetchAndAddWorktree(context.Background(), tgtRepo, tgtWt, branch, "origin", badHead); err == nil {
		t.Fatal("expected head-mismatch error")
	}
}

func TestGitCommitAndPush_DetachedHeadRefused(t *testing.T) {
	_, _, wtPath, _ := makeSharedRemoteWorkspace(t)
	// Detach HEAD in the worktree.
	tgit(t, wtPath, "config", "user.name", "a")
	tgit(t, wtPath, "config", "user.email", "a@t.local")
	tgit(t, wtPath, "add", "-A")
	tgit(t, wtPath, "commit", "-m", "c")
	head := strings.TrimSpace(tgit(t, wtPath, "rev-parse", "HEAD"))
	tgit(t, wtPath, "checkout", head) // detach

	if _, err := gitCommitAndPush(context.Background(), wtPath, "", "origin"); err == nil {
		t.Fatal("expected detached-HEAD refusal")
	}
}

func TestGitCommitAndPush_NoRemoteFailsLoudly(t *testing.T) {
	// A worktree whose repo has no matching remote: push must fail with a
	// clear error (the shared-remote precondition, ADR-057 D-6).
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	tgit(t, root, "init", repo)
	tgit(t, repo, "config", "user.name", "a")
	tgit(t, repo, "config", "user.email", "a@t.local")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgit(t, repo, "add", "-A")
	tgit(t, repo, "commit", "-m", "c")
	wt := filepath.Join(root, "wt")
	if _, err := EnsureWorktree(context.Background(), WorktreeSpec{Repo: repo, Path: wt, Branch: "hub/x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gitCommitAndPush(context.Background(), wt, "hub/x", "origin"); err == nil {
		t.Fatal("expected push failure with no remote")
	}
}

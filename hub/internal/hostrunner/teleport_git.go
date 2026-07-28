package hostrunner

import (
	"context"
	"fmt"
	"strings"
)

// Git handoff for session teleport (ADR-057 D-2/D-6). A worktree session's
// working files move between hosts through a git remote both hosts can reach:
// the SOURCE commits any uncommitted work on the session branch and pushes it;
// the TARGET fetches that branch and checks it out into a fresh worktree. These
// build on the same primitives worktree.go already uses (runGit, isDirty,
// branchExists, EnsureWorktree) so the teleport path and the normal spawn path
// treat git identically.
//
// The shared-remote assumption is T1's precondition (ADR-057 D-6): hosts that
// share no remote need a hub-relayed bare repo, which is T3. A push/fetch
// failure surfaces verbatim so the precondition fails loudly rather than
// silently corrupting a half-moved session.

// teleportCommitIdentity is used only for the WIP snapshot commit a teleport
// makes on the source, so `git commit` never fails on a repo with no
// user.name/user.email configured. It is a marker, not a real author — the
// working files are the payload, not the commit metadata.
var teleportCommitIdent = []string{
	"-c", "user.name=termipod-teleport",
	"-c", "user.email=teleport@termipod.local",
}

// gitCommitAndPush is the SOURCE side of a teleport git handoff. It commits any
// uncommitted work in the worktree at worktreePath onto `branch` (defaulting to
// the worktree's current branch) and pushes it to `remote` (default "origin"),
// returning the resulting branch head SHA and the RESOLVED branch name — the
// caller needs the latter for the unpack args when it passed branch as "". A
// clean worktree is pushed as-is (no empty commit). The caller relocates the
// engine-state separately (D-2).
func gitCommitAndPush(ctx context.Context, worktreePath, branch, remote string) (headSHA, resolvedBranch string, err error) {
	if worktreePath == "" {
		return "", "", fmt.Errorf("teleport: empty worktree path")
	}
	if !isGitRepo(ctx, worktreePath) {
		return "", "", fmt.Errorf("teleport: %s is not a git worktree", worktreePath)
	}
	if remote == "" {
		remote = "origin"
	}
	if branch == "" {
		out, berr := runGit(ctx, worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
		if berr != nil {
			return "", "", fmt.Errorf("teleport: resolve branch: %w: %s", berr, out)
		}
		branch = strings.TrimSpace(out)
		if branch == "" || branch == "HEAD" {
			return "", "", fmt.Errorf("teleport: worktree is in detached HEAD; cannot teleport")
		}
	}

	// Snapshot uncommitted work so it survives the move. A clean worktree
	// skips straight to push (its last commit is already the payload).
	if isDirty(ctx, worktreePath) {
		if out, aerr := runGit(ctx, worktreePath, "add", "-A"); aerr != nil {
			return "", "", fmt.Errorf("teleport: git add: %w: %s", aerr, out)
		}
		args := append(append([]string{}, teleportCommitIdent...),
			"commit", "--no-verify", "-m", "teleport: WIP snapshot")
		if out, cerr := runGit(ctx, worktreePath, args...); cerr != nil {
			return "", "", fmt.Errorf("teleport: git commit: %w: %s", cerr, out)
		}
	}

	if out, perr := runGit(ctx, worktreePath, "push", remote, branch); perr != nil {
		return "", "", fmt.Errorf("teleport: push %s to %s failed (no shared remote?): %w: %s",
			branch, remote, perr, out)
	}

	out, herr := runGit(ctx, worktreePath, "rev-parse", "HEAD")
	if herr != nil {
		return "", "", fmt.Errorf("teleport: resolve head: %w: %s", herr, out)
	}
	return strings.TrimSpace(out), branch, nil
}

// gitFetchAndAddWorktree is the TARGET side of a teleport git handoff. It
// fetches `branch` from `remote` (default "origin") into `repo` and checks it
// out into a new worktree at worktreePath. When expectHead is non-empty it
// verifies the checked-out HEAD matches the SHA the source pushed, so a stale
// or wrong-branch fetch fails loudly rather than resuming on the wrong state.
func gitFetchAndAddWorktree(ctx context.Context, repo, worktreePath, branch, remote, expectHead string) error {
	if repo == "" || worktreePath == "" || branch == "" {
		return fmt.Errorf("teleport: repo, worktree path and branch are required")
	}
	if !isGitRepo(ctx, repo) {
		return fmt.Errorf("teleport: %s is not a git repo", repo)
	}
	if remote == "" {
		remote = "origin"
	}

	// Fetch with an explicit refspec so the LOCAL branch ref is created or
	// fast-forwarded to what the source just pushed. A bare `fetch remote
	// branch` only updates the remote-tracking ref: a stale local branch left
	// from a prior residence of this session on this host (a return teleport —
	// source cleanup removes the worktree, not the branch) would then be
	// checked out as-is and fail the head verification below. Non-fast-forward
	// local divergence or a branch checked out in another worktree still fails
	// loudly, which is the right outcome for both.
	if out, ferr := runGit(ctx, repo, "fetch", remote, branch+":"+branch); ferr != nil {
		return fmt.Errorf("teleport: fetch %s from %s failed (no shared remote?): %w: %s",
			branch, remote, ferr, out)
	}

	if out, werr := runGit(ctx, repo, "worktree", "add", worktreePath, branch); werr != nil {
		return fmt.Errorf("teleport: worktree add %s: %w: %s", branch, werr, out)
	}

	if expectHead != "" {
		out, herr := runGit(ctx, worktreePath, "rev-parse", "HEAD")
		if herr != nil {
			return fmt.Errorf("teleport: verify head: %w: %s", herr, out)
		}
		if got := strings.TrimSpace(out); got != expectHead {
			return fmt.Errorf("teleport: head mismatch after fetch: got %s want %s", got, expectHead)
		}
	}
	return nil
}

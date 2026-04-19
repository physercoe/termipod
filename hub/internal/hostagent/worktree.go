package hostagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Worktree management is a best-effort convenience: spawn specs can ask for
// a git worktree to be created ahead of the pane, and terminate commands
// can clean it up on the way out. We intentionally don't fail the spawn
// just because git is unhappy — the pane still launches and the user can
// decide whether to fix or ignore.
//
// Design: one worktree per agent, rooted at the spawn's WorktreePath.
// Branch defaults to `hub/<handle>` when the spec omits one, so concurrent
// spawns with distinct handles never collide in the same repo.

type WorktreeSpec struct {
	Repo   string // path to the source git repo
	Path   string // absolute path for the new worktree dir
	Branch string // branch name; must be unique per repo
	Base   string // start point for the branch (defaults to HEAD)
}

// EnsureWorktree creates Path as a worktree of Repo on Branch. If Path
// already exists and is already a worktree of Repo, it's treated as a
// no-op — this makes host-agent safe to restart without double-creating.
//
// Returns (created bool, err). `created` distinguishes "we just made it"
// from "it was already there" for logging; callers usually don't branch
// on it.
func EnsureWorktree(ctx context.Context, spec WorktreeSpec) (bool, error) {
	if spec.Repo == "" || spec.Path == "" {
		return false, nil // nothing to do
	}
	if !isGitRepo(ctx, spec.Repo) {
		return false, fmt.Errorf("not a git repo: %s", spec.Repo)
	}
	if info, err := os.Stat(spec.Path); err == nil && info.IsDir() {
		if isGitRepo(ctx, spec.Path) {
			// Already a checkout — assume it's ours from a prior run.
			return false, nil
		}
		return false, fmt.Errorf("%s exists and is not a git worktree", spec.Path)
	}
	branch := spec.Branch
	if branch == "" {
		branch = "hub/" + deriveBranchSuffix(spec.Path)
	}

	// If the branch already exists, add the worktree on it; otherwise create
	// the branch at Base (default HEAD) and check it out in the new worktree.
	if branchExists(ctx, spec.Repo, branch) {
		out, err := runGit(ctx, spec.Repo, "worktree", "add", spec.Path, branch)
		if err != nil {
			return false, fmt.Errorf("worktree add existing: %w: %s", err, out)
		}
		return true, nil
	}
	args := []string{"worktree", "add", "-b", branch, spec.Path}
	if spec.Base != "" {
		args = append(args, spec.Base)
	}
	out, err := runGit(ctx, spec.Repo, args...)
	if err != nil {
		return false, fmt.Errorf("worktree add new: %w: %s", err, out)
	}
	return true, nil
}

// RemoveWorktree cleans up the worktree at Path, prefering a safe removal
// (clean tree). Returns (dirty bool, err): `dirty` = true means the
// worktree had uncommitted changes and was left in place. Callers typically
// log that and move on — destroying a dirty agent's work would be
// unrecoverable.
func RemoveWorktree(ctx context.Context, spec WorktreeSpec) (dirty bool, err error) {
	if spec.Repo == "" || spec.Path == "" {
		return false, nil
	}
	if _, err := os.Stat(spec.Path); os.IsNotExist(err) {
		// Already gone (crashed mid-way, or never created); treat as success.
		_, _ = runGit(ctx, spec.Repo, "worktree", "prune")
		return false, nil
	}
	if isDirty(ctx, spec.Path) {
		return true, nil
	}
	out, err := runGit(ctx, spec.Repo, "worktree", "remove", spec.Path)
	if err != nil {
		return false, fmt.Errorf("worktree remove: %w: %s", err, out)
	}
	return false, nil
}

// isGitRepo returns true if `path` sits inside a git working tree. We use
// rev-parse because it understands both regular repos and worktrees
// (unlike just checking for a `.git` directory).
func isGitRepo(ctx context.Context, path string) bool {
	_, err := runGit(ctx, path, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func branchExists(ctx context.Context, repo, branch string) bool {
	_, err := runGit(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// isDirty returns true when the worktree has any uncommitted changes,
// staged or not, tracked or untracked. Conservative: on git error we
// report dirty so the caller won't remove an indeterminate worktree.
func isDirty(ctx context.Context, path string) bool {
	out, err := runGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return true
	}
	return strings.TrimSpace(out) != ""
}

func runGit(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// deriveBranchSuffix produces a filesystem-safe slug from the worktree
// path's basename — used when the spec omits an explicit branch name.
// Avoids collisions from concurrent spawns in the same repo.
func deriveBranchSuffix(path string) string {
	// Strip trailing separators and keep the last component.
	p := strings.TrimRight(path, "/\\")
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "worktree"
	}
	return p
}

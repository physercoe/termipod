package hostrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit sets up a real git repo with one commit so `git worktree add`
// has a HEAD to branch from. Returns the repo path.
func gitInit(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		// Isolate from the developer's global git config — the local tests
		// should not depend on (or pollute) user.email / user.name.
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	return repo
}

func TestEnsureAndRemoveWorktree_Clean(t *testing.T) {
	repo := gitInit(t)
	wtPath := filepath.Join(t.TempDir(), "wt")
	spec := WorktreeSpec{Repo: repo, Path: wtPath, Branch: "feature/x"}

	created, err := EnsureWorktree(context.Background(), spec)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !created {
		t.Errorf("expected created=true on first call")
	}
	// README.md should exist in the new worktree — confirms checkout worked.
	if _, err := os.Stat(filepath.Join(wtPath, "README.md")); err != nil {
		t.Errorf("worktree contents missing: %v", err)
	}

	// Second call is a no-op; path already a git dir.
	created, err = EnsureWorktree(context.Background(), spec)
	if err != nil {
		t.Fatalf("ensure idempotent: %v", err)
	}
	if created {
		t.Errorf("expected created=false on second call")
	}

	// Clean removal.
	dirty, err := RemoveWorktree(context.Background(), spec)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if dirty {
		t.Errorf("fresh worktree flagged dirty")
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after remove: %v", err)
	}
}

func TestRemoveWorktree_DirtyPreserved(t *testing.T) {
	repo := gitInit(t)
	wtPath := filepath.Join(t.TempDir(), "wt")
	spec := WorktreeSpec{Repo: repo, Path: wtPath, Branch: "feature/dirty"}

	if _, err := EnsureWorktree(context.Background(), spec); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Introduce an uncommitted change — the worktree must NOT be removed.
	if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"),
		[]byte("unsaved work\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	dirty, err := RemoveWorktree(context.Background(), spec)
	if err != nil {
		t.Fatalf("remove dirty: %v", err)
	}
	if !dirty {
		t.Errorf("expected dirty=true for untracked file")
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("dirty worktree should remain on disk, got: %v", err)
	}
}

func TestEnsureWorktree_NoopWithoutSpec(t *testing.T) {
	// Missing Repo/Path is a valid no-op (spawn has no worktree request);
	// must not error so callers can unconditionally call it.
	created, err := EnsureWorktree(context.Background(), WorktreeSpec{})
	if err != nil || created {
		t.Errorf("empty spec: got created=%v err=%v", created, err)
	}
}

func TestEnsureWorktree_NonGitRepoRejects(t *testing.T) {
	notRepo := t.TempDir()
	wtPath := filepath.Join(t.TempDir(), "wt")
	_, err := EnsureWorktree(context.Background(),
		WorktreeSpec{Repo: notRepo, Path: wtPath, Branch: "b"})
	if err == nil {
		t.Errorf("expected error when Repo is not a git checkout")
	}
}

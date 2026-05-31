package hostrunner

import (
	"os"
	"path/filepath"
	"testing"
)

// DeriveWorkdir is the shared workdir resolver consumed by M1, M2,
// and (via the same precedence rules) the M4 launchers. The cases
// below lock the four-rung precedence (explicit > project-bound >
// project-less > legacy-empty) so a regression doesn't reintroduce
// the v1.0.709 codex M2 smoke-failure where a steward spawned
// without a project_id but WITH context_files errored at
// writeContextFiles instead of getting a derived workdir.
func TestDeriveWorkdir(t *testing.T) {
	cases := []struct {
		name           string
		team           string
		defaultWorkdir string
		projectID      string
		handle         string
		childID        string
		needsWorkdir   bool
		want           string
	}{
		{
			name:           "explicit-wins-over-everything",
			team:           "acme",
			defaultWorkdir: "~/hub-work/general",
			projectID:      "01HXYZABCDEFGH",
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   true,
			// Operator-pinned path is taken verbatim — no team segment
			// injected (the operator chose the absolute path).
			want: "~/hub-work/general",
		},
		{
			name:           "project-id-derives-with-team-segment",
			team:           "acme",
			defaultWorkdir: "",
			projectID:      "01HXYZABCDEFGH", // first 8 = 01HXYZAB
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "acme", "01HXYZAB", "coder"),
		},
		{
			name:           "project-id-derives-even-without-context",
			team:           "acme",
			defaultWorkdir: "",
			projectID:      "01HXYZAB", // exact 8 chars
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   false,
			want:           filepath.Join("~", "hub-work", "acme", "01HXYZAB", "coder"),
		},
		{
			name:           "handle-falls-back-to-child-id",
			team:           "acme",
			defaultWorkdir: "",
			projectID:      "01HXYZAB",
			handle:         "",
			childID:        "agent-1",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "acme", "01HXYZAB", "agent-1"),
		},
		{
			// The codex-steward smoke failure (v1.0.709): no project,
			// context_files materialisation requires a workdir → use
			// the project-less _team namespace, under the team segment.
			name:           "team-fallback-when-no-project-and-needs-workdir",
			team:           "acme",
			defaultWorkdir: "",
			projectID:      "",
			handle:         "codex-steward",
			childID:        "agent-codex",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "acme", "_team", "codex-steward"),
		},
		{
			// Back-compat: an empty team collapses the segment, so
			// pre-W5 callers and demo spawns keep their legacy paths.
			name:           "empty-team-keeps-legacy-path",
			team:           "",
			defaultWorkdir: "",
			projectID:      "01HXYZAB",
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "01HXYZAB", "coder"),
		},
		{
			// Legacy demo path: nothing to materialise into a workdir,
			// the agent runs from host-runner's cwd. Preserved for
			// back-compat with templates that ship without
			// context_files / mcp_token (the workdir-less smoke
			// fixtures in the existing test suite).
			name:           "empty-when-no-project-and-no-workdir-need",
			team:           "acme",
			defaultWorkdir: "",
			projectID:      "",
			handle:         "demo",
			childID:        "agent-demo",
			needsWorkdir:   false,
			want:           "",
		},
		{
			// Defensive: a misconfigured spawn with no project, no
			// handle, and no childID asks for a workdir — surface
			// the misconfiguration by leaving it empty rather than
			// collapsing every such spawn into one shared dir.
			name:           "empty-when-needs-workdir-but-no-identifiers",
			team:           "acme",
			defaultWorkdir: "",
			projectID:      "",
			handle:         "",
			childID:        "",
			needsWorkdir:   true,
			want:           "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveWorkdir(
				tc.team,
				tc.defaultWorkdir,
				tc.projectID,
				tc.handle,
				tc.childID,
				tc.needsWorkdir,
			)
			if got != tc.want {
				t.Fatalf("DeriveWorkdir = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestDeriveWorkdir_TeamsDoNotCollide is the W5 isolation assertion
// (ADR-037 D6): two teams sharing the same project prefix and handle
// resolve to distinct on-host workdirs, so they cannot share a mutable
// subtree on a shared host.
func TestDeriveWorkdir_TeamsDoNotCollide(t *testing.T) {
	a := DeriveWorkdir("team-a", "", "01HXYZAB", "coder", "agent-1", true)
	b := DeriveWorkdir("team-b", "", "01HXYZAB", "coder", "agent-1", true)
	if a == b {
		t.Fatalf("teams collided on workdir: %q", a)
	}
	if want := filepath.Join("~", "hub-work", "team-a", "01HXYZAB", "coder"); a != want {
		t.Fatalf("team-a workdir = %q; want %q", a, want)
	}
	if want := filepath.Join("~", "hub-work", "team-b", "01HXYZAB", "coder"); b != want {
		t.Fatalf("team-b workdir = %q; want %q", b, want)
	}
}

// TestEnsureTeamWorkRoot is the W5 FS-layer guard (ADR-037 D6): the
// per-team root is created 0o700 so the OS denies cross-team reads
// (load-bearing when teams run under distinct OS users). An empty team
// is a no-op for back-compat.
func TestEnsureTeamWorkRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := ensureTeamWorkRoot("team-a")
	if err != nil {
		t.Fatalf("ensureTeamWorkRoot: %v", err)
	}
	want := filepath.Join(home, "hub-work", "team-a")
	if got != want {
		t.Fatalf("root = %q; want %q", got, want)
	}
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat team root: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("team root perm = %o; want 700", perm)
	}

	// Empty team is a no-op (legacy spawns): no path, no error, and no
	// stray `~/hub-work` created. Use a fresh HOME so the team-a dirs
	// above don't mask the absence assertion.
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	r, err := ensureTeamWorkRoot("")
	if err != nil || r != "" {
		t.Fatalf("empty team: got (%q, %v); want (\"\", nil)", r, err)
	}
	if _, err := os.Stat(filepath.Join(home2, "hub-work")); !os.IsNotExist(err) {
		t.Fatalf("empty team created ~/hub-work (stat err=%v)", err)
	}
}

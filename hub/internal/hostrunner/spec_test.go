package hostrunner

import (
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
		defaultWorkdir string
		projectID      string
		handle         string
		childID        string
		needsWorkdir   bool
		want           string
	}{
		{
			name:           "explicit-wins-over-everything",
			defaultWorkdir: "~/hub-work/general",
			projectID:      "01HXYZABCDEFGH",
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   true,
			want:           "~/hub-work/general",
		},
		{
			name:           "project-id-derives-from-pid8-and-handle",
			defaultWorkdir: "",
			projectID:      "01HXYZABCDEFGH", // first 8 = 01HXYZAB
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "01HXYZAB", "coder"),
		},
		{
			name:           "project-id-derives-even-without-context",
			defaultWorkdir: "",
			projectID:      "01HXYZAB", // exact 8 chars
			handle:         "coder",
			childID:        "agent-1",
			needsWorkdir:   false,
			want:           filepath.Join("~", "hub-work", "01HXYZAB", "coder"),
		},
		{
			name:           "handle-falls-back-to-child-id",
			defaultWorkdir: "",
			projectID:      "01HXYZAB",
			handle:         "",
			childID:        "agent-1",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "01HXYZAB", "agent-1"),
		},
		{
			// The codex-steward smoke failure (v1.0.709): no project,
			// context_files materialisation requires a workdir → use
			// the project-less _team namespace.
			name:           "team-fallback-when-no-project-and-needs-workdir",
			defaultWorkdir: "",
			projectID:      "",
			handle:         "codex-steward",
			childID:        "agent-codex",
			needsWorkdir:   true,
			want:           filepath.Join("~", "hub-work", "_team", "codex-steward"),
		},
		{
			// Legacy demo path: nothing to materialise into a workdir,
			// the agent runs from host-runner's cwd. Preserved for
			// back-compat with templates that ship without
			// context_files / mcp_token (the workdir-less smoke
			// fixtures in the existing test suite).
			name:           "empty-when-no-project-and-no-workdir-need",
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

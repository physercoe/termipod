package server

import (
	"errors"
	"strings"
	"testing"
)

// TestMutateBackendCmdFlag_ClaudeModel — verify the claude `--model X`
// flag swap (the canonical use case for the picker on a claude steward).
func TestMutateBackendCmdFlag_ClaudeModel(t *testing.T) {
	in := `kind: steward
backend:
  kind: claude-code
  cmd: claude --model claude-3-5-sonnet-20241022 --print --output-format stream-json
prompt: foo
`
	out, err := mutateBackendCmdFlag(in, "model", "claude-3-7-opus")
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if !strings.Contains(out, "claude --model claude-3-7-opus --print --output-format stream-json") {
		t.Errorf("expected new model in cmd; got:\n%s", out)
	}
	// Sanity: prompt must not have moved or duplicated.
	if strings.Count(out, "prompt:") != 1 {
		t.Errorf("prompt: count != 1 in output; YAML structure regressed:\n%s", out)
	}
}

// TestMutateBackendCmdFlag_PermissionMode — same shape, different flag.
func TestMutateBackendCmdFlag_PermissionMode(t *testing.T) {
	in := `kind: steward
backend:
  kind: claude-code
  cmd: claude --permission-mode default --print
`
	out, err := mutateBackendCmdFlag(in, "permission-mode", "yolo")
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if !strings.Contains(out, "--permission-mode yolo --print") {
		t.Errorf("expected permission-mode flipped; got:\n%s", out)
	}
}

// TestMutateBackendCmdFlag_FlagAbsent — unknown flag errors with
// errFlagNotInCmd so the caller can decide between a typed 422 and a
// future "append flag if missing" path.
func TestMutateBackendCmdFlag_FlagAbsent(t *testing.T) {
	in := `backend:
  cmd: claude --print
`
	_, err := mutateBackendCmdFlag(in, "model", "x")
	if !errors.Is(err, errFlagNotInCmd) {
		t.Fatalf("err = %v; want errFlagNotInCmd", err)
	}
}

// TestMutateBackendCmdFlag_NoBackend — spec without a backend mapping
// is a config bug, not a no-op. Must surface as a typed error.
func TestMutateBackendCmdFlag_NoBackend(t *testing.T) {
	in := `kind: steward
prompt: foo
`
	_, err := mutateBackendCmdFlag(in, "model", "x")
	if err == nil || !strings.Contains(err.Error(), "missing backend") {
		t.Fatalf("err = %v; want missing-backend error", err)
	}
}

// TestMutateBackendCmdFlag_PreservesOtherFields — channel_id, project_id,
// fallback_modes etc. must round-trip unchanged. Otherwise a respawn
// could silently drop binding metadata and orphan the agent from its
// session/project.
func TestMutateBackendCmdFlag_PreservesOtherFields(t *testing.T) {
	in := `kind: steward
channel_id: chan-123
project_id: proj-456
fallback_modes: [M2, M4]
backend:
  kind: claude-code
  cmd: claude --model old --print
worktree:
  repo: git@example.com:foo/bar.git
  branch: feature/x
`
	out, err := mutateBackendCmdFlag(in, "model", "new")
	if err != nil {
		t.Fatalf("mutate: %v", err)
	}
	for _, must := range []string{
		"channel_id: chan-123",
		"project_id: proj-456",
		"fallback_modes:",
		"repo: git@example.com:foo/bar.git",
		"branch: feature/x",
		"--model new --print",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("output missing %q\nfull yaml:\n%s", must, out)
		}
	}
}

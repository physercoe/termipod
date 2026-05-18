package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// W4 fail-fast: DoSpawn must reject specs that have no backend.cmd
// after rendering. The pre-bundle behaviour (v1.0.619 and earlier)
// let the empty-cmd spec reach the host-runner, which fell through
// to the launcher placeholder (interactive bash), keystroked the
// task prompt into the shell, and entered a respawn loop. See
// docs/discussions/validate-at-every-boundary.md §1.

func TestDoSpawn_FailFast_EmptySpawnSpec(t *testing.T) {
	s, _ := newTestServer(t)
	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "ff-empty",
		Kind:        "claude-code",
		SpawnSpec:   "",
	})
	if err == nil {
		t.Fatal("expected error for empty SpawnSpec; got nil")
	}
	// Empty spec is caught earlier by the required-field check
	// (handle/kind/spawn_spec_yaml all required); status 400.
	if status != http.StatusBadRequest {
		t.Errorf("status = %d; want %d (existing required-field gate)",
			status, http.StatusBadRequest)
	}
}

func TestDoSpawn_FailFast_NoBackendBlock(t *testing.T) {
	s, _ := newTestServer(t)
	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "ff-noblock",
		Kind:        "claude-code",
		// Spec has no backend block at all.
		SpawnSpec: "driving_mode: M4\n",
	})
	if err == nil {
		t.Fatal("expected 422 error for spec with no backend block; got nil")
	}
	if status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d; want 422", status)
	}
	if !strings.Contains(err.Error(), "backend.cmd") {
		t.Errorf("error = %q; should name backend.cmd", err.Error())
	}
	// W3 (ADR-031): the 422 also points at tools_get for the full
	// input shape, so a steward can recover without guessing.
	if !strings.Contains(err.Error(), "tools_get") {
		t.Errorf("error = %q; should point at tools_get('agents_spawn')", err.Error())
	}
}

func TestDoSpawn_FailFast_BackendBlockNoCmd(t *testing.T) {
	s, _ := newTestServer(t)
	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "ff-nocmd",
		Kind:        "claude-code",
		// backend block present but cmd missing.
		SpawnSpec: "backend:\n  kind: claude-code\n  model: claude-opus-4-7\n",
	})
	if err == nil {
		t.Fatal("expected 422 error for backend block without cmd; got nil")
	}
	if status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d; want 422", status)
	}
	if !strings.Contains(err.Error(), "backend.cmd") {
		t.Errorf("error = %q; should name backend.cmd", err.Error())
	}
}

func TestDoSpawn_FailFast_TemplateReferenceCanonicalForm(t *testing.T) {
	// The canonical agent-template reference per
	// docs/reference/agent-template-naming.md is the prefixed form
	// `agents.<basename>`. The hub-side W1 merge loads the template
	// and populates backend.cmd, so this spec passes the W4 gate and
	// the spawn succeeds.
	s, _ := newTestServer(t)
	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "ff-template-canonical",
		Kind:        "claude-code",
		SpawnSpec:   "template: agents.coder\n",
	})
	if err != nil {
		t.Fatalf("canonical template form rejected: status=%d err=%v", status, err)
	}
	if status < 200 || status >= 300 {
		t.Errorf("unexpected status = %d (err=%v); want 2xx", status, err)
	}
}

func TestDoSpawn_FailFast_AcceptsExplicitBackendCmd(t *testing.T) {
	// Happy path: fully-specified spec passes the gate.
	s, _ := newTestServer(t)
	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "ff-happy",
		Kind:        "claude-code",
		SpawnSpec:   "backend:\n  cmd: claude --print\n  kind: claude-code\n",
	})
	if err != nil {
		t.Fatalf("expected success; got err=%v status=%d", err, status)
	}
}

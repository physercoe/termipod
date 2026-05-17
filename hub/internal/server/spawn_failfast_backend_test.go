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

func TestDoSpawn_FailFast_TemplateReferenceAloneRejectedPreW1(t *testing.T) {
	// This test pins the BUG the incident reproduced: a bare
	// `template: coder.v1` spec is rejected by W4 because
	// renderSpawnSpec (pre-W1) does NOT load the template's
	// backend.cmd. After W1 lands, this test should be updated to
	// expect success (template merge populates backend.cmd).
	//
	// The point of keeping this test even after W1: if W1 ever
	// regresses, W4 still catches it and the steward sees 422
	// instead of bash-pane-respawn-loop.
	s, _ := newTestServer(t)
	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "ff-template-only",
		Kind:        "claude-code",
		SpawnSpec:   "template: coder.v1\n",
	})
	// Pre-W1: rejected by W4 with 422. Post-W1: succeeds with 201
	// because the template merge populates backend.cmd before W4 runs.
	// What's NOT acceptable is the pre-bundle silent fall-through to
	// bash. Assert one of {422 with backend.cmd error, 2xx success}.
	switch {
	case status == http.StatusUnprocessableEntity:
		if !strings.Contains(err.Error(), "backend.cmd") {
			t.Errorf("status=422 error = %q; should name backend.cmd", err.Error())
		}
	case status >= 200 && status < 300:
		if err != nil {
			t.Errorf("status=%d but err = %v", status, err)
		}
		// W1 landed; template merge populated backend.cmd.
	default:
		t.Errorf("unexpected status = %d (err=%v); want 422 (pre-W1) or 2xx (post-W1)",
			status, err)
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

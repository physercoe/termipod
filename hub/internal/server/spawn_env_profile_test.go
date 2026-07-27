package server

import (
	"context"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/hostrunner"
)

// End-to-end: a spawn that attaches an env_profile_id gets the profile's
// env_vars materialized into the persisted spawn_spec_yaml (env-profiles plan,
// E1b), in the exact shape host-runner's ParseSpec reads.

func querySpawnSpec(t *testing.T, s *Server, agentID string) string {
	t.Helper()
	var spec string
	if err := s.db.QueryRow(
		`SELECT COALESCE(spawn_spec_yaml, '') FROM agent_spawns WHERE child_agent_id = ?`,
		agentID).Scan(&spec); err != nil {
		t.Fatalf("query spawn_spec_yaml: %v", err)
	}
	return spec
}

func TestDoSpawn_EnvProfile_MaterializedIntoSpec(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	prof, err := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name:        "gpu",
		EnvVars:     map[string]string{"HF_HOME": "/data/hf", "CUDA_VISIBLE_DEVICES": "0"},
		SetupScript: "pip install -r requirements.txt",
		// secret_refs present → should spawn (with a warning), not fail.
		SecretRefs: []secretRef{{Key: "OPENAI_API_KEY", VaultItem: "openai-prod"}},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	out, status, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle:  "gpu-worker",
		Kind:         "claude-code",
		EnvProfileID: prof.ID,
		SpawnSpec:    "backend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	spec := querySpawnSpec(t, s, out.AgentID)
	if !strings.Contains(spec, "env_profile_id: "+prof.ID) {
		t.Fatalf("persisted spec missing env_profile_id:\n%s", spec)
	}
	parsed, err := hostrunner.ParseSpec(spec)
	if err != nil {
		t.Fatalf("ParseSpec of persisted spec: %v", err)
	}
	if parsed.EnvVars["HF_HOME"] != "/data/hf" || parsed.EnvVars["CUDA_VISIBLE_DEVICES"] != "0" {
		t.Fatalf("env_vars not materialized: %v", parsed.EnvVars)
	}
	if parsed.SetupScript != "pip install -r requirements.txt" {
		t.Fatalf("setup_script not materialized: %q", parsed.SetupScript)
	}
	if parsed.SetupFailurePolicy != "fail" {
		t.Fatalf("setup_failure_policy should default to fail, got %q", parsed.SetupFailurePolicy)
	}
	if parsed.Backend.Cmd != "echo test" {
		t.Fatalf("existing backend.cmd clobbered: %q", parsed.Backend.Cmd)
	}
}

func TestDoSpawn_EnvProfile_NotFound(t *testing.T) {
	s, _ := newTestServer(t)

	_, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:  "orphan-worker",
		Kind:         "claude-code",
		EnvProfileID: "does-not-exist",
		SpawnSpec:    "backend:\n  cmd: echo test\n",
	})
	if err == nil {
		t.Fatalf("expected error for unknown env_profile_id")
	}
	if status != 400 {
		t.Fatalf("status = %d; want 400 for unknown env_profile_id", status)
	}
}

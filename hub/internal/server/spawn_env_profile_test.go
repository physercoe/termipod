package server

import (
	"context"
	"net/http"
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

// ADR-056 D-4: a profile carrying secret_refs REQUIRES a client-sealed
// env_secret_envelope. A spawn without one is refused 422 — headless/API spawns
// of secret-bearing profiles fail by design.
func TestDoSpawn_SecretProfile_WithoutEnvelope_Refused(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	prof, err := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name:       "prod-secrets",
		SecretRefs: []secretRef{{Key: "OPENAI_API_KEY", VaultItem: "openai-prod"}},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	_, status, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle:  "w",
		Kind:         "claude-code",
		EnvProfileID: prof.ID,
		SpawnSpec:    "backend:\n  cmd: echo test\n",
		// No env_secret_envelope.
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got status=%d err=%v", status, err)
	}
	if err == nil || !strings.Contains(err.Error(), "secret_refs") {
		t.Fatalf("expected a secret_refs 422 error, got %v", err)
	}
}

// With an envelope, a secret-bearing spawn succeeds and the opaque envelope is
// stored verbatim on the spawn row (the hub never decrypts it).
func TestDoSpawn_SecretProfile_WithEnvelope_Stored(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	prof, err := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name:       "prod-secrets",
		SecretRefs: []secretRef{{Key: "OPENAI_API_KEY", VaultItem: "openai-prod"}},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	const envelope = `{"v":1,"host_id":"h1","profile_id":"p1","epk":"AAAA","nonce":"BBBB","ct":"CCCC"}`
	out, status, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle:       "w",
		Kind:              "claude-code",
		EnvProfileID:      prof.ID,
		EnvSecretEnvelope: envelope,
		SpawnSpec:         "backend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}

	var stored string
	if err := s.db.QueryRow(
		`SELECT COALESCE(env_secret_envelope, '') FROM agent_spawns WHERE child_agent_id = ?`,
		out.AgentID).Scan(&stored); err != nil {
		t.Fatalf("query envelope: %v", err)
	}
	if stored != envelope {
		t.Fatalf("stored envelope mismatch:\n got %s\nwant %s", stored, envelope)
	}
}

func TestProjectEnvProfileIDFromYAML(t *testing.T) {
	if got := projectEnvProfileIDFromYAML(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := projectEnvProfileIDFromYAML("phases: [a, b]\n"); got != "" {
		t.Fatalf("absent key: %q", got)
	}
	if got := projectEnvProfileIDFromYAML("env_profile_id: prof-9\nphases: [x]\n"); got != "prof-9" {
		t.Fatalf("present: %q", got)
	}
	if got := projectEnvProfileIDFromYAML("{not: valid: yaml"); got != "" {
		t.Fatalf("parse-fail should yield empty: %q", got)
	}
}

func seedProjectWithConfig(t *testing.T, s *Server, name, configYAML string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, config_yaml, created_at, kind)
		VALUES (?, ?, ?, ?, ?, 'goal')`,
		id, defaultTeamID, name, configYAML, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func TestDoSpawn_EnvProfile_ProjectInherit(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	prof, err := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name:    "proj-default",
		EnvVars: map[string]string{"TEAM_ENV": "prod"},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	projID := seedProjectWithConfig(t, s, "inherit-proj", "env_profile_id: "+prof.ID+"\n")

	// Spawn bound to the project, WITHOUT an explicit env_profile_id — it
	// should inherit the project's.
	out, status, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle: "inherit-worker",
		Kind:        "claude-code",
		SpawnSpec:   "project_id: " + projID + "\nbackend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	parsed, err := hostrunner.ParseSpec(querySpawnSpec(t, s, out.AgentID))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if parsed.EnvVars["TEAM_ENV"] != "prod" {
		t.Fatalf("project env profile not inherited: %v", parsed.EnvVars)
	}
}

func TestDoSpawn_EnvProfile_ExplicitBeatsProject(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	projProf, _ := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name: "proj-prof", EnvVars: map[string]string{"WHICH": "project"}})
	spawnProf, _ := s.createEnvProfile(ctx, defaultTeamID, envProfileBody{
		Name: "spawn-prof", EnvVars: map[string]string{"WHICH": "spawn"}})
	projID := seedProjectWithConfig(t, s, "override-proj", "env_profile_id: "+projProf.ID+"\n")

	out, _, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle:  "override-worker",
		Kind:         "claude-code",
		EnvProfileID: spawnProf.ID,
		SpawnSpec:    "project_id: " + projID + "\nbackend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	parsed, _ := hostrunner.ParseSpec(querySpawnSpec(t, s, out.AgentID))
	if parsed.EnvVars["WHICH"] != "spawn" {
		t.Fatalf("explicit env_profile_id should beat project's: %v", parsed.EnvVars)
	}
}

func TestDoSpawn_EnvProfile_InheritedMissingIsLenient(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()

	// Project references a profile id that doesn't exist (e.g. deleted after).
	projID := seedProjectWithConfig(t, s, "stale-proj", "env_profile_id: ghost-profile\n")

	// The spawn must succeed (no 400) — inheritance is best-effort.
	out, status, err := s.DoSpawn(ctx, defaultTeamID, spawnIn{
		ChildHandle: "stale-worker",
		Kind:        "claude-code",
		SpawnSpec:   "project_id: " + projID + "\nbackend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("inherited-missing profile should not fail spawn: %v (status=%d)", err, status)
	}
	parsed, _ := hostrunner.ParseSpec(querySpawnSpec(t, s, out.AgentID))
	if parsed.EnvProfileID != "" || len(parsed.EnvVars) != 0 {
		t.Fatalf("stale inherit should materialize nothing: id=%q vars=%v",
			parsed.EnvProfileID, parsed.EnvVars)
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

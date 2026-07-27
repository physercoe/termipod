package server

import (
	"context"
	"encoding/json"
	"testing"
)

// Exercises the env-profile store methods (shared by the REST handlers and the
// future env_profile_* MCP tools) end to end: create → get → list → patch →
// delete, plus the boundary validations (env-var name, failure policy,
// network-policy mode, unique name per team).

func TestEnvProfileCRUD(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	created, err := s.createEnvProfile(ctx, team, envProfileBody{
		Name:        "gpu-box",
		Description: "CUDA training env",
		SetupScript: "pip install -r requirements.txt",
		EnvVars:     map[string]string{"CUDA_VISIBLE_DEVICES": "0", "HF_HOME": "/data/hf"},
		SecretRefs:  []secretRef{{Key: "OPENAI_API_KEY", VaultItem: "openai-prod"}},
		NetworkPolicy: networkPolicy{
			Mode:      "allowlist",
			Allowlist: []string{"api.openai.com"},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Name != "gpu-box" {
		t.Fatalf("unexpected created row: %+v", created)
	}
	if created.EnvVars["HF_HOME"] != "/data/hf" || len(created.EnvVars) != 2 {
		t.Fatalf("env_vars not round-tripped: %+v", created.EnvVars)
	}
	if len(created.SecretRefs) != 1 || created.SecretRefs[0].VaultItem != "openai-prod" {
		t.Fatalf("secret_refs not round-tripped: %+v", created.SecretRefs)
	}
	if created.NetworkPolicy.Mode != "allowlist" || len(created.NetworkPolicy.Allowlist) != 1 {
		t.Fatalf("network_policy not round-tripped: %+v", created.NetworkPolicy)
	}
	if created.SetupFailurePolicy != "fail" {
		t.Fatalf("failure policy should default to fail, got %q", created.SetupFailurePolicy)
	}

	// Get by id.
	got, err := s.getEnvProfileByID(ctx, team, created.ID)
	if err != nil || got.Name != "gpu-box" {
		t.Fatalf("get: %v %+v", err, got)
	}

	// List.
	if ps, _ := s.listEnvProfiles(ctx, team); len(ps) != 1 {
		t.Fatalf("list: want 1, got %d", len(ps))
	}

	// Patch: change env_vars + failure policy; name untouched.
	patch := json.RawMessage(`{"env_vars":{"CUDA_VISIBLE_DEVICES":"1"},"setup_failure_policy":"continue"}`)
	upd, msg, err := s.patchEnvProfile(ctx, team, created.ID, patch)
	if err != nil || msg != "" {
		t.Fatalf("patch: err=%v msg=%q", err, msg)
	}
	if upd.Name != "gpu-box" {
		t.Fatalf("patch clobbered name: %q", upd.Name)
	}
	if upd.EnvVars["CUDA_VISIBLE_DEVICES"] != "1" || len(upd.EnvVars) != 1 {
		t.Fatalf("patch env_vars: %+v", upd.EnvVars)
	}
	if upd.SetupFailurePolicy != "continue" {
		t.Fatalf("patch failure policy: %q", upd.SetupFailurePolicy)
	}

	// Delete.
	ok, err := s.deleteEnvProfile(ctx, team, created.ID)
	if err != nil || !ok {
		t.Fatalf("delete: %v %v", err, ok)
	}
	if ps, _ := s.listEnvProfiles(ctx, team); len(ps) != 0 {
		t.Fatalf("after delete: want 0, got %d", len(ps))
	}
}

func TestEnvProfileValidation(t *testing.T) {
	// Bad env-var name rejected.
	b := envProfileBody{Name: "x", EnvVars: map[string]string{"1BAD": "v"}}
	if msg := validateEnvProfile(&b); msg == "" {
		t.Fatalf("expected invalid env var name to be rejected")
	}
	// Empty name rejected.
	b = envProfileBody{Name: "   "}
	if msg := validateEnvProfile(&b); msg == "" {
		t.Fatalf("expected empty name to be rejected")
	}
	// Secret ref without vault_item rejected.
	b = envProfileBody{Name: "x", SecretRefs: []secretRef{{Key: "TOK", VaultItem: ""}}}
	if msg := validateEnvProfile(&b); msg == "" {
		t.Fatalf("expected secret ref without vault_item to be rejected")
	}
	// Unknown non-empty network mode is a caller error — normalizing it
	// would silently store fail-OPEN policy ("bogus" → "open") that E4's
	// enforcement would honor.
	b = envProfileBody{Name: "x", NetworkPolicy: networkPolicy{Mode: "bogus"}}
	if msg := validateEnvProfile(&b); msg == "" {
		t.Fatalf("expected unknown network mode to be rejected")
	}
	// Empty mode still defaults to open (the declared schema default);
	// nil maps defaulted.
	b = envProfileBody{Name: "x"}
	if msg := validateEnvProfile(&b); msg != "" {
		t.Fatalf("clean body rejected: %q", msg)
	}
	if b.NetworkPolicy.Mode != "open" {
		t.Fatalf("empty mode should default to open, got %q", b.NetworkPolicy.Mode)
	}
	if b.EnvVars == nil || b.SecretRefs == nil {
		t.Fatalf("nil maps/slices should default to empty")
	}
}

func TestEnvProfileUniqueName(t *testing.T) {
	s, _ := newTestServer(t)
	ctx := context.Background()
	team := defaultTeamID

	if _, err := s.createEnvProfile(ctx, team, envProfileBody{Name: "dup"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Second profile with the same (team, name) must hit the UNIQUE index.
	if _, err := s.createEnvProfile(ctx, team, envProfileBody{Name: "dup"}); err == nil {
		t.Fatalf("expected unique-constraint error on duplicate name")
	}
}

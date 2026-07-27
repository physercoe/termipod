package server

import (
	"strings"
	"testing"

	"github.com/termipod/hub/internal/hostrunner"
)

// Exercises materializeEnvProfile (env-profiles plan, E1b/E1c) and — the load-
// bearing check — that host-runner's ParseSpec reads back exactly what the hub
// spliced in. Testing the producer against the real consumer (not a second copy
// of the parse logic) is what catches a schema drift between the two packages.

func prof(id string, env map[string]string) envProfileOut {
	p := envProfileOut{ID: id}
	p.EnvVars = env
	return p
}

func TestMaterializeEnvProfileRoundTrip(t *testing.T) {
	base := "backend:\n  cmd: claude --print\nproject_id: p1\n"
	p := prof("prof-abc", map[string]string{"HF_HOME": "/data/hf", "CUDA_VISIBLE_DEVICES": "0"})
	p.SetupScript = "pip install -r requirements.txt"
	p.SetupFailurePolicy = "continue"

	out := materializeEnvProfile(base, p)

	if !strings.Contains(out, "env_profile_id: prof-abc") {
		t.Fatalf("missing env_profile_id:\n%s", out)
	}
	if !strings.Contains(out, "cmd: claude --print") || !strings.Contains(out, "project_id: p1") {
		t.Fatalf("clobbered existing keys:\n%s", out)
	}

	// Round-trip through the REAL host-runner parser.
	spec, err := hostrunner.ParseSpec(out)
	if err != nil {
		t.Fatalf("host-runner ParseSpec: %v", err)
	}
	if spec.EnvProfileID != "prof-abc" {
		t.Fatalf("env_profile_id round-trip: %q", spec.EnvProfileID)
	}
	if spec.EnvVars["HF_HOME"] != "/data/hf" || spec.EnvVars["CUDA_VISIBLE_DEVICES"] != "0" {
		t.Fatalf("env_vars round-trip: %v", spec.EnvVars)
	}
	if spec.SetupScript != "pip install -r requirements.txt" {
		t.Fatalf("setup_script round-trip: %q", spec.SetupScript)
	}
	if spec.SetupFailurePolicy != "continue" {
		t.Fatalf("setup_failure_policy round-trip: %q", spec.SetupFailurePolicy)
	}
	if spec.Backend.Cmd != "claude --print" {
		t.Fatalf("backend.cmd survived: %q", spec.Backend.Cmd)
	}
}

func TestMaterializeEnvProfileFailurePolicyDefaults(t *testing.T) {
	p := prof("p", nil)
	p.SetupScript = "echo hi"
	// No explicit policy → normalized to fail-closed in the spec.
	out := materializeEnvProfile("backend:\n  cmd: claude\n", p)
	spec, _ := hostrunner.ParseSpec(out)
	if spec.SetupFailurePolicy != "fail" {
		t.Fatalf("default failure policy = %q; want fail", spec.SetupFailurePolicy)
	}
}

func TestMaterializeEnvProfileEmptyOmitsKeys(t *testing.T) {
	out := materializeEnvProfile("backend:\n  cmd: claude\n", prof("prof-x", nil))
	if strings.Contains(out, "env_vars:") {
		t.Fatalf("empty env should omit env_vars:\n%s", out)
	}
	if strings.Contains(out, "setup_script:") || strings.Contains(out, "setup_failure_policy:") {
		t.Fatalf("empty setup_script should omit setup keys:\n%s", out)
	}
	if !strings.Contains(out, "env_profile_id: prof-x") {
		t.Fatalf("provenance id should still be set:\n%s", out)
	}
}

func TestMaterializeEnvProfileIdempotentReplace(t *testing.T) {
	base := "backend:\n  cmd: claude\n"
	once := materializeEnvProfile(base, prof("p", map[string]string{"A": "1"}))
	twice := materializeEnvProfile(once, prof("p", map[string]string{"A": "2"}))
	if strings.Count(twice, "env_profile_id:") != 1 {
		t.Fatalf("env_profile_id duplicated:\n%s", twice)
	}
	if strings.Count(twice, "env_vars:") != 1 {
		t.Fatalf("env_vars duplicated:\n%s", twice)
	}
	spec, _ := hostrunner.ParseSpec(twice)
	if spec.EnvVars["A"] != "2" {
		t.Fatalf("re-materialize should replace value: %v", spec.EnvVars)
	}
}

func TestMaterializeEnvProfileParseFailUnchanged(t *testing.T) {
	bad := "backend: {cmd: claude"
	if got := materializeEnvProfile(bad, prof("p", map[string]string{"A": "1"})); got != bad {
		t.Fatalf("parse failure should return spec unchanged, got:\n%s", got)
	}
}

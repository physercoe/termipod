package server

import (
	"strings"
	"testing"

	"github.com/termipod/hub/internal/hostrunner"
)

// Exercises materializeEnvProfile (env-profiles plan, E1b) and — the load-
// bearing check — that host-runner's ParseSpec reads back exactly what the hub
// spliced in. Testing the producer against the real consumer (not a second copy
// of the parse logic) is what catches a schema drift between the two packages.

func TestMaterializeEnvProfileRoundTrip(t *testing.T) {
	base := "backend:\n  cmd: claude --print\nproject_id: p1\n"
	env := map[string]string{"HF_HOME": "/data/hf", "CUDA_VISIBLE_DEVICES": "0"}

	out := materializeEnvProfile(base, "prof-abc", env)

	// The hub-visible provenance + snapshot values are present.
	if !strings.Contains(out, "env_profile_id: prof-abc") {
		t.Fatalf("missing env_profile_id:\n%s", out)
	}
	// Untouched keys survive.
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
	if spec.Backend.Cmd != "claude --print" {
		t.Fatalf("backend.cmd survived: %q", spec.Backend.Cmd)
	}
}

func TestMaterializeEnvProfileEmptyEnvOmitsKey(t *testing.T) {
	out := materializeEnvProfile("backend:\n  cmd: claude\n", "prof-x", nil)
	if strings.Contains(out, "env_vars:") {
		t.Fatalf("empty env should omit env_vars:\n%s", out)
	}
	if !strings.Contains(out, "env_profile_id: prof-x") {
		t.Fatalf("provenance id should still be set:\n%s", out)
	}
}

func TestMaterializeEnvProfileIdempotentReplace(t *testing.T) {
	base := "backend:\n  cmd: claude\n"
	once := materializeEnvProfile(base, "p", map[string]string{"A": "1"})
	twice := materializeEnvProfile(once, "p", map[string]string{"A": "2"})
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
	// Not valid YAML (a bare unterminated flow mapping) → return unchanged
	// rather than fail the spawn.
	bad := "backend: {cmd: claude"
	if got := materializeEnvProfile(bad, "p", map[string]string{"A": "1"}); got != bad {
		t.Fatalf("parse failure should return spec unchanged, got:\n%s", got)
	}
}

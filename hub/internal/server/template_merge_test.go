package server

import (
	"context"
	"strings"
	"testing"
)

// W1 template merge: when spawn_spec_yaml contains `template: <name>`,
// renderSpawnSpec loads the named template and deep-merges its fields
// under the spec. The pre-bundle behaviour (v1.0.619) treated the
// `template:` key as opaque and passed it through unchanged — the
// host-runner then received a spec with no backend.cmd, fell through
// to the launcher placeholder, and entered the keystroke-into-bash
// respawn loop. See docs/discussions/validate-at-every-boundary.md.

func TestMergeTemplateReference_LoadsBundledTemplate(t *testing.T) {
	s, _ := newTestServer(t)
	merged, err := s.mergeTemplateReference("template: coder.v1\n")
	if err != nil {
		t.Fatalf("mergeTemplateReference: %v", err)
	}
	if !strings.Contains(merged, "cmd:") || !strings.Contains(merged, "claude") {
		t.Errorf("merged spec missing backend.cmd from coder.v1: %s", merged)
	}
	if strings.Contains(merged, "template:") {
		t.Errorf("merged spec still carries `template:` field; want it dropped post-merge")
	}
}

func TestMergeTemplateReference_NoTemplateKey_Passthrough(t *testing.T) {
	s, _ := newTestServer(t)
	in := "backend:\n  cmd: claude --print\n"
	out, err := s.mergeTemplateReference(in)
	if err != nil {
		t.Fatalf("mergeTemplateReference: %v", err)
	}
	if out != in {
		t.Errorf("spec without template: should pass through unchanged; got %q", out)
	}
}

func TestMergeTemplateReference_MissingTemplate_Errors(t *testing.T) {
	s, _ := newTestServer(t)
	_, err := s.mergeTemplateReference("template: does-not-exist\n")
	if err == nil {
		t.Fatal("expected error for missing template; got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error %q should name the missing template", err.Error())
	}
}

func TestMergeTemplateReference_SpecOverridesTemplate(t *testing.T) {
	// Steward references a template AND overrides a single nested
	// field. The merge keeps the template's other backend fields and
	// only swaps the overridden one.
	s, _ := newTestServer(t)
	spec := "template: coder.v1\nbackend:\n  model: claude-haiku-4-5-20251001\n"
	merged, err := s.mergeTemplateReference(spec)
	if err != nil {
		t.Fatalf("mergeTemplateReference: %v", err)
	}
	if !strings.Contains(merged, "claude-haiku-4-5-20251001") {
		t.Errorf("spec override should have replaced backend.model: %s", merged)
	}
	if !strings.Contains(merged, "cmd:") {
		t.Errorf("template's backend.cmd should still be present after deep merge: %s", merged)
	}
}

func TestMergeTemplateReference_RejectsPathTraversal(t *testing.T) {
	s, _ := newTestServer(t)
	for _, bad := range []string{
		"template: ../etc/passwd\n",
		"template: ../../secrets\n",
		"template: .hidden\n",
	} {
		_, err := s.mergeTemplateReference(bad)
		if err == nil {
			t.Errorf("mergeTemplateReference(%q): expected error, got nil", bad)
		}
	}
}

// End-to-end: the original incident's spec now succeeds.
func TestDoSpawn_TemplateOnlySpec_Succeeds(t *testing.T) {
	s, _ := newTestServer(t)
	out, status, err := s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "summarizer-2",
		Kind:        "claude-code",
		SpawnSpec:   "template: coder.v1\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v (status=%d)", err, status)
	}
	if out.AgentID == "" {
		t.Error("expected non-empty AgentID after successful template-only spawn")
	}
}

func TestDeepMergeYAMLMaps_NestedMapsMerge(t *testing.T) {
	base := map[string]any{
		"backend": map[string]any{
			"cmd":   "claude --print",
			"model": "claude-opus-4-7",
		},
		"prompt": "coder.v1.md",
	}
	over := map[string]any{
		"backend": map[string]any{
			"model": "claude-haiku-4-5", // override
		},
	}
	got := deepMergeYAMLMaps(base, over)
	be, _ := got["backend"].(map[string]any)
	if be["cmd"] != "claude --print" {
		t.Errorf("base backend.cmd should survive merge; got %v", be["cmd"])
	}
	if be["model"] != "claude-haiku-4-5" {
		t.Errorf("over backend.model should win; got %v", be["model"])
	}
	if got["prompt"] != "coder.v1.md" {
		t.Errorf("base prompt should survive; got %v", got["prompt"])
	}
}

func TestDeepMergeYAMLMaps_ListReplacesNotAppends(t *testing.T) {
	base := map[string]any{"fallback_modes": []any{"M2", "M4"}}
	over := map[string]any{"fallback_modes": []any{"M4"}}
	got := deepMergeYAMLMaps(base, over)
	fm, _ := got["fallback_modes"].([]any)
	if len(fm) != 1 || fm[0] != "M4" {
		t.Errorf("lists should replace, not append; got %v", fm)
	}
}

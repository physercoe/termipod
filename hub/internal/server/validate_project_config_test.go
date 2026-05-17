package server

import (
	"strings"
	"testing"
)

func TestValidateProjectConfigYAML_EmptyOK(t *testing.T) {
	for _, in := range []string{"", "  ", "\n\n"} {
		if reason := validateProjectConfigYAML(in, false); reason != "" {
			t.Errorf("empty/whitespace input %q should pass; got %q", in, reason)
		}
		if reason := validateProjectConfigYAML(in, true); reason != "" {
			t.Errorf("empty input %q should pass even as template; got %q", in, reason)
		}
	}
}

func TestValidateProjectConfigYAML_MalformedRejected(t *testing.T) {
	bad := ":\n  - not valid: [unclosed"
	reason := validateProjectConfigYAML(bad, false)
	if !strings.Contains(reason, "invalid YAML") {
		t.Errorf("malformed YAML should be rejected: %q", reason)
	}
}

func TestValidateProjectConfigYAML_TemplateMustHavePhases(t *testing.T) {
	noPhases := `display_name: "Empty"
description: "Goes nowhere"
`
	reason := validateProjectConfigYAML(noPhases, true)
	if !strings.Contains(reason, "phases") {
		t.Errorf("template without phases should be rejected: %q", reason)
	}

	// Non-template version of the same payload is fine.
	if reason := validateProjectConfigYAML(noPhases, false); reason != "" {
		t.Errorf("non-template without phases should pass: %q", reason)
	}
}

func TestValidateProjectConfigYAML_TemplateWithPhasesPasses(t *testing.T) {
	good := `display_name: "Research"
phases:
  - id: idea
    description: "Brainstorm"
  - id: experiment
    description: "Run it"
`
	if reason := validateProjectConfigYAML(good, true); reason != "" {
		t.Errorf("template with phases should pass: %q", reason)
	}
}

func TestValidateProjectConfigYAML_EmptyPhasesArrayRejected(t *testing.T) {
	emptyArr := `display_name: "Empty"
phases: []
`
	reason := validateProjectConfigYAML(emptyArr, true)
	if !strings.Contains(reason, "phases") {
		t.Errorf("template with empty phases array should be rejected: %q", reason)
	}
}

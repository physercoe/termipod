package server

import (
	"strings"
	"testing"
)

// W10b unit tests for the validator. Cover the happy path + each
// failure class so a future regression in the validator surfaces here
// rather than at hub start.

func TestValidateBundledAgentTemplate_AcceptsValid(t *testing.T) {
	yaml := `template: agents.test-good
backend:
  cmd: claude --print
  kind: claude-code
`
	if reason := validateBundledAgentTemplate([]byte(yaml)); reason != "" {
		t.Errorf("valid template rejected: %q", reason)
	}
}

func TestValidateBundledAgentTemplate_RejectsMissingTemplateKey(t *testing.T) {
	yaml := `backend:
  cmd: claude --print
`
	reason := validateBundledAgentTemplate([]byte(yaml))
	if !strings.Contains(reason, "template:") {
		t.Errorf("rejection should name the missing field: %q", reason)
	}
}

func TestValidateBundledAgentTemplate_RejectsEmptyBackendCmd(t *testing.T) {
	yaml := `template: agents.test-bad
backend:
  kind: claude-code
`
	reason := validateBundledAgentTemplate([]byte(yaml))
	if !strings.Contains(reason, "backend.cmd") {
		t.Errorf("rejection should name backend.cmd: %q", reason)
	}
}

func TestValidateBundledAgentTemplate_RejectsBackendCmdWhitespaceOnly(t *testing.T) {
	yaml := `template: agents.test-ws
backend:
  cmd: "   "
`
	reason := validateBundledAgentTemplate([]byte(yaml))
	if !strings.Contains(reason, "backend.cmd") {
		t.Errorf("whitespace-only cmd should be rejected: %q", reason)
	}
}

func TestValidateBundledAgentTemplate_RejectsMalformedYAML(t *testing.T) {
	bad := []byte(":\n  - not valid: [unclosed")
	reason := validateBundledAgentTemplate(bad)
	if !strings.Contains(reason, "YAML parse") {
		t.Errorf("malformed YAML should yield parse error: %q", reason)
	}
}

// Smoke test the audit walker itself: bundled templates all pass.
// If any bundled template regresses, this fails BEFORE the more
// permissive integration tests, pointing at the offending file.
func TestAuditBundledAgentTemplates_AllBundledTemplatesValid(t *testing.T) {
	if err := auditBundledAgentTemplates(); err != nil {
		t.Fatalf("bundled-template audit failed: %v", err)
	}
}

// v1.0.621 name-match enforcement per
// docs/reference/agent-template-naming.md.

func TestValidateAgentTemplateNameMatch_MatchPasses(t *testing.T) {
	body := []byte("template: agents.coder\nbackend:\n  cmd: claude --print\n")
	if reason := validateAgentTemplateNameMatch("templates/agents/coder.v1.yaml", body); reason != "" {
		t.Errorf("expected match for coder.v1.yaml ↔ agents.coder; got %q", reason)
	}
}

func TestValidateAgentTemplateNameMatch_MultiSegmentBasenameMatches(t *testing.T) {
	body := []byte("template: agents.steward.general\nbackend:\n  cmd: claude --print\n")
	if reason := validateAgentTemplateNameMatch("templates/agents/steward.general.v1.yaml", body); reason != "" {
		t.Errorf("expected match for steward.general.v1.yaml ↔ agents.steward.general; got %q", reason)
	}
}

func TestValidateAgentTemplateNameMatch_MissingPrefixFails(t *testing.T) {
	body := []byte("template: coder\nbackend:\n  cmd: claude --print\n")
	reason := validateAgentTemplateNameMatch("templates/agents/coder.v1.yaml", body)
	if !strings.Contains(reason, "agents.coder") {
		t.Errorf("rejection should name expected id `agents.coder`: %q", reason)
	}
}

func TestValidateAgentTemplateNameMatch_WrongBasenameFails(t *testing.T) {
	body := []byte("template: agents.summarizer\nbackend:\n  cmd: claude --print\n")
	reason := validateAgentTemplateNameMatch("templates/agents/coder.v1.yaml", body)
	if !strings.Contains(reason, "agents.coder") {
		t.Errorf("rejection should name expected id derived from filename: %q", reason)
	}
}

func TestValidateAgentTemplateNameMatch_VersionSuffixVariations(t *testing.T) {
	for _, c := range []struct {
		filename string
		template string
	}{
		{"coder.v1.yaml", "agents.coder"},
		{"coder.v2.yaml", "agents.coder"},
		{"coder.v10.yaml", "agents.coder"},
	} {
		body := []byte("template: " + c.template + "\nbackend:\n  cmd: x\n")
		if reason := validateAgentTemplateNameMatch("templates/agents/"+c.filename, body); reason != "" {
			t.Errorf("%s ↔ %s rejected: %q", c.filename, c.template, reason)
		}
	}
}

func TestValidateAgentTemplateNameMatch_NoVersionSuffixTolerated(t *testing.T) {
	// Tolerate filenames without .v<N> — return empty so the basic
	// validator's "missing fields" check stays the primary signal.
	body := []byte("template: agents.weird\nbackend:\n  cmd: x\n")
	if reason := validateAgentTemplateNameMatch("templates/agents/weird.yaml", body); reason != "" {
		t.Errorf("file with no .v<N> should be tolerated by name-match check; got %q", reason)
	}
}

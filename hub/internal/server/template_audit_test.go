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

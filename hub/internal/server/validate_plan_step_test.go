package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// W10a unit tests for the plan_step spec_json validator. Pre-bundle
// the plans.steps.create handler accepted any spec shape and quietly
// inserted a step the executor couldn't act on — a stalled-plan bug
// from docs/discussions/validate-at-every-boundary.md §3 Layer 1.

func TestValidatePlanStepSpec_AgentSpawnRequiresChildHandle(t *testing.T) {
	if reason := validatePlanStepSpec("agent_spawn", json.RawMessage(`{}`)); reason == "" {
		t.Error("empty spec for agent_spawn should be rejected")
	}
	reason := validatePlanStepSpec("agent_spawn", json.RawMessage(`{"template": "coder.v1"}`))
	if !strings.Contains(reason, "child_handle") {
		t.Errorf("missing child_handle should be named: %q", reason)
	}
}

func TestValidatePlanStepSpec_AgentSpawnAcceptsValid(t *testing.T) {
	good := json.RawMessage(`{"child_handle": "summarizer", "template": "coder.v1"}`)
	if reason := validatePlanStepSpec("agent_spawn", good); reason != "" {
		t.Errorf("valid agent_spawn rejected: %q", reason)
	}
}

func TestValidatePlanStepSpec_LlmCallRequiresPrompt(t *testing.T) {
	if reason := validatePlanStepSpec("llm_call", json.RawMessage(`{}`)); reason == "" {
		t.Error("empty spec for llm_call should be rejected")
	}
	good := json.RawMessage(`{"prompt": "Summarize §3 of the paper."}`)
	if reason := validatePlanStepSpec("llm_call", good); reason != "" {
		t.Errorf("valid llm_call rejected: %q", reason)
	}
}

func TestValidatePlanStepSpec_ShellRequiresCmd(t *testing.T) {
	if reason := validatePlanStepSpec("shell", json.RawMessage(`{}`)); reason == "" {
		t.Error("empty shell spec should be rejected")
	}
	good := json.RawMessage(`{"cmd": "make test"}`)
	if reason := validatePlanStepSpec("shell", good); reason != "" {
		t.Errorf("valid shell rejected: %q", reason)
	}
}

func TestValidatePlanStepSpec_McpCallRequiresTool(t *testing.T) {
	if reason := validatePlanStepSpec("mcp_call", json.RawMessage(`{}`)); reason == "" {
		t.Error("empty mcp_call should be rejected")
	}
	good := json.RawMessage(`{"tool": "agents.spawn", "args": {}}`)
	if reason := validatePlanStepSpec("mcp_call", good); reason != "" {
		t.Errorf("valid mcp_call rejected: %q", reason)
	}
}

func TestValidatePlanStepSpec_HumanDecisionAllowsEmpty(t *testing.T) {
	// human_decision is the one kind where empty spec_json is OK —
	// the step's own copy is the prompt.
	if reason := validatePlanStepSpec("human_decision", json.RawMessage(`{}`)); reason != "" {
		t.Errorf("human_decision should accept empty spec: %q", reason)
	}
	if reason := validatePlanStepSpec("human_decision", nil); reason != "" {
		t.Errorf("human_decision should accept nil spec: %q", reason)
	}
}

func TestValidatePlanStepSpec_RejectsEmptyStringFields(t *testing.T) {
	// A whitespace-only required field is as bad as missing.
	bad := json.RawMessage(`{"prompt": "   "}`)
	if reason := validatePlanStepSpec("llm_call", bad); reason == "" {
		t.Error("whitespace-only prompt should be rejected")
	}
}

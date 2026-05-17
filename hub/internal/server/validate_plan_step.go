package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validatePlanStepSpec checks that spec_json for a plan_steps row
// carries the per-kind fields the executor needs to do useful work.
// Returns "" when valid, a short error string when not. W10a of
// docs/plans/spawn-robustness-and-validators.md.
//
// The validator is intentionally lenient about extra fields (steward
// prompts may add metadata the executor ignores) and strict about
// required fields (the executor stalls without them). Each kind has a
// small required-set:
//
//   - agent_spawn: child_handle (template id), at minimum
//   - llm_call:    prompt (the model needs SOMETHING to respond to)
//   - shell:       cmd
//   - mcp_call:    tool
//   - human_decision: prompt
//
// Empty spec_json is allowed for `human_decision` (the step body
// itself is the prompt) but rejected for the other four kinds.
func validatePlanStepSpec(kind string, spec json.RawMessage) string {
	// Empty / null / "{}" — depends on kind.
	asMap := map[string]any{}
	if len(spec) > 0 {
		_ = json.Unmarshal(spec, &asMap)
	}
	requireFields := func(fields ...string) string {
		var missing []string
		for _, f := range fields {
			v, ok := asMap[f]
			if !ok {
				missing = append(missing, f)
				continue
			}
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
				missing = append(missing, f)
			}
		}
		if len(missing) == 0 {
			return ""
		}
		return fmt.Sprintf(
			"plan step kind=%q requires spec_json field(s): %s. "+
				"See docs/discussions/validate-at-every-boundary.md §3.",
			kind, strings.Join(missing, ", "))
	}
	switch kind {
	case "agent_spawn":
		return requireFields("child_handle")
	case "llm_call":
		return requireFields("prompt")
	case "shell":
		return requireFields("cmd")
	case "mcp_call":
		return requireFields("tool")
	case "human_decision":
		// Optional — the step's own copy serves as the prompt when
		// spec_json is empty. No required-field check.
		return ""
	default:
		// Unknown kind — caller's existing kind allowlist catches
		// this before we get here. Permissive fallback.
		return ""
	}
}

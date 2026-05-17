package server

import "encoding/json"

// validatePolicyOverridesJSON checks that the policy_overrides_json
// field on projects.create / projects.update is a well-formed JSON
// object when present. Empty/absent is OK (project keeps inherited
// team-default policy).
//
// We deliberately don't validate against a specific policy schema —
// the policy engine evolves, MVP keeps its schema in flux, and we'd
// rather forward unknown keys than reject them. The validator's job
// is to keep the column queryable: malformed JSON or a top-level
// array/string/number would break SELECT queries and any downstream
// engine that expects a dict.
//
// Returns "" when valid, a structured error otherwise.
func validatePolicyOverridesJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "policy_overrides_json: invalid JSON"
	}
	if _, ok := v.(map[string]any); !ok {
		return "policy_overrides_json: must be a JSON object " +
			"(arrays / scalars are rejected; use {} for an empty override set)"
	}
	return ""
}

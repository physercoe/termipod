package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidatePolicyOverridesJSON_EmptyOK(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("")} {
		if reason := validatePolicyOverridesJSON(raw); reason != "" {
			t.Errorf("empty raw rejected: %q", reason)
		}
	}
}

func TestValidatePolicyOverridesJSON_AcceptsEmptyObject(t *testing.T) {
	if reason := validatePolicyOverridesJSON(json.RawMessage(`{}`)); reason != "" {
		t.Errorf("empty object rejected: %q", reason)
	}
}

func TestValidatePolicyOverridesJSON_AcceptsRealOverrides(t *testing.T) {
	good := json.RawMessage(`{
		"spawn": {"tier": "critical"},
		"budget_cents_per_day": 5000
	}`)
	if reason := validatePolicyOverridesJSON(good); reason != "" {
		t.Errorf("real overrides rejected: %q", reason)
	}
}

func TestValidatePolicyOverridesJSON_RejectsArray(t *testing.T) {
	reason := validatePolicyOverridesJSON(json.RawMessage(`[{"k":"v"}]`))
	if !strings.Contains(reason, "JSON object") {
		t.Errorf("array should be rejected: %q", reason)
	}
}

func TestValidatePolicyOverridesJSON_RejectsScalar(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`"override-as-string"`),
		json.RawMessage(`42`),
		json.RawMessage(`true`),
	} {
		if reason := validatePolicyOverridesJSON(raw); !strings.Contains(reason, "JSON object") {
			t.Errorf("scalar %s should be rejected: %q", raw, reason)
		}
	}
}

func TestValidatePolicyOverridesJSON_RejectsMalformed(t *testing.T) {
	reason := validatePolicyOverridesJSON(json.RawMessage(`{not valid`))
	if !strings.Contains(reason, "invalid JSON") {
		t.Errorf("malformed should be rejected: %q", reason)
	}
}

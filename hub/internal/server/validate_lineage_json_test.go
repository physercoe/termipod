package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateLineageJSON_EmptyOK(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("")} {
		if reason := validateLineageJSON(raw); reason != "" {
			t.Errorf("empty raw rejected: %q", reason)
		}
	}
}

func TestValidateLineageJSON_AcceptsEmptyObject(t *testing.T) {
	if reason := validateLineageJSON(json.RawMessage(`{}`)); reason != "" {
		t.Errorf("empty object rejected: %q", reason)
	}
}

func TestValidateLineageJSON_AcceptsFullObject(t *testing.T) {
	good := json.RawMessage(`{
		"upstream_run_ids": ["r1","r2"],
		"upstream_artifact_ids": ["a1"],
		"parameters": {"lr": 0.001}
	}`)
	if reason := validateLineageJSON(good); reason != "" {
		t.Errorf("full lineage object rejected: %q", reason)
	}
}

func TestValidateLineageJSON_RejectsArray(t *testing.T) {
	reason := validateLineageJSON(json.RawMessage(`[]`))
	if !strings.Contains(reason, "JSON object") {
		t.Errorf("array should be rejected with shape error: %q", reason)
	}
}

func TestValidateLineageJSON_RejectsScalar(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`"string"`),
		json.RawMessage(`42`),
		json.RawMessage(`true`),
		json.RawMessage(`null`),
	} {
		reason := validateLineageJSON(raw)
		if !strings.Contains(reason, "JSON object") {
			t.Errorf("scalar %s should be rejected: %q", raw, reason)
		}
	}
}

func TestValidateLineageJSON_RejectsMalformedJSON(t *testing.T) {
	reason := validateLineageJSON(json.RawMessage(`{not valid`))
	if !strings.Contains(reason, "invalid JSON") {
		t.Errorf("malformed JSON should be rejected: %q", reason)
	}
}

func TestValidateLineageJSON_TolerantOfUnknownKeys(t *testing.T) {
	// Forward-compat: new keys ship before the schema doc is updated.
	in := json.RawMessage(`{"upstream_run_ids":["r1"],"experimental_field":42}`)
	if reason := validateLineageJSON(in); reason != "" {
		t.Errorf("unknown keys should be tolerated: %q", reason)
	}
}

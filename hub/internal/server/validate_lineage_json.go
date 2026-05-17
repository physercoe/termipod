package server

import (
	"encoding/json"
)

// validateLineageJSON checks the shape of the lineage_json field used
// by artifacts.create + runs.attach_artifact. Empty/absent is always
// OK (lineage is optional). When present:
//
//   - Must parse as valid JSON.
//   - Must be a JSON object (not an array, string, number, or bool).
//   - May carry any subset of known keys: upstream_run_ids,
//     upstream_artifact_ids, parameters. Unknown keys are tolerated so
//     the schema can evolve without coordinated client+server rollout.
//
// Returns "" when valid, a structured error otherwise.
//
// Shared by both artifact-create paths; the MCP runs.attach_artifact
// tool also routes through handleCreateArtifact and inherits this
// gate.
func validateLineageJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "lineage_json: invalid JSON"
	}
	if _, ok := v.(map[string]any); !ok {
		return "lineage_json: must be a JSON object " +
			"(arrays / strings / numbers / booleans are rejected)"
	}
	return ""
}

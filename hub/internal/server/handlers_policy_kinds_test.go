package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// ADR-030 W21 — GET /v1/teams/{t}/policy/kinds returns the parsed
// `kinds:` block from team/policy.yaml as JSON. Consumed by the
// mobile read-only policy viewer so the Flutter binary doesn't need
// a YAML parser.

func TestGetPolicyKinds_MissingFile_ReturnsEmptyMap(t *testing.T) {
	// No policy.yaml written → endpoint returns {"kinds": {}} so the
	// mobile viewer renders the empty-state instead of an error.
	s, token := newA2ATestServer(t)

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/policy/kinds", nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds, ok := out["kinds"].(map[string]any)
	if !ok {
		t.Fatalf("kinds missing or not object: %v", out["kinds"])
	}
	if len(kinds) != 0 {
		t.Errorf("kinds = %v, want empty map", kinds)
	}
}

func TestGetPolicyKinds_FullPolicy_ParsesEveryField(t *testing.T) {
	s, token := newA2ATestServer(t)
	writePolicy(t, s, s.cfg.DataRoot, `
tiers:
  spawn: moderate
quorum:
  moderate: 2
kinds:
  deliverable.set_state:
    default_tier: project-steward
    commits: true
    override_allowed: true
    quorum:
      project-steward:
        m: 1
  phase.advance:
    default_tier: principal
    commits: true
    override_allowed: true
    quorum:
      principal:
        m: 1
  task.set_status:
    default_tier: project-steward
    commits: false
    override_allowed: false
    quorum:
      project-steward:
        m: 1
`)

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/policy/kinds", nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds, ok := out["kinds"].(map[string]any)
	if !ok {
		t.Fatalf("kinds missing or not object")
	}
	if len(kinds) != 3 {
		t.Errorf("kinds count = %d, want 3", len(kinds))
	}

	// Spot-check one kind to verify every field round-trips.
	deliverable, ok := kinds["deliverable.set_state"].(map[string]any)
	if !ok {
		t.Fatalf("deliverable.set_state missing or not object")
	}
	if deliverable["default_tier"] != "project-steward" {
		t.Errorf("deliverable.default_tier = %v, want project-steward",
			deliverable["default_tier"])
	}
	if deliverable["commits"] != true {
		t.Errorf("deliverable.commits = %v, want true", deliverable["commits"])
	}
	if deliverable["override_allowed"] != true {
		t.Errorf("deliverable.override_allowed = %v, want true",
			deliverable["override_allowed"])
	}
	quorum, ok := deliverable["quorum"].(map[string]any)
	if !ok {
		t.Fatalf("deliverable.quorum missing")
	}
	ps, ok := quorum["project-steward"].(map[string]any)
	if !ok {
		t.Fatalf("deliverable.quorum.project-steward missing")
	}
	// JSON numbers decode as float64.
	if m, _ := ps["m"].(float64); m != 1 {
		t.Errorf("deliverable.quorum.project-steward.m = %v, want 1", ps["m"])
	}

	// Negative-case kind: override disabled, no commits.
	taskKind, ok := kinds["task.set_status"].(map[string]any)
	if !ok {
		t.Fatalf("task.set_status missing")
	}
	if taskKind["commits"] != false {
		t.Errorf("task.commits = %v, want false", taskKind["commits"])
	}
	if taskKind["override_allowed"] != false {
		t.Errorf("task.override_allowed = %v, want false",
			taskKind["override_allowed"])
	}
}

func TestGetPolicyKinds_PolicyWithoutKindsBlock_ReturnsEmptyMap(t *testing.T) {
	// Legacy policy file with only the pre-ADR-030 sections (tiers,
	// quorum). The endpoint returns {"kinds": {}} — the new block is
	// optional, not a parse error.
	s, token := newA2ATestServer(t)
	writePolicy(t, s, s.cfg.DataRoot, `
tiers:
  spawn: moderate
quorum:
  moderate: 2
`)

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/policy/kinds", nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	kinds, _ := out["kinds"].(map[string]any)
	if len(kinds) != 0 {
		t.Errorf("kinds = %v, want empty map when block omitted", kinds)
	}
}

func TestGetPolicyKinds_MalformedYaml_Returns500(t *testing.T) {
	// Defensive — a malformed policy.yaml shouldn't crash the viewer
	// silently; surface the parse error via 500 so the operator knows
	// to fix the file. The hub's own runtime policy reload (called
	// from handlePutPolicy) refuses to write malformed files, so this
	// only happens when the file is hand-edited on the host outside
	// the hub's PUT flow.
	s, token := newA2ATestServer(t)
	writePolicy(t, s, s.cfg.DataRoot, "kinds: [this is not valid")

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/policy/kinds", nil)
	if status != 500 {
		t.Errorf("get = %d (body=%s), want 500 on malformed yaml",
			status, string(body))
	}
}

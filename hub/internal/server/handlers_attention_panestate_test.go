package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The pane-state classifier's attention row, exercised against the real
// handlers rather than a host-runner-side stub.
//
// host-runner's own tests fake the hub, so they prove the state machine and
// prove nothing about the contract. The retract leg in particular rests on
// three hub-side facts that live in a different package and could each be
// changed by someone who never opens the pane-state code:
//
//  1. `idle` is not in attentionAwaitsAgentReply, so /resolve accepts it —
//     /decide is for rows that owe a parked agent a reply, and this row owes
//     nobody anything.
//  2. a detector-supplied `pending_payload` survives the round-trip, since it
//     is the only place the rule id and manifest version are carried.
//  3. resolving twice is a 409, not a 500 — the director dismissing the row
//     before host-runner notices the pane moved on is the normal race.
//
// Plan: docs/plans/pane-state-manifests.md P3.
func TestPaneStateAttentionRaisesAndResolves(t *testing.T) {
	s, _ := newA2ATestServer(t)
	// A host token, because that is who raises this row — and because the
	// origin-chip rule keys off the token KIND. A principal token here would
	// have quietly passed a test of the wrong thing.
	token := mintToken(t, s, "host", map[string]any{"team": defaultTeamID, "role": "host"})

	// What paneStateWatch.raise() posts, field for field.
	code, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention", map[string]any{
			"scope_kind":   "team",
			"kind":         "idle",
			"summary":      "agent blocked at a prompt: cx (live_strong_blocker)",
			"severity":     "minor",
			"actor_handle": "cx",
			"pending_payload": map[string]any{
				"detector":         "panestate",
				"state":            "blocked",
				"agent_id":         "ag-1",
				"family":           "codex",
				"pane":             "%7",
				"manifest_id":      "codex",
				"manifest_version": "1",
				"rule_id":          "live_strong_blocker",
			},
		})
	if code != http.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("create response has no id: %s", body)
	}

	// The evidence survives the round-trip: a client that renders the card
	// reads the rule id from here, and P4's explain verb keys off the pane.
	code, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention/"+created.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("get = %d: %s", code, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("get response: %v", err)
	}
	payload, ok := got["pending_payload"].(map[string]any)
	if !ok {
		t.Fatalf("pending_payload missing or not an object: %s", body)
	}
	if payload["rule_id"] != "live_strong_blocker" || payload["detector"] != "panestate" {
		t.Errorf("payload lost its evidence: %+v", payload)
	}
	if got["actor_kind"] != "agent" || got["actor_handle"] != "cx" {
		t.Errorf("origin chip = %v/%v, want agent/cx", got["actor_kind"], got["actor_handle"])
	}

	// The retract leg. /resolve, NOT /decide: nothing is parked on this row.
	code, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+created.ID+"/resolve",
		map[string]any{})
	if code != http.StatusNoContent {
		t.Fatalf("resolve = %d, want 204 — is `idle` in attentionAwaitsAgentReply now? %s",
			code, body)
	}

	code, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention/"+created.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("get after resolve = %d: %s", code, body)
	}
	got = nil
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("get response: %v", err)
	}
	if got["status"] != "resolved" {
		t.Errorf("status = %v, want resolved", got["status"])
	}

	// Losing the race to a director who dismissed it first is a 409 the
	// caller logs and drops, not a failure it retries.
	code, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/attention/"+created.ID+"/resolve",
		map[string]any{})
	if code != http.StatusConflict {
		t.Fatalf("second resolve = %d, want 409: %s", code, body)
	}
	if !strings.Contains(string(body), "already resolved") {
		t.Errorf("409 body should say why: %s", body)
	}
}

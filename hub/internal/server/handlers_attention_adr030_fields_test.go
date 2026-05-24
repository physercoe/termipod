package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// ADR-030 W19.6: handleListAttention + handleGetAttention must expose
// the 5 ADR-030 columns (change_kind, assigned_tier, change_spec_json,
// target_ref_json, executed_json) AND the escalation_state column
// (migration 0042) on attentionOut. The Phase 3 mobile per-kind
// propose cards (W15-W18) consume these in a single fetch.
//
// Tests below cover:
//   1. pre-0045 rows (NULL columns) → fields elided from JSON via
//      omitempty + COALESCE defaults
//   2. propose-shaped rows (all 5 ADR-030 columns populated) →
//      every field round-trips through the JSON wire
//   3. escalated rows (escalation_state != 'none') → field exposed
//   4. include_escalated query param parses without error (MVP no-op;
//      forward-compat hook for the tier-narrow widening per plan W19.6)

func insertProposeShapedAttention(t *testing.T, s *Server) string {
	t.Helper()
	id := NewID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO attention_items (
			id, scope_kind, scope_id, kind, summary, severity,
			current_assignees_json, decisions_json, escalation_history_json,
			status, created_at,
			change_kind, assigned_tier,
			change_spec_json, target_ref_json, executed_json
		) VALUES (?, 'team', ?, 'propose', 'task close-out', 'major',
		          '["@steward.proj-x"]', '[]', '[]',
		          'open', ?,
		          ?, ?, ?, ?, ?)`,
		id, defaultTeamID, now,
		"task.set_status", "project-steward",
		`{"from_status":"in_progress","to_status":"done","summary":"shipped"}`,
		`{"task_id":"task-123"}`,
		``); err != nil {
		t.Fatalf("insert propose-shaped attention: %v", err)
	}
	return id
}

func TestListAttention_ExposesADR030Fields(t *testing.T) {
	s, token := newA2ATestServer(t)
	id := insertProposeShapedAttention(t, s)

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention",
		nil)
	if status != 200 {
		t.Fatalf("list = %d body=%s", status, string(body))
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var row map[string]any
	for _, r := range out {
		if r["id"] == id {
			row = r
			break
		}
	}
	if row == nil {
		t.Fatalf("propose row %s missing from list response", id)
	}
	if row["change_kind"] != "task.set_status" {
		t.Errorf("change_kind = %v, want task.set_status", row["change_kind"])
	}
	if row["assigned_tier"] != "project-steward" {
		t.Errorf("assigned_tier = %v, want project-steward", row["assigned_tier"])
	}
	// change_spec + target_ref ship as raw JSON objects on the wire.
	cs, ok := row["change_spec"].(map[string]any)
	if !ok {
		t.Fatalf("change_spec missing or not object: %v", row["change_spec"])
	}
	if cs["to_status"] != "done" {
		t.Errorf("change_spec.to_status = %v, want done", cs["to_status"])
	}
	tr, ok := row["target_ref"].(map[string]any)
	if !ok {
		t.Fatalf("target_ref missing or not object: %v", row["target_ref"])
	}
	if tr["task_id"] != "task-123" {
		t.Errorf("target_ref.task_id = %v, want task-123", tr["task_id"])
	}
	// executed is empty pre-decide; field elides via omitempty.
	if _, present := row["executed"]; present {
		t.Errorf("executed present on pre-decide row; want elided")
	}
	// escalation_state defaults to 'none' (migration 0042); elides
	// via omitempty only when truly empty — COALESCE returns the string
	// 'none', which serializes as "none" and is present on the wire.
	if row["escalation_state"] != "none" {
		t.Errorf("escalation_state = %v, want 'none' (default from migration 0042)",
			row["escalation_state"])
	}
}

func TestGetAttention_ExposesADR030Fields(t *testing.T) {
	s, token := newA2ATestServer(t)
	id := insertProposeShapedAttention(t, s)

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention/"+id,
		nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if row["change_kind"] != "task.set_status" {
		t.Errorf("change_kind = %v, want task.set_status", row["change_kind"])
	}
	if row["assigned_tier"] != "project-steward" {
		t.Errorf("assigned_tier = %v, want project-steward", row["assigned_tier"])
	}
	if cs, ok := row["change_spec"].(map[string]any); !ok || cs["from_status"] != "in_progress" {
		t.Errorf("change_spec.from_status = %v, want in_progress", cs)
	}
}

func TestListAttention_LegacyRowOmitsADR030Fields(t *testing.T) {
	// Pre-ADR-030 rows have NULL in change_kind/assigned_tier/etc.
	// The handler COALESCEs them to empty strings; omitempty elides
	// them from JSON. escalation_state defaults to 'none' from
	// migration 0042's NOT NULL DEFAULT.
	s, token := newA2ATestServer(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := insertAttention(t, s, "" /*tier*/, now, []string{"@steward"})

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention/"+id,
		nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := row["change_kind"]; present {
		t.Errorf("change_kind present on legacy row; want elided (omitempty)")
	}
	if _, present := row["assigned_tier"]; present {
		t.Errorf("assigned_tier present on legacy row; want elided")
	}
	if _, present := row["change_spec"]; present {
		t.Errorf("change_spec present on legacy row; want elided")
	}
	if _, present := row["target_ref"]; present {
		t.Errorf("target_ref present on legacy row; want elided")
	}
	if _, present := row["executed"]; present {
		t.Errorf("executed present on legacy row; want elided")
	}
	// escalation_state always present (NOT NULL DEFAULT 'none' since 0042).
	if row["escalation_state"] != "none" {
		t.Errorf("escalation_state = %v, want 'none' on legacy row", row["escalation_state"])
	}
}

func TestListAttention_EscalatedRowExposesEscalationState(t *testing.T) {
	s, token := newA2ATestServer(t)
	id := insertProposeShapedAttention(t, s)
	if _, err := s.db.Exec(
		`UPDATE attention_items SET escalation_state = 'escalated_principal' WHERE id = ?`,
		id); err != nil {
		t.Fatalf("update escalation_state: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention/"+id,
		nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if row["escalation_state"] != "escalated_principal" {
		t.Errorf("escalation_state = %v, want escalated_principal", row["escalation_state"])
	}
	// W19.5 mobile-side _isAddressee predicate compares assigned_tier
	// to the viewer's tier; row['assigned_tier'] stays 'project-steward'
	// even after escalation (per ADR-030 D-7 Option 2′ — decision stays).
	if row["assigned_tier"] != "project-steward" {
		t.Errorf("assigned_tier = %v after escalation, want project-steward (decision stays)",
			row["assigned_tier"])
	}
}

func TestListAttention_IncludeEscalatedQueryParam(t *testing.T) {
	// MVP: include_escalated is parsed-but-unused (forward-compat hook
	// for the tier-narrow widening per plan W19.6). The query must
	// succeed regardless of the value; the response shape must not
	// differ between true and false (no tier filter is active today,
	// so widening has nothing to widen against).
	s, token := newA2ATestServer(t)
	insertProposeShapedAttention(t, s)

	for _, v := range []string{"", "true", "false", "bogus"} {
		path := "/v1/teams/" + defaultTeamID + "/attention"
		if v != "" {
			path += "?include_escalated=" + v
		}
		status, body := doReq(t, s, token, http.MethodGet, path, nil)
		if status != 200 {
			t.Errorf("include_escalated=%q → %d body=%s", v, status, string(body))
		}
	}
}

func TestListAttention_ExecutedFieldPopulatedAfterPropose(t *testing.T) {
	// Once the propose-decision handler mirrors executed_json onto the
	// row (W8 dispatcher refactor), the wire field is non-empty.
	s, token := newA2ATestServer(t)
	id := insertProposeShapedAttention(t, s)
	if _, err := s.db.Exec(
		`UPDATE attention_items SET executed_json = ? WHERE id = ?`,
		`{"from_status":"in_progress","to_status":"done","no_op":false}`, id); err != nil {
		t.Fatalf("update executed_json: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/attention/"+id,
		nil)
	if status != 200 {
		t.Fatalf("get = %d body=%s", status, string(body))
	}
	var row map[string]any
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ex, ok := row["executed"].(map[string]any)
	if !ok {
		t.Fatalf("executed missing or not object: %v", row["executed"])
	}
	if ex["to_status"] != "done" || ex["no_op"] != false {
		t.Errorf("executed = %v, want from/to + no_op:false", ex)
	}
}

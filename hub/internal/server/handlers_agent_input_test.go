package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newInputRouter mounts handlePostAgentInput on a throwaway chi router so
// tests exercise routing + URL params without depending on server.go's
// real wiring (which the parent owns until P1.8 lands). Once the parent
// wires POST /input on the main router this helper can be retired.
func newInputRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Post("/v1/teams/{team}/agents/{agent}/input", s.handlePostAgentInput)
	return r
}

func seedAgentForInput(t *testing.T, s *Server) string {
	t.Helper()
	agentID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO agents (id, team_id, handle, kind, created_at)
		VALUES (?, ?, 'worker', 'claude-code', ?)`,
		agentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return agentID
}

func postInput(t *testing.T, h http.Handler, team, agent string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/teams/"+team+"/agents/"+agent+"/input",
		bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	raw, _ := io.ReadAll(rr.Body)
	return rr.Code, raw
}

func TestPostAgentInput_HappyPath_AllKinds(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	cases := []struct {
		name    string
		body    map[string]any
		wantRow string // expected kind column value
	}{
		{"text", map[string]any{"kind": "text", "body": "hi"}, "input.text"},
		{"approval", map[string]any{
			"kind": "approval", "decision": "approve", "request_id": "r1",
		}, "input.approval"},
		{"answer", map[string]any{
			"kind": "answer", "request_id": "tool-1", "body": "Red",
		}, "input.answer"},
		{"cancel", map[string]any{"kind": "cancel"}, "input.cancel"},
		{"attach", map[string]any{
			"kind": "attach", "document_id": "doc-1",
		}, "input.attach"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postInput(t, h, defaultTeamID, agentID, tc.body)
			if status != http.StatusCreated {
				t.Fatalf("status = %d body=%s", status, raw)
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if out["id"] == nil || out["seq"] == nil || out["ts"] == nil {
				t.Errorf("missing id/seq/ts: %v", out)
			}
			// Verify the row exists with producer='user' and the expected
			// kind prefix.
			var producer, kind string
			if err := s.db.QueryRow(
				`SELECT producer, kind FROM agent_events WHERE id = ?`,
				out["id"],
			).Scan(&producer, &kind); err != nil {
				t.Fatalf("select row: %v", err)
			}
			if producer != "user" {
				t.Errorf("producer = %q, want user", producer)
			}
			if kind != tc.wantRow {
				t.Errorf("kind = %q, want %q", kind, tc.wantRow)
			}
		})
	}
}

func TestPostAgentInput_ValidationErrors(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty_body", map[string]any{"kind": "text", "body": ""}},
		{"bad_decision", map[string]any{
			"kind": "approval", "decision": "maybe", "request_id": "r1",
		}},
		{"bad_decision_with_empty_option_id", map[string]any{
			"kind": "approval", "decision": "proceed_once",
			"request_id": "r1", "option_id": "",
		}},
		{"missing_request_id", map[string]any{
			"kind": "approval", "decision": "approve",
		}},
		{"missing_document_id", map[string]any{"kind": "attach"}},
		{"answer_missing_request_id", map[string]any{
			"kind": "answer", "body": "Red",
		}},
		{"answer_missing_body", map[string]any{
			"kind": "answer", "request_id": "tool-1",
		}},
		{"unknown_kind", map[string]any{"kind": "shout", "body": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postInput(t, h, defaultTeamID, agentID, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d want 400, body=%s", status, raw)
			}
		})
	}
}

func TestPostAgentInput_ApprovalOptionID(t *testing.T) {
	// v1.0.82 widened approval vocabulary from approve|deny to
	// approve|allow|deny|cancel and added option_id so the phone can
	// forward the exact ACP-assigned choice. Verify all four decisions
	// accept an option_id and persist it into the payload.
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	cases := []struct {
		name     string
		decision string
		optionID string
	}{
		{"allow_with_option", "allow", "bash-run"},
		{"approve_with_option", "approve", "allow-once"},
		{"deny_with_option", "deny", "reject-once"},
		{"cancel_with_option", "cancel", ""},
		// v1.0.405: ACP M1 agents (gemini-cli) use option-id strings as
		// decision words. Mobile forwards o.id verbatim as both decision
		// AND option_id. With option_id present any decision string
		// must be accepted — the option_id is the source of truth.
		{"acp_proceed_once", "proceed_once", "proceed_once"},
		{"acp_proceed_always_server", "proceed_always_server", "proceed_always_server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"kind":       "approval",
				"decision":   tc.decision,
				"request_id": "req-" + tc.name,
			}
			if tc.optionID != "" {
				body["option_id"] = tc.optionID
			}
			status, raw := postInput(t, h, defaultTeamID, agentID, body)
			if status != http.StatusCreated {
				t.Fatalf("status=%d body=%s", status, raw)
			}
			var out map[string]any
			_ = json.Unmarshal(raw, &out)

			var payloadRaw string
			if err := s.db.QueryRow(
				`SELECT payload_json FROM agent_events WHERE id = ?`,
				out["id"],
			).Scan(&payloadRaw); err != nil {
				t.Fatalf("select payload: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
				t.Fatalf("payload decode: %v", err)
			}
			if payload["decision"] != tc.decision {
				t.Errorf("decision = %v, want %q", payload["decision"], tc.decision)
			}
			if payload["request_id"] != "req-"+tc.name {
				t.Errorf("request_id = %v", payload["request_id"])
			}
			if tc.optionID != "" {
				if payload["option_id"] != tc.optionID {
					t.Errorf("option_id = %v, want %q",
						payload["option_id"], tc.optionID)
				}
			} else if _, present := payload["option_id"]; present {
				t.Errorf("option_id should be absent when not provided, got %v",
					payload["option_id"])
			}
		})
	}
}

func TestPostAgentInput_UnknownKindMessage(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "shout"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	// Contract: error body carries the normative kinds list so clients
	// can surface a helpful message without hardcoding.
	if !bytes.Contains(raw, []byte("text|approval|answer|attention_reply|cancel|attach|set_mode|set_model")) {
		t.Errorf("error body missing kinds list: %s", raw)
	}
}

func TestPostAgentInput_AgentNotFound(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)

	status, raw := postInput(t, h, defaultTeamID, "ghost-agent",
		map[string]any{"kind": "text", "body": "hi"})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d want 404, body=%s", status, raw)
	}
}

// TestPostAgentInput_ProducerAttribution covers the A2A wedge: when a
// caller sets producer="a2a" on the wire body, the persisted event row
// must carry that value instead of the default "user". Unknown values
// are rejected so the column vocabulary stays small.
func TestPostAgentInput_ProducerAttribution(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	cases := []struct {
		name     string
		body     map[string]any
		want     string
		wantCode int
	}{
		{"default_stays_user", map[string]any{
			"kind": "text", "body": "hi",
		}, "user", http.StatusCreated},
		{"explicit_user", map[string]any{
			"kind": "text", "body": "hi", "producer": "user",
		}, "user", http.StatusCreated},
		{"a2a_peer", map[string]any{
			"kind": "text", "body": "hi", "producer": "a2a",
		}, "a2a", http.StatusCreated},
		{"unknown_rejected", map[string]any{
			"kind": "text", "body": "hi", "producer": "bot",
		}, "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postInput(t, h, defaultTeamID, agentID, tc.body)
			if status != tc.wantCode {
				t.Fatalf("status = %d want %d, body=%s", status, tc.wantCode, raw)
			}
			if tc.wantCode != http.StatusCreated {
				return
			}
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			var producer string
			if err := s.db.QueryRow(
				`SELECT producer FROM agent_events WHERE id = ?`,
				out["id"],
			).Scan(&producer); err != nil {
				t.Fatalf("select row: %v", err)
			}
			if producer != tc.want {
				t.Errorf("producer = %q, want %q", producer, tc.want)
			}
		})
	}
}

// seedAgentWithKindMode is a W2.1 helper: seedAgentForInput hardcodes
// kind=claude-code with no driving_mode. Routing tests need explicit
// (kind, driving_mode) pairs so they can target each cell of the
// runtime_mode_switch table. Handle is derived from the agent id so
// repeated calls in one server instance don't collide on
// UNIQUE(team_id, handle).
func seedAgentWithKindMode(t *testing.T, s *Server, kind, drivingMode string) string {
	t.Helper()
	agentID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO agents (id, team_id, handle, kind, driving_mode, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		agentID, defaultTeamID, "w-"+agentID[len(agentID)-8:], kind, drivingMode, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return agentID
}

// TestPostAgentInput_SetModeRouting_RPC — gemini-cli M1 → rpc → emit
// input.set_mode event same as text path. Driver-side dispatch lands
// in W2.2; W2.1 verifies the routing token reaches "rpc" and the
// audit row is written.
func TestPostAgentInput_SetModeRouting_RPC(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentWithKindMode(t, s, "gemini-cli", "M1")

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "set_mode", "mode_id": "yolo"})
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)

	var kind, payloadRaw string
	if err := s.db.QueryRow(
		`SELECT kind, payload_json FROM agent_events WHERE id = ?`,
		out["id"]).Scan(&kind, &payloadRaw); err != nil {
		t.Fatalf("select row: %v", err)
	}
	if kind != "input.set_mode" {
		t.Errorf("kind = %q, want input.set_mode", kind)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(payloadRaw), &payload)
	if payload["mode_id"] != "yolo" {
		t.Errorf("mode_id = %v, want yolo", payload["mode_id"])
	}
}

// TestPostAgentInput_SetModelRouting_PerTurnArgv — gemini-cli M2 →
// per_turn_argv → emit input.set_model event for the driver to stash.
// Driver-side argv splice lands in W2.4.
func TestPostAgentInput_SetModelRouting_PerTurnArgv(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentWithKindMode(t, s, "gemini-cli", "M2")

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "set_model", "model_id": "gemini-2.5-flash"})
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)

	var kind, payloadRaw string
	if err := s.db.QueryRow(
		`SELECT kind, payload_json FROM agent_events WHERE id = ?`,
		out["id"]).Scan(&kind, &payloadRaw); err != nil {
		t.Fatalf("select row: %v", err)
	}
	if kind != "input.set_model" {
		t.Errorf("kind = %q, want input.set_model", kind)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(payloadRaw), &payload)
	if payload["model_id"] != "gemini-2.5-flash" {
		t.Errorf("model_id = %v, want gemini-2.5-flash", payload["model_id"])
	}
}

// TestPostAgentInput_SetModelRouting_Respawn — claude-code M2 →
// respawn → handler calls respawnWithSpecMutation. W2.3 implements;
// W2.1 stub returns errRespawnSpecMutationNotImplemented → 501.
func TestPostAgentInput_SetModelRouting_Respawn(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentWithKindMode(t, s, "claude-code", "M2")

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "set_model", "model_id": "claude-3-7-opus"})
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d want 501, body=%s", status, raw)
	}
	if !bytes.Contains(raw, []byte("not yet implemented")) {
		t.Errorf("body missing wedge marker: %s", raw)
	}
	// Stub path must NOT write an input event row — the audit emit
	// belongs to W2.3's helper after the respawn lifecycle lands.
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(1) FROM agent_events WHERE agent_id = ? AND kind LIKE 'input.set_%'`,
		agentID).Scan(&n)
	if n != 0 {
		t.Errorf("respawn stub emitted %d input rows; want 0", n)
	}
}

// TestPostAgentInput_SetModeRouting_Unsupported — driving_mode missing
// from the family's runtime_mode_switch map → 422 with a typed error.
// Covers two paths: an unknown family and a known family on a mode it
// hasn't declared (M4 for any of our entries).
func TestPostAgentInput_SetModeRouting_Unsupported(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	cases := []struct {
		name        string
		kind        string
		drivingMode string
	}{
		{"unknown_family", "no-such-family", "M1"},
		{"undeclared_mode", "claude-code", "M4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentID := seedAgentWithKindMode(t, s, tc.kind, tc.drivingMode)
			status, raw := postInput(t, h, defaultTeamID, agentID,
				map[string]any{"kind": "set_mode", "mode_id": "yolo"})
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d want 422, body=%s", status, raw)
			}
			if !bytes.Contains(raw, []byte("does not support")) {
				t.Errorf("body missing typed marker: %s", raw)
			}
		})
	}
}

// TestPostAgentInput_SetMode_MissingFields — mode_id required for
// set_mode, model_id required for set_model. Validation fires before
// routing so an unknown family combined with a missing field still
// yields 400 (the field-validation error is the more actionable one).
func TestPostAgentInput_SetMode_MissingFields(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentWithKindMode(t, s, "gemini-cli", "M1")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"set_mode_no_id", map[string]any{"kind": "set_mode"}},
		{"set_model_no_id", map[string]any{"kind": "set_model"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postInput(t, h, defaultTeamID, agentID, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d want 400, body=%s", status, raw)
			}
		})
	}
}

func TestPostAgentInput_MonotonicSeq(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	var lastSeq int64
	for i := 0; i < 3; i++ {
		status, raw := postInput(t, h, defaultTeamID, agentID,
			map[string]any{"kind": "text", "body": "n"})
		if status != http.StatusCreated {
			t.Fatalf("post %d: status=%d body=%s", i, status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		seqF, _ := out["seq"].(float64)
		seq := int64(seqF)
		if seq != lastSeq+1 {
			t.Errorf("seq = %d, want %d", seq, lastSeq+1)
		}
		lastSeq = seq
	}
}

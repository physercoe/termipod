package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
			// ADR-032: the A2A post-back carries the envelope provenance
			// the relay stamped — here a steward dispatching a directive.
			"kind": "text", "body": "hi", "producer": "a2a",
			"from_role": "peer_steward", "from_handle": "research-steward",
			"a2a_kind": "directive",
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

// TestPostAgentInput_SetModelRouting_Respawn_NoSession — claude-code
// M2 + respawn route runs the helper. With no live session attached,
// helper returns "no live session" → 500. Full happy-path is covered
// in respawn_with_spec_mutation_test.go where DoSpawn integration
// can be exercised.
func TestPostAgentInput_SetModelRouting_Respawn_NoSession(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentWithKindMode(t, s, "claude-code", "M2")

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "set_model", "model_id": "claude-3-7-opus"})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d want 500, body=%s", status, raw)
	}
	if !bytes.Contains(raw, []byte("no live session")) {
		t.Errorf("body missing 'no live session' marker: %s", raw)
	}
	// Even on respawn errors we must NOT have emitted an input event
	// row — the helper rolls back via DoSpawn's tx; for the
	// no-session path no DB work landed at all.
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(1) FROM agent_events WHERE agent_id = ? AND kind LIKE 'input.set_%'`,
		agentID).Scan(&n)
	if n != 0 {
		t.Errorf("respawn path emitted %d input rows; want 0", n)
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

// b64png returns a base64-encoded blob of n bytes prefixed with the
// 8-byte PNG magic so the tests use realistic-looking but fake image
// data. Validation is structural (length, mime, base64 well-formedness)
// — we don't sniff bytes — so the prefix is cosmetic.
func b64bytes(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// TestPostAgentInput_ImagesHappyPath — W4.1: text input with valid
// images plumbs through to payload_json["images"] verbatim. Drivers
// that map to engine-native content arrays read this in W4.2-W4.5.
func TestPostAgentInput_ImagesHappyPath(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	body := map[string]any{
		"kind": "text",
		"body": "describe these",
		"images": []map[string]string{
			{"mime_type": "image/png", "data": b64bytes(1024)},
			{"mime_type": "image/jpeg", "data": b64bytes(2048)},
		},
	}
	status, raw := postInput(t, h, defaultTeamID, agentID, body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)

	var payloadRaw string
	if err := s.db.QueryRow(
		`SELECT payload_json FROM agent_events WHERE id = ?`,
		out["id"]).Scan(&payloadRaw); err != nil {
		t.Fatalf("select payload: %v", err)
	}
	var payload struct {
		Text   string `json:"text"` // ADR-032: envelope text, was payload.body
		Images []struct {
			MimeType string `json:"mime_type"`
			Data     string `json:"data"`
		} `json:"images"`
	}
	if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.Text != "describe these" {
		t.Errorf("text = %q", payload.Text)
	}
	if len(payload.Images) != 2 {
		t.Fatalf("images = %d, want 2", len(payload.Images))
	}
	if payload.Images[0].MimeType != "image/png" {
		t.Errorf("images[0].mime_type = %q", payload.Images[0].MimeType)
	}
	if payload.Images[1].MimeType != "image/jpeg" {
		t.Errorf("images[1].mime_type = %q", payload.Images[1].MimeType)
	}
}

// TestPostAgentInput_ImagesOnly — body may be empty when at least one
// image is present (e.g. "here, look" gestures). Pre-W4.1 contract
// required body; relaxed for multimodal turns.
func TestPostAgentInput_ImagesOnly(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	body := map[string]any{
		"kind": "text",
		"images": []map[string]string{
			{"mime_type": "image/webp", "data": b64bytes(64)},
		},
	}
	status, raw := postInput(t, h, defaultTeamID, agentID, body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
}

// TestPostAgentInput_ImagesValidation — W4.1: the four rejection paths
// from the plan. Each must return 400 with a typed error fragment so
// mobile renders an actionable snackbar without parsing the message.
func TestPostAgentInput_ImagesValidation(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	cases := []struct {
		name   string
		images []map[string]string
		marker string // substring expected in error body
	}{
		{
			name: "bad_mime",
			images: []map[string]string{
				{"mime_type": "image/bmp", "data": b64bytes(64)},
			},
			marker: "not allowed",
		},
		{
			name: "missing_data",
			images: []map[string]string{
				{"mime_type": "image/png", "data": ""},
			},
			marker: "data required",
		},
		{
			name: "malformed_base64",
			images: []map[string]string{
				{"mime_type": "image/png", "data": "!!!not-base64!!!"},
			},
			marker: "malformed base64",
		},
		{
			name: "too_large",
			images: []map[string]string{
				{"mime_type": "image/png", "data": b64bytes(maxImageSizeBytes + 1)},
			},
			marker: "exceeds",
		},
		{
			name: "too_many",
			images: []map[string]string{
				{"mime_type": "image/png", "data": b64bytes(64)},
				{"mime_type": "image/png", "data": b64bytes(64)},
				{"mime_type": "image/png", "data": b64bytes(64)},
				{"mime_type": "image/png", "data": b64bytes(64)},
			},
			marker: "at most 3 images",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"kind":   "text",
				"body":   "x",
				"images": tc.images,
			}
			status, raw := postInput(t, h, defaultTeamID, agentID, body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d want 400, body=%s", status, raw)
			}
			if !strings.Contains(string(raw), tc.marker) {
				t.Errorf("body missing %q: %s", tc.marker, raw)
			}
		})
	}
}

// TestPostAgentInput_PdfHappyPath — W7.2: PDF attachments plumb
// through to payload_json["pdfs"] verbatim, including the optional
// filename. Drivers that map to engine-native document blocks read
// this in driver_stdio.go (Claude), driver_appserver.go (Codex),
// driver_acp.go (Gemini).
func TestPostAgentInput_PdfHappyPath(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	body := map[string]any{
		"kind": "text",
		"body": "summarise this paper",
		"pdfs": []map[string]string{
			{"mime_type": "application/pdf", "data": b64bytes(2048), "filename": "paper.pdf"},
		},
	}
	status, raw := postInput(t, h, defaultTeamID, agentID, body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	var payloadRaw string
	if err := s.db.QueryRow(
		`SELECT payload_json FROM agent_events WHERE id = ?`,
		out["id"]).Scan(&payloadRaw); err != nil {
		t.Fatalf("select payload: %v", err)
	}
	if !strings.Contains(payloadRaw, `"pdfs"`) {
		t.Errorf("payload missing pdfs: %s", payloadRaw)
	}
	if !strings.Contains(payloadRaw, `"filename":"paper.pdf"`) {
		t.Errorf("payload missing pdf filename: %s", payloadRaw)
	}
}

// TestPostAgentInput_AudioVideoHappyPath — W7.2: audio + video plumb
// through (Gemini-only at the driver level; the hub accepts them
// regardless of the agent's family — the driver does the gating).
func TestPostAgentInput_AudioVideoHappyPath(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	body := map[string]any{
		"kind":   "text",
		"body":   "describe both",
		"audios": []map[string]string{{"mime_type": "audio/mpeg", "data": b64bytes(64), "filename": "memo.mp3"}},
		"videos": []map[string]string{{"mime_type": "video/mp4", "data": b64bytes(128), "filename": "clip.mp4"}},
	}
	status, raw := postInput(t, h, defaultTeamID, agentID, body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
}

// TestPostAgentInput_MultimodalValidation — W7.2: per-modality MIME
// allowlist + size caps. Each subtest pokes one validator hole.
func TestPostAgentInput_MultimodalValidation(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	cases := []struct {
		name    string
		body    map[string]any
		wantMsg string
	}{
		{
			name: "pdf unknown mime",
			body: map[string]any{
				"kind": "text",
				"body": "x",
				"pdfs": []map[string]string{{"mime_type": "application/zip", "data": "QQ=="}},
			},
			wantMsg: "mime_type",
		},
		{
			name: "audio empty data",
			body: map[string]any{
				"kind":   "text",
				"body":   "x",
				"audios": []map[string]string{{"mime_type": "audio/mpeg", "data": ""}},
			},
			wantMsg: "data required",
		},
		{
			name: "video unknown mime",
			body: map[string]any{
				"kind":   "text",
				"body":   "x",
				"videos": []map[string]string{{"mime_type": "video/avi", "data": "QQ=="}},
			},
			wantMsg: "mime_type",
		},
		{
			name: "too many pdfs",
			body: map[string]any{
				"kind": "text",
				"body": "x",
				"pdfs": []map[string]string{
					{"mime_type": "application/pdf", "data": "QQ=="},
					{"mime_type": "application/pdf", "data": "QQ=="},
				},
			},
			wantMsg: "at most",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := postInput(t, h, defaultTeamID, agentID, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d want 400, body=%s", status, raw)
			}
			if !strings.Contains(string(raw), tc.wantMsg) {
				t.Errorf("body missing %q: %s", tc.wantMsg, raw)
			}
		})
	}
}

// TestPostAgentInput_ImagesIgnoredOnNonText — images on a non-text kind
// is forward-compat noise: validation only fires on text. Sending a
// malformed image alongside cancel kind still succeeds. (Drivers that
// see images on cancel will still ignore them.)
func TestPostAgentInput_ImagesIgnoredOnNonText(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	// Even with malformed base64 and bad mime, cancel goes through:
	// validation only runs on text. This locks the forward-compat
	// principle so future kinds can opt into images without changing
	// the validation path.
	body := map[string]any{
		"kind":   "cancel",
		"images": []map[string]string{{"mime_type": "image/bmp", "data": "!!!"}},
	}
	status, raw := postInput(t, h, defaultTeamID, agentID, body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
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

// TestPostAgentInput_RenderedTextStamped — v1.0.708 polish
// (ADR-032 D-10). When the input is a regular text turn (not raw),
// the hub stamps payload.rendered_text against the operator-editable
// envelope templates so the host-runner can pass it through verbatim
// without needing access to the hub's filesystem.
//
// Verifies:
//   - rendered_text is present alongside the structured envelope keys
//   - the rendered string contains the principal-directive header
//     and the body verbatim
//   - raw inputs (slash commands) do NOT carry rendered_text (no
//     envelope was rendered)
//   - the embedded default produces the same prose contract today's
//     hardcoded host-runner path emits (sender/kind/reply line all
//     reach the engine)
func TestPostAgentInput_RenderedTextStamped(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	t.Run("regular_text_stamps_rendered_text", func(t *testing.T) {
		body := map[string]any{
			"kind": "text",
			"body": "survey the citation graph",
		}
		status, raw := postInput(t, h, defaultTeamID, agentID, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d body=%s", status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		var payloadJSON string
		_ = s.db.QueryRow(
			`SELECT payload_json FROM agent_events WHERE id = ?`,
			out["id"],
		).Scan(&payloadJSON)
		var payload map[string]any
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		rendered, _ := payload["rendered_text"].(string)
		if rendered == "" {
			t.Fatalf("rendered_text missing or empty: %v", payload)
		}
		// Embedded default uses today's hardcoded prose; the rendered
		// string must contain the directive header + body + chat reply
		// contract. If any of these disappears, the hub-side render +
		// host-runner pass-through path is broken end-to-end.
		for _, want := range []string{
			"[directive from the principal]",
			"survey the citation graph",
			"Reply in this chat",
		} {
			if !strings.Contains(rendered, want) {
				t.Errorf("rendered_text missing %q: %q", want, rendered)
			}
		}
	})

	t.Run("raw_slash_command_omits_rendered_text", func(t *testing.T) {
		// Raw inputs bypass envelope composition entirely (v1.0.707);
		// there's nothing to render. Confirm rendered_text is absent
		// on those rows so the host-runner falls through to the
		// verbatim body via the no-envelope branch.
		body := map[string]any{
			"kind": "text",
			"body": "/clear",
			"raw":  true,
		}
		status, raw := postInput(t, h, defaultTeamID, agentID, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d body=%s", status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		var payloadJSON string
		_ = s.db.QueryRow(
			`SELECT payload_json FROM agent_events WHERE id = ?`,
			out["id"],
		).Scan(&payloadJSON)
		var payload map[string]any
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		if _, ok := payload["rendered_text"]; ok {
			t.Errorf("rendered_text leaked into a raw input: %v",
				payload["rendered_text"])
		}
	})
}

// TestPostAgentInput_RenderedTextHonoursOperatorOverride — v1.0.708
// end-to-end smoke. Edit the envelope template on disk (the same path
// mobile's TemplateEditorScreen writes to), post a text input, and
// confirm the rendered_text reflects the override.
//
// This is the load-bearing assurance the user asked for: "can I
// iterate the prompt without rebuilding?" The mtime hot-reload pulls
// the new template in on the next Resolve, which runs per-render. No
// hub restart involved.
func TestPostAgentInput_RenderedTextHonoursOperatorOverride(t *testing.T) {
	s, dir := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	// Write a distinct override at the canonical operator path.
	overrideDir := filepath.Join(dir, "team", "templates", "envelope")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	override := filepath.Join(overrideDir, "active.yaml")
	customYAML := `schema_version: 1
snapshot_date: 2026-05-25
frame: |
  >>> CUSTOM-FRAME {{.Kind}} {{.Sender}} <<<
  {{.Text}}
  {{.ReplyInstruction}}
roles:
  principal: "THE-OPERATOR"
  system: "system"
  peer_steward: "@{{.Handle}} (steward)"
  peer_worker: "@{{.Handle}} (worker)"
reply_instruction:
  a2a: "a2a"
  attention_reply: "att"
  none: "n"
  chat: "chat-reply"
fallbacks:
  empty_kind: "msg"
  empty_handle: "anon"
`
	if err := os.WriteFile(override, []byte(customYAML), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	body := map[string]any{"kind": "text", "body": "smoke"}
	status, raw := postInput(t, h, defaultTeamID, agentID, body)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	var payloadJSON string
	_ = s.db.QueryRow(
		`SELECT payload_json FROM agent_events WHERE id = ?`,
		out["id"],
	).Scan(&payloadJSON)
	var payload map[string]any
	_ = json.Unmarshal([]byte(payloadJSON), &payload)
	rendered, _ := payload["rendered_text"].(string)
	if !strings.Contains(rendered, ">>> CUSTOM-FRAME") {
		t.Errorf("operator override frame not used: %q", rendered)
	}
	if !strings.Contains(rendered, "THE-OPERATOR") {
		t.Errorf("operator override role label not used: %q", rendered)
	}
	if !strings.Contains(rendered, "smoke") {
		t.Errorf("body lost: %q", rendered)
	}
}

// TestPostAgentInput_RawSlashCommand_BypassesEnvelope — v1.0.707
// polish. The "raw" wire flag on input.text events tells the hub to
// skip the principal-directive envelope wrap so engine-control slash
// commands (/clear, /compact, /model …) reach the engine verbatim
// instead of being framed as a prose directive the engine ignores.
//
// Verifies:
//  - raw=true stores payload.text = body with NO envelope keys
//    (from / to / kind / thread are all absent).
//  - payload.raw=true is preserved as a marker so the mobile feed
//    can suppress the misleading "from: principal" header row on
//    these turns.
//  - raw=false (default) preserves the existing envelope-composing
//    behaviour — guards against a regression where the flag becomes
//    sticky or its default flips.
//  - A2A producers ignore raw — peer messages always carry their
//    envelope per ADR-032 D-1.
func TestPostAgentInput_RawSlashCommand_BypassesEnvelope(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	t.Run("raw_true_strips_envelope", func(t *testing.T) {
		body := map[string]any{
			"kind": "text",
			"body": "/clear",
			"raw":  true,
		}
		status, raw := postInput(t, h, defaultTeamID, agentID, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d body=%s", status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		var payloadJSON string
		if err := s.db.QueryRow(
			`SELECT payload_json FROM agent_events WHERE id = ?`,
			out["id"],
		).Scan(&payloadJSON); err != nil {
			t.Fatalf("select payload: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["text"] != "/clear" {
			t.Errorf("payload.text = %v, want /clear", payload["text"])
		}
		if payload["raw"] != true {
			t.Errorf("payload.raw = %v, want true (marker for mobile)",
				payload["raw"])
		}
		// Envelope keys MUST be absent — that's the whole point.
		for _, k := range []string{"from", "to", "kind", "thread"} {
			if _, ok := payload[k]; ok {
				t.Errorf("payload.%s present on a raw input — envelope leaked", k)
			}
		}
	})

	t.Run("raw_false_preserves_envelope", func(t *testing.T) {
		body := map[string]any{
			"kind": "text",
			"body": "hello",
			"raw":  false,
		}
		status, raw := postInput(t, h, defaultTeamID, agentID, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d body=%s", status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		var payloadJSON string
		_ = s.db.QueryRow(
			`SELECT payload_json FROM agent_events WHERE id = ?`,
			out["id"],
		).Scan(&payloadJSON)
		var payload map[string]any
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		// Envelope keys MUST be present for the default principal-
		// directive path — that's the v1.0.706-and-earlier contract.
		if _, ok := payload["from"]; !ok {
			t.Errorf("payload.from absent on a non-raw input — envelope dropped")
		}
		if payload["kind"] != "directive" {
			t.Errorf("payload.kind = %v, want directive", payload["kind"])
		}
		if payload["text"] != "hello" {
			t.Errorf("payload.text = %v, want hello", payload["text"])
		}
		if payload["raw"] == true {
			t.Errorf("payload.raw=true leaked into a non-raw input")
		}
	})

	t.Run("raw_unset_default_path", func(t *testing.T) {
		// Omitting `raw` entirely must be equivalent to raw:false —
		// guard against a server-side default flip.
		body := map[string]any{
			"kind": "text",
			"body": "hello again",
		}
		status, raw := postInput(t, h, defaultTeamID, agentID, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d body=%s", status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		var payloadJSON string
		_ = s.db.QueryRow(
			`SELECT payload_json FROM agent_events WHERE id = ?`,
			out["id"],
		).Scan(&payloadJSON)
		var payload map[string]any
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		if _, ok := payload["from"]; !ok {
			t.Errorf("envelope dropped when raw omitted — default flipped")
		}
	})

	t.Run("a2a_producer_ignores_raw", func(t *testing.T) {
		// Even with raw:true, an A2A-producer input must keep its
		// envelope — peer-originated messages always carry from/to
		// per ADR-032 D-1, and the raw escape hatch is a principal-
		// only signal.
		body := map[string]any{
			"kind":        "text",
			"body":        "/clear",
			"raw":         true,
			"producer":    "a2a",
			"from_role":   "peer_steward",
			"from_handle": "research-steward",
			"a2a_kind":    "directive",
		}
		status, raw := postInput(t, h, defaultTeamID, agentID, body)
		if status != http.StatusCreated {
			t.Fatalf("status = %d body=%s", status, raw)
		}
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		var payloadJSON string
		_ = s.db.QueryRow(
			`SELECT payload_json FROM agent_events WHERE id = ?`,
			out["id"],
		).Scan(&payloadJSON)
		var payload map[string]any
		_ = json.Unmarshal([]byte(payloadJSON), &payload)
		if _, ok := payload["from"]; !ok {
			t.Errorf("envelope dropped on A2A producer — raw bypassed contract")
		}
		if payload["raw"] == true {
			t.Errorf("payload.raw=true leaked into A2A input")
		}
	})
}

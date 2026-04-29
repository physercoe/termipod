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
	if !bytes.Contains(raw, []byte("text|approval|answer|attention_reply|cancel|attach")) {
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

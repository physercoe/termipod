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
	if !bytes.Contains(raw, []byte("text|approval|cancel|attach")) {
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

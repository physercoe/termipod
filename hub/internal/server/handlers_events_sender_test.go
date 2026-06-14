package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// F-08 — a posted channel event's sender and cost attribution are
// derived from the authenticated token, not the request body. Agent
// bearers are refused at the middleware (F-01), so the live caller
// shapes that reach the handler are the host relay (X-Agent-Id) and
// humans; these tests pin both.

func seedEventChannel(t *testing.T, s *Server, id string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO channels (id, team_id, scope_kind, name, created_at) VALUES (?, 'default', 'team', 'general', ?)`,
		id, NowUTC()); err != nil {
		t.Fatalf("seed channel %q: %v", id, err)
	}
}

func spentCents(t *testing.T, s *Server, agentID string) int {
	t.Helper()
	var c int
	if err := s.db.QueryRow(`SELECT spent_cents FROM agents WHERE id = ?`, agentID).Scan(&c); err != nil {
		t.Fatalf("read spent_cents for %q: %v", agentID, err)
	}
	return c
}

func lastEventFrom(t *testing.T, s *Server, channelID string) string {
	t.Helper()
	var from string
	if err := s.db.QueryRow(
		`SELECT COALESCE(from_id, '') FROM events WHERE channel_id = ? ORDER BY received_ts DESC LIMIT 1`,
		channelID).Scan(&from); err != nil {
		t.Fatalf("read event for %q: %v", channelID, err)
	}
	return from
}

// An agent token presented as a REST bearer is refused at the auth
// middleware (F-01) — it cannot even reach the events handler, so a
// forged from_id / usage_tokens block never lands and no victim is
// charged. (eventSender's agent branch remains as defense-in-depth for
// the handler layer; the live agent path is the host relay below.)
func TestPostEvent_AgentBearerRefusedAtMiddleware(t *testing.T) {
	s, _ := newA2ATestServer(t)
	seedEventChannel(t, s, "chan-f08")

	attacker := seedAgentWithKind(t, s, defaultTeamID, "atk", "claude-code", "")
	victim := seedAgentWithKind(t, s, defaultTeamID, "vic", "claude-code", "")

	atkTok := mintToken(t, s, "agent", map[string]any{
		"team": defaultTeamID, "role": "worker",
		"agent_id": attacker, "handle": "atk",
	})

	status, body := doReq(t, s, atkTok, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/channels/chan-f08/events",
		map[string]any{
			"type":         "message",
			"from_id":      victim, // forged
			"parts":        []map[string]any{{"kind": "text", "text": "hi"}},
			"usage_tokens": map[string]any{"cost_cents": 500},
		})
	if status != http.StatusForbidden {
		t.Fatalf("agent bearer post = %d body=%s; want 403", status, string(body))
	}

	// No event was written and no one was charged.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE channel_id = 'chan-f08'`).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Errorf("events written = %d; want 0 (request refused at middleware)", n)
	}
	if got := spentCents(t, s, victim); got != 0 {
		t.Errorf("victim spent_cents = %d; want 0", got)
	}
}

// A host token is the deputy relaying for its agents: it names the
// agent via the X-Agent-Id header host-runner stamps, and that identity
// is trusted for both attribution and spend.
func TestPostEvent_HostRelayUsesStampedAgentID(t *testing.T) {
	s, _ := newA2ATestServer(t)
	seedEventChannel(t, s, "chan-f08h")
	worker := seedAgentWithKind(t, s, defaultTeamID, "w", "claude-code", "")
	hostTok := mintToken(t, s, "host", map[string]any{"team": defaultTeamID, "role": "host"})

	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{
		"type":         "status",
		"parts":        []map[string]any{{"kind": "text", "text": "x"}},
		"usage_tokens": map[string]any{"cost_cents": 300},
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/channels/chan-f08h/events", &buf)
	req.Header.Set("Authorization", "Bearer "+hostTok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Id", worker)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("host relay post = %d body=%s", rr.Code, rr.Body.String())
	}

	if got := lastEventFrom(t, s, "chan-f08h"); got != worker {
		t.Errorf("from_id = %q; want stamped worker %q", got, worker)
	}
	if got := spentCents(t, s, worker); got != 300 {
		t.Errorf("worker spent_cents = %d; want 300 (host-relayed cost)", got)
	}
}

func TestListEvents_BeforeCursor(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedEventChannel(t, s, "chan-before")

	// Post several events to the channel; they get sequential received_ts.
	hostTok := mintToken(t, s, "host", map[string]any{"team": defaultTeamID, "role": "host"})
	worker := seedAgentWithKind(t, s, defaultTeamID, "w1", "claude-code", "")
	var recvTS []string
	for i := 0; i < 5; i++ {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{
			"type":  "message",
			"parts": []map[string]any{{"kind": "text", "text": "msg " + itos(i)}},
		})
		req := httptest.NewRequest(http.MethodPost,
			"/v1/teams/"+defaultTeamID+"/channels/chan-before/events", &buf)
		req.Header.Set("Authorization", "Bearer "+hostTok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Id", worker)
		rr := httptest.NewRecorder()
		s.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("post event %d: %d body=%s", i, rr.Code, rr.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
		// Read back the received_ts.
		var evt struct {
			ReceivedTS string `json:"received_ts"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &evt); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		recvTS = append(recvTS, evt.ReceivedTS)
	}

	// No params — returns events DESC (unchanged).
	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/channels/chan-before/events", nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d body=%s", status, body)
	}
	var all []map[string]any
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("no params got %d events, want 5", len(all))
	}

	// ?before=<newest+1> — all 5 should be returned DESC.
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/channels/chan-before/events?limit=10&before="+recvTS[4], nil)
	if status != http.StatusOK {
		t.Fatalf("before newest+1: %d body=%s", status, body)
	}
	var beforeAll []map[string]any
	if err := json.Unmarshal(body, &beforeAll); err != nil {
		t.Fatalf("decode before all: %v", err)
	}
	// All events except the cursor itself (strict <) should be returned.
	if len(beforeAll) < 4 || len(beforeAll) > 5 {
		t.Fatalf("before newest+1 got %d events, want 4 or 5", len(beforeAll))
	}

	// ?before=<oldest> — zero events (all have received_ts >= oldest).
	status, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/channels/chan-before/events?limit=10&before="+recvTS[0], nil)
	if status != http.StatusOK {
		t.Fatalf("before oldest: %d body=%s", status, body)
	}
	var beforeNone []map[string]any
	if err := json.Unmarshal(body, &beforeNone); err != nil {
		t.Fatalf("decode before none: %v", err)
	}
	if len(beforeNone) != 0 {
		t.Fatalf("before oldest got %d events, want 0", len(beforeNone))
	}

	// Walk backwards page by page with before=.
	cursor := "9999"
	var pages [][]map[string]any
	for {
		status, body = doReq(t, s, token, http.MethodGet,
			"/v1/teams/"+defaultTeamID+"/channels/chan-before/events?limit=2&before="+cursor, nil)
		if status != http.StatusOK {
			t.Fatalf("walk page: %d body=%s", status, body)
		}
		var page []map[string]any
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("decode walk page: %v", err)
		}
		if len(page) == 0 {
			break
		}
		pages = append(pages, page)
		cursor, _ = page[len(page)-1]["received_ts"].(string)
	}
	total := 0
	for _, p := range pages {
		total += len(p)
	}
	if total != 5 {
		t.Errorf("before walk total = %d, want 5", total)
	}
}

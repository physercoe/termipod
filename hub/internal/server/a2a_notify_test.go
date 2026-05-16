package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// W2.11: every successful A2A relay drops a kind='a2a.received'
// producer='system' event into the receiver's most-recent active
// session so multi-agent coordination doesn't pay the InputRouter
// poll latency.
func TestNotifyA2AReceived_DeliversWithAttribution(t *testing.T) {
	s, _ := newTestServer(t)
	recvID := seedAgentWithActiveSession(t, s, "@worker.recv", "worker.v1")

	body := []byte(`{"params":{"message":{"parts":[{"kind":"text","text":"Need a sanity check on the 502 graph."}]}}}`)
	s.notifyA2AReceived(context.Background(), recvID, body, "steward.proj", "agent-steward-1")

	var (
		kind, producer, payloadJSON string
	)
	if err := s.db.QueryRow(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND kind = 'a2a.received'
		 ORDER BY seq DESC LIMIT 1`, recvID,
	).Scan(&kind, &producer, &payloadJSON); err != nil {
		t.Fatalf("query event: %v", err)
	}
	if producer != "system" {
		t.Errorf("producer = %q, want system", producer)
	}
	var p struct {
		FromHandle  string `json:"from_handle"`
		FromAgentID string `json:"from_agent_id"`
		Preview     string `json:"preview"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.FromHandle != "steward.proj" {
		t.Errorf("from_handle = %q, want steward.proj", p.FromHandle)
	}
	if p.FromAgentID != "agent-steward-1" {
		t.Errorf("from_agent_id = %q, want agent-steward-1", p.FromAgentID)
	}
	if !strings.Contains(p.Preview, "sanity check") {
		t.Errorf("preview missing text: %q", p.Preview)
	}
	if !strings.Contains(p.Body, "@steward.proj") {
		t.Errorf("body missing handle attribution: %q", p.Body)
	}
}

// W2.11: unauthed peer relays (no resolvable bearer) still notify the
// receiver, but with "peer message" attribution instead of @handle.
func TestNotifyA2AReceived_PeerMessage(t *testing.T) {
	s, _ := newTestServer(t)
	recvID := seedAgentWithActiveSession(t, s, "@worker.peer", "worker.v1")

	body := []byte(`{"params":{"message":{"parts":[{"kind":"text","text":"external ping"}]}}}`)
	s.notifyA2AReceived(context.Background(), recvID, body, "", "")

	var payloadJSON string
	if err := s.db.QueryRow(`
		SELECT payload_json FROM agent_events
		 WHERE agent_id = ? AND kind = 'a2a.received'
		 ORDER BY seq DESC LIMIT 1`, recvID,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("query event: %v", err)
	}
	var p struct {
		Body string `json:"body"`
	}
	_ = json.Unmarshal([]byte(payloadJSON), &p)
	if !strings.Contains(p.Body, "peer message") {
		t.Errorf("body missing peer-message label: %q", p.Body)
	}
	if !strings.Contains(p.Body, "external ping") {
		t.Errorf("body missing preview: %q", p.Body)
	}
}

// W2.11: no live session for the receiver → silently degrade.
func TestNotifyA2AReceived_NoLiveSessionSilent(t *testing.T) {
	s, _ := newTestServer(t)
	// Seed an agent but NOT a session — the helper short-circuits.
	id := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, '@worker.nosession', 'worker.v1', 'running', ?)`,
		id, defaultTeamID, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	s.notifyA2AReceived(context.Background(), id, []byte(`{}`), "", "")
	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'a2a.received'`, id,
	).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Errorf("notify fired with no session; got %d events", count)
	}
}

func TestA2ANotifyBody_Shapes(t *testing.T) {
	cases := []struct {
		name, handle, agentID, preview, want string
	}{
		{"handle + preview", "alice", "", "hello there", "A2A from @alice: hello there"},
		{"handle no preview", "alice", "", "", "A2A from @alice."},
		{"agent_id fallback", "", "agent-12", "ping", "A2A from `agent-12`: ping"},
		{"peer no preview", "", "", "", "A2A peer message."},
		{"strips leading @", "@bob", "", "hi", "A2A from @bob: hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a2aNotifyBody(tc.handle, tc.agentID, tc.preview)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

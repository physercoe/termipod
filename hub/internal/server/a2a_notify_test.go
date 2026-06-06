package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// W2.11 (revised): every successful A2A relay drops a kind='a2a.sent'
// producer='system' event into the SENDER's most-recent active session
// so its chat surfaces what it just dispatched. The receiver gets no
// sibling banner — the host-runner's a2aHubDispatcher already POSTs
// the message body as an input.text producer='a2a' event into the
// receiver's stream, which renders as the actual A2A turn.
func TestNotifyA2ASent_DeliversWithReceiverAttribution(t *testing.T) {
	s, _ := newTestServer(t)
	senderID := seedAgentWithActiveSession(t, s, "@steward.general", "steward.v1")

	body := []byte(`{"params":{"message":{"parts":[{"kind":"text","text":"Need a sanity check on the 502 graph."}]}}}`)
	s.notifyA2ASent(context.Background(), senderID, body, "worker.recv", "agent-recv-1")

	var (
		kind, producer, payloadJSON string
	)
	if err := s.eventsDB.QueryRow(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND kind = 'a2a.sent'
		 ORDER BY seq DESC LIMIT 1`, senderID,
	).Scan(&kind, &producer, &payloadJSON); err != nil {
		t.Fatalf("query event: %v", err)
	}
	if producer != "system" {
		t.Errorf("producer = %q, want system", producer)
	}
	var p struct {
		ToHandle  string `json:"to_handle"`
		ToAgentID string `json:"to_agent_id"`
		Preview   string `json:"preview"`
		Body      string `json:"body"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.ToHandle != "worker.recv" {
		t.Errorf("to_handle = %q, want worker.recv", p.ToHandle)
	}
	if p.ToAgentID != "agent-recv-1" {
		t.Errorf("to_agent_id = %q, want agent-recv-1", p.ToAgentID)
	}
	if !strings.Contains(p.Preview, "sanity check") {
		t.Errorf("preview missing text: %q", p.Preview)
	}
	if !strings.Contains(p.Body, "@worker.recv") {
		t.Errorf("body missing handle attribution: %q", p.Body)
	}
	if !strings.HasPrefix(p.Body, "→ A2A to ") {
		t.Errorf("body missing outbound arrow prefix: %q", p.Body)
	}
}

// Sender unknown (empty agent_id) → notifier is a no-op. The relay
// handler gates on this; the helper double-checks defensively so
// future callers can't push system events into a vacuum.
func TestNotifyA2ASent_UnknownSenderSilent(t *testing.T) {
	s, _ := newTestServer(t)
	// No prior agent_events for any agent; expect no rows added.
	s.notifyA2ASent(context.Background(), "", []byte(`{}`), "worker.recv", "agent-recv-1")
	var count int
	if err := s.eventsDB.QueryRow(`SELECT COUNT(*) FROM agent_events WHERE kind = 'a2a.sent'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("notify fired with empty sender; got %d events", count)
	}
}

// No live session for the sender → silently degrade (e.g. background
// chassis agent without an attached chat surface).
func TestNotifyA2ASent_NoLiveSessionSilent(t *testing.T) {
	s, _ := newTestServer(t)
	id := NewID()
	now := NowUTC()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, '@steward.nosession', 'steward.v1', 'running', ?)`,
		id, defaultTeamID, now); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	s.notifyA2ASent(context.Background(), id, []byte(`{}`), "worker.recv", "agent-recv-1")
	var count int
	if err := s.eventsDB.QueryRow(`
		SELECT COUNT(*) FROM agent_events
		 WHERE agent_id = ? AND kind = 'a2a.sent'`, id,
	).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Errorf("notify fired with no session; got %d events", count)
	}
}

func TestA2ASentBody_Shapes(t *testing.T) {
	cases := []struct {
		name, handle, agentID, preview, want string
	}{
		{"handle + preview", "alice", "", "hello there", "→ A2A to @alice: hello there"},
		{"handle no preview", "alice", "", "", "→ A2A to @alice."},
		{"agent_id fallback", "", "agent-12", "ping", "→ A2A to `agent-12`: ping"},
		{"peer no preview", "", "", "", "→ A2A to peer."},
		{"strips leading @", "@bob", "", "hi", "→ A2A to @bob: hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a2aSentBody(tc.handle, tc.agentID, tc.preview)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

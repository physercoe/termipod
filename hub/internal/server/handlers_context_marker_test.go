package server

import (
	"context"
	"net/http"
	"testing"
)

// TestPostAgentInput_EmitsCompactMarker is the end-to-end pin for
// ADR-014 OQ-4's "hub transcript = operation log" model: when the
// user types `/compact` to a claude-code agent, the input route
// records the user text as `input.text` *and* drops a typed
// `context.compacted` marker (`producer=system`) immediately after.
// Mobile renders the marker as an inline operation chip so the
// operator sees where the engine context truncated even though
// the hub transcript continues unbroken.
func TestPostAgentInput_EmitsCompactMarker(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "text", "body": "/compact"})
	if status != http.StatusCreated {
		t.Fatalf("post /compact: %d %s", status, raw)
	}

	// Two rows must exist: the user's input.text first, then the
	// system-emitted context.compacted marker. Order matters because
	// the marker is "this is what just happened", read after the
	// user's command in the transcript.
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT kind, producer FROM agent_events
		 WHERE agent_id = ? ORDER BY seq ASC`, agentID)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()

	type evt struct{ kind, producer string }
	var events []evt
	for rows.Next() {
		var e evt
		if err := rows.Scan(&e.kind, &e.producer); err != nil {
			t.Fatalf("scan: %v", err)
		}
		events = append(events, e)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	if events[0].kind != "input.text" || events[0].producer != "user" {
		t.Errorf("event[0] = %+v; want kind=input.text producer=user", events[0])
	}
	if events[1].kind != "context.compacted" || events[1].producer != "system" {
		t.Errorf("event[1] = %+v; want kind=context.compacted producer=system",
			events[1])
	}
}

// TestPostAgentInput_EmitsClearAndRewindMarkers asserts the other
// two claude verbs land their respective typed kinds. Together with
// the compact case this pins the full claude command set; gemini's
// /compress / /clear are unit-tested in detector tests rather than
// here because the e2e plumbing is identical.
func TestPostAgentInput_EmitsClearAndRewindMarkers(t *testing.T) {
	cases := []struct {
		body     string
		wantKind string
	}{
		{"/clear", "context.cleared"},
		{"/rewind", "context.rewound"},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			s, _ := newTestServer(t)
			h := newInputRouter(s)
			agentID := seedAgentForInput(t, s)

			status, raw := postInput(t, h, defaultTeamID, agentID,
				map[string]any{"kind": "text", "body": tc.body})
			if status != http.StatusCreated {
				t.Fatalf("post %s: %d %s", tc.body, status, raw)
			}
			var got string
			_ = s.db.QueryRow(`
				SELECT kind FROM agent_events
				 WHERE agent_id = ? AND producer = 'system'
				 ORDER BY seq DESC LIMIT 1`, agentID).Scan(&got)
			if got != tc.wantKind {
				t.Errorf("body=%q: marker kind=%q want %q",
					tc.body, got, tc.wantKind)
			}
		})
	}
}

// TestPostAgentInput_NoMarkerForRegularText — plain user input
// must not emit any marker. The detector's correctness is unit-
// tested separately; this is a guard against accidentally tripping
// the emit path on every input.
func TestPostAgentInput_NoMarkerForRegularText(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "text", "body": "hello, please continue"})
	if status != http.StatusCreated {
		t.Fatalf("post: %d %s", status, raw)
	}
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_events
		   WHERE agent_id = ? AND producer = 'system'`,
		agentID).Scan(&n)
	if n != 0 {
		t.Errorf("got %d system events for plain text; want 0", n)
	}
}

// TestPostAgentInput_NoMarkerForNonTextInput — kinds other than
// `text` (approval, answer, cancel, attach) skip the detector
// entirely. Even if a tool answer body happened to look like
// `/compact`, that's not a chat command — it's a tool response and
// the engine doesn't see it as a slash invocation.
func TestPostAgentInput_NoMarkerForNonTextInput(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := seedAgentForInput(t, s)

	status, raw := postInput(t, h, defaultTeamID, agentID, map[string]any{
		"kind":       "answer",
		"request_id": "tool-1",
		"body":       "/compact",
	})
	if status != http.StatusCreated {
		t.Fatalf("post: %d %s", status, raw)
	}
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_events
		   WHERE agent_id = ? AND producer = 'system'`,
		agentID).Scan(&n)
	if n != 0 {
		t.Errorf("got %d system events for answer-kind input; want 0", n)
	}
}

// TestPostAgentInput_MarkerForUnsupportedEngineSilent — codex
// agents whose slash vocabulary we haven't audited skip emission
// silently. The user's input.text still lands so the engine can
// process whatever it actually does with `/compact`; we just don't
// fabricate a marker for behaviour we can't predict.
func TestPostAgentInput_MarkerForUnsupportedEngineSilent(t *testing.T) {
	s, _ := newTestServer(t)
	h := newInputRouter(s)
	agentID := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, created_at)
		VALUES (?, ?, 'codex-worker', 'codex', ?)`,
		agentID, defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed codex agent: %v", err)
	}

	status, raw := postInput(t, h, defaultTeamID, agentID,
		map[string]any{"kind": "text", "body": "/compact"})
	if status != http.StatusCreated {
		t.Fatalf("post: %d %s", status, raw)
	}
	var n int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM agent_events WHERE agent_id = ?`, agentID).Scan(&n)
	if n != 1 {
		t.Errorf("codex /compact wrote %d events; want 1 (input.text only)", n)
	}
}

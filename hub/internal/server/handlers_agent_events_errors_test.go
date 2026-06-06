package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// seedEventFull inserts one event with an explicit kind + payload at a given
// (seq, ts) — the error-keyset tests need a mix of error and non-error kinds on
// a deterministic timeline, which the text-only seedEventAt can't give.
func seedEventFull(t *testing.T, s *Server, agentID, sessionID string,
	seq int, ts, kind, payloadJSON string) {
	t.Helper()
	if _, err := s.eventsWriteDB.Exec(`
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		VALUES (?,?,?,?,?,?,?,?)`,
		NewID(), agentID, seq, ts, kind, "agent", payloadJSON, sessionID,
	); err != nil {
		t.Fatalf("seed event seq=%d: %v", seq, err)
	}
}

// getEventsRaw GETs the events endpoint (a bare JSON array of events).
func getEventsRaw(t *testing.T, c *e2eCtx, path string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(
		context.Background(), http.MethodGet, c.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status %d: %s", path, resp.StatusCode, b)
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	return out
}

type seqMembership map[int64]bool

func (m seqMembership) has(s int64) bool { return m[s] }

func errSeqSet(events []map[string]any) seqMembership {
	s := seqMembership{}
	for _, e := range events {
		s[int64(e["seq"].(float64))] = true
	}
	return s
}

// TestErrorEventsKeyset_FiltersCanonicalErrors pins ADR-039 P3: ?error=true
// returns ONLY the canonical error events (the same predicate the digest fold
// uses — failing tool_result / tool_call_update / turn.result / error), never
// the surrounding non-error rows, and pages via the (ts, seq) keyset.
func TestErrorEventsKeyset_FiltersCanonicalErrors(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	// Replace the vector's events with a controlled mix.
	if _, err := c.s.eventsWriteDB.Exec(
		`DELETE FROM agent_events WHERE agent_id = ?`, agentID); err != nil {
		t.Fatalf("clear events: %v", err)
	}
	const (
		t1 = "2026-06-01T00:00:01Z"
		t2 = "2026-06-01T00:00:02Z"
		t3 = "2026-06-01T00:00:03Z"
		t4 = "2026-06-01T00:00:04Z"
		t5 = "2026-06-01T00:00:05Z"
		t6 = "2026-06-01T00:00:06Z"
	)
	seedEventFull(t, c.s, agentID, sessionID, 1, t1, "text", `{"text":"hi"}`)
	seedEventFull(t, c.s, agentID, sessionID, 2, t2, "tool_result", `{"is_error":true,"tool_use_id":"a"}`)
	seedEventFull(t, c.s, agentID, sessionID, 3, t3, "tool_call_update", `{"status":"running","toolCallId":"b"}`)
	seedEventFull(t, c.s, agentID, sessionID, 4, t4, "tool_call_update", `{"status":"failed","toolCallId":"c"}`)
	seedEventFull(t, c.s, agentID, sessionID, 5, t5, "tool_result", `{"is_error":false,"tool_use_id":"d"}`)
	seedEventFull(t, c.s, agentID, sessionID, 6, t6, "error", `{"message":"boom"}`)

	base := fmt.Sprintf("/v1/teams/%s/agents/%s/events", defaultTeamID, agentID)
	q := url.Values{"session": {sessionID}, "error": {"true"}}

	// All errors: seq 2, 4, 6 — and nothing else. Default (no cursor) is DESC.
	got := getEventsRaw(t, c, base+"?"+q.Encode())
	if len(got) != 3 {
		t.Fatalf("error events = %d, want 3 (got %v)", len(got), got)
	}
	want := map[int64]bool{2: true, 4: true, 6: true}
	for s := range want {
		if !errSeqSet(got).has(s) {
			t.Errorf("missing error seq %d; got %v", s, errSeqSet(got))
		}
	}
	// DESC ordering: newest error first.
	if int64(got[0]["seq"].(float64)) != 6 {
		t.Errorf("first error seq = %v, want 6 (DESC)", got[0]["seq"])
	}

	// Keyset page OLDER than the newest error (before the seq-6 error): seq 4, 2.
	q2 := url.Values{
		"session":    {sessionID},
		"error":      {"true"},
		"before_ts":  {t6},
		"before_seq": {"6"},
	}
	older := getEventsRaw(t, c, base+"?"+q2.Encode())
	if len(older) != 2 {
		t.Fatalf("older error events = %d, want 2 (got %v)", len(older), older)
	}
	if s := errSeqSet(older); s.has(6) || !s.has(4) || !s.has(2) {
		t.Errorf("older errors = %v, want {2,4}", s)
	}
}

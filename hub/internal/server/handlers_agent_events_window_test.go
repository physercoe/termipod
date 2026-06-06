package server

import (
	"fmt"
	"testing"
)

// seedEventAt inserts one event with an explicit (seq, ts) so a test can build
// a deterministic timeline with a ts tie — which the default seedAgentEvent
// (NowUTC) can't do.
func seedEventAt(t *testing.T, s *Server, agentID, sessionID string, seq int, ts string) {
	t.Helper()
	_, err := evWForAgent(t, s, agentID).Exec(`
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		VALUES (?,?,?,?,?,?,?,?)`,
		NewID(), agentID, seq, ts, "text", "agent", `{"text":"x"}`, sessionID)
	if err != nil {
		t.Fatalf("seed event seq=%d: %v", seq, err)
	}
}

// The session-scoped `(ts, seq)` compound keyset (plan P2) windows around a
// precise anchor without dropping same-ts siblings — the property the plain
// `ts < ?` / `ts > ?` cursors can't give. We seed a deliberate ts tie (seq 3,4,5
// all at t1) so a window split at seq 4 exercises exactly that boundary.
func TestListAgentEvents_CompoundKeysetWindow(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	// Drop the vector's NowUTC-stamped events; seed a controlled timeline.
	if _, err := evWForAgent(t, c.s, agentID).Exec(`DELETE FROM agent_events WHERE agent_id = ?`, agentID); err != nil {
		t.Fatalf("clear events: %v", err)
	}
	const (
		t0 = "2026-06-01T00:00:00Z"
		t1 = "2026-06-01T00:00:01Z" // seq 3,4,5 — the tie.
		t2 = "2026-06-01T00:00:02Z"
		t3 = "2026-06-01T00:00:03Z"
	)
	seedEventAt(t, c.s, agentID, sessionID, 1, t0)
	seedEventAt(t, c.s, agentID, sessionID, 2, t0)
	seedEventAt(t, c.s, agentID, sessionID, 3, t1)
	seedEventAt(t, c.s, agentID, sessionID, 4, t1)
	seedEventAt(t, c.s, agentID, sessionID, 5, t1)
	seedEventAt(t, c.s, agentID, sessionID, 6, t2)
	seedEventAt(t, c.s, agentID, sessionID, 7, t3)

	// Backward half: strictly before the key (t1, 4) →
	// ts < t1 (seq 1,2) ∪ (ts = t1 AND seq < 4) (seq 3) = {1,2,3}.
	before := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?session=%s&before_ts=%s&before_seq=4&limit=50",
		defaultTeamID, agentID, sessionID, t1))
	if got := seqSet(before); !sameInts(got, []int{1, 2, 3}) {
		t.Fatalf("before window = %v, want [1 2 3] (same-ts seq 3 kept, seq 5 excluded)", got)
	}

	// Forward half: strictly after the key (t1, 4) →
	// ts > t1 (seq 6,7) ∪ (ts = t1 AND seq > 4) (seq 5) = {5,6,7}.
	after := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?session=%s&after_ts=%s&after_seq=4&limit=50",
		defaultTeamID, agentID, sessionID, t1))
	if got := seqSet(after); !sameInts(got, []int{5, 6, 7}) {
		t.Fatalf("after window = %v, want [5 6 7] (same-ts seq 5 kept, seq 3 excluded)", got)
	}

	// The two halves plus the anchor partition the whole run with no overlap
	// and no gap — the contiguity the random-access loader relies on.
	union := append(seqSet(before), 4)
	union = append(union, seqSet(after)...)
	if !sameInts(union, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("before ∪ {anchor} ∪ after = %v, want the full contiguous 1..7", union)
	}

	// after_seq = 3 pulls the anchor itself (seq 4) into the forward half —
	// how the loader includes the anchor row in the reset window. Forward
	// order is (ts ASC, seq ASC), so the anchor is the first row.
	withAnchor := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?session=%s&after_ts=%s&after_seq=3&limit=50",
		defaultTeamID, agentID, sessionID, t1))
	got := seqSet(withAnchor)
	if len(got) == 0 || got[0] != 4 {
		t.Fatalf("after_seq=3 window = %v, want it to start at the anchor seq 4", got)
	}
}

// The list endpoint must echo each event's session_id at the top level —
// mobile resolves an agent's run session from the newest event's session_id
// to anchor the Insights analysis surface (digest + turns). Without it the
// archived-agent screen and project-agent sheet silently drop the Insights
// tab. Regression guard for that footgun.
func TestListAgentEvents_ReturnsSessionID(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	rows := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?tail=1&limit=5",
		defaultTeamID, agentID))
	if len(rows) == 0 {
		t.Fatalf("expected at least one event for the seeded run")
	}
	for _, r := range rows {
		if got, _ := r["session_id"].(string); got != sessionID {
			t.Fatalf("event session_id = %q, want %q (row=%v)", got, sessionID, r)
		}
	}
}

func seqSet(rows []map[string]any) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, int(r["seq"].(float64)))
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}

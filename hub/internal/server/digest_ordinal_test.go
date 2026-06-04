package server

import (
	"context"
	"testing"
)

// P2 of ADR-042: the turn index and error samples carry session_ordinal, so a
// session-scoped read disambiguates rows that share a (per-agent) start_seq
// after a resume.

// End-to-end: two agents in one session → their turns share start_seq=1 (it is
// per-agent) but get distinct, dense start_ordinals. This is the foundation the
// Insight Navigator lands on.
func TestSessionTurns_StartOrdinalDisambiguatesResumedAgents(t *testing.T) {
	s, _ := newA2ATestServer(t)
	ctx := context.Background()
	a := seedAgentRow(t, s, defaultTeamID, "p2-a", "claude-code")
	b := seedAgentRow(t, s, defaultTeamID, "p2-b", "claude-code")
	const session = "p2-session"

	// One turn-opening event per agent in the same session (the resume shape).
	seedAgentEvent(t, s, a, session, "text", map[string]any{"body": "from a"})
	seedAgentEvent(t, s, b, session, "text", map[string]any{"body": "from b"})

	// Materialize the turn index for both agents (the lazy backfill reads
	// session_ordinal off the events).
	if _, err := ensureAgentDigest(ctx, s.db, a, defaultTeamID); err != nil {
		t.Fatalf("backfill a: %v", err)
	}
	if _, err := ensureAgentDigest(ctx, s.db, b, defaultTeamID); err != nil {
		t.Fatalf("backfill b: %v", err)
	}

	turns, err := s.listSessionTurns(ctx, session, "", 50)
	if err != nil {
		t.Fatalf("listSessionTurns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns spanning both agents, got %d", len(turns))
	}

	ordByAgent := map[string]int64{}
	for _, tn := range turns {
		// Per-agent start_seq collides — both agents' first turn opens at seq 1.
		if tn.StartSeq != 1 {
			t.Fatalf("agent %s: want start_seq 1 (per-agent), got %d", tn.AgentID, tn.StartSeq)
		}
		ordByAgent[tn.AgentID] = tn.StartOrdinal
	}
	// start_ordinal is session-unique and dense: {1, 2}, one per agent — so a
	// navigator anchor resolves the right turn even though start_seq is 1 for
	// both.
	if ordByAgent[a] == ordByAgent[b] {
		t.Fatalf("start_ordinal collided (%d) — the bug; must differ per agent", ordByAgent[a])
	}
	if ordByAgent[a] != 1 || ordByAgent[b] != 2 {
		t.Fatalf("want dense session ordinals a=1 b=2, got a=%d b=%d", ordByAgent[a], ordByAgent[b])
	}
}

// Unit: the fold threads each event's Ordinal onto its turn (StartOrdinal) and
// its error samples (SampleOrdinals), aligned 1:1 with SampleSeqs.
func TestDigestFold_ThreadsOrdinalOntoAnchors(t *testing.T) {
	events := []foldEvent{
		{Seq: 10, Ordinal: 3, Kind: "turn.start", TS: "2026-06-04T00:00:00Z",
			Payload: map[string]any{"turn_id": "t1"}},
		{Seq: 11, Ordinal: 4, Kind: "error", TS: "2026-06-04T00:00:01Z",
			Payload: map[string]any{"type": "boom"}},
		{Seq: 12, Ordinal: 5, Kind: "turn.result", TS: "2026-06-04T00:00:02Z",
			Payload: map[string]any{"status": "success"}},
	}
	d, turns := computeAgentDigest("a", "team", events)

	if len(turns) != 1 {
		t.Fatalf("want 1 turn, got %d", len(turns))
	}
	if turns[0].StartOrdinal != 3 {
		t.Fatalf("turn StartOrdinal: want 3 (the turn.start's ordinal), got %d", turns[0].StartOrdinal)
	}
	c := d.Errors["error:boom"]
	if c == nil {
		t.Fatalf("error class error:boom not recorded")
	}
	if len(c.SampleOrdinals) != len(c.SampleSeqs) {
		t.Fatalf("SampleOrdinals (%d) not aligned 1:1 with SampleSeqs (%d)",
			len(c.SampleOrdinals), len(c.SampleSeqs))
	}
	if len(c.SampleOrdinals) != 1 || c.SampleOrdinals[0] != 4 {
		t.Fatalf("want error SampleOrdinals [4], got %v", c.SampleOrdinals)
	}
}

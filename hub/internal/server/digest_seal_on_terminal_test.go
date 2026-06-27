package server

import (
	"context"
	"net/http"
	"testing"
)

// TestDigestSealedOnCrash verifies the #118 §4 fold-on-close: when an agent
// flips to a crash/failure terminal state (not the operator stop path), the run
// digest is folded current + outcome-stamped right then, so the first Insight
// open is an O(1) read rather than a full O(n) backfill.
func TestDigestSealedOnCrash(t *testing.T) {
	s, token := newA2ATestServer(t)
	ctx := context.Background()
	const sesID = "ses-crash"
	const agentID = "agent-crash"

	seedSessionWithAgent(t, s, defaultTeamID, sesID, agentID)
	insertEventRow(t, s, agentID, sesID, 1, "text", `{"text":"a"}`)
	insertEventRow(t, s, agentID, sesID, 2, "tool_call", `{"name":"read","id":"c1"}`)
	insertEventRow(t, s, agentID, sesID, 3, "text", `{"text":"b"}`)

	// Pre-condition: no digest row yet.
	dr, err := s.digestReader(defaultTeamID)
	if err != nil {
		t.Fatalf("digestReader: %v", err)
	}
	if _, ok, _ := loadAgentDigest(ctx, dr, agentID); ok {
		t.Fatal("digest unexpectedly present before terminal transition")
	}

	// Crash the agent via the same PATCH the host-runner reconcile uses.
	status, body := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/agents/"+agentID+"/",
		map[string]any{"status": "crashed"})
	if status != http.StatusNoContent {
		t.Fatalf("PATCH crashed: status=%d body=%s", status, body)
	}

	// Post-condition: digest exists, watermark caught up to the last event, and
	// the terminal outcome is stamped — i.e. no read-time backfill is owed.
	d, ok, err := loadAgentDigest(ctx, dr, agentID)
	if err != nil {
		t.Fatalf("loadAgentDigest: %v", err)
	}
	if !ok {
		t.Fatal("digest not sealed after crash (no row) — fold-on-close missing")
	}
	if d.WatermarkSeq != 3 {
		t.Fatalf("watermark = %d, want 3 (digest left stale after crash)", d.WatermarkSeq)
	}
	if d.Outcome == "" {
		t.Fatal("digest outcome not stamped on crash")
	}
}

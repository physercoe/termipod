package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// post_notice is the answerless sibling of the request_* family: a
// one-way FYI for the principal. These tests pin the two properties
// that keep it on the right side of the Me-page boundary:
//
//  1. It returns immediately with status="posted" (NOT
//     "awaiting_response") — fire-and-forget, the agent keeps working.
//  2. The attention_items row it opens carries kind='notice' and an
//     EMPTY pending_payload — that absence is exactly what mobile's
//     _filterForAttention reads to file it under "Messages" (FYI), not
//     "Requests". A regression that stamps a pending_payload here would
//     silently promote every notice into the Requests inbox.

func TestPostNotice_PostsFyiWithoutPendingPayload(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "notice-poster",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\nbackend:\n  cmd: echo test\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"summary": "Phase 2 done — deployed v3 to staging, moving to phase 3.",
	})
	res, jerr := c.s.mcpPostNotice(
		context.Background(), defaultTeamID, out.AgentID, args)
	if jerr != nil {
		t.Fatalf("mcpPostNotice: %s", jerr.Message)
	}
	if got := mcpToolBodyField(res, "status"); got != "posted" {
		t.Fatalf("status = %q; want posted (fire-and-forget, not awaiting_response)", got)
	}
	if got := mcpToolBodyField(res, "kind"); got != "notice" {
		t.Fatalf("kind = %q; want notice", got)
	}

	// The row must classify as a Message: kind='notice', default
	// severity 'minor', and crucially an empty pending_payload.
	var kind, severity, pending, assignees string
	if err := c.s.db.QueryRow(`
		SELECT kind, severity,
		       COALESCE(pending_payload_json, ''),
		       COALESCE(current_assignees_json, '')
		  FROM attention_items
		 WHERE actor_handle = ? ORDER BY created_at DESC LIMIT 1`,
		"notice-poster",
	).Scan(&kind, &severity, &pending, &assignees); err != nil {
		t.Fatalf("attention lookup: %v", err)
	}
	if kind != "notice" {
		t.Errorf("row kind = %q; want notice", kind)
	}
	if severity != "minor" {
		t.Errorf("default severity = %q; want minor", severity)
	}
	if pending != "" {
		t.Errorf("pending_payload = %q; a notice MUST carry none or it lands in Requests", pending)
	}
	if assignees == "" || assignees == "[]" {
		t.Errorf("assignees = %q; want the principal so it reaches the director's inbox", assignees)
	}
}

func TestPostNotice_RejectsBlankSummaryAndCriticalSeverity(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	// Missing summary → -32602.
	if _, jerr := c.s.mcpPostNotice(
		context.Background(), defaultTeamID, "agent-x",
		json.RawMessage(`{}`),
	); jerr == nil {
		t.Fatal("blank summary accepted; want -32602")
	}

	// 'critical' is reserved for blocking asks — a notice blocks nothing.
	if _, jerr := c.s.mcpPostNotice(
		context.Background(), defaultTeamID, "agent-x",
		json.RawMessage(`{"summary":"x","severity":"critical"}`),
	); jerr == nil {
		t.Fatal("severity=critical accepted; want -32602 (minor|major only)")
	}
}

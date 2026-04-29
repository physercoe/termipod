package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Turn-based contract for request_help (and its siblings request_approval,
// request_select). The MCP tool returns immediately with awaiting_response;
// the principal's reply lands as a separate input.attention_reply event on
// the originating agent's stream when /decide resolves the attention.
//
// These tests pin three properties:
//
//  1. The MCP call returns synchronously without holding a long-poll.
//  2. /decide on a help_request resolves the attention AND fans out an
//     input.attention_reply with the correct payload to the originating
//     agent (looked up via session_id → current_agent_id).
//  3. Approve-without-body still 400s; reject-as-dismissal still succeeds.

func TestRequestHelp_ReturnsAwaitingResponseImmediately(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "help-asker",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"question": "Should I refactor auth before or after the cache layer?",
		"context":  "Both touch the same module.",
		"mode":     "clarify",
	})

	// Turn-based: the call must return synchronously, not block on a
	// long-poll. Bound it loosely (1s) so a regression that re-introduces
	// the long-poll (10 min) makes this test fail fast rather than hang.
	start := time.Now()
	doneCh := make(chan any, 1)
	go func() {
		res, _ := c.s.mcpRequestHelp(
			context.Background(), defaultTeamID, out.AgentID, args)
		doneCh <- res
	}()
	select {
	case res := <-doneCh:
		elapsed := time.Since(start)
		if elapsed > 1*time.Second {
			t.Fatalf("mcpRequestHelp held the call for %v; expected immediate return", elapsed)
		}
		gotStatus := mcpToolBodyField(res, "status")
		if gotStatus != "awaiting_response" {
			t.Fatalf("status = %q; want awaiting_response", gotStatus)
		}
		if mcpToolBodyField(res, "kind") != "help_request" {
			t.Fatalf("kind != help_request: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("mcpRequestHelp blocked past 2s — long-poll regression")
	}
}

func TestDecide_HelpRequestFansOutAttentionReply(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "fanout-asker",
		Kind:            "claude-code",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	// Agent calls request_help → attention with session_id stamped.
	args, _ := json.Marshal(map[string]any{
		"question": "Should I refactor X first?",
		"mode":     "clarify",
	})
	if _, jerr := c.s.mcpRequestHelp(
		context.Background(), defaultTeamID, out.AgentID, args,
	); jerr != nil {
		t.Fatalf("mcpRequestHelp: %s", jerr.Message)
	}

	// Look up the attention id (most recent help_request for this agent).
	var attentionID string
	if err := c.s.db.QueryRow(`
		SELECT id FROM attention_items
		 WHERE kind = 'help_request' AND actor_handle = ?
		 ORDER BY created_at DESC LIMIT 1`, "fanout-asker",
	).Scan(&attentionID); err != nil {
		t.Fatalf("attention lookup: %v", err)
	}

	// Snapshot the agent's seq before /decide so we can verify the
	// attention_reply lands as the *next* event, not a pre-existing one.
	var seqBefore int64
	_ = c.s.db.QueryRow(`
		SELECT COALESCE(MAX(seq), 0) FROM agent_events WHERE agent_id = ?`,
		out.AgentID,
	).Scan(&seqBefore)

	// /decide with body=<reply> resolves the attention and fans out the
	// attention_reply to the originating agent.
	status, _ := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+attentionID+"/decide",
		map[string]any{
			"decision": "approve",
			"by":       "@principal",
			"body":     "Refactor auth first; cache changes will reuse the new shape.",
		})
	if status != 200 {
		t.Fatalf("decide = %d", status)
	}

	// Verify the input.attention_reply event was posted to the agent.
	rows, err := c.s.db.Query(`
		SELECT kind, producer, payload_json
		  FROM agent_events
		 WHERE agent_id = ? AND seq > ?
		 ORDER BY seq ASC`, out.AgentID, seqBefore)
	if err != nil {
		t.Fatalf("agent_events query: %v", err)
	}
	defer rows.Close()
	var matched bool
	for rows.Next() {
		var kind, producer, payload string
		if err := rows.Scan(&kind, &producer, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if kind != "input.attention_reply" {
			continue
		}
		if producer != "user" {
			t.Errorf("attention_reply producer = %q; want user", producer)
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(payload), &p)
		if p["request_id"] != attentionID {
			t.Errorf("attention_reply.request_id = %v; want %s", p["request_id"], attentionID)
		}
		if p["kind"] != "help_request" {
			t.Errorf("attention_reply.kind = %v; want help_request", p["kind"])
		}
		if !strings.Contains(p["body"].(string), "Refactor auth first") {
			t.Errorf("attention_reply.body missing principal's text: %v", p["body"])
		}
		matched = true
		break
	}
	if !matched {
		t.Fatalf("no input.attention_reply event posted to agent after /decide")
	}
}

func TestDecide_HelpRequestRejectsApproveWithoutBody(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	id := NewID()
	now := NowUTC()
	if _, err := c.s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at,
			actor_kind, actor_handle, pending_payload_json
		) VALUES (?, NULL, 'team', NULL, 'help_request',
		          'q?', 'minor', '[]', 'open', ?,
		          'agent', 'someone', '{}')`, id, now,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// approve without body must 400.
	status, body := c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "approve", "by": "@me"})
	if status != 400 {
		t.Fatalf("approve-without-body = %d (body %v); want 400", status, body)
	}

	// reject without body must succeed (dismissal, no answer needed).
	status, _ = c.call("POST",
		"/v1/teams/"+c.teamID+"/attention/"+id+"/decide",
		map[string]any{"decision": "reject", "by": "@me",
			"reason": "not for me"})
	if status != 200 {
		t.Fatalf("reject = %d; want 200", status)
	}
}

// mcpToolBodyField reaches into mcpResultJSON's wrapper to read a single
// top-level field from the inner content[0].text payload. mcpResultJSON
// returns {content:[{type:'text', text:<json-string>}]}.
func mcpToolBodyField(reply any, field string) string {
	m, ok := reply.(map[string]any)
	if !ok {
		return ""
	}
	contentArr, ok := m["content"].([]any)
	if !ok || len(contentArr) == 0 {
		return ""
	}
	first, ok := contentArr[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := first["text"].(string)
	var inner map[string]any
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		return ""
	}
	v, _ := inner[field].(string)
	return v
}

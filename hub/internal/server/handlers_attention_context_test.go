package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// /attention/{id}/context returns the originating session pointer and
// the last 10 transcript turns leading up to the request. Pins three
// behaviors:
//
//  1. session_id is populated by the request_* MCP tools at insert time.
//  2. context endpoint surfaces the originating agent_id + handle and
//     the recent events (newest-first).
//  3. attentions without a session pointer (system-originated, legacy
//     rows) degrade to empty events rather than 404 — mobile detail
//     screen can render the metadata-only fallback.
func TestAttentionContext_RoundTripFromHelpRequest(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "ctx-asker",
		Kind:            "claude-code",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}

	// Find the auto-opened session and seed two transcript turns so the
	// context endpoint has something to surface.
	var sessionID string
	if err := c.s.db.QueryRow(
		`SELECT id FROM sessions WHERE current_agent_id = ?`, out.AgentID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	for i, text := range []string{"first turn", "second turn"} {
		seedAgentEvent(t, c.s, out.AgentID, sessionID, "text",
			map[string]any{"body": text, "seq_label": i})
	}

	// Agent calls request_help — populates session_id on the attention.
	args, _ := json.Marshal(map[string]any{
		"question": "Should I refactor X first?",
		"context":  "Both touch the same module.",
	})
	go func() {
		_, _ = c.s.mcpRequestHelp(
			context.Background(), defaultTeamID, out.AgentID, args)
	}()

	// Wait for the row to land — the goroutine starts the long-poll
	// after the INSERT, so within a few tens of ms it should be visible.
	var attentionID string
	for range 40 {
		var found string
		_ = c.s.db.QueryRow(`
			SELECT id FROM attention_items
			 WHERE kind = 'help_request'
			   AND COALESCE(session_id, '') = ?
			 LIMIT 1`, sessionID).Scan(&found)
		if found != "" {
			attentionID = found
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if attentionID == "" {
		t.Fatalf("help_request attention with session_id never inserted")
	}

	// Hit the context endpoint.
	req, _ := http.NewRequest("GET",
		c.srv.URL+"/v1/teams/"+c.teamID+"/attention/"+attentionID+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET context: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("context status = %d body=%s", resp.StatusCode, body)
	}
	var got attentionContextOut
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID != sessionID {
		t.Fatalf("session_id = %q; want %q", got.SessionID, sessionID)
	}
	if got.AgentID != out.AgentID {
		t.Fatalf("agent_id = %q; want %q", got.AgentID, out.AgentID)
	}
	if got.AgentHandle != "ctx-asker" {
		t.Fatalf("agent_handle = %q; want ctx-asker", got.AgentHandle)
	}
	if len(got.Events) != 2 {
		t.Fatalf("event count = %d; want 2 (the seeded turns)", len(got.Events))
	}
	// Newest-first: seq DESC.
	if got.Events[0]["seq"].(float64) <= got.Events[1]["seq"].(float64) {
		t.Fatalf("events not seq DESC: %v", got.Events)
	}
}

func TestAttentionContext_NoSessionPointerReturnsEmpty(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	// Seed a system-originated attention (no session_id) the way budget
	// exhaustion or a legacy row would look.
	id := NewID()
	now := NowUTC()
	if _, err := c.s.db.Exec(`
		INSERT INTO attention_items (
			id, project_id, scope_kind, scope_id, kind,
			summary, severity, current_assignees_json, status, created_at
		) VALUES (?, NULL, 'team', NULL, 'approval_request',
		          'no session', 'minor', '[]', 'open', ?)`, id, now,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req, _ := http.NewRequest("GET",
		c.srv.URL+"/v1/teams/"+c.teamID+"/attention/"+id+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET context: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	var got attentionContextOut
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID != "" {
		t.Fatalf("session_id should be empty; got %q", got.SessionID)
	}
	if len(got.Events) != 0 {
		t.Fatalf("events should be empty; got %d", len(got.Events))
	}
}

// seedAgentEvent writes one row to agent_events for use by tests that
// need the originating session to have a transcript to render.
func seedAgentEvent(t *testing.T, s *Server, agentID, sessionID, kind string, payload map[string]any) {
	t.Helper()
	pj, _ := json.Marshal(payload)
	_, err := s.db.Exec(`
		INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		SELECT ?, ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?
		  FROM agent_events WHERE agent_id = ?`,
		NewID(), agentID, NowUTC(), kind, "agent", string(pj), sessionID, agentID)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

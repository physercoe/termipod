package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// seedChannelAndAgent drops the minimum rows needed for post_excerpt: a
// channel the excerpt posts into, and an agent owning pane_id=P so the
// pane_ref comes back populated.
func seedChannelAndAgent(t *testing.T, s *Server, paneID, hostID string) (channelID, agentID string) {
	t.Helper()
	ctx := context.Background()
	channelID = NewID()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO channels (id, scope_kind, name, created_at)
		VALUES (?, 'team', 'meta', ?)`, channelID, NowUTC()); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if hostID != "" {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO hosts (id, team_id, name, status, capabilities_json, created_at)
			VALUES (?, ?, 'h1', 'online', '{}', ?)`,
			hostID, defaultTeamID, NowUTC()); err != nil {
			t.Fatalf("seed host: %v", err)
		}
	}
	agentID = NewID()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (
			id, team_id, handle, kind, host_id, pane_id, created_at
		) VALUES (?, ?, 'worker', 'claude-code', NULLIF(?, ''), ?, ?)`,
		agentID, defaultTeamID, hostID, paneID, NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return channelID, agentID
}

// TestMCP_PostExcerpt_RecordsEventWithPaneRef is the happy path:
// given an agent bound to pane %3 on host h1, posting an excerpt must
// produce an event whose parts contain one excerpt part populated with
// the original content + line range, plus a pane_ref row that
// identifies the source pane.
func TestMCP_PostExcerpt_RecordsEventWithPaneRef(t *testing.T) {
	s, _ := newTestServer(t)
	channelID, agentID := seedChannelAndAgent(t, s, "%3", "h1")

	args, _ := json.Marshal(map[string]any{
		"channel_id": channelID,
		"line_from":  10,
		"line_to":    12,
		"content":    "$ make test\nPASS\nok  ./...\n",
		"summary":    "tests passing after refactor",
	})
	out, jerr := s.mcpPostExcerpt(context.Background(), agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPostExcerpt: %+v", jerr)
	}
	if out == nil {
		t.Fatalf("nil result")
	}

	// Read it back from the events table.
	var (
		partsJSON, paneRefJSON, fromID string
	)
	if err := s.db.QueryRow(`
		SELECT parts_json, COALESCE(pane_ref_json, ''), COALESCE(from_id, '')
		FROM events WHERE channel_id = ? ORDER BY received_ts DESC LIMIT 1`,
		channelID).Scan(&partsJSON, &paneRefJSON, &fromID); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if fromID != agentID {
		t.Errorf("from_id = %q, want %q", fromID, agentID)
	}

	var parts []map[string]any
	_ = json.Unmarshal([]byte(partsJSON), &parts)
	// Expect summary-text part (index 0) followed by excerpt (index 1).
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2 (summary+excerpt)", len(parts))
	}
	if parts[0]["kind"] != "text" || !strings.Contains(parts[0]["text"].(string), "refactor") {
		t.Errorf("summary text missing: %+v", parts[0])
	}
	if parts[1]["kind"] != "excerpt" {
		t.Errorf("second part not excerpt: %+v", parts[1])
	}
	ex, _ := parts[1]["excerpt"].(map[string]any)
	if ex == nil {
		t.Fatalf("excerpt body missing")
	}
	if int(ex["line_from"].(float64)) != 10 || int(ex["line_to"].(float64)) != 12 {
		t.Errorf("line range wrong: %v..%v", ex["line_from"], ex["line_to"])
	}
	if !strings.Contains(ex["content"].(string), "PASS") {
		t.Errorf("content not stored verbatim: %q", ex["content"])
	}

	var paneRef map[string]any
	_ = json.Unmarshal([]byte(paneRefJSON), &paneRef)
	if paneRef["pane_id"] != "%3" || paneRef["host_id"] != "h1" {
		t.Errorf("pane_ref mismatch: %+v", paneRef)
	}
}

// TestMCP_PostExcerpt_NoPaneBinding: an agent without a registered pane
// (service bot, test fixture) should still succeed — the feed gets the
// content, pane_ref just has empty identifiers. We don't fail the call
// because losing the excerpt would be worse than losing the jump-back.
func TestMCP_PostExcerpt_NoPaneBinding(t *testing.T) {
	s, _ := newTestServer(t)
	channelID, agentID := seedChannelAndAgent(t, s, "", "")

	args, _ := json.Marshal(map[string]any{
		"channel_id": channelID,
		"content":    "hello from a paneless bot",
	})
	_, jerr := s.mcpPostExcerpt(context.Background(), agentID, args)
	if jerr != nil {
		t.Fatalf("mcpPostExcerpt: %+v", jerr)
	}

	var paneRefJSON string
	if err := s.db.QueryRow(`
		SELECT COALESCE(pane_ref_json, '') FROM events WHERE channel_id = ?`,
		channelID).Scan(&paneRefJSON); err != nil {
		t.Fatalf("read: %v", err)
	}
	var paneRef map[string]any
	_ = json.Unmarshal([]byte(paneRefJSON), &paneRef)
	if paneRef["pane_id"] != "" || paneRef["host_id"] != "" {
		t.Errorf("pane_ref should be empty: %+v", paneRef)
	}
}

// TestMCP_PostExcerpt_RequiresChannelAndContent: malformed calls must
// come back as a -32602 params error, not a 5xx and not a silent drop.
func TestMCP_PostExcerpt_RequiresChannelAndContent(t *testing.T) {
	s, _ := newTestServer(t)
	_, agentID := seedChannelAndAgent(t, s, "%1", "")

	// missing channel_id
	args, _ := json.Marshal(map[string]any{"content": "x"})
	_, jerr := s.mcpPostExcerpt(context.Background(), agentID, args)
	if jerr == nil || jerr.Code != -32602 {
		t.Errorf("expected -32602 for missing channel_id, got %+v", jerr)
	}

	// missing content
	args, _ = json.Marshal(map[string]any{"channel_id": "c1"})
	_, jerr = s.mcpPostExcerpt(context.Background(), agentID, args)
	if jerr == nil || jerr.Code != -32602 {
		t.Errorf("expected -32602 for missing content, got %+v", jerr)
	}
}

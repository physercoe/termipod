package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	url2 "net/url"
	"testing"
)

// Resume mints a new agent attached to the same session row; events
// from the prior agent stay stamped with the prior agent_id. The
// pre-fix list endpoint AND'd `agent_id = ?` into every query, so a
// cold-open backfill against the new agent's id returned the empty
// new-agent set instead of the session's full transcript. This pins
// the right behavior: a session-scoped tail returns events from
// every agent that has stamped session_id, ordered by ts.
func TestListAgentEvents_SessionScoped_SpansResumedAgents(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	// First agent — the "before resume" one.
	first, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "resume-test",
		Kind:            "claude-code",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("DoSpawn first: %v", err)
	}
	var sessionID string
	if err := c.s.db.QueryRow(
		`SELECT id FROM sessions WHERE current_agent_id = ?`, first.AgentID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	// Three turns under the first agent.
	for i, body := range []string{"hello", "second", "third"} {
		seedAgentEvent(t, c.s, first.AgentID, sessionID, "text",
			map[string]any{"body": body, "i": i})
	}

	// Mint a "second" agent and re-point the session at it (the resume
	// path's effect, distilled). Stamp two events under the new agent.
	second, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle: "resume-test-2",
		Kind:        "claude-code",
		HostID:      hostID,
		SpawnSpec:   "driving_mode: M2\n",
	})
	if err != nil {
		t.Fatalf("DoSpawn second: %v", err)
	}
	if _, err := c.s.db.Exec(
		`UPDATE sessions SET current_agent_id = ? WHERE id = ?`,
		second.AgentID, sessionID); err != nil {
		t.Fatalf("repoint session: %v", err)
	}
	for i, body := range []string{"resumed-1", "resumed-2"} {
		seedAgentEvent(t, c.s, second.AgentID, sessionID, "text",
			map[string]any{"body": body, "i": i})
	}

	// Cold-open style: tail=true&session=<id> against the *new* agent.
	// Pre-fix this returned 2 rows (the new agent's two events) and the
	// transcript looked half-empty. Now should return all 5.
	url := fmt.Sprintf(
		"%s/v1/teams/%s/agents/%s/events?tail=true&limit=50&session=%s",
		srv.URL, defaultTeamID, second.AgentID, sessionID)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 events spanning both agents, got %d", len(rows))
	}
	// Verify we have rows from both agents (the bug would only return
	// `second.AgentID` rows).
	seenAgents := map[string]int{}
	for _, e := range rows {
		seenAgents[fmt.Sprint(e["agent_id"])]++
	}
	if seenAgents[first.AgentID] != 3 {
		t.Fatalf("first agent: want 3 events, got %d", seenAgents[first.AgentID])
	}
	if seenAgents[second.AgentID] != 2 {
		t.Fatalf("second agent: want 2 events, got %d", seenAgents[second.AgentID])
	}
}

// before_ts paginates session-scoped feeds across the agent boundary.
// Per-agent seq is unusable as a cursor when the session spans two
// agents (seq=2 under agent-A and seq=2 under agent-B are different
// rows, not duplicates), so the mobile feed switches to ts when a
// session filter is set. This test pins the cursor semantics.
func TestListAgentEvents_SessionScoped_BeforeTsPaginates(t *testing.T) {
	c := newE2E(t)
	srv := httptest.NewServer(c.s.router)
	t.Cleanup(srv.Close)

	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	out, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "page-test",
		Kind:            "claude-code",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	var sessionID string
	if err := c.s.db.QueryRow(
		`SELECT id FROM sessions WHERE current_agent_id = ?`, out.AgentID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	for i := range 6 {
		seedAgentEvent(t, c.s, out.AgentID, sessionID, "text",
			map[string]any{"i": i})
	}

	// First page (tail) — newest 3.
	url := fmt.Sprintf(
		"%s/v1/teams/%s/agents/%s/events?tail=true&limit=3&session=%s",
		srv.URL, defaultTeamID, out.AgentID, sessionID)
	rows := getEvents(t, url, c.token)
	if len(rows) != 3 {
		t.Fatalf("page 1: want 3, got %d", len(rows))
	}
	cursor := fmt.Sprint(rows[len(rows)-1]["ts"])
	if cursor == "" {
		t.Fatalf("cursor ts missing on page 1")
	}

	// Second page using before_ts (encode — ISO ts contains `:`).
	url = fmt.Sprintf(
		"%s/v1/teams/%s/agents/%s/events?before_ts=%s&limit=3&session=%s",
		srv.URL, defaultTeamID, out.AgentID,
		url2.QueryEscape(cursor), sessionID)
	page2 := getEvents(t, url, c.token)
	if len(page2) == 0 {
		t.Fatalf("page 2: want >=1 row, got 0")
	}
	// Ensure no overlap on event id between pages.
	page1Ids := map[string]bool{}
	for _, e := range rows {
		page1Ids[fmt.Sprint(e["id"])] = true
	}
	for _, e := range page2 {
		if page1Ids[fmt.Sprint(e["id"])] {
			t.Fatalf("page 2 overlaps page 1 on id %v", e["id"])
		}
	}
}

func getEvents(t *testing.T, url, token string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rows
}

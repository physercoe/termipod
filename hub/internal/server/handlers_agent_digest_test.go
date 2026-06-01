package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// seedVectorRun spawns a claude-code agent with an auto-opened session and
// seeds it with the shared canonical vector's events. Returns agent + session
// ids. The events are inserted directly (no fold), so a digest read exercises
// the lazy backfill.
func seedVectorRun(t *testing.T, c *e2eCtx) (agentID, sessionID string) {
	t.Helper()
	hostID := seedHostCaps(t, c.s, `{
		"agents": {"claude-code": {"installed": true, "supports": ["M2"]}}
	}`)
	res, _, err := c.s.DoSpawn(context.Background(), defaultTeamID, spawnIn{
		ChildHandle:     "digest-vec",
		Kind:            "claude-code",
		HostID:          hostID,
		SpawnSpec:       "driving_mode: M2\nbackend:\n  cmd: echo test\n",
		AutoOpenSession: true,
	})
	if err != nil {
		t.Fatalf("DoSpawn: %v", err)
	}
	agentID = res.AgentID
	if err := c.s.db.QueryRow(
		`SELECT id FROM sessions WHERE current_agent_id = ?`, agentID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("session lookup: %v", err)
	}
	_, events := loadDigestVector(t)
	for _, e := range events {
		seedAgentEvent(t, c.s, agentID, sessionID, e.Kind, e.Payload)
	}
	return agentID, sessionID
}

func TestAgentDigestEndpoint_BackfillsAndSummarizes(t *testing.T) {
	c := newE2E(t)
	agentID, _ := seedVectorRun(t, c)

	status, body := c.call(http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/agents/%s/digest", defaultTeamID, agentID), nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, body)
	}
	if got := int64(body["event_count"].(float64)); got != 11 {
		t.Errorf("event_count = %d, want 11", got)
	}
	if got := int64(body["turn_count"].(float64)); got != 2 {
		t.Errorf("turn_count = %d, want 2", got)
	}
	if got := int64(body["error_count"].(float64)); got != 3 {
		t.Errorf("error_count = %d, want 3", got)
	}
	if got := int64(body["tool_failed"].(float64)); got != 1 {
		t.Errorf("tool_failed = %d, want 1", got)
	}
	if got := body["cost_usd"].(float64); got < 0.0299 || got > 0.0301 {
		t.Errorf("cost_usd = %v, want ~0.03", got)
	}
}

func TestSessionDigestEndpoint_RollsUpAgents(t *testing.T) {
	c := newE2E(t)
	_, sessionID := seedVectorRun(t, c)

	status, body := c.call(http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/sessions/%s/digest", defaultTeamID, sessionID), nil)
	if status != http.StatusOK {
		t.Fatalf("status %d: %v", status, body)
	}
	if body["session_id"] != sessionID {
		t.Errorf("session_id = %v, want %s", body["session_id"], sessionID)
	}
	if got := int64(body["event_count"].(float64)); got != 11 {
		t.Errorf("event_count = %d, want 11", got)
	}
	if got := int64(body["error_count"].(float64)); got != 3 {
		t.Errorf("error_count = %d, want 3", got)
	}
	if _, ok := body["agent_ids"].([]any); !ok {
		t.Errorf("agent_ids missing: %v", body["agent_ids"])
	}
}

// TestListAgentEvents_KindFilter verifies the kind= param returns only the
// matching kinds, full-run (server-side), across the agent cursor.
func TestListAgentEvents_KindFilter(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	_ = sessionID

	rows := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?since=0&limit=50&kind=tool_call,tool_result",
		defaultTeamID, agentID))
	// vector: tool_call ×2 (seq 3, 8), tool_result ×2 (seq 4, 9) = 4.
	if len(rows) != 4 {
		t.Fatalf("kind-filtered rows = %d, want 4", len(rows))
	}
	for _, r := range rows {
		k := r["kind"].(string)
		if k != "tool_call" && k != "tool_result" {
			t.Errorf("unexpected kind %q in kind-filtered listing", k)
		}
	}
}

// TestListAgentEvents_AfterTS verifies the session-scoped forward window.
func TestListAgentEvents_AfterTS(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)

	// Grab the full session tail to find a midpoint ts.
	all := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?session=%s&limit=50",
		defaultTeamID, agentID, sessionID))
	if len(all) != 11 {
		t.Fatalf("session events = %d, want 11", len(all))
	}
	midTS := all[5]["ts"].(string)

	fwd := getEventRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/events?session=%s&after_ts=%s&limit=50",
		defaultTeamID, agentID, sessionID, midTS))
	if len(fwd) == 0 || len(fwd) >= 11 {
		t.Fatalf("after_ts window = %d, want a strict forward slice", len(fwd))
	}
	for _, r := range fwd {
		if ts := r["ts"].(string); ts <= midTS {
			t.Errorf("after_ts row ts %q not strictly after %q", ts, midTS)
		}
	}
}

// TestInsightsReconcilesWithDigest pins the discussion §14 fix: the canonical
// total_errors from /v1/insights (windowed scan) equals the per-run digest's
// error_count (incremental fold) over the same run — same union, both ends.
func TestInsightsReconcilesWithDigest(t *testing.T) {
	c := newE2E(t)
	agentID, _ := seedVectorRun(t, c)

	_, digest := c.call(http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/agents/%s/digest", defaultTeamID, agentID), nil)
	digestErrors := int64(digest["error_count"].(float64))

	// Wide window so the whole run is in scope.
	status, ins := c.call(http.MethodGet, fmt.Sprintf(
		"/v1/insights?agent_id=%s&since=2000-01-01T00:00:00Z&until=2100-01-01T00:00:00Z",
		agentID), nil)
	if status != http.StatusOK {
		t.Fatalf("insights status %d: %v", status, ins)
	}
	errs := ins["errors"].(map[string]any)
	insTotal := int64(errs["total_errors"].(float64))

	if insTotal != digestErrors {
		t.Errorf("insights total_errors=%d, digest error_count=%d (must reconcile)", insTotal, digestErrors)
	}
	if digestErrors != 3 {
		t.Errorf("error_count=%d, want 3 (vector canonical union)", digestErrors)
	}
}

func getEventRows(t *testing.T, c *e2eCtx, path string) []map[string]any {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.srv.URL+path, nil)
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
	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	return rows
}

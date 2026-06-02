package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// getTurnRows GETs a turns-listing endpoint and returns the `turns` array.
func getTurnRows(t *testing.T, c *e2eCtx, path string) []map[string]any {
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
	var env struct {
		Turns []map[string]any `json:"turns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode turns: %v", err)
	}
	return env.Turns
}

// TestAgentTurnsEndpoint_ListsBackfilledTurns verifies the turn index lists the
// canonical vector's two turns (lazy-backfilled on read), in idx order, with
// the start_seq anchors the mobile loader jumps to.
func TestAgentTurnsEndpoint_ListsBackfilledTurns(t *testing.T) {
	c := newE2E(t)
	agentID, _ := seedVectorRun(t, c)

	turns := getTurnRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/turns", defaultTeamID, agentID))
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}

	t0 := turns[0]
	if got := int(t0["idx"].(float64)); got != 0 {
		t.Errorf("turn[0].idx = %d, want 0", got)
	}
	if got := int64(t0["start_seq"].(float64)); got != 1 {
		t.Errorf("turn[0].start_seq = %d, want 1", got)
	}
	if got := int64(t0["end_seq"].(float64)); got != 6 {
		t.Errorf("turn[0].end_seq = %d, want 6", got)
	}
	if t0["status"] != "success" {
		t.Errorf("turn[0].status = %v, want success", t0["status"])
	}
	if t0["open"].(bool) {
		t.Errorf("turn[0].open = true, want false (closed turn)")
	}

	t1 := turns[1]
	if got := int64(t1["start_seq"].(float64)); got != 7 {
		t.Errorf("turn[1].start_seq = %d, want 7", got)
	}
	if got := int64(t1["error_count"].(float64)); got != 3 {
		t.Errorf("turn[1].error_count = %d, want 3", got)
	}
	if got := int64(t1["tool_failed"].(float64)); got != 1 {
		t.Errorf("turn[1].tool_failed = %d, want 1", got)
	}
}

// TestAgentTurnsEndpoint_AfterCursor verifies after=<idx> pages forward.
func TestAgentTurnsEndpoint_AfterCursor(t *testing.T) {
	c := newE2E(t)
	agentID, _ := seedVectorRun(t, c)

	turns := getTurnRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/agents/%s/turns?after=0", defaultTeamID, agentID))
	if len(turns) != 1 {
		t.Fatalf("turns after idx 0 = %d, want 1", len(turns))
	}
	if got := int(turns[0]["idx"].(float64)); got != 1 {
		t.Errorf("paged turn idx = %d, want 1", got)
	}
}

// TestSessionTurnsEndpoint_RollsUpAgents verifies the session listing returns
// the ts-ordered union of its agents' turns.
func TestSessionTurnsEndpoint_RollsUpAgents(t *testing.T) {
	c := newE2E(t)
	agentID, sessionID := seedVectorRun(t, c)
	_ = agentID

	turns := getTurnRows(t, c, fmt.Sprintf(
		"/v1/teams/%s/sessions/%s/turns", defaultTeamID, sessionID))
	if len(turns) != 2 {
		t.Fatalf("session turns = %d, want 2", len(turns))
	}
	// Ordered by start_ts ascending.
	if turns[0]["start_ts"].(string) >= turns[1]["start_ts"].(string) {
		t.Errorf("session turns not ts-ordered: %v >= %v",
			turns[0]["start_ts"], turns[1]["start_ts"])
	}
}

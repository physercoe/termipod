package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestHubStats_ResponseShape verifies the /v1/hub/stats payload exposes
// the four top-level blocks ADR-022 D2 / insights-phase-1.md W1 require:
// version, machine, db (with tables), live. We don't assert exact numbers
// (machine + db both depend on the test host), only that the shape is
// stable so the mobile renderer never NPEs on a missing key.
func TestHubStats_ResponseShape(t *testing.T) {
	c := newE2E(t)

	status, body := c.call("GET", "/v1/hub/stats", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/hub/stats = %d body=%v", status, body)
	}

	for _, key := range []string{"version", "machine", "db", "live", "uptime_seconds"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing %q key; body=%v", key, body)
		}
	}

	machine, ok := body["machine"].(map[string]any)
	if !ok {
		t.Fatalf("machine block not an object: %T", body["machine"])
	}
	for _, key := range []string{"os", "arch", "cpu_count", "mem_bytes"} {
		if _, ok := machine[key]; !ok {
			t.Errorf("machine block missing %q; got %v", key, machine)
		}
	}

	db, ok := body["db"].(map[string]any)
	if !ok {
		t.Fatalf("db block not an object: %T", body["db"])
	}
	for _, key := range []string{"size_bytes", "schema_version", "tables"} {
		if _, ok := db[key]; !ok {
			t.Errorf("db block missing %q; got %v", key, db)
		}
	}

	tables, ok := db["tables"].(map[string]any)
	if !ok {
		t.Fatalf("db.tables not an object: %T", db["tables"])
	}
	// agents row should be present even if zero — newE2E seeds the team
	// but no agents, so count(*) returns 0 cleanly.
	row, ok := tables["agents"].(map[string]any)
	if !ok {
		t.Fatalf("db.tables.agents missing; got %v", tables)
	}
	if _, ok := row["rows"]; !ok {
		t.Errorf("db.tables.agents.rows missing; got %v", row)
	}

	live, ok := body["live"].(map[string]any)
	if !ok {
		t.Fatalf("live block not an object: %T", body["live"])
	}
	for _, key := range []string{"active_agents", "open_sessions", "sse_subscribers"} {
		if _, ok := live[key]; !ok {
			t.Errorf("live block missing %q; got %v", key, live)
		}
	}
}

// TestHubStats_AuthRequired verifies the endpoint is gated by the auth
// middleware. /v1/_info is the only public endpoint per auth/token.go;
// /v1/hub/stats must reject anonymous callers.
func TestHubStats_AuthRequired(t *testing.T) {
	c := newE2E(t)

	resp, err := http.Get(c.srv.URL + "/v1/hub/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anon GET /v1/hub/stats = %d, want 401", resp.StatusCode)
	}
}

// TestHubStats_RowCountCache verifies the 30s row-count TTL: a row
// inserted between two consecutive calls must not appear in the second
// call's payload until the cache window expires. We don't sleep 30s in
// the test — instead we hand-poke the cache timestamp to simulate the
// TTL boundary.
func TestHubStats_RowCountCache(t *testing.T) {
	c := newE2E(t)

	status, body := c.call("GET", "/v1/hub/stats", nil)
	if status != 200 {
		t.Fatalf("first call = %d body=%v", status, body)
	}
	first := tableRows(t, body, "agents")

	// Insert one agent. Cache is hot; second call must still report
	// `first` rows for agents.
	if _, err := c.s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?)`,
		"agent-cache-test", c.teamID, "cache-test", "claude-code", NowUTC()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	_, body = c.call("GET", "/v1/hub/stats", nil)
	if got := tableRows(t, body, "agents"); got != first {
		t.Errorf("hot cache hit: agents.rows=%d, want %d (insert should be hidden)", got, first)
	}

	// Force the cache to look stale, then verify the new row shows up.
	hubStatsCache.mu.Lock()
	hubStatsCache.taken = time.Now().Add(-2 * hubStatsTTL)
	hubStatsCache.mu.Unlock()

	_, body = c.call("GET", "/v1/hub/stats", nil)
	if got := tableRows(t, body, "agents"); got != first+1 {
		t.Errorf("after TTL: agents.rows=%d, want %d", got, first+1)
	}
}

// TestHubStats_RelayBlock verifies the W3 throughput counters land in
// the live block once a relay has seen traffic, and stay omitted (only
// the active+dropped gauges) on a quiet hub. Active/dropped are the
// "is the relay loop alive" signal; bytes_per_sec/pairs answer "what
// is it doing right now."
func TestHubStats_RelayBlock(t *testing.T) {
	c := newE2E(t)

	// Quiet hub: no relay traffic. Active + dropped MUST be present
	// (mobile reads them unconditionally); pairs/bytes MUST be absent.
	_, body := c.call("GET", "/v1/hub/stats", nil)
	live, ok := body["live"].(map[string]any)
	if !ok {
		t.Fatalf("live block missing")
	}
	if _, ok := live["a2a_relay_active"]; !ok {
		t.Errorf("quiet hub: a2a_relay_active should be present (gauge)")
	}
	if _, ok := live["a2a_dropped_total"]; !ok {
		t.Errorf("quiet hub: a2a_dropped_total should be present (counter)")
	}
	if _, ok := live["a2a_bytes_per_sec"]; ok {
		t.Errorf("quiet hub: a2a_bytes_per_sec should be omitted; got %v", live["a2a_bytes_per_sec"])
	}
	if _, ok := live["a2a_relay_pairs"]; ok {
		t.Errorf("quiet hub: a2a_relay_pairs should be omitted; got %v", live["a2a_relay_pairs"])
	}

	// Drive the metrics directly (a real relay round-trip needs a
	// host-runner echoing /tunnel/next; tunnel_a2a_test covers that
	// integration path). We just need bytes_per_sec to be > 0 and a
	// pair to surface.
	c.s.tunnel.metrics.Record("host-gpu", "agent-w", 30_000)

	_, body = c.call("GET", "/v1/hub/stats", nil)
	live = body["live"].(map[string]any)
	if v, ok := live["a2a_bytes_per_sec"]; !ok || toInt64(v) <= 0 {
		t.Errorf("active hub: a2a_bytes_per_sec should be positive; got %v", v)
	}
	pairs, ok := live["a2a_relay_pairs"].([]any)
	if !ok || len(pairs) == 0 {
		t.Fatalf("active hub: a2a_relay_pairs should have ≥1 entry; got %v", live["a2a_relay_pairs"])
	}
	pair := pairs[0].(map[string]any)
	if pair["host"] != "host-gpu" || pair["agent"] != "agent-w" {
		t.Errorf("pair = %v, want host-gpu/agent-w", pair)
	}
	if toInt64(pair["bytes_per_sec"]) <= 0 {
		t.Errorf("pair bytes_per_sec = %v, want > 0", pair["bytes_per_sec"])
	}
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

func tableRows(t *testing.T, body map[string]any, table string) int64 {
	t.Helper()
	db, ok := body["db"].(map[string]any)
	if !ok {
		t.Fatalf("db block missing")
	}
	tables, ok := db["tables"].(map[string]any)
	if !ok {
		t.Fatalf("tables block missing")
	}
	row, ok := tables[table].(map[string]any)
	if !ok {
		t.Fatalf("table %q missing from response", table)
	}
	// JSON numbers come back as float64 through json.Unmarshal into
	// map[string]any; cast to int64 for clean comparisons.
	switch v := row["rows"].(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		t.Fatalf("rows field has unexpected type %T", row["rows"])
		return 0
	}
}

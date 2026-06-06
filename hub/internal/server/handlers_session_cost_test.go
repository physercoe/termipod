package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// TestSessionCostEndpoint_EmptySession — GET /cost on a session with
// no usage events returns 200 with TotalUSD=0 and empty maps. The chip
// self-gates on the empty shape per ADR-036 D9.
func TestSessionCostEndpoint_EmptySession(t *testing.T) {
	s, token := newA2ATestServer(t)
	ctx := context.Background()
	const sesID = "ses-empty"
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions
		   (id, team_id, title, scope_kind, status, opened_at, last_active_at)
		 VALUES (?, ?, ?, ?, 'active', ?, ?)`,
		sesID, defaultTeamID, "empty", "team", NowUTC(), NowUTC(),
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesID+"/cost", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	var got sessionCostOut
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID != sesID {
		t.Errorf("SessionID = %q want %q", got.SessionID, sesID)
	}
	if got.TotalUSD != 0 {
		t.Errorf("TotalUSD = %v want 0", got.TotalUSD)
	}
	if !got.Imputed {
		t.Error("Imputed = false; subscription disclaimer must always be true")
	}
	if got.SnapshotDate == "" {
		t.Error("SnapshotDate empty; embedded pricing must surface its date")
	}
}

// TestSessionCostEndpoint_WithUsageEvents — full integration: usage
// events on the session sum to the expected dollar amount and the
// per-model breakdown is populated.
func TestSessionCostEndpoint_WithUsageEvents(t *testing.T) {
	s, token := newA2ATestServer(t)
	const sesID = "ses-with-cost"
	const agentID = "agent-x"

	seedSessionWithAgent(t, s, defaultTeamID, sesID, agentID)
	insertUsageEvent(t, s, agentID, sesID, 1,
		`{"model":"claude-opus-4-7","input_tokens":1000,"output_tokens":500,"cache_read":2000,"cache_create":100}`)
	insertUsageEvent(t, s, agentID, sesID, 2,
		`{"model":"claude-sonnet-4-6","input_tokens":10000,"output_tokens":2000}`)
	// Non-usage event that must be ignored — guards the kind filter.
	insertEventRow(t, s, agentID, sesID, 3, "text",
		`{"model":"claude-opus-4-7","text":"hello"}`)

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesID+"/cost", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	var got sessionCostOut
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// opus: 1000*15 + 500*75 + 2000*1.5 + 100*18.75 (per 1M tokens)
	//     = 0.015 + 0.0375 + 0.003 + 0.001875 = 0.057375
	// sonnet: 10000*3 + 2000*15 (per 1M) = 0.03 + 0.03 = 0.06
	wantTotal := 0.057375 + 0.06
	if !nearFloat(got.TotalUSD, wantTotal, 1e-9) {
		t.Errorf("TotalUSD = %v want %v", got.TotalUSD, wantTotal)
	}
	if _, ok := got.Breakdown["claude-opus-4-7"]; !ok {
		t.Errorf("Breakdown missing opus key: %v", got.Breakdown)
	}
	if _, ok := got.Breakdown["claude-sonnet-4-6"]; !ok {
		t.Errorf("Breakdown missing sonnet key: %v", got.Breakdown)
	}
	if got.Tokens["claude-opus-4-7"].Input != 1000 {
		t.Errorf("Tokens[opus].Input = %v want 1000", got.Tokens["claude-opus-4-7"].Input)
	}
	if len(got.Missing) != 0 {
		t.Errorf("Missing should be empty: %v", got.Missing)
	}
}

// TestSessionCostEndpoint_404OnUnknownSession — a session id that
// doesn't belong to the caller's team returns 404, not 500.
func TestSessionCostEndpoint_404OnUnknownSession(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/does-not-exist/cost", nil)
	if status != http.StatusNotFound {
		t.Errorf("status = %d want 404", status)
	}
}

// TestSessionGetInlinesScalarCost — the parent GET /sessions/{id}
// response carries `session_cost_usd_imputed` so the chip lights up
// without an extra round-trip. nil for empty session; populated when
// usage events exist.
func TestSessionGetInlinesScalarCost(t *testing.T) {
	s, token := newA2ATestServer(t)

	const sesEmpty = "ses-empty-scalar"
	const sesCost = "ses-cost-scalar"
	const agentID = "agent-y"
	if _, err := s.db.Exec(
		`INSERT INTO sessions
		   (id, team_id, title, scope_kind, status, opened_at, last_active_at)
		 VALUES (?, ?, 'a', 'team', 'active', ?, ?)`,
		sesEmpty, defaultTeamID, NowUTC(), NowUTC(),
	); err != nil {
		t.Fatal(err)
	}

	seedSessionWithAgent(t, s, defaultTeamID, sesCost, agentID)
	insertUsageEvent(t, s, agentID, sesCost, 1,
		`{"model":"claude-opus-4-7","input_tokens":1000000}`) // 1M input opus = $15

	// Empty session: omitted (nil → omitempty drops the key).
	_, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesEmpty, nil)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	if _, present := raw["session_cost_usd_imputed"]; present {
		t.Errorf("empty session leaked session_cost_usd_imputed field: %v", raw)
	}

	// Populated session: scalar matches the rich endpoint.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/sessions/"+sesCost, nil)
	var got sessionOut
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionCostUSDImputed == nil {
		t.Fatalf("populated session missing scalar cost; body=%s", body)
	}
	if !nearFloat(*got.SessionCostUSDImputed, 15.0, 1e-9) {
		t.Errorf("scalar cost = %v want 15.0", *got.SessionCostUSDImputed)
	}
}

// --- helpers ---------------------------------------------------------

// seedSessionWithAgent inserts the agent + session rows in the order
// required by FK constraints (agents → sessions.current_agent_id).
func seedSessionWithAgent(t *testing.T, s *Server, team, sesID, agentID string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO agents (id, team_id, handle, kind, created_at)
		 VALUES (?, ?, ?, 'claude-code', ?)`,
		agentID, team, "h-"+agentID, NowUTC()); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO sessions
		   (id, team_id, title, scope_kind, current_agent_id,
		    status, opened_at, last_active_at)
		 VALUES (?, ?, 'cost test', 'team', ?, 'active', ?, ?)`,
		sesID, team, agentID, NowUTC(), NowUTC()); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func insertUsageEvent(t *testing.T, s *Server, agentID, sesID string, seq int, payload string) {
	t.Helper()
	insertEventRow(t, s, agentID, sesID, seq, "usage", payload)
}

func insertEventRow(t *testing.T, s *Server, agentID, sesID string, seq int, kind, payload string) {
	t.Helper()
	if _, err := evWForTeam(t, s, defaultTeamID).Exec(
		`INSERT INTO agent_events
		   (id, agent_id, seq, ts, kind, producer, payload_json, session_id)
		 VALUES (?, ?, ?, ?, ?, 'agent', ?, ?)`,
		"evt-"+sesID+"-"+itoaInt(seq),
		agentID, seq, NowUTC(), kind, payload, sesID,
	); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}

func nearFloat(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func itoaInt(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

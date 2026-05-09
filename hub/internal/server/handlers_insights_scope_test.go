package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// callInsightsScope is the multi-scope counterpart to callInsights:
// callers pass any of project_id / team_id / agent_id / engine /
// host_id and an optional time window. Returns the parsed response
// (nil on non-200) and the body string (for diagnostics).
func callInsightsScope(t *testing.T, srv *Server, token string, params map[string]string, since, until time.Time) (int, *insightsResponse, string) {
	t.Helper()
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	if !since.IsZero() {
		q.Set("since", since.Format(time.RFC3339))
	}
	if !until.IsZero() {
		q.Set("until", until.Format(time.RFC3339))
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/insights?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		return rr.Code, nil, rr.Body.String()
	}
	var out insightsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rr.Code, &out, rr.Body.String()
}

// TestInsights_RequiresExactlyOneScope locks the /v1/insights contract:
// caller must specify exactly one of project_id / team_id / agent_id /
// engine / host_id. Both "none" and "two" must 400 with a clear message
// — silent fallback to "everything" would be a security footgun once
// agent/host scopes can pivot through team boundaries.
func TestInsights_RequiresExactlyOneScope(t *testing.T) {
	srv, tok, _, project, _, _ := insightsSetup(t)

	// Zero scopes: 400.
	status, _, body := callInsightsScope(t, srv, tok, map[string]string{}, time.Time{}, time.Time{})
	if status != http.StatusBadRequest {
		t.Errorf("no scope: status=%d, want 400 (body=%s)", status, body)
	}
	if !strings.Contains(body, "exactly one") {
		t.Errorf("no scope: body=%q, want hint that exactly one is required", body)
	}

	// Two scopes: also 400.
	status, _, body = callInsightsScope(t, srv, tok, map[string]string{
		"project_id": project,
		"agent_id":   "agent-x",
	}, time.Time{}, time.Time{})
	if status != http.StatusBadRequest {
		t.Errorf("two scopes: status=%d, want 400 (body=%s)", status, body)
	}
}

// TestInsights_TeamScope_AggregatesAcrossProjects seeds two projects
// under the same team, with token-bearing events on each. A team-scoped
// query must roll the spend up across both projects; a project-scoped
// query must isolate to one. This is the load-bearing case for "lift
// to all hosts" managers ask for once they have multiple projects.
func TestInsights_TeamScope_AggregatesAcrossProjects(t *testing.T) {
	srv, tok, team, projectA, agentA, sessionA := insightsSetup(t)

	// Project B in the same team — fresh agent + session so the events
	// route through scope=project_b.
	now := NowUTC()
	projectB := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, 'demo-b', ?, 'goal')`,
		projectB, team, now); err != nil {
		t.Fatalf("seed project B: %v", err)
	}
	agentB := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'steward-b', 'gemini-cli', 'running', ?)`,
		agentB, team, now); err != nil {
		t.Fatalf("seed agent B: %v", err)
	}
	sessionB := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO sessions
			(id, team_id, scope_kind, scope_id, current_agent_id,
			 status, opened_at, last_active_at)
		VALUES (?, ?, 'project', ?, ?, 'active', ?, ?)`,
		sessionB, team, projectB, agentB, now, now); err != nil {
		t.Fatalf("seed session B: %v", err)
	}

	insertEvent(t, srv, agentA, sessionA, "usage", map[string]any{
		"input_tokens": 100, "output_tokens": 10,
	})
	insertEvent(t, srv, agentB, sessionB, "usage", map[string]any{
		"input_tokens": 200, "output_tokens": 20,
	})

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	// project A only — must NOT include B.
	_, outA, body := callInsightsScope(t, srv, tok,
		map[string]string{"project_id": projectA}, since, until)
	if outA == nil {
		t.Fatalf("project A query failed: %s", body)
	}
	if outA.Spend.TokensIn != 100 {
		t.Errorf("project A tokens_in=%d, want 100 (body=%s)", outA.Spend.TokensIn, body)
	}

	// team scope — must include both.
	_, outT, body := callInsightsScope(t, srv, tok,
		map[string]string{"team_id": team}, since, until)
	if outT == nil {
		t.Fatalf("team query failed: %s", body)
	}
	if outT.Spend.TokensIn != 300 {
		t.Errorf("team tokens_in=%d, want 300 (100+200) (body=%s)", outT.Spend.TokensIn, body)
	}
	if outT.Scope.Kind != "team" || outT.Scope.ID != team {
		t.Errorf("team scope echo = %+v, want team/%s", outT.Scope, team)
	}
}

// TestInsights_AgentScope_OnlyIncludesOneAgent locks single-agent
// drilldown. Agent-scoped queries are how the future Agent Detail tab
// (Phase 2 W4) will read cost-per-agent.
func TestInsights_AgentScope_OnlyIncludesOneAgent(t *testing.T) {
	srv, tok, team, project, agentA, session := insightsSetup(t)

	// Second agent in the same project, separate session row.
	now := NowUTC()
	agentB := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO agents (id, team_id, handle, kind, status, created_at)
		VALUES (?, ?, 'worker', 'codex', 'running', ?)`,
		agentB, team, now); err != nil {
		t.Fatalf("seed agent B: %v", err)
	}
	sessionB := NewID()
	if _, err := srv.db.Exec(`
		INSERT INTO sessions
			(id, team_id, scope_kind, scope_id, current_agent_id,
			 status, opened_at, last_active_at)
		VALUES (?, ?, 'project', ?, ?, 'active', ?, ?)`,
		sessionB, team, project, agentB, now, now); err != nil {
		t.Fatalf("seed session B: %v", err)
	}

	insertEvent(t, srv, agentA, session, "usage", map[string]any{
		"input_tokens": 500,
	})
	insertEvent(t, srv, agentB, sessionB, "usage", map[string]any{
		"input_tokens": 700,
	})

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()
	_, out, body := callInsightsScope(t, srv, tok,
		map[string]string{"agent_id": agentB}, since, until)
	if out == nil {
		t.Fatalf("agent query failed: %s", body)
	}
	if out.Spend.TokensIn != 700 {
		t.Errorf("agent B tokens_in=%d, want 700 (body=%s)", out.Spend.TokensIn, body)
	}
	if out.Scope.Kind != "agent" || out.Scope.ID != agentB {
		t.Errorf("scope echo=%+v, want agent/%s", out.Scope, agentB)
	}
}

// TestInsights_EngineScope_FiltersByAgentKind seeds two agents with
// different `agents.kind` values (the engine identifier — claude-code,
// gemini-cli, codex) and verifies engine-scoped reads sum across the
// matching subset only. Engine arbitrage drilldowns (Phase 2 W5)
// depend on this.
func TestInsights_EngineScope_FiltersByAgentKind(t *testing.T) {
	srv, tok, team, project, _, _ := insightsSetup(t)

	now := NowUTC()
	geminiAgent := NewID()
	codexAgent := NewID()
	for _, a := range []struct{ id, kind string }{
		{geminiAgent, "gemini-cli"},
		{codexAgent, "codex"},
	} {
		if _, err := srv.db.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			VALUES (?, ?, ?, ?, 'running', ?)`,
			a.id, team, "agent-"+a.kind, a.kind, now); err != nil {
			t.Fatalf("seed %s agent: %v", a.kind, err)
		}
	}
	for _, a := range []struct {
		agentID string
		input   int
	}{
		{geminiAgent, 1000},
		{codexAgent, 2000},
	} {
		sessionID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO sessions
				(id, team_id, scope_kind, scope_id, current_agent_id,
				 status, opened_at, last_active_at)
			VALUES (?, ?, 'project', ?, ?, 'active', ?, ?)`,
			sessionID, team, project, a.agentID, now, now); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		insertEvent(t, srv, a.agentID, sessionID, "usage", map[string]any{
			"input_tokens": a.input,
		})
	}

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	_, out, body := callInsightsScope(t, srv, tok,
		map[string]string{"engine": "gemini-cli"}, since, until)
	if out == nil {
		t.Fatalf("engine query failed: %s", body)
	}
	if out.Spend.TokensIn != 1000 {
		t.Errorf("engine=gemini tokens_in=%d, want 1000 (body=%s)", out.Spend.TokensIn, body)
	}

	// by_engine drilldown should still report the gemini-cli row.
	if agg, ok := out.ByEngine["gemini-cli"]; !ok || agg.TokensIn != 1000 {
		t.Errorf("by_engine[gemini-cli]=%+v, want tokens_in=1000", agg)
	}
	// codex traffic must NOT leak into the engine-scoped response.
	if agg, ok := out.ByEngine["codex"]; ok && agg.TokensIn > 0 {
		t.Errorf("by_engine[codex]=%+v, want absent or zero in gemini-cli scope", agg)
	}
}

// TestInsights_HostScope_FiltersByAgentHost wires a host_id onto an
// agent and asserts the host-scoped read sees only its events. This
// pivots through agents.host_id rather than the events table directly.
func TestInsights_HostScope_FiltersByAgentHost(t *testing.T) {
	srv, tok, team, project, _, _ := insightsSetup(t)

	now := NowUTC()
	hostA := NewID()
	hostB := NewID()
	for i, h := range []string{hostA, hostB} {
		if _, err := srv.db.Exec(`
			INSERT INTO hosts (id, team_id, name, status, created_at)
			VALUES (?, ?, ?, 'connected', ?)`,
			h, team, fmt.Sprintf("host-%d", i), now); err != nil {
			t.Fatalf("seed host %s: %v", h, err)
		}
	}
	for _, h := range []struct {
		hostID string
		tokens int
	}{
		{hostA, 333},
		{hostB, 777},
	} {
		agentID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, host_id, created_at)
			VALUES (?, ?, ?, 'claude-code', 'running', ?, ?)`,
			agentID, team, "agent-"+h.hostID, h.hostID, now); err != nil {
			t.Fatalf("seed agent: %v", err)
		}
		sessionID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO sessions
				(id, team_id, scope_kind, scope_id, current_agent_id,
				 status, opened_at, last_active_at)
			VALUES (?, ?, 'project', ?, ?, 'active', ?, ?)`,
			sessionID, team, project, agentID, now, now); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		insertEvent(t, srv, agentID, sessionID, "usage", map[string]any{
			"input_tokens": h.tokens,
		})
	}

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	_, out, body := callInsightsScope(t, srv, tok,
		map[string]string{"host_id": hostA}, since, until)
	if out == nil {
		t.Fatalf("host query failed: %s", body)
	}
	if out.Spend.TokensIn != 333 {
		t.Errorf("host A tokens_in=%d, want 333 (body=%s)", out.Spend.TokensIn, body)
	}

	_, outB, body := callInsightsScope(t, srv, tok,
		map[string]string{"host_id": hostB}, since, until)
	if outB == nil {
		t.Fatalf("host B query failed: %s", body)
	}
	if outB.Spend.TokensIn != 777 {
		t.Errorf("host B tokens_in=%d, want 777 (body=%s)", outB.Spend.TokensIn, body)
	}
}

// TestInsights_ScopeCacheKeysIsolate verifies the response cache keys
// fold scope kind into their prefix — so a project_id="abc" read can't
// shadow an agent_id="abc" read even when ids collide. Real ULIDs
// don't collide across kinds, but a cache-key bug would still mask
// real data.
func TestInsights_ScopeCacheKeysIsolate(t *testing.T) {
	srv, tok, _, project, _, _ := insightsSetup(t)
	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	// Prime the cache with a project read.
	_, outProject, body := callInsightsScope(t, srv, tok,
		map[string]string{"project_id": project}, since, until)
	if outProject == nil {
		t.Fatalf("project query failed: %s", body)
	}

	// Now read with the same id but as agent_id. There's no agent with
	// this id (project ids are ULIDs not in agents.id), so the response
	// should reflect the agent-scope (zero tokens), not the cached
	// project response.
	_, outAgent, body := callInsightsScope(t, srv, tok,
		map[string]string{"agent_id": project}, since, until)
	if outAgent == nil {
		t.Fatalf("agent query failed: %s", body)
	}
	if outAgent.Scope.Kind != "agent" {
		t.Errorf("scope echo=%+v, want agent (cache may be leaking)", outAgent.Scope)
	}
}

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

// TestInsights_ToolsBlock_AggregatesToolCallsAndApprovals seeds three
// tool_call rows + three turn.result rows + two resolved
// approval_requests (one approved, one rejected) and verifies the
// W5c (insights-phase-2) tools block surfaces:
//   - tool_calls = 3
//   - tools_per_turn = 1.0
//   - approvals_total = 2
//   - approvals_approved = 1
//   - approval_rate = 0.5
func TestInsights_ToolsBlock_AggregatesToolCallsAndApprovals(t *testing.T) {
	srv, tok, _, project, agent, session := insightsSetup(t)

	for i := 0; i < 3; i++ {
		insertEvent(t, srv, agent, session, "tool_call", map[string]any{
			"name": fmt.Sprintf("Tool%d", i),
		})
		insertEvent(t, srv, agent, session, "turn.result", map[string]any{
			"status":      "success",
			"duration_ms": 100,
		})
	}

	// Two resolved approval_requests in the same project — one
	// approved, one rejected. Mirrors handlers_attention.go's append
	// of one decision row on resolution.
	now := NowUTC()
	for _, c := range []struct {
		id, decision string
	}{
		{NewID(), "approve"},
		{NewID(), "reject"},
	} {
		decisions := fmt.Sprintf(`[{"decision":%q,"actor":"test","ts":%q}]`,
			c.decision, now)
		if _, err := srv.db.Exec(`
			INSERT INTO attention_items
				(id, project_id, scope_kind, scope_id, kind, summary,
				 current_assignees_json, decisions_json, status,
				 created_at, session_id)
			VALUES (?, ?, 'project', ?, 'approval_request', 'test',
				'[]', ?, 'resolved', ?, ?)`,
			c.id, project, project, decisions, now, session); err != nil {
			t.Fatalf("seed attention %s: %v", c.id, err)
		}
	}

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()
	_, out, body := callInsightsScope(t, srv, tok,
		map[string]string{"project_id": project}, since, until)
	if out == nil {
		t.Fatalf("project query failed: %s", body)
	}
	if out.Tools.ToolCalls != 3 {
		t.Errorf("tool_calls=%d, want 3 (body=%s)", out.Tools.ToolCalls, body)
	}
	if out.Tools.ToolsPerTurn != 1.0 {
		t.Errorf("tools_per_turn=%v, want 1.0", out.Tools.ToolsPerTurn)
	}
	if out.Tools.ApprovalsTotal != 2 {
		t.Errorf("approvals_total=%d, want 2", out.Tools.ApprovalsTotal)
	}
	if out.Tools.ApprovalsApproved != 1 {
		t.Errorf("approvals_approved=%d, want 1", out.Tools.ApprovalsApproved)
	}
	if out.Tools.ApprovalRate != 0.5 {
		t.Errorf("approval_rate=%v, want 0.5", out.Tools.ApprovalRate)
	}
}

// TestInsights_LifecycleBlock_PopulatedForProjectScope seeds two
// phase transitions, two deliverables (one ratified), and three
// acceptance criteria (one met, one failed, one pending) and verifies
// the W5d block reports the correct counts + ratios. Project-only —
// the block must NOT appear for team/agent/engine/host scopes.
func TestInsights_LifecycleBlock_PopulatedForProjectScope(t *testing.T) {
	srv, tok, _, project, _, _ := insightsSetup(t)

	// phase_history: empty → idea → research. The first transition is
	// "set initial phase" so duration is from t1; the second is the
	// real advance.
	t1 := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	t2 := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	historyJSON := fmt.Sprintf(`{"transitions":[
		{"from":"","to":"idea","at":%q},
		{"from":"idea","to":"research","at":%q}
	]}`, t1, t2)
	if _, err := srv.db.Exec(`
		UPDATE projects SET phase = 'research', phase_history = ? WHERE id = ?`,
		historyJSON, project); err != nil {
		t.Fatalf("seed phase history: %v", err)
	}

	now := NowUTC()
	for _, d := range []struct{ id, state string }{
		{NewID(), "ratified"},
		{NewID(), "draft"},
	} {
		if _, err := srv.db.Exec(`
			INSERT INTO deliverables
				(id, project_id, phase, kind, ratification_state, created_at)
			VALUES (?, ?, 'research', 'doc', ?, ?)`,
			d.id, project, d.state, now); err != nil {
			t.Fatalf("seed deliverable %s: %v", d.id, err)
		}
	}
	for _, c := range []struct{ id, state string }{
		{NewID(), "met"},
		{NewID(), "failed"},
		{NewID(), "pending"},
	} {
		if _, err := srv.db.Exec(`
			INSERT INTO acceptance_criteria
				(id, project_id, phase, kind, body, state, created_at)
			VALUES (?, ?, 'research', 'text', ?, ?, ?)`,
			c.id, project, "Crit "+c.id, c.state, now); err != nil {
			t.Fatalf("seed criterion %s: %v", c.id, err)
		}
	}

	since := time.Now().Add(-3 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	// Project scope — lifecycle must be present and populated.
	_, out, body := callInsightsScope(t, srv, tok,
		map[string]string{"project_id": project}, since, until)
	if out == nil {
		t.Fatalf("project query failed: %s", body)
	}
	lc := out.Lifecycle
	if lc == nil {
		t.Fatalf("lifecycle block missing for project scope (body=%s)", body)
	}
	if lc.CurrentPhase != "research" {
		t.Errorf("current_phase=%q, want research", lc.CurrentPhase)
	}
	if len(lc.Phases) != 2 {
		t.Errorf("phases len=%d, want 2 (entries=%+v)", len(lc.Phases), lc.Phases)
	}
	if lc.DeliverablesTotal != 2 || lc.DeliverablesRatified != 1 {
		t.Errorf("deliverables total=%d ratified=%d, want 2/1",
			lc.DeliverablesTotal, lc.DeliverablesRatified)
	}
	if lc.RatificationRate != 0.5 {
		t.Errorf("ratification_rate=%v, want 0.5", lc.RatificationRate)
	}
	if lc.CriteriaTotal != 3 || lc.CriteriaMet != 1 || lc.StuckCount != 1 {
		t.Errorf("criteria total=%d met=%d stuck=%d, want 3/1/1",
			lc.CriteriaTotal, lc.CriteriaMet, lc.StuckCount)
	}

	// Team scope — lifecycle must be omitted.
	_, outTeam, body := callInsightsScope(t, srv, tok,
		map[string]string{"team_id": "insights-test"}, since, until)
	if outTeam == nil {
		t.Fatalf("team query failed: %s", body)
	}
	if outTeam.Lifecycle != nil {
		t.Errorf("lifecycle present on team scope: %+v", outTeam.Lifecycle)
	}
}

// resetInsightsCache wipes the package-level response cache. Several
// tests in this file seed `team_id=insights-test` then query with
// `time.Now().Add(-1 * time.Hour)`-style windows; the cache key folds
// in (scope_kind, scope_id, since, until) and on systems with
// microsecond-resolution clocks the windows can collide between
// adjacent tests, leaking the previous test's body into the next
// one's response. Call this at the top of any test that issues
// scope-parameterized reads.
func resetInsightsCache() {
	hubInsightsCache.mu.Lock()
	hubInsightsCache.entries = map[string]insightsCacheEntry{}
	hubInsightsCache.mu.Unlock()
}

// TestInsights_TeamStewards_FiltersToStewardHandles seeds three
// agents on one team — a steward (handle `steward`), a domain steward
// (handle `research-steward`), and a worker (handle `worker`). Each
// emits a usage event. A team-scoped read must see all three; a
// team+kind=steward read must see only the two stewards. The general
// steward (`@steward`) is also included in the predicate, asserted by
// a fourth agent.
func TestInsights_TeamStewards_FiltersToStewardHandles(t *testing.T) {
	resetInsightsCache()
	srv, tok, team, project, agentDefault, sessionDefault := insightsSetup(t)
	now := NowUTC()

	type seedAgent struct {
		handle string
		tokens int
	}
	// agentDefault is already 'steward' from insightsSetup; seed three
	// more to round out the matrix.
	extras := []seedAgent{
		{"@steward", 50},          // general singleton
		{"research-steward", 100}, // domain steward
		{"worker", 999},           // non-steward
	}
	for _, a := range extras {
		agentID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			VALUES (?, ?, ?, 'claude-code', 'running', ?)`,
			agentID, team, a.handle, now); err != nil {
			t.Fatalf("seed agent %s: %v", a.handle, err)
		}
		sessionID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO sessions
				(id, team_id, scope_kind, scope_id, current_agent_id,
				 status, opened_at, last_active_at)
			VALUES (?, ?, 'project', ?, ?, 'active', ?, ?)`,
			sessionID, team, project, agentID, now, now); err != nil {
			t.Fatalf("seed session %s: %v", a.handle, err)
		}
		insertEvent(t, srv, agentID, sessionID, "usage", map[string]any{
			"input_tokens": a.tokens,
		})
	}
	insertEvent(t, srv, agentDefault, sessionDefault, "usage", map[string]any{
		"input_tokens": 25,
	})

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	// team scope (no kind) — sums all four: 25 + 50 + 100 + 999 = 1174.
	_, outTeam, body := callInsightsScope(t, srv, tok,
		map[string]string{"team_id": team}, since, until)
	if outTeam == nil {
		t.Fatalf("team query failed: %s", body)
	}
	if outTeam.Spend.TokensIn != 1174 {
		t.Errorf("team tokens_in=%d, want 1174 (body=%s)", outTeam.Spend.TokensIn, body)
	}
	if outTeam.Scope.Kind != "team" {
		t.Errorf("team scope.kind=%q, want team", outTeam.Scope.Kind)
	}

	// team + kind=steward — sums only stewards: 25 + 50 + 100 = 175.
	_, outStewards, body := callInsightsScope(t, srv, tok,
		map[string]string{"team_id": team, "kind": "steward"}, since, until)
	if outStewards == nil {
		t.Fatalf("team_stewards query failed: %s", body)
	}
	if outStewards.Spend.TokensIn != 175 {
		t.Errorf("team_stewards tokens_in=%d, want 175 (body=%s)",
			outStewards.Spend.TokensIn, body)
	}
	if outStewards.Scope.Kind != "team_stewards" {
		t.Errorf("scope.kind=%q, want team_stewards", outStewards.Scope.Kind)
	}
}

// TestInsights_ByAgent_PopulatesAndSortsByTokensIn seeds three agents
// on one project, each with different token counts, and verifies:
//   - by_agent is present and has 3 rows
//   - rows are sorted by tokens_in desc
//   - handle + engine + status are populated from the agents JOIN
//   - by_agent is omitted on agent scope (degenerate)
func TestInsights_ByAgent_PopulatesAndSortsByTokensIn(t *testing.T) {
	resetInsightsCache()
	srv, tok, team, project, agentBase, sessionBase := insightsSetup(t)
	now := NowUTC()

	insertEvent(t, srv, agentBase, sessionBase, "usage", map[string]any{
		"input_tokens": 100,
	})

	// Two more agents with different token counts so we can verify ordering.
	extras := []struct {
		handle string
		kind   string
		tokens int
	}{
		{"alpha-steward", "gemini-cli", 500},
		{"worker", "codex", 50},
	}
	for _, e := range extras {
		agentID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO agents (id, team_id, handle, kind, status, created_at)
			VALUES (?, ?, ?, ?, 'running', ?)`,
			agentID, team, e.handle, e.kind, now); err != nil {
			t.Fatalf("seed agent %s: %v", e.handle, err)
		}
		sessionID := NewID()
		if _, err := srv.db.Exec(`
			INSERT INTO sessions
				(id, team_id, scope_kind, scope_id, current_agent_id,
				 status, opened_at, last_active_at)
			VALUES (?, ?, 'project', ?, ?, 'active', ?, ?)`,
			sessionID, team, project, agentID, now, now); err != nil {
			t.Fatalf("seed session %s: %v", e.handle, err)
		}
		insertEvent(t, srv, agentID, sessionID, "usage", map[string]any{
			"input_tokens": e.tokens,
		})
	}

	since := time.Now().Add(-1 * time.Hour).UTC()
	until := time.Now().Add(1 * time.Hour).UTC()

	_, out, body := callInsightsScope(t, srv, tok,
		map[string]string{"project_id": project}, since, until)
	if out == nil {
		t.Fatalf("project query failed: %s", body)
	}
	if len(out.ByAgent) != 3 {
		t.Fatalf("by_agent len=%d, want 3 (body=%s)", len(out.ByAgent), body)
	}
	// Sort: alpha-steward (500) > base steward (100) > worker (50).
	if out.ByAgent[0].Handle != "alpha-steward" || out.ByAgent[0].TokensIn != 500 {
		t.Errorf("by_agent[0]=%+v, want alpha-steward/500", out.ByAgent[0])
	}
	if out.ByAgent[2].Handle != "worker" || out.ByAgent[2].TokensIn != 50 {
		t.Errorf("by_agent[2]=%+v, want worker/50", out.ByAgent[2])
	}
	// Engine + status pulled from agents JOIN.
	if out.ByAgent[0].Engine != "gemini-cli" || out.ByAgent[0].Status != "running" {
		t.Errorf("by_agent[0] engine/status=%q/%q, want gemini-cli/running",
			out.ByAgent[0].Engine, out.ByAgent[0].Status)
	}

	// Agent scope — by_agent must be absent (omitempty drops the field).
	_, outAgent, body := callInsightsScope(t, srv, tok,
		map[string]string{"agent_id": agentBase}, since, until)
	if outAgent == nil {
		t.Fatalf("agent query failed: %s", body)
	}
	if len(outAgent.ByAgent) != 0 {
		t.Errorf("by_agent on agent scope=%+v, want empty/absent", outAgent.ByAgent)
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

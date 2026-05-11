package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// /v1/insights scope-parameterized aggregator (ADR-022 D3,
// insights-phase-1 W2 + insights-phase-2 W1). Returns the Tier-1
// dimensions — spend / latency / errors / concurrency — summed across
// `agent_events` filtered by the requested scope (project / team /
// agent / engine / host) and the optional time range. Caches the
// response with a 30s TTL keyed on the (scope_kind, scope_id, since,
// until) tuple so a Project Detail screen refreshing on tab-switch
// doesn't re-scan agent_events every time. The scope filter SQL lives
// in insights_scope.go.

type insightsCacheEntry struct {
	taken time.Time
	body  []byte
}

type insightsCache struct {
	mu      sync.Mutex
	entries map[string]insightsCacheEntry
}

const insightsTTL = 30 * time.Second

// hubInsightsCache is package-level so it survives reuse across requests.
// We don't bound the entry count: project_id × since × until is small at
// MVP scale (one project tile fixed at "last 24h", one drilldown at
// "last 7d") so the worst case is a few entries per active project.
var hubInsightsCache = &insightsCache{entries: map[string]insightsCacheEntry{}}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope, err := parseInsightsScope(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// since/until are RFC3339; absent since defaults to 24h ago, absent
	// until defaults to now. Parse leniently — clamp to a sane window
	// rather than 400-ing on a stray timezone-less string, since this
	// endpoint is read by mobile widgets that build the timestamps
	// themselves.
	now := time.Now().UTC()
	until := now
	if v := strings.TrimSpace(q.Get("until")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = t.UTC()
		}
	}
	since := until.Add(-24 * time.Hour)
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t.UTC()
		}
	}
	if !since.Before(until) {
		writeErr(w, http.StatusBadRequest, "since must be before until")
		return
	}

	// Cache key folds the scope kind into the prefix so a
	// project-scoped read never collides with an agent-scoped read that
	// happens to share an id space.
	cacheKey := scope.Kind + ":" + scope.ID + "|" + since.Format(time.RFC3339Nano) + "|" + until.Format(time.RFC3339Nano)

	hubInsightsCache.mu.Lock()
	if entry, ok := hubInsightsCache.entries[cacheKey]; ok && time.Since(entry.taken) < insightsTTL {
		body := entry.body
		hubInsightsCache.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	hubInsightsCache.mu.Unlock()

	out, err := buildInsightsResponse(r.Context(), s.db, scope, since, until)
	if err == nil &&
		(scope.Kind == "team" || scope.Kind == "team_stewards") {
		// project-overview-attention-redesign W3 — cross-project rollup
		// only makes sense on team-scope; bare function in
		// buildInsightsResponse can't reach the template registry so we
		// fold this in as a Server-method tail step.
		err = s.fillInsightsByProject(r.Context(), scope, out)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	body, err := json.Marshal(out)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	hubInsightsCache.mu.Lock()
	hubInsightsCache.entries[cacheKey] = insightsCacheEntry{taken: time.Now(), body: body}
	hubInsightsCache.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// insightsResponse is the on-the-wire Tier-1 shape from
// docs/plans/insights-phase-1.md §3 W2. Mobile renders one tile per
// top-level group; by_engine / by_model are the drilldown lists.
type insightsResponse struct {
	Scope       insightsScope          `json:"scope"`
	Spend       insightsSpend          `json:"spend"`
	Latency     insightsLatency        `json:"latency"`
	Errors      insightsErrors         `json:"errors"`
	Concurrency insightsConcurrency    `json:"concurrency"`
	Tools       insightsTools          `json:"tools"`
	// Lifecycle is populated only for project scope (W5d
	// insights-phase-2). The other scopes have no project to
	// resolve `phase_history` / `deliverables` / `acceptance_criteria`
	// against, so the field is omitted via the pointer-+-omitempty.
	Lifecycle *insightsLifecycle     `json:"lifecycle,omitempty"`
	ByEngine    map[string]insightsAgg `json:"by_engine"`
	ByModel     map[string]insightsAgg `json:"by_model"`
	// ByAgent is populated whenever the scope can plausibly hold more
	// than one agent — project / team / team_stewards / engine / host.
	// Skipped (nil → JSON `null` via omitempty) on agent scope, where
	// the breakdown is degenerate. Sorted by tokens_in desc so the
	// highest-spend row renders first.
	ByAgent []insightsAgentAgg `json:"by_agent,omitempty"`
	// ByProject is populated only on team / team_stewards scope. One
	// row per goal-kind, non-archived project in the team. Sorted by
	// last_activity desc, hard-capped at 100 rows. Workspaces
	// (kind='standing') are filtered out — they have no phase or
	// progress and need their own post-MVP surface.
	// See plan `project-overview-attention-redesign.md` W3.
	ByProject []insightsProjectAgg `json:"by_project,omitempty"`
}

// insightsProjectAgg is one row of the team-scope project breakdown.
// Powers the cross-project overview surface (Projects-list AppBar
// Insights icon → TeamOverviewInsightsScreen).
//
// `progress` follows the weighted formula resolved 2026-05-11 (plan
// open-question Q2 (c)):
//
//	progress = (phases_done + current_phase_AC_ratio) / phases_total
//
// `phases_done` = count of transitions in `projects.phase_history`.
// `current_phase_AC_ratio` = met / (pending+met+failed+waived) on the
// current phase only. `phases_total` = count of `phase_specs` entries
// in the template YAML.
//
// `open_criteria` counts ACs in state IN ('pending', 'failed') across
// *all* phases of the project, not just the current one — the director
// asked "what's outstanding here" reads project-wide.
//
// `last_activity` is the latest `agent_events.created_at` for the
// project. Empty string when the project has no events yet.
type insightsProjectAgg struct {
	ProjectID     string  `json:"project_id"`
	Name          string  `json:"name"`
	CurrentPhase  string  `json:"current_phase"`
	Status        string  `json:"status"`
	Progress      float64 `json:"progress"`
	OpenAttention int     `json:"open_attention"`
	OpenCriteria  int     `json:"open_criteria"`
	LastActivity  string  `json:"last_activity"`
}

// insightsAgentAgg is one row of the per-agent breakdown. handle +
// engine + status come from the agents table at materialization time;
// token / turn counts are folded in the spend loop.
type insightsAgentAgg struct {
	AgentID     string `json:"agent_id"`
	Handle      string `json:"handle"`
	Engine      string `json:"engine"`
	Status      string `json:"status"`
	TokensIn    int64  `json:"tokens_in"`
	TokensOut   int64  `json:"tokens_out"`
	CacheRead   int64  `json:"cache_read"`
	CacheCreate int64  `json:"cache_create"`
	Turns       int64  `json:"turns"`
	Errors      int64  `json:"errors"`
}

// insightsLifecycle is the W5d project-lifecycle rollup. Each phase
// gets a (phase, entered_at, duration_s) row so the mobile renderer
// can show a horizontal timeline. The trailing phase's duration is
// "open-ended" — measured to now() — so the user sees how long the
// project has been parked in its current phase.
//
// Deliverables and criteria counts answer the "is this thing
// finishable" question. Stuck count = criteria stuck in 'failed'
// state — actionable item that the steward should clear.
type insightsLifecycle struct {
	CurrentPhase         string          `json:"current_phase"`
	Phases               []phaseTimespan `json:"phases"`
	DeliverablesTotal    int64           `json:"deliverables_total"`
	DeliverablesRatified int64           `json:"deliverables_ratified"`
	RatificationRate     float64         `json:"ratification_rate"`
	CriteriaTotal        int64           `json:"criteria_total"`
	CriteriaMet          int64           `json:"criteria_met"`
	CriterionPassRate    float64         `json:"criterion_pass_rate"`
	StuckCount           int64           `json:"stuck_count"`
}

type phaseTimespan struct {
	Phase     string `json:"phase"`
	EnteredAt string `json:"entered_at"`
	DurationS int64  `json:"duration_s"`
}

// insightsTools is the W5c (insights-phase-2) tool-call efficiency
// rollup. tool_calls = total `agent_events.kind='tool_call'` rows in
// the scope; tools_per_turn = tool_calls / turn_count; approvals
// counts come from attention_items where kind='approval_request'.
type insightsTools struct {
	ToolCalls         int64   `json:"tool_calls"`
	ToolsPerTurn      float64 `json:"tools_per_turn"`
	ApprovalsTotal    int64   `json:"approvals_total"`
	ApprovalsApproved int64   `json:"approvals_approved"`
	ApprovalRate      float64 `json:"approval_rate"`
}

type insightsScope struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Since string `json:"since"`
	Until string `json:"until"`
}

type insightsSpend struct {
	TokensIn    int64 `json:"tokens_in"`
	TokensOut   int64 `json:"tokens_out"`
	CacheRead   int64 `json:"cache_read"`
	CacheCreate int64 `json:"cache_create"`
}

type insightsLatency struct {
	TurnP50Ms int64 `json:"turn_p50_ms"`
	TurnP95Ms int64 `json:"turn_p95_ms"`
	Samples   int64 `json:"samples"`
}

type insightsErrors struct {
	FailedTurns       int64 `json:"failed_turns"`
	DriverDisconnects int64 `json:"driver_disconnects"`
	OpenAttention     int64 `json:"open_attention"`
}

type insightsConcurrency struct {
	ActiveAgents int64   `json:"active_agents"`
	OpenSessions int64   `json:"open_sessions"`
	TurnsPerMin  float64 `json:"turns_per_min"`
}

type insightsAgg struct {
	TokensIn    int64 `json:"tokens_in"`
	TokensOut   int64 `json:"tokens_out"`
	CacheRead   int64 `json:"cache_read"`
	CacheCreate int64 `json:"cache_create"`
	Turns       int64 `json:"turns"`
}

func buildInsightsResponse(ctx context.Context, db *sql.DB, scope *scopeFilter, since, until time.Time) (*insightsResponse, error) {
	out := &insightsResponse{
		Scope: insightsScope{
			Kind:  scope.Kind,
			ID:    scope.ID,
			Since: since.Format(time.RFC3339),
			Until: until.Format(time.RFC3339),
		},
		ByEngine: map[string]insightsAgg{},
		ByModel:  map[string]insightsAgg{},
	}

	if err := readInsightsSpendAndLatency(ctx, db, scope, since, until, out); err != nil {
		return nil, err
	}
	if err := readInsightsErrors(ctx, db, scope, since, until, out); err != nil {
		return nil, err
	}
	if err := readInsightsConcurrency(ctx, db, scope, since, until, out); err != nil {
		return nil, err
	}
	if err := readInsightsTools(ctx, db, scope, since, until, out); err != nil {
		return nil, err
	}
	if err := readInsightsLifecycle(ctx, db, scope, out); err != nil {
		return nil, err
	}
	return out, nil
}

// readInsightsSpendAndLatency walks every spend-bearing event in the
// window (kind in usage / turn.result) and folds the canonical token
// fields into spend, by_engine, by_model. Latency comes from the
// turn.result.duration_ms values pulled into a slice for in-process
// p50/p95.
//
// The query pulls payload_json + producer rather than aggregating in
// SQL because (a) by_model is a JSON map whose key set we can't predict
// in SQL without a recursive CTE, and (b) p50/p95 isn't built into
// SQLite. At MVP scale (project-day rowcount well under 10k usually)
// the in-process fold is faster than scaffolding both.
func readInsightsSpendAndLatency(
	ctx context.Context, db *sql.DB,
	scope *scopeFilter, since, until time.Time,
	out *insightsResponse,
) error {
	args := append([]any{}, scope.EventsArgs...)
	args = append(args, since.Format(time.RFC3339), until.Format(time.RFC3339))
	rows, err := db.QueryContext(ctx, `
		SELECT kind, payload_json, agent_id
		  FROM agent_events
		 WHERE `+scope.EventsClause+`
		   AND ts >= ? AND ts < ?
		   AND kind IN ('usage','turn.result')`,
		args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	durations := make([]int64, 0, 64)
	agentEngines := map[string]string{}
	// Per-agent fold for by_agent. Skipped on agent scope (single-row
	// view, breakdown is degenerate). The map is materialized into
	// out.ByAgent after the loop with one agents-table JOIN per id.
	wantByAgent := scope.Kind != "agent"
	byAgent := map[string]*insightsAgentAgg{}
	for rows.Next() {
		var kind, payloadJSON, agentID string
		if err := rows.Scan(&kind, &payloadJSON, &agentID); err != nil {
			return err
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
			continue
		}

		// Engine resolution is per-agent; cache lookups across the loop.
		// agents.kind carries the engine identifier (claude-code,
		// gemini-cli, codex) — there's no separate engine column; see
		// detectContextMutation in context_mutation.go for the same
		// pattern.
		engine, ok := agentEngines[agentID]
		if !ok {
			_ = db.QueryRowContext(ctx,
				`SELECT kind FROM agents WHERE id = ?`, agentID,
			).Scan(&engine)
			agentEngines[agentID] = engine
		}

		// Top-level token sums. claude-sdk uses input_tokens/output_tokens;
		// codex+gemini turn.result lifts the same names. cache_read /
		// cache_create are present on claude only; turn.result aggregates
		// don't surface them at the top level (only inside by_model).
		in := readNumber(p, "input_tokens")
		outTok := readNumber(p, "output_tokens")
		cacheRead := readNumber(p, "cache_read")
		cacheCreate := readNumber(p, "cache_create")

		out.Spend.TokensIn += in
		out.Spend.TokensOut += outTok
		out.Spend.CacheRead += cacheRead
		out.Spend.CacheCreate += cacheCreate

		isTurn := kind == "turn.result"
		if isTurn {
			if d := readNumber(p, "duration_ms"); d > 0 {
				durations = append(durations, d)
			}
		}

		// model — claude-sdk usage events name a single model per event;
		// turn.result top-level may not. by_model is the authoritative
		// per-model breakdown when present.
		if model, ok := p["model"].(string); ok && model != "" && in+outTok+cacheRead+cacheCreate > 0 {
			agg := out.ByModel[model]
			agg.TokensIn += in
			agg.TokensOut += outTok
			agg.CacheRead += cacheRead
			agg.CacheCreate += cacheCreate
			if isTurn {
				agg.Turns++
			}
			out.ByModel[model] = agg
		}
		if bm, ok := p["by_model"].(map[string]any); ok {
			for name, raw := range bm {
				entry, _ := raw.(map[string]any)
				if entry == nil {
					continue
				}
				agg := out.ByModel[name]
				mIn := readNumber(entry, "input")
				mOut := readNumber(entry, "output")
				mCacheR := readNumber(entry, "cache_read")
				mCacheC := readNumber(entry, "cache_create")
				agg.TokensIn += mIn
				agg.TokensOut += mOut
				agg.CacheRead += mCacheR
				agg.CacheCreate += mCacheC
				if isTurn {
					agg.Turns++
				}
				out.ByModel[name] = agg
			}
		}

		// Engine rollup keyed by the agent's engine field (claude-code,
		// gemini-cli, codex). Empty engine drops into "unknown" so we
		// never silently lose token-bearing events.
		engineKey := engine
		if engineKey == "" {
			engineKey = "unknown"
		}
		eAgg := out.ByEngine[engineKey]
		eAgg.TokensIn += in
		eAgg.TokensOut += outTok
		eAgg.CacheRead += cacheRead
		eAgg.CacheCreate += cacheCreate
		if isTurn {
			eAgg.Turns++
		}
		out.ByEngine[engineKey] = eAgg

		// Per-agent fold. Status / handle come from the agents table at
		// materialization; here we just accumulate token + turn counters.
		if wantByAgent && agentID != "" {
			aAgg, ok := byAgent[agentID]
			if !ok {
				aAgg = &insightsAgentAgg{AgentID: agentID, Engine: engineKey}
				byAgent[agentID] = aAgg
			}
			aAgg.TokensIn += in
			aAgg.TokensOut += outTok
			aAgg.CacheRead += cacheRead
			aAgg.CacheCreate += cacheCreate
			if isTurn {
				aAgg.Turns++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	out.Latency.Samples = int64(len(durations))
	out.Latency.TurnP50Ms = percentile(durations, 0.50)
	out.Latency.TurnP95Ms = percentile(durations, 0.95)

	// Materialize by_agent. Pull handle + status for each accumulated
	// agent_id; sort by tokens_in desc so the highest spender is first.
	if wantByAgent && len(byAgent) > 0 {
		for id, agg := range byAgent {
			var handle, status string
			_ = db.QueryRowContext(ctx,
				`SELECT COALESCE(handle, ''), COALESCE(status, '')
				   FROM agents WHERE id = ?`, id,
			).Scan(&handle, &status)
			agg.Handle = handle
			agg.Status = status
		}
		// Failed-turn errors per agent — single grouped query rather than
		// per-row roundtrip. Filter clause mirrors the spend window so the
		// counts are aligned to the same scope/range.
		errArgs := append([]any{}, scope.EventsArgs...)
		errArgs = append(errArgs, since.Format(time.RFC3339), until.Format(time.RFC3339))
		errRows, err := db.QueryContext(ctx, `
			SELECT agent_id, count(*) FROM agent_events
			 WHERE `+scope.EventsClause+`
			   AND ts >= ? AND ts < ?
			   AND kind = 'turn.result'
			   AND COALESCE(json_extract(payload_json, '$.status'), 'success') <> 'success'
			 GROUP BY agent_id`,
			errArgs...,
		)
		if err == nil {
			for errRows.Next() {
				var id string
				var n int64
				if scanErr := errRows.Scan(&id, &n); scanErr == nil {
					if agg, ok := byAgent[id]; ok {
						agg.Errors = n
					}
				}
			}
			errRows.Close()
		}

		out.ByAgent = make([]insightsAgentAgg, 0, len(byAgent))
		for _, agg := range byAgent {
			out.ByAgent = append(out.ByAgent, *agg)
		}
		sort.Slice(out.ByAgent, func(i, j int) bool {
			a, b := out.ByAgent[i], out.ByAgent[j]
			if a.TokensIn != b.TokensIn {
				return a.TokensIn > b.TokensIn
			}
			return a.AgentID < b.AgentID
		})
	}

	// turns_per_min from the same dataset; window in minutes can never be
	// zero because we already validated since < until. Concurrency block
	// owns the field but the rate uses the turn.result count we just
	// produced (sum of by_engine.turns).
	var totalTurns int64
	for _, e := range out.ByEngine {
		totalTurns += e.Turns
	}
	windowMin := until.Sub(since).Minutes()
	if windowMin > 0 {
		out.Concurrency.TurnsPerMin = float64(totalTurns) / windowMin
	}
	return nil
}

func readInsightsErrors(
	ctx context.Context, db *sql.DB,
	scope *scopeFilter, since, until time.Time,
	out *insightsResponse,
) error {
	// Failed turns: kind=turn.result with status != 'success'.
	failedArgs := append([]any{}, scope.EventsArgs...)
	failedArgs = append(failedArgs, since.Format(time.RFC3339), until.Format(time.RFC3339))
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM agent_events
		 WHERE `+scope.EventsClause+`
		   AND ts >= ? AND ts < ?
		   AND kind = 'turn.result'
		   AND COALESCE(json_extract(payload_json, '$.status'), 'success') <> 'success'`,
		failedArgs...,
	).Scan(&out.Errors.FailedTurns); err != nil {
		return err
	}

	// Driver disconnects: Phase 1 leaves this at 0. Hostrunner only emits
	// lifecycle/started + lifecycle/stopped today; "stopped" doesn't
	// distinguish an intentional Stop from a crash, so counting it would
	// overstate the rate. Phase 2 plan extends drivers to emit a
	// crash-typed phase before recording it here.
	out.Errors.DriverDisconnects = 0

	// Open attention items reach the scope through their session row;
	// scope.SessionsClause covers each scope kind's path (project via
	// scope_kind/scope_id, team via team_id, agent via current_agent_id,
	// engine/host via the agents subquery).
	attArgs := append([]any{}, scope.SessionsArgs...)
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM attention_items ai
		 JOIN sessions s ON s.id = ai.session_id
		 WHERE `+scope.SessionsClause+`
		   AND ai.status = 'open'`,
		attArgs...,
	).Scan(&out.Errors.OpenAttention); err != nil {
		return err
	}
	return nil
}

func readInsightsConcurrency(
	ctx context.Context, db *sql.DB,
	scope *scopeFilter, since, until time.Time,
	out *insightsResponse,
) error {
	// Open sessions in the scope. scope.SessionsClause already matches
	// the right column set per scope kind (see insights_scope.go).
	_ = since
	_ = until
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM sessions s
		 WHERE `+scope.SessionsClause+`
		   AND s.status = 'active'`,
		append([]any{}, scope.SessionsArgs...)...,
	).Scan(&out.Concurrency.OpenSessions); err != nil {
		return err
	}
	// Active agents: agents currently driving a session in the scope.
	// The two subqueries do double duty — the inner SELECT finds the
	// scoped sessions, the outer JOIN on agents.id picks the running
	// ones. For agent-scope this is degenerate (one or zero rows).
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM agents
		 WHERE id IN (
		   SELECT s.current_agent_id FROM sessions s
		    WHERE `+scope.SessionsClause+`
		      AND s.status = 'active' AND s.current_agent_id IS NOT NULL)
		   AND status = 'running'`,
		append([]any{}, scope.SessionsArgs...)...,
	).Scan(&out.Concurrency.ActiveAgents); err != nil {
		return err
	}
	return nil
}

// readInsightsTools fills the W5c tool-call efficiency block.
//
//   - tool_calls — count of `agent_events.kind='tool_call'` rows in the
//     scope. Doesn't count tool_call_update because each call's lifecycle
//     can emit several updates; the user-facing "tools" count means the
//     distinct invocations, not the streaming progress frames.
//   - tools_per_turn — divides by the count of `kind='turn.result'` in
//     the same window. Zero turns ⇒ ratio is 0 (rather than NaN) so the
//     mobile renderer doesn't have to special-case `null`.
//   - approvals_total / approvals_approved — `attention_items` rows
//     scoped via the session join, kind='approval_request', resolved
//     status. Approve detection walks `decisions_json` via json_each.
//     Approval_request rows carry exactly one resolving decision (the
//     code path in handlers_attention.go appends one entry on
//     resolution), so EXISTS-on-approve is reliable.
func readInsightsTools(
	ctx context.Context, db *sql.DB,
	scope *scopeFilter, since, until time.Time,
	out *insightsResponse,
) error {
	args := append([]any{}, scope.EventsArgs...)
	args = append(args, since.Format(time.RFC3339), until.Format(time.RFC3339))
	var toolCalls int64
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM agent_events
		 WHERE `+scope.EventsClause+`
		   AND ts >= ? AND ts < ?
		   AND kind = 'tool_call'`,
		args...,
	).Scan(&toolCalls); err != nil {
		return err
	}
	out.Tools.ToolCalls = toolCalls

	// turn count for the ratio. We could fold this into the
	// spend-and-latency loop but the extra query keeps that path
	// untouched and the count is constant-time on the (project_id, ts)
	// index.
	var turnCount int64
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM agent_events
		 WHERE `+scope.EventsClause+`
		   AND ts >= ? AND ts < ?
		   AND kind = 'turn.result'`,
		args...,
	).Scan(&turnCount); err != nil {
		return err
	}
	if turnCount > 0 {
		out.Tools.ToolsPerTurn = float64(toolCalls) / float64(turnCount)
	}

	// Approval funnel — total resolved approval_requests, plus the
	// subset whose decisions_json contains an approve verdict.
	approvalArgs := append([]any{}, scope.SessionsArgs...)
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM attention_items ai
		 JOIN sessions s ON s.id = ai.session_id
		 WHERE `+scope.SessionsClause+`
		   AND ai.kind = 'approval_request'
		   AND ai.status = 'resolved'`,
		approvalArgs...,
	).Scan(&out.Tools.ApprovalsTotal); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM attention_items ai
		 JOIN sessions s ON s.id = ai.session_id
		 WHERE `+scope.SessionsClause+`
		   AND ai.kind = 'approval_request'
		   AND ai.status = 'resolved'
		   AND EXISTS (
		     SELECT 1 FROM json_each(ai.decisions_json) je
		      WHERE json_extract(je.value, '$.decision') = 'approve'
		   )`,
		approvalArgs...,
	).Scan(&out.Tools.ApprovalsApproved); err != nil {
		return err
	}
	if out.Tools.ApprovalsTotal > 0 {
		out.Tools.ApprovalRate =
			float64(out.Tools.ApprovalsApproved) / float64(out.Tools.ApprovalsTotal)
	}
	return nil
}

// readInsightsLifecycle fills the W5d block for project scope.
// Other scopes return nil with no error (Lifecycle stays unset and
// the response omits the key).
//
//   - phases — (phase, entered_at, duration_s) rows derived from
//     `projects.phase_history`. The trailing phase's duration runs
//     to now() so the mobile timeline shows the live "we've been
//     parked here" gap.
//   - deliverables — count by ratification_state.
//   - criteria — count by state ('met' / 'failed').
//   - stuck_count — criteria in 'failed' state (the actionable
//     bucket); 'pending' is the normal idle state, not a problem.
func readInsightsLifecycle(
	ctx context.Context, db *sql.DB,
	scope *scopeFilter, out *insightsResponse,
) error {
	if scope.Kind != "project" {
		return nil
	}
	projectID := scope.ID

	var currentPhase, historyJSON string
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(phase, ''), COALESCE(phase_history, '')
		  FROM projects WHERE id = ?`, projectID,
	).Scan(&currentPhase, &historyJSON); err != nil {
		// project missing → no lifecycle to report; skip cleanly.
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	lc := &insightsLifecycle{CurrentPhase: currentPhase}
	if historyJSON != "" {
		var hist phaseHistoryDoc
		if err := json.Unmarshal([]byte(historyJSON), &hist); err == nil {
			lc.Phases = computePhaseTimespans(hist.Transitions)
		}
	}

	if err := db.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(SUM(CASE WHEN ratification_state = 'ratified' THEN 1 ELSE 0 END), 0)
		  FROM deliverables WHERE project_id = ?`, projectID,
	).Scan(&lc.DeliverablesTotal, &lc.DeliverablesRatified); err != nil {
		return err
	}
	if lc.DeliverablesTotal > 0 {
		lc.RatificationRate =
			float64(lc.DeliverablesRatified) / float64(lc.DeliverablesTotal)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT count(*),
		       COALESCE(SUM(CASE WHEN state = 'met'    THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN state = 'failed' THEN 1 ELSE 0 END), 0)
		  FROM acceptance_criteria WHERE project_id = ?`, projectID,
	).Scan(&lc.CriteriaTotal, &lc.CriteriaMet, &lc.StuckCount); err != nil {
		return err
	}
	if lc.CriteriaTotal > 0 {
		lc.CriterionPassRate =
			float64(lc.CriteriaMet) / float64(lc.CriteriaTotal)
	}

	out.Lifecycle = lc
	return nil
}

// computePhaseTimespans walks an ordered transition list and emits
// one phaseTimespan per *destination* phase. Each row's duration is
// the gap to the next transition's `at`, except the final row whose
// duration runs to time.Now() so the renderer sees how long the
// project has been parked in the current phase.
func computePhaseTimespans(trs []phaseTransition) []phaseTimespan {
	if len(trs) == 0 {
		return nil
	}
	out := make([]phaseTimespan, 0, len(trs))
	for i, t := range trs {
		var endAt time.Time
		if i+1 < len(trs) {
			endAt, _ = time.Parse(time.RFC3339, trs[i+1].At)
		} else {
			endAt = time.Now().UTC()
		}
		startAt, err := time.Parse(time.RFC3339, t.At)
		if err != nil {
			continue
		}
		duration := endAt.Sub(startAt)
		if duration < 0 {
			duration = 0
		}
		out = append(out, phaseTimespan{
			Phase:     t.To,
			EnteredAt: t.At,
			DurationS: int64(duration.Seconds()),
		})
	}
	return out
}

// percentile returns the int64 floor at the q-th percentile (0..1) of a
// duration sample slice. Empty slice returns 0. Uses linear interpolation
// between adjacent samples so a 4-sample distribution doesn't snap to
// the same value for both p50 and p95.
func percentile(in []int64, q float64) int64 {
	if len(in) == 0 {
		return 0
	}
	v := append([]int64(nil), in...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if len(v) == 1 {
		return v[0]
	}
	pos := q * float64(len(v)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return v[lo]
	}
	frac := pos - float64(lo)
	return int64(float64(v[lo])*(1-frac) + float64(v[hi])*frac)
}

// readNumber accepts the float64-from-JSON-decode case plus int64 (in
// case a future caller hands us a marshalled struct). Returns 0 for
// missing or non-numeric values.
func readNumber(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	}
	return 0
}

// byProjectMaxRows hard-caps the team-scope project rollup. If a team
// holds more goal-kind projects than this, the trailing tail (oldest
// activity) is dropped. Pagination would buy more, but at MVP scale a
// team with >100 projects is itself an open question.
const byProjectMaxRows = 100

// fillInsightsByProject computes the per-project rollup for team /
// team_stewards scope. Filters out workspaces (kind='standing') and
// archived projects so the cross-project overview surface shows live
// work only. Sort: last_activity DESC.
//
// Reads template YAML for phases_total — uses a per-call cache keyed by
// template_id, since one team typically uses 1–3 distinct templates and
// reparsing the YAML per project would be wasteful.
//
// See plan `project-overview-attention-redesign.md` W3 and the open
// questions resolved 2026-05-11 (Q2 progress formula, Q3 workspace
// filter, Q5 open_criteria definition).
func (s *Server) fillInsightsByProject(
	ctx context.Context, scope *scopeFilter, out *insightsResponse,
) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(phase, ''), COALESCE(status, ''),
		       COALESCE(phase_history, ''), COALESCE(template_id, '')
		  FROM projects
		 WHERE team_id = ?
		   AND kind = 'goal'
		   AND status != 'archived'`, scope.ID)
	if err != nil {
		return err
	}
	type projRow struct {
		id, name, phase, status, history, templateID string
	}
	var projects []projRow
	for rows.Next() {
		var p projRow
		if err := rows.Scan(
			&p.id, &p.name, &p.phase, &p.status,
			&p.history, &p.templateID,
		); err != nil {
			rows.Close()
			return err
		}
		projects = append(projects, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(projects) == 0 {
		out.ByProject = []insightsProjectAgg{}
		return nil
	}

	ids := make([]any, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.id)
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	// Aggregate fan-out: three bulk queries indexed by project_id. Cheap
	// at MVP scale; revisit if a team ever pushes past byProjectMaxRows.
	lastActivity := map[string]string{}
	{
		r, err := s.db.QueryContext(ctx,
			`SELECT project_id, MAX(ts) FROM agent_events
			  WHERE project_id IN (`+placeholders+`)
			  GROUP BY project_id`, ids...)
		if err != nil {
			return err
		}
		for r.Next() {
			var pid string
			var ts sql.NullString
			if err := r.Scan(&pid, &ts); err != nil {
				r.Close()
				return err
			}
			if ts.Valid {
				lastActivity[pid] = ts.String
			}
		}
		r.Close()
	}

	openAttention := map[string]int{}
	{
		r, err := s.db.QueryContext(ctx,
			`SELECT project_id, COUNT(*) FROM attention_items
			  WHERE project_id IN (`+placeholders+`) AND status = 'open'
			  GROUP BY project_id`, ids...)
		if err != nil {
			return err
		}
		for r.Next() {
			var pid string
			var n int
			if err := r.Scan(&pid, &n); err != nil {
				r.Close()
				return err
			}
			openAttention[pid] = n
		}
		r.Close()
	}

	// (project_id, phase, state) → count
	type acKey struct{ pid, phase, state string }
	acCounts := map[acKey]int{}
	{
		r, err := s.db.QueryContext(ctx,
			`SELECT project_id, phase, state, COUNT(*) FROM acceptance_criteria
			  WHERE project_id IN (`+placeholders+`)
			  GROUP BY project_id, phase, state`, ids...)
		if err != nil {
			return err
		}
		for r.Next() {
			var pid, phase, state string
			var n int
			if err := r.Scan(&pid, &phase, &state, &n); err != nil {
				r.Close()
				return err
			}
			acCounts[acKey{pid, phase, state}] = n
		}
		r.Close()
	}

	phasesTotalCache := map[string]int{}

	out.ByProject = make([]insightsProjectAgg, 0, len(projects))
	for _, p := range projects {
		phasesDone := 0
		if p.history != "" {
			var hist phaseHistoryDoc
			if err := json.Unmarshal([]byte(p.history), &hist); err == nil {
				phasesDone = len(hist.Transitions)
			}
		}

		// Current-phase AC ratio drives the within-phase progress slice.
		var curTotal, curMet int
		for _, state := range []string{"pending", "met", "failed", "waived"} {
			n := acCounts[acKey{p.id, p.phase, state}]
			curTotal += n
			if state == "met" {
				curMet = n
			}
		}
		var acRatio float64
		if curTotal > 0 {
			acRatio = float64(curMet) / float64(curTotal)
		}

		// Project-wide open criteria — sum across all phases, both
		// pending and failed (Q5). Past-phase pendings can mean
		// "intentionally waived without state update"; we still surface
		// them so the director isn't blind to drift.
		openCriteria := 0
		for k, n := range acCounts {
			if k.pid != p.id {
				continue
			}
			if k.state == "pending" || k.state == "failed" {
				openCriteria += n
			}
		}

		phasesTotal, cached := phasesTotalCache[p.templateID]
		if !cached {
			phasesTotal = s.templatePhaseCount(p.templateID)
			phasesTotalCache[p.templateID] = phasesTotal
		}

		progress := 0.0
		if phasesTotal > 0 {
			progress = (float64(phasesDone) + acRatio) / float64(phasesTotal)
			if progress > 1.0 {
				progress = 1.0
			}
			if progress < 0 {
				progress = 0
			}
		}

		out.ByProject = append(out.ByProject, insightsProjectAgg{
			ProjectID:     p.id,
			Name:          p.name,
			CurrentPhase:  p.phase,
			Status:        p.status,
			Progress:      progress,
			OpenAttention: openAttention[p.id],
			OpenCriteria:  openCriteria,
			LastActivity:  lastActivity[p.id],
		})
	}

	sort.Slice(out.ByProject, func(i, j int) bool {
		// Empty strings sort last so projects with no activity sink.
		a, b := out.ByProject[i].LastActivity, out.ByProject[j].LastActivity
		if a == "" && b == "" {
			return out.ByProject[i].Name < out.ByProject[j].Name
		}
		if a == "" {
			return false
		}
		if b == "" {
			return true
		}
		return a > b
	})
	if len(out.ByProject) > byProjectMaxRows {
		out.ByProject = out.ByProject[:byProjectMaxRows]
	}
	return nil
}

// templatePhaseCount returns the number of phases declared in a
// template's `phase_specs` block. 0 means missing template or
// unparseable YAML — the caller treats that as "progress unknown" and
// leaves the field at zero.
func (s *Server) templatePhaseCount(templateID string) int {
	if templateID == "" {
		return 0
	}
	body := s.readProjectTemplateYAML(templateID)
	if body == "" {
		return 0
	}
	var head phaseSpecsHead
	if err := yaml.Unmarshal([]byte(body), &head); err != nil {
		return 0
	}
	return len(head.PhaseSpecs)
}

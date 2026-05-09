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
	ByEngine    map[string]insightsAgg `json:"by_engine"`
	ByModel     map[string]insightsAgg `json:"by_model"`
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
	}
	if err := rows.Err(); err != nil {
		return err
	}

	out.Latency.Samples = int64(len(durations))
	out.Latency.TurnP50Ms = percentile(durations, 0.50)
	out.Latency.TurnP95Ms = percentile(durations, 0.95)

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

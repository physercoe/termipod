package server

import (
	"errors"
	"net/url"
	"strings"
)

// insights_scope.go — query-param parsing + per-table predicate
// generation for the multi-scope `/v1/insights` endpoint
// (insights-phase-2 W1). ADR-022 D3 already shaped the surface as
// scope-parameterized; Phase 1 wired the project branch only. This
// file owns the rest.
//
// Scope kinds shipped here: project / team / agent / engine / host.
// `user` is deferred — there's no per-token user attribution at MVP
// (ADR-005 has principal/director rather than per-user identity);
// adding it would mean adding a real users table first.

// errInsightsScope is the shared bad-request error message. The handler
// 400s with the unwrapped string.
var errInsightsScope = errors.New(
	"exactly one of project_id, team_id, agent_id, engine, host_id is required")

// scopeFilter encapsulates how to constrain insights queries to a single
// scope. Each table the aggregator touches needs a different predicate
// because the foreign keys live in different places (agent_events has
// project_id directly, but reaches team via agents.team_id; sessions has
// team_id directly but reaches project via scope_kind+scope_id). Rather
// than scatter switch statements through three handlers, build the SQL
// fragments once and inline them at each query site.
//
// Each *Clause field is a boolean expression that callers join into a
// compound WHERE without leading AND. Each *Args slice carries the `?`
// bindings positionally — callers append to their own arg list.
type scopeFilter struct {
	Kind string // mirrors scope.kind in the response
	ID   string // mirrors scope.id

	// EventsClause is over `agent_events` columns directly. Inline-able
	// as `WHERE ... AND <EventsClause>`.
	EventsClause string
	EventsArgs   []any

	// SessionsClause is over `sessions` columns. **Always prefixes
	// columns with `s.`** so the same fragment slots into a JOIN with
	// `attention_items` (which also has scope_kind/scope_id) without
	// ambiguity. Standalone sessions queries are expected to alias the
	// table as `s` (e.g. `FROM sessions s WHERE …`).
	SessionsClause string
	SessionsArgs   []any
}

// parseInsightsScope extracts a scope from query params. Exactly one of
// the supported keys must be set; absence or multiple => 400.
//
// Returns the bound scopeFilter ready to splice into a WHERE clause.
func parseInsightsScope(q url.Values) (*scopeFilter, error) {
	candidates := []struct {
		key  string
		kind string
	}{
		{"project_id", "project"},
		{"team_id", "team"},
		{"agent_id", "agent"},
		{"engine", "engine"},
		{"host_id", "host"},
	}
	var (
		kind string
		id   string
		seen int
	)
	for _, c := range candidates {
		v := strings.TrimSpace(q.Get(c.key))
		if v == "" {
			continue
		}
		seen++
		kind = c.kind
		id = v
	}
	if seen != 1 {
		return nil, errInsightsScope
	}
	return newScopeFilter(kind, id), nil
}

// newScopeFilter builds the per-table predicates for a (kind, id) pair.
// The mapping is the source of truth for "what does X scope mean in
// SQL"; consult the table below before adding a new branch.
//
//   project: agent_events has project_id directly (post-0036). Sessions
//            uses (scope_kind='project', scope_id=?).
//
//   team:    agent_events.agent_id → agents.team_id; the IN (SELECT...)
//            keeps the index-friendly main query simple. Sessions has
//            team_id directly.
//
//   agent:   single-row filter on both tables.
//
//   engine:  agents.kind carries the engine identifier (claude-code,
//            gemini-cli, codex). No separate engine column. Both events
//            and sessions filter through agents.
//
//   host:    agents.host_id is the spawn host. Sessions reaches host via
//            sessions.current_agent_id → agents.host_id.
func newScopeFilter(kind, id string) *scopeFilter {
	switch kind {
	case "project":
		return &scopeFilter{
			Kind: "project", ID: id,
			EventsClause:   "project_id = ?",
			EventsArgs:     []any{id},
			SessionsClause: "s.scope_kind = 'project' AND s.scope_id = ?",
			SessionsArgs:   []any{id},
		}
	case "team":
		return &scopeFilter{
			Kind: "team", ID: id,
			EventsClause:   "agent_id IN (SELECT id FROM agents WHERE team_id = ?)",
			EventsArgs:     []any{id},
			SessionsClause: "s.team_id = ?",
			SessionsArgs:   []any{id},
		}
	case "agent":
		return &scopeFilter{
			Kind: "agent", ID: id,
			EventsClause:   "agent_id = ?",
			EventsArgs:     []any{id},
			SessionsClause: "s.current_agent_id = ?",
			SessionsArgs:   []any{id},
		}
	case "engine":
		return &scopeFilter{
			Kind: "engine", ID: id,
			EventsClause:   "agent_id IN (SELECT id FROM agents WHERE kind = ?)",
			EventsArgs:     []any{id},
			SessionsClause: "s.current_agent_id IN (SELECT id FROM agents WHERE kind = ?)",
			SessionsArgs:   []any{id},
		}
	case "host":
		return &scopeFilter{
			Kind: "host", ID: id,
			EventsClause:   "agent_id IN (SELECT id FROM agents WHERE host_id = ?)",
			EventsArgs:     []any{id},
			SessionsClause: "s.current_agent_id IN (SELECT id FROM agents WHERE host_id = ?)",
			SessionsArgs:   []any{id},
		}
	}
	return nil
}

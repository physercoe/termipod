# Insights Phase 2 — multi-scope expansion + Tier-2 dimensions

> **Type:** plan
> **Status:** Done — MVP scope (W1-W5a-W5d shipped 2026-05-09; W5e/W5f/W6 deferred post-MVP)
> **Audience:** contributors
> **Last verified vs code:** v1.0.462

**TL;DR.** [ADR-022](../decisions/022-observability-surfaces.md)
Phase 2 graduates the Insights surface from project-scoped (Phase 1)
to multi-scope, accessible from five entry points. Adds Tier-2
dimensions: engine arbitrage, lifecycle flow, tool-call efficiency,
unit economics, snippet / template usage, multi-host distribution.
This doc is the sketch — concrete wedge numbering, file paths, and
acceptance criteria land when Phase 2 enters In flight (after
Phase 1 ships).

---

## 1. What Phase 1 leaves unsolved

After [ADR-022](../decisions/022-observability-surfaces.md) Phase 1
ships, the Insights surface answers project-scoped Tier-1 questions
only. Phase 2 fills four gaps:

1. **Scope expansion.** Same Tier-1 metrics, but selectable across
   project / team / agent / engine / host / user / time-range
   scopes.
   [ADR-022 D3](../decisions/022-observability-surfaces.md) already
   shapes the endpoint — the parameter set exists, only the
   `project_id` branch is wired in Phase 1.
2. **Surface expansion.** The Insights view becomes accessible from
   Me tab, Activity AppBar, Hosts Detail, Agent Detail. Today
   (Phase 1) only Project Detail and Hub Detail wire it.
3. **Tier-2 dimensions.** Six new metric families that don't fit
   Tier-1's mobile-glance budget but answer drill-down questions
   managers actually ask.
4. **Performance posture.** When does the rollup table from
   [ADR-022 D5](../decisions/022-observability-surfaces.md) become
   necessary? Phase 2 wires the alert that triggers that work.

---

## 2. Wedge sketch

Phase 2 is **6 wedges** — exact numbering and file paths land when
Phase 2 enters In flight. This is the architectural sketch.

### W1 — Multi-scope `/v1/insights` (SHIPPED v1.0.457-alpha)

Lifted the handler from project-only to **5 scopes** —
`project_id` / `team_id` / `agent_id` / `engine` / `host_id`. The
`user_id` scope is parked: ADR-005's principal/director model has no
users table at MVP, so per-token attribution doesn't exist; the
endpoint 400s with the same "exactly one of …" error if you pass it.
Time-range params (`since`, `until`) are independent of scope and
unchanged from Phase 1 W2.

**Mobile-side scope chip is deferred** to whichever wedge first lands
multiple entry points. On Project Detail (the only Phase-1 entry
point) there's no scope to pick — you're already on a project — so a
chip would be vestigial. The `InsightsScope` value object +
`getInsights({projectId, teamId, agentId, engine, hostId})` API are
ready for W2-W4 callers.

**Files shipped:**
- `hub/internal/server/insights_scope.go` — query-param parsing +
  per-table predicate generation. SessionsClause prefixes columns
  with `s.` so it slots into JOIN-with-attention_items without
  ambiguity.
- `hub/internal/server/handlers_insights.go` — refactor: every SQL
  site swapped from hardcoded `project_id = ?` to
  `<scope.EventsClause>` / `<scope.SessionsClause>`. Cache key now
  prefixed with scope kind so cross-scope reads can't shadow.
- `hub/internal/server/handlers_insights_scope_test.go` — 6 new
  tests: exactly-one-scope contract, team aggregates across
  projects, agent isolates, engine filters by agents.kind, host
  filters by agents.host_id, cache-key isolation.
- `lib/services/hub/hub_client.dart` — `getInsights` /
  `getInsightsCached` now take `{projectId?, teamId?, agentId?,
  engine?, hostId?}`. Throws synchronously if exactly-one rule is
  violated rather than waiting for the hub 400.
- `lib/providers/insights_provider.dart` — new `InsightsScope` value
  object (5 named ctors); family provider keyed by the typed scope.
- `lib/widgets/insights_panel.dart` — takes `InsightsScope` instead
  of a raw `projectId`; re-exports `InsightsScope` for callers that
  already import the panel.

### W2 — Insights icon on Activity AppBar (SHIPPED v1.0.458-alpha)

Lands the **first fullscreen Insights view** (ADR-022 D7 — "one
fullscreen view") and the cross-link from Activity. Tapping the
new Insights icon in the Activity AppBar pushes
`InsightsScreen(scope: …)` with the narrowest active filter as the
scope: project filter → project scope, otherwise team scope keyed
on the audit feed's current team. Actor / prefix filters don't
participate — they're who-did-what axes, not metric scopes.

The fullscreen view is a thin wrapper around `InsightsPanel` so the
tile rendering stays in one place; what it adds is a scope banner +
explicit refresh button + landing space for Tier-2 drilldown sheets
(W5). Future entry points (Me Stats card W3, Hosts/Agent Detail W4)
push the same screen with their own scope.

**Files shipped:**
- `lib/screens/insights/insights_screen.dart` — fullscreen wrapper.
  Resolves project name from `hubProvider.projects` for the scope
  banner; other scope kinds show the raw id (no name lookup at MVP).
- `lib/screens/team/audit_screen.dart` — Insights AppBar action +
  `_openInsights` method that maps current filters to scope.

### W3 — Me tab → Stats card (SHIPPED v1.0.459-alpha)

Me tab gains a compact Stats card below the Activity digest:
today's team-wide spend (tokens in + out) plus Δ% vs the prior 7-day
**average** (not total — averaging keeps the comparison
day-on-day-meaningful). Up arrow + warning color when spend climbs;
down arrow + success color when it drops; "—" when prior is zero so
a cold-start team doesn't render a misleading percentage. Tap →
`InsightsScreen(scope: InsightsScope.team(teamId))`.

Hidden when no traffic has flowed (todayTokens == 0 && priorAvg ==
0), since glancing zeros adds noise without insight. Two
`/v1/insights` reads under the hood — today (24h) and the 7 days
before that — folded by `meTeamSpendDeltaProvider`. The hub's 30s
response cache covers tab re-mounts; cache-fall-back is propagated
as a `staleSince` field for future "stale dot" UI.

**Files shipped:**
- `lib/providers/me_stats_provider.dart` — new
  `meTeamSpendDeltaProvider`, family-keyed by team id; returns
  `MeSpendDelta` with todayTokens / prior7dAvgTokens / deltaPct /
  staleSince / error.
- `lib/widgets/me_stats_card.dart` — new `MeStatsCard` widget.
  Lands in its own file (per the agent_feed-don't-grow lesson —
  me_screen.dart was already 1064 lines).
- `lib/screens/me/me_screen.dart` — sliver insertion below the
  ActivityDigestCard; gates on `hubState.configured && teamId`
  non-empty so the two-window read can't 400 on cold start.

### W4 — Hosts Detail / Agent Detail → Insights (SHIPPED v1.0.460-alpha)

**Plan adjustment:** Both Host Detail and Agent Detail are bottom
sheets, not tabbed screens. The literal "Insights tab" was right for
Agent (its sheet already has a 3-tab DefaultTabController; we
extended to 4) but wrong for Host (a flat SingleChildScrollView —
adding tabs would have rebuilt the whole sheet). Pragmatic
adaptation:

- **Agent Detail** — added a 4th tab `Insights` showing
  `InsightsPanel(scope: InsightsScope.agent(_id))`. Embedded so the
  user keeps the sheet's lifecycle controls (Pause / Terminate /
  Respawn) one tab away.
- **Host Detail** — added an `Insights` outlined button between the
  bind/unbind row and the Delete-host action. Pops the sheet and
  pushes `InsightsScreen(scope: InsightsScope.host(hostId))` so the
  fullscreen view gets the whole vertical budget. host scope folds
  through `agents.host_id` per W1's scopeFilter.

Both surfaces validate the InsightsScope API W1 designed for; the
mobile scope chip stays deferred — a chip strip on the fullscreen
view becomes useful with W5's Tier-2 dimensions, since the user will
want to swap scopes inside the same drilldown context.

### W5 — Tier-2 dimensions

Six dimension blocks. Reframed mid-flight as **W5a-W5f sub-wedges**
because each dimension's data source has its own readiness profile —
shipping them serially lets the cheap ones land while the expensive
ones remain queued.

#### W5a — Engine + model breakdown (SHIPPED v1.0.461-alpha)

The hub's `/v1/insights` response already carries `by_engine` and
`by_model` rollups (Phase 1 W2 / handlers_insights.go); this wedge
is pure-mobile rendering. Adds an `InsightsBreakdownSection` below
the panel on `InsightsScreen`. Two stacked tables — by engine, by
model — each row showing tokens (sorted descending), a share bar
relative to the max, turn count, and tokens/turn. The tokens/turn
column is the actionable engine-arbitrage signal: a steady
tokens/turn at a per-engine price differential is what "should I
pivot to a cheaper engine" asks. Once the pricing table lands
(post-MVP per ADR-022) it becomes the numerator of $/turn directly.

**File shipped:**
- `lib/widgets/insights_breakdown_section.dart` —
  `InsightsBreakdownSection` widget; reads the same provider the
  panel does, so no second round-trip.

#### W5b — Multi-host distribution (SHIPPED v1.0.462-alpha)

Pure-mobile widget reading cached `hubProvider.hosts` +
`hubProvider.agents`. Per-host: agent count (sorted descending), a
share bar relative to the max, capability fingerprint
(CPU + memory) drawn from `capabilities.host` when available, status
dot. Hides itself on degenerate scopes (agent / host) and when fewer
than two hosts contribute. Project scope falls back to "team-wide
agents" because the hub agents endpoint doesn't carry project linkage
— the strict join lives in W5d.

GPU split + disk-per-host stay deferred — neither field exists in
`capabilities_json` today. Token spend per host needs a `by_host`
rollup the hub doesn't compute (would mirror by_engine work). Both
trail W5b.

**File shipped:**
- `lib/widgets/insights_host_distribution.dart` —
  `InsightsHostDistribution` widget.

#### W5c — Tool-call efficiency (SHIPPED v1.0.462-alpha)

Hub adds a `tools` block to `/v1/insights`:
- `tool_calls` — `agent_events.kind='tool_call'` count in scope
  (excludes `tool_call_update`, which streams progress frames).
- `tools_per_turn` — call count divided by `turn.result` count.
- `approvals_total` / `approvals_approved` — resolved
  `attention_items` of kind `approval_request`, walked via
  `json_each(decisions_json)` for an `EXISTS` approve check.
- `approval_rate` — derived ratio.

Mobile adds a `TOOL CALLS` section under the panel: total + per-turn
+ approval rate with a color-coded bar (green ≥ 85%, warning ≥ 50%,
error otherwise — low approval rates suggest gate misalignment).
Hides on zero-call zero-approval scopes.

**Files shipped:**
- `hub/internal/server/handlers_insights.go` — `insightsTools` type,
  `readInsightsTools` helper.
- `hub/internal/server/handlers_insights_scope_test.go` —
  `TestInsights_ToolsBlock_AggregatesToolCallsAndApprovals`.
- `lib/widgets/insights_tools_section.dart` — `InsightsToolsSection`.

#### W5d — Lifecycle flow (SHIPPED v1.0.462-alpha)

Hub adds a project-only `lifecycle` block (omitted on team / agent /
engine / host scopes via pointer + omitempty):
- `current_phase` — `projects.phase`.
- `phases` — derived from `projects.phase_history.transitions`; one
  row per destination phase with `entered_at` + `duration_s`. The
  trailing phase's duration runs to `time.Now()` so the renderer
  shows the live "we've been parked here" gap.
- `deliverables_total` / `deliverables_ratified` /
  `ratification_rate` — straight count + ratio over `deliverables`.
- `criteria_total` / `criteria_met` / `criterion_pass_rate` /
  `stuck_count` — same shape over `acceptance_criteria`. Stuck =
  `state='failed'` (the actionable bucket); pending is normal idle.

Mobile renders three sub-sections: phase timeline (per-phase row,
duration bar, current-phase dot), ratification + criterion rate
rows with color-coded bars, plus an inline warning row when
`stuck_count > 0` ("clear with the steward"). Section hides when
the project has no phase history AND no deliverables AND no
criteria.

**Files shipped:**
- `hub/internal/server/handlers_insights.go` —
  `insightsLifecycle` + `phaseTimespan` types,
  `readInsightsLifecycle` + `computePhaseTimespans` helpers.
- `hub/internal/server/handlers_insights_scope_test.go` —
  `TestInsights_LifecycleBlock_PopulatedForProjectScope` (covers
  presence on project, absence on team).
- `lib/widgets/insights_lifecycle_section.dart` —
  `InsightsLifecycleSection`.

#### W5e/W5f deferred

| Sub-wedge | Dimension | Status | Render |
|---|---|---|---|
| W5a | Engine + model breakdown | SHIPPED v1.0.461 | tokens, share-bar, turns, tokens/turn |
| W5b | Multi-host distribution | SHIPPED v1.0.462 | agent count per host with capability fingerprint |
| W5c | Tool-call efficiency | SHIPPED v1.0.462 | tool calls, tools/turn, approval rate (color-coded) |
| W5d | Lifecycle flow | SHIPPED v1.0.462 | phase timeline, ratification rate, criterion pass-rate, stuck count |
| W5e | Unit economics ($/session etc) | **deferred post-MVP** | Needs a pricing table — token×$ per model. ADR-022 marks pricing post-MVP; the current token-based metrics are the MVP proxy |
| W5f | Snippet / template usage | **deferred post-MVP** | Needs snippet-press telemetry events (currently the action bar fires `snippet` without emitting an event) |

The lifecycle dimension reads from `phase_specs`, `deliverables`,
`acceptance_criteria` (added by W5 / W6 of
[project-lifecycle-mvp.md](project-lifecycle-mvp.md)). DORA-for-AI
flow rate.

### W6 — Performance posture + rollup trigger (DEFERRED post-MVP)

Adds a p95-latency alert on the Insights endpoint. When p95 exceeds
1s on real workloads, this is the trigger to land the materialized
rollup table (`agent_event_rollups` keyed `team_id, project_id,
agent_id, engine, day`).

**Deferred 2026-05-09**: alpha workloads are tiny — the trigger
condition (p95 > 1s) cannot fire today. The design *is* "wait for
real load, then act." Reopen this wedge when the first real
deployment crosses the threshold; until then the on-demand
aggregation in handlers_insights.go is fine.

---

## 3. Open questions

- **`audit_events.project_id` column.** Deferred from Phase 1
  (`json_extract` works at MVP scale). Phase 2's lifecycle-flow
  dimension reads `audit_events` for phase advance counts; if
  `json_extract` latency dominates Insights p95, the column add
  gets pulled into W6's rollup trigger.
- **User-scoped insights.** ADR-005's principal/director model has
  one human per session today. The `user_id` scope dimension
  assumes per-token attribution that doesn't fully exist yet. May
  land as a sub-wedge.
- **Time-series retention.** Phase 2 still computes on demand.
  Historical curves (DB growth over weeks, spend over months) need
  the rollup table. Post-MVP per
  [ADR-022 D5](../decisions/022-observability-surfaces.md).

## 4. Acceptance criteria (sketch)

- [ ] Insights view accessible from Project Detail, Hub Detail, Me
  tab, Activity AppBar, Hosts Detail, Agent Detail.
- [ ] Scope chip cycles project / team / agent / engine / host /
  user / time-range; each re-fetches.
- [ ] Six Tier-2 drilldown sheets render from the Tier-1 tiles or
  from Settings.
- [ ] p95 latency on `/v1/insights` < 1s on a realistic 6-month-old
  project fixture; if not, follow-up rollup wedge files against
  [ADR-022 D5](../decisions/022-observability-surfaces.md).

## 5. References

- [ADR-022 — observability surfaces](../decisions/022-observability-surfaces.md)
  (parent decision; D3 sketch + D5 rollup trigger).
- [insights-phase-1.md](insights-phase-1.md) — predecessor wedge
  set.
- [Discussion: observability-gap](../discussions/observability-gap.md)
  — narrative.
- [project-lifecycle-mvp.md](project-lifecycle-mvp.md) — source of
  phase / deliverable / criterion data for the lifecycle-flow
  dimension.

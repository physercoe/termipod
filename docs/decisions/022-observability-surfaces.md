# 022. Observability surfaces — insights aggregator + hub stats

> **Type:** decision
> **Status:** Proposed (2026-05-09)
> **Audience:** contributors
> **Last verified vs code:** v1.0.443

**TL;DR.** Termipod ships per-session telemetry today (token totals,
latency, rate-limit progress on the in-session strip) but no
cross-session aggregate, no hub-self capacity report, and no
lifecycle-flow metrics. This ADR locks how we close that gap: a
purpose-built `/v1/hub/stats` endpoint for hub-self capacity (NOT a
synthetic `hosts` row), a project-scoped Insights surface on Project
Detail for per-scope spend / latency / reliability, and a multi-scope
Insights surface in Phase 2 reachable from Project / Me / Activity /
Hosts / Agent details. Folds data we already capture (per-turn
`usage` and `turn.result.by_model` agent_events) under FinOps + SRE
Golden Signals + DORA-for-AI Tier-1 / Tier-2 framing.

## Context

Real-world device testing through v1.0.440-alpha exposed three
observability gaps:

- **No project-scoped aggregate.** Tokens per turn flow into
  `agent_events.kind=usage` (claude SDK) and
  `agent_events.kind=turn.result` carrying `by_model` (codex /
  gemini ACP) but are only aggregated per session in the
  `_TelemetryStrip` widget (`lib/widgets/agent_feed.dart:5037`).
  Asking "how much did this project cost yesterday" requires manual
  SQL. Managers can't answer ROI questions.

- **Hub box is invisible to itself.** The `hosts` table models NAT'd
  worker boxes that push capacity *up* to the hub via the
  host-runner's `ProbeHostInfo`
  (`hub/internal/hostrunner/host_info.go:35`) → `hosts.capabilities_json`.
  The hub itself is the receiver, not the sender — no row, no probe,
  no DB-size or row-count surface. `/v1/_info` returns build version
  only.

- **No lifecycle-flow metrics.** ADR-001's research lifecycle
  (5 phases × deliverables × criteria) ships full data but no
  rate-of-flow surface — managers cannot tell "is this project
  advancing?" without scrolling the audit feed.

Industry grounding (cf. SRE Book ch.4 Golden Signals; FinOps
Foundation Inform pillar; DORA Accelerate framework):

| Frame | Tier-1 metrics |
|---|---|
| FinOps Inform | spend per scope, daily Δ, top-N by spend, unit economics |
| SRE Golden Signals | latency p50 / p95, traffic, errors, saturation |
| DORA-for-AI | flow rate (phase advance, deliverable ratification, criterion pass-rate) |

These three frames cover what manager + ops + lead-engineer roles
look at in production AI-product systems. Termipod has the raw data
for all three; what's missing is the aggregator layer + the surface.

## Decision

### D1. Two distinct surfaces. Insights ≠ Activity.

Activity = chronological audit ("what happened, who did what when").
Insights = aggregated state ("how much, how fast, how often").
Industry convention separates them — Datadog APM ≠ Audit Trail,
Sentry Performance ≠ Issues, AWS CloudTrail ≠ Cost Explorer.

Folding aggregate metrics *into* Activity mixes two cognitive modes
and forces filters that don't compose well (a chronological filter
chip and an aggregation scope chip want to live in the same row but
mean different things). Folding them *adjacent* to Activity (an
Insights icon in the Activity AppBar that opens a scope-inherited
Insights view) keeps them associable without merging.

### D2. Hub stats is a purpose-built endpoint, NOT a synthetic `hosts` row.

The `hosts` table cannot cleanly absorb the hub:

- `team_id` is `NOT NULL` — hub is multi-team / global.
- `host_token_hash` + `last_seen_at` model an outbound connection
  from worker → hub. The hub has no outbound to itself.
- The `status` enum (`disconnected` / `connected`) is for an
  external link that doesn't exist in the hub-self case.
- Endpoints live under `/v1/teams/{teamId}/hosts/...` — wrong
  scope for global hub data.

Counter-options considered and rejected:

- **Synthetic hosts row** (`hosts(id='_self')`, one per team) —
  per-team duplication of one physical hub, vacuous heartbeat
  semantics, and ops-alerting conflation outweigh tile-renderer
  reuse.
- **First-class `hub_metrics` time-series table** — clean long-term
  but premature for MVP; adds upfront schema work for historical
  retention we don't need yet.

Decision: a new `/v1/hub/stats` endpoint, authed at hub-admin scope,
returning machine facts (reuse `hostrunner.ProbeHostInfo`) + DB
stats (size, per-table rows + bytes, schema version) + live counts
(active agents, open sessions, SSE subscribers, A2A relay
throughput). Mobile renders a synthetic Hub group atop the Hosts
list with the same visual language as a hostrunner tile — same
shape, different data source, different actions (stat-focused, no
Enter-pane).

### D3. Insights = scope-parameterized aggregator. Phase 1 = project; Phase 2 = all scopes.

A single `/v1/insights` endpoint takes scope parameters
(`project_id`, `team_id`, `agent_id`, `engine`, `host_id`,
`user_id`, `since`, `until`) and returns the same Tier-1 shape:

```json
{
  "scope": {"kind": "project", "id": "...", "since": "...", "until": "..."},
  "spend":   {"tokens_in": ..., "tokens_out": ..., "cache_read": ..., "cache_create": ..., "cost_usd": ..., "delta_pct_vs_prior_period": ...},
  "latency": {"turn_p50_ms": ..., "turn_p95_ms": ..., "turn_p99_ms": ..., "ttft_p50_ms": ...},
  "errors":  {"failed_turns": ..., "driver_disconnects": ..., "open_attention": ...},
  "concurrency": {"active_agents": ..., "open_sessions": ..., "turns_per_min": ...},
  "by_engine": {...},
  "by_model":  {...}
}
```

Phase 1 ships the project-scope path (Project Detail → Insights
sub-section). Phase 2 expands the scope filter chip and the surface
appears in Me → Stats card, Activity AppBar icon, Hosts Detail,
Agent Detail. The endpoint shape doesn't change between phases —
only the scope branches that are wired.

### D4. agent_events grain — add `project_id` column in Phase 1.

Today `agent_events` has no `project_id`; project attribution
requires JOIN through `sessions(scope_kind='project', scope_id)`,
and team-scoped sessions don't aggregate to a project at all.
Phase 2 multi-scope queries on raw `agent_events` won't perform
without a real index.

Decision: add `agent_events.project_id` column + index
`(project_id, ts)` in Phase 1's W2 migration. Backfill from
`sessions` at migration time. New writes after the migration
populate the column directly; the JOIN-via-sessions path is an MVP
detour, not a long-term shape.

`audit_events.project_id` is similarly missing today (filter goes
through `json_extract(meta_json, '$.project_id')`); deferred — it's
cheap to add later and the json_extract path is fine at MVP scale.

### D5. Materialized rollups are post-MVP.

Real-time aggregation over `agent_events` works at MVP scale (low
thousands of events per project). The `agent_event_rollups` table —
keyed `(team_id, project_id, agent_id, engine, day)` for daily
granularity — gates scaling past ~100k events per project but does
not gate the surfaces in Phase 1 or Phase 2.

Decision: ship Phase 1 + Phase 2 with on-demand aggregation. Add
the rollup table when query latency on the Insights surface exceeds
1s on real workloads. Until then: cache-first per ADR-006 (mobile
renders the cached snapshot then refreshes; the aggregator returns
within the SLO at MVP scale). Phase 2 wires the alert that triggers
the rollup work.

### D6. Cache-first for the Insights surface.

Per ADR-006. Mobile renders the previous-snapshot Insights
immediately, fires the live fetch in the background, swaps in when
the live response lands. Stale banner if the cached snapshot is
older than 60s. This composes with the existing `staleSince`
channel on `HubState`.

### D7. Tile placement — multiple entry points, one fullscreen view.

| Entry | Phase | Default scope | Purpose |
|---|---|---|---|
| Project Detail → Insights sub-section | Phase 1 | this project | "How is this project doing?" |
| Hosts tab → Hub group → Hub Detail | Phase 1 | hub (global) | DB / capacity / relay |
| Me tab → Stats card | Phase 2 | team-wide | morning manager glance, deep-link to fullscreen |
| Activity AppBar → Insights icon | Phase 2 | inherits Activity filter | jump from "what happened" → "how much" |
| Hosts Detail → Insights tab | Phase 2 | this host | per-machine load |
| Agent Detail → Insights tab | Phase 2 | this agent | per-steward / worker spend |

All entry points open the same Insights view; only the default
scope chip differs.

**Forbidden:** a sixth bottom-nav tab. The 5-tab IA (Projects /
Activity / Me / Hosts / Settings) is locked by the IA spec; Me's
center bumper position carries a layout invariant.

## Consequences

**Becomes possible:**

- Manager-level glance answer to "how much is this project costing?"
  — Phase 1.
- Ops-level answer to "is the hub healthy / how full is the DB?" —
  Phase 1.
- Cross-scope comparisons (project vs project, engine vs engine,
  host vs host) — Phase 2.
- DORA-for-AI flow metrics (phase advance rate, ratification rate)
  once Phase 2's lifecycle dimension lands.
- Foundation for per-engine arbitrage decisions ($/turn split by
  claude / codex / gemini).

**Becomes harder:**

- New schema column (`agent_events.project_id`) means a migration
  with backfill in W2; backfill is not free at scale (one-pass over
  `agent_events`). Mitigation: chunked backfill under a single
  transaction per chunk; tested in CI on a 100k-row fixture.
- The Insights endpoint becomes a hot path; without rollups it does
  table scans over `agent_events` for each request. MVP scale
  carries this; the trigger for D5 must be wired (alert on p95
  latency > 1s in Phase 2 W6).

**Becomes forbidden:**

- Embedding aggregate metrics into Activity. Activity stays
  chronological / forensic. Insights stays aggregate / numeric.
  Cross-link via the Activity AppBar icon, don't merge.
- Hub-stats-as-`hosts`-row. The `hosts` table is for NAT'd worker
  boxes that the host-runner reports up about. The hub reports
  about itself via its own endpoint.
- Surfacing Tier-2 / Tier-3 metrics on a glanceable surface. Tier-1
  is mobile-glance (5 tiles); everything else is drill-down sheets,
  not headline tiles.
- Per-engine special-cases inside the aggregator. Capability
  branches live in driver translation; the aggregator reads the
  canonical `agent_events` shape every driver normalizes to.

## References

- ADR-001 — research lifecycle (Phase 2 lifecycle dimension reads
  phases / deliverables / criteria).
- ADR-003 — A2A relay required (relay throughput is hub-side and
  lands in `/v1/hub/stats.live`).
- ADR-006 — cache-first cold start (Insights surface inherits this
  rule).
- ADR-016 — subagent scope manifest (Tier-3 governance metric reads
  scope-violation count when post-MVP).
- SRE Book ch.4 — Monitoring Distributed Systems / Golden Signals
  (https://sre.google/sre-book/monitoring-distributed-systems/).
- FinOps Foundation framework — Inform → Optimize → Operate
  (https://www.finops.org/framework/).
- DORA Accelerate — flow metrics adapted for AI-product systems
  (https://dora.dev/).
- Plans: [insights-phase-1.md](../plans/insights-phase-1.md),
  [insights-phase-2.md](../plans/insights-phase-2.md).
- Discussions: [observability-gap.md](../discussions/observability-gap.md)
  (narrative behind this ADR);
  [observability-post-mvp-dimensions.md](../discussions/observability-post-mvp-dimensions.md)
  (dimensions explicitly out-of-scope — AI quality signals, mobile
  RUM, cache & sync health, drift detection, self-host operational,
  product analytics, non-LLM infra cost, formal SLOs, agent-research
  signals).

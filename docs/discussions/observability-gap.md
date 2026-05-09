# Observability gap — insights surface and hub-self capacity

> **Type:** discussion
> **Status:** Resolved (2026-05-09) → [ADR-022](../decisions/022-observability-surfaces.md), [insights-phase-1.md](../plans/insights-phase-1.md), [insights-phase-2.md](../plans/insights-phase-2.md)
> **Audience:** contributors · reviewers
> **Last verified vs code:** v1.0.443

**TL;DR.** Through device testing v1.0.440-alpha the user noticed
termipod has no aggregate observability surface — no
tokens-per-project, no DB-size answer, no lifecycle-flow rate. This
doc tells the story of how we landed the four-doc resolution (one
ADR, two plan docs, this discussion). The architectural calls —
Activity ≠ Insights, hub-stats-not-as-host-row, scope-parameterized
aggregator, `project_id` column add — live in
[ADR-022](../decisions/022-observability-surfaces.md). The narrative
for *why now and how we got here* lives here.

---

## 1. The trigger — what was missing

A v1.0.440-alpha test session surfaced three concrete questions the
product couldn't answer:

1. **"How much have I spent on this project?"** — token totals exist
   per turn (`agent_events.kind=usage` for claude SDK;
   `agent_events.kind=turn.result` carrying `by_model` for codex /
   gemini ACP) and aggregate per session in the per-session
   telemetry strip (`lib/widgets/agent_feed.dart:5037`).
   Cross-session aggregation does not.
2. **"How big is the hub DB?"** — `/v1/_info` returns build version
   only. No DB size, no row counts, no growth slope. The hub box is
   invisible to itself.
3. **"Are there other dimensions we're missing?"** — implicit in
   the framing.

The first question is FinOps. The second is SRE saturation. The
third is the meta-question — what *should* a product like this
measure, and what dimensions exist in industry-standard practice
that we haven't connected yet?

## 2. The asymmetry that made hub-stats hard

The interesting architectural call was hub-self capacity, not
project insights.

The `hosts` table models NAT'd worker boxes. They push capacity up
to the hub via the host-runner's `ProbeHostInfo`
(`hub/internal/hostrunner/host_info.go:35`), which writes to
`hosts.capabilities_json`. The hub is the *receiver*, not the
sender. This works fine — it's the natural shape of "agents on
workers, hub coordinates."

But it leaves the hub itself with no equivalent row. There's no
`last_seen_at` for the hub (it's always there or you can't reach
it), no `host_token_hash` (it doesn't auth itself), no `team_id`
that fits (the hub is multi-team).

Three options were considered:

- **Option A — purpose-built `/v1/hub/stats` endpoint.** Minimal
  change, semantically honest.
- **Option B — synthetic `hosts` row** (`hosts(id='_self')`, one
  per team). Reuses the tile renderer.
- **Option C — separate first-class `hub_metrics` resource** with
  optional time-series table.

A wins on cost + cleanliness. B fails on multi-tenant scope (per-team
duplication of one physical hub) and lies in `host_token_hash`. C is
the post-MVP evolution when historical retention matters. Recorded
as ADR-022 D2.

## 3. The Activity ≠ Insights call

The other interesting call was where Insights *isn't*.

The Activity tab is chronological — the `audit_events` feed plus a
24h `ActivityDigestCard` showing event counts and top-actors. It
tells the *story of what happened*. A natural temptation is to fold
spend / latency / reliability tiles into Activity since both
surfaces involve event-shaped data.

The temptation is wrong. Activity is forensic, Insights is
aggregate. Industry separates them — Datadog APM is not their Audit
Trail; Sentry Performance is not their Issues; AWS CloudTrail and
Cost Explorer are different products. The cognitive load when both
are merged is what users feel as "this dashboard does too many
things at once."

Resolution: Insights is its own surface accessible from multiple
entry points (Project Detail, Me tab, Activity AppBar icon, Hosts
Detail, Agent Detail) — but not its own bottom-nav tab. The 5-tab
IA is locked. The Activity AppBar gets an Insights icon that opens
the Insights view filtered to whatever Activity is currently scoped
to; that cross-link is the lightweight bond. Recorded as ADR-022
D1, D7.

## 4. Tier framing — why three frames, not one

The user asked for "well-grounded practice — what is most concerning
to managers / ops?" rather than a domain-specific list. Three
industry frames converge on the answer:

- **FinOps Foundation framework** (Inform → Optimize → Operate).
  Tier-1 = Inform: spend per scope, daily Δ, top-N, unit
  economics ($/session, $/deliverable, $/attention).
- **SRE Golden Signals** (Latency, Traffic, Errors, Saturation).
  Tier-1 ops surface for any production system.
- **DORA / Accelerate** flow metrics adapted to AI-product
  systems. Phase advance rate, ratification rate, criterion
  pass-rate.

Tier-1 = mobile glance (5 numbers). Tier-2 = drilldown sheets (≈15
numbers). Tier-3 = post-MVP forensics (governance, security,
knowledge curves).

Phase 1 ships Tier-1 *project-scoped*. Phase 2 expands Tier-1 to all
scopes and adds Tier-2.

## 5. The decision moment

Three things made the four-doc resolution cheap:

1. **Token data already flows.** All three drivers (claude SDK /
   codex exec_resume / gemini ACP) emit per-turn token usage to
   `agent_events`. Mobile already aggregates per-session. Phase 1
   W2 is wiring + UI, not capture.
2. **No new primitive.** ADR-022 doesn't add tables; it adds a
   column (`agent_events.project_id`) and an endpoint
   (`/v1/hub/stats`). The rollup table that *would* be a new
   primitive is post-MVP.
3. **Surfaces are additive.** The Insights view is a new screen,
   the Hub group is a new render tier on the Hosts tab, Project
   Detail gets a sub-section. None of this changes existing
   surfaces.

The director's framing locked the phasing: *"phase 1 is W1, W2, W3,
then add the other (more scope, not just project) tier 1 and
tier 2 info after W3 for next phase."*

This is the same pattern as ADR-001's lifecycle amendment — small
architectural surface, content edits, ships in additive wedges. A
properly-factored system absorbs new observability the same way it
absorbs a new demo phase.

## 6. What this discussion means for new contributors

If you join after 2026-05-09 and read
[ADR-022](../decisions/022-observability-surfaces.md), the operative
sections are D1 (surfaces separate), D2 (hub stats not host), D3
(scope-parameterized), D4 (`project_id` column add). The plan docs
([insights-phase-1.md](../plans/insights-phase-1.md) for W1 / W2 / W3
detail; [insights-phase-2.md](../plans/insights-phase-2.md) for the
multi-scope expansion sketch) tell you what to build.

If your work touches `agent_events` writes, note D4: the
`project_id` column is being added in Phase 1 W2 and backfilled
from `sessions`. New writes after that migration must populate the
column directly. The session-scope JOIN path is an MVP detour, not
a long-term shape.

If your work touches mobile observability widgets, today's
per-session telemetry strip stays in place. Phase 1 adds a separate
fullscreen Insights view; Phase 2 makes that view
scope-parameterized. The session strip and the project-scope view
*coexist* — they answer different questions.

## 7. References

- [ADR-022 — observability surfaces](../decisions/022-observability-surfaces.md)
  (the locked decisions).
- [insights-phase-1.md](../plans/insights-phase-1.md) — W1 / W2 / W3
  implementation detail.
- [insights-phase-2.md](../plans/insights-phase-2.md) — multi-scope
  expansion sketch.
- [ADR-006](../decisions/006-cache-first-cold-start.md) —
  cache-first cold start (composes with Insights cache).
- [ADR-003](../decisions/003-a2a-relay-required.md) — A2A relay
  required (relay throughput is hub-side, lands in W3).
- SRE Book ch.4 — https://sre.google/sre-book/monitoring-distributed-systems/
- FinOps Foundation framework — https://www.finops.org/framework/
- DORA Accelerate — https://dora.dev/

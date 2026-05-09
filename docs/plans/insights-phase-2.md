# Insights Phase 2 — multi-scope expansion + Tier-2 dimensions

> **Type:** plan
> **Status:** Proposed (2026-05-09)
> **Audience:** contributors
> **Last verified vs code:** v1.0.443

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

### W1 — Multi-scope filter chip on Insights view

Lift the `/v1/insights` handler from project-only to all 7 scopes
(`project_id` / `team_id` / `agent_id` / `engine` / `host_id` /
`user_id` / `since` / `until`). Mobile gets a scope filter chip at
the top of the Insights view; chip selection re-fetches and
re-renders. Endpoint shape doesn't change — only the branches that
are wired.

### W2 — Insights icon on Activity AppBar

Activity tab gets an Insights AppBar action that opens the Insights
view filtered to the same `(action_filter, since)` Activity is
currently scoped to. The cross-link is the lightweight bond between
forensic and aggregate views (see
[ADR-022 D1](../decisions/022-observability-surfaces.md)).

### W3 — Me tab → Stats card

Me tab below the digest gets a small Stats card showing team-wide
spend today + Δ vs 7d. Tap → fullscreen Insights view scoped to
team.

### W4 — Hosts Detail / Agent Detail → Insights tab

Each detail screen gets an Insights tab (alongside existing tabs).
Default scope = the entity in question (this host / this agent).

### W5 — Tier-2 dimensions

Six dimension blocks, each rendered as a drilldown sheet from a
Tier-1 tile (or a new entry on a related screen):

| Dimension | Surfaced from | Render |
|---|---|---|
| Engine arbitrage | Spend tile | $/turn × success% split by engine |
| Lifecycle flow | (new tile) | time-in-phase, ratification rate, criterion pass-rate, gate-stuck count |
| Tool-call efficiency | Errors tile | gate approve%, retries, tools/turn |
| Unit economics | Spend tile | $/session, $/deliverable ratified, $/attention resolved |
| Snippet / template usage | (new sheet on Settings) | which presets used, mode/model picker churn |
| Multi-host distribution | Capacity tile (Hub Detail) | agent count per host, GPU vs CPU load, disk per host |

The lifecycle dimension reads from `phase_specs`, `deliverables`,
`acceptance_criteria` (added by W5 / W6 of
[project-lifecycle-mvp.md](project-lifecycle-mvp.md)). DORA-for-AI
flow rate.

### W6 — Performance posture + rollup trigger

Adds a p95-latency alert on the Insights endpoint. When p95 exceeds
1s on real workloads, this is the trigger to land the materialized
rollup table (`agent_event_rollups` keyed `team_id, project_id,
agent_id, engine, day`). The rollup work is post-Phase-2; W6 wires
the trigger and writes the post-MVP follow-up.

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

# Adaptive project lifecycle — rollout

> **Type:** plan
> **Status:** Done (2026-06-05) — P1–P3 released (v1.0.804-alpha). The
> phased implementation of [ADR-044](../decisions/044-adaptive-project-lifecycle.md)
> (the lifecycle is an adaptive roadmap, not a fixed template contract);
> the ADR holds the *what/why*, this plan the *how* (and now the shipped
> record).
> **Audience:** contributors
> **Last verified vs code:** v1.0.807-alpha

**TL;DR.** Make a project's lifecycle adaptive: agents **materialize**
deliverables, criteria are **editable via governed propose verbs**, and
phase advance becomes **AC-driven and system-approved** (ADR-044). Built
hub-first and Go-testable in three phases, all shipped.

## Phases

- **P1 — read affordance + direct materialization/marking. (SHIPPED.)**
  First the reads (decision 4): `criteria.list`, `deliverables.list` /
  `deliverables.get` (with components), and a `phase.status` summary — as
  MCP tools (each = catalog entry + dispatcher + handler in lockstep, over
  the existing REST queries `server.go:518-548`). **`phase.status` reuses
  the aggregate that already exists** — `handleGetProjectOverview`
  (`handlers_deliverables.go:824`) already composes phase + phase_index +
  phases + active-phase deliverables-with-components + counts
  (deliverables total/ratified, criteria total/met); P1 just wraps it for
  MCP, no new query. Then the direct writes (no governance — the agent's
  own work product): MCP tools to **attach / update / remove** a
  component on one's own deliverable + transition `draft↔in-review`
  (decision 1, Q1), and **mark a `text`/`metric` criterion met/failed**
  (decision 2).
- **P2 — `criteria.*` + `deliverable.create` propose kinds. (SHIPPED.)**
  Registered four governed verbs (`criteria.create` / `criteria.update` /
  `criteria.delete` — Q2 — plus `deliverable.create`) via
  `RegisterProposeKind`, each with Validate/DryRun/Apply/Rollback
  (`apply_criteria.go`, `apply_deliverable_create.go`). Apply mirrors the
  `handleCreate*`/`handlePatch*` SQL; `criteria.delete` is net-new and its
  Rollback re-inserts the captured row snapshot. The decide handler
  dispatches every kind generically through the registry
  (`LookupProposeKind`), so no per-kind wiring was needed. No bundled
  `policy.yaml` exists, so the kinds take the permissive `KindFor` default
  — `default_tier: principal` (director approval). Marking met/failed stays
  the P1 direct tool, not a propose.
- **P3 — AC-driven auto-advance. (SHIPPED.)** `maybeAutoAdvancePhase`
  (`phase_auto_advance.go`) fires from the criterion-satisfied paths
  (`handleMarkCriterion` and the gate cascade `cascadeDeliverableRatified`):
  it advances one phase when the current phase declares ≥1 required
  criterion and all are satisfied (met or waived — `requiredCriteriaCounts`
  total>0 && pending==0), emitting `project.phase_advanced` (via
  `auto-advance`) + hydrating the destination. An unmet required criterion
  blocks — the evaluator just doesn't fire (Q3); a phase with no required
  criteria waits for a manual REST advance rather than cascading forward.
  Gate criteria are the human-gate primitive (the ratify cascade fires
  them). Best-effort: a failure never fails the mark. **Retired** propose
  `phase.advance` (Q4) — deleted `apply_phase_advance.go`; the director's
  manual REST `/phase/advance` remains for off-criteria moves. No real
  data to migrate (alpha).

## References

- [ADR-044](../decisions/044-adaptive-project-lifecycle.md) — the locked
  decisions (Q1–Q4) this plan implements.
- [ADR-030](../decisions/030-governed-actions-and-propose-verb.md) — the
  governed-action / propose-verb model P2 builds on.

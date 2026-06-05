# 044. The project lifecycle is adaptive, not a fixed template contract

> **Type:** decision
> **Status:** Accepted (2026-06-05) — director resolved the four open
> questions (Q1–Q4 below). Director feedback on the deliverable/criteria/
> phase-advance model surfaced during code-migration lifecycle testing
> (issues [#18](https://github.com/physercoe/termipod/issues/18),
> [#19](https://github.com/physercoe/termipod/issues/19), and the gating
> discussion). Builds on the per-phase hydration foundation (issue #20,
> shipped) and the governed-action model of [ADR-030](030-governed-actions-and-propose-verb.md).
> **Audience:** contributors
> **Last verified vs code:** v1.0.803-alpha

**TL;DR.** A project template's `phase_specs` is currently a *fixed
contract*: deliverables hydrate as empty slots an agent can't fill,
acceptance criteria are immutable, and phase advance either ignores the
criteria (propose path) or hard-blocks on them (legacy REST). But a
template is a **draft roadmap** — as a project proceeds, new situations
arise and change is inevitable. We make the lifecycle adaptive: agents
**materialize** deliverables (and may propose new ones), criteria are
**editable via governed propose verbs**, and phase advance becomes
**AC-driven and system-approved**, with human gating expressed as a
`gate`-kind criterion rather than a separate approval step.

## Context

After issue #20 (per-phase hydration) a project's deliverables/criteria
panels reflect its template. But the model stops there:

- **Deliverables are empty draft slots.** Hydration creates a
  `deliverables` row (kind/required/ord); there is **no agent path to
  materialize it** — attaching the produced document/artifact/run/commit
  components is REST-only (`handlers_deliverables.go` `POST
  …/deliverables/{id}/components`), with no MCP tool. So an agent cannot
  hand the director something to review. `propose(deliverable.set_state)`
  can flip state but not attach content.
- **Criteria are immutable.** The only registered propose kinds are
  `agent.spawn / deliverable.set_state / phase.advance / task.set_status
  / template.install` (`apply_*.go`). There is no `criteria.create/
  update/delete`, so the gates a director ratifies against are frozen at
  template-author time.
- **Phase advance doesn't reflect the criteria.** The propose
  `phase.advance` apply **deliberately skips** AC gating —
  `apply_phase_advance.go:13-19`: *"the approver IS the gate."* The
  legacy REST path (`handlers_phase.go`) checks `requiredCriteriaPending`
  and 409s. Neither expresses "advance when the work is actually done."

The director's framing: the template **only restricts kind/type, not
content**; the content is the agent's job, and the roadmap must flex as
the project meets reality.

## Decision

1. **Agents materialize deliverables; new deliverables are governed.**
   A worker/project agent fills a hydrated draft deliverable by attaching
   the components it produced and moving it `draft → in-review` for
   director ratification. Working *one's own* draft deliverable is the
   agent's work product, done with **direct** MCP tools (not a propose):
   the agent may **attach, update, and remove** components and set
   `draft ↔ in-review` (Q1). Creating a **new** deliverable beyond the
   template changes the ratification surface, so it is a governed
   `deliverable.create` propose verb the director approves.
   (`deliverable.set_state → ratified` stays governed as today.)

2. **Acceptance criteria are editable via propose; agents can mark
   progress.** Criteria can be added, revised, or removed as the roadmap
   evolves. Because criteria *define* the gates the director ratifies
   against, **every definition change is a governed propose verb**
   (`criteria.create` / `criteria.update` / `criteria.delete`) requiring
   director approval. **Marking** a criterion is split by kind:
   - **`text` / `metric`** — the agent doing the work may mark it
     **met / failed** directly (new MCP action); the director can
     **revert / waive / override** at any time. This gives the worker a
     signal path so AC-driven auto-advance (decision 3) is agent-driven,
     while the director keeps the final word.
   - **`gate`** — stays chassis-evaluated: it auto-fires `met` via the
     gate cascade when its linked deliverable is ratified
     (`cascadeDeliverableRatified`); it cannot be marked by hand today
     (`handlers_criteria.go:371-373`) and that holds. The director's
     judgment enters here, through ratification.

   (Audit finding: marking is currently director-only REST with **no MCP
   path at all** — `server.go:545-547`; this decision opens the
   `text`/`metric` subset to agents.)

3. **Phase advance is AC-driven and system-approved.** Replace "the
   approver is the gate" with: the system **auto-advances** a phase once
   all *required* acceptance criteria for it are met. Where a human
   decision is genuinely required, it is modelled as a **`gate`-kind
   criterion** a human marks met — so the human-in-the-loop lives *inside*
   the criteria set, not as a separate phase-advance approval. An unmet
   *required* criterion (gate or not) **blocks** the advance — never a
   silent skip, never an attention-only nudge (Q3). The propose
   `phase.advance` verb is **retired** (Q4); the legacy 409 condition
   becomes the auto-advance trigger.

4. **Agents can read criteria *and* deliverables.** Blocking (decision 3)
   is only honest if the blocked agent can see the gate — and decision 1
   ("materialize the deliverable") is impossible if the agent can't see
   the draft slot it must fill or confirm an attach landed. Today neither
   is readable over MCP: criteria/deliverables are REST-only
   (`server.go:518-548`) with **no MCP tool** and **no MCP resource** (the
   only `termipod://…/deliverables/…` reference is the `mobile.navigate`
   URI, `tools.go:1077` — a phone destination, not an agent read), and
   `projects.get` returns the phase but not AC/deliverable state
   (`handlers_projects.go:67-69`). So this ADR adds an agent-facing read
   surface as a **prerequisite of P1**: `criteria.list`, `deliverables.list`
   / `deliverables.get` (with components), and a `phase.status` summary
   (required vs. met count, the blocking criteria, deliverable
   ratification states).

## Implementation surface (phased, hub-first, Go-testable)

- **P1 — read affordance + direct materialization/marking.** First the
  reads (decision 4): `criteria.list`, `deliverables.list` /
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
- **P3 — AC-driven auto-advance.** An evaluator that, when a required
  criterion is marked met (and after hydration), checks whether the
  current phase's required criteria are all satisfied and advances
  automatically (idempotent; emits `project.phase_advanced`). An unmet
  required criterion blocks (Q3). Gate criteria are the human-gate
  primitive. **Retire** propose `phase.advance` (Q4) — migration note for
  any current caller. This is the load-bearing change and lands last.

## Consequences

**Easier:** the template becomes a starting roadmap, not a frozen
contract; agents can actually produce reviewable deliverables; the
director's judgment is captured uniformly as gate criteria; the lifecycle
adapts to mid-flight reality.

**Harder / now constrained:** the governance surface grows (more propose
kinds — each needs the three-in-lockstep catalog/dispatcher/handler and a
policy tier); auto-advance needs careful "all required ACs met"
evaluation with idempotency and an audit trail; retiring propose
`phase.advance` is a behaviour change for any caller that proposes
advances today (migration note required).

**Out of scope:** deliverable *content* validation (the hub holds
metadata, not bytes — A3); cross-phase criteria; template versioning of
an in-flight project.

## Resolved (director, 2026-06-05)

- **Q1 — component handling.** Direct agent tools, and broader than just
  attach: the agent owns its draft, so it may **attach, update, and
  remove** components directly. Creating a *new* deliverable stays
  governed. (Folded into decision 1.)
- **Q2 — criteria verbs.** **Three verbs** — `criteria.create` /
  `criteria.update` / `criteria.delete` — not one `criteria.set`. Keeps
  each Apply/Rollback single-purpose, makes the director's approval card
  state the intent, and maps `create`/`update` onto existing handlers
  (only `delete` is net-new). Distinct from `deliverable.set_state`,
  which moves a deliverable *instance* through its ratification
  lifecycle; `criteria.*` edits the *rubric* itself. (Folded into P2.)
- **Q3 — unmet required AC.** **Block.** Never a silent skip, never
  attention-only. The blocked agent must be able to read the gate — which
  exposed that no such read affordance exists today for *either* criteria
  or deliverables (both REST-only, no MCP tool or resource), so decision 4
  adds reads for both. (Folded into decisions 3 + 4.)
- **Q4 — propose `phase.advance`.** **Retire it** entirely. Advance is
  now system-approved off the AC state; human judgment lives in `gate`
  criteria, not a separate override. (Folded into decision 3 + P3.)

## References

- Issues: #18 (MCP deliverable.create), #19 (MCP criteria.create), #20
  (per-phase hydration — the foundation, shipped).
- Code: `internal/server/handlers_deliverables.go` (incl.
  `handleGetProjectOverview` — the aggregate `phase.status` reuses),
  `handlers_criteria.go`, `apply_phase_advance.go`,
  `template_hydration.go`, `propose_kinds.go`.
- Audit (2026-06-05): full project-domain affordance sweep — REST routes
  (`server.go:472-551`) vs. both MCP registries (`toolspec.go`,
  `tools.go`, `native_tools.go`). Projects/plans/tasks/runs/artifacts/
  documents/channels/reviews are fully agent-reachable; the only gaps are
  deliverables (no MCP read/write), criteria (no MCP read/mark/edit), and
  a phase-status aggregate — i.e. exactly the lifecycle this ADR governs.
- Related ADRs: [030](030-governed-actions-and-propose-verb.md) (the propose
  verb), [029](029-tasks-as-first-class-primitive.md) (first-class units of work).

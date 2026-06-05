# 044. The project lifecycle is adaptive, not a fixed template contract

> **Type:** decision
> **Status:** Proposed (2026-06-05) — director feedback on the deliverable/
> criteria/phase-advance model surfaced during code-migration lifecycle
> testing (issues [#18](https://github.com/physercoe/termipod/issues/18),
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
   director ratification. Attaching to / drafting *one's own* deliverable
   is the agent's work product (direct MCP tool, not a propose). Creating
   a **new** deliverable beyond the template changes the ratification
   surface, so it is a governed `deliverable.create` propose verb the
   director approves. (`deliverable.set_state → ratified` stays governed
   as today.)

2. **Acceptance criteria are editable via propose.** Criteria can be
   added, revised, or removed as the roadmap evolves. Because criteria
   define the gates the director ratifies against, **every change is a
   governed propose verb** (`criteria.create` / `criteria.update` /
   `criteria.delete`, or a single `criteria.set`) requiring director
   approval. Marking a criterion met / failed / waived stays as the
   existing direct action.

3. **Phase advance is AC-driven and system-approved.** Replace "the
   approver is the gate" with: the system **auto-advances** a phase once
   all *required* acceptance criteria for it are met. Where a human
   decision is genuinely required, it is modelled as a **`gate`-kind
   criterion** a human marks met — so the human-in-the-loop lives *inside*
   the criteria set, not as a separate phase-advance approval. The
   propose `phase.advance` verb is retired (or repurposed to an explicit
   override); the legacy 409 condition becomes the auto-advance trigger.

## Implementation surface (phased, hub-first, Go-testable)

- **P1 — deliverable materialization.** An MCP tool to attach a
  component to a deliverable + transition `draft→in-review` (catalog
  entry + dispatcher + handler in lockstep; mirrors the REST path). No
  new governance.
- **P2 — `criteria.*` + `deliverable.create` propose kinds.** Register
  the verbs (`RegisterProposeKind`), with Validate/DryRun/Apply/Rollback,
  reusing the create/patch logic in `handlers_criteria.go` /
  `handlers_deliverables.go`. Policy tiers default to director approval.
- **P3 — AC-driven auto-advance.** An evaluator that, when a required
  criterion is marked met (and after hydration), checks whether the
  current phase's required criteria are all satisfied and advances
  automatically (idempotent; emits `project.phase_advanced`). Gate
  criteria are the human-gate primitive. Retire/repurpose propose
  `phase.advance`. This is the load-bearing change and lands last.

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

## Open questions for the director

- **Q1.** Component-attach: direct agent tool (agent owns its draft) vs.
  governed? Recommendation: direct for attach/draft, governed for create.
- **Q2.** `criteria.*` as three verbs or one `criteria.set`?
- **Q3.** On auto-advance, should a *non-gate* required criterion left
  unmet ever block silently, or always surface as an attention item?
- **Q4.** Retire propose `phase.advance` entirely, or keep it as an
  explicit principal override of the AC gate?

## References

- Issues: #18 (MCP deliverable.create), #19 (MCP criteria.create), #20
  (per-phase hydration — the foundation, shipped).
- Code: `internal/server/handlers_deliverables.go`,
  `handlers_criteria.go`, `apply_phase_advance.go`,
  `template_hydration.go`, `propose_kinds.go`.
- Related ADRs: [030](030-governed-actions-and-propose-verb.md) (the propose
  verb), [029](029-tasks-as-first-class-primitive.md) (first-class units of work).

---
name: Coordination basis and decision classification
description: Reads an external first-principles essay (the "gravity of coordination" / five-layer object model and its self-critique) against termipod's spine. The essay re-derives, for a single-principal cooperative harness, an operating model very close to what termipod already is — heaviest force is the human↔agent division of judgment; primary basis is managerial/delegation ("classify decisions, not tasks"); overlays are a control loop and an independent verifier; the layered object model is demoted to the harness's implementation blueprint, not the operating model. Maps that conclusion onto the existing axioms (A1/A3), permission-model, governance-roles, and orchestration-layer to validate convergence, then names five genuine gaps where the essay is sharper than the docs: (1) reversibility-by-design as an attention-buyback lever, (2) the generative decision-classification axes behind the binary "auto-allow routine / surface strategic", (3) "classify decisions, not tasks" as a named principle, (4) the escalation UX contract (options+tradeoffs+recommendation, batched, posed-not-asked), (5) recording the operating-vs-implementation basis distinction to guard against frame capture. Proposes a graduation path to a blueprint corollary / ADRs. No axiom doc is amended by this doc.
---

# Coordination basis and decision classification

> **Type:** discussion
> **Status:** Open (2026-05-30) — raised after reading an external
> first-principles essay on multi-agent coordination against the
> spine. The essay's conclusion converges strongly with termipod's
> existing axioms; this doc records that convergence and isolates the
> handful of places where the essay is sharper than the docs, as
> proposals that can graduate to a [`blueprint.md`](../spine/blueprint.md)
> corollary or new ADRs. **This doc amends no axiom** — it is the
> "surface the gap for discussion" step the
> [contributing conventions](../../CLAUDE.md) ask for before touching
> load-bearing design.
> **Audience:** contributors
> **Last verified vs code:** v1.0.751

**TL;DR.** An external essay derives, from first principles, the
"best basis" for a single-principal cooperative agent harness whose
purpose is to route the irreducibly-human work (strategy, judgment,
taste, decision) to the human and absorb everything else. Its answer:
the **heaviest coordination force is the division of cognitive labor
between human and agent** — not implementation — so the operating
model should be a **managerial / delegation basis** ("classify
decisions, not tasks"), overlaid with a **control loop** (per
delegated unit) and an **independent verifier** (correctness gate
before anything reaches the human); the layered "object model" of the
software is *demoted* to the harness's implementation blueprint, not
the operating model. **Termipod already embodies most of this** —
axioms A1/A3, the permission model's "auto-allow routine, surface
strategic", the Director role, the steward/worker split, and the
orchestration layer's loop-closure are the same conclusions under a
governance/filtering vocabulary. The essay contributes five sharper
points the spine does not yet state, listed in §3. The single
highest-leverage one is **reversibility-by-design as an attention
lever**.

---

## 1. The essay's argument, compressed

The essay runs in three moves.

1. **A "coordination force field."** Architecture is "frozen
   coordination"; wherever many parties must agree on one
   schema/contract/invariant, coordination pressure concentrates into
   a *load-bearing* element (a "pillar"). An agent system stands in a
   small number of irreducible relations (to its purpose-giver, to its
   own continuity, to the world, to peers, to a standard, to
   consequences, to itself-as-unit). These relations are the invariant
   object.

2. **No privileged decomposition.** A "five-layer object model"
   (Cognitive / Organizational / Communication / Execution / Meta) is
   *one basis* in which to express that field — good for *buildability*
   and blind to conflict, dynamics, and emergence. Other bases
   (economic/game-theoretic, control-loop, dependability, field/swarm,
   Marr's levels) each diagonalize a different force and blind a
   different one. **Completeness is task-relative**: a basis is
   complete for *your* task iff it has no "pillar-shaped silence" with
   respect to the forces that actually bear load in your system.
   Treating any one basis as privileged is *frame capture*.

3. **Run the selection algorithm on this task.** For a *single-
   principal, cooperative* harness where the human supplies
   strategy/judgment/taste and agents execute verifiable-ish work
   (research, coding, building artifacts):
   - The economic/game-theoretic basis is *killed* as primary — there
     are no self-interested adversaries, one human owns everything.
   - The heaviest force is the **human↔agent division of judgment**:
     escalate too much and the human is babysitting (leverage
     destroyed); escalate too little and the agent silently makes a
     taste/strategy call it had no business making.
   - **Primary basis = managerial/delegation. The crux is "classify
     decisions, not tasks":** a task contains interleaved decisions of
     different classes; delegating by task either loses control of the
     human-class decisions buried inside or claws the whole task back
     to review. Delegate by *decision* and agents fly through the
     mechanical ones, surfacing only the ones that need the human.
   - The classification rule (a generative heuristic, not a literal
     formula): a decision is **human-class** when it is irreversible,
     low-verifiability, wide-blast-radius, or taste-laden; **agent-
     class** when reversible, cheaply checkable, narrow, and
     mechanical. Schematically `escalate ∝ (irreversibility ·
     blast_radius · taste_load) / verifiability > θ`.
   - **Overlays:** a **sense–decide–act–learn control loop** with
     loop-breakers (max iterations, stuck-detection) inside each
     delegated unit; an **independent verifier** (separate from the
     executor, "doesn't grade its own homework") that hard-gates
     output against acceptance criteria before it reaches the human.
   - **The layered object model is demoted** to "the blueprint for
     building the harness software," *not* the model for structuring
     the work.
   - **MVP shape:** orchestrator/lead-proxy · executor agents ·
     independent verifier · context store; three gates in attention-
     economizing order — (1) cheap **intent/spec gate** up front
     (approve the plan once before expensive execution), (2) silent
     **autonomous execution** (no interruptions), (3) **verify +
     escalate** after, with escalations **batched and posed as a
     choice with options + tradeoffs + a recommendation**, never an
     open-ended "what now?".
   - **Deliberately left out for MVP:** game-theoretic mechanism
     design, swarm/emergence, heavy information-theoretic optimization
     — low gravity for a single-principal cooperative case; adding
     them buys brittleness disguised as thoroughness.

The independent reading worth keeping: the essay is not proposing a
new architecture so much as **giving a vocabulary** (forces, bases,
decision-classes) for the one termipod already chose, plus a few
levers termipod under-uses.

---

## 2. Validation map — the essay against the spine

The convergence is the primary result. Each essay claim already has a
home in the spine, usually under a governance/filtering vocabulary
rather than a delegation one.

| Essay claim | Where termipod already states it |
|---|---|
| Attention is the scarce resource; it is the heaviest force | **Axiom A1** ([`blueprint.md` §2](../spine/blueprint.md)); "realization efficiency *is* the economy of scarce attention" ([`orchestration-layer.md` §6](../spine/orchestration-layer.md)) |
| Bounded autonomy: a rule must exist before the action | **Axiom A3** ([`blueprint.md` §2](../spine/blueprint.md)) |
| Classify into delegate-freely vs surface-to-human | "auto-allow routine, surface strategic" design principle ([`permission-model.md`](../reference/permission-model.md)); the two-layer tool-call-gate vs attention-gate split |
| Human-class decision owner | the **Director** framing — "approve a plan, ratify a result, decide between options" ([`governance-roles.md`](../spine/governance-roles.md)) |
| Manager that routes judgment up; IC that executes | the **steward / worker** split and the manager/IC invariant ([`governance-roles.md`](../spine/governance-roles.md); [`blueprint.md` §3.3](../spine/blueprint.md)) |
| Tier-based escalation of governed actions | the four-tier authorization ladder ([ADR-030](../decisions/030-governed-actions-and-propose-verb.md)) |
| Control loop + loop-breakers + escalation on stall | loop-closure invariant + "no node may be a silent sink" ([`orchestration-layer.md` §3](../spine/orchestration-layer.md); [ADR-034](../decisions/034-orchestration-loop-closure.md)) |
| Intent/spec gate up front; verify before presenting | project phase gating with acceptance criteria; deliverable reviews ([ADR-025](../decisions/025-project-steward-accountability.md); ADR-029 tasks) |
| The layered model is *one basis*, a lens not a box | already stated: "the orchestration layer is a lens, not a fourth box" ([`orchestration-layer.md` §1](../spine/orchestration-layer.md)) — termipod is already partly **basis-fluent** |
| Deliberately leave out adversarial / multi-tenant scope | **Non-goals** ([`blueprint.md` §10](../spine/blueprint.md)) reject single-agent-UX competition, multi-tenant gating, etc. |

The takeaway: termipod's three axioms (filtering / distribution /
governance) already cover the force profile the essay arrives at. The
essay's "managerial/delegation primary basis" is termipod's
Director↔steward↔worker structure seen from the judgment-routing end
instead of the governance end. **They are the same architecture.**

---

## 3. The five gaps — where the essay is sharper than the docs

These are the places where the spine has the *mechanism* but not the
*principle*, or states a coarser version. Each is a proposal, with a
suggested home, for graduation (§4).

### 3.1 Reversibility-by-design as an attention-buyback lever  *(highest leverage)*

The essay's sharpest move is that the escalation pressure
`(irreversibility · blast_radius · taste_load) / verifiability` has a
**designable numerator**: making an action reversible (checkpoints,
branches, sandboxes) or cheaply verifiable converts a *human-class*
decision into an *agent-class* one, and thereby **buys back the
principal's attention** — the scarcest resource (A1).

Termipod has the *mechanisms* — git worktrees per worker, "terminate
is reversible-via-respawn" ([ADR-025](../decisions/025-project-steward-accountability.md)),
ad-hoc reversibility/cost-to-reverse tables in
[ADR-023](../decisions/023-agent-driven-mobile-ui.md) and
[ADR-024](../decisions/024-project-detail-chassis.md) — but **no
governing principle** that says *prefer reversible-by-default
constructions specifically to demote decisions out of the human's
queue*. Today reversibility is treated as a per-decision property to
note, not as a **lever to pull** on the attention budget. Naming it
would give template/policy authors a reason to add a checkpoint or a
sandbox: not "for safety" but "to stop paying the principal's
attention for this."

*Proposed home:* a corollary in [`forbidden-patterns.md`](../spine/forbidden-patterns.md)
or a short [`blueprint.md`](../spine/blueprint.md) amendment under A1/A3
("reversible-by-default to buy back attention"), with the mechanism
catalog (worktrees, checkpoints, dry-run, respawn) cited.

### 3.2 The decision-classification is binary + enumerated, not generative

[`permission-model.md`](../reference/permission-model.md) classifies
*routine* vs *strategic*; [ADR-030](../decisions/030-governed-actions-and-propose-verb.md)
enumerates governed-action *kinds* in a per-(kind, tier) policy table.
Neither records the **axes** that explain *why* a kind is strategic or
sits at a given tier — reversibility, verifiability, blast-radius,
taste-load. A template author classifying a *new* action kind has the
table to copy from but no rule to derive from. Writing the axes down
turns the enumerated policy into a principled one and makes
`governed-actions.yaml` extensible by reasoning rather than by
precedent.

*Proposed home:* enrich [`permission-model.md`](../reference/permission-model.md)
(the "auto-allow routine, surface strategic" section) with the axes,
and reference them from [ADR-030](../decisions/030-governed-actions-and-propose-verb.md)'s
policy rationale.

### 3.3 "Classify decisions, not tasks" — unnamed

Termipod's first-class unit of dispatched work is the **Task**
(ADR-029). The essay's warning is that a task contains interleaved
decision-classes, so task-granularity delegation tends to either bury
human-class decisions or force a whole-task claw-back. Termipod's
escape valve already exists — governed actions and attention items
fire *mid-task*, at the decision, not at task boundaries — so the
mechanism is sound. What is missing is the *named principle* that
tells a steward prompt / template author to **surface at the decision,
not at the task**. Stating it would tighten prompt authoring and
explain why mid-task escalation is correct rather than a leak.

*Proposed home:* a note in [`orchestration-layer.md`](../spine/orchestration-layer.md)
(the loop is the unit of *liveness*; the decision is the unit of
*delegation*) or [`governance-roles.md`](../spine/governance-roles.md)
(steward responsibility wording).

### 3.4 The escalation contract is under-specified — and it is *per request-kind*

The attention primitives exist — `request_approval`, `request_select`
(carries options), `request_help` ([`attention-interaction-model.md`](attention-interaction-model.md);
[ADR-020](../decisions/020-director-action-surface.md);
[ADR-011](../decisions/011-turn-based-attention-delivery.md)). What was
not stated as a **contract** is the *form* an escalation must take — and
the key refinement (raised in review) is that the form is **not uniform;
it depends on the kind of act**:

- a **decision** (`request_approval` / `request_select` / a `propose`)
  is *posed* — concrete options + tradeoffs + a recommended default,
  never an open-ended "what now?";
- a **help / clarification** (`request_help`) instead carries concrete
  **situational context** — what was tried, what is blocking, the
  specific info needed — so the higher tier grasps the situation. Forcing
  an options list on an open question is as wrong as posing a bare
  decision.

This is a per-kind **communication contract between actors** — the
agent→principal case of the orchestration layer's typed envelope
(`kind ∈ {directive, question, report, notification}`). It is an A1
(attention-economy) obligation: the wrong form spends the principal's
judgment on framing instead of deciding (decision) or on reconstructing
context (help).

*Landed:* the contract clause is in [`governance-roles.md`](../spine/governance-roles.md)
(steward responsibility) and carried — prompt-soft — by the 8 bundled
steward prompts. Hard enforcement (a hub validator) is the deferred
3.4-code in §4.

### 3.6 Execution-policy loop-breakers are time/spend-based, not failure-based

*(Added 2026-05-30, from a review question — does the harness bound
agent execution by failure count / retries / exceptions?)* The
discussion's control-loop overlay calls for **loop-breakers (max
iterations, stuck-detection)**. Verified against the code, termipod
implements the *time / spend / stuck* breakers but **not the
failure-count or iteration ones**:

| Breaker | In code |
|---|---|
| inactivity 20m → escalate; absolute 2h → terminate | ✅ `loop_sweep.go` (ADR-034) |
| spend cap (`budget_cents`) → pause + attention | ✅ `budget.go` |
| stall → widen assignees; stuck-pane → attention | ✅ `escalation.go`, `runner.go` |
| dead pane → `crashed` | ✅ `reconcile.go` |
| **consecutive-failure cap / retry cap** | ❌ none — retries are ad-hoc, unbounded |
| **iteration / turn cap** | ❌ none |
| **in-loop exception recovery (`recover()`)** | ❌ none — failures surface as terminal states, not bounded retries |

The consequence: an agent that fails the same compile 30× silently
burns budget until the **2h absolute cap or the spend cap** trips —
coarse backstops, not a tight loop-breaker. A failure-cap that breaks at
failure #N and escalates is the missing piece; its code spec is the
3.6-code candidate in §4. Risk to weigh (a default-correctness concern): the failure *classifier*
is the load-bearing part — false positives pause healthy agents — so it
should default to **escalate, not terminate** (cheap-to-recover),
consistent with the §3.1 reversibility corollary.

### 3.5 Record the operating-vs-implementation basis distinction

The essay's meta-point — that the operating model (human↔agent
judgment division) and the software's structural layering are
*different bases*, and conflating them is frame capture — is worth
one explicit paragraph. [`orchestration-layer.md`](../spine/orchestration-layer.md)
already demonstrates the instinct ("a layer is a lens, not a box");
making the *operating* basis explicit (and naming the heaviest force
as the human↔agent judgment boundary) would inoculate future design
against importing an implementation-shaped taxonomy as if it were the
way work is structured. The **Non-goals** ([`blueprint.md` §10](../spine/blueprint.md))
already encode the "deliberately leave out" half (no game theory, no
swarm, no multi-tenant) — this gap is only the positive half: *name
the basis we did choose, and why.*

*Proposed home:* a short framing paragraph in [`blueprint.md`](../spine/blueprint.md)
§2 (after the three axioms) or the head of
[`orchestration-layer.md`](../spine/orchestration-layer.md).

---

## 4. Graduation path — principle now, code only when driven

**Correction (2026-05-30).** An earlier draft of this section proposed
folding 3.1 + 3.2 into "a single new ADR." That conflated two different
things. In this repo an **ADR records a decision realized in code**,
usually with a rollout plan — the exemplar is
[ADR-030](../decisions/030-governed-actions-and-propose-verb.md) →
[`governed-actions-mvp-rollout.md`](../plans/governed-actions-mvp-rollout.md)
(~1300–1500 LOC, a `policy.go` schema change, five `apply_*.go`
handlers). All five gaps in §3, *as stated*, are **principles** — they
are doc enrichments with **zero code**. They do not need an ADR.

**What was done now (doc-only).** Each principle was enriched into its
best-fit spine doc, with a cross-reference back here:

| Gap | Landed in |
|---|---|
| 3.1 reversibility lever | [`blueprint.md`](../spine/blueprint.md) §2 — corollary under the axioms |
| 3.2 classification axes | [`permission-model.md`](../reference/permission-model.md) — the "auto-allow routine, surface strategic" section |
| 3.3 classify decisions, not tasks | [`orchestration-layer.md`](../spine/orchestration-layer.md) §3 — the decision is the unit of delegation |
| 3.4 escalation contract (**per request-kind**) | [`governance-roles.md`](../spine/governance-roles.md) — the steward's surface-work responsibility, **plus the 8 bundled steward prompts** (behaviour-as-data) |
| 3.5 operating-vs-implementation basis | [`orchestration-layer.md`](../spine/orchestration-layer.md) §1 — extends "a layer is a lens, not a box" |

**What is deferred to an ADR (the *code* versions).** An ADR earns its
place only if/when a concrete driver makes us build the machinery —
and the code surfaces are real and verified, not hypothetical:

- **3.2-code — an axis-driven classifier.** The governed-action policy
  ships today as a hand-authored `kinds:` block in `policy.yaml`
  (`hub/internal/server/policy.go`, `Kinds map[string]KindPolicy`; the
  tier is carried as `AssignedTier`). The code version adds
  reversibility/verifiability/blast-radius/taste-load fields per kind
  and a resolver that *computes* the tier from the axes instead of
  hand-authoring it. New schema + resolver + tests.
- **3.1-code — flip a default.** Worker worktrees are `optional` today
  (`hub/internal/hostrunner/spec.go`). Making worktree/checkpoint
  reversible-by-**default** so the steward can auto-demote decisions is
  a behaviour/policy change.
- **3.4-code — enforce, don't just prompt.** A hub-side validator on
  attention payloads (a *decision*-kind item must carry ≥2 options + a
  recommendation; a *help*-kind item must carry context) would make the
  §3.4 contract hard rather than prompt-soft.
- **3.6-code — failure-cap loop-breaker (see §3.6).** A
  consecutive-failure counter + threshold that breaks a thrashing agent
  loop and escalates, complementing the existing time/spend breakers in
  `loop_sweep.go` / `budget.go`. The likely largest of these; the code
  spec is in §3.6.

Per the essay's own anti-pillar-inflation rule — and §5's first open
question — the disciplined MVP call is **document the heuristic, build
the classifier later**. The hand-authored `kinds:` table already works
for a single principal; an axis-resolver is additive value, not a
current need. So: **no ADR is opened now.** When a driver becomes
concrete, two ADRs are pre-shaped: *"Decision classification and the
reversibility lever"* (3.1-code + 3.2-code travel together, since the
reversibility lever is one of the classification axes) and *"Execution
loop-breakers: failure-cap policy"* (3.6-code, companion to ADR-034's
time-based breakers).

New terms this doc coins locally — *coordination force*, *basis /
projection*, *decision class (human-class / agent-class)*,
*reversibility lever*, *attention buyback* — are **discussion-local**
and deliberately **not** added to [`glossary.md`](../reference/glossary.md)
yet. If any graduate into an axiom or ADR, they get a canonical
glossary entry at that point, per the glossary-first convention.

## 5. Open questions

- Is the multi-axis classification worth formalizing into
  `governed-actions.yaml` (a per-kind axis annotation), or is the
  enumerated table plus a documented heuristic enough for a single
  principal? (Pillar-inflation risk: don't over-specify the
  orchestrator for the modal cooperative case.)
- Does the reversibility lever change any *default* — e.g. should
  worker worktrees + checkpointing be on-by-default precisely so the
  steward can auto-demote more decisions? That is a policy default, not
  just a doc.
- The essay's "independent verifier, separate from the executor" —
  termipod's reviews are often steward-reviews-worker. Is that
  *independent enough*, or does the gap (§3 of
  [`agent-driven-system-probing.md`](agent-driven-system-probing.md))
  warrant a distinct verifier role post-MVP?

## 6. Cross-references

- [`blueprint.md`](../spine/blueprint.md) — the three axioms this maps onto.
- [`orchestration-layer.md`](../spine/orchestration-layer.md) — loop-closure, the "lens not a box" basis instinct.
- [`governance-roles.md`](../spine/governance-roles.md) — Director / steward / worker ontology.
- [`permission-model.md`](../reference/permission-model.md) — "auto-allow routine, surface strategic".
- [ADR-030](../decisions/030-governed-actions-and-propose-verb.md) — governed actions + tier ladder.
- [ADR-034](../decisions/034-orchestration-loop-closure.md) — loop-closure runtime.
- [`attention-interaction-model.md`](attention-interaction-model.md) — the attention gate primitives.

---
name: Governed actions and the propose verb
description: Generalise apply-on-approve to a single MCP verb `propose(kind, target_ref, change_spec, ...)` covering deliverable.set_state, phase.advance, task.set_status, and re-addressing of worker permission_prompt. Adds a 4-tier authorisation ladder (worker / project-steward / general-steward / principal), per-(kind, tier) policy, principal-override audit, and one-cycle deprecation aliases for approval_request+spawnIn and template_proposal. MVP single-principal with multi-member schema hooks.
---

# 030. Governed actions and the `propose` verb

> **Type:** decision
> **Status:** Proposed (2026-05-17) — D-1 through D-9 locked in
> the 2026-05-17 design conversation following the
> [tool-call-approval-patterns reference](../reference/tool-call-approval-patterns.md)
> and [worker-permission-routing discussion](../discussions/worker-permission-routing-to-steward.md).
> Amended 2026-05-20 — see §Amendments at end (D-7 superseded by
> Option 2′; reconciliations with ADR-032 + ADR-034; principal vs
> owner; line-ref drift fixed).
> **Audience:** contributors
> **Last verified vs code:** v1.0.685-alpha
> **Freshness:** contract

**TL;DR.** Promote *apply-on-approve* from two bespoke branches
(`approval_request+spawnIn` → `DoSpawn`; `template_proposal` →
`installProposedTemplate`) to a single generic MCP verb,
`propose(kind, target_ref, change_spec, reason, addressee_tier?,
dry_run?)`. Introduce the term pair **governed action** (the
umbrella category for operations the system gates on
authorisation) and **commit** (the subset that updates the
project's canonical record). Define a four-tier authorisation
ladder — worker / project-steward / general-steward / principal
— sourced from [spine/governance-roles.md](../spine/governance-roles.md);
no new role is added. Per-(kind, tier) policy lives in
`team/policy/governed-actions.yaml`. MVP ships four kinds
(`deliverable.set_state`, `phase.advance`, `task.set_status`,
`worker_tool_call.escalate`); the first three are propose
kinds, the fourth re-addresses the existing `permission_prompt`
row to the project steward. MVP keeps quorum trivial (M=1 of N
at every tier) and ships **no auto-escalation** (no timeout
daemon, no reject-bubble); reject is terminal at the addressed
tier. Principal-override of any lower-tier decision is allowed
with no time window. Existing apply-on-approve branches become
one-cycle deprecated aliases. The full design rationale is in
[discussions/governed-actions-and-propose-verb.md](../discussions/governed-actions-and-propose-verb.md);
the work is in
[plans/governed-actions-mvp-rollout.md](../plans/governed-actions-mvp-rollout.md).

## Context

[Blueprint](../spine/blueprint.md) axiom A3 requires that *every
autonomous action must be bounded by a rule that existed before
the action happened*. Two of termipod's load-bearing actions
honour A3 today via system-side apply-on-approve:

1. `agent.spawn` of a worker by a non-steward — gated through
   `approval_request + spawnIn` payload; the hub calls
   `DoSpawn(...)` only after `/decide(approve)` at
   `handlers_attention.go:378`.
2. `template.install` of a new bundled template — gated through
   `template_proposal`; the hub calls `installProposedTemplate(...)`
   only after `/decide(approve)` at `handlers_attention.go:400`.

Everything else load-bearing in termipod's project record
(deliverable state transitions, phase advance, task close-out,
worker per-call tool gates) is honoured by *prompt convention
only*. A steward whose prompt told it to call
`request_approval(...)` first could in principle write the
deliverable file itself in the same MCP turn; nothing in the
verb layer prevents it. The same pattern leaves worker
`permission_prompt` rows team-wide-addressed, putting every
gated tool call from every worker on the principal's mobile Me
inbox even though [ADR-025](025-project-steward-accountability.md)
D3 makes the project steward accountable for its workers.

The class of problems shares structure:

- An agent wants to perform a state change.
- The change is observable to other actors (downstream agents,
  the principal, the future audit reader).
- The change is hard to undo without losing trust or
  consistency.
- The natural authoriser is not always the principal — sometimes
  it's the project steward (the agent that owns the project's
  workers), sometimes it's the general steward.

The two existing apply-on-approve branches are special cases of
a generic verb. Generalising them lets us extend the
*system-applies-on-approve* discipline to every load-bearing
state change without inventing a new attention kind per case,
and gives us a single surface to attach tier-aware authorisation
to.

Three pressures forced the decision now:

1. **The principal's session walkthrough (2026-05-17)** surfaced
   four bugs that all reduce to "approve isn't load-bearing
   enough" — see
   [discussions/governed-actions-and-propose-verb.md §1](../discussions/governed-actions-and-propose-verb.md#1-why-this-is-the-problem).
2. **The worker-permission-routing discussion** parked the same
   question for a single kind (worker tool calls). Solving it in
   isolation would add a third bespoke routing path; solving it
   generically retires that discussion and unlocks the rest of
   the category at the same cost.
3. **Multi-member is post-MVP but on the roadmap.** Schema
   choices made now (tier addressing, per-(kind, tier) policy,
   override audit row) are cheap; retrofitting them onto more
   bespoke branches would be expensive.

This ADR records the decisions. The discussion records the
alternatives, the terminology trade-offs, and the prior-art
audit.

## Decisions

### D-1. `propose` is the single MCP verb for system-applies-on-approve mutations

A new MCP tool `propose(kind, target_ref, change_spec, reason,
addressee_tier?, dry_run?)` returns `{request_id,
status:"awaiting_response"}` immediately (Pattern C in
[tool-call-approval-patterns.md](../reference/tool-call-approval-patterns.md))
and raises a new `attention_items` row.

- `kind` (string, required): dispatcher key — see D-4 for the
  MVP set.
- `target_ref` (object, required): identifies the row(s) to
  mutate. Shape per-kind; always includes `project_id` for
  project-scoped kinds.
- `change_spec` (object, required): the payload the system will
  apply on approve. Shape per-kind.
- `reason` (string, required): why the agent is asking. Stored
  in the row and shown to the authoriser.
- `addressee_tier` (string, optional): hint — `steward` or
  `principal`. Policy may override.
- `dry_run` (boolean, default false): if true, the
  awaiting_response payload also contains the diff the system
  *would* apply, so the authoriser can preview before deciding.
  The attention row is still created — `dry_run` is a preview
  flag on the *content* shown to the authoriser, not a
  "don't persist" flag.

On `/decide(approve)` (`handlers_attention.go` decide handler),
the dispatcher reads `attention_items.change_kind` and calls
`applyGovernedAction(kind, target_ref, change_spec)`. The result
is written to `attention_items.executed_json` and returned to
the authoriser as part of the decide response — same shape as
today's `executed` field for spawn and template-install.

On `/decide(reject)`, the existing fan-back via
`dispatchAttentionReply` delivers `{request_id, kind:"propose",
change_kind, decision:"reject", reason}` to the requester's
session as a fresh `input.attention_reply` event. The requester
sees the rejection on its next turn.

The verb is **agent-callable** (steward + worker, gated via
`hub/config/roles.yaml` per
[ADR-016](016-subagent-scope-manifest.md) D6).

### D-2. Terminology — governed action + commit

The system adopts a term pair:

- **Governed action.** Any operation the system gates on
  authorisation before applying. Includes `agent.spawn`,
  `template.install`, `permission_policy.change`,
  `artifact.publish`, and every other future kind. The category
  is defined by *whether the verb layer enforces the gate*, not
  by whether the operation changes data (every write does).
- **Commit.** The subset of governed actions that update the
  project's canonical record-of-truth — currently
  `deliverable.set_state`, `phase.advance`, `task.set_status`.
  Atomic, audited, observable by downstream actors who read
  those rows to decide their next move. Per-(kind) policy in D-6
  carries a `commits: true|false` boolean.

The pair earns the distinction in the multi-member future
(commits may require M>1 of N principals; non-commit governed
actions may not). In MVP single-principal it's a decorative
distinction, but the vocabulary is cheap and matters for the
follow-up ADR.

Rejected names: *mutation* (already used in
[fork-and-engine-context-mutations](../discussions/fork-and-engine-context-mutations.md)
for engine-context state and in
[reference/glossary.md](../reference/glossary.md) for audit-event
sense); *authorised act* (loses policy framing); *privileged
operation* (OS-kernel connotation); *covenant* / *act of record*
(legalistic). Full alternative analysis in
[discussion §3](../discussions/governed-actions-and-propose-verb.md#3-terminology-governed-action-vs-commit).

### D-3. Four-tier authorisation ladder

Sourced verbatim from
[spine/governance-roles.md](../spine/governance-roles.md). No
new role is added; this ADR adds an *authorisation layer* over
the existing role ontology.

| Tier | Canonical name | Population (MVP) | Authority |
|---|---|---|---|
| `worker` | worker | many | Proposes governed actions; cannot authorise |
| `project-steward` | domain steward | 1 per engaged project | Authorises kinds with `default_tier="project-steward"`; cannot override principal |
| `general-steward` | general steward (`@steward`) | 1 per team | Authorises kinds with `default_tier="general-steward"`; cannot override principal |
| `principal` | principal (director framing at decide-time) | 1 in MVP, many in multi-member | Authorises kinds with `default_tier="principal"`; **may override any lower-tier decision** |

The shorter aliases (`project-steward`, `general-steward`) match
prevailing conversation in `governance-roles.md`. The canonical
forms (domain steward / general steward) remain valid in prose;
the ADR uses the hyphenated forms as policy-file values for
machine parsing.

### D-4. MVP propose kinds — three new + one routing extension

| Kind | Type | change_spec shape | Apply function | Default tier | commits |
|---|---|---|---|---|---|
| `deliverable.set_state` | propose | `{state: "draft" \| "in-review" \| "ratified" \| "failed" \| "withdrawn", reason?}` | `setDeliverableState(target_ref.deliverable_id, change_spec.state, via=propose)` | principal | yes |
| `phase.advance` | propose | `{from_phase, to_phase, reason?}` | `advanceProjectPhase(target_ref.project_id, change_spec.to_phase)` | principal | yes |
| `task.set_status` | propose | `{status: "done" \| "cancelled", result_summary?}` | `setTaskStatus(target_ref.task_id, change_spec.status, ...)` (extends ADR-029 D-3) | project-steward | yes |
| `worker_tool_call.escalate` | re-addressing (not a propose) | n/a — re-addresses existing `permission_prompt` rows | (no new dispatcher; uses existing parked-RPC / sync-MCP machinery) | project-steward | no |

`worker_tool_call.escalate` is deliberately **not** a new
propose kind because the codex parked-RPC and claude sync-MCP
machinery already exists. It's a routing extension to the
existing `permission_prompt` row: when raised by a worker whose
`parent_agent_id` is a steward, the row is stamped with
`assigned_tier = "project-steward"` and addressed to the parent
steward's session. Same approve dispatch (the engine driver still
unparks the call); just different addressee. This corresponds to
Option A in
[worker-permission-routing-to-steward.md §3](../discussions/worker-permission-routing-to-steward.md#3-what-steward-as-first-line-approver-would-look-like).

The deferred propose kinds (post-MVP, listed so future PRs don't
re-litigate scope): `criterion.set_state`, `agent.terminate`,
`agent.archive`, `permission_policy.change`, `artifact.publish`,
`project.archive`, `project.update_metadata`. Each is "add an arm
to the kind dispatcher" with no schema change.

### D-5. Deprecation of the two existing apply-on-approve paths

The two existing branches become one-cycle deprecated aliases:

| Existing kind | Becomes | Migration |
|---|---|---|
| `approval_request` with `spawnIn` payload in `pending_payload_json` | `propose(kind="agent.spawn", target_ref={project_id}, change_spec={template, handle, ...})` | The decide handler's spawn dispatcher recognises both shapes; new code emits the propose shape, old MCP calls still resolve. |
| `template_proposal` | `propose(kind="template.install", target_ref={team_id}, change_spec={category, name, blob_sha256})` | `installProposedTemplate` becomes the apply function for the `template.install` kind. |

One-cycle = until the multi-member/escalation follow-up ADR
lands. At that point the aliases are removed; clients must use
the propose form.

### D-6. Per-(kind, tier) policy file

A new file `team/<team>/policy/governed-actions.yaml` is the
single source of truth for "who decides what":

```yaml
version: 1

kinds:
  deliverable.set_state:
    default_tier: principal
    quorum:
      principal: { M: 1 }
    commits: true
    override_allowed: true
    escalate_on_reject: false       # MVP: never
    escalate_on_timeout: false      # MVP: never (no timeout daemon)

  phase.advance:
    default_tier: principal
    quorum:
      principal: { M: 1 }
    commits: true
    override_allowed: true
    escalate_on_reject: false
    escalate_on_timeout: false

  task.set_status:
    default_tier: project-steward
    quorum:
      project-steward: { M: 1 }
    commits: true
    override_allowed: true
    escalate_on_reject: false
    escalate_on_timeout: false

  worker_tool_call.escalate:
    default_tier: project-steward
    quorum:
      project-steward: { M: 1 }
    commits: false
    override_allowed: true
    escalate_on_reject: false
    escalate_on_timeout: false

  # Deprecated propose-aliases (one-cycle compatibility per D-5)
  agent.spawn:
    default_tier: principal
    quorum:
      principal: { M: 1 }
    commits: false
    override_allowed: true

  template.install:
    default_tier: principal
    quorum:
      principal: { M: 1 }
    commits: false
    override_allowed: true
```

The file is read at hub start; reads are cached per-team and
invalidated on file mtime change. The reader is a small
go-yaml deserialiser into a typed `GovernedActionPolicy` struct.

[ADR-016 D6](016-subagent-scope-manifest.md)'s
`hub/config/roles.yaml` is the *role manifest* (which roles can
call which `hub://*` tools). The new file is the *action policy*
(per kind, who authorises, what quorum). The two are sibling
surfaces with different lifecycles — roles change rarely,
action policy changes with the kind set.

### D-7. Quorum and escalation — MVP keeps both trivial

| Concern | MVP value | Rationale |
|---|---|---|
| Quorum at each tier (M of N) | M=1 of N | Single-principal; nothing to aggregate |
| Auto-escalate on `/decide(reject)` | None | Reject is terminal at the addressed tier. The requester may re-propose with new information — convention only, encoded in steward prompts. |
| Auto-escalate on timeout | None | No timeout daemon ships. Stale rows remain `open`; the principal will notice if project progress stalls. |
| Re-propose-to-higher-tier by requester | Convention only | Prompt rule: "if a propose is rejected, do not immediately re-propose to a higher tier. Re-examine the reason; only re-propose if new information is available." |

All schema hooks for the multi-member future are in place
(`assigned_tier`, `current_assignees_json` array, per-(kind, tier)
policy file). The follow-up ADR is purely state-machine work.

### D-8. Principal override of lower-tier decisions

The principal may override **any** lower-tier decision (approve
or reject) on any kind with `override_allowed: true`. No time
window — override is allowed anytime until the next governed
action invalidates the row (e.g. a subsequent
`deliverable.set_state.unratified` invalidates a prior
`deliverable.set_state.ratified`).

Override mechanism: the principal calls
`/decide(approve|reject)` on a row that's already resolved by
a lower tier. The decide handler detects the override, appends
a new `decisions_json` entry, and emits a new
`audit_events.action="attention.override"` row carrying:

```json
{
  "original_decision": "approve",
  "original_tier": "project-steward",
  "original_by": "<lower-tier agent_id or handle>",
  "override_decision": "reject",
  "override_tier": "principal",
  "override_by": "<principal handle>",
  "kind": "<change_kind>",
  "reason": "<override reason from /decide body>"
}
```

When override changes the decision (approve → reject or
vice-versa), the apply function runs (or is undone, where
possible) per kind-specific semantics:

- For `agent.spawn`: override-to-reject after spawn cannot
  un-spawn; instead, the override emits a follow-up `agent.terminate`
  governed action with the override audit row as cause.
- For `template.install`: override-to-reject after install
  rolls back the install (deletes the file, restores prior
  version if any). The override audit row records the rollback.
- For `deliverable.set_state` / `phase.advance` / `task.set_status`:
  override-to-reject reverts the row to its prior state value;
  override audit records the revert.

The principal-override surface in mobile is a menu entry on
resolved attention rows: "Override decision" → confirmation
sheet with reason field → `/decide(approve|reject)` with
`override=true` flag. The flag is informational only; the
override is detected server-side by row state.

### D-9. Audit + agent_events coverage

Every propose lifecycle event emits an audit row:

| Event | `audit_events.action` | `meta` |
|---|---|---|
| Propose row created | `propose.created` | `{kind, target_ref, change_spec, by_agent}` |
| Decide (any tier, any decision) | `attention.decide` (existing) | `{decision, kind, tier, by, reason}` — extended with `tier` for propose rows |
| Apply executed | `propose.applied` | `{kind, target_ref, executed, by_tier}` — emitted on successful apply |
| Apply failed | `propose.apply_failed` | `{kind, target_ref, error}` |
| Override | `attention.override` (new) | per D-8 shape |
| Fan-back delivered | (no separate row — covered by `agent_events` insert) | n/a |

For fan-back to the requester, the existing
`dispatchAttentionReply` allowlist gains `propose` as an allowed
kind; `input.attention_reply` carries `{request_id, kind:"propose",
change_kind, decision, reason, executed?}` so the requesting
agent on its next turn knows both the decision and (if approve)
what was applied.

## Alternatives considered

### A-1. Split into two ADRs (verb + tier ladder)

Initially drafted. Rejected — the verb cannot be specified
without addressing semantics, and tier addressing has no
meaning without a verb to address. Splitting would force
cross-references everywhere and make future readers chase two
docs to understand one trust model. Single ADR with sectioned
decisions matches the ADR-029 pattern (eight decisions in one
doc).

### A-2. Single term ("governed action" only)

Rejected for the multi-member future. In MVP single-principal,
the commit distinction is decorative. In multi-member, M>1 of N
principals for commits vs M=1 for non-commit governed actions
is a natural cleavage that the pair handles cleanly and the
single term would express as awkward conditionals. The cost of
the second glossary entry is small; the future-proofing is
real. Full analysis in
[discussion §3](../discussions/governed-actions-and-propose-verb.md#3-terminology-governed-action-vs-commit).

### A-3. `worker_tool_call.escalate` as a new propose kind (Option B)

Considered: wrap the worker permission-prompt row as
`propose(kind="worker_tool_call.escalate", target_ref={...},
change_spec={tool_name, input, tool_use_id})`. Rejected for
MVP — adds ~250 LOC of propose-dispatcher integration for the
sync claude hook path and the parked-RPC codex path, with no
functional gain over the simpler row-re-addressing of Option A.
Worth revisiting in the follow-up ADR if the propose-card
mobile UI proves uniform enough that two attention kinds are
worse than one. See
[worker-permission-routing-to-steward.md §3](../discussions/worker-permission-routing-to-steward.md#3-what-steward-as-first-line-approver-would-look-like).

### A-4. Auto-escalation on reject or timeout in MVP

Rejected for MVP. Reject-escalation invites loops (rejected →
re-routed up → rejected → escalated → re-routed up); the
principal's "I'd notice stalled progress" is a sufficient
liveness signal at single-principal scale. Timeout-escalation
needs a daemon and timeout-per-kind policy and bubble-up rules;
all of that is purely state-machine work that depends on no
schema change. Defer to the follow-up ADR where multi-member
ladders make it load-bearing.

### A-5. Principal override with a time window

Rejected for MVP. Common in corporate governance (you have N
days to appeal an approval) but adds complexity (window
policy per kind, window expiry handler, "is the row still
overridable" predicate) for no MVP value. Single-principal
override-anytime is simple and matches single-user expectations.
Multi-member ADR may revisit.

### A-6. Make `propose` worker-only (stewards keep direct mutation paths)

Rejected. Stewards already have skip permissions for their own
direct tools (per
[permission-model.md](../reference/permission-model.md) Mode 3),
which lets them bypass the verb gate entirely. The point of
`propose` is *system-applies-on-approve enforcement* — restricting
it to workers would leave stewards with the same prompt-only
discipline we're trying to retire. Stewards call `propose` for
governed mutations to project record; their direct tool surface
remains for non-governed work (reading, writing in their
workdir, internal A2A coordination).

### A-7. Reuse `request_approval` instead of a new verb

Rejected. `request_approval` is a *yes/no question* — its return
shape is `{decision, reason}`, no payload semantics. Overloading
it with a `change_spec` would mix conversational and load-bearing
concerns at the verb layer (the very confusion that motivated
this ADR). Keeping `request_approval` as the conversational
verb and adding `propose` as the load-bearing verb preserves
the distinction.

## Consequences

### Positive

- **A3 is honoured for the project record.** Deliverable state,
  phase, task close-out are now system-applied on approve, not
  agent-self-applied. The "prompt told me to ask first" loophole
  closes.
- **Principal load drops for worker tool calls.** Worker
  `permission_prompt` rows route to the project steward by
  default; principal sees only escalations (none in MVP, since
  we ship no escalation — but the structural improvement is
  immediate: the principal's inbox stops collecting every
  worker's Write/Bash gate).
- **One verb, kind-dispatched.** Every future load-bearing
  mutation becomes "add a kind, add an apply function, add a
  policy row." No new attention kind per case, no new
  apply-on-approve branch in the decide handler.
- **Multi-member is unblocked.** Tier addressing, per-(kind,
  tier) quorum, override audit — all the schema hooks are in
  place. The follow-up ADR is state-machine work.
- **Mobile UX consolidates.** One propose-card shape (with
  per-kind body rendering) replaces the proliferation of
  one-off attention cards.
- **The two existing apply-on-approve paths get a uniform
  audit story.** `propose.applied` / `propose.apply_failed`
  cover them too via the alias path.

### Negative / accepted

- **One-cycle deprecation period for the two aliases.** Existing
  `approval_request+spawnIn` and `template_proposal` clients keep
  working until the follow-up ADR removes the aliases.
  Acceptable; the alias dispatch is ~30 LOC.
- **Steward prompts gain a re-propose rule.** Every bundled
  steward template (4-5 files) gets a short paragraph: "If a
  propose is rejected, do not immediately re-propose to a higher
  tier. Re-examine the reason; only re-propose if new
  information is available." This is prompt convention, not
  enforcement — accepted because the alternative
  (`escalation_count_per_kind` policy) is overkill for MVP.
- **`worker_tool_call.escalate` is a routing extension, not a
  propose kind.** Means two attention kind families on the
  authoriser surface (`propose` cards and `permission_prompt`
  cards). Accepted for MVP — see A-3.
- **No principal-override window.** A principal can override a
  steward-approve from yesterday. Single-principal makes this
  fine; multi-member may want a window.
- **MVP single-principal means quorum is decorative.** The
  `M: 1` everywhere is uniform; the per-(kind, tier) policy
  shape is real but its degrees of freedom won't be exercised
  until multi-member ships. Worth the schema cost to avoid
  retrofitting.

## Open follow-ups

- **Wedge-shaping** for the eight items in
  [discussion §10](../discussions/governed-actions-and-propose-verb.md#10-open-items-wedge-shaping-not-design-shaping).
  None block this ADR; covered in the plan.
- **Per-kind mobile card design** for the four MVP kinds. Plan
  W7 covers the cards; visual review during the wedge.
- **Escalation + multi-member quorum follow-up ADR.** Sequenced
  after this ADR ships and the alias deprecation window closes.
  Will add: auto-escalation transitions, M>1 quorum for commits,
  override windows, carbon-copy mode, and remove the deprecated
  aliases.
- **Glossary backfill.** New entries: "governed action", "commit
  (action sense)", "propose (verb)". Plan W11 covers it.
- **Lint coverage.** A `scripts/lint-governed-actions.sh` that
  walks `team/policy/governed-actions.yaml` against the
  dispatcher's registered kinds and fails on drift. Plan W3.

## References

- Discussion that produced this ADR:
  [discussions/governed-actions-and-propose-verb.md](../discussions/governed-actions-and-propose-verb.md)
- Execution plan:
  [plans/governed-actions-mvp-rollout.md](../plans/governed-actions-mvp-rollout.md)
- Prior-art audit (in the discussion):
  [§8 How this composes with prior ADRs](../discussions/governed-actions-and-propose-verb.md#8-how-this-composes-with-prior-adrs)
- Tool-call approval patterns reference:
  [reference/tool-call-approval-patterns.md](../reference/tool-call-approval-patterns.md)
- Worker permission routing discussion (subsumed):
  [discussions/worker-permission-routing-to-steward.md](../discussions/worker-permission-routing-to-steward.md)
- Foundational axiom: [spine/blueprint.md A3](../spine/blueprint.md#2-design-philosophy-three-axioms)
- Role ontology: [spine/governance-roles.md](../spine/governance-roles.md)
- Current apply-on-approve dispatch:
  `hub/internal/server/handlers_attention.go:378-419` (the two
  branches at `:378` for `approval_request+spawnIn` and `:400`
  for `template_proposal`).
- Related ADRs:
  [ADR-005](005-owner-authority-model.md) (owner authority),
  [ADR-011](011-turn-based-attention-delivery.md) (turn-based delivery),
  [ADR-016](016-subagent-scope-manifest.md) (role gates via `roles.yaml`),
  [ADR-017](017-layered-stewards.md) (steward tier vocabulary),
  [ADR-020](020-director-action-surface.md) (director-side mutations on documents/deliverables — agent-side counterpart added here),
  [ADR-025](025-project-steward-accountability.md) (D3 — first kind-gated mutation, precedent for the generalisation),
  [ADR-029](029-tasks-as-first-class-primitive.md) (auto-derive + override pattern; `task.set_status` operates on its rows).

## Amendments

### 2026-05-20 — D-7 superseded by Option 2′ (decision stays, signal walks)

When this ADR was drafted (2026-05-17), D-7 declared
*"No auto-escalation on timeout — no timeout daemon ships."*
Two days later, [ADR-034](034-orchestration-loop-closure.md) shipped
exactly such a daemon as part of the orchestration loop-closure
runtime (`hub/internal/server/loop_sweep.go`, v1.0.632-alpha). The
sweep advances `attention_items.escalation_state` along the tier
ladder (`none → escalated_steward → escalated_principal`) when
per-hop deadlines pass. D-7 as written contradicts the realised
behaviour.

A 2026-05-20 review surfaced this and considered three reconciliations:

1. **Opt propose rows out of loop-closure** (set their
   `inactivity_deadline = NULL` on insert). Preserves D-7 literally
   but loses the liveness signal it leaned on.
2. **Visibility-only escalation** — `escalation_state` advances, but
   `assigned_tier` stays immutable; the principal's pathway remains
   D-8 override.
3. **Addressee handoff** — `escalation_state` advances *and*
   `assigned_tier` mutates to the next tier; the row's decision
   authority moves up the ladder.

**Option 3 was rejected for MVP** because it forces a cluster of
additional MVP work that D-7 explicitly deferred: a `/decide` gate
against `assigned_tier`, per-tier deadline reset on re-address, a
terminal state when the principal also times out, and matching policy
schema (`escalate_on_timeout`, `escalate_to`). Each is sound and each
belongs in the multi-member follow-up ADR; none belongs in this MVP.

**Option 2 was extended (Option 2′)** to address the legitimate "how
does the principal know it's stuck?" requirement that motivated
option 3 in the first place. The state-machine stays simple
(`assigned_tier` is immutable; the original addressee can always
decide); the *signal* walks the ladder so the principal learns about
staleness without polling.

#### D-7 (amended)

Quorum stays M=1 of N at each tier (single principal, M decorative).
**`assigned_tier` is set at row creation by policy and never mutated
by the sweep.**

ADR-034's loop-closure runtime advances `escalation_state` along the
tier ladder (`none → escalated_steward → escalated_principal`) when
per-hop deadlines pass. Each transition:

1. Emits `audit_events.action="attention.escalation_advanced"` with
   `meta={attention_id, change_kind, from_state, to_state,
   original_assigned_tier}`.
2. Surfaces in the mobile **pull** surfaces (no separate push
   channel in MVP — see the "Pre-W1 blocking decisions" amendment
   below, item 5). The audit row appears in the `agent_events`
   stream that Activity tab already consumes; the Me-page query
   widening (W19.6) makes the escalated row visible in the
   principal's Me-page on next foreground/pull; the Me-page digest
   card surfaces the count.

**The row's decision authority does not move.** The original
addressee can still decide at any point; if they do between
escalation transitions, the row resolves normally and the audit trail
records the late-but-valid decision. The principal's pathway to act
remains **D-8 override** — escalation makes them aware; override is
the verb. The audit row on principal action is `attention.override`,
not `attention.decide`, because `assigned_tier` was still below
principal at decide-time.

Reject remains terminal at the addressed tier. Re-propose-to-higher-
tier by the requester stays convention-only (prompt rule).

Multi-member quorum (M>1), addressee handoff, per-tier deadline
reset, and the matching `escalate_to` policy field are deferred to
the multi-member ADR. The schema hooks in D-6 stay forward-
compatible; the `escalate_on_timeout` boolean is reinterpreted to
mean "fire signal", not "move addressee".

When the principal also times out, the row stays open with periodic
re-notifications on a backoff (e.g. daily). Principal-as-last-stop is
conceptual, not enforced — no auto-close.

#### IA fit on mobile (Me-page)

The current Me-page (`lib/screens/me/me_screen.dart`) is a single
scrolling page with a four-chip filter bar — *All · Requests ·
Agents · Messages* — keyed by attention `kind`, not by escalation
state. The buckets:

- **Requests** — `approval_request`, `permission_prompt`, `select`,
  `help_request`, `elicit`, `template_proposal`,
  `project_steward_request`. The "agent waiting on the user" bucket.
- **Agents** — `idle`, `agent_error`.
- **Messages** — every other attention kind (catch-all).

`propose` is a Request by kind (the system is asking someone to
decide). Stalled propose rows stay in Requests for the principal —
they don't migrate to Messages or get their own filter chip. The
"stalled" axis becomes a card decoration plus a top-of-Me digest
card, mirroring the existing bottom "Since you were last here"
digest pattern:

- **Per-card stalled variant.** When the viewer is *not* the row's
  `assigned_tier` but the row's `escalation_state` puts it in their
  surface, the card renders with: a top pill (`⏱ Stuck 4h —
  addressed to @steward.proj-92`); action buttons `Override` /
  `View source` instead of `Approve` / `Reject`. Tapping `Override`
  opens the D-8 confirmation sheet.
- **Top-of-Me digest card.** Renders when escalated-row count > 0:
  *"3 decisions stalled at stewards · 1 stalled with you >24h"*.
  Tap → narrows the Me-page list to escalated rows.
- **No filter-chip rename.** Messages stays the catch-all.
- **No new attention kind.** Escalation is the existing
  `escalation_state` column from migration 0042; the row stays
  `kind='propose'` (or `'permission_prompt'` for the worker-tool
  case).

The push-notification channel from item 2 above fires *once per
state transition* (deduped by `escalation_state`), not per sweep
tick.

### 2026-05-20 — Reconciliations with ADR-032 (envelope) and ADR-034 (loop-closure)

Two adjacent ADRs shipped between this ADR's drafting and the
2026-05-20 review. The reconciliations:

**ADR-032 envelope on propose fan-back.** [ADR-032](032-message-routing-envelope.md)
(shipped v1.0.632) decorates every `input.text` edge with the
envelope `{from, to, kind, text, cause, thread}`. The propose
fan-back uses `input.attention_reply`, not `input.text` — its
existing payload shape stays, but the hub-side compose site
(`dispatchAttentionReply`) populates the same envelope fields
alongside `{request_id, kind:"propose", change_kind, decision,
reason?, executed?}` so downstream lineage queries (the directive
trace) can resolve a propose-decision edge the same way they resolve
a directed-input edge. The plan's W11 is amended to require envelope
composition at fan-back time.

**ADR-034 loop-entity columns on `attention_items`.** [ADR-034](034-orchestration-loop-closure.md)
migration 0042 already adds `inactivity_deadline`,
`last_progress_at`, `opened_at`, `absolute_cap`, `escalation_state`,
`terminal_reason`, and `cause` to `attention_items`. None of these
collide with the ADR-030 W1 columns (`change_kind`, `assigned_tier`,
`change_spec_json`, `target_ref_json`, `executed_json`), but two
conceptual overlaps need explicit noting so future contributors
don't collapse one into the other:

1. **`cause` (ADR-034) vs `target_ref_json` (ADR-030).** For a
   `task.set_status` propose, `target_ref_json.task_id` and `cause`
   commonly hold the same task ID — but they serve different roles.
   `cause` is the lineage pointer the directive trace walks
   (per ADR-034 D-8); `target_ref_json` is the mutation target the
   apply function reads on approve. The hub's propose handler MUST
   populate both columns: `cause` from the propose call's enclosing
   task context (if any), `target_ref_json` from the propose
   argument.
2. **`assigned_tier` (ADR-030) vs `escalation_state` (ADR-034).**
   `assigned_tier='project-steward'` is *who decides*;
   `escalation_state='escalated_steward'` is *what the sweep has
   already signalled*. The loop sweep MUST NOT re-emit an
   `escalation_advanced` audit/push for the same `escalation_state`
   value across ticks — the column is also the dedup key.

The plan's W1 is amended: migration number is **0045** (not 004X
or 0044 — the 0044 slot was taken by the post-v1.0.636 handle-
normalization migration shipped between this ADR's audit and W1
implementation), and a short paragraph documents the two overlaps.

### 2026-05-20 — Principal ≠ owner

D-3's principal tier is "director framing at decide-time." This is
**not** the same as ADR-028's `requireOwner(w, r)` gate on the
`/v1/admin/*` surface. ADR-028 introduced an owner-kind bearer token
that gates fleet/host/db/audit operations
(`hub/internal/server/handlers_admin.go:70` and siblings, v1.0.636).

- **Owner** = hub-admin token bearer; called for cluster operations
  (host shutdown/restart/update, db vacuum, audit cross-team
  queries). Today's bearer-kind check.
- **Principal** = the human director addressed via attention rows;
  decided through the `/decide` endpoint on
  `attention_items` rows whose `assigned_tier='principal'`.

The `/decide` handler does **not** today gate on owner-kind; it
accepts any team-scoped bearer. ADR-030's propose decide path
should stay that way for MVP (single principal, single team-scoped
identity). The multi-member follow-up ADR will introduce
principal-tier identity proofs separately from owner — until then,
the existing team-scoped bearer is the principal proxy.

This distinction matters because over-restricting the propose decide
path to owner-kind would make single-principal MVP work fine but
silently block multi-member; the schema-hooks-for-multi-member work
in D-6 + D-7 would then be partly undone by the auth gate.

### 2026-05-20 — Plan drift items addressed in the plan rewrite

The 2026-05-20 audit also found stale file/line refs in the plan,
which are fixed in [plans/governed-actions-mvp-rollout.md](../plans/governed-actions-mvp-rollout.md)
without being relitigated here. For the record:

- W4's MCP tool registration moved from `hub/internal/hubmcpserver/tools.go`
  to `hub/internal/server/native_tools.go`'s `buildNativeTools()` —
  ADR-033 (shipped v1.0.631) reorganised the tool catalog.
- W10's `mcpPermissionPrompt` is at `mcp_more.go:687`, not `~1045`.
- W12's bundled steward templates are **9 files** as of v1.0.673
  (was 5 in the original ADR draft; 8 before antigravity shipped at
  v1.0.641 per ADR-035):
  `steward.v1.md`, `steward.general.v1.md`,
  `steward.claude-m4.v1.md`, `steward.codex.v1.md`,
  `steward.gemini.v1.md`, `steward.kimi.v1.md`,
  `steward.research.v1.md`, `steward.infra.v1.md`,
  `steward.antigravity.v1.md`. The W10 strict same-project parent-
  steward predicate is engine-neutral and applies uniformly to all 9
  — antigravity's permission-prompt detector (deferred to ADR-035
  W11) will route through `mcpPermissionPrompt` the same way the
  other 4 engines do, so no per-engine carve-out is needed.
- W13's scenarios renumber to S33-S40 — confirmed (highest
  existing is S32, ADR-034 stuck-task recovery).

### 2026-05-20 — Pre-W1 blocking decisions resolved

The 2026-05-20 audit surfaced five open questions that would block
W1 starts (the plan said "no work started"). The decisions, with
their rationale:

#### 1. Policy file shape — extend the existing `policy.yaml` (single file, two coexisting shapes)

The existing `hub/internal/server/policy.go` already loads
`<dataRoot>/team/policy.yaml` into a `Policy` struct carrying
`tiers / approvers / quorum / escalation`. ADR-030 D-6 originally
proposed a *second* file `team/<team>/policy/governed-actions.yaml`
with a richer per-`kind` shape.

**Decision:** extend the existing `policy.yaml` with a new top-level
`kinds:` block. One file, one reader. The new block coexists with
the legacy `tiers`/`approvers`/`quorum` (which the alias-compat
spawn/template-install paths still consult during the one-cycle
deprecation window).

The amended file shape:

```yaml
# legacy shape — still consulted by alias-compat dispatch
tiers:
  spawn: moderate
  tool:write_file: low
approvers:
  moderate: ["@steward", "@principal"]
quorum:
  moderate: 1

# new ADR-030 shape — read by the propose dispatcher
kinds:
  deliverable.set_state:
    default_tier: principal
    quorum:
      principal: { M: 1 }
    commits: true
    override_allowed: true
    escalate_on_reject: false
    escalate_on_timeout: false
  task.set_status:
    default_tier: project-steward
    quorum:
      project-steward: { M: 1 }
    commits: true
    override_allowed: true
    escalate_on_reject: false
    escalate_on_timeout: false
  # … rest per D-6
```

The reader: when handling a propose call of `kind=X`, look up
`Policy.Kinds[X]` first; if missing, fall through to a permissive
default (M=1 at `default_tier=principal`) plus a WARN log. Reload on
mtime change (existing pattern in `policy.go`).

Plan W2's file path correction: **`<dataRoot>/team/policy.yaml`**
(not `<dataRoot>/policy/governed-actions.yaml`); the new struct
field is `Kinds map[string]KindPolicy` added to the existing `Policy`
struct.

The existing `EscalationPolicy.WidenTo` mechanism (which would
implement option 3 of D-7) is intentionally **not** wired up for
propose rows in MVP. The D-7 Option 2′ amendment keeps
`assigned_tier` immutable; using `WidenTo` would re-introduce the
addressee handoff we deferred to the multi-member follow-up ADR.
This is a divergence between policy capability (machinery exists)
and policy use (not exercised for propose) that future contributors
must respect.

#### 2. `assigned_tier` — add as a new column

The existing `attention_items.current_assignees_json` carries an
array of handles (resolved via `policy.ApproversFor(tier)`).
ADR-030 D-3 wants the *tier name itself* alongside, for fast Me-page
query widening (W19.6) and for the audit metadata.

**Decision:** add `assigned_tier` as a new column in migration 0045
(per W1; renumbered from the originally-planned 0044 after the
post-v1.0.636 handle-normalization migration shipped first). Resolution flow on propose: the dispatcher reads
`Policy.Kinds[kind].default_tier`, stamps it into `assigned_tier`,
then expands to `current_assignees_json` via the existing
`approvers` map (keeping the legacy column populated for the
existing escalation/widen machinery).

W19.6's query widening uses `assigned_tier` directly (cheap string
compare), not a `JOIN` against the policy file.

Plan W1's column list is unchanged; this amendment confirms the
choice is "add the column" rather than "derive at decide-time."

#### 3. W10 worker→steward predicate — strict same-project

When a worker raises `permission_prompt`, the row is re-addressed to
the parent steward only when:

```
worker.parent_agent_id IS NOT NULL
AND parent_agent.kind LIKE 'steward.%'
AND parent_agent.project_id = worker.project_id
```

**Decision:** strict. Orphan workers (no parent), workers with a
non-steward parent, and workers whose parent steward is from a
different project — all keep their `permission_prompt` rows
team-wide-addressed as today. This matches ADR-025 D3
("workers project-scoped + sessioned; one steward per engaged
project").

The third-clause (`project_id` match) avoids a v1.0.605-class bug
where a parent-id pointer survived but the project binding had
drifted. If the binding fails, surface team-wide rather than
mis-route to a steward of an unrelated project.

#### 4. W4 propose target — caller-`project_id` for workers; stewards/principal may cross

`propose` calls enforce a target-scope check before raising the
row:

| Caller `kind` | Constraint |
|---|---|
| worker | `target_ref.project_id` MUST equal `caller.project_id`. Cross-project propose → 403 with a structured error. |
| `steward.<domain>.*` (project / domain steward) | May cross projects (a domain steward coordinating across owned projects). |
| `steward.general.*` (general steward) | May cross projects (the general steward routes work across the team). |
| principal | May cross (does not typically call MCP; the gate is informational). |

The check lives in `handlers_propose.go`, runs after `kind` validation
and before policy lookup. Rejection is a 403 with `code: "out_of_scope"`
so steward prompts can teach workers to delegate cross-project work
through the steward rather than directly proposing across the boundary.

#### 5. W11.5 escalation channel — audit row only (no separate push infra)

The repo has **no mobile push infrastructure** (no FCM / APNS /
firebase / device-token tables). The W11.5 wedge previously read as
if it would build one.

**Decision:** the escalation channel for MVP is the **audit row
alone**. When `loop_sweep.go` flips `escalation_state`, it
`s.recordAudit("attention.escalation_advanced", ...)`. That row
lands in the `agent_events` stream the mobile Activity tab already
consumes; the Me-page query widening (W19.6) makes the escalated
row visible on next foreground/pull; the Me-page digest card
surfaces the count.

This means: **the principal learns about escalation on next
foreground/pull, not via a real-time push.** Acceptable for
single-principal MVP — the principal is the human director and
checks the app on roughly the same cadence as the sweep tick (45s).
Real-time push is a clean post-MVP add (FCM/APNS adapter + device-
token table + a notifications service).

W11.5 LOC is correspondingly trimmed (~30 LOC instead of 50): just
the audit-row emit + dedup-by-`escalation_state`. The plan §1 phase
table reflects the reduced Phase 1 total (~1080 LOC, not 1100).
The 24h/7-cap backoff machinery is also dropped from MVP — without
a real-time push, repeated re-pushes aren't a concern; the principal
sees the row each time they open Me.

The mobile-side digest card (W19.6) is the visibility surface; the
audit row is the signal trail. Both pull-shaped, both work without
push infra.

---
name: Governed actions and the propose verb
description: Generalise the apply-on-approve mechanism (today only used by approval_request+spawnIn and template_proposal) into a single `propose` MCP verb with a 4-tier authorization ladder. Names the term pair governed-action / commit, locks the MVP scope (4 kinds, M=1 quorum, no escalation), and reconciles with prior ADRs 005/011/016/017/020/025/029.
---

# Governed actions and the `propose` verb

> **Type:** discussion
> **Status:** Open (2026-05-17) — resolves into ADR-030 (single ADR
> covering verb + tier ladder; the two are inseparable). Pre-ADR
> shape is locked on the six design decisions in §6; remaining
> open items in §10 are wedge-shaping, not design-shaping.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.619-alpha

**TL;DR.** Today only two operations apply system-side on approve
(`approval_request + spawnIn` → `DoSpawn`; `template_proposal` →
file install). Every other "agent asks principal" verb
(`request_select`, `request_help`, vanilla `request_approval`) is
conversational — the system records the decision and fans it back
as an `input.attention_reply`; the agent itself performs the
mutation on its next turn. That leaves a class of mutations
(deliverable state, phase advance, task close-out, worker tool
calls) enforced only by prompt-discipline, which contradicts
[blueprint A3](../spine/blueprint.md#2-design-philosophy-three-axioms)
("every autonomous action must be bounded by a rule that existed
before the action happened"). This doc proposes a single new MCP
verb `propose(kind, target_ref, change_spec, reason, addressee_tier?,
dry_run?)`, a 4-tier authorization ladder (worker / project-steward
/ general-steward / principal), and a kind-dispatcher in the
approve handler that generalises the existing two apply-on-approve
paths. The term pair **governed action** (umbrella) + **commit**
(subset that updates the canonical project record) is the
vocabulary. MVP ships four propose kinds + a re-addressing
extension to `permission_prompt`. Multi-member hooks are baked
into the schema; escalation/quorum-M>1/timeout-bubbling are
deferred to a follow-up ADR.

This is a pre-ADR design discussion. Read this; ADR-030 will be
the decision artefact.

---

## 1. Why this is the problem

The principal walked through the current approve flow in a session
(2026-05-17) and surfaced four observations that compound:

1. **Worker tool-call approvals burden the principal.** Every gated
   tool call from every worker on every project hits the principal's
   Me-page inbox. The project steward — which per
   [ADR-025](../decisions/025-project-steward-accountability.md) D3
   is the accountable owner of the project's workers — isn't even
   notified.
2. **Seed-demo attention items are inert on approve.** Five
   lifecycle attentions are seeded with no `session_id`, so
   `dispatchAttentionReply` short-circuits at line 661-663. Tap
   approve, nothing happens beyond the audit row. (Tracked
   separately; not in scope for this ADR — fix is doc-as-comment
   per [§9](#9-seed-demo-disposition).)
3. **`select`/`help_request` have no system-side semantics.** The
   "concreteness" of approve comes entirely from the agent's
   prompt — *behavioural* contract, not enforced. A steward
   ignoring its BOUNDARIES could call `request_approval(...)` and
   then call Write anyway in the same turn; nothing in the MCP
   layer prevents it.
4. **Only two operations are actually apply-on-approve today.**
   `handlers_attention.go:378-414` — `approval_request + spawnIn`
   calls `DoSpawn`; `template_proposal` calls
   `installProposedTemplate`. Both are kind-specific branches in
   the decide handler. Everything else is fan-back-only.

The first issue is the user-friction observation that motivated
[worker-permission-routing-to-steward](worker-permission-routing-to-steward.md);
the rest are the structural class. This discussion takes the
generalised view: instead of solving the worker-tool-call routing
in isolation, name the category and design the verb.

### What blueprint A3 actually requires

[Blueprint](../spine/blueprint.md) axiom A3 is unambiguous:

> Every autonomous action must be bounded by a rule that existed
> *before the action happened*.

For `agent.spawn` and `template.install` this is true today — the
system literally cannot perform either without an authorised
`/decide(approve)`. For deliverable ratification, criterion
state-transition, phase advance, task close-out, worker
permission-prompt tool calls — A3 is honoured by *prompt
convention only*. The system trusts the agent to follow
BOUNDARIES. Sometimes that's enough; for load-bearing project
record edits it shouldn't be.

This ADR closes the gap by giving the system a verb to
authorise + apply each load-bearing mutation.

---

## 2. What's already governed (the prior art)

Termipod has been incrementally building toward this without
naming the category. Five precedents:

| Precedent | What it gates | Mechanism | Source |
|---|---|---|---|
| `agents.spawn` (worker by non-steward) | Spawning a worker into a project | `approval_request + spawnIn` payload → `DoSpawn` on approve | `handlers_attention.go:378-398` |
| `templates.install` (new template) | Writing a template YAML to disk | `template_proposal` attention → `installProposedTemplate` on approve | `handlers_attention.go:400-414` |
| Project steward materialization | Spawning a project steward | `mcpRequestProjectSteward` → `handleEnsureProjectSteward` after fan-back (general steward calls ensure) | [ADR-025](../decisions/025-project-steward-accountability.md) D2/D4 |
| Worker spawn within project | Project steward exclusively owns project worker spawns | Role-gated in `roles.yaml` ([ADR-016](../decisions/016-subagent-scope-manifest.md) D6) + dispatcher check | ADR-025 D3 |
| Director-on-document moves | Annotation + send-back-with-notes | Director-side input (PATCH / POST) directly mutates state | [ADR-020](../decisions/020-director-action-surface.md) D1-D5 |

The first two are **system-applies-on-approve** (the system reads
the change_spec from `pending_payload_json` and performs the
mutation). The third and fourth are **role-routed-and-self-applied**
(only the authorised role can call the mutation; the system
doesn't apply it itself). The fifth is **principal-applies-directly**
(no agent in the loop).

What's missing is the verb that lets any agent **propose** any
governed change_spec and have the system **apply** it on approve,
addressed to the appropriate tier. The two existing apply-on-approve
paths become aliases for this verb.

### Why "propose" is the right name (and the collision with template_proposal)

The verb `propose` collides loosely with `template_proposal` (the
attention kind). Reading the collision charitably: `template_proposal`
is exactly an instance of the proposal pattern. Our verb
generalises it — `propose(kind=template.install, ...)` produces a
row that today's code would call a `template_proposal`. ADR-030
will deprecate the bespoke kind in favour of the generic
`propose(...)` shape, keeping the old kind as a one-cycle alias so
existing rows continue to resolve. The glossary entry for "propose
(verb)" should land alongside the existing "template proposal"
entry as the canonical form.

Alternative names considered and rejected:

- `submit_change` — implies external submission; loses the agent-
  to-principal framing.
- `request_mutation` — couples the API to a CRUD framing the term
  discussion in §3 explicitly rejects.
- `request_act` / `request_action` — too vague; "act" is the entire
  verb space.
- `request_commit` — would collide with our subset term in §3.

---

## 3. Terminology: governed action vs commit

The choice is between a single term and a pair. The pair earns its
keep specifically in the multi-member future:

- **Governed action.** Any operation the system gates on
  authorisation. Includes spawn, template install, permission
  policy change, artifact publish — all gated, none of which
  write a canonical record entry.
- **Commit.** The subset of governed actions that update the
  project's canonical record-of-truth: deliverable state,
  criterion state, phase, task status. Atomic, audited,
  observable by downstream actors who read those rows to decide
  their next move.

In MVP single-principal, the distinction is decorative — both
categories resolve to single-principal approval. In multi-member,
the pair carries real weight:

| Concern (multi-member) | Single-term | Pair |
|---|---|---|
| "Commits need M-of-N principals; spawns need 1-of-N" | awkward conditionals | natural — quorum per category |
| Override semantics (principal can override another principal's spawn-approve, but a committed ratification of a deliverable shouldn't be unilaterally undone) | hard to express | the asymmetry is in the vocabulary |
| Audit prose ("you committed this" vs "you authorised this") reads differently for the two | loses tone | matches intuition |

### Why not "mutation"

`docs/discussions/fork-and-engine-context-mutations.md` already
uses "mutation" to mean engine-side context state changes
(`/compact`, `/clear`, `/rewind`). The term is also CRUD-flavoured
in industry — it foregrounds *the change* rather than *the
authorisation*. We want the latter. The glossary should reserve
"mutation" for the engine-context sense (or the audit-event sense
in `reference/glossary.md`) and prefer "governed action" or
"commit" for the authorisation-gated category.

### Why not "authorised act" / "privileged operation" / etc.

- *Authorised act* is fine but loses the policy framing — it
  emphasises the gate without naming the layer.
- *Privileged operation* leans OS-kernel; our authorisation is
  policy not capability.
- *Covenant* / *act of record* are too legal in tone.

**Governed action + commit** stays close to corporate-governance
and VCS analogues, both of which contributors know cold.

---

## 4. The verb shape

```
propose(
  kind:            "<dispatcher key — see §5>",
  target_ref:      { project_id, deliverable_id?, criterion_id?, task_id?,
                     agent_id?, ... },
  change_spec:     { ...kind-specific payload — the patch to apply... },
  reason:          "<why the agent is asking>",
  addressee_tier?: "steward" | "principal",      // hint; policy may override
  dry_run?:        true | false                  // default false
)
```

- Returns `{request_id, status:"awaiting_response"}` immediately
  (Pattern C shape — see
  [tool-call-approval-patterns.md](../reference/tool-call-approval-patterns.md)).
  Agent ends its turn.
- The `request_id` is a fresh `attention_items.id`. The row stores
  `kind = "propose"`, `change_kind = <kind>`, `change_spec_json =
  <change_spec>`, `target_ref_json = <target_ref>`,
  `assigned_tier = <resolved_tier>`,
  `current_assignees_json = <addressees>`.
- On `/decide(approve)`: the decide handler reads
  `change_kind` and dispatches to `applyGovernedAction(kind,
  target_ref, change_spec)` — same generalised path as today's
  `installProposedTemplate` and the `spawnIn → DoSpawn` branch.
- On `/decide(reject)`: stores the decision + reason, fans back
  `{decision:"reject", reason}` to the requester via
  `dispatchAttentionReply`. **No re-addressing to a higher tier**
  (see §7).
- `dry_run=true`: returns the diff that *would* be applied as part
  of the awaiting_response payload, so the authorising tier can
  preview before deciding. The row is still created — `dry_run`
  is a preview flag on the *content* shown to the authoriser, not
  a "don't persist" flag.

### How it composes with attention infrastructure

- `attention_items` gains one column: `assigned_tier` (string,
  values `worker` / `project-steward` / `general-steward` /
  `principal`). The string is sourced from `governance-roles.md`'s
  five-role ontology, treating `worker` as the implicit "no
  authorisation required" tier (workers don't authorise; they
  propose).
- `current_assignees_json` stays an array (already is) for
  multi-member-future.
- `dispatchAttentionReply` allowlist gains `kind = "propose"`.
- `policy.QuorumFor(tier)` already exists per tier; we extend the
  policy file to be per-(kind, tier).
- `audit_events` gains `action = "attention.override"` row when a
  higher tier overrides a lower-tier decision.

---

## 5. MVP kinds (four)

The first-principles cut from the session discussion. Each kind
names the dispatcher key + the apply function the system calls on
approve.

| Kind | change_spec shape | Apply function (proposed) | Default tier |
|---|---|---|---|
| `deliverable.set_state` | `{state: "draft"\|"in-review"\|"ratified"\|"failed"\|"withdrawn", reason?}` | `setDeliverableState(target_ref.deliverable_id, change_spec.state, by=<requesting_agent>, via=propose)` | principal (commits) |
| `phase.advance` | `{from_phase, to_phase, reason?}` | `advanceProjectPhase(target_ref.project_id, change_spec.to_phase)` | principal (commits) |
| `task.set_status` (→done) | `{status: "done"\|"cancelled", result_summary?}` | `setTaskStatus(target_ref.task_id, change_spec.status, ...)` | project-steward (commits, but steward-tier authorises) |
| `worker_tool_call.escalate` | n/a — re-addresses existing `permission_prompt` rows | (no new dispatcher — re-uses existing permission_prompt machinery; just changes addressing) | project-steward (governed action, not commit) |

Note: `worker_tool_call.escalate` is **not** a new propose kind
because the codex parked-RPC and claude sync-MCP machinery already
exist. It's a **routing extension** to the existing
`permission_prompt` row: when raised by a worker whose
`parent_agent_id` is a steward, set `assigned_tier = "project-steward"`
and address the row to that steward. Same approve dispatch (the
engine driver still does the unparking); just different addressee.

This option corresponds to Option A in the
[worker-permission-routing](worker-permission-routing-to-steward.md)
§3 discussion. Option B (wrap as `propose(worker_tool_call.escalate,
...)`) is the long-term clean shape but ~250 LOC more for MVP and
gains nothing functionally — deferred.

### Deferred propose kinds (post-MVP)

Listed here so future PRs don't re-litigate scope:

- `criterion.set_state` (pending → met / failed / waived)
- `agent.terminate` (today: REST; should be governed)
- `agent.archive`
- `permission_policy.change` (project skip-flag toggle)
- `artifact.publish` (GitHub push, paper submission)
- `project.archive`
- `project.update_metadata` (title, description)

The shape of each is "add a new arm to the kind dispatcher with
its apply function and default tier."

### Deprecated propose-aliases

| Existing kind | Becomes | Migration |
|---|---|---|
| `approval_request + spawnIn` payload | `propose(kind="agent.spawn", target_ref={project_id}, change_spec={template, handle, ...})` | One-cycle alias: old kind still resolves; spawn dispatcher recognises both shapes. Removed in a follow-up ADR. |
| `template_proposal` | `propose(kind="template.install", target_ref={team_id}, change_spec={category, name, blob_sha256})` | One-cycle alias; existing `installProposedTemplate` becomes the apply function for the new kind. |

---

## 6. The 4-tier authorisation ladder

Tier names are sourced from
[`spine/governance-roles.md`](../spine/governance-roles.md). We do
**not** add a new role — we add an authorisation layer over the
existing role ontology. Per governance-roles.md's "Adding a new
role" guidance, this design explicitly avoids that path.

| Tier | Population (MVP) | Authority (under the propose verb) |
|---|---|---|
| `worker` | many | Proposes governed actions; cannot authorise |
| `project-steward` (= domain steward in canon) | 1 per engaged project | Authorises propose kinds with `default_tier="project-steward"`; may carbon-copy principal post-hoc (future) |
| `general-steward` (`@steward`) | 1 per team | Authorises propose kinds with `default_tier="general-steward"`; orchestrates handoffs |
| `principal` | 1 in MVP | Authorises propose kinds with `default_tier="principal"`; **may override** any lower-tier approve/reject |

Mapping to canonical roles:
- "worker" = worker (governance-roles.md).
- "project-steward" = domain steward (governance-roles.md). The
  ADR introduces the shorter alias because the existing
  conversational use of "project steward" matches it 1:1.
- "general-steward" = general steward.
- "principal" = principal-acting-as-director (the director framing
  applies at the moment of authorisation).

### Quorum and escalation in MVP

| Decision | MVP value |
|---|---|
| Quorum at each tier | M=1 of N (any one member of the tier resolves) |
| Auto-escalate on timeout | **None.** No timeout daemon. |
| Auto-escalate on reject | **None.** Reject is terminal at the addressed tier. |
| Re-propose by requester after reject | Convention only — agents may re-propose with new information, but the convention (encoded in prompt) is *don't* unless something changed. The principal will notice if project progress stalls. |
| Principal override of lower-tier approve | **Yes, no window.** Override anytime until the next governed action invalidates the row. Recorded as `audit_events.action="attention.override"`. |

This is a deliberately minimal MVP. Multi-member-future will need:
auto-escalation transitions, M>1 quorum for commits, override
windows, carbon-copy mode. All schema hooks are in place
(`assigned_tier`, `current_assignees_json` array, per-(kind, tier)
policy file) so the follow-up ADR is purely state-machine work.

---

## 7. Policy file

```yaml
# team/policy/governed-actions.yaml — new file
# Schema: per-kind, per-tier authorisation policy.
version: 1

kinds:
  deliverable.set_state:
    default_tier: principal
    quorum:
      principal: { M: 1 }
    commits: true                  # subject to commit semantics (D-3)
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

  # Worker tool-call routing — re-addresses existing permission_prompt
  # rows. Listed here so the policy file is the single source of
  # truth for "who decides what."
  worker_tool_call.escalate:
    default_tier: project-steward
    quorum:
      project-steward: { M: 1 }
    commits: false                 # governed action, not a commit
    override_allowed: true
    escalate_on_reject: false
    escalate_on_timeout: false

  # Deprecated propose-aliases (one-cycle compatibility)
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

The file lives under `team/<team>/policy/governed-actions.yaml`,
sibling to the existing `team/policy/` conventions. ADR-016's
`hub/config/roles.yaml` is the *role manifest* (which roles can
call which `hub://*` tools); the new file is the *action policy*
(per kind, who authorises). The two are distinct surfaces with
distinct lifecycles — roles change rarely, action policy changes
with the kind set.

---

## 8. How this composes with prior ADRs

| ADR / doc | Relationship | Notes |
|---|---|---|
| [ADR-005 — owner authority model](../decisions/005-owner-authority-model.md) | CITES | The principal/director authority distinction is preserved. The "director" framing applies at the authorise moment. |
| [ADR-011 — turn-based attention delivery](../decisions/011-turn-based-attention-delivery.md) | EXTENDS | `propose` returns `awaiting_response` immediately and fans back `input.attention_reply` on decide — same Pattern C shape as `request_select`/`request_help`. |
| [ADR-016 — subagent scope manifest](../decisions/016-subagent-scope-manifest.md) | CITES + EXTENDS | `propose` is a steward-and-worker callable verb; goes in `roles.yaml` allowed-tools. The new `team/policy/governed-actions.yaml` is a sibling, not a replacement. |
| [ADR-017 — layered stewards](../decisions/017-layered-stewards.md) | CITES | The 4-tier ladder uses the existing general/domain (project) steward tier vocabulary. |
| [ADR-020 — director-action surface](../decisions/020-director-action-surface.md) | EXTENDS | ADR-020 defines *director-side* moves on documents/deliverables (annotation + send-back-with-notes). This ADR adds the *agent-proposed* counterpart for the same state mutations. The director-direct path stays; agents now have a verb to propose the same transitions. |
| [ADR-025 — project steward accountability](../decisions/025-project-steward-accountability.md) | CITES + EXTENDS | D3 ("only project steward may spawn project workers") is the precedent — first kind-gated mutation in the system. The propose verb generalises that gating mechanism to deliverable/phase/task. |
| [ADR-029 — tasks as first-class primitive](../decisions/029-tasks-as-first-class-primitive.md) | CITES + EXTENDS | D-3's "auto-derive + manual override wins" is the override pattern this ADR formalises. `task.set_status` propose kind operates on the same `tasks` rows ADR-029 introduced. |
| [`reference/permission-model.md`](../reference/permission-model.md) | EXTENDS | Adds a third concept beyond the tool-call gate (modes 1/2/3) and the attention gate: the **governed-action layer** — the subset of the attention gate that applies state on approve. |
| [`reference/attention-kinds.md`](../reference/attention-kinds.md) | EXTENDS | Adds the `propose` kind to the canon. Old kinds (`approval_request + spawnIn`, `template_proposal`) become aliases. |
| [`reference/attention-delivery-surfaces.md`](../reference/attention-delivery-surfaces.md) | EXTENDS | The `propose` kind delivers via the same surfaces (Me-tab queue, badge, local notification, future ntfy push); severity inherits from the kind in policy. |
| [`reference/tool-call-approval-patterns.md`](../reference/tool-call-approval-patterns.md) | EXTENDS | `propose` fits cleanly as a Pattern C verb; the patterns reference will gain a §3.x entry documenting the dispatcher branch. |
| [`discussions/worker-permission-routing-to-steward.md`](worker-permission-routing-to-steward.md) | SUPERSEDED-INTO | The MVP "Option A" (re-address `permission_prompt` row, no new kind) lands as `worker_tool_call.escalate` in this ADR. That discussion flips to *Resolved → ADR-030* on landing. |
| [`spine/governance-roles.md`](../spine/governance-roles.md) | CITES (no change) | The 4-tier ladder uses existing role names; no role added. The §References section gains an entry pointing at ADR-030. |
| [`spine/blueprint.md`](../spine/blueprint.md) | CITES (A3) | This ADR is the concrete mechanism for axiom A3's "every autonomous action must be bounded by a rule that existed before the action happened." |
| [`discussions/fork-and-engine-context-mutations.md`](fork-and-engine-context-mutations.md) | NO CONFLICT | Uses "mutation" for engine-context state changes — disjoint sense from this ADR's scope. The terminology choice (governed action / commit, not mutation) prevents collision. |

**No supersessions.** Two minor amendments:
- `attention-kinds.md` adds `propose` and marks the two existing
  kinds as one-cycle aliases.
- `worker-permission-routing-to-steward.md` flips to *Resolved →
  ADR-030* on landing.

---

## 9. Seed-demo disposition

The principal directed: **do not modify the seed-demo code**.
Lifecycle attentions remain UI-only demonstrations of the Me-tab
queue surface. Mitigation:

1. **One comment block** in `hub/internal/server/seed_demo_lifecycle.go`
   near line 1444 (the attention insertion) pointing at this
   discussion: *"NOTE: seeded attentions are UI-only — no session_id,
   so dispatchAttentionReply short-circuits at handlers_attention.go:661.
   This is intentional; see docs/discussions/governed-actions-and-propose-verb.md §1."*
2. **New scenarios** in
   [`docs/how-to/test-steward-lifecycle.md`](../how-to/test-steward-lifecycle.md):
   one per MVP propose kind, exercised end-to-end on a live (not
   seeded) project + steward + worker triad. Numbering picks up
   from the existing 32 scenarios.

---

## 10. Open items (wedge-shaping, not design-shaping)

The six design questions from the session walkthrough are all
locked (see §3 / §5 / §6). What remains is execution detail for
ADR-030 + plan:

1. **Quorum default for commits in MVP.** `principal.M = 1` is
   uncontroversial for MVP (single-principal) but tagged in the
   policy comment as "review for M > 1 when multi-member ships."
   Confirm tag placement.
2. **Re-propose convention encoding.** Stewards' bundled prompts
   gain a short rule: "If a propose is rejected, do not
   immediately re-propose to a higher tier. Re-examine the
   reason; only re-propose if new information is available."
   Wedge: edit `steward.v1.md` + variants.
3. **Mobile rendering of `propose` cards.** Each kind gets a
   purpose-built card on the Me tab — `deliverable.set_state`
   shows the deliverable preview + state-transition arrow + diff
   (when `dry_run`); `phase.advance` shows the phase-change graph;
   `task.set_status` shows the task body; `worker_tool_call.escalate`
   reuses today's permission-prompt card. ~150 LOC mobile.
4. **Override audit-row shape.** New `audit_events.action =
   "attention.override"` row with `meta.original_decision`,
   `meta.original_tier`, `meta.override_decision`,
   `meta.override_tier`, `meta.override_by`. Confirm field names.
5. **Deprecation timeline for the two aliases.** One-cycle = until
   the follow-up ADR (escalation/multi-member). Restate this in
   ADR-030 D-? so the deprecation doesn't dangle.
6. **Test scenarios in test-steward-lifecycle.md.** One per kind
   × happy-path + reject-path + (for `worker_tool_call.escalate`)
   override-path. Estimated 8 new scenarios.

---

## 11. Cost summary for ADR-030

| Surface | LOC | Notes |
|---|---|---|
| Hub: schema migration (`attention_items.assigned_tier`, `governed_actions.yaml` reader) | ~80 | One column, one parser, one policy struct |
| Hub: `propose` MCP verb + kind dispatcher | ~300 | New MCP tool registration, request validation, dispatcher with 4 arms (3 propose kinds + alias compatibility) |
| Hub: apply functions for the 4 MVP kinds | ~200 | Three new (`setDeliverableState`, `advanceProjectPhase`, `setTaskStatus` already exists from ADR-029); routing change for `permission_prompt` re-addressing |
| Hub: override audit row + handler hook | ~50 | One new action kind + emit site |
| Hub: tests (per-kind happy/reject/override) | ~300 | Eight scenarios |
| Steward prompts: re-propose rule | ~30 prose | Each of 4-5 templates |
| Mobile: per-kind `propose` card | ~150 | Four cards + routing |
| Mobile: override affordance | ~40 | New menu entry when row is resolved by lower tier |
| Docs: ADR-030 | ~250 prose | Single ADR, no split |
| Docs: ADR-030 plan | ~150 prose | Wedge sequencing |
| Docs: `governed-actions.yaml` schema reference | ~100 prose | New ref doc |
| Docs: amend attention-kinds, attention-delivery-surfaces, permission-model, tool-call-approval-patterns | ~100 prose | Cross-doc consistency |
| Docs: seed-demo comment + 8 new test scenarios | ~120 prose | per §9 |
| Glossary: add "governed action", "commit (action sense)", "propose (verb)" | ~30 prose | Three entries |

**Total: ~1300-1500 LOC code + ~750 lines prose. Single ADR + single plan.**

---

## 12. Status

Open. Design locked on §3 / §5 / §6; remaining items in §10 are
wedge-shaping. Next: draft ADR-030 + plan; on landing, this
discussion flips to **Resolved → ADR-030** and
`worker-permission-routing-to-steward.md` flips to **Resolved →
ADR-030**.


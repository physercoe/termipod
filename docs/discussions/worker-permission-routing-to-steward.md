# Routing worker permission_prompt to the project steward

> **Type:** discussion
> **Status:** Open (2026-05-17) — surfaced during a session walkthrough
> of the permission_prompt flow with the principal. Not actionable
> yet; resolves into an ADR if we decide to ship, or fades if we
> decide principal-direct is the correct long-term posture.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.619-alpha

**TL;DR.** Today every worker tool-call approval lands in the
team-wide attention inbox with no addressing — the principal sees
it on the Me page and decides. The project steward, which per
ADR-025 is the accountable owner of the project's workers, isn't
notified and isn't the intended approver. There's a coherent design
extension that makes the project steward the first-line approver
with the principal as the escalation path; we've chosen to *not*
ship it today so it doesn't accidentally land without an explicit
ADR. This doc captures the question, the cost, and the trade-offs.

---

## 1. What we have today

See [tool-call-approval-patterns.md](../reference/tool-call-approval-patterns.md)
for the full mechanics. For this discussion all three approval
patterns (sync, async-parked, async-turn) write the same row shape:

```
attention_items (
  scope_kind = 'team',
  scope_id   = NULL,
  project_id = NULL,
  current_assignees_json = '[]',
  actor_kind = 'agent',
  actor_handle = <worker handle>,
  ...
)
```

No `project_id`, no specific assignee. The hub treats it as a team-
wide ask. Whoever taps `/decide` first resolves it (subject to
`policy.QuorumFor(tier)`). In practice that's always the principal
via the mobile attention inbox.

The project steward *technically* has `attention.decide` capability
(every steward template grants it) but:

1. It isn't notified about the row.
2. No bundled steward prompt tells it to watch for or respond to
   worker `permission_prompt` items.
3. The row has no `project_id` so even `attention.list?project_id=…`
   wouldn't surface it as project-scoped work.

So the steward could in principle approve a worker's tool call, but
nothing in the system currently routes the ask to it.

---

## 2. Why this question came up

The user spawned a coder.v1 worker in `prompt` mode, the worker
attempted Write, the permission gate fired, and the principal (not
the steward) was expected to approve. The principal asked: *is this
the design? shouldn't the steward — the agent that's accountable
for the worker — be the first one asked?*

The question has real substance because ADR-025 (project steward
accountability) builds the system around "one steward owns one
project's spawn authority." If the steward is accountable for
spawning the worker, it's reasonable to expect it to also be
accountable for the worker's tool decisions. Otherwise the
principal is responsible for both top-level approval (yes, spawn
this worker for this task) AND for every tool call the worker
attempts — burdening the principal twice for what is supposed to be
delegated work.

---

## 3. What "steward as first-line approver" would look like

**Three concrete edits** would make the project steward a first-
class participant in the approval loop. They compose; partial
adoption is possible.

### 3.1 Address the attention row

When the requesting agent has a `parent_agent_id` AND that parent is
a steward, stamp:

```
attention_items.scope_kind = 'project'
attention_items.scope_id   = <agent.project_id>
attention_items.project_id = <agent.project_id>
attention_items.current_assignees_json = '["<parent_steward_id>"]'
```

The existing `/decide` flow is unchanged — only the addressing
changes. The row remains queryable team-wide if no steward exists
(falls back to today's behaviour). Cost: ~30-50 LOC of payload
shaping at the four insertion points (`mcpPermissionPrompt`,
`request_approval`, `request_select`, `request_help` —
`request_project_steward` already addresses the principal because
the steward IS what's being requested).

### 3.2 Push a `permission_prompt.routed` event into the steward's session

So the steward sees the ask inline in its chat rather than having to
poll `attention.list`. Same shape as W2.9/W2.10/W2.11 notifications:

```
agent_events (
  agent_id = <steward_id>,
  kind     = 'permission_prompt.routed',
  producer = 'system',
  payload  = { attention_id, tool_name, input, tier, worker_handle, body: "…rendered…" },
  session_id = <steward's active session>,
)
```

Plus `bus.Publish(agentBusKey(steward_id))` so the host-runner's
InputRouter delivers it on the steward's next turn. Cost: ~80-100
LOC + a helper following the existing notify pattern.

### 3.3 Steward prompt rule: decide promptly or escalate

Every bundled steward prompt (`steward.v1.md`,
`steward.research.v1.md`, codex/gemini/kimi variants) gains a short
section under "Tasks" / "Authority":

> When you see a `permission_prompt.routed` notification, evaluate
> whether the requested call is in scope for the worker's task. If
> yes, call `attention.decide(approve)`. If no, call `decide(reject)`.
> If you're unsure, escalate to {{principal.handle}} via
> `request_help(mode=clarify, question=…)` and decide based on
> their reply.

Without this prompt rule the addressing change is inert — the
steward sees the notification but has no contract telling it to
act.

---

## 4. Trade-offs

| Argument FOR steward-as-approver | Argument AGAINST |
|---|---|
| ADR-025 makes the steward accountable for its workers; tool-call approval is part of that accountability | Auto-approval chains are a known LLM-system anti-pattern — agent A authorizes agent B to do X creates audit gaps and unclear blame for misuse |
| Principal isn't burdened with per-tool approvals for routine worker actions in the worker's own workdir | The principal already has tier auto-allow for Pattern A; the remaining escalations are exactly the calls a human should see |
| Mirrors how a human manager delegates — they approve "do this project," subordinates make per-action decisions within the boundary | Stewards are LLMs; their judgement about "is this in scope" is exactly the kind of decision we'd want a human reviewing for adversarial inputs (prompt injection) |
| Stewards already have skip permissions for their own tools, so they're already trusted with destructive actions in their workdir | "Trusted with own workdir" ≠ "trusted to grant permissions to others" — different blast radius |
| Reduces principal-on-mobile interruption rate (current friction the user has surfaced repeatedly in testing) | Mobile UI can grow filters / smarter routing without changing the trust model (e.g. auto-approve trivial categories from trusted projects) |

The strongest argument against — auto-approval chains — is real and
not specific to termipod. Mitigations exist (always carbon-copy
the principal on steward-approved actions; require principal-only
for tier=critical; cap steward approvals at N per session) but each
adds complexity.

The strongest argument for is the user-friction one: the principal
hits an approval cycle on every gated tool call from every worker
on every project they're running. If we believe stewards are agents
of the principal acting within bounds the principal authorized at
spawn time, then *first-line* approval by the steward (with
escalation paths preserved) is a coherent extension.

---

## 5. Middle-ground options

### 5.1 Carbon-copy mode

Steward approves; the same attention row also gets an
`audit_events.action='permission_prompt.steward_approved'` row +
optional `attention_items` row for the principal to **review post-
hoc** (info-only, no decide affordance). Principal stays in the
loop without blocking the steward's decision.

### 5.2 Tier-aware delegation

Steward can decide tier=`significant` autonomously; tier=`critical`
always escalates to the principal regardless of routing. Mirrors the
existing tier auto-allow ladder (trivial → routine → significant →
critical) but extends one rung further.

### 5.3 Opt-in per project

A `projects.delegate_approvals: true` field on the project row that
turns on steward-as-approver for that project only. Lets the
principal start with one trusted project (e.g. an internal-tools
sandbox) and expand once confident. Default off preserves today's
behaviour for everyone else.

---

## 6. Cost summary

| Surface | LOC | Notes |
|---|---|---|
| Hub: row addressing | ~30-50 | Four insertion points |
| Hub: `permission_prompt.routed` event helper | ~80-100 | Following W2.9 pattern |
| Hub: tests | ~150 | Per-pattern coverage |
| Steward prompts | ~50 prose | Each of 4-5 templates |
| Mobile: steward's session UI per-kind rendering for new event | ~30-50 | If steward UX gets a dedicated card |
| ADR | ~200 prose | Recording the trust-model decision |

Total: ~600-700 LOC + an ADR. Single wedge.

---

## 7. Open questions to resolve before we'd ship

1. **Default routing**: does steward-as-approver become the default
   for new spawns, or stay behind a `delegate_approvals` flag?
2. **Tier cap**: what tiers can the steward decide autonomously?
3. **Carbon-copy**: does the principal get a post-hoc audit
   notification, an info-only attention row, or nothing extra?
4. **Audit visibility**: does the activity feed distinguish
   `permission_prompt.steward_approved` from `…principal_approved`
   so the principal can scan for steward overrides?
5. **Cross-project steward absence**: if a project has no live
   steward (only the worker), do we fall back to principal direct
   (current behaviour) or queue until a steward is materialized?
6. **Adversarial input**: should certain tool kinds (e.g. tools that
   touch credentials, secrets, or external networks) be exempt from
   steward delegation? Defining "critical" precisely matters.

---

## 8. Status

Open. Recorded after a session-walkthrough discussion 2026-05-17.
No ADR drafted; no implementation work scheduled. This doc is the
parking spot for the design discussion so the next session can pick
it up cold.

Resolves into either:
- An ADR adopting one of §5's variants (with this doc cited as
  priors), OR
- A "stay with principal-direct" decision recorded as a stub ADR or
  a note in `docs/spine/governance-roles.md`, with this discussion
  flipping to **Dropped**.

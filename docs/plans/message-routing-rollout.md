---
name: Message routing rollout
description: Phased rollout of the orchestration contract — ADR-032 (the message envelope {from,to,kind,text,cause,thread} + the message-admission pipeline) and ADR-034 (the loop-closure runtime — terminal-reason taxonomy, per-hop deadlines, the deadline sweep, stall escalation, lifecycle hooks, the directive trace). Ten wedges across hub-server, host-runner, persona prompts, and the mobile app; the whole rollout is MVP — loop closure is not deferred. Re-wedged 2026-05-19 from the original forward-only envelope plan per the orchestration-contract discussion.
---

# Message routing rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18; **re-wedged 2026-05-19**) — widened
> from the original forward-only envelope (6 wedges) to the full
> orchestration contract (10 wedges), per the
> [orchestration-contract discussion](../discussions/orchestration-contract.md).
> No work started. Implements [ADR-032](../decisions/032-message-routing-envelope.md)
> (the message envelope) and [ADR-034](../decisions/034-orchestration-loop-closure.md)
> (loop closure). The whole rollout is MVP.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** Replace the v1.0.626 / v1.0.630 band-aids with the
orchestration layer's real type system. Three phases, ten wedges,
**all MVP** — loop closure is not deferred (2026-05-19 decision):

- **Phase A — The envelope ([ADR-032](../decisions/032-message-routing-envelope.md), 4 wedges).**
  The envelope `{from,to,kind,text,cause,thread}`; hub-server composes
  it; drivers render it; the message-admission pipeline validates
  every envelope fail-safe.
- **Phase B — Loop closure ([ADR-034](../decisions/034-orchestration-loop-closure.md), 4 wedges).**
  The terminal-reason taxonomy + open-set tracking; per-hop deadlines
  and the sweep; stall escalation; lifecycle hooks; the directive
  trace.
- **Phase C — Agent-facing + mobile (2 wedges).** Per-persona prompts
  teaching the envelope and the loop; the mobile app reads the new
  envelope and terminal-reason fields.

There is **no backward-compat shim** — the envelope is the only
accepted body shape from the rollout commit (ADR-032 D-8); cut over on
a drained hub.

---

## 0. Phase / wedge summary

| Phase | # | Wedge | Approx | Depends on |
|---|---|---|---|---|
| A | A1 | Envelope schema + writer-side compose helper | ~120 LOC | — |
| A | A2 | Hub callers compose envelopes + explicit `kind` param | ~190 LOC | A1 |
| A | A3 | Driver-side unwrap + render (incl. self-echo drop) | ~150 LOC | A1 |
| A | A4 | The message-admission pipeline | ~150 LOC | A1, A2 |
| B | B1 | Loop-entity data model — extend `tasks` + `attention_items` | ~120 LOC | — |
| B | B2 | Per-hop deadlines + the reconcile sweep + escalation | ~220 LOC | B1 |
| B | B3 | Lifecycle hooks — `PreAgentIdle`, `PostDirectiveOutcome` | ~150 LOC | B1 |
| B | B4 | The directive-trace query endpoint | ~110 LOC | A1, B1 |
| C | C1 | Per-persona prompts — the envelope + the loop | ~200 prose | A1–A3, B1–B2 |
| C | C2 | Mobile — render `from`/`kind`, handle `terminal_reason` | ~150 LOC | A1, B1 |

Order: **A1 → {A2, A3} → A4 → B1 → {B2, B3, B4} → {C1, C2}.** Phase A
and Phase B's B1 are independent and may proceed in parallel; C2 needs
only A1 + B1.

---

## 1. Wedges in detail

### Phase A — The envelope

#### A1 — Envelope schema + writer-side compose helper

The envelope Go struct + JSON shape per [ADR-032](../decisions/032-message-routing-envelope.md)
D-1, and a hub-server helper that composes it.

```go
type MessageEnvelope struct {
    From   MessageEndpoint `json:"from"`
    To     MessageEndpoint `json:"to"`
    Kind   string          `json:"kind"`   // directive|question|report|notification
    Text   string          `json:"text"`
    Cause  string          `json:"cause,omitempty"`  // ULID; "" = untied
    Thread MessageThread   `json:"thread"`
}
type MessageEndpoint struct {
    Role    string `json:"role"`    // principal|peer_steward|peer_worker|system
    Handle  string `json:"handle,omitempty"`
    AgentID string `json:"agent_id,omitempty"`
}
type MessageThread struct {
    Transport string `json:"transport"` // session|a2a|attention
    ID        string `json:"id"`
}
```

- New file `hub/internal/server/input_envelope.go`; helper
  `composeMessage(...)` — the single authoring point (ADR-032 D-6).
- `reply_via` is **not** a struct field — A3 derives + renders it.
- **Payload layout.** The envelope is marshaled **as the
  `agent_events` payload itself** — `{from,to,kind,text,cause,thread}`
  at the payload top level, *not* nested under
  `payload['body']` / `payload['envelope']`. The envelope's `text`
  field **replaces** the legacy `payload['body']` an `input.text` row
  carries today: the driver text branch reads `payload['body']`, so it
  is updated to the envelope in A3, and mobile's `input.text` path
  already falls back to `text`. This is a coordinated A1→A2→A3 change
  cut over on a drained hub (ADR-032 D-8) — not a silent additive.
- **Acceptance:** round-trips cleanly; every `kind`/`role` combination
  composes. **Tests:** `TestComposeMessage_*`.

#### A2 — Hub callers compose envelopes + explicit `kind` param

Every `input.text` `agent_events` row carries the envelope as its flat
payload, composed via A1's helper. The hub-server write sites
(verified against the code — *not* the three originally drafted):

1. **`handlers_agent_input.go`** (`handlePostAgentInput`) — writes the
   `input.text` row for **both** principal direct input
   (`producer=user` → `from.role=principal`, `kind=directive`) **and**
   the A2A post-back (`producer=a2a` → `from.role=peer_steward|
   peer_worker`, envelope `kind` from the relayed metadata). This is
   the single A2A `input.text` write site — the host-runner's
   `a2a_dispatcher` POSTs the relayed message back here.
2. **`task_notify.go`** (system task-outcome wakes) — `from.role=system`,
   `kind=notification`, `cause` = the task.
3. **`mcp_orchestrate.go`** (`postSyntheticUserInput` — fanout /
   spawn-with-task first turn) — `from.role=system`, `kind=directive`.

`tunnel_a2a.go` is **not** an `agent_events` write site — it is the
relay. It stamps envelope provenance (`from_role`, `from_handle`, plus
the sender-declared `kind`/`cause`) into the A2A body's
`message.metadata.termipod` bag, replacing the v1.0.630 `[A2A from
@sender]` text prefix; the recipient host-runner's `a2a_dispatcher`
forwards that bag to `handlePostAgentInput`.

And: `a2a_invoke` gains an explicit **`kind` parameter**
(`directive|question|report`) plus an optional `cause` — catalog entry
+ handler (ADR-032 D-6); the hub stamps them into the relay metadata.
(`delegate`, listed in the original draft, is a `channels`-row tool
unrelated to the message envelope — out of scope.)

- **Acceptance:** every post-A2 `input.text` row carries the envelope;
  the prefix decoration is gone. **Tests:** updated assertions in
  `task_notify_input_test.go`, `tunnel_a2a_test.go`,
  `handlers_agent_input_test.go`, `spawn_with_task_test.go`;
  `stampA2AEnvelopeMeta` tests.

#### A3 — Driver-side unwrap + render

A host-runner helper unwraps the envelope and renders an
engine-facing text turn — `from`, `kind`, and a derived **`reply_via`**
instruction (ADR-032 D-5). A `notification` renders the *kind's
contract* ("informational re directive X; no reply routed; act if it
concerns work you own"), never a bald "no reply expected."
Self-echo (`from == to`) is dropped here.

- New helper `hub/internal/hostrunner/input_envelope.go`. It is wired in
  at **`input_router.go`'s `tick()`** — the single chokepoint that
  dispatches every `input.*` event to every driver — *not* in each
  driver's `case "text":` branch. The rendered text replaces
  `payload["body"]`, the field every driver text branch already reads,
  so the drivers (claude-code sendkeys, `driver_appserver.go`,
  `driver_exec_resume.go`, `driver_pane.go`) stay envelope-agnostic and
  untouched. A no-envelope (legacy/malformed) row falls back to its raw
  `text` so the engine still receives something.
- **Acceptance:** envelope → rendered turn with the right reply
  instruction per `kind`/`role`; self-echo dropped. **Tests:**
  `TestRenderInboundEnvelope_*`, `TestDeriveReplyVia`,
  `TestInputRouter_SkipsSelfEcho`.

#### A4 — The message-admission pipeline

A deterministic pipeline at the hub-server compose boundary, run
before the `agent_events` row is written (ADR-032 D-7):

1. `validateEnvelope` — schema-valid; `cause` resolves to a live
   entity if set.
2. routing-legality — `from` permitted to address `to`; `deny > allow`;
   reuse [ADR-016](../decisions/016-subagent-scope-manifest.md)'s
   worker→non-parent A2A block as a deny rule.
3. context — an agent-declared `report` must reference an entity
   assigned to that agent / an open `question` to `to`. (Phase A
   implements the `cause`-resolvability half in stage 1; the
   assignee-scoped refinement lands with the loop-entity model in
   B1.)

Fail-safe: a malformed envelope from a hub writer site fails fast (a
programming error); a bad envelope from an agent is rejected with an
[ADR-031](../decisions/031-agent-tool-ergonomics.md) `hint` so the
agent can retry. Never crash, never silently drop.

- **Acceptance:** malformed/illegal envelopes are rejected with the
  classified outcome; valid ones admit. **Tests:**
  `TestAdmission_{Validate,RoutingLegality,Context}_*`.

### Phase B — Loop closure

#### B1 — Loop-entity data model: extend `tasks` + `attention_items`

The loop-entity is a **role over two existing tables, not a new
table** (ADR-034 D-8). A directive is a root `tasks` row, a task is a
child `tasks` row (`tasks.parent_task_id` already gives the tree); a
question is an `attention_item` row.

- An **additive** numbered migration — **no backfill, `status`
  unchanged**: `tasks` and `attention_items` each gain the per-hop
  deadline columns (`inactivity_deadline`, `last_progress_at`,
  `opened_at`, `absolute_cap`, `escalation_state`) and a
  `terminal_reason` column (the 5-value enum `completed | failed |
  killed | timed_out | superseded`, ADR-034 D-6), set on close
  alongside the unchanged human-facing `status`; `attention_items`
  also gains a `cause` pointer to its enclosing task. seed-demo's
  task fixtures are unaffected — their `status` values stay valid.
- A Go `LoopEntity` interface both tables satisfy; the open-set is a
  `UNION` over open `tasks` and open question-kind `attention_items`
  (ADR-034 D-1).
- **Acceptance:** every loop-entity closes with exactly one
  `terminal_reason`; the open-set query returns both kinds; no new
  table. **Tests:** `TestLoopEntity_*`, the additive-migration test.

#### B2 — Per-hop deadlines + the reconcile sweep + escalation

Per-hop deadline columns (`inactivity_deadline`, `last_progress_at`,
`opened_at`, `absolute_cap`, `escalation_state`) and the `loop_sweep`
goroutine — a periodic hub-server reconcile sweep (ADR-034 D-2/D-3).
Each tick: for every non-parked open entity, breach of the inactivity
deadline → a stall `notification` one level up (ADR-034 D-4), advancing
`escalation_state` idempotently; breach of the absolute cap →
`timed_out` termination.

- New `hub/internal/server/loop_sweep.go`, modelled on `host_sweep.go`.
- **Deadline population.** Deadlines are stamped *lazily* — the sweep
  stamps `opened_at` + the two deadlines on first sight of an unstamped
  open entity, so no task-create site needs to know about deadlines.
  Progress slides the inactivity deadline: `bumpLoopProgress` (wired
  into the agent-events append) resets it whenever the entity's
  assignee emits an event. Escalation pushes the deadline forward a
  budget so the next level fires after another window, not next tick.
  Budget calibration stays post-MVP (§2).
- **Acceptance:** a stalled hop escalates to the steward, then the
  principal, exactly once per level; a parked hop does not escalate.
  **Tests:** `TestLoopSweep_{Stall,Escalate,ParkedSkipped,AbsoluteCap}`.

#### B3 — Lifecycle hooks

Hub-side orchestration hooks, YAML-configured (ADR-034 D-5):
`PreAgentIdle` blocks an agent going idle while it owns open
loop-entities and re-wakes it with the open set; `PostDirectiveOutcome`
checks a closing `report` is a synthesis, not a bare relay.

- `loop_hooks.go` + the embedded `loop_hooks_defaults.yaml` config.
  `PreAgentIdle` fires on a `lifecycle` idle/stopped event in the
  agent-events append path; since a hub-side hook cannot hard-block the
  engine, it realises "block" as an immediate **re-wake** carrying the
  open set — functionally the same: the agent does not get to rest
  while it owns open work. `PostDirectiveOutcome` fires when a root
  task closes `done` (in `notifyTaskAssigner`) and records a
  `loop.relay_not_synthesis` audit flag on a bare-relay close.
- **Acceptance:** an agent with an open directive is re-woken on idle;
  a relay-only closing report is flagged. **Tests:** `TestHook_PreAgentIdle_*`,
  `TestHook_PostDirectiveOutcome_*`.

#### B4 — The directive-trace query endpoint

A hub endpoint reconstructing a directive's timeline by walking the
`cause` chain over `agent_events` + `attention_items` + `audit_events`
(ADR-034 D-7) — a query, no new event stream.

- **Acceptance:** the trace for a directive shows every hop in order,
  including a `[STALL]` marker. **Tests:** `TestDirectiveTrace_*`.
- The mobile *screen* that renders this endpoint is sequenced
  separately with the Layer-A surfaces (see §2).

### Phase C — Agent-facing

#### C1 — Per-persona prompts: the envelope + the loop

Each of the 10 main persona prompts (4 stewards + 6 workers) gains two
sections: **"How messages are addressed"** (the `kind` set, the `from`
roles, the reply mechanism per `kind`/`role`) and **"Closing the
loop"** (you own the directives addressed to you; emit a terminal
`report`; do not go idle with open work — `PreAgentIdle` will refuse
it anyway, but the prompt installs the disposition).

- Each `hub/templates/prompts/*.md`; near the top, before tool guidance.
- **Acceptance:** every main prompt has both sections; the bundled
  template var-ref audit passes. **Tests:** existing audit lint.

#### C2 — Mobile: render `from`/`kind`, handle `terminal_reason`

The mobile app is a second consumer of the envelope and of the
loop-entity tables — two changes keep it at parity with the schema
change:

1. **Envelope display.** The transcript feed reads `payload['text']`,
   which still resolves (A1's flat payload layout). It additionally
   reads `from` — a sender chip — and `kind` — a badge. This replaces
   what v1.0.630's `[A2A from @sender]` text prefix carried: without
   it, an A2A message would render with no visible sender.
2. **Terminal reasons.** `status` is **unchanged** (ADR-034 D-6 keeps
   it), so the ~10 hardcoded status sites and the status pickers in
   `task_detail_screen.dart` / `project_detail_screen.dart` keep
   working as-is. `terminal_reason` is rendered as *additive detail*
   on a closed task — e.g. "Cancelled — timed out" — on the task and
   attention surfaces.

The app reads hub entities as `Map<String, dynamic>` (no typed Dart
classes — CLAUDE.md), so this is rendering code: `lib/widgets/agent_feed.dart`
(the feed `from`/`kind`); the task surfaces — `task_detail_screen.dart`,
`project_detail_screen.dart`, `overview_widgets/task_milestone_list.dart`
— for `terminal_reason` detail; and the attention surfaces
(`me_screen.dart`, `approval_detail_screen.dart`), since `attention_items`
also carry `terminal_reason`.

- **Acceptance:** an A2A message in the transcript shows its sender; a
  `timed_out` task renders a styled chip, not a blank.
- **Tests:** widget tests; CI-verified — there is no local Flutter SDK.
- This is **parity only.** The directive-trace *screen* and the
  Layer-A surfaces remain out of scope (§2).

---

## 2. Out of scope for this plan

- **Layer-A awareness surfaces** — the principal's inbox, read-state,
  the Requests/Messages/Agents reframe (`feedback-loop-closure.md`
  §5). Mobile/IA work; sequenced separately; reconciles into
  `information-architecture.md`.
- **The directive-trace mobile screen** — B4 ships the hub endpoint;
  the Flutter screen rides with the Layer-A surfaces.
- **Sibling-outcome fan-out** — routing a worker's outcome to a
  *peer* steward in the same project. A routing-policy add-on, not the
  contract; post-MVP.
- **Per-engine adapter rendering** — engine-specific envelope text
  templates if engines diverge; one rendering for MVP.
- **Deadline-default calibration** — B2 ships configurable defaults;
  tuning the values is post-MVP on-device work.

---

## 3. Risks

- **Engines ignore the rendered envelope.** Mitigation: A3 renders an
  unambiguous text turn (the v1.0.630 prefix already worked); the
  hard tiers (A4, B2/B3) do not depend on the LLM reading anything.
- **The MVP is large** (9 wedges, ~1.4k LOC). Mitigation: Phase A and
  B1 parallelize; the rollout ships as one or two releases. Loop
  closure is in MVP by explicit 2026-05-19 decision — a forward-only
  contract that only hopes the loop closes is the half-duplex problem
  again.
- **The sweep escalates noisily.** Mitigation: idempotent
  `escalation_state` (B2) — one notification per level; parked hops
  skipped.
- **Cutover orphans an in-flight plain-string event.** Mitigation:
  cut over on a drained hub (ADR-032 D-8).

---

## 4. Acceptance for the bundle

The rollout ships ADR-032 + ADR-034 as one MVP (one or two releases).
Acceptance:

- A worker receiving an A2A message reads `from`/`kind` from the
  rendered envelope and replies via the stated mechanism — no body
  prefix to parse.
- A steward woken by a system `notification` sees it must *act*, not
  reply.
- A directive's loop is tracked: a stalled hop escalates to the
  principal with the stalled node named; the directive trace shows it.
- An agent cannot go idle with an open directive.
- The v1.0.630 prefix decoration is removed from `tunnel_a2a.go`.
- The mobile transcript shows an A2A message's sender, and renders the
  new `terminal_reason` values as styled chips.

On-device verification: principal directs a mission; steward dispatches
a worker; worker reports back and the loop closes; a deliberately
stalled worker escalates to the principal within one sweep interval.
This is the gate that flips [ADR-032](../decisions/032-message-routing-envelope.md)
and [ADR-034](../decisions/034-orchestration-loop-closure.md) to
`Accepted`.

---

## 5. References

- [ADR-032](../decisions/032-message-routing-envelope.md) — the message envelope (Phase A).
- [ADR-034](../decisions/034-orchestration-loop-closure.md) — the loop-closure runtime (Phase B).
- [`../discussions/orchestration-contract.md`](../discussions/orchestration-contract.md)
  — the design rationale; §6 is the envelope, §8 the lifecycle walkthrough.
- [`../discussions/feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
  — the half-duplex diagnosis; its Layer A is the deferred surface track (§2).
- [`../spine/orchestration-layer.md`](../spine/orchestration-layer.md)
  — the spine axiom this rollout implements.
- [`../discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md)
  — the principle behind A4's fail-safe admission.

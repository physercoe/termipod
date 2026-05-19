---
name: Message routing rollout
description: Phased rollout of the orchestration contract — ADR-032 (the message envelope {from,to,kind,text,cause,thread} + the message-admission pipeline) and ADR-034 (the loop-closure runtime — terminal-reason taxonomy, per-hop deadlines, the deadline sweep, stall escalation, lifecycle hooks, the directive trace). Nine wedges across hub-server, host-runner, and persona prompts; the whole rollout is MVP — loop closure is not deferred. Re-wedged 2026-05-19 from the original forward-only envelope plan per the orchestration-contract discussion.
---

# Message routing rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18; **re-wedged 2026-05-19**) — widened
> from the original forward-only envelope (6 wedges) to the full
> orchestration contract (9 wedges), per the
> [orchestration-contract discussion](../discussions/orchestration-contract.md).
> No work started. Implements [ADR-032](../decisions/032-message-routing-envelope.md)
> (the message envelope) and [ADR-034](../decisions/034-orchestration-loop-closure.md)
> (loop closure). The whole rollout is MVP.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** Replace the v1.0.626 / v1.0.630 band-aids with the
orchestration layer's real type system. Three phases, nine wedges,
**all MVP** — loop closure is not deferred (2026-05-19 decision):

- **Phase A — The envelope ([ADR-032](../decisions/032-message-routing-envelope.md), 4 wedges).**
  The envelope `{from,to,kind,text,cause,thread}`; hub-server composes
  it; drivers render it; the message-admission pipeline validates
  every envelope fail-safe.
- **Phase B — Loop closure ([ADR-034](../decisions/034-orchestration-loop-closure.md), 4 wedges).**
  The terminal-reason taxonomy + open-set tracking; per-hop deadlines
  and the sweep; stall escalation; lifecycle hooks; the directive
  trace.
- **Phase C — Agent-facing (1 wedge).** Per-persona prompts teaching
  the envelope and the loop.

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
| B | B1 | Terminal-reason taxonomy + loop-entity open-set | ~150 LOC | — |
| B | B2 | Per-hop deadlines + the reconcile sweep + escalation | ~220 LOC | B1 |
| B | B3 | Lifecycle hooks — `PreAgentIdle`, `PostDirectiveOutcome` | ~150 LOC | B1 |
| B | B4 | The directive-trace query endpoint | ~110 LOC | A1, B1 |
| C | C1 | Per-persona prompts — the envelope + the loop | ~200 prose | A1–A3, B1–B2 |

Order: **A1 → {A2, A3} → A4 → B1 → {B2, B3, B4} → C1.** Phase A and
Phase B's B1 are independent and may proceed in parallel.

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
- **Acceptance:** round-trips cleanly; every `kind`/`role` combination
  composes. **Tests:** `TestComposeMessage_*`.

#### A2 — Hub callers compose envelopes + explicit `kind` param

The three `agent_events` write sites compose the envelope via A1's
helper:

1. **`handlers_agent_input.go`** (principal input) — `from.role=principal`,
   `kind=directive`.
2. **`tunnel_a2a.go`** (A2A relay) — `from.role=peer_steward|peer_worker`,
   `kind` from the call's explicit parameter; drop the v1.0.630
   `[A2A from @sender]` prefix.
3. **`task_notify.go`** (system wakes) — `from.role=system`,
   `kind=notification` (or `directive` for a schedule fire).

And: `a2a_invoke` / `delegate` gain an explicit **`kind` parameter**
(`directive|question|report`) — catalog entry, dispatcher, handler
(ADR-032 D-6). The hub reads it, validates it (A4), stamps the
envelope.

- **Acceptance:** every post-A2 `input.text` row carries the envelope;
  the prefix decoration is gone. **Tests:** updated assertions in
  `task_notify_input_test.go`, `tunnel_a2a_test.go`,
  `handlers_agent_input_test.go`; `a2a_invoke` kind-param tests.

#### A3 — Driver-side unwrap + render

A host-runner helper unwraps the envelope and renders an
engine-facing text turn — `from`, `kind`, and a derived **`reply_via`**
instruction (ADR-032 D-5). A `notification` renders the *kind's
contract* ("informational re directive X; no reply routed; act if it
concerns work you own"), never a bald "no reply expected."
Self-echo (`from == to`) is dropped here.

- New helper `hub/internal/hostrunner/input_envelope.go`; called from
  each driver's `case "text":` branch (claude-code sendkeys,
  `driver_appserver.go`, `driver_exec_resume.go`, `driver_pane.go`).
- **Acceptance:** envelope → rendered turn with the right reply
  instruction per `kind`/`role`; self-echo dropped. **Tests:**
  `TestEnvelopeRender_*`, `TestInputRouter_SkipsSelfEcho`.

#### A4 — The message-admission pipeline

A deterministic pipeline at the hub-server compose boundary, run
before the `agent_events` row is written (ADR-032 D-7):

1. `validateEnvelope` — schema-valid; `cause` resolves to a live
   entity if set.
2. routing-legality — `from` permitted to address `to`; `deny > allow`;
   reuse [ADR-016](../decisions/016-subagent-scope-manifest.md)'s
   worker→non-parent A2A block as a deny rule.
3. context — an agent-declared `report` must reference an entity
   assigned to that agent / an open `question` to `to`.

Fail-safe: a malformed envelope from a hub writer site fails fast (a
programming error); a bad envelope from an agent is rejected with an
[ADR-031](../decisions/031-agent-tool-ergonomics.md) `hint` so the
agent can retry. Never crash, never silently drop.

- **Acceptance:** malformed/illegal envelopes are rejected with the
  classified outcome; valid ones admit. **Tests:**
  `TestAdmission_{Validate,RoutingLegality,Context}_*`.

### Phase B — Loop closure

#### B1 — Terminal-reason taxonomy + loop-entity open-set

Replace the thin `done/blocked/cancelled` with the enumerated
terminal-reason set `completed | blocked | failed | killed |
timed_out | superseded` (ADR-034 D-6), and add the hub-tracked **open
set** of loop-entities (directives / tasks / questions) — the basis
of the loop-closure invariant (ADR-034 D-1).

- A numbered SQL migration: terminal-reason column, loop-entity
  open/closed state, `parent` pointer for the `cause` tree.
- **Acceptance:** every loop-entity has exactly one terminal reason on
  close; the open set is queryable. **Tests:** `TestLoopEntity_*`,
  taxonomy migration test.

#### B2 — Per-hop deadlines + the reconcile sweep + escalation

Per-hop deadline columns (`inactivity_deadline`, `last_progress_at`,
`opened_at`, `absolute_cap`, `escalation_state`) and the `loop_sweep`
goroutine — a periodic hub-server reconcile sweep (ADR-034 D-2/D-3).
Each tick: for every non-parked open entity, breach of the inactivity
deadline → a stall `notification` one level up (ADR-034 D-4), advancing
`escalation_state` idempotently; breach of the absolute cap →
`timed_out` termination.

- New `hub/internal/server/loop_sweep.go`, modelled on `host_sweep.go`.
- **Acceptance:** a stalled hop escalates to the steward, then the
  principal, exactly once per level; a parked hop does not escalate.
  **Tests:** `TestLoopSweep_{Stall,Escalate,ParkedSkipped,AbsoluteCap}`.

#### B3 — Lifecycle hooks

Hub-side orchestration hooks, YAML-configured (ADR-034 D-5):
`PreAgentIdle` blocks an agent going idle while it owns open
loop-entities and re-wakes it with the open set; `PostDirectiveOutcome`
checks a closing `report` is a synthesis, not a bare relay.

- Hook dispatch surface + the two hook points; bundled YAML defaults.
- **Acceptance:** an agent with an open directive cannot idle; a
  relay-only closing report is flagged. **Tests:** `TestHook_PreAgentIdle_*`,
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

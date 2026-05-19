---
name: Orchestration loop-closure runtime
description: The Layer-B half of the orchestration contract — the runtime that guarantees a principal directive's loop closes rather than silently stalling. Establishes the loop-closure invariant (a directive is not done until a terminal report carrying its cause reaches the issuer's inbox); per-hop deadlines that localize stalls to a node; a hub-server periodic sweep as the deadline clock; stall escalation one level up the chain as the no-silent-sink guarantee; orchestration lifecycle hooks (PreAgentIdle, PostDirectiveOutcome) as YAML data; an enumerated terminal-reason taxonomy replacing thin done/blocked/cancelled; and a directive-trace view reconstructed from events by walking the cause chain. Companion to ADR-032 (the message envelope); together they are the orchestration contract.
---

# 034. Orchestration loop-closure runtime

> **Type:** decision
> **Status:** Proposed (2026-05-19) — the Layer-B half of the
> orchestration contract; companion to [ADR-032](032-message-routing-envelope.md)
> (the message envelope). Rationale in [`orchestration-contract.md`](../discussions/orchestration-contract.md)
> and [`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md).
> Ships in the same MVP rollout as ADR-032 — loop closure is MVP, not
> deferred (2026-05-19 decision). Flips to `Accepted` with ADR-032.
> **Audience:** contributors
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** [ADR-032](032-message-routing-envelope.md) types the
*messages* of the orchestration layer. This ADR types its *loop*: the
runtime that guarantees a principal's directive reaches an observable
terminal state rather than silently stalling at some hop. Seven
decisions: (D-1) the **loop-closure invariant** — a directive is not
`done` until a terminal `report` carrying its `cause` reaches the
issuer's inbox; (D-2) **per-hop deadlines** that localize a stall to a
specific node; (D-3) the deadline **clock is a hub-server periodic
sweep**, not per-hop timers and not derived-on-read; (D-4) **stall
escalation** one level up the chain — the "no silent sink" guarantee;
(D-5) **orchestration lifecycle hooks** (`PreAgentIdle`,
`PostDirectiveOutcome`) as YAML data; (D-6) an enumerated
**terminal-reason taxonomy** replacing thin `done/blocked/cancelled`;
(D-7) a **directive-trace** view reconstructed from events by walking
the `cause` chain — no new event stream. Together with ADR-032 this is
the orchestration contract; both ship in one MVP rollout.

---

## 1. Context

[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
diagnoses termipod as a **half-duplex loop**: the forward path
(principal → steward → worker) is designed; the return path exists
only for *exceptions* (an `attention_items` row). Normal progress, and
— worse — a *silently stalled hop*, produce no principal-facing
signal. [ADR-032](032-message-routing-envelope.md) gives every message
a `cause` (lineage) and a `kind`; that makes the loop *expressible*.
It does not make the loop *guaranteed to close*. A `directive` with no
`report` ever flowing back is, today, indistinguishable from one still
in progress.

This ADR is the runtime that closes that gap. The
[orchestration-contract discussion](../discussions/orchestration-contract.md)
§10 establishes it is **MVP, not deferred** — a contract that types
the forward path but only *hopes* the loop closes is the half-duplex
problem again — and that it warrants its own ADR because it is a
distinct architectural commitment (a runtime, not a schema) while
shipping in the same rollout as ADR-032.

## 2. Decisions

### D-1. The loop-closure invariant

A directive/task/question entity is **not `done`** in the data model
until a terminal `report` whose `cause` is that entity has reached the
issuer's inbox (and, transitively, the principal's for a root
directive). The hub tracks the set of *open* loop-entities and
structurally refuses to let one vanish. This is the north star every
other decision serves: D-2..D-4 detect non-arrival, D-5 prevents an
agent abandoning an open loop, D-6 enumerates the legal terminal
states, D-7 makes the open set inspectable. Rationale:
[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §6.8.

### D-2. Per-hop deadlines

Each open loop-entity carries an **expected-response deadline per
hop** — the time by which the node currently holding it must produce
its next signal (a dispatch, a `report`, a `question`). Deadlines are
**per-hop, not per-mission**: a global mission timeout tells you the
mission is late but not *where*; a per-hop deadline localizes the
stall to a specific node ("the steward received the directive 20 min
ago and has not dispatched"). Defaults come from the agent family /
template; a directive may override. Rationale:
[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §6.2.

### D-3. The deadline clock is a hub-server periodic sweep

The clock that fires on a missed deadline is a **periodic hub-server
sweep** over open loop-entities (`now − last_progress_at > deadline`),
following the existing `host_sweep.go` pattern.

Rejected: **per-hop timer goroutines** — one goroutine per open hop
does not scale and complicates cancellation. **Derived-on-read** —
computing staleness only when something queries cannot fire when the
principal is *not* looking, which is precisely the silent-sink failure
this ADR exists to kill. The sweep runs unconditionally on the hub,
which is the loop's terminal observer of last resort.

### D-4. Stall escalation — the no-silent-sink guarantee

When the sweep finds a breached deadline, the hub emits a
`notification` ([ADR-032](032-message-routing-envelope.md) D-2)
**one level up** the chain: a worker stall notifies its parent
steward; if that steward is itself unresponsive past *its* deadline,
the next sweep escalates to the principal. The notification carries
the stalled entity's `cause` so it lands in the right directive trace.
This makes "no node may be a silent sink"
([`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §3)
a *structural* guarantee rather than a per-agent hope. The rendered
notification states action is expected (ADR-032 D-5) — it is not a
bare FYI.

### D-5. Orchestration lifecycle hooks, as data

The hub runs **lifecycle hooks** at orchestration events, configured
as **YAML data** (per CLAUDE.md "behaviour is data"; the model is
Claude Code's hook system — declarative config, a `decision: block`
protocol):

- **`PreAgentIdle`** — fires when an agent attempts to go idle or
  terminate. If it still owns open loop-entities, the hook **blocks**
  the idle and re-wakes the agent with the open set. This is the D-1
  invariant enforced at the agent boundary — the distributed analog of
  Claude Code's `Stop` hook.
- **`PostDirectiveOutcome`** — fires when a `report` closes a
  directive. Validates the report is a genuine **synthesis**, not a
  bare relay of a child's output ("synthesis is not relay" —
  [`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §6.7).

Hooks are an extensibility surface: a new closure policy is a YAML
file, not Go code. Message-level admission is **not** a hook — that is
ADR-032 D-7's deterministic pipeline; hooks here are loop-*lifecycle*.

### D-6. The terminal-reason taxonomy

A loop-entity terminates with exactly one **enumerated reason**, each
with defined cleanup and a defined inbox consequence, replacing the
thin `done / blocked / cancelled`:

`completed · blocked · failed · killed · timed_out · superseded`

A normal close is `completed` via a terminal `report` (ADR-032 D-2).
`timed_out` is raised by the D-3 sweep; `killed` is operator
termination (distinct from `failed` — the v1.0.628 fix already cracked
the thin model); `superseded` is a directive replaced by a newer one.
Each terminal reason is delivered to the issuer as a `report` (normal)
or a `notification` (abnormal — `timed_out`, `killed`). Rationale:
[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §9 Q-T.

### D-7. The directive trace — reconstructed, not a new stream

The per-directive timeline ("principal issued → steward received →
task dispatched → … → [STALL 18m] → …") is **reconstructed** by
querying `agent_events` filtered on the `cause` chain (walk
`entity.parent`), joined with `attention_items` and `audit_events` —
**not** a new event stream. ADR-032's `cause` makes this a query, not
new plumbing. The trace is the principal's single screen for "which
node is holding the ball." Rationale:
[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §6.5.

## 3. Consequences

**Positive.** A principal directive cannot silently vanish — every
loop reaches an enumerated terminal state, and a stall surfaces *to
the principal* with the stalled node named, instead of being found by
debugging. The trace is one screen. Loop latency becomes a measured
quantity (realization efficiency — discussion §7).

**Negative.** Real new runtime: a sweep job, deadline columns, the
hook dispatch surface, the terminal-reason migration. The MVP rollout
is larger because this ships with ADR-032 rather than after it.

**Neutral / deferred.** Layer-A awareness surfaces (read-state, the
inbox, the three-tab reframe — `feedback-loop-closure.md` §5) are
mobile/IA work; they consume this runtime but are sequenced separately
and reconcile into `information-architecture.md`. Deadline-default
tuning and the escalation-target policy (straight to principal vs up
the steward chain) are config, refined post-MVP.

## 4. Alternatives considered

| Alternative | Why rejected |
|---|---|
| Global per-mission deadline | Tells you the mission is late, not *where*. D-2. |
| Per-hop timer goroutines | Does not scale; cancellation complexity. D-3. |
| Derived-on-read staleness | Cannot fire when nobody is looking — the silent-sink bug itself. D-3. |
| A dedicated directive-trace event stream | New plumbing; `cause` already makes the trace a query. D-7. |
| Defer Layer B to post-MVP | A forward-only contract that only hopes the loop closes is half-duplex. Discussion §10. |
| Keep thin `done/blocked/cancelled` | No defined cleanup or inbox consequence per reason; v1.0.628 already cracked it. D-6. |

## 5. Implementation

Ships in the same MVP rollout as [ADR-032](032-message-routing-envelope.md)
— see [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
(to be re-wedged to cover both ADRs). Build order: D-6 taxonomy + D-1
open-set tracking (data model) → D-2/D-3 deadlines + sweep → D-4
escalation → D-5 hooks → D-7 trace view. Flips to `Accepted` with
ADR-032 after on-device verification.

## 6. References

- [`../discussions/feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
  — the half-duplex diagnosis and Layer A / Layer B split; this ADR is Layer B.
- [`../discussions/orchestration-contract.md`](../discussions/orchestration-contract.md)
  — the unifying frame; §10 establishes Layer B as MVP.
- [ADR-032](032-message-routing-envelope.md) — the message envelope; `cause` and `kind` are this ADR's substrate.
- [ADR-029](029-tasks-as-first-class-primitive.md) — Task, the loop-entity this runtime tracks.
- [ADR-022](022-observability-surfaces.md) — insights consume the trace and loop-latency metrics.
- [ADR-016](016-subagent-scope-manifest.md) — escalation respects the steward accountability chain.

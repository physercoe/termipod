---
name: Orchestration loop-closure runtime
description: The Layer-B half of the orchestration contract — the runtime that guarantees a principal directive's loop closes rather than silently stalling. Establishes the loop-closure invariant (a directive is not done until a terminal report carrying its cause reaches the issuer's inbox); per-hop deadlines that localize stalls to a node; a hub-server periodic sweep as the deadline clock; stall escalation one level up the chain as the no-silent-sink guarantee; orchestration lifecycle hooks (PreAgentIdle, PostDirectiveOutcome) as YAML data; an additive terminal-reason column that augments (not replaces) the human-facing task status; a directive-trace view reconstructed from events by walking the cause chain; and the loop-entity modelled as a role over the existing tasks and attention_items tables — no new table. Companion to ADR-032 (the message envelope); together they are the orchestration contract.
---

# 034. Orchestration loop-closure runtime

> **Type:** decision
> **Status:** Proposed (2026-05-19) — the Layer-B half of the
> orchestration contract; companion to [ADR-032](032-message-routing-envelope.md)
> (the message envelope). Rationale in [`orchestration-contract.md`](../discussions/orchestration-contract.md)
> and [`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md).
> Ships in the same MVP rollout as ADR-032 — loop closure is MVP, not
> deferred (2026-05-19 decision). **Implemented 2026-05-19** (all 10
> wedges on `main`, build + hub test suite green); stays `Proposed`
> until the on-device verification gate, then flips to `Accepted` with
> ADR-032.
> **Audience:** contributors
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** [ADR-032](032-message-routing-envelope.md) types the
*messages* of the orchestration layer. This ADR types its *loop*: the
runtime that guarantees a principal's directive reaches an observable
terminal state rather than silently stalling at some hop. Eight
decisions: (D-1) the **loop-closure invariant** — a directive is not
`done` until a terminal `report` carrying its `cause` reaches the
issuer's inbox; (D-2) **per-hop deadlines** that localize a stall to a
specific node; (D-3) the deadline **clock is a hub-server periodic
sweep**, not per-hop timers and not derived-on-read; (D-4) **stall
escalation** one level up the chain — the "no silent sink" guarantee;
(D-5) **orchestration lifecycle hooks** (`PreAgentIdle`,
`PostDirectiveOutcome`) as YAML data; (D-6) an enumerated
**terminal-reason column** augmenting the human-facing `status`;
(D-7) a **directive-trace** view reconstructed from events by walking
the `cause` chain — no new event stream; (D-8) the loop-entity is a
**role over the existing `tasks` and `attention_items` tables**, not a
new table. Together with ADR-032 this is the orchestration contract;
both ship in one MVP rollout.

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

### D-2. Per-hop deadlines — two kinds

Each open loop-entity carries deadlines **per hop** — bound to the
node currently holding it, not to the whole mission. A global mission
timeout tells you the mission is late but not *where*; a per-hop
deadline localizes the stall ("the steward received the directive 20
min ago and has not dispatched"). Two kinds, both checked by D-3:

- An **inactivity deadline** (sliding) — the entity is stalled if
  `now − last_progress_at > inactivity_deadline`. `last_progress_at`
  is bumped by *any* event on the entity (a child `report`, a tool
  call, a turn). This is the primary detector: a hop still producing
  output is alive even if slow.
- An optional **absolute cap** — `now − opened_at > absolute_cap`
  terminates the entity `timed_out` (D-6). It bounds a hop that emits
  noise forever without real progress.

Both **pause** while the entity is legitimately parked awaiting a
human decision (the permission-aware-deadline pattern, v1.0.448) — a
parked hop is not a stalled hop. Defaults come from the agent family /
template; a directive may override. Rationale:
[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §6.2.

### D-3. The deadline clock is a periodic reconcile sweep

The clock is a single hub-server **reconcile sweep** — a `loop_sweep`
goroutine ticking every ~30–60s (configurable; must be ≪ the smallest
deadline). Each tick scans the open loop-entities and, for every
non-parked one, compares elapsed time to its D-2 deadlines — emitting
a stall escalation (D-4) or a `timed_out` termination (D-6). It
follows termipod's existing `host_sweep.go` pattern.

Reasoned from first principles:

- **Crash-recoverable for free.** Deadline state is persisted columns
  (`deadline`, `last_progress_at`, `opened_at`, `escalation_state`); a
  hub restart loses nothing — the next tick re-derives. In-memory
  per-entity timers would not survive a restart.
- **No timer churn.** With a sweep, a progress event or a deadline
  change is one column write. With per-entity timers, every progress
  event forces a cancel-and-reschedule; at fleet scale (hundreds of
  open entities, frequent progress) that churn dominates.
- **Fires unobserved.** A periodic sweep runs unconditionally — it
  detects a stall whether or not anyone is looking. Derived-on-read
  (computing staleness only on query) structurally *cannot* fire when
  nobody queries — precisely the silent-sink failure this ADR exists
  to kill ([`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §3).
- **Coarse granularity suffices.** Hop deadlines are minutes; a
  30–60s sweep gives ≤ one-tick detection lag, negligible against a
  minutes-scale deadline. Sub-second precision (timer wheels) is
  unwarranted machinery.

It is the **reconcile-loop pattern** — the shape of Kubernetes
controllers, lease / visibility-timeout reapers, and job-queue
sweepers — not an invention.

**Idempotent escalation.** Each entity carries an `escalation_state`
(`none → escalated_steward → escalated_principal`); the sweep advances
it one level per breach and never re-fires a level already escalated.
This bounds notification volume — the sweep cannot spam.

Rejected: **per-hop timer goroutines** (not crash-recoverable; timer
churn). **Derived-on-read** (cannot fire unobserved — the silent-sink
bug itself). **Hierarchical timing wheels** (high-precision machinery
unwarranted for minutes-scale deadlines).

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

### D-6. The terminal-reason taxonomy — additive, not a replacement

The human-facing lifecycle `status` (`todo / in_progress / blocked /
done / cancelled`) is **kept unchanged**. A new **`terminal_reason`**
column is added — set when a loop-entity closes — with five values:

`completed · failed · killed · timed_out · superseded`

`status` and `terminal_reason` serve two consumers: `status` is the
task-management lifecycle the human UI renders (load-bearing across
~10 mobile sites, including status pickers); `terminal_reason` is the
close-classification the loop-closure runtime needs (the sweep,
escalation, the trace, realization-efficiency). They are additive,
not redundant — `done` + `completed` is a workflow state plus a close
reason. `terminal_reason` refines the outcome: `done` → `completed`;
`cancelled` → `failed | killed | timed_out | superseded` — so a
*failed* task has a clean home (`cancelled` is the umbrella, the
reason says why). `timed_out` is raised by the D-3 sweep; `killed` is
operator termination (distinct from `failed`); `superseded` is a task
replaced by a newer one. A close is delivered to the issuer as a
`report` (normal) or a `notification` (abnormal — `timed_out`,
`killed`).

**`blocked` is a live `status`, never a terminal reason.** A blocked
task is *open*, awaiting intervention (v1.0.628's "preserve blocked on
manual stop" treats it as a live state). A `report` carrying a
`blocked` outcome *advances* the entity; only a *terminal* `report`
closes it (ADR-032 D-2).

This **augments** the thin model rather than replacing it: the
inability of `done/blocked/cancelled` to express *why* a task ended is
cured by the new column, without churning the human-facing `status`
set. Rationale: [`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
§9 Q-T; orchestration-contract discussion (2026-05-19 `lib/`
verification).

### D-7. The directive trace — reconstructed, not a new stream

The per-directive timeline ("principal issued → steward received →
task dispatched → … → [STALL 18m] → …") is **reconstructed** by
querying `agent_events` filtered on the `cause` chain (walk
`entity.parent`), joined with `attention_items` and `audit_events` —
**not** a new event stream. ADR-032's `cause` makes this a query, not
new plumbing. The trace is the principal's single screen for "which
node is holding the ball." Rationale:
[`feedback-loop-closure.md`](../discussions/feedback-loop-closure.md) §6.5.

### D-8. The loop-entity data model — a role over two existing tables

The loop-entity (D-1's open-set, D-2's deadlines, D-6's terminal
reasons, D-7's trace) is **not a new table.** It is a *role* that two
existing primitives satisfy:

- A **directive** and a **task** are both `tasks` rows. `tasks`
  already carries `parent_task_id` (self-referential), `status`,
  `assignee_id`, and `created_by_id` — it is already a tree of
  addressed work. A directive is simply a **root task**
  (`parent_task_id` NULL, `assignee_id` = a steward); a task is a
  child task. The directive/task distinction is *positional* — it is
  already encoded by `parent_task_id`.
- A **question** is an `attention_item` row — a pending ask, not a
  unit of work. `attention_items` already carries the question-shaped
  kinds (help / select / approval / elicit) and a resolved/unresolved
  lifecycle.

The loop-closure runtime operates over a Go `LoopEntity` interface
that both tables satisfy; the open-set (D-1) is a `UNION` over open
`tasks` and open question-kind `attention_items`; `cause`
([ADR-032](032-message-routing-envelope.md) D-3) is a single ULID
resolving to a `tasks` *or* an `attention_items` row, validated by the
admission pipeline against both.

The migration is **additive — no new table:**

- `tasks` gains the D-2 deadline columns (`inactivity_deadline`,
  `last_progress_at`, `opened_at`, `absolute_cap`, `escalation_state`)
  and a `terminal_reason` column (the D-6 enum) — additive, set on
  close; the human-facing `status` set is unchanged.
- `attention_items` gains the same deadline columns and a `cause`
  pointer to its enclosing task.

Rejected: a new `directives` table (a near-clone of `tasks` for a
distinction `parent_task_id` already records); a unified
`loop_entities` table (collapsing `tasks` + `attention_items` is a
rearchitecture, and conflates a unit of work with a pending ask — the
system rightly separates the task surface from the attention queue).

**Consequence for [ADR-029](029-tasks-as-first-class-primitive.md).**
Its Task is "the first-class unit of *steward-dispatched* work"; a
root task — a principal directive — is *principal*-dispatched.
ADR-029's scope broadens to "the first-class unit of *directed
work*": a wording refinement, not a structural change. The glossary
`Task` entry follows when this ADR is Accepted.

Rationale: orchestration-contract discussion §6.3; verified against
`migrations/0001_initial.up.sql` (`tasks` already has
`parent_task_id`, `status`, `assignee_id`).

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
| Keep `status` thin, no `terminal_reason` | `done/blocked/cancelled` cannot express *why* a task ended; D-6 augments it with an additive column. |
| Make `status` lifecycle-only (drop `done`/`cancelled`) | Churns ~10 mobile sites + the status pickers + a data backfill; D-6 keeps `status` and adds alongside. |

## 5. Implementation

**Implemented 2026-05-19** in the same MVP rollout as
[ADR-032](032-message-routing-envelope.md) — see
[`message-routing-rollout.md`](../plans/message-routing-rollout.md)
(re-wedged 2026-05-19 to cover both ADRs), shipped as 10 wedges on
`main` (commits `eb12a09`…`2a498df`). This ADR is Phase B: B1 extends
`tasks` + `attention_items` (migration `0042` — deadline columns,
`terminal_reason`, the open-set) → B2 the deadlines + reconcile sweep +
escalation → B3 the lifecycle hooks → B4 the directive trace. The hub
build and full Go test suite are green. Flips to `Accepted` with
ADR-032 once the on-device verification gate passes.

## 6. References

- [`../discussions/feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
  — the half-duplex diagnosis and Layer A / Layer B split; this ADR is Layer B.
- [`../discussions/orchestration-contract.md`](../discussions/orchestration-contract.md)
  — the unifying frame; §10 establishes Layer B as MVP.
- [ADR-032](032-message-routing-envelope.md) — the message envelope; `cause` and `kind` are this ADR's substrate.
- [ADR-029](029-tasks-as-first-class-primitive.md) — Task, the loop-entity this runtime tracks.
- [ADR-022](022-observability-surfaces.md) — insights consume the trace and loop-latency metrics.
- [ADR-016](016-subagent-scope-manifest.md) — escalation respects the steward accountability chain.

## 7. Amendments

### 2026-05-19 — per-project deadline override

D-2 said the per-hop deadline budgets "come from the agent family /
template; a directive may override," and the rollout shipped them as
hardcoded Go constants (`loop_sweep.go` — `loopInactivityBudget`,
`loopAbsoluteCapBudget`) with deadline-default calibration deferred.
This amendment realises the override as a **per-project setting**:

- Migration `0043` adds two nullable columns to `projects` —
  `loop_inactivity_minutes` and `loop_absolute_cap_minutes`. `NULL` =
  use the hub default; a positive integer overrides it for every
  loop-entity in the project.
- `loopBudgets(ctx, projectID)` resolves them; the sweep uses it
  everywhere it sets a deadline — lazy-stamp, the escalation push, and
  the per-task progress bump.
- It is **settable from the mobile project-edit sheet** (two
  minutes fields) and over the `projects.update` REST/MCP path.

**Why per-project columns, not `policy_overrides_json`.** The deadline
budgets are a typed, first-class, mobile-settable orchestration
setting; `policy_overrides_json` is the free-form bag the policy
engine reads. Dedicated columns keep the value typed end to end (int
minutes, no JSON merge on the mobile side) and keep loop-closure
config separate from policy-engine config.

### 2026-05-19 — lifecycle-hook config disk overlay

D-5's hooks shipped configured by `loop_hooks_defaults.yaml`, but
bundled via `//go:embed` — changing a hook needed a rebuild. This
amendment gives the hook config a disk overlay, mirroring the
agent-family-YAML pattern:

- `Server.New()` seeds `<dataRoot>/loop-hooks.yaml` from the embedded
  default when absent (never overwriting an operator edit), then loads
  it; the embedded YAML stays the fallback for a missing / unparseable
  overlay (fail-safe).
- SIGHUP hot-reloads the overlay — a hook can be toggled without even
  a restart, alongside `policy.yaml`.
- The live config is held in an `atomic.Value` so the sweep / request
  goroutines never race the reload.

### Still not configurable

Intentionally so for MVP — the remaining loop-closure enforcement
knobs:

- the **sweep interval** (`loopSweepInterval`, 45 s) — a daemon-level
  operational constant, not a per-project concern;
- the **escalation-target policy** (always one level up the chain) —
  §3 already records this as post-MVP config;
- the **question-kind set** (`questionAttentionKinds`) — a structural
  Go constant, not a tunable.

Surfacing the escalation-target policy is the natural next
configurability step.

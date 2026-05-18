---
name: Feedback-loop closure — the return path to the principal
description: Termipod's forward path (principal → steward → worker) is designed; the return path is not. An agent reaches the principal only by raising an attention_items row — exceptions surface, normal progress does not. There is no read-state model, no unread marker on session cards, no inbox of agent-generated output, and no liveness guarantee that a stalled hop escalates instead of silently swallowing a directive. The doc diagnoses termipod as a half-duplex loop, separates the problem into Layer A (awareness — read-state, unread, inbox) and Layer B (liveness — correlation, deadlines, stall escalation, directive trace), audits what surfaces today vs what does not, and recommends a phased design with Layer A first.
---

# Feedback-loop closure — the return path to the principal

> **Type:** discussion
> **Status:** Open (2026-05-18) — raised as the symmetric half of
> [ADR-032](../decisions/032-message-routing-envelope.md): that ADR
> routes messages *to* agents; this doc asks how messages and progress
> get *back to the principal*, and how the loop is guaranteed to close.
> No ADR locked yet — discussion only.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** An interactive agent runtime (claude-code) has a closed
event loop: user input → turn → output → user sees it → repeat. The
user sits at the terminal end; output cannot fail to arrive. Termipod
is the distributed, multi-hop, asynchronous generalization of that
loop — `principal → steward → worker(s) → …back up… → principal` —
but **only the forward path is designed.** The return path exists
only for *exceptions*: an agent reaches the principal if and only if
it raises an `attention_items` row (approval, error, idle,
revision-requested). Normal progress — a turn completes, a steward
writes a status report, a worker emits a result — produces
`agent_events` transcript rows that **nothing watches on the
principal's behalf.** There is no read-state model, no unread marker
on the active-session card, no inbox of agent→principal messages, and
no liveness guarantee that a silently-stalled hop escalates rather
than swallowing the directive. This doc splits the gap into **Layer A
(awareness)** and **Layer B (liveness)**, audits the current
surfaces, and recommends a phased design — Layer A first.

---

## 1. The diagnosis: a half-duplex loop

claude-code's loop is **closed, in-process, single-hop.** The user is
always at the terminal end of the pipe; there is nowhere else for
output to go, so nothing the agent does can fail to reach them.

Termipod is the **distributed, multi-hop, asynchronous** version:

```
principal ──directive──▶ steward ──task──▶ worker(s)
    ▲                       │                  │
    └───────?───────────────┴────────?─────────┘
                  the return path
```

The forward path is a designed primitive:

- principal → agent: `input.text`, task dispatch, the A2A envelope
  ([ADR-032](../decisions/032-message-routing-envelope.md)).

The return path is designed **only for exceptions.** An agent reaches
the principal exactly when it writes an `attention_items` row:
`approval_request`, `permission_prompt`, `select`, `help_request`,
`elicit`, `template_proposal`, `project_steward_request`, `idle`,
`agent_error`, `revision_requested`. Everything else an agent
produces is an `agent_events` transcript row, and **no surface watches
those on the principal's behalf.**

So the principal learns about agent activity through exactly two
channels:

1. **Attention items** — exceptions and asks.
2. **Manually opening a session** and reading the transcript.

There is no third channel for "this agent produced output you have
not seen." That is the half-duplex gap. [ADR-032](../decisions/032-message-routing-envelope.md)
closed routing *into* agents; the symmetric half — routing *back to
the principal*, and guaranteeing the loop closes — is the subject of
this doc, and it is larger than ADR-032.

---

## 2. The observations that raised this

From on-device use, the principal asked a cluster of questions that
all point at the same gap:

- *If an agent generates a new message, how does the principal know?*
  They do not, unless it is an attention item.
- *The current design audits A2A events (tool calls), not normal
  transcript log changes.* Correct — `audit_events` gets an
  `a2a.message_sent` row (added v1.0.608), but a worker producing a
  transcript turn produces no audit signal and no principal-facing
  signal.
- *There is no unread / new-message marker on the active-session
  card.* Correct — `_ActiveSessionsStrip` (`lib/screens/me/me_screen.dart`)
  renders title / scope / steward / engine·host and nothing else. A
  grep for `unread` / `last_read` / `last_seen` across `lib/` and
  `hub/` returns nothing.
- *What is the Agents tab for?* It filters attention to `idle` and
  `agent_error` only — exception kinds, not progress.
- *What is the Messages tab for?* It is the **default bucket** — every
  attention kind that is not a Request and not an Agent kind
  (`revision_requested`, generic). It is a catch-all junk drawer, not
  an inbox of agent output.
- *Where does the principal find which agent produced new
  transcript/output?* Nowhere. They open each session by hand.

Every observation is accurate. They are symptoms of one undesigned
primitive.

---

## 3. First principles

1. **Every directive has an observable terminal state.** When the
   principal directs the steward (and the steward a worker), the
   principal must be able to learn — without polling — one of
   `in-progress / done / blocked / failed`. Today `blocked` and `done`
   surface via task status and the v1.0.626 wake; `in-progress with
   new output` and `silently stalled` surface as nothing. The terminal
   set should be *enumerated* with defined cleanup per reason — not the
   thin `done/blocked/cancelled` of today (see §8 prior art and the
   open question Q-T in §9).

2. **No node may be a silent sink.** Each hop in
   principal → steward → worker is a potential black hole. If a
   steward receives a worker's `blocked` and does nothing, the
   principal never finds out. Liveness must be a *system* guarantee,
   not a per-agent good-behavior hope.

3. **The principal is the loop's terminal observer.** Like
   claude-code's user, the principal sits at the end of the loop.
   *Something* must always flow back — even if only "still working" or
   "stuck at step X."

4. **Separate the firehose from the digest.** The principal cannot
   watch transcripts. They need a low-cardinality "what changed since
   I last looked," with drill-in. The Me page's "Since you were last
   here" digest (`activity_digest_card.dart`) is a start — but it
   digests the *activity feed*, not unseen *agent output*.

---

## 4. Two separable layers

The principal raised these as one question; they are two problems:

| | **Layer A — Awareness** | **Layer B — Liveness** |
|---|---|---|
| Question | "Which agent produced output I have not seen?" | "Is the loop still moving, or did a node drop the ball?" |
| Tells you about | Output that *did* arrive | Output that *never will* (a stalled hop) |
| Primitive | read-state, unread markers, an inbox | correlation IDs, per-hop deadlines, stall escalation, a directive trace |
| Plane | UI + data model | protocol + runtime |

They are independent — A can ship without B. But the principal's
framing — *"if there is something wrong, principal should know
instead of debugging"* — is **purely Layer B.** Layer A only reports
output that arrived; it is structurally blind to a directive that
vanished because a hop went silent.

Both are needed. Recommended sequencing: **A first** (smaller, a
visible UX win, no protocol change), **B second**.

---

## 5. Layer A — Awareness

### 5.1 The read-state primitive

`agent_events` already carries a monotonic per-feed `seq` (the
`maxSeq` cursor in `agent_events_provider.dart` uses it for
resubscribe dedup). Add a hub-side table:

```
session_read_state(principal_id, session_id) → last_read_seq, last_read_at
```

Hub-side, not device-local, so read-state follows the principal
across devices. `unread = max_seq(session) > last_read_seq`.
`SessionChatScreen` advances the cursor on scroll-to-bottom.

That single primitive unlocks:

- An unread dot / count on the active-session card in
  `_ActiveSessionsStrip`.
- A real "what is new" surface (see §5.3).
- A per-session "N new since you left" line.

### 5.2 The principal has an inbox — and it is ADR-032's dual

[ADR-032](../decisions/032-message-routing-envelope.md)'s envelope
carries a `reply_via` field (`chat / a2a / attention_reply / none`).
That field *is* the Requests-vs-Messages distinction:

- `reply_via ≠ none` → the principal must act → a **Request**.
- `reply_via = none` → agent→principal FYI → a **Message**.

The reframe: **the principal has an inbox, and it is the symmetric
dual of ADR-032's envelope.** Just as a message routed *to* an agent
gets an envelope, a message routed *to the principal* — a steward
status report, a worker result summary, an A2A whose ultimate
recipient is the principal — should be a **first-class inbox item**:
not a transcript row nobody reads, and not crammed into
`attention_items` where "you must act" and "FYI" are conflated.

Today the `Messages` tab is a junk drawer *precisely because* that
primitive does not exist — there is nothing principled to put in it,
so it collects whatever the other two filters reject.

### 5.3 Reframing the three Me-page tabs

| Tab | Today | Proposed |
|---|---|---|
| **Requests** | attention kinds = approval/permission/select/help/elicit/template-proposal/steward-request | unchanged — inbox items where `reply_via ≠ none` |
| **Messages** | catch-all: `revision_requested` + generic | inbox items where `reply_via = none` — steward reports, worker result summaries, A2A addressed to the principal |
| **Agents** | `idle` + `agent_error` only | agent *lifecycle + liveness* — spawned / idle / archived / **stalled** / error (Layer B feeds the "stalled" kind) |

The split becomes principled: **Requests = act, Messages = read,
Agents = the fleet's health.** Each maps to a clear data source.

### 5.4 The progress-tick — a UI-only progress event

Unread/inbox tells the principal an agent *produced* something. It
does not tell them an agent is *alive and advancing* between outputs.
Claude Code's in-process loop has a dedicated event type for exactly
this — `ProgressMessage` (§8): a real-time progress signal that is
UI-only and never enters the model's context.

Termipod has no equivalent. An agent mid-task produces transcript
rows (too granular, nobody watches) or an attention item (too heavy —
it implies an ask) — nothing in between. A lightweight, UI-only
progress-tick event — "turn N started", "tool X running", "still
working" — would let the active-session card and the Agents tab show
*advancing* vs *quiet* without a transcript dive. It feeds Layer B's
liveness classifier too (§6.4): a progress-tick is a heartbeat.

### 5.5 Layer A is mostly UI + a little plumbing

No protocol change of consequence. One hub table, one cursor-advance
endpoint, an unread badge on two widgets, a writer that promotes
`reply_via = none` envelopes into Message inbox rows, and a UI-only
progress-tick event. This is the MVP.

---

## 6. Layer B — Liveness

This is the "don't make me debug" half. Termipod has the parts
scattered but unassembled.

1. **End-to-end correlation.** Channels already carry `task_id` /
   `correlation_id` ([ADR-019](../decisions/019-channels-as-event-log.md)).
   Stamp *every* principal directive with a `correlation_id` and
   propagate it through every hop. "Where is correlation X right now?"
   becomes answerable.

2. **Per-hop deadline.** A directive carries an expected-response
   deadline at each hop. Termipod has *fragments* — permission-aware
   deadline, post-drain grace, `task.notify` — but **no end-to-end
   deadline owned by the principal's directive.**

3. **Stall escalation — the "no silent sink" guarantee.** When a hop
   goes quiet past its deadline, raise an attention item *one level
   up*, ultimately to the principal: "worker @x blocked 20m; steward
   @y has not responded." This makes Principle 2 structural rather
   than a hope.

4. **Liveness ≠ progress.** `steward_liveness.dart` already
   classifies `healthy / idle / stuck` on `(status, age(last_event_at))`
   — but only for the *team steward*. Generalize it to every agent,
   and keep two axes distinct: *engine-process healthy* vs *task
   advancing*. An agent can be alive and quiet.

5. **The directive-trace view — the single missing artifact.** A
   per-directive timeline:

   ```
   principal issued ─▶ steward received ─▶ task dispatched ─▶
   worker spawned ─▶ turn 1 … ─▶ worker blocked ─▶ steward notified
   ─▶ [STALL 18m] ─▶ …
   ```

   This is the distributed analog of claude-code's transcript: the
   principal opens it and *sees which node is holding the ball*.
   Without it, "something is wrong" means opening N sessions by hand.
   With it, debugging is one screen.

6. **Fail-fast vs feedback-to-recover.** Claude Code's loop (§8)
   splits errors two ways: some fail the turn immediately
   (`blocking_limit`), others convert into a message the model can
   recover from (`stop_hook_blocking`). Termipod's worker
   blocked-protocol (v1.0.627) is the second kind — error becomes
   feedback. Every boundary in the loop should classify its failures
   into one of these two; an *unclassified* failure is the
   silent-sink risk.

7. **Synthesis is not relay.** Claude Code's coordinator pattern
   (§8) forbids a coordinator writing specs "based on your findings"
   — it must personally comprehend worker output, because every
   intermediary in a message chain loses detail. The prompt-layer
   corollary for termipod: a steward that forwards a worker outcome
   without synthesizing it is a degradation node, not a relay.
   Steward prompts should *require* synthesis. This is the
   prompt-layer half of Principle 2 — no silent sink — sitting
   alongside the protocol-layer escalation in item 3.

8. **The loop-closure invariant.** A directive is not `done` in the
   data model until a terminal event has *propagated back to the
   principal's inbox*. The hub tracks the set of open directives and
   structurally refuses to let one vanish silently.

---

## 7. Relationship to existing decisions

- **[ADR-032](../decisions/032-message-routing-envelope.md)** — this
  doc is its symmetric half. ADR-032 routes *to* agents; the return
  path routes *to the principal*. The `reply_via` field ADR-032
  already defines is the hinge: it is what classifies a return message
  as Request vs Message (§5.2).
- **[ADR-019](../decisions/019-channels-as-event-log.md)** — channels
  already carry `correlation_id`. Layer B's tracing builds on it
  rather than inventing a new identifier.
- **[ADR-025](../decisions/025-project-steward-accountability.md)** —
  steward accountability defines *who* answers for a worker; the
  loop-closure invariant defines *by when* and *what the principal
  sees if they do not*.
- **`steward_liveness.dart`** — the `healthy/idle/stuck` classifier is
  Layer B's seed; it needs generalizing from steward-only to every
  agent.
- **v1.0.626 + v1.0.630** — both were forward-path band-aids; they do
  not touch the return path. This doc does not retire them.

---

## 8. Prior art — Claude Code's in-process loop

The single-process Claude Code runtime already implements, *in the
small*, the loop termipod must build *distributed*. (Source:
《御舆 — 解码 Agent Harness》, chapters 2 "对话循环 — Agent 的心跳"
and 10 "协调器模式" — `github.com/lintsinghua/claude-code-book`.)
The patterns port; the *mechanism* — an in-process `async function*`
generator — does not, because termipod's loop crosses hub / host /
mobile process boundaries.

Worth borrowing:

- **Tracked transitions.** Every loop iteration records a
  `transition` reason (`next_turn`, `max_output_tokens_recovery`, …),
  forming a built-in audit trail. Precedent for the directive-trace
  view (§6.5): termipod's distributed loop should stamp *why* each hop
  advanced, so the trace is reconstructable from events alone.
- **Enumerated terminal reasons.** The loop terminates with one of
  ten named reasons, each with defined cleanup
  (`completed / aborted_tools / max_turns / model_error / …`).
  Termipod's thin `done/blocked/cancelled` is the absence of this; the
  v1.0.628 fix was patching a missing taxonomy (see §9 Q-T).
- **`ProgressMessage`.** A real-time, UI-only progress event, never
  sent to the model. Termipod has no equivalent — the missing Layer A
  primitive in §5.4.
- **`SystemMessage` is UI-only**, structurally distinct from
  model-input events. The v1.0.626 incident — a `task_completed`
  system event no driver handled — was the cost of *not* having that
  separation.
- **Fail-fast vs feedback-to-recover** (§6.6) and **synthesis is not
  relay** (§6.7) — both named directly by chapters 2 and 10.
- **Coordinator-worker (chapter 10).** Termipod's
  principal → steward → worker *is* the coordinator pattern. Claude
  Code's worker→coordinator completion is an XML `<task-notification>`
  carrying `status` ∈ {completed, failed, killed} **plus metrics**
  (tokens, tool-call count, duration). Termipod's v1.0.626 wake landed
  on the same shape (notification in a user-role message) but carries
  no metrics — enriching the payload is a cheap borrow. The
  `killed`-distinct-from-`failed` split validates v1.0.628.

What does **not** port: generator-level backpressure, `.return()`
cancellation, and the local-filesystem scratchpad all assume one
process. Termipod must rebuild these as explicit distributed protocol
— flow control, RPC cancellation, hub-stored shared space. That
rebuild *is* this doc's thesis.

---

## 9. Open questions

1. **One ADR or two?** Layer A (envelope/inbox/read-state) and Layer B
   (deadlines/escalation/trace) are distinct architectural
   commitments. A single ADR-033 covering both, or ADR-033 (return
   path / inbox) + ADR-034 (liveness / loop closure)? Lean: decide
   after this discussion settles; do not pre-commit.
2. **Read-state granularity.** Per-session is the obvious unit. Do
   sub-session feeds (a worker's feed inside a project the principal
   never opened directly) need their own cursor, or does
   roll-up-to-session suffice for MVP?
3. **Who owns the deadline clock?** Hub-side timer vs a sweep job vs
   derived-on-read from `last_event_at`. The `host_sweep.go` pattern
   is a precedent.
4. **Escalation target.** Always the principal, or up the steward
   chain first (worker stall → project steward → general steward →
   principal)? The latter respects [ADR-025](../decisions/025-project-steward-accountability.md)
   but adds latency to the principal's awareness.
5. **Does the trace view need a new event stream**, or can it be
   *reconstructed* by querying `agent_events` + `attention_items` +
   `audit_events` filtered by `correlation_id`? Reconstruction is
   cheaper if correlation propagation is reliable.
6. **Notification budget.** An inbox of every `reply_via = none`
   message could be noisy. Does the digest ("Since you were last
   here") absorb the low-priority tail, with only escalations pushing?
7. **Terminal-reason taxonomy (Q-T).** Should termipod replace the
   thin `done/blocked/cancelled` task statuses with an *enumerated*
   terminal-reason set (à la §8), each with defined cleanup and a
   defined principal-inbox consequence? What is the right set —
   e.g. `completed / blocked / failed / killed / timed_out /
   superseded`? The v1.0.628 fix (preserve `blocked` on manual stop)
   was the first crack in the thin model.

---

## 10. Recommendation

1. Treat the return path as a **first-class primitive**, the
   symmetric dual of ADR-032's forward envelope.
2. **Layer A first** — a hub-side `session_read_state` table, an
   unread badge on the active-session card and session list, a writer
   that promotes `reply_via = none` envelopes into Message inbox rows,
   and the three-tab reframe (Requests = act / Messages = read /
   Agents = fleet health). No protocol change; a visible UX win.
3. **Layer B second** — end-to-end `correlation_id` propagation,
   per-hop deadlines, stall escalation up to the principal, a
   generalized per-agent liveness classifier, and the directive-trace
   view. This is the "principal should know instead of debugging"
   guarantee.
4. **The loop-closure invariant** is the north star: a principal
   directive is not closed until a terminal event has reached the
   principal's inbox. Both layers serve it — A makes arrival visible,
   B makes non-arrival visible.

Companion plan and ADR(s) to follow once the open questions in §9 are
resolved.

---

## Appendix — Borrowable concepts, ranked by value

The full ledger behind §8. Source: 《御舆 — 解码 Agent Harness》
chapters 2 ("对话循环 — Agent 的心跳") and 10 ("协调器模式 —
多智能体编排") — `github.com/lintsinghua/claude-code-book`. Ranked
by value to termipod; "ch" = source chapter.

| # | Concept (ch) | Maps to | Borrow as |
|---|---|---|---|
| 1 | **Tracked transitions** — every loop `continue` records a `transition` reason; the loop carries its own audit trail (ch2) | §6.5 directive-trace view | Stamp *why* each hop advanced on every event; the trace is then reconstructable from events alone. |
| 2 | **Ten enumerated terminal reasons**, each with defined cleanup (ch2) | §3 principle 1, §9 Q-T | Replace thin `done/blocked/cancelled` with an enumerated terminal-reason set; v1.0.628 was patching its absence. |
| 3 | **`ProgressMessage`** — real-time, UI-only progress event, never sent to the model (ch2) | §5.4 progress-tick | The missing Layer A primitive: a lightweight "still working" signal between transcript rows and attention items. |
| 4 | **`SystemMessage` is UI-only**, distinct from model-input events (ch2) | the v1.0.626 incident | Structurally separate "wake the engine" from "tell the UI" — v1.0.626 conflated them. |
| 5 | **Fail-fast vs feedback-to-recover** — some errors fail the turn, others convert to recoverable feedback (ch2) | §6.6 | Every boundary classifies its failures into one of the two; an unclassified failure is the silent-sink risk. |
| 6 | **XML `<task-notification>` with metrics** — worker→coordinator completion carries `status` + tokens + tool-call count + duration (ch10) | §8, v1.0.626 wake | Validates the v1.0.626 shape; enrich the wake payload with metrics so a steward/principal can assess without opening the session. |
| 7 | **`completed / failed / killed`** — three statuses, `killed` distinct from `failed` (ch10) | §9 Q-T, v1.0.628 | Operator-kill is a first-class peer of task-failure in the terminal taxonomy. |
| 8 | **"Understanding cannot be delegated"** — the coordinator must personally synthesize worker output; "based on your findings" phrasing is prohibited (ch10) | §6.7 synthesis-is-not-relay | Steward prompts must *require* synthesis; a relay-only steward is a degradation node — the prompt-layer half of no-silent-sink. |
| 9 | **Stop-resume / worker resurrection** — a message to a stopped worker auto-respawns it with that message as the new prompt (ch10) | inbox model (§5.2) | "Message a dead agent → it wakes" is a cleaner affordance than explicit respawn. |
| 10 | **Scratchpad** — shared permission-free space driving Research→Synthesis→Implementation→Verification (ch10) | `documents` primitive | Partial overlap; the four-phase workflow framing is borrowable for project templates. |

Validated-without-action (book confirms existing termipod decisions):
worker toolset isolation = [ADR-016](../decisions/016-subagent-scope-manifest.md);
"no worker-checks-worker" = ADR-016's worker→non-parent A2A block;
coordinator-mediated comms = [ADR-007](../decisions/007-mcp-vs-a2a-protocol-roles.md).

Does **not** port (assumes a single process): `async function*`
backpressure, `.return()` cancellation, the local-filesystem
scratchpad path. Termipod rebuilds these as distributed protocol.

---
name: Message routing — the orchestration message envelope
description: The L3 message contract. Every message between agents, and between an agent and the principal, is a structured envelope {from, to, kind, text, cause, thread}. kind is a closed four-value enum (directive/question/report/notification); cause is a lineage reference threading each message to the directive it serves; thread is transport correlation, distinct from cause. The envelope is composed entirely by hub-server; agents author no envelope fields — they declare kind via an explicit tool parameter (a2a_invoke/delegate) or by tool identity, and the hub validates and stamps. A message-admission pipeline at the compose boundary validates and routing-checks every envelope, fail-safe. reply_via is derived and rendered, not stored. No backward-compat shim — envelope-only. Revised 2026-05-19 from the original message-routing-envelope scope per the orchestration-contract discussion; companion runtime ADR-034 covers loop closure.
---

# 032. Message routing — the orchestration message envelope

> **Type:** decision
> **Status:** Proposed (2026-05-18; **revised 2026-05-19**; **D-9 raw-bypass + D-10 configurable templates amendments 2026-05-25**) — D-1..D-10
> widen the original `message-routing-envelope` scope to the full L3
> message contract, per the [orchestration-contract discussion](../discussions/orchestration-contract.md).
> The ADR is still Proposed (never Accepted), so revision in place is
> sound. Companion runtime ADR: [ADR-034](034-orchestration-loop-closure.md)
> (loop closure / liveness). Companion rollout plan:
> [`message-routing-rollout.md`](../plans/message-routing-rollout.md) —
> **implemented 2026-05-19** (all 10 wedges on `main`, build + hub test
> suite green). Flips to `Accepted` after on-device verification
> confirms engines read the rendered envelope reliably.
> **Audience:** contributors
> **Last verified vs code:** v1.0.708-alpha

**TL;DR.** Every message crossing an agent boundary — principal→agent,
agent→agent (A2A), agent→principal, system→agent — is a structured
**envelope** `{from, to, kind, text, cause, thread}`. `kind` is a
closed four-value enum (`directive | question | report |
notification`); `cause` is a **lineage reference** threading each
message to the directive it serves (the contract's spine); `thread` is
transport correlation, a tagged union, *distinct from* `cause`. The
envelope is composed **entirely by hub-server** — agents author no
envelope fields; they declare `kind` via an explicit parameter on
`a2a_invoke`/`delegate` or by tool identity, and the hub validates and
stamps the rest. A **message-admission pipeline** at the compose
boundary validates and routing-checks every envelope, fail-safe.
`reply_via` is **derived and rendered, not stored**. **No
backward-compat shim** — envelope-only from the rollout commit. This
replaces the v1.0.626 (`input.text` unification) and v1.0.630 (`[A2A
from @sender]` prefix) band-aids with the orchestration layer's first
real type system.

---

## 1. Context

Termipod has at least four semantically distinct message sources —
principal direct input, peer A2A, agent→principal returns, system
wakes — but the engine never sees the source as structured data.
Everything collapses into `input.text` events whose `producer` column
never reaches the driver. v1.0.626 unified system wakes onto
`input.text`; v1.0.630 decorated the A2A body with an `[A2A from
@sender]` prefix. Both are workarounds for a missing contract.

The [orchestration-contract discussion](../discussions/orchestration-contract.md)
reframes the gap: message routing is the **orchestration layer's
missing type system**, not an A2A bug fix; the forward and return
paths are **one bidirectional contract**; a **lineage field** is its
spine. This ADR was originally scoped (2026-05-18, D-1..D-5) for
forward-only envelope metadata; it is revised here to the full
contract. The full rationale for every decision below lives in that
discussion — this ADR records the decisions; §-references point into
it.

## 2. Decisions

### D-1. The envelope schema

Every message crossing an agent boundary is the structured envelope:

```
{
  from:   { role, handle, agent_id },   // who sent it
  to:     { role, handle, agent_id },   // who receives it
  kind:   "directive" | "question" | "report" | "notification",
  text:   "<message body>",
  cause:  <ULID> | null,                // lineage — the directive this serves
  thread: { transport: "session" | "a2a" | "attention", id: <ULID> }
}
```

`role` ∈ `principal | peer_steward | peer_worker | system` — source-of-message
semantics, orthogonal to L1's turn-position `user`/`assistant` (the
2026-05-18 rationale against mapping OpenAI roles stands). Both
endpoints are explicit: the return path and the directive trace need
`from` *and* `to`. The `agent_events.producer` column remains as a
denormalized audit projection of `from.role`; the envelope is
canonical. Full schema rationale: discussion §6.1.

### D-2. `kind` — a closed four-value enum

`kind` is the illocutionary force of the message — reasoned by what
the loop machinery must decide (open / close / advance / neutral):

| `kind` | Loop effect | Typical senders |
|---|---|---|
| `directive` | **opens** a loop | principal→steward; steward→worker; A2A delegation; schedule fire |
| `question` | opens a **blocking** sub-loop | worker→steward escalation; steward→principal approval |
| `report` | **advances or closes** a loop | worker→steward result; steward→principal status |
| `notification` | loop-**neutral** | stall escalation; operator action; infra event |

Two openers, one closer, one neutral. The set is closed and ratified
against the project lifecycle (discussion §8); an unknown `kind` falls
back to `notification`, never `directive`. **Closure rule:** a `report`
whose `cause = E` from E's assignee closes E **when it carries a
terminal outcome** (it sets E's `terminal_reason` —
[ADR-034](034-orchestration-loop-closure.md) D-6); a `report` carrying
a non-terminal outcome (`blocked`, interim progress) *advances* E
without closing it. A `question`'s answer is always terminal — a
`report` whose `cause` is the question closes it. Abnormal closes (timeout,
cancel) are Layer-B terminal events surfaced as `notification`s
([ADR-034](034-orchestration-loop-closure.md) D-6). Rationale:
discussion §6.2, §6.5.

### D-3. `cause` — the lineage reference

`cause` is the contract's spine: a bidirectional envelope still leaves
the loop *open* unless each message says which directive it serves.
`cause` is **a reference, not an enum or free-text** — the ULID of the
directive/task entity the message belongs to. Nullable (`null` =
not tied to a tracked directive). The directive tree is reconstructed
by walking `entity.parent` (single parent pointer). `cause` is **not a
new primitive** — it is the Task identity
([ADR-029](029-tasks-as-first-class-primitive.md)) or the
principal-directive `correlation_id` propagated. The hub validates it
(D-7 stage 1). Rationale: discussion §6.3.

### D-4. `thread` — transport correlation, distinct from `cause`

`thread` identifies the transport/conversational channel a message
rides — *which exchange*, at the delivery layer — as a tagged union
`{transport, id}`. It is **orthogonal to `cause`**: a return `report`
and its originating `directive` share a `cause` but ride *different*
threads; two messages in one thread can serve different causes. All
lineage lives in `cause`; `thread` carries none. This replaces the
original D-1's three-nullable-sibling `thread`. Rationale: §6.4.

### D-5. `reply_via` is derived and rendered, not stored

`reply_via` (`chat | a2a | attention_reply | none`) is **not an
envelope field**. The hub derives it deterministically from `from` +
`thread.transport` + `kind` and **renders it as an explicit
instruction** into the agent-facing text. The agent reads it; it never
computes it. For `notification`, the rendering states the kind's
contract ("informational re directive X; no reply routed; act if it
concerns work you own") — never a bald "no reply expected," which
would wrongly imply no *action* is expected. Rationale: §6.5.

### D-6. Authorship — hub-server composes; agents declare `kind`

The **hub composes the entire envelope**; an agent authors **zero**
envelope fields. The agent contributes only `text` and the choice of
affordance. `kind` is determined as follows:

- `a2a_invoke` / `delegate` gain an **explicit `kind` parameter**
  (`directive | question | report` — `notification` is system-only).
  The agent declares intent; the hub validates and stamps. This
  replaces heuristic reply-detection (2026-05-19 decision).
- `request_help` / `request_select` / `request_approval` → `question`
  by tool identity. `tasks_update(status=…)` / `tasks_complete` /
  turn-completion → `report`. Principal `/input` → `directive` (MVP).
  Hub-originated → `directive` (schedule) or `notification`.

Composition is on **hub-server** (`internal/server`, which owns the
`agent_events` table — the three write sites `handlers_agent_input.go`,
`tunnel_a2a.go`, `task_notify.go`). The **host-runner** renders the
envelope per engine; it does not compose — A2A crosses hosts and only
hub-server owns the `cause` registry. One compose helper, one
authoring point. Rationale: §6.6.

### D-7. The message-admission pipeline

Every envelope passes a deterministic **admission pipeline** at the
hub-server compose boundary before the `agent_events` row is written
— the hard-enforcement tier of the contract:

1. **`validateEnvelope`** — schema-valid; `from`/`to`/`kind`
   well-formed; `cause` resolves to a live entity if non-null.
2. **routing-legality** — is `from` permitted to address `to`?
   `deny > allow`; [ADR-016](016-subagent-scope-manifest.md)'s
   worker→non-parent A2A block is already a deny rule of this kind.
3. **context** — kind-specific: an agent-declared `report` must have a
   `cause` referencing an entity assigned to that agent or an open
   `question` addressed to it.

Principle (borrowed from Claude Code's permission pipeline): **error
handling is safe, not correct.** A malformed envelope from a *hub
writer site* is a programming error → fail fast. A bad envelope from
an *agent* is recoverable → reject with a structured hint (reuse
[ADR-031](031-agent-tool-ergonomics.md)'s `hint` envelope) so the
agent can retry. Never crash, never silently drop. Rationale:
discussion §10; [`validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md).

### D-8. No backward-compat shim

There is **no** plain-string compatibility shim and **no** v1.1.0
cutoff (this deletes the original D-4). Termipod is in solo use with
no persisted `agent_events` message data to migrate; the envelope is
the only accepted body shape from the rollout commit. A plain-string
body is malformed input → rejected (D-7). Cut over on a drained hub.

### D-9. Engine-control raw bypass

Amendment added 2026-05-25 (v1.0.707-alpha). Engine-control slash
commands (`/clear`, `/compact`, `/model …`, `/effort xhigh`, …) are
exempt from the envelope wrap — the producing client sets a `raw:
true` flag on its `input.text` post, and the hub stamps
`payload.text` directly without composing a `MessageEnvelope`. The
host-runner's `renderInboundEnvelope` no-envelope fallback returns
the body verbatim to the driver, so the engine sees the slash
command as the first token of the turn (the only position where
claude-code parses it as a control op).

**Scope of the exemption.** The flag is effective only for
`producer == "user"` and `kind == "text"`. Peer-originated messages
(`producer == "a2a"`) remain enveloped — peer-to-peer messages
always carry provenance per D-1 and an A2A "slash command" makes no
sense in the inter-agent contract. The flag is rejected with a
silent no-op on every other shape (set_mode / answer / cancel /
attach), keeping the contract surface narrow.

**Provenance on raw rows.** The persisted `agent_events` row carries
`payload.raw: true` as a marker so consumers (mobile feed, future
audit tooling) can distinguish "raw control op" from "envelope was
malformed and stripped". The marker is forward-compat for further
raw-domain shapes (engine-native non-slash control surfaces a future
engine may ship); today only the slash-command shape gate on the
mobile side flips it.

**Why this is not a regression of D-1.** D-1 says the envelope is
the only accepted shape "for inter-agent and principal-directive
turns". Engine-control ops are neither — they're an op on the engine
itself, observed *through* the chat surface for ergonomic reasons but
not addressed *at* the agent's reasoning. The D-9 exemption draws
the line at "is this content meant to advance the agent's reasoning
loop?". A directive advances it (envelope required); a `/clear`
nukes the loop's state (envelope wrong — the engine treats the
prefixed turn as prose and never recognises the command). The
admission pipeline (D-7) only fires on envelope-bearing rows, so raw
rows skip admission entirely; this is fine because there is no
provenance to validate and no cause to walk.

### D-10. Envelope rendering is hot-loadable template config

Amendment added 2026-05-25 (v1.0.708-alpha). The prose the engine
sees — the bracketed header (`[<kind> from <sender>]`), the role
labels (`"the principal"`, `"@h (a peer steward)"`, …), and the
per-`reply_via` instruction — is now sourced from an
operator-editable YAML at `<HUB_DATA>/team/templates/envelope/active.yaml`
rather than hardcoded Go prose. The hub-side
`hub/internal/envelope/` package owns parsing + validation +
rendering; the resulting string is stamped onto each input.text
event as `payload.rendered_text` before the row is persisted, and
the host-runner forwards it verbatim to the driver.

**Why move rendering from the host-runner to the hub.** The
templates live on the hub's filesystem (the operator's iteration
surface — mobile's `TemplateEditorScreen` or `ssh && $EDITOR`).
Host-runners run on separate hosts (blueprint §3.2) and do not share
the hub's data root. Rendering on the hub side ships the result over
the existing `agent_events` channel; the host-runner stays
transport-only and never needs to know what the templates say.

**Closed enums remain code-defined.** The four kinds
(`directive`/`question`/`report`/`notification` — D-2), the four
roles (`principal`/`peer_steward`/`peer_worker`/`system` — D-2), the
three transports (`session`/`a2a`/`attention` — D-2 + D-5), and the
`deriveReplyVia(kind, transport)` mapping (D-5) are protocol
contracts. The template *variables against* them — a YAML edit that
introduces an unknown role hits the loader's `roles["default"]`
fallback (or, failing that, the bare-handle render), and an unknown
`reply_via` collapses the instruction line to empty. The template
cannot redefine the protocol; only its visible prose.

**Three-tier resolution + graceful degradation.** Mirrors the
pricing-loader pattern (ADR-036 D-10 + the
`feedback_hot_loadable_config_with_embedded_default` discipline):
operator override on disk → embedded `templates/envelope/active.yaml`
on `hub.TemplatesFS` → per-key fallback to the embedded value for
the missing key alone. mtime-driven hot-reload — operator edits land
on the next `Resolve()` call, no hub restart. Validate failure on the
override file warns under `envelope.config_error` and falls through to
embedded; the engine never sees a half-rendered turn.

**Host-runner defence-in-depth.** When the hub's render path is
unavailable (legacy rows pre-D-10, hub-side loader disabled in a
test, future engine-direct admission path), the host-runner's
`renderEnvelopeTurn` retains the original hardcoded prose as a
fallback that's bytewise-identical to the embedded YAML's content.
This is the same redundancy class as the embedded fallback inside
the hub-side loader: the consumer is always-defended.

**What this does not break.** D-1 (envelope schema), D-2 (closed
enums), D-3 (binding by handle), D-5 (reply_via derivation), D-6
(single compose authority), D-7 (admission pipeline), and D-8 (no
shim) all remain in force. D-9's raw-bypass also remains in force:
raw inputs skip envelope composition entirely, so they have no
`rendered_text` either, and the host-runner falls through to the
verbatim body via the no-envelope branch.

**What this does not break.** "No shim" concerns *persisted runtime
data*, not first-party code. `seed-demo` writes only static state
(projects, tasks, documents, runs, `attention_items`) — it never
writes `input.text` / `agent_events` message rows — so it is
unaffected. Test fixtures that build plain-string bodies are *code*,
updated in lockstep with the rollout wedges that change the
compose/unwrap path — coordinated change, not a shim. The shim D-8
declines is specifically a *data-migration* shim, and there is no
persisted message data to migrate. Rationale: discussion §10 item 5.

## 3. Consequences

**Positive.** The engine receives every message as well-typed,
hub-validated structure — provenance, illocutionary force, and lineage
are first-class, not prose the LLM hopes to parse. The loop becomes
*closable* (`cause`) and *measurable* (per-directive realization
efficiency — discussion §7). The admission pipeline makes the contract
*enforced*, not merely *hoped*. Dropping the shim removes a wedge of
migration complexity.

**Negative.** Larger scope than the original ADR — `to`, `kind`,
`cause`, the admission pipeline, the `kind` parameter on two tools.
The rollout plan must be re-wedged. Engines must read the rendered
envelope at least as well as the v1.0.630 prefix — the on-device gate
before `Accepted`.

**Neutral / deferred.** The loop-closure *runtime* (deadlines, stall
escalation, lifecycle hooks, the directive trace) is
[ADR-034](034-orchestration-loop-closure.md) — a separable ADR, but it
ships in the same MVP rollout. The `spine/` axiom for the orchestration
contract is written once this ADR and ADR-034 are Accepted (discussion
appendix).

## 4. Alternatives considered

| Alternative | Why rejected |
|---|---|
| Structured kinds (`input.user.text`, …) | Kind taxonomy bloats every driver switch; new sources need new kinds everywhere. |
| Hub-side body decoration (v1.0.630 prefix) | Doesn't scale; agent knowledge lives in prose it hopes to parse. |
| First-class MCP roles beyond `user`/`assistant` | Deviates from the MCP spec; fights the LLM training distribution. |
| Forward-only envelope (the original 2026-05-18 D-1) | Half-duplex contract; the return path grows its own band-aids. Discussion §5. |
| Agent composes its own envelope | Unverifiable `from`/`cause`; nothing to enforce. Discussion §6.5. |
| Keep a backward-compat shim | Technical debt with no legacy data to justify it. D-8. |

## 5. Implementation

**Implemented 2026-05-19** — see
[`message-routing-rollout.md`](../plans/message-routing-rollout.md),
re-wedged to this revised scope and shipped as 10 wedges on `main`
(commits `eb12a09`…`2a498df`). Phase A (this ADR — A1 envelope schema,
A2 hub-side composition, A3 driver render, A4 the admission pipeline)
and [ADR-034](034-orchestration-loop-closure.md)'s loop-closure runtime
landed together as the one MVP. The hub build and full Go test suite
are green; the mobile changes are CI-verified. The ADR stays `Proposed`
until the on-device verification gate (the rollout plan §4) confirms
engines read the rendered envelope reliably — then it flips to
`Accepted`.

## 6. References

- [`../discussions/orchestration-contract.md`](../discussions/orchestration-contract.md)
  — the full design rationale; §6 is the envelope spec this ADR locks.
- [`../discussions/message-routing-to-agents.md`](../discussions/message-routing-to-agents.md)
  — the original gap analysis + four design alternatives.
- [`../discussions/feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
  — the return-path half; its Layer B is ADR-034.
- [ADR-034](034-orchestration-loop-closure.md) — the loop-closure runtime.
- [ADR-029](029-tasks-as-first-class-primitive.md) — Task identity, which `cause` propagates.
- [ADR-016](016-subagent-scope-manifest.md) — scope manifest; its A2A block is an admission deny rule.
- [ADR-031](031-agent-tool-ergonomics.md) — the `hint` envelope reused by D-7 rejections.

---
name: Message routing to agents
description: Termipod collapses three semantically distinct message sources — principal direct, peer A2A, system wake — into one `input.text` event kind with a producer column visible only to audit. The engine never sees the source. v1.0.626 + v1.0.630 patched two symptoms (wake never woke; A2A had no sender attribution) with band-aids (kind unification, body-prefix decoration); neither addressed the underlying gap. The doc captures the source taxonomy, audits what's routed vs what isn't, surveys four design alternatives (structured kinds, envelope metadata, body decoration, first-class MCP roles), and recommends envelope metadata with a routing matrix.
---

# Message routing to agents

> **Type:** discussion
> **Status:** Open (2026-05-18) — captured after v1.0.626 (wake event delivery) and v1.0.630 (A2A sender attribution) shipped band-aids for symptoms of the same underlying gap. Companion plan at [`message-routing-rollout.md`](../plans/message-routing-rollout.md).
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** Termipod has at least three semantically distinct
message sources — principal direct input, peer A2A, system wakes
— but the engine never knows which one is talking. Everything
collapses into `input.text` events whose `producer` column
(user/a2a/system) lives at the audit layer only. The engine sees
identical text, replies via the same default chat mechanism, and
gets confused when the right reply is a different mechanism (A2A
back to sender, or no reply for system). v1.0.626 routed system
wakes by unifying on `input.text`; v1.0.630 added a body prefix
`[A2A from @sender]` so the LLM could read the attribution. Both
are workarounds for the missing first-class structure. The
principle: **the source of a message determines the reply
mechanism, the authority, the framing, and the threading; the
engine must be told the source as first-class data, not as a
string prefix the LLM hopes to notice.** This doc captures the
source taxonomy, audits the routing matrix today, surveys four
designs, and recommends envelope metadata (option 2 below).

---

## 1. The incidents as case studies

### 1.1 v1.0.626 — task.notify wake never woke

A worker called `tasks.update(status="blocked", ...)`. The
hub's `notifyTaskAssigner` posted a `task.notify` card to the
steward's feed AND an `input.task_completed` system-producer
event meant to wake the steward's engine. The card rendered on
mobile. The wake never reached the engine — every driver's
`Input(kind, payload)` switch had no case for `task_completed`;
all fell through to `default: unsupported input kind`. The
steward saw the card on its feed but its compose box stayed
idle.

Root cause: the W5 wedge (v1.0.611) wired emitter + router
filter but didn't tell the drivers about the new kind. The fix
(v1.0.626) unified the wake on `input.text` with body field —
which works, but loses the lifecycle taxonomy at the dispatch
layer. Now system wakes look identical to user input from the
driver's view.

### 1.2 v1.0.630 — A2A messages had no sender attribution

A project steward sent an A2A message to a worker. The hub
relayed the JSON-RPC `message/send` envelope unchanged. The
host-runner's `a2aHubDispatcher` posted the text to the worker
as `{kind: text, body: <raw>, producer: a2a}`. The
`producer="a2a"` was recorded for audit but never reached the
engine — drivers only see `kind` + `body`. Worker read the body
as if it were a direct user prompt, replied "to the user" via
its chat surface, and the steward got nothing back.

Root cause: producer is invisible to the engine. The fix
(v1.0.630) decorated the message body with a `[A2A from
@sender]` prefix + reply hint at the hub relay layer. The
prefix works because LLMs read prefixes — but it's a band-aid:
the agent's "knowledge" that this is A2A lives only in the body
text the LLM happens to parse.

### 1.3 The pattern

Both bugs are different surfaces of the same gap: **the engine
cannot distinguish message sources at protocol level.** The
hub knows the source (auth + producer column); the engine
doesn't. Every fix has to smuggle the source into the body in
some form. There's no structural slot for "where did this come
from."

---

## 2. The source taxonomy

What kinds of messages reach an agent today? Audit, with each
source's properties:

### 2.1 Principal direct input

The principal types into the chat surface on mobile, or pastes
into the SessionChatScreen compose box. Hub records as
`input.text` producer=user, posts to InputRouter, dispatches as
`kind=text` to driver.

- **Authority:** highest (the principal is the human in the loop).
- **Reply mechanism:** chat surface — engine's default text output
  back through the driver, rendered as a message in the same
  session.
- **Framing in engine prompt:** "the user said …" (default LLM
  framing for role=user).
- **Threading:** within the chat session; no explicit thread id.

### 2.2 Peer A2A

Another agent in the team (typically a parent steward or peer
steward) called `a2a.invoke(handle=..., text=...)`. The hub
relays the JSON-RPC `message/send` envelope to the receiver's
host-runner. Host-runner's `a2aHubDispatcher` extracts the text
and posts as `input.text` producer=a2a.

- **Authority:** medium (peer agent — usually parent steward
  for workers, or peer steward for cross-stewards).
- **Reply mechanism:** `a2a.invoke(handle=<sender>, text=...)` —
  back to sender via the same A2A protocol; NOT the chat
  surface (the principal isn't watching).
- **Framing in engine prompt:** "your parent steward @X asks …" /
  "peer steward @Y reports …".
- **Threading:** A2A task id carries through; multiple turns of
  the same A2A conversation share a task.

### 2.3 System wake

The hub itself emits an event to wake the agent. Sources:

- Task transitions (`task.notify` → `input.text` system, v1.0.626).
- Lifecycle phases (started, paused, terminated — typically
  render-only).
- Schedule fires (cron-triggered steward turns — not currently
  wired through this path).
- Permission-prompt resolutions (`input.approval` user — but the
  resolution itself is system-triggered in some flows).

For task-outcome wakes:

- **Authority:** low (informational; the engine decides what to do).
- **Reply mechanism:** none required — the engine reasons and
  takes its own action (call `documents.read`, decide next step).
- **Framing:** "the system reports: task X transitioned to
  done|blocked|cancelled with summary …".
- **Threading:** no explicit thread; correlation via task_id in
  payload.

### 2.4 Attention replies

The principal resolved an attention item (approval, select,
help_request, etc) on mobile. Hub posts `input.attention_reply`
producer=user.

- **Authority:** highest (principal).
- **Reply mechanism:** depends on the resolved attention kind
  (the agent unblocks its parked tool call OR starts a fresh
  turn with the principal's text reply).
- **Framing:** "the principal answered your request: …".
- **Threading:** via `request_id` in payload (links to the
  parked attention item).

### 2.5 Cancel / interrupt

The principal taps cancel on a running turn. Hub posts
`input.cancel` producer=user.

- **Authority:** highest (principal).
- **Reply mechanism:** the cancel itself is the action; engine
  may emit a "turn cancelled" event.
- **Framing:** not a message in the LLM sense; a protocol-level
  interrupt.
- **Threading:** the current turn.

### 2.6 What's NOT routed but maybe should be

- **Schedule fires.** Cron-triggered steward turns (e.g. nightly
  briefing) currently flow through the briefing template's
  spawn mechanism, not as input.text wakes. Could be either —
  the question is whether the steward should know "this was a
  schedule, not a user request."
- **Policy / template changes.** A steward's toolkit changed
  (new template overlaid by the principal); the steward could
  benefit from knowing. Currently silent.
- **Sibling worker outcomes.** A worker spawned by a peer
  steward completes; if the work was cross-cutting, the
  parent steward might want to know. Currently silent.

### 2.7 What IS routed but maybe shouldn't be

- **Every lifecycle phase.** When a steward spawns a worker,
  `lifecycle.started` fires. The steward already knows it
  spawned. Routing to engine may add noise.
- **Self-emitted events.** An agent's own `journal_append` calls
  trigger events that the agent then sees. Filtering by
  producer (self) avoids the echo.

---

## 3. The routing matrix today

| Source | Event kind | Producer | Driver receives | Engine knows source? |
|---|---|---|---|---|
| Principal direct | `input.text` | user | `kind=text, body` | No |
| A2A from peer | `input.text` | a2a | `kind=text, body` + v1.0.630 body prefix | Via body prefix only |
| Task outcome wake | `input.text` | system | `kind=text, body` (v1.0.626) | No (looks like user) |
| Attention reply | `input.attention_reply` | user | `kind=attention_reply, payload` | Via kind |
| Cancel | `input.cancel` | user | `kind=cancel` | Via kind |
| Approval reply (legacy) | `input.approval` | user | `kind=approval, payload` | Via kind |

Three distinct sources collapsed onto `input.text` + body. The
engine can only distinguish via the body's text content (the
v1.0.630 prefix for A2A; the v1.0.626 "Task 'X' done. Decide
next step." prefix for system wakes).

The other kinds (`input.attention_reply`, `input.cancel`,
`input.approval`) get first-class structural treatment — the
driver sees the kind, the engine knows it's not generic text.

The asymmetry: source-with-its-own-kind gets respected; sources
collapsed into `input.text` don't. The collapse is historical,
not principled.

---

## 4. First principles

What does the engine need to know about a message, structurally?

1. **Source identity.** Who sent this? (Principal, agent handle,
   system component name.)
2. **Source role.** What's their authority? (principal > peer
   steward > peer worker > system.)
3. **Reply mechanism.** How does the engine respond? (chat
   surface, `a2a.invoke(...)`, `attention.reply(...)`, none.)
4. **Threading context.** Is this a continuation? Of what?
   (chat session id, A2A task id, attention request id.)
5. **Framing hint.** How should the agent narrate this internally?
   ("the user said…", "your parent steward asks…", "the system
   reports…").

These are 5 fields. They could fit in:

- 5 separate kinds (one per source type) + a payload field for each. Cost: kind taxonomy bloats.
- 1 kind (`input.message`) + a structured payload with all 5 fields. Cost: every consumer learns to read the structure.
- 1 kind per reply mechanism (`input.user_text`, `input.a2a_text`, `input.system_notice`). Cost: collapses framing into kind name; loses room for system to fan out.
- Current state (`input.text` + body decoration). Cost: framing is text the LLM hopes to parse.

The 5-field set is the irreducible model. The question is how to encode it on the wire and how to surface it to the LLM.

---

## 5. Four design alternatives

### Option 1 — Structured kinds

Use a kind per source type: `input.user.text`, `input.a2a.text`,
`input.system.task_outcome`, `input.system.lifecycle`, etc.

- **Pro:** drivers see source-in-kind; routing is at the dispatch
  layer. No body parsing required. Each kind can have its own
  payload schema. Engine prompts teach per-kind reply semantics.
- **Con:** every driver's switch grows. Existing kind taxonomy
  expands from ~6 to ~12. Adding a new source = a new kind +
  driver case for every driver (4 drivers today).
- **Industry parallel:** OpenAI `role: user|assistant|system|tool`
  is the analog. Roles are first-class enum values, not strings
  in the body.
- **Termipod fit:** matches the existing approach for
  `input.attention_reply` and `input.cancel`. Just extends the
  pattern.

### Option 2 — Envelope metadata

Keep `input.text` but require the body to be a structured
envelope:

```json
{
  "from": {"role": "peer_steward", "handle": "@research-steward"},
  "text": "please review the memo",
  "reply_via": "a2a",
  "thread": {"a2a_task_id": "t1"}
}
```

The LLM reads the structured fields. The engine prompt teaches:
"input.text payloads have a `from` field; if `from.role` is
`peer_steward`, reply via `a2a.invoke(handle=from.handle, ...)`."

- **Pro:** kind taxonomy stays small. One driver dispatch path.
  Easy to extend (add new fields without touching drivers).
- **Con:** every consumer of `input.text` body learns to parse
  the structure. The driver no longer just hands the body to
  the engine as a text turn — it interprets first. LLMs can
  read JSON well but it's a step removed from plain text.
- **Industry parallel:** email headers (From, To, Reply-To,
  In-Reply-To) — structured metadata on top of an opaque body.
- **Termipod fit:** requires hub-side decoration to write the
  envelope; drivers pass through (or unwrap for engine display).

### Option 3 — Hub-side body decoration (current state)

Hub prepends source info to text body. v1.0.630's `[A2A from
@sender]` is the canonical example. v1.0.626's "Task 'X'
done. Decide next step." for system wakes too.

- **Pro:** zero protocol change. Works today.
- **Con:** doesn't scale — every new source needs a new
  prefix convention. LLMs *usually* read prefixes but not
  always. Loses structure (engine can't easily extract sender
  handle to call back to). Auditing is "read the body."
- **Industry parallel:** Slack bot mentions (`<@U123> said:
  ...`) — prefix-based attribution.
- **Termipod fit:** what we have now. Acknowledged as
  band-aid in both v1.0.626 and v1.0.630 commits.

### Option 4 — First-class MCP roles

Extend MCP's `role` enum past `user/assistant` to include
`peer/system/principal`. Push the distinction down to the MCP
spec level.

- **Pro:** standardised across MCP servers. Drivers/engines
  that respect MCP roles automatically distinguish.
- **Con:** deviates from MCP spec. LLMs may not respect novel
  roles (trained on user/assistant primarily). Requires
  per-engine adapter work for those that don't.
- **Industry parallel:** none direct — would be novel.
- **Termipod fit:** poor — fights the MCP spec, and the engine
  side is the part we control least.

---

## 6. The recommended option — envelope metadata (option 2)

The principle: **structured metadata at the protocol level,
LLM-friendly text presentation at the engine surface.**

The wire envelope:

```json
{
  "from": {
    "role": "principal" | "peer_steward" | "peer_worker" | "system",
    "handle": "@handle" | null,
    "agent_id": "01K..." | null
  },
  "text": "<the message body>",
  "reply_via": "chat" | "a2a" | "attention_reply" | "none",
  "thread": {
    "a2a_task_id": "..." | null,
    "attention_request_id": "..." | null,
    "session_id": "..."
  }
}
```

The driver-side default rendering for the engine turn:

```
[from @research-steward (peer_steward)] please review the memo

Reply via: a2a.invoke(handle="research-steward", text=...)
```

(I.e. the LLM sees the same human-readable text v1.0.630
generates today, but the structured `from`/`reply_via` is
canonical and the driver does the rendering, not the hub.)

Engine prompts gain a "How messages are addressed" section:

> Messages you receive carry a `from` field naming the sender.
> Check it before replying:
> - `from.role == "principal"`: reply via chat surface (default).
> - `from.role == "peer_steward"` or `"peer_worker"`: reply via
>   `a2a.invoke(handle=from.handle, text=...)`.
> - `from.role == "system"`: usually no reply needed; act on the
>   information (e.g. task completed → read the artifact, decide).

Wins:
- Kind taxonomy stays at ~6.
- Engine sees source as first-class — `from.role` field, not a
  text prefix to parse.
- New sources (schedule fires, policy changes) get the same
  envelope shape — extend `from.role`, no driver changes.
- Audit row still has producer column; the envelope's `from`
  field is just the canonical projection.

Losses:
- Migration cost: every site that posts `input.text` (hub-side,
  test fixtures) now writes the envelope, not bare text.
  Drivers / hostrunner unwrap to render. Backward-compat shim:
  if body is plain string, treat as `{from: {role: "user"}, text:
  body}`.
- One extra layer of structure for the LLM to parse — but
  structured JSON inside the body is something LLMs handle
  better than implicit prefixes.

---

## 7. Routing matrix — the recommended target state

| Source | Kind | from.role | reply_via | Engine handles |
|---|---|---|---|---|
| Principal direct | `input.text` | principal | chat | default text reply |
| Peer A2A (parent / sibling steward) | `input.text` | peer_steward / peer_worker | a2a | `a2a.invoke(handle=from.handle, ...)` |
| System task outcome | `input.text` | system | none | act on info; no reply needed |
| System schedule fire | `input.text` | system | none | do the scheduled work |
| Attention reply | `input.attention_reply` | principal | none | unpark the request |
| Cancel | `input.cancel` | principal | none | abort current turn |

Notes:
- Attention reply + cancel keep their own kinds (they have
  fundamentally different payloads + handling, not just
  different sources).
- The "system schedule fire" entry is new wiring; not in
  termipod today.
- "from.handle" is empty for system; "from.role=principal" sets
  the implicit handle to the team's principal.

---

## 8. Audit findings — should-route gaps

Things that should be routed as messages but aren't today:

1. **Schedule fires** (cron-triggered briefing/research). Today
   flows through spawn; if the steward were already running, the
   schedule could wake it via `input.text` with `from.role=system,
   reply_via=none`.
2. **Policy/template changes.** Principal overlays a new
   template; the steward should know its toolkit changed.
   Currently silent.
3. **Sibling worker outcomes** for stewards that share
   project scope. Currently each steward only sees its own
   children's outcomes via task.notify (v1.0.626).

Things that ARE routed but might not need to be:

4. **Self-emitted events.** Agents see echoes of their own
   `journal_append` calls. Filter by `from.role != self`.
5. **Verbose lifecycle phases.** `lifecycle.started` for every
   spawn — the parent already knows it spawned. Filter to only
   route lifecycle events that the agent didn't initiate.

These are not blockers for the envelope wedge — but worth
tracking once the structural design is in place.

---

## 9. What this does NOT solve

- **Backpressure / rate limiting.** A flood of system events
  could overwhelm an engine. The envelope is metadata about
  source; not a rate limiter.
- **Message ordering across sources.** If A2A and user input
  arrive in close succession, the engine sees them in DB
  insertion order. The envelope doesn't add ordering guarantees.
- **Engine-side prompt drift.** The per-persona "how to read
  from" section needs maintenance as new roles are added.
  Same enforcement problem as MCP description hygiene.

---

## 10. Open questions

1. **Where does the envelope unwrap happen — hub-side (write
   envelope to DB) or driver-side (render to text turn)?** Two
   options. Hub-side: hub composes the structured body before
   inserting; drivers see canonical structure. Driver-side:
   hub inserts raw text + producer; drivers consult producer
   to compose the envelope at dispatch. Hub-side is simpler
   (one writer); driver-side keeps the DB schema unchanged.
2. **What's the migration path for existing `input.text` rows?**
   Backward-compat shim makes them look like
   `{from: {role: "user"}, text: <body>}`. New rows write the
   full envelope. Eventually old rows decay out of the
   poll window.
3. **Should `reply_via` be the agent's responsibility to honor,
   or should the system reject mis-replies?** Loose: agent
   reads `reply_via`, ignores at own peril. Strict: if
   `from.role=peer_steward` and the agent emits chat text, the
   chat output goes nowhere visible. Loose first.
4. **Does this interact with the request/propose verb design
   from ADR-030?** Yes — `from.role=system` is the same channel
   ADR-030's "system commits" would flow through. The envelope
   could carry an ADR-030 `commit_id` field.

---

## 11. References

- [validate-at-every-boundary.md](validate-at-every-boundary.md)
  — companion principle; the "test-the-end-of-the-pipe corollary"
  was distilled from the v1.0.626 wake-delivery failure.
- [worker-permission-routing-to-steward.md](worker-permission-routing-to-steward.md)
  — discusses worker → steward attention routing; some overlap
  with the system-source case here.
- [decisions/030-governed-actions-and-propose-verb.md](../decisions/030-governed-actions-and-propose-verb.md)
  — proposes a new verb that would emit system events the
  envelope schema needs to carry.
- [agent-tool-ergonomics.md](agent-tool-ergonomics.md) — sibling
  discussion shipped same day; both are about "the engine
  knowing what's going on without LLM-vibes."

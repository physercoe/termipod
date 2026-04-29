# Attention interaction model — long-poll vs turn-based

> **Type:** discussion
> **Status:** Resolved (2026-04-29) → `../decisions/011-turn-based-attention-delivery.md`
> **Audience:** contributors
> **Last verified vs code:** v1.0.338

**TL;DR.** When an agent calls `request_approval`, `request_select`, or
`request_help`, today's model holds the MCP call open via a 10-min
long-poll until the principal answers. That works for sub-minute
replies and breaks for everything longer — exactly the use case the
mobile-first hand-off product targets. This doc audits why the
polling model is wrong for human-AI interaction, why three of four
attention kinds can be made turn-based but one cannot, and what
"turn-based" looks like end-to-end. Resolution: ADR-011 accepted
turn-based delivery for the three async kinds and documented
`permission_prompt`'s sync constraint as a vendor-contract
limitation.

---

## 1. Why this is a problem

Today's flow for `request_help` (and the structurally identical
`request_select`):

```text
agent calls request_help via MCP
hub INSERTs attention_items row, status='open'
hub long-polls (waitForAttentionResolution, 10 min cap)
    ┌─ user answers → attention resolved → MCP returns body
    └─ 10 min elapse → attention auto-resolved (no decision)
                     → MCP returns {timeout: true}
```

Three forces make this fragile:

1. **The cadence is wrong.** Humans answer in seconds, minutes,
   hours, or days. The product is mobile-first hand-off — the
   principal can be asleep, in a meeting, on vacation. A 10-min
   hard cap silently drops 12-min replies.
2. **The connection is the persistence.** The state lives in an
   open HTTP request between agent and hub. HTTP/2 idle-stream
   timeouts, reverse-proxy idle policies (nginx defaults to 60s
   without tuning), TCP keepalive on flaky mobile networks — any
   of these can kill the connection mid-wait, and the agent gets
   either an error or a synthetic timeout.
3. **The cost is paid by everyone.** The engine process holds memory
   while the model sits paused. The hub holds a goroutine + a DB
   poll. The agent's outer turn budget (Claude's max-turns) ticks
   down for the timeout-handling response when no answer comes.

The 10-minute timeout isn't a feature; it's a workaround for a
broken model.

## 2. The four interaction kinds today

| Kind | Model | Returns |
|---|---|---|
| `permission_prompt` | Synchronous block | `{behavior: "allow"|"deny", ...}` after user decides |
| `approval_request` | Fire-and-forget | `{id}` immediately; agent must poll `get_attention` to check |
| `select` | Long-poll (10 min) | `{option_id, decision}` or `{timeout: true}` |
| `help_request` | Long-poll (10 min) | `{body, decision}` or `{timeout: true}` |

Three different models for what is structurally the same
interaction ("agent asks human, human eventually answers"). That
inconsistency is itself a smell — uniformity-by-default copied the
permission_prompt pattern when designing `request_select` (and we
copied it again for `request_help`).

## 3. Two underlying delivery models

### 3.1 Synchronous block

State lives in the open connection. Bounded by whichever timeout
fires first — our 10-min cap, the proxy's HTTP idle limit, or a
network blip. Agent process holds a tool-call frame open the whole
time. Used by `permission_prompt`, `select`, `help_request` today.

### 3.2 Turn-based

State lives in the conversation history (`agent_events` table).
Each side completes its turn and yields. No connection pinning;
survives restarts; no time bound. Used informally by
`approval_request` (returns immediately, agent moves on) and by
every chat system in the world (this conversation included).

## 4. Why turn-based is right for human-agent interaction

Three structural properties make it correct:

1. **Asymmetric cadence.** Humans answer in seconds → days; agents
   respond in seconds. Pinning resources to the slower side's
   decision loop is wrong by orders of magnitude.

2. **Persistence belongs in the conversation, not the connection.**
   If a power blip drops the network mid-question, the conversation
   shouldn't be lost — and it isn't, because both sides have a
   message history. Long-polling violates this: the connection IS
   the state.

3. **The "answer" is fundamentally another turn, not a function-call
   return.** A human's reply has full conversational standing — they
   might ask a follow-up, change plans, attach context. Forcing it
   through a tool-result shape squeezes a turn into a function
   return type.

These properties don't depend on which engine, which transport, or
which timeout. They follow from the asymmetry between the two
participants.

## 5. The transport investigation (MCP)

Initial framing: "the long-poll dies on a network blip; that's why
it's broken." But this isn't the engine's fault — the engine waits
forever. The kernel doesn't time out a `read()` on stdin. The
constraint is whichever HTTP-style hop sits idle.

MCP supports three transports:

- **stdio** — JSON-RPC over a subprocess's pipes. Local, no idle
  timeout.
- **HTTP+SSE** — server runs as an HTTP service. Subject to all
  the network-layer timeouts above.
- **Streamable HTTP** — newer evolution of HTTP+SSE.

Our spawn-time `.mcp.json` materializer points the agent at an HTTP
URL (the egress proxy at `127.0.0.1:NNNN`, which forwards to the
hub). So the engine talks HTTP — but the project also ships a stdio
MCP bridge (`hub-mcp-bridge` / `host-runner mcp-bridge`,
implementation in `internal/mcpbridge/` and `internal/hostrunner/mcp_gateway.go`).
It's there but not on the hot path.

This unlocks a **bridge-mediated stdio** pattern: agent ↔ local
bridge over stdio (no timeout — kernel pipes), bridge ↔ hub over
reconnecting SSE with `since` cursor (we already have SSE; we
already have cursors). Engine sits indefinitely; bridge survives
network blips.

Bridge-mediated stdio is a workaround for a forced-sync constraint.
Turn-based is the better model when sync isn't forced. We need only
one workaround, for the one kind that genuinely can't be turn-based.

## 6. Why permission_prompt stays sync

The engine's permission hook protocol is defined as a synchronous
request/response. Claude's `canUseTool` callback returns
`{behavior: "allow" | "deny", ...}`. There is no "deferred — I'll
tell you later" branch in the schema. If we return early without an
answer, Claude treats it as undefined behavior — most likely
`deny-and-retry` or give-up.

Could we redesign permission_prompt to be turn-based?

A turn-based permission would have the engine emit a "permission
deferred" turn, end its current run, wait for the user's approval,
re-spawn or wake with the approval as the next user turn, then
re-attempt the tool call. This is what hand-off agents like Devin
do. It requires the engine to define a "deferred" branch in its
hook protocol — none of Claude/Codex/Gemini do. Without that,
faking it (return `{deny, "ask again later"}` and hope the model
interprets it correctly) is prompt-engineering on top of undefined
behavior.

So `permission_prompt` is sync **because the engines we support
define the contract that way**, not because of design preference.
With the engines we have, we're stuck with sync at that boundary.

Mitigation for `permission_prompt`'s sync constraint: bridge-mediated
stdio (§5). Engine ↔ bridge stdio has no timeout; bridge ↔ hub SSE
reconnects automatically. The engine can wait hours/days for
permission as long as the bridge process stays alive. **Deferred
post-MVP** — switching the materialized `.mcp.json` from `url:` to
`command/args:` is a self-contained wedge that can ship anytime.

## 7. What turn-based looks like end-to-end

```text
agent: request_help({question: "..."})
hub:   → returns {id, status: "awaiting_response"}
agent: ends turn
       (engine sits idle, no tokens consumed)

principal opens app at any later time (minutes / hours / days)
       /decide → { decision: "approve", body: "..." }
hub:   resolves attention + posts input.attention_reply event
runner: forwards as a user-text turn to the engine over stdin
agent: wakes, sees "[reply to help_request <id>] <body>",
       processes as next user turn, continues
```

Key moves:

- **Tool returns immediately** with `{id, status: "awaiting_response"}`.
- **Tool description** instructs the agent to end its turn after
  calling. (Prompt engineering is load-bearing here — see ADR-011
  D5.)
- **Server-side fan-out** at `/decide` time: hub looks up the
  originating agent via `attention.session_id →
  sessions.current_agent_id` and posts an `input.attention_reply`
  agent_event with the decision payload.
- **Host-runner translation** maps `attention_reply` to a user-text
  turn (NOT a `tool_result` — the original tool call has already
  returned). Per-kind formatting in `formatAttentionReplyText`:
  approval → "Approved" / "Rejected", select → "Selected: X",
  help → body verbatim. Short correlation prefix
  `[reply to <kind> <id-prefix>]` for disambiguating multiple
  in-flight requests.

This unifies three of four kinds under one model, removes ~150
LoC of long-poll machinery, eliminates the timeout failure mode,
and matches how chat systems actually work.

## 8. Options considered

**A. Turn-based for the three async kinds** (chosen). Drop long-poll
on `request_select` / `request_help`; deliver the resolution as a
new user turn via `input.attention_reply`. `request_approval` was
already 90% there — the missing piece was delivering the resolution
back rather than expecting the agent to poll.

**B. Bridge-mediated stdio for all four kinds.** Switch agents to
stdio MCP via the local bridge. Bridge holds the engine waiting;
bridge ↔ hub uses reconnecting SSE so transient hub disconnects
don't break the wait. **Rejected** for the three async kinds because
it preserves the long-poll model that was wrong on its merits — it
just makes it network-resilient. Three engines staying alive for
hours, processes pinned, memory held, all to support a wait the
conversation history could handle for free. Uniformity at the cost
of the better design. **Accepted** as the post-MVP mitigation for
`permission_prompt` only.

**C. Lengthen the long-poll timeout to e.g. 1 hour.** Smallest
change. Doesn't solve the structural problem (still loses 1h+
replies), still pinned to connection lifetime. Chosen as a stopgap
for `permission_prompt` until the bridge ships.

**D. Polling-from-the-agent-side via `get_attention`.** Agent calls
`request_X`, returns immediately, agent re-checks status every N
turns. **Rejected**: every poll is a model turn (tokens spent),
poll cadence is arbitrary, the agent has to be re-prompted to do
this. Turn-based delivery is strictly cheaper.

## 9. Resolution

Adopted Option A. See `../decisions/011-turn-based-attention-delivery.md`
for the formal decision (D1–D6) and consequences. The change shipped
as v1.0.338. Permission_prompt's bridge-mediated stdio remains a
post-MVP wedge; not blocked on anything else, can ship when the
operational pain of permission-prompt connection drops becomes real.

## 10. References

- ADR-011 (decision): `../decisions/011-turn-based-attention-delivery.md`
- Reference (mechanics): `../reference/attention-kinds.md` §5
- Earlier decision touched: ADR-005 (`../decisions/005-owner-authority-model.md`)
  — the ratification list now reads `request_approval` / `request_select`
  / `request_help` as turn-based, `permission_prompt` as the principled
  exception.
- Implementation:
  - `hub/internal/server/mcp_more.go` (long-poll removal, tool descriptions)
  - `hub/internal/server/handlers_attention.go` (`dispatchAttentionReply`)
  - `hub/internal/server/handlers_agent_input.go` (`attention_reply` kind)
  - `hub/internal/hostrunner/driver_stdio.go` (`formatAttentionReplyText`)
- Tests:
  - `TestRequestHelp_ReturnsAwaitingResponseImmediately` (synchronous return)
  - `TestDecide_HelpRequestFansOutAttentionReply` (end-to-end fan-out)
  - `TestStdioDriver_InputFrames/attention_reply_*` (per-kind formatting)

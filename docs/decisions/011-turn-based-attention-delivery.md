# 011. Turn-based delivery for async attention kinds

> **Type:** decision
> **Status:** Accepted (2026-04-29)
> **Audience:** contributors
> **Last verified vs code:** v1.0.347

**TL;DR.** `request_approval`, `request_select`, and `request_help`
return immediately with `{id, status: "awaiting_response"}`. The
agent ends its turn. The principal's reply is delivered as a fresh
user turn through a new `input.attention_reply` agent_event when
`/decide` resolves the attention. The 10-minute long-poll model
this replaced was structurally wrong for human-AI interaction
where reply latency runs from seconds to days. `permission_prompt`
remains synchronous — Claude's `canUseTool` hook protocol has no
"deferred" branch, so sync is a vendor-contract limitation, not a
design choice.

## Context

`discussions/attention-interaction-model.md` audited the current
model and laid out the choice. Three of four attention kinds today
hold an HTTP request open while waiting for the principal to answer
(`permission_prompt` and the recently-added `request_select` and
`request_help`); the fourth (`request_approval`) is fire-and-forget
but has no resolution-delivery path back to the agent. The
inconsistency is uniformity-by-default — `request_select` and
`request_help` copied the long-poll pattern from
`permission_prompt` rather than picking the right model on its
merits.

The polling model fails three structural tests:

1. **Cadence asymmetry.** Humans answer in seconds, minutes, hours,
   or days. Agents respond in seconds. A 10-min hard cap on the
   wait is workable only when reply latency is much shorter than
   the cap — exactly the opposite of the mobile-first hand-off
   product target.

2. **Connection-as-state.** The polling model puts persistence in
   an open HTTP request. HTTP/2 idle-stream timeouts, reverse-proxy
   policies, and TCP keepalive on flaky mobile networks all defeat
   it. The 10-minute timeout is a workaround, not a feature.

3. **Wrong shape for the answer.** A reply has full conversational
   standing — the human can ask follow-ups, attach context, change
   plans. Forcing it through a tool-result return type squeezes a
   turn into a function call.

The four-kinds catalog (per `discussions/attention-interaction-model.md` §2):

| Kind | Model before this ADR |
|---|---|
| `permission_prompt` | sync block (vendor-contract forced) |
| `approval_request` | fire-and-forget, no resolution path |
| `select` | long-poll (10 min) |
| `help_request` | long-poll (10 min) |

## Decision

Adopt turn-based delivery for `request_approval`, `request_select`,
and `request_help`. Document `permission_prompt`'s sync model as the
principled exception.

**D1. The three async tools return immediately.**

`mcpRequestApproval` already does. `mcpRequestSelect` and
`mcpRequestHelp` drop their `waitForAttentionResolution` calls and
the `requestSelectTimeout` / `requestHelpTimeout` constants. All
three return `{id, kind, status: "awaiting_response", requested_by}`
synchronously after inserting the `attention_items` row.

**D2. `/decide` fans out the resolution as a new user turn.**

`handleDecideAttention` gains a `dispatchAttentionReply` helper.
After resolving the attention, the helper:

1. Looks up the originating agent: `attention.session_id →
   sessions.current_agent_id`.
2. Inserts an `agent_events` row with `kind="input.attention_reply"`,
   `producer="user"`, payload carrying
   `{request_id, kind, decision, body, option_id, reason}`.
3. Publishes the event on the agent's bus key so the host-runner's
   `InputRouter` picks it up immediately.

Best-effort: a fan-out hiccup is logged but does not roll back the
`/decide` write. Attentions with no `session_id` (system-originated
rows from before v1.0.336, or `budget` / spawn-approval paths) are
silently skipped — there's no live agent to deliver to.

**D3. Resumed-session targeting is intentional.**

If the session was resumed since the attention was raised,
`current_agent_id` points at the new agent. The reply goes there.
The new agent inherits the conversation history and is the
correct recipient — it sees its predecessor's question in context
and the reply alongside.

**D4. Host-runner translates `attention_reply` to a user-text turn.**

`StdioDriver.Input` gains an `attention_reply` case in
`buildStreamJSONInputFrame`. Unlike `answer` (which emits a
`tool_result` content block keyed by `tool_use_id`), this emits a
plain `text` content block in a `user`-role frame — the original
tool call has already returned, so there's no pending `tool_use_id`
to reply against.

Per-kind text format from `formatAttentionReplyText`:

- `approval_request` → "Approved" / "Approved. Reason: …" / "Rejected" / "Rejected. Reason: …"
- `select` → "Selected: <option>" / "No option chosen. Reason: …"
- `help_request` → "<body>" verbatim, or "Dismissed without reply"

A short correlation prefix `[reply to <kind> <id-prefix>]` (id
truncated to 8 chars) is included so the agent can match replies to
multiple in-flight requests. Full id is in the audit row.

**D5. Tool descriptions tell the agent to end its turn.**

The `request_approval`, `request_select`, `request_help` MCP tool
descriptions all carry an explicit instruction:

> Returns immediately with `{id, status: "awaiting_response"}`.
> END YOUR TURN AFTER CALLING. The principal's reply arrives as
> your next user turn.

Prompt engineering is load-bearing here. An agent that doesn't end
its turn after calling will see `awaiting_response` as the answer
and may hallucinate continuation. The reference doc
(`reference/attention-kinds.md`) carries the long form; the tool
docstring carries the short form.

**D6. `permission_prompt` stays synchronous — vendor-contract limitation.**

Claude's `canUseTool` hook protocol returns `{behavior: "allow" |
"deny", ...}` synchronously. There is no "deferred" branch in the
schema; returning early without an answer is undefined behavior.

> **Update (2026-04-29, ADR-012 D7):** This applies to Claude only.
> Codex's `app-server` JSON-RPC protocol exposes deferrable
> per-tool-call approval requests
> (`item/commandExecution/requestApproval` etc.) over a long-lived
> stdio pipe with no timeout — the same shape this section
> identifies as missing from `canUseTool`. So the bridge-mediated
> stdio mitigation below is now Claude-only; Codex bridges
> `permission_prompt` directly via app-server. Gemini's CLI has
> only `--yolo` / `--approval-mode` and inherits the sync-or-bypass
> trade-off Claude's `canUseTool` has.

This is a constraint we can't fix from the hub. The mitigation
(post-MVP) is **bridge-mediated stdio**: switch the spawn-time
`.mcp.json` materializer from `url:` to `command/args:` pointing
at the local `host-runner mcp-bridge`. Engine ↔ bridge over stdio
has no idle timeout; bridge ↔ hub over reconnecting SSE survives
network blips. Engine can wait indefinitely for permission while
the bridge handles transport-level reconnect. Tracked as a
self-contained wedge; not blocked on anything.

## Consequences

**Becomes possible:**

- 3-day-later replies work. The principal can answer at any time,
  including after the app has been killed and restarted, after a
  hub redeploy, after a host-runner restart. Persistence lives in
  `agent_events` + `attention_items` — both durable.
- `permission_prompt` is now the only place in the system holding
  an HTTP connection open waiting for human action. That's a much
  smaller surface for transport-fragility tuning.
- Engine memory during the wait could be freed (full pause-and-
  resume — engine exits, re-spawns when the answer arrives). Not
  shipped in this ADR; the engine still sits alive between turns
  today, which is fine for the initial wedge but worth revisiting
  for hosts running many idle stewards.

**Becomes harder:**

- Agent prompt engineering is load-bearing. A custom-prompted
  steward that doesn't read the updated tool description and keeps
  generating after `awaiting_response` will misbehave. Mitigation:
  the docstring is explicit, and the reference doc
  (`reference/attention-kinds.md`) carries worked examples.
- Multiple in-flight requests need correlation. The
  `[reply to <kind> <id-prefix>]` prefix in user-turn text is the
  agent's only signal. If the agent ignores the prefix and routes
  the reply to the wrong question, it'll still work-ish — both
  questions stay open and the principal's reply contributes to one
  thread — but the agent's mental model is incoherent. Worth
  watching in real conversations.

**Becomes forbidden:**

- Re-introducing long-poll on the three async kinds. The structural
  argument doesn't change with engine count or scale. If a future
  use case really needs sync semantics for one of these (e.g. a
  permission-like gate), it should join `permission_prompt` under
  D6 rather than re-introduce the long-poll model that was wrong on
  its merits.
- Mixing `attention_reply` and `answer` semantics. They're separate
  agent_input kinds for a reason: `answer` replies to a still-open
  tool call (`tool_result` shape), `attention_reply` is a fresh
  user turn (no `tool_use_id`). Conflating them silently in the
  driver would re-introduce the broken model.

## References

- Discussion: `../discussions/attention-interaction-model.md` (full
  audit + Option A/B/C/D analysis + transport investigation +
  permission_prompt rationale)
- Reference: `../reference/attention-kinds.md` §5 (mechanics +
  per-kind text format)
- ADR touched: `005-owner-authority-model.md` (the ratification list
  now reads `request_approval` / `request_select` / `request_help` as
  turn-based, `permission_prompt` as the sync exception)
- Implementation:
  - `hub/internal/server/mcp_more.go` — drop long-poll from
    `mcpRequestSelect` and `mcpRequestHelp`; updated tool
    descriptions for all three async kinds.
  - `hub/internal/server/handlers_attention.go` —
    `dispatchAttentionReply` helper; called from
    `handleDecideAttention` for the three async kinds.
  - `hub/internal/server/handlers_agent_input.go` — accept new
    `attention_reply` input kind.
  - `hub/internal/hostrunner/driver_stdio.go` —
    `formatAttentionReplyText` + the `attention_reply` case in
    `buildStreamJSONInputFrame`.
- Tests:
  - `TestRequestHelp_ReturnsAwaitingResponseImmediately` (1s upper
    bound — fail-fast on long-poll regression)
  - `TestDecide_HelpRequestFansOutAttentionReply` (end-to-end
    agent → /decide → agent_input)
  - `TestMCP_RequestSelect_TurnBasedRoundTrip` (replaces the prior
    `_StoresOptionsAndLongPolls` test)
  - `TestStdioDriver_InputFrames/attention_reply_*` (per-kind
    formatting: help_request approve, select approve,
    approval_request reject)
- Post-MVP follow-up (not blocked on anything):
  bridge-mediated stdio for `permission_prompt`. Switch
  `writeMCPConfig` from URL to `command/args` pointing at
  `host-runner mcp-bridge`; engine waits indefinitely on stdio,
  bridge handles transport reconnect to hub via SSE.

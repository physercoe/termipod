---
name: Message routing — envelope metadata on input.text
description: Lock in the message-routing design picks. Every input.text body carries a structured envelope `{from, text, reply_via, thread}` with a 4-role taxonomy (principal / peer_steward / peer_worker / system); MVP reply_via enum is `chat | a2a | attention_reply | none`; envelope is composed at the hub-write boundary; backward-compat shim accepting legacy plain-string bodies stays until a named cutoff version then drops; the existing producer column coexists with the envelope (audit-time query convenience). Retires v1.0.626's input.text unification + v1.0.630's body-prefix decoration as band-aids.
---

# 032. Message routing — envelope metadata on `input.text`

> **Type:** decision
> **Status:** Proposed (2026-05-18) — D-1 through D-5 locked in the 2026-05-18 design conversation following the [message-routing-to-agents discussion](../discussions/message-routing-to-agents.md). Companion rollout plan at [`../plans/message-routing-rollout.md`](../plans/message-routing-rollout.md). Flips to `Accepted` after on-device verification confirms LLMs read the envelope at least as reliably as v1.0.630's body prefix.
> **Audience:** contributors
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** Replace the v1.0.626 (`input.text` unification for system wakes) and v1.0.630 (`[A2A from @sender]` body prefix) band-aids with first-class envelope metadata so the engine knows the source of every input as structured data, not as text the LLM hopes to parse. Five locked decisions: (D-1) every `input.text` body carries a structured envelope `{from, text, reply_via, thread}` with a **4-role taxonomy** (`principal | peer_steward | peer_worker | system`), (D-2) **MVP `reply_via` enum** is `chat | a2a | attention_reply | none`, (D-3) the envelope is **composed at the hub-write boundary** (one writer site; drivers unwrap to render); (D-4) the **backward-compat shim** accepting legacy plain-string bodies stays **until a named cutoff version** (v1.1.0) then drops with an explicit deprecation warning during the window; (D-5) the existing `producer` column **coexists** with the envelope (audit-time query convenience; envelope is the canonical projection). MVP retires both band-aids by giving the engine structural source awareness; the agent's reply mechanism becomes a contract, not a prefix-parsing accident.

---

## 1. Context

Termipod has at least three semantically distinct message sources today — principal direct input, peer A2A invocations, and system wakes (task transitions, lifecycle phases) — but the engine never sees the source as structured data. Everything collapses into `input.text` events whose `producer` column (`user | a2a | system`) lives at the audit layer only and never reaches the driver, let alone the LLM.

Two recent band-aids tried to close the resulting gaps from opposite sides:
- **v1.0.626** unified system wakes onto `input.text` after the discovery that the original W5 wedge emitted `input.task_completed` kind which no driver dispatched. The wake-delivery path now works but loses lifecycle taxonomy at the dispatch layer.
- **v1.0.630** decorated the A2A relay body with `[A2A from @sender]` prefix so the LLM could read the attribution. Works because LLMs parse prefixes — but the agent's "knowledge" that this is A2A lives only in the text the LLM happens to notice.

The discussion at [`../discussions/message-routing-to-agents.md`](../discussions/message-routing-to-agents.md) names the underlying gap: **the source of a message determines the reply mechanism, the authority, the framing, and the threading; the engine must be told the source as first-class data.** Four design alternatives were surveyed:
1. Structured kinds (`input.user.text`, `input.a2a.text`, `input.system.task_outcome`) — kind taxonomy bloats.
2. **Envelope metadata** (this ADR) — `input.text` body becomes structured.
3. Hub-side body decoration (current v1.0.630) — works but doesn't scale.
4. First-class MCP roles beyond user/assistant — deviates from MCP spec, fights the LLM training distribution.

Option 2 was picked. This ADR locks in the five blocking decisions.

## 2. Decisions

### D-1. Envelope schema with 4-role taxonomy

Every `input.text` event's body is a structured envelope:

```json
{
  "from": {
    "role": "principal" | "peer_steward" | "peer_worker" | "system",
    "handle": "@research-steward" | null,
    "agent_id": "01K..." | null
  },
  "text": "<the message body>",
  "reply_via": "chat" | "a2a" | "attention_reply" | "none",
  "thread": {
    "session_id": "...",
    "a2a_task_id": "..." | null,
    "attention_request_id": "..." | null
  }
}
```

**Role taxonomy:**
- **`principal`** — the human in the loop typed into a chat surface. `from.handle` is the principal's team handle.
- **`peer_steward`** — another steward (parent or peer) sent via A2A. `from.handle` is the steward's handle; `from.agent_id` is its ULID.
- **`peer_worker`** — another worker (typically sibling) sent via A2A. Same shape as peer_steward.
- **`system`** — the hub itself emitted (task transition, schedule fire, etc). `from.handle` is empty or a system-component name (`"system:task_notify"`, `"system:schedule:nightly-briefing"`).

Rationale for 4-role vs collapsing peer_steward + peer_worker into one `peer`: prompts gain cleaner conditionals (`if from.role == "peer_steward": treat as authority` vs `peer_worker: treat as collaboration request`). The discrimination is at the engine layer — at the wire layer, peer_steward and peer_worker share the A2A delivery mechanism, so collapsing would gain nothing. Cost: one more enum value. Worth it for prompt clarity.

Rationale for excluding OpenAI's `user | assistant | system | tool` mapping: OpenAI roles are turn-position semantics (who speaks next in a chat); our roles are source-of-message semantics (who sent this). They're orthogonal axes; mapping one onto the other introduces ambiguity.

### D-2. `reply_via` enum: `chat | a2a | attention_reply | none` (MVP)

The reply mechanism the agent should use to respond:

- **`chat`** — emit text output normally; appears in the principal's chat session.
- **`a2a`** — call `a2a.invoke(handle=from.handle, text=...)` back to sender.
- **`attention_reply`** — the message is itself an attention resolution; the agent unparks the request rather than replying conversationally.
- **`none`** — no reply expected; act on the information (read the named artifact, decide, take action) and continue.

**MVP set.** Future extensions worth considering but explicitly NOT in scope for this ADR:
- `journal` (private note-to-self response).
- `channel_post` (broadcast to a project channel).
- `request_help` (escalate via attention rather than reply).

A future ADR may extend the enum; new values land with a clear migration path for engines that don't recognize them (fall back to ignoring, NOT to misinterpreting as `chat`).

### D-3. Hub-side composition; driver-side rendering

**The envelope is composed once, at the hub write boundary.** Three sites are responsible:
- `task_notify.go` (system task-outcome wakes): composes with `from.role="system"`, `reply_via="none"`.
- `tunnel_a2a.go handleRelay` (peer A2A relays): composes with `from.role="peer_steward"|"peer_worker"`, `reply_via="a2a"`. Replaces the v1.0.630 body-prefix decoration.
- `handlers_agents.go handlePostAgentInput` (principal direct input): composes with `from.role="principal"`, `reply_via="chat"`.

Each site uses a single helper (`composeInputMessage(...)` in `input_envelope.go`) so the envelope schema has one authoring point.

**Drivers unwrap at dispatch.** Each driver's `case "text":` branch runs the body through an unwrap helper that:
1. Detects envelope shape (presence of `from` field).
2. If envelope, renders an LLM-friendly text turn:
   ```
   [from @research-steward (peer_steward)] please review the memo

   Reply via: a2a.invoke(handle="research-steward", text=...)
   ```
3. If legacy plain-string body (backward-compat per D-4), wraps as `{from: {role: "principal"}, text: <body>, reply_via: "chat"}` and renders the same way.

Rationale for hub-side composition over driver-side: hub knows the source via auth + the calling-context (which handler is invoking the helper). Driver-side composition would require passing producer column + sender attribution + thread context through the dispatch path. Hub-side is the smaller surface change. Cost: every existing `input.text` writer site must migrate; mitigated by D-4's shim.

Rationale for driver-side rendering (vs handing structured JSON to the LLM directly): LLMs read structured JSON well but the v1.0.630 evidence is that the rendered text-with-prefix already works. Pre-rendering at the driver gives the LLM what it's empirically known to handle; the structural envelope is the canonical record, the rendering is the engine-facing view. Drivers can update their rendering style per-engine without changing the wire format.

### D-4. Backward-compat shim until v1.1.0 cutoff

During the rollout window, the driver-side unwrap helper accepts **both** the new envelope shape AND legacy plain-string bodies. Legacy bodies are treated as principal-direct (`from.role="principal"`, `reply_via="chat"`).

**Cutoff: v1.1.0.** At that release, the shim drops. Plain-string bodies after v1.1.0 are treated as malformed input and rejected.

**Deprecation signal during the window:** when the legacy path is hit, the hostrunner emits a warning log naming the agent_id and event_id. Operators can grep for these to find any caller that hasn't migrated.

Rationale over "forever" (always accept plain string): the shim is technical debt the codebase pays for every dispatch; named-cutoff makes the deadline concrete. Rationale over "one release cycle": v1.0.x is a fast-moving release line (we've shipped v1.0.620 → v1.0.630 in two days); a one-cycle window is too tight for any non-hub-side caller to migrate.

### D-5. `producer` column coexists with envelope

The existing `producer` column on `agent_events` (`user | a2a | system`) **stays**. The envelope's `from.role` field is the canonical source-of-truth; `producer` is the audit-time projection.

**Coexistence rules:**
- Hub writers always populate BOTH `producer` and the envelope's `from.role` from the same source context. Audit queries reading `producer` keep working.
- `from.role` carries finer information than `producer` (`peer_steward` vs `peer_worker` both project to `producer="a2a"`).
- A CI lint asserts the projection: for every test row, `from.role`'s projection (per a static table) matches `producer`.

Rationale over "replace producer entirely":
- Audit consumers (existing dashboards, the W2.11 `a2a.sent` event, future reporting tools) query by producer without parsing JSON. Dropping producer breaks every one of them.
- The projection is lossless in the direction that matters (producer → set of envelope roles): a `producer="a2a"` row is either `peer_steward` or `peer_worker`; consumers that need the discrimination read the envelope, consumers that just need "is this peer-originated" read producer.
- Coexistence cost is one helper that fills both fields from one source; negligible.

## 3. Consequences

### Positive
- The engine knows the source of every input as structured data; reply mechanism is a contract, not a prefix-parsing accident.
- v1.0.626 (input.text unification) and v1.0.630 (body-prefix decoration) become canonical, not band-aids — the rendering layer still produces the same LLM-facing text, but the underlying structure is principled.
- New sources (schedule fires, sibling outcomes per the rollout plan's Phase 3) get the envelope shape automatically — extend `from.role` without driver changes.
- Per-persona prompts gain one rule ("messages carry a `from` field; check it before replying") that scales across roles.

### Negative
- Migration cost: 3 hub writer sites update; backward-compat shim runs until v1.1.0; ~700 LOC total per the plan.
- One layer of structure between the wire format and the LLM-facing text — drivers do the unwrap. Slightly more complex than passing bytes through, but the unwrap is mechanical.
- Audit queries reading `producer` get coarser data than the envelope; consumers that want fine-grained source need to parse JSON or join.

### Neutral / deferred
- **Reject mis-replies enforcement** — if `from.role=peer_steward` and the agent emits chat text instead of `a2a.invoke`, the chat output goes nowhere visible. Start loose (no enforcement); tighten in a future ADR if mis-replies prove common in device testing.
- **Schedule fires + sibling outcomes routing** — Phase 3 of the rollout plan. Uses the envelope shape; doesn't require changes here.
- **Per-engine adapter rendering** — drivers may need engine-specific text templates if LLMs vary in how they read the rendered prefix. Out of scope; one rendering for MVP.

## 4. Alternatives considered

| Alternative | Why rejected |
|---|---|
| Structured kinds (`input.user.text`, `input.a2a.text`, `input.system.task_outcome`) | Kind taxonomy bloats; every driver's switch grows; new sources require new kinds across all drivers. |
| Hub-side body decoration (current v1.0.630) | Doesn't scale; every source needs a prefix convention; agent knowledge lives in body text it hopes to parse. |
| First-class MCP roles (extend `user | assistant`) | Deviates from MCP spec; LLMs may not respect novel roles (trained primarily on user/assistant). |
| Collapse `peer_steward` + `peer_worker` into single `peer` | Prompts lose the authority discrimination; cost of an extra enum value < cost of conditional logic in prompts. |
| Replace `producer` column entirely | Breaks every audit consumer; coexistence cost is negligible. |
| Forever-shim (always accept plain string) | Technical debt without a deadline; codebase pays the cost every dispatch indefinitely. |
| One-cycle deprecation window | Too tight for the v1.0.x release cadence (10+ releases in 2 days). |

Full alternatives analysis in [discussion §5, §6](../discussions/message-routing-to-agents.md).

## 5. Implementation

See [`../plans/message-routing-rollout.md`](../plans/message-routing-rollout.md):
- **Phase 1 (MVP, 3 wedges):** envelope schema + writer-side compose at the 3 hub sites + driver-side unwrap with backward-compat shim.
- **Phase 2 (MVP, 1 wedge):** per-persona "How messages are addressed" section in 10 main prompts.
- **Phase 3 (post-MVP, 2 wedges):** route schedule fires + sibling outcomes; filter self-echo + verbose lifecycle.

Status flips to `Accepted` when Phase 1 + Phase 2 ship AND on-device verification confirms LLMs read the envelope at least as reliably as v1.0.630's body prefix did. The latter is the empirical gate — the discussion §10 Q1 question. If on-device shows the rendering needs tweaking, the ADR's rendering choice in D-3 can be revised before Accepted without changing the wire envelope (D-1, D-2, D-5).

## 6. References

- [`../discussions/message-routing-to-agents.md`](../discussions/message-routing-to-agents.md) — full framing + alternatives analysis; §6 is the recommended option this ADR locks in.
- [`../plans/message-routing-rollout.md`](../plans/message-routing-rollout.md) — execution.
- [`../discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md) — the "test-the-end-of-the-pipe corollary" was distilled from the v1.0.626 wake-delivery failure that motivated this ADR.
- [`../discussions/auto-notification-coverage.md`](../discussions/auto-notification-coverage.md) — adjacent: which events auto-notify; the routing matrix here partially closes its gaps.
- [`030-governed-actions-and-propose-verb.md`](030-governed-actions-and-propose-verb.md) — the envelope may carry an ADR-030 `commit_id` field in a future extension; not in scope for this ADR.
- [`031-agent-tool-ergonomics.md`](031-agent-tool-ergonomics.md) — sibling ADR landed same day; both shape the agent-side contract surface.

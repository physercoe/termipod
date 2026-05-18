---
name: Message routing rollout
description: Phased rollout of the envelope-metadata design (option 2 from message-routing-to-agents.md) for routing source-distinguished messages to agents. Phase 1 promotes producer column into an envelope `from` field on input.text events; Phase 2 teaches per-persona prompts to read the envelope; Phase 3 closes the should-route audit gaps (schedule fires, sibling outcomes) and the self-echo filter. Six wedges across hub + hostrunner + prompts; ~700 LOC + ~250 lines prose. MVP is phases 1 + 2.
---

# Message routing rollout

> **Type:** plan
> **Status:** Proposed (2026-05-18) — three phases, six wedges, no work started. Companion discussion at [`../discussions/message-routing-to-agents.md`](../discussions/message-routing-to-agents.md); the four design alternatives + recommendation (option 2, envelope metadata) are there.
> **Audience:** contributors · principal · QA
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** Replace the v1.0.626 + v1.0.630 body-prefix
band-aids with first-class envelope metadata so the engine
knows the source of every input. Three phases:

- **Phase 1 — Envelope (MVP, 3 wedges):** every `input.text`
  body gains a structured envelope (`from.role`, `from.handle`,
  `reply_via`, `thread`); hub composes it on write; drivers
  unwrap to LLM-friendly text turn. Backward-compat shim for
  legacy plain-string bodies. ~350 LOC + tests.
- **Phase 2 — Prompts (MVP, 1 wedge):** every persona prompt
  gains a "How messages are addressed" section teaching the
  per-role reply mechanism. ~150 lines prose.
- **Phase 3 — Route the unrouted + filter the over-routed
  (post-MVP, 2 wedges):** wire schedule fires + sibling
  outcomes as envelope-bearing events; filter self-emitted
  echoes from the per-agent feed. ~200 LOC.

The MVP phases (1 + 2) are the minimum to retire the
v1.0.626/630 band-aids and give the engine structural source
awareness. Phase 3 closes the audit gaps surfaced in the
discussion §8.

---

## 0. Phase / wedge summary

| Phase | # | Wedge | Approx | Depends on |
|---|---|---|---|---|
| 1 | W1 | Define envelope schema + writer-side compose | ~120 LOC | — |
| 1 | W2 | Hub callers post envelope (notifyTaskAssigner, a2a relay, /input handler) | ~120 LOC | W1 |
| 1 | W3 | Driver-side unwrap to LLM text turn | ~110 LOC | W1, W2 |
| 2 | W4 | Per-persona "How messages are addressed" section | ~150 prose | W1-W3 |
| 3 | W5 | Route schedule fires + sibling outcomes through envelope | ~120 LOC | W1-W4 |
| 3 | W6 | Filter self-emitted echoes + verbose lifecycle | ~80 LOC | W1 |

Implementation order is **W1 → W2 (depends W1) → W3 (depends
W2) → W4 (depends W3) → W5 (depends W1-W4) → W6 (depends W1)**.

---

## 1. Wedges in detail

### Phase 1 — Envelope

#### W1 — Envelope schema + writer-side compose

Define the envelope Go struct + JSON shape. Add a hub-side
helper that composes the envelope from (producer, sender info,
target thread).

**Schema (Go + JSON):**

```go
type InputMessageEnvelope struct {
    From     InputMessageFrom    `json:"from"`
    Text     string              `json:"text"`
    ReplyVia string              `json:"reply_via"` // "chat" | "a2a" | "attention_reply" | "none"
    Thread   InputMessageThread  `json:"thread,omitempty"`
}

type InputMessageFrom struct {
    Role    string `json:"role"` // "principal" | "peer_steward" | "peer_worker" | "system"
    Handle  string `json:"handle,omitempty"`   // "@research-steward" for peer; empty for system
    AgentID string `json:"agent_id,omitempty"` // for audit cross-ref
}

type InputMessageThread struct {
    SessionID        string `json:"session_id,omitempty"`
    A2ATaskID        string `json:"a2a_task_id,omitempty"`
    AttentionReqID   string `json:"attention_request_id,omitempty"`
}
```

**Helper (hub/internal/server/input_envelope.go new file):**

```go
// composeInputMessage assembles the envelope from a source context.
// Falls back to from.role="system" with empty handle when the source
// can't be resolved (audit-only path).
func composeInputMessage(role, handle, agentID, text, replyVia string, thread InputMessageThread) string { ... }
```

**Implementation site:** new file `hub/internal/server/input_envelope.go`.

**Acceptance:**
- Envelope serializes / deserializes round-trip cleanly.
- Helper produces canonical JSON the consumer can parse.

**Tests:** `TestComposeInputMessage_*`, structured assertions
on each role + reply_via combo.

#### W2 — Hub callers post envelope

Three call sites today post `input.text` bodies. Each gets
rewritten to compose the envelope via the W1 helper:

1. **`notifyTaskAssigner`** (`hub/internal/server/task_notify.go`):
   currently posts `{body: <text>, ...}` for system task-outcome
   wakes. Replace body string with envelope where
   `from.role="system"`, `reply_via="none"`, `thread.session_id=<steward's>`.
2. **A2A relay decoration** (`hub/internal/server/tunnel_a2a.go`):
   v1.0.630 decorates body with `[A2A from @sender]` prefix.
   Replace with envelope where `from.role="peer_steward"` (or
   `peer_worker`), `from.handle=<sender>`, `reply_via="a2a"`,
   `thread.a2a_task_id=<task>`. Drop the prefix decoration —
   driver-side W3 renders.
3. **Principal direct input** (`hub/internal/server/handlers_agents.go`
   `handlePostAgentInput`): currently posts `{body: <text>, ...}`
   for `kind=text` producer=user. Replace with envelope where
   `from.role="principal"`, `from.handle=<principal handle>`,
   `reply_via="chat"`.

**Backward-compat shim:** during the rollout window, the
driver-side W3 accepts BOTH the new envelope shape AND legacy
plain-string bodies. Legacy bodies are treated as
`{from: {role: "principal"}, text: <body>, reply_via: "chat"}`.
After one release cycle, the shim can drop.

**Implementation site:** the three named files. Each call
site swaps its body-string composition for a
`composeInputMessage(...)` call.

**Acceptance:**
- Every `input.text` row written post-W2 has the envelope shape.
- Existing tests for these three call sites pass after
  updating their assertions (envelope shape instead of plain
  body).

**Tests:** updated assertions in `task_notify_input_test.go`,
`tunnel_a2a_test.go`, `handlers_agents_test.go`.

#### W3 — Driver-side unwrap to LLM text turn

Drivers receive `input.text` payloads. Add an unwrapping helper
that:
1. Detects envelope shape (presence of `from` field).
2. If envelope, renders a structured text turn for the engine:

```
[from @research-steward (peer_steward)] please review the memo

Reply via: a2a.invoke(handle="research-steward", text=...)
```

3. If legacy plain string, treat as
   `{from: {role: "principal"}, text: <body>, reply_via: "chat"}`
   (backward-compat).
4. Pass the rendered text to the existing driver text path
   (unchanged).

The structural rendering preserves what v1.0.630's body
prefix gave us, but now driven by canonical envelope fields,
not free-form text. The engine sees identical-looking text;
the underlying source-of-truth is structured.

**Implementation site:** new helper
`hub/internal/hostrunner/input_envelope.go`. Called from each
driver's text branch:
- `hub/internal/drivers/local_log_tail/claude_code/sendkeys.go`
- `hub/internal/hostrunner/driver_appserver.go`
- `hub/internal/hostrunner/driver_exec_resume.go`
- `hub/internal/hostrunner/driver_pane.go`

Each driver's `case "text":` branch first runs the body
through the unwrap helper to get the rendered text turn.

**Acceptance:**
- Envelope body → renders to LLM text with `[from @X (role)]`
  prefix + `Reply via: …` hint.
- Legacy plain string → renders to the bare text (backward
  compat).
- Driver's existing behavior unchanged for the rendered text.

**Tests:** `TestInputEnvelopeUnwrap_*` per driver case.

### Phase 2 — Prompts

#### W4 — Per-persona "How messages are addressed" section

Every persona prompt gains an "Inbox" section explaining the
envelope. Section template:

```markdown
## How messages are addressed

Messages you receive carry a `from` field naming the sender's
role and a `reply_via` field naming the right reply mechanism.
Check both before responding:

| from.role | What it means | Reply via |
|---|---|---|
| `principal` | The human (`@{{principal.handle}}`) typed into chat | Default chat output (your text turn appears in the principal's session) |
| `peer_steward` | A peer or parent steward (e.g. `@{{parent.handle}}`) sent A2A | `a2a.invoke(handle=from.handle, text=...)` — NOT chat (the principal isn't watching this conversation) |
| `peer_worker` | A peer worker spawned by another steward | `a2a.invoke(handle=from.handle, text=...)` |
| `system` | The hub auto-pushed a wake (task transition, schedule fire) | No reply needed — act on the information (e.g. read the named artifact, decide next step) |

The text part already includes a parenthetical hint when the
reply isn't chat; the structured fields are the canonical
source.
```

**Persona prompts in scope:** all 10 main prompts (4 stewards
+ 6 workers, same set as agent-tool-ergonomics W4).

**Implementation site:** each `hub/templates/prompts/*.md`. The
section goes near the top (after the persona intro) so the
agent reads the inbox protocol before encountering any
specific tool guidance.

**Acceptance:**
- Every main persona prompt has the section.
- Audit lint (`auditBundledTemplateVarRefs`) passes — the
  section uses only `{{parent.handle}}` and `{{principal.handle}}`
  which are bound.

**Tests:** existing audit lint must continue to pass.

### Phase 3 — Polish (post-MVP)

#### W5 — Route schedule fires + sibling outcomes through envelope

Two new sources gain first-class routing:

1. **Schedule fires.** When a cron-triggered template
   activates (e.g. nightly briefing), wake the existing
   steward (if running) via `input.text` with
   `from.role="system"`, `from.handle="schedule:<name>"`,
   `reply_via="none"`, `text="Schedule '<name>' fired. Run
   the scheduled work."`. Falls back to spawn-fresh if no
   steward is running.

2. **Sibling worker outcomes.** When a worker spawned by peer
   steward A completes, peer steward B (same project, working
   on a related task) may want to know. Today this is silent.
   The envelope makes it cheap to route: post `input.text`
   with `from.role="system"`, `from.handle="sibling:<worker
   handle>"`, `reply_via="none"`, `text="Sibling worker '<X>'
   completed task '<Y>': <summary>"`.

   This is opt-in per steward via a new
   `notify_on_sibling_outcomes` flag on the steward template
   to avoid noise.

**Implementation site:** new `hub/internal/server/schedule_wake.go`
+ extension to `hub/internal/server/task_notify.go` for
sibling fan-out.

**Acceptance:**
- Cron fire on a project with an active steward wakes the
  steward with the named envelope.
- Sibling outcome events fan out to opted-in stewards in the
  same project.

**Tests:** `TestScheduleWake_*`, `TestSiblingOutcomeFanout_*`.

#### W6 — Filter self-emitted echoes + verbose lifecycle

Two filter rules:

1. **Self-echo:** if `from.agent_id == receiving_agent_id`,
   skip dispatch. (Agents shouldn't see their own
   journal_append echoes, channel posts, etc.)

2. **Verbose lifecycle:** `lifecycle.*` events with
   `from.role="system"` and `phase in {started, ready}` are
   render-only — don't dispatch to the engine. The parent
   already knows it spawned. Only `phase in {failed, crashed,
   terminated_unexpectedly}` route.

**Implementation site:** filter in
`hub/internal/hostrunner/input_router.go` (the existing
allowlist gets two new conditions).

**Acceptance:**
- A steward calling `journal_append` doesn't see the resulting
  event in its own input stream.
- A steward spawning 5 workers doesn't see 5 wake events for
  the started phase.

**Tests:** `TestInputRouter_SkipsSelfEcho`,
`TestInputRouter_FiltersVerboseLifecycle`.

---

## 2. Out of scope for this plan

- **Replacing kind taxonomy** — `input.text` stays;
  `input.attention_reply` / `input.cancel` keep their distinct
  kinds (they have different payloads, not just different
  sources). Discussion §5 option 1 (per-source kind explosion)
  is explicitly rejected.
- **First-class MCP roles beyond user/assistant** — would
  deviate from MCP spec; not in scope.
- **Engine-side prompt patching** for engines that don't
  follow envelope reply hints — handled by per-engine adapters
  if/when needed.
- **Threading model overhaul** — the envelope's `thread` field
  is enough to correlate; full threading semantics
  (turn-of-turn, branching) are a separate design.
- **Backpressure / rate limiting** for high-volume sources —
  the envelope is metadata, not a rate limiter; that's a
  separate concern.

---

## 3. Risks

- **LLM doesn't read envelope reliably.** Mitigation: the
  driver-side W3 renders a clear text representation. The
  envelope is structural; the rendering is what the LLM sees.
  If the LLM ignores it, the text representation is still
  unambiguous.
- **Backward-compat shim becomes permanent.** Mitigation:
  enforce a deprecation window (one release cycle) and add a
  warning log when the legacy path is hit. Plan a follow-up
  wedge to drop the shim.
- **Sibling outcomes flood stewards.** Mitigation: opt-in flag
  on steward template (W5); default off.
- **Schedule wake races with new spawn.** Mitigation: W5
  checks if a steward is running before deciding wake-vs-spawn;
  no concurrent both.

---

## 4. Acceptance for the bundle

The plan ships as one or two releases (MVP = phases 1 + 2;
phase 3 in a follow-up). Acceptance:

- A worker receiving an A2A message reads `from.handle` and
  replies via `a2a.invoke(handle=from.handle, ...)` without
  needing to parse a body prefix.
- A steward woken by `task.notify` sees `from.role="system"`
  and reasons "I should act, not reply."
- The principal's direct input shows up with
  `from.role="principal"` — the engine knows it's talking to
  the human.
- Legacy plain-string bodies still work (backward-compat shim
  in W3).
- v1.0.630's body-prefix decoration can be removed from
  `tunnel_a2a.go decorateA2ABodyWithSender` (replaced by
  envelope + driver-side rendering).

Verification on-device with the same smoke task from
2026-05-18: steward sends A2A to worker; worker replies via
`a2a.invoke` back to steward; steward wakes on task.notify and
reads the doc.

---

## 5. References

- [`../discussions/message-routing-to-agents.md`](../discussions/message-routing-to-agents.md)
  — the framing this plan implements. Discussion §6 is the
  recommended option (envelope metadata).
- [`../discussions/validate-at-every-boundary.md`](../discussions/validate-at-every-boundary.md)
  — the "test-the-end-of-the-pipe corollary" was distilled
  from the v1.0.626 wake-delivery failure that motivated
  this plan.
- [`../discussions/auto-notification-coverage.md`](../discussions/auto-notification-coverage.md)
  — adjacent discussion on which events should auto-notify;
  W5/W6 here partially address it.
- [`../decisions/030-governed-actions-and-propose-verb.md`](../decisions/030-governed-actions-and-propose-verb.md)
  — the envelope could carry an ADR-030 `commit_id` field as
  a future extension.

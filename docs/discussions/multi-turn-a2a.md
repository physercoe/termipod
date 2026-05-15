# Multi-turn A2A — analysis + decision deferred

> **Type:** discussion
> **Status:** Active (2026-05-15) — open design discussion; no ADR yet. Decoupled from ADR-027 (LocalLogTailDriver); does not block its implementation.
> **Audience:** principal · contributors
> **Last verified vs code:** `hub/internal/hostrunner/a2a*` as of commit `da74157`

**TL;DR.** termipod's current A2A implementation is **single-task-per-agent
by design** — `a2a_dispatcher.go:38` calls it "the single-turn shape of
today's drivers." Cross-mode dispatch (e.g. a steward in M2 sending to a
worker in M4 or the new ADR-027 LocalLogTailDriver) **works for one
round**: the message lands as text in the target's input channel,
the target's output is harvested into the task history, and the task
terminates. **Long-running tasks (hours) are supported**; the lifecycle
is async and the task store never auto-evicts. **Multi-turn back-and-forth
within one task is not supported** — a second `message/send` to the
same agent cancels the prior task. This doc captures the gap, what
fixing it would cost, why it's not blocking, and the path to an eventual
ADR.

---

## 1. What's there today

### 1.1 Wire protocol (`hub/internal/hostrunner/a2a/`)

Standard **A2A v0.3** — `message/send`, `tasks/get`, `tasks/cancel`. Task
lifecycle: `submitted → working → completed | failed | canceled`. The
`input-required` state **is defined in the enum** (`tasks.go`
`TaskStateInputRequired`) but **no driver currently emits it**.

### 1.2 Per-agent terminus

Each host-runner publishes:

- `/a2a/<agent-id>/.well-known/agent.json` — agent-card discovery
- JSON-RPC endpoint per live agent for `message/send` / `tasks/get` / `tasks/cancel`

### 1.3 Dispatch path (peer → target) — mode-agnostic

```
peer ──A2A HTTP──► host-runner ──hub POST /input──► InputRouter ──► driver.Input(text)
                                                                          │
                                                                          ├─ M1: session/prompt to ACP daemon
                                                                          ├─ M2: write to subprocess stdin
                                                                          └─ M4 (new): tmux send-keys to TUI pane
```

`a2aHubDispatcher.Dispatch` extracts text parts from the peer's
message and posts to hub's `/input` with `producer="a2a"`. Routes the
same way phone/web input does — single audit trail. **The driving
mode of the target doesn't matter to the dispatcher; the driver's
`Input(text)` method handles mode-specific delivery.**

### 1.4 Response harvesting (target → peer)

`a2aHubDispatcher.OnAgentEvent` taps the driver's outbound stream:

- `producer="agent"` events → append text to the task's history; flip state `submitted → working`.
- `producer="system"` + `kind="lifecycle"` + `phase="stopped"` → flip state to `completed`; release the per-agent slot.

### 1.5 Concurrency — single task per agent

From `a2a_dispatcher.go:38`:

> Only one live task per agent is tracked. A second `message/send`
> arriving before the first finishes marks the prior one canceled
> (terminal) and supersedes the correlation, mirroring the
> single-turn shape of today's drivers.

The `open map[string]*a2aOpenTask` is keyed on `agentID`, not
`taskID`. A follow-up `message/send` to the same task ID gets the
same supersede treatment.

### 1.6 Relay (ADR-003)

GPU hosts are NAT'd → host-runners long-poll hub for queued A2A
requests via `tunnel.go::RunTunnel`. Each tunnel request is
independent and short; long-running tasks don't hold tunnel
connections. Hub mediates the relay.

---

## 2. Task duration — actually fine for hours

Question raised: can A2A handle a worker task that takes hours?

**Yes, with two notes.** The lifecycle is fully asynchronous:

| Phase | Time budget | Source |
|---|---|---|
| `message/send` HTTP call → dispatcher | ≤30 s | `tasks.go:288` context timeout on the dispatcher goroutine |
| Dispatch (text extraction + POST to hub `/input`) | ms | bounded by the 30 s above; in practice ~50 ms |
| `working` state — agent processing the input | **unbounded** | no timeout; task lives in the store until terminal flip or host-runner restart |
| Peer polling `tasks/get` | each call ms | peer can poll for hours; in-memory state served |
| Response harvest | each AgentEvent → store mutation | bounded only by driver event rate |
| Task store eviction | **none** | `tasks.go:94` — "No eviction today — tasks live until host-runner restart" |

The 30-second context **only bounds the dispatcher's POST-to-hub
call**, not the agent's work duration. Once the message is in the
target's input queue, the dispatcher goroutine exits; the task lives
on in the store while the agent works.

**Two caveats for very-long tasks:**

1. **Host-runner restart loses task correlation.** The TaskStore is
   in-memory. If host-runner restarts (deploy, crash, OOM), all
   open A2A tasks evaporate. The agent process keeps running (M4's
   tmux pane survives; M1/M2 subprocesses may or may not depending
   on parent-pipe semantics), but peer `tasks/get` calls return
   "not found." There's no protocol-level reconnection mechanism.

   Mitigation possibilities: persist the task store in SQLite (small
   wedge), or treat host-runner as a fragile resource and have peers
   re-issue tasks on session-resume signals. **Neither is currently
   implemented.**

2. **No streaming update channel.** A2A v0.3 supports streaming
   updates via `tasks/sendSubscribe`, but termipod implements only
   the request-response `message/send` + polling `tasks/get`. For
   an 8-hour task, the peer polls every N seconds and sees history
   accumulate over time. No push notifications.

   Mitigation: the polling pattern is sufficient if the peer's own
   loop is also long-running (e.g. a steward that delegates to a
   worker can poll once a minute without missing anything). Phase 2
   could add SSE streaming if push semantics become load-bearing.

**Practical recommendation for the current fan-out/gather pattern:**
fine as-is. Workers run for hours; stewards poll periodically;
results land via the harvest path. The bottleneck isn't duration,
it's the multi-turn shape (next section).

---

## 3. The multi-turn gap

What doesn't work today, with a concrete example:

```
steward → worker: "Compile and report errors."
worker  ↩ "1234 errors found. Top one is X. Should I include the
            full traceback in my report?"
steward → worker: "yes please."         ← cancels the prior task
worker  ↩ (interrupted)
```

The second `message/send` reuses the agent's slot in
`open[agentID]` and supersedes. The worker's first reply is in the
prior task's history, but the prior task is now `canceled`; the
steward's follow-up created a new task with no shared context.

Workarounds available **today** (caller-managed threading):

1. **Pack history into one message.** Steward concatenates
   `"Earlier you reported 1234 errors. I now want you to..."` into
   a single fresh `message/send`. Each turn is a new task with full
   replay. Wasteful on context tokens but functional.
2. **Don't try to converse over A2A.** Use A2A for kick-off-and-wait
   only; conversation lives in the steward↔mobile-user channel or
   the steward's own working memory.

Both work. Neither matches what A2A v0.3 calls multi-turn.

---

## 4. What real multi-turn would require

To honor the protocol's intent (`input-required` → peer responds
with `message/send` referencing the same `taskId` → conversation
continues):

### 4.1 Honor `input-required` state in drivers

The target driver needs to emit a lifecycle event when waiting for
peer input. Today no driver does. Per-driver work:

| Mode | What "awaiting peer input" looks like | New event to emit |
|---|---|---|
| **M1 (ACP)** | claude/gemini emits `session/request_user_input` or stalls awaiting `session/prompt`; could also be triggered by `AskUserQuestion` tool from the agent side | `lifecycle{phase:"awaiting_input"}` after driver detects the gap |
| **M2 (stream-json)** | stdin idle for >N seconds while stdout quiet (heuristic) | same |
| **M4 (LocalLogTailDriver, new)** | `Notification{notification_type:"idle_prompt"}` hook fires — already empirically observed (probe 2026-05-15) | direct mapping; **M4 actually has the cleanest signal** |
| **M4 (legacy raw-PTY, deprecated)** | capture-pane heuristic | rejected (see ADR-027) |

The new M4 driver from ADR-027 is **better-positioned for multi-turn
than M1/M2 today** — its Notification hook already structurally
signals idle/awaiting state. If multi-turn becomes load-bearing,
this is the engine where to validate the design first.

### 4.2 Per-task input routing in the dispatcher

Replace `open[agentID]` with `open[taskID]`:

```go
// today
type a2aHubDispatcher struct {
    open map[string]*a2aOpenTask  // agentID → task
}
// future
type a2aHubDispatcher struct {
    open map[string]*a2aOpenTask  // taskID → task
    // agentID lookup for "list my open tasks" is a secondary index
}
```

Follow-up `message/send` with an existing `taskID` appends rather
than supersedes. New `message/send` without a `taskID` creates a
fresh task as today.

### 4.3 Conversation isolation in the driver

An agent has one input stream today. Multi-turn A2A from one peer
would interleave with phone/web input on the same stream. Two
options:

- **(a) `thread_id` on input events** — driver propagates back on
  output so dispatcher can re-correlate. Clean but invasive (every
  driver + InputRouter changes).
- **(b) Accept interleaving, trust the agent to disambiguate from
  message content** — lazy but workable. Peers prefix their messages
  with their `agent_id`; the target agent disambiguates by reading
  context.

Option (b) is the cheaper start; (a) becomes necessary if
agents-talking-to-multiple-peers-concurrently becomes a real pattern.

### 4.4 "Done" semantics

What ends a multi-turn task? Options:

- Worker emits a `done` signal in its message ("That's all from me.")
- Peer issues `tasks/cancel`
- Idle timeout (e.g. 1 hour with no new messages → auto-complete)
- All of the above; whichever fires first

ADR-worthy decision.

---

## 5. Cost estimate

Wedge breakdown for a minimal multi-turn implementation:

| Wedge | LOC est. | Notes |
|---|---|---|
| 1. Dispatcher: `open[agentID]` → `open[taskID]` + follow-up routing | ~80 | + tests |
| 2. Driver event: `lifecycle{phase:"awaiting_input"}` for **M4 only** (cleanest signal — see §4.1) | ~30 | piggybacks on Notification hook |
| 3. State flip: dispatcher sets `input-required` on receiving the awaiting-input lifecycle | ~20 | wires the M4 path |
| 4. Done semantics: idle timeout + explicit done | ~40 | configurable knob |
| 5. Optional: SQLite-persisted task store for restart survival | ~150 | could defer; not strictly required for multi-turn correctness |
| 6. Optional: `tasks/sendSubscribe` SSE streaming | ~200 | nice-to-have; polling works |

**~170 LOC for the core multi-turn loop on M4 alone.** M1/M2 add
~30-60 LOC each for their awaiting-input heuristics. Total ~300 LOC
for full coverage across modes.

Not a small wedge but not enormous either. The bigger cost is the
design debate around §4.3 (thread isolation) and §4.4 (done
semantics).

---

## 6. Why this isn't blocking

ADR-027 (LocalLogTailDriver) is a driver-level change. A2A is a
dispatcher-level change. They share nothing implementation-wise:

- The new M4 driver's `Input(text)` method does `tmux send-keys`.
  A2A's dispatch path goes `peer → hub /input → InputRouter → driver.Input`.
  Single-turn A2A through the new driver works the same way it
  works through any other driver.
- Multi-turn requires changing the dispatcher (§4.2) and adding a
  new driver event (§4.1), not changing the driver's input shape.

**Doing them together would double the scope and tangle the test
matrix.** Better to ship ADR-027 single-turn-A2A-compatible (as
designed), then revisit multi-turn separately when (a) demand
surfaces or (b) we want to take advantage of the new M4's
clean `awaiting_input` signal.

---

## 7. Recommendation

**Defer to a future ADR (probably ADR-028 after ADR-027 ships).**
Triggers to elevate it from "deferred" to "now":

1. **A real user pattern.** Mobile user watches a worker do
   something on phone, naturally wants to reply, finds the
   second-message-cancels-prior behavior confusing. Today this
   doesn't happen because A2A is wired only between agents, not
   exposed to mobile users.
2. **Steward UX wants real conversation.** If a steward delegates
   to a worker and wants to clarify mid-task instead of issuing a
   fresh fan-out, multi-turn becomes natural.
3. **Subagent (Task tool) chains.** When Task() subagents call
   their own Task() subagents and need to interrogate each other,
   the lack of multi-turn forces packing context. This is already
   visible in agent transcripts as "I'll just include everything
   prior."

Pick the trigger; design then; implement after.

---

## 8. Open questions (none blocking)

1. **Is host-runner restart survival a multi-turn requirement?** If a
   conversation is 8 hours and host-runner restarts at hour 4, do
   we want to resume? Currently no — task evaporates. Persistence
   (§5 wedge 5) is independent of multi-turn correctness but
   becomes more painful when conversations are longer.
2. **Does termipod want to mediate or just relay?** Today A2A is
   pure relay (hub doesn't read content). Multi-turn might want
   the hub to track conversation state for audit / replay /
   billing — but that violates the "data ownership law"
   (blueprint §3.4). Need an explicit decision.
3. **Does multi-turn change the agent-card?** A2A clients see
   advertised capabilities at `/a2a/<agent-id>/.well-known/agent.json`.
   If multi-turn is opt-in per agent, the card should advertise
   support so clients can decide whether to fall back to
   pack-history-into-one-message.

---

## 9. Cross-references

- [decisions/003-a2a-relay-required.md](../decisions/003-a2a-relay-required.md) — why GPU hosts go through hub-relay
- [decisions/007-mcp-vs-a2a-protocol-roles.md](../decisions/007-mcp-vs-a2a-protocol-roles.md) — A2A for agent↔agent, MCP for agent↔hub
- [decisions/016-subagent-scope-manifest.md](../decisions/016-subagent-scope-manifest.md) — which agents can A2A to which
- [decisions/027-local-log-tail-driver.md](../decisions/027-local-log-tail-driver.md) — the new M4 driver this discussion runs alongside (not coupled)
- `hub/internal/hostrunner/a2a/` — A2A server + tasks + tunnel
- `hub/internal/hostrunner/a2a_dispatcher.go` — peer → hub input routing + harvest
- A2A v0.3 spec: `a2a-protocol.org/latest/specification/`

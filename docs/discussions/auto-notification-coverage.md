# Auto-notification coverage across lifecycle events

> **Type:** discussion
> **Status:** Open (2026-05-16) — written after the ADR-029 D-8 wedges (W2.6–W2.11) shipped, when the principal asked "are there other automatic notifications we're missing?" Resolves into one or more follow-up ADRs as the load-bearing gaps get scheduled.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.610-alpha (post-W2.11, unpushed)

**TL;DR.** Termipod has two push-notification primitives today:
`attention_items` rows for the principal/steward inbox, and
`agent_events` `producer='system'` rows injected into a specific
agent's active session. A 2026-05-16 audit found that only four event
classes route through one of these primitives; the rest are silent
audit-only. Two more (`run.notify`, `a2a.received`) just shipped in
W2.10/W2.11; this discussion enumerates the remaining gaps so we can
schedule the rest deliberately rather than discover them one bug
report at a time.

---

## 1. The two primitives, restated

- **`attention_items` + `bus.Publish`** — async inbox. Mobile + web
  long-poll SSE consumers see new items; the principal/steward
  inbox renders them. Right shape for "you need to decide / approve
  / acknowledge". Persistent (rows don't auto-dismiss).
- **`agent_events` `producer='system'` + `bus.Publish(agentBusKey)`** —
  per-agent push. Drives the in-chat surface for one specific
  receiver. Right shape for "thing X happened that's relevant to
  this agent's conversation." Ephemeral (renders in chat,
  permanent in event stream, but not held in an inbox).

A third primitive — `local_notifications` on the mobile device — is
out of scope here; that's the OS-level push surface and rides on top
of whichever hub-side event it's listening to.

---

## 2. Coverage snapshot (post-W2.11)

| Event | Code | Push? |
|---|---|---|
| Approval-gated spawn | `handlers_agents.go:createSpawnApproval` | ✓ attention_items |
| Permission prompt | `mcp_more.go:handlePermissionPrompt` | ✓ attention_items |
| Budget exceeded | `budget.go:accumulateSpend` | ✓ attention_items |
| Deliverable send-back | `handlers_deliverables.go:1019` | ✓ attention_items |
| Approval resolved | `handlers_attention.go:695` | ✓ agent_events |
| Session resume | `handlers_sessions.go:634` | ✓ agent_events |
| Mode / model change | `handlers_sessions.go:868` | ✓ agent_events (`system`) |
| **Task terminal (W2.9)** | `task_notify.go` | ✓ agent_events (`task.notify`) |
| **Run terminal (W2.10)** | `run_notify.go` | ✓ agent_events (`run.notify`) |
| **A2A received (W2.11)** | `a2a_notify.go` | ✓ agent_events (`a2a.received`) |
| Agent terminate (no task linkage) | `handlers_agents.go:343` | ⚠ audit only |
| Agent archive | `handlers_agents.go:482` | ⚠ audit only |
| Session open / fork / archive / delete | `handlers_sessions.go` | ⚠ audit only |
| Document create / update | `handlers_documents.go:143` | ⚠ audit only |
| Artifact create | `handlers_artifacts.go:150` | ⚠ audit only |
| Deliverable create / ratify | `handlers_deliverables.go:323,513` | ⚠ audit only |
| Plan / plan_step update | `handlers_plans.go:294,511` | ⚠ audit only |
| Project phase transition | `handlers_projects.go:309` | ⚠ audit only |
| Project create / archive | `handlers_projects.go:305,499` | ⚠ audit only |
| Schedule firing | `handlers_schedules.go:291` | ⚠ audit only |
| Host disconnect / stale / crash | *no detection code* | ✗ no detection |

---

## 3. Load-bearing gaps to schedule next

These are the ones where the absence of push actively costs the user.

### 3.1 Host health (`host.stale` / `host.offline` attention)

When a host-runner crashes or stops heartbeating, the hub has no
detection logic. Spawn commands continue to enqueue into a dead
tunnel; agents on that host appear `running` indefinitely until an
operator notices. The audit found *no detection code at all*.

**Recommendation.** Add a periodic sweep (default 30s):

```sql
UPDATE hosts SET status='stale'
 WHERE status='online' AND last_seen_at < datetime('now', '-2 minutes');
UPDATE hosts SET status='offline'
 WHERE status='stale'  AND last_seen_at < datetime('now', '-10 minutes');
```

On `online → stale` and `stale → offline` transitions, fire an
`attention_items` row tier=`moderate`, target=`@principal`+team
steward. This is the only host-level "your fleet is in trouble"
signal mobile gets.

Approx 80–120 LOC + a `goroutine` started by `Server.Serve`.

### 3.2 Project phase transitions

Phase advances change what's expected of every agent bound to the
project (the prompts the project steward operates under, the
deliverables the workers should be producing). When the principal
flips `phase='ratify'` from mobile, none of the live agents hear
about it — they keep operating under the prior phase's context.

**Recommendation.** Mirror `task.notify`. On `project.phase_set`, find
every agent with `agents.project_id = ?` and `status = 'running'`,
look up their active session, inject a
`kind='project.phase_changed' producer='system'` event with the new
phase + the phase brief snippet. The receiving agent sees "Project
phase advanced to <name>" in its session and can react on its next
turn.

Approx 80 LOC. Companion to W2.9/2.10/2.11 file pattern.

### 3.3 Ad-hoc agent terminate

When a worker terminates and it's not linked to a task (e.g. a
fire-and-forget probe spawned via the legacy path), `W2.9` doesn't
fire. The parent steward gets no signal that its child is gone — it
finds out only when its next `agents.list live=true` call comes back
shorter than expected.

**Recommendation.** Add `notifyAgentParent(ctx, team, agentID,
fromStatus, toStatus)` triggered from the same site as
`deriveTaskStatusFromAgent`. When the agent has `parent_agent_id`
set AND no task linkage AND status flips to a terminal state, push
`kind='agent.terminated' producer='system'` into the parent's
session. Body: `"Worker @<handle> exited (<reason>)."`. Skips when a
task-linkage path already fired (avoid double-notify).

Approx 100 LOC.

---

## 4. Medium-priority gaps

### 4.1 Document / artifact create + deliverable create/ratify

When a worker publishes work (a memo, a paper, an artifact, a
ratified deliverable), the principal currently sees it only via
mobile pull-to-refresh or by tapping into the project. For
collaborative flows the steward who delegated should get a push
("@worker.research published doc-7").

**Recommendation.** When the artifact / document / deliverable's
creator is *not* the principal, post a `kind='work.published'
producer='system'` event into the creator's parent_agent_id's
session (typically the project steward). The same pattern can ride
the deliverable's ratification: `kind='deliverable.ratified'` on
status flip. This bundles three event classes through one helper.

Approx 150 LOC (one helper, three call sites).

### 4.2 Plan / plan_step update

A plan step transitioning may change what its worker should be
doing. Today only the task-link cascade (W2.9) covers cases where
the plan step has a linked task. Direct plan-step updates without
task linkage are silent.

**Recommendation.** Lower priority than 4.1 — plan steps without
task linkage are usually chassis-driven, and the chassis polls the
plan. Worth tracking but probably not worth a wedge until a real
user surfaces the need.

---

## 5. Lower priority / by-design silent

- **Session open / fork / close / delete** — internal lifecycle; the
  principal sees state changes in the sessions list. Push would add
  more noise than signal.
- **Project create / archive** — infrequent operator action; mobile
  pull-to-refresh is fine.
- **Schedule firing** — the firing itself is plumbing; the *triggered*
  action (spawn, task, etc.) notifies downstream via its own path.

---

## 6. Cross-cutting questions

### 6.1 Where to route by default — attention_items vs agent_events?

Rule of thumb from the audit:
- **`attention_items`** when the event needs *acknowledgment* (the
  user/agent has to decide / approve / dismiss). Persistent, inbox-
  shaped.
- **`agent_events` system row** when the event is *informational*
  (the user/agent just needs to know it happened). Ephemeral in the
  inbox sense; renders in chat.

The two are not mutually exclusive — a critical event can do both
(attention item for the inbox badge + chat injection for context).
Default to whichever side the receiver lives on:
- Principal-facing → attention_items (mobile's inbox is the right
  surface).
- Agent-facing → agent_events injection into the agent's session.

### 6.2 What about flutter_local_notifications?

Mobile already wires `flutter_local_notifications` against attention
items (v1.0.323). Anything that writes an attention item gets a
device-level push for free. Anything that writes only an
`agent_events` system row does *not* — it surfaces inline in the
agent's chat the next time the user opens it, but doesn't ring the
phone. This is the right split:
- "ring the phone" = principal needs to act → attention_item.
- "log it inline" = agent already has the surface open → agent_event.

If we ever want device-level push for agent-side events (e.g. "your
worker finished while you were away"), the natural plumbing is a
ServiceWorker-style subscription that mirrors a subset of agent
events to the local notifications path. That's a separate concern
(notification-routing UX) not addressed here.

---

## 7. Open follow-ups (not in this discussion)

- **A2A back-channel from hub itself.** Today the hub posts
  `producer='system'` events to a single receiver. If we ever want
  hub-system messages that show up as A2A traffic to multiple
  observers (e.g. a sweep status broadcast), we need either a
  multi-receiver fan-out helper or a synthetic system-actor with an
  agent_id. Parked.
- **Notification throttling / deduplication.** A noisy worker
  (training 100 runs in a row) could spam its parent steward. No
  rate-limiting today. Worth revisiting once 4.1 lands.
- **Mobile rendering of new system event kinds.** `task.notify`,
  `run.notify`, `a2a.received` all use `producer='system'` but
  different `kind` values. Mobile chat surface needs per-kind
  rendering (icon, color, action chip). Currently they render as
  generic system rows. ADR-029 Phase 2 W8/W9 cover `task.notify`;
  W2.10/W2.11 rendering is a Phase 2 follow-up.

---

## 8. Status — links forward

- Audit conducted: 2026-05-16 (this doc captures the result)
- W2.9 `task.notify`: shipped post-v1.0.610-alpha
- W2.10 `run.notify`: shipped (unpushed at write time)
- W2.11 `a2a.received`: shipped (unpushed at write time)
- Host health (§3.1), project phase (§3.2), ad-hoc agent terminate
  (§3.3): unscheduled
- Document / artifact / deliverable push (§4.1): unscheduled

When any §3 or §4 item lands, give it its own ADR + plan and link
back here. This discussion flips to Resolved once the §3
load-bearing items ship; the §4–§5 items may stay deferred
indefinitely.

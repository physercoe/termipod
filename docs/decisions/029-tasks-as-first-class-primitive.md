# 029. Tasks as the first-class primitive for steward-dispatched work

> **Type:** decision
> **Status:** Proposed (2026-05-16) — D-1 through D-8 locked in the 2026-05-16 design conversation; Phase 1 (D-1 through D-7) shipped v1.0.610-alpha; Phase 1.5 (D-8) shipped post-v1.0.610-alpha
> **Audience:** contributors
> **Last verified vs code:** v1.0.610-alpha

**TL;DR.** Promote `tasks` from "kanban side-table the principal
can use" to the canonical surface for any steward-dispatched
unit of project work. Every spawn that represents project work
either references an existing task (`task_id`) or creates one
inline (`task: {…}`); ad-hoc fire-and-forget spawns remain valid
but explicit (no task linkage). Task status auto-derives from
the linked agent's lifecycle (spawn → `in_progress`, terminate-
clean → `done`, crash/fail → `blocked`); manual `tasks.update`
always wins as override. Audit every task mutation including
auto-flips so the activity feed has a complete history. Resolve
the "todo" name collision at source by renaming
`NoteKind.todo` → `NoteKind.reminder`; project tasks keep
`status='todo'` as the canonical sense. Mobile `_TaskTile`
renders the triad (assignee + assigner + relative time) and tap
opens a task detail screen surfacing the linked spawn / session
/ audit. Round out the MCP lifecycle by adding `tasks.delete`
(REST exists, MCP wrapper does not) and an explicit `cancelled`
status (auto-derive never produces it; human/steward-only
terminal state). The full design discussion is at
[discussions/tasks-as-first-class-primitive.md](../discussions/tasks-as-first-class-primitive.md);
the work is in
[plans/tasks-first-class-rollout.md](../plans/tasks-first-class-rollout.md).

## Context

When the project steward spawns a worker today, the worker
appears in the Agents tab but the project's Tasks tab stays
empty. Tracing the code surfaced four independent gaps that all
contribute to the same symptom:

1. **Spawn ↔ task linkage missing.** `agents.spawn`
   (`hub/internal/hubmcpserver/tools.go:409-424`) takes no
   `task_id` field; the MCP edge doesn't exist.
2. **Mobile doesn't render assignee/assigner.** Both columns
   (`assignee_id`, `created_by_id`) exist in the schema since
   `migrations/0001_initial.up.sql:139-153` but
   `_TaskTile.build` at `project_detail_screen.dart:960-1065`
   only paints title + body preview + priority dot.
3. **Task mutations are unaudited.** `handlers_tasks.go` has
   zero `recordAudit(…)` calls. The activity feed has no record
   of task lifecycle, including auto-flips from
   `syncPlanStepTaskStatus` (`handlers_tasks.go:314-328`).
4. **"Todo" is overloaded.** `tasks.status='todo'` on the hub
   collides with `NoteKind.todo` in the local per-device sqflite
   note store (`lib/services/notes/notes_db.dart:52`). No
   glossary entry distinguishes the two senses.

The discussion captures the first-principles review (what a task
primitive must carry — identity / ownership / state / linkage)
and the industry-practice comparators (Linear, Jira, GitHub
Issues, Asana). This ADR records the decisions; the discussion
records the alternatives.

## Decisions

### D-1. Tasks are the first-class primitive for steward-dispatched work

Every spawn that represents a unit of project work either
references an existing `task_id` or creates one inline via
`task: {title, body_md, priority}`. Ad-hoc fire-and-forget
spawns (steward exploration, quick probes) remain valid and
explicit — they pass neither field, no task row is created, and
the worker only surfaces on the Agents tab.

**What "represents project work" means in practice:** any spawn
where `project_id` is set and `parent_agent_id` is a project
steward. The general steward can't spawn project-bound workers
directly (per ADR-025 W9); it delegates via
`request_project_steward`. Once a project steward is materialized,
its spawns are the load-bearing path this ADR addresses.

**Why not require a task on every project-bound spawn.** Quick
probes ("re-check the GPU is free"; "pull the latest weights")
don't deserve a task row. Forcing one creates taskboard noise
that mirrors the "every CI run gets a Jira ticket" anti-pattern.
The ad-hoc escape hatch is load-bearing.

### D-2. Spawn↔task edge has three shapes (A3)

`agents.spawn` accepts:

- **`task_id: <id>`** — link to an existing task. The hub
  validates `task.project_id == spawn.project_id`. On success,
  sets `tasks.assignee_id = new_agent.id` and stamps
  `agent_spawns.task_id = task_id`.
- **`task: {title, body_md?, priority?}`** — inline-create. Hub
  creates the task row + spawn row in one transaction. Stamps
  `task.assignee_id = new_agent.id`,
  `task.created_by_id = parent_agent_id`,
  `task.status = 'in_progress'`, `task.started_at = now`.
  `parent_agent_id` is the spawn's parent (the project steward
  in the canonical flow); this preserves the "assigner" trail
  per ADR-025's accountability model.
- **Neither field** — ad-hoc fire-and-forget; no task row.
  Existing behavior, preserved.

Mutual exclusion: passing both fields returns 400. The hub
refuses ambiguity rather than guess intent.

**`created_by_id` when the caller isn't an agent.** Two callers
have no `parent_agent_id` to stamp:
(a) the principal calling `agents.spawn` directly via REST /
mobile UI, and (b) the principal calling `tasks.create` directly.
In both cases `tasks.created_by_id` lands NULL (the column allows
it per the existing schema). Mobile renders NULL as **"you"** when
the current auth context is the principal who's looking at the
row, and **"unknown"** when it isn't (e.g. another team member
viewing a principal-created task). No new schema column is added
— the auth-context resolution is a UI concern. Audit rows already
carry `actor_kind` so the historical "who" is preserved via the
audit chain even when the task column is NULL.

**Spawn against a terminal task is rejected (409).** If
`task_id` references a task whose status is `done` or `cancelled`,
`agents.spawn` returns 409 with a hint to call
`tasks.update status='in_progress'` first. Making "reopen" an
explicit decision keeps the audit chain clean — implicit reopen
hides a state mutation behind a different verb and breaks the
attribution model. `blocked` tasks are exempt (a new spawn is
the canonical way to unblock).

**Why A3 over A1 (two-call) or A2 (auto-only).** A1 forces every
steward to remember a two-step dance and produces a window where
the spawn exists but the task doesn't — bad for the
attribution story. A2 removes the ad-hoc escape hatch that's
load-bearing for exploration. A3 is the GitHub Issues +
`gh pr create --linked-issue` shape: independent primitive, sugar
for the common case, escape hatch for the long tail.

### D-3. Status auto-derives from linked agent lifecycle; manual override wins

Task status transitions are driven by the linked spawn/agent:

| Trigger | Effect |
|---|---|
| `spawn` created with task linkage | `task.status = 'in_progress'`, `task.started_at = now` |
| Linked agent transitions to `terminated` (any cause) | `task.status = 'done'`, `task.completed_at = now` |
| Linked agent transitions to `crashed` or `failed` | `task.status = 'blocked'` |
| Agent emits `task.complete` event (optional) | `task.status = 'done'`, `task.completed_at = ts`, `task.result_summary = payload.summary` |
| Principal/steward calls `tasks.update status=…` | Manual override; auto-derive defers until the next agent-side transition |

The manual override always wins because the principal may mark
"done by inspection" or "blocked pending external decision"
independent of agent state. The next agent-side transition
resumes auto-derive — this is the same coupling pattern
`syncPlanStepTaskStatus` already uses (`handlers_tasks.go:309-312`).

Plan-step status sync stays as today; the spawn-side derive is
additive and operates on the same task row when both linkages
exist.

**Flip-on-spawn, not flip-on-running.** The task transitions to
`in_progress` at spawn-creation time, not when the agent first
reports `running`. This is deterministic and race-free — the
spawn row is the intent record, and a worker that fails to ever
reach `running` (host unreachable, template error, image pull
failure) flows through the standard `crashed/failed` transition,
which auto-derives the task to `blocked`. The window where "spawn
exists, task still todo" is a worse user signal than "task is
in_progress, agent is pending" — the user sees movement
immediately when they ask for work.

**Most-recent-spawn drives the task status.** When a worker
crashes and the steward spawns a replacement, two `agent_spawns`
rows now share one `task_id`. Auto-derive walks
`agent_spawns WHERE task_id=? ORDER BY spawned_at DESC LIMIT 1`
and operates on that one agent. Simple, predictable, matches the
mental model "this worker is the one currently doing the task";
the older crashed spawn is preserved for the audit chain but no
longer drives the status. Multi-worker-on-one-task (fan-out) is
out of scope — one task, one active worker; if you want parallel
work, create sibling tasks.

**`terminated` → `done` regardless of cause.** When the agent
status flips to `terminated`, the task auto-derives to `done`
even when the cause was `agents.terminate` from the steward
(rather than a clean self-exit). The steward who wanted to record
the work as cancelled instead calls `tasks.update
status='cancelled'` after the terminate — the manual override
path. Distinguishing "steward-killed mid-task" from "agent
finished cleanly" at the auto-derive layer would require either
a richer agent terminal vocabulary (terminated_clean /
terminated_killed) or a heuristic on whether a `task.complete`
event preceded the terminate; both add branching for a case the
manual-override path already covers.

### D-4. Audit every task mutation

Six sites gain `recordAudit(team, action, "task", id, summary, meta)`:

| Site | Action | Meta |
|---|---|---|
| `handleCreateTask` | `task.create` | `source: 'ad_hoc' \| 'plan' \| 'spawn'` |
| `handleUpdateTask` (status flip) | `task.status` | `from`, `to`, `source: 'principal' \| 'steward' \| 'auto'` |
| `handleUpdateTask` (non-status fields) | `task.update` | changed-field list |
| `handleDeleteTask` | `task.delete` | — |
| `syncPlanStepTaskStatus` auto-flip | `task.status` | `source: 'plan_step'`, `plan_step_id` |
| Spawn-side auto-create / auto-derive | `task.create` / `task.status` | `source: 'spawn'`, `spawn_id`, `agent_id` |

The activity feed becomes a full task history with attribution.
**Auto-flips emit audit too** — the pre-v1.0.610 silent flip is
the bug we're closing alongside the create path.

### D-5. Resolve the "todo" name collision at source

Rename `NoteKind.todo` → `NoteKind.reminder` in
`lib/services/notes/notes_db.dart` and the one screen string in
`note_editor_screen.dart`. The personal note kind becomes
"reminder"; the project-task status `todo` stays as the canonical
sense.

Glossary entries:

- **task** — already exists at `glossary.md:794`; extend the
  *Distinguish from* line to call out the note-kind collision is
  resolved by R1 (rename to `reminder`).
- **note** — new entry. Personal scratch, device-local, kinds:
  `note` (free-form) and `reminder` (was `todo`); never synced
  to the hub (per the file header). *Distinguish from:* **task**.

**Why R1 over a glossary-only fix (R2).** A glossary entry helps
the next reader but the next *agent* still trips on raw code
references. The rename is a one-file change with no production
data implications — the kind string is stored on-device and the
migration is a single `UPDATE notes SET kind='reminder' WHERE
kind='todo'` on the sqflite side. Cheap, permanent, removes the
class of ambiguity.

### D-6. Mobile renders the triad and links the work

`_TaskTile` (`project_detail_screen.dart:960-1065`) gains:

- **Assignee handle + status pip** (running/idle/blocked color
  matched to the agent's current state)
- **Assigner handle** as a smaller "by @x" attribution line
- **Relative timestamp** — "started 3m ago" / "done 1h ago" / no
  timestamp when never started

Tap routes to a task detail screen showing the linked spawn (its
agent feed), the linked session (its chat — same `SessionChatScreen`
the Sessions tab uses), and an audit timeline filtered to
`target_kind='task' AND target_id=<task_id>`.

Tile information density stays within the existing visual
budget — assignee + assigner replace a previously-empty row of
chrome, not on top of it.

### D-7. Task lifecycle is fully covered by MCP — add `tasks.delete` + `cancelled` status

The MCP surface already exposes `tasks.{list,get,create,update}`,
so the steward can create and modify tasks today. Two gaps close
the lifecycle so the steward can complete the round-trip:

1. **`tasks.delete` MCP wrapper.** The REST handler
   `handleDeleteTask` exists; the MCP tool does not. Without it,
   a steward that creates a task by mistake (or wants to undo an
   inline-create from a misfired spawn) has no MCP path to drop
   the row. Adding the wrapper is ~15 LOC alongside the existing
   cluster in `hub/internal/hubmcpserver/tools.go`. Tier:
   `TierRoutine` to match `tasks.update`.

2. **`cancelled` as a first-class status.** Vocabulary today is
   `todo / in_progress / done` (+ implicit `blocked` from D-3).
   When the decision is "we tried this approach and decided not
   to ship it", the only options today are delete (destructive,
   no audit trail of the decision) or mark `done` (misleading).
   `cancelled` fills the gap. The schema column is `TEXT NOT
   NULL DEFAULT 'todo'` with no CHECK constraint, so no migration
   is required — the status is added by docs / glossary / mobile
   rendering only.

**Auto-derive contract for `cancelled`:** terminal and
human-only. Auto-derive (D-3) never transitions INTO `cancelled`,
and once a task is `cancelled` no auto-derive transition fires —
the steward / principal owns the decision until a manual
`tasks.update` reopens the row. Mirrors how `done` is treated
when the principal manually marks it.

**Why these two together.** Delete and cancel solve adjacent
problems: delete removes a wrong-entry; cancel preserves the
record of a decision. A system with only delete encourages
rewriting history; a system with only cancel encourages
hoarding rows. Both keep the audit chain honest.

**Why not also add `paused` / `on_hold`.** Considered. Rejected
because "we'll come back to this later" is what `todo` already
means — re-decorating it adds vocabulary without adding
discriminator value. If a real use case surfaces (e.g. "blocked
on external dependency, distinct from blocked-by-crash"), revisit.

### D-8. Worker delivery + assigner notification are first-class edges

D-1 through D-7 established the task primitive but left the
information-flow edges underspecified: a steward could call
`agents.spawn task: {…}`, the task row landed, and the worker
spawned — but `body_md` never reached the worker, and when the
worker finished the steward had no push signal. Workers learned
about their assignment via an out-of-band `a2a.invoke`, and
stewards learned about completion via polling. Both edges were
manual conventions, not enforced by the primitive.

Promote them to load-bearing properties of the task linkage:

**Down (steward → worker) has two delivery channels.** A task body
lands in the worker before its first turn:

1. **Standing context in CLAUDE.md.** The rendered `## Task`
   section under `context_files.CLAUDE.md` carries the task title
   (as an H1) and body_md. Worker re-reads this any time it needs
   to recall what it's been asked to do — same surface the persona
   override uses.
2. **First-turn trigger via InputRouter.** A
   `producer='user' kind='input.text'` row is inserted into
   `agent_events` immediately after the spawn commits, carrying
   the same title + body string as the user-message payload. The
   host-runner's `InputRouter` delivers it to the driver on the
   next tick. The worker starts the turn without waiting for an
   external nudge.

CLAUDE.md alone is insufficient (engines treat it as system
context, not a trigger), and the auto-input alone is insufficient
(workers need a re-readable reference, not just a message in
history). Both edges ship together.

**Up (worker → assigner) is a single notification channel.** When
`tasks.status` transitions to a terminal state (done / blocked /
cancelled), the hub posts a `kind='task.notify' producer='system'`
event into the assigner's most-recent active session. Payload
carries `task_id`, `title`, `from`, `to`, `result_summary`, and a
prerendered `body` string. The steward sees it inline in chat
without polling.

Triggered from both flip sites:
- Manual updates via `handlePatchTask` (MCP `tasks.update` /
  `tasks.complete`, mobile UI flip).
- Auto-derive via `deriveTaskStatusFromAgent` (worker terminates,
  crashes, or fails).

Best-effort: NULL `created_by_id` (principal-direct task) and no
live session for the assigner both silently degrade. The audit row
remains the durable record; the notification is the push convenience.

**`tasks.complete` is the worker's close-out verb.** Adds an MCP
tool with shape `{project_id, task, summary?}` that bundles
`status='done'` + `completed_at` + `result_summary` in one call.
The MCP description steers workers here (rather than generic
`tasks.update`) for the close-out path; `tasks.update` remains the
verb for blocked / cancelled / re-opens / mid-flight edits.
`result_summary` lands in the schema column added by W1 (which had
been provisioned but never written until this decision).

**Why a system event, not an A2A back-channel.** A2A requires the
assigner to expose an A2A card and the hub to resolve it, plus a
sender identity. For hub-driven notifications (auto-derive on
agent terminate) there's no natural sender — the worker is dead,
the hub itself has no agent_id. A system-attributed
`producer='system'` event renders cleanly in the existing chat
surface (same treatment as `system.mode_changed`) and doesn't
require sender plumbing. A2A stays available for ad-hoc
mid-conversation traffic in either direction.

**Why not also auto-fire on `task.create` or `in_progress` flips.**
Considered. Rejected because the steward already knows it just
created the task (it's the caller) or that work has started (it
just spawned the worker). The notification is for transitions the
assigner cannot infer from its own actions.

**Notification primitive generalises beyond tasks.** The
`producer='system' kind='<x>.notify'` pattern introduced by W2.9
is now the canonical "push to a specific agent's session" channel
on the hub. A 2026-05-16 audit
([discussions/auto-notification-coverage.md](../discussions/auto-notification-coverage.md))
enumerated the other lifecycle events that should ride the same
primitive; W2.10 (`run.notify` on terminal run transitions) and
W2.11 (`a2a.received` on peer message delivery) ship the next two
incrementally. Remaining gaps (host health, project phase
transitions, ad-hoc agent terminate, document/artifact publish)
are scheduled out of that discussion, not this ADR.

## Alternatives considered

### A-1. Require task on every project-bound spawn (no escape hatch)

Considered for D-1. Rejected because quick probes ("check GPU
free", "list templates") don't deserve a task row, and forcing
one creates the "every CI run gets a Jira ticket" anti-pattern.
The ad-hoc escape hatch is load-bearing for steward exploration.

### A-2. Two-call only (D-2 A1)

Steward calls `tasks.create` then `agents.spawn task_id=...`.
Cleaner separation but forces every steward to remember the
two-step dance and produces a window where the spawn exists but
the task doesn't. Worse attribution story for negligible
schema savings.

### A-3. Auto-create only (D-2 A2)

Every spawn always creates a task. Removes the ad-hoc escape
hatch. Rejected per D-1's "fire-and-forget remains valid"
constraint.

### A-4. Glossary entry instead of rename for D-5

Cheap fix; entry pins the two senses. But the next *agent*
reading code still trips on raw `NoteKind.todo` references. The
rename is one file with no production-data implications (kind
is device-local, sqflite migration is trivial). Permanent
removal of the ambiguity beats a glossary footnote.

### A-5. Multi-assignee + labels in MVP

Industry-standard for general task systems. Rejected for MVP
because (a) single-assignee matches Linear's MVP shape for the
same reason — one owner is easier to attribute, (b) labels pay
no rent in an agent-native system until we have a real use case.
Deferred per discussion §8.

## Consequences

### Positive

- **The Tasks tab becomes the canonical project view.** Every
  unit of work has a row with attribution and a tap-path to the
  live work. The Agents tab becomes a process-level drill-down
  rather than a competing surface.
- **Activity feed gains task history.** The `audit_events`-backed
  feed shows create/status/update/delete with attribution,
  closing the "who flipped this to done at 3am" hole.
- **Auto-derive eliminates manual status flipping for the
  common case.** Steward spawns → task auto-flips to
  in_progress. Agent terminates clean → done. The principal
  only intervenes for overrides.
- **"Todo" stops being ambiguous in code/docs.** Renaming
  `NoteKind.todo` makes the project-task sense the only one in
  the codebase.

### Negative / accepted

- **`agents.spawn` schema grows** by two optional fields
  (`task_id`, `task`). Documented as mutually exclusive in the
  tool description; hub returns 400 on both-set.
- **`agent_spawns` and `tasks` gain new columns** —
  `agent_spawns.task_id`, `tasks.started_at`, `tasks.completed_at`,
  `tasks.result_summary`. One migration per phase.
- **Audit-row volume increases** — every status flip writes a
  row. At MVP scale (<100k task events per phase 1, per the
  insights doc's same target) this is below the noise floor of
  existing event volume.
- **The `NoteKind.todo → reminder` rename** is a breaking
  device-storage change. Migration: one `UPDATE` SQL on app
  start; no risk because notes are device-local and the column
  is a string enum.

## Open follow-ups

- **Due dates + notification plumbing.** `tasks.due_at` schema +
  notification trigger when due is in the near future. Builds on
  the v1.0.323+ notification surface. Phase 3 / follow-up wedge.
- **Task templates** — a "spawn-with-task-template" surface for
  repeating workflows (nightly probe, weekly review). Could
  become a fifth template kind. Out of scope for this round.
- **Multi-assignee** — pair-on-task. Defer until a real use case.
- **Personal-task sync (the "promote notes-with-`done`-flag to
  hub-side personal tasks" question).** Per the
  `notes_db.dart` header, deferred until v2 — needs a
  conflict-resolution story across devices.
- **OpenAPI lint for `agents.spawn` mutual exclusion** —
  analogous to existing `lint-openapi.sh` checks, catches future
  callers that pass both fields.

## References

- Discussion that produced this ADR:
  [discussions/tasks-as-first-class-primitive.md](../discussions/tasks-as-first-class-primitive.md)
- Execution plan:
  [plans/tasks-first-class-rollout.md](../plans/tasks-first-class-rollout.md)
- Current task schema: `hub/migrations/0001_initial.up.sql:139-153`,
  `0020_tasks_plan_step_id.up.sql`, `0021_tasks_priority.up.sql`
- Current spawn handler: `hub/internal/server/handlers_agents.go`
  (`spawnAgent`); MCP at `hub/internal/hubmcpserver/tools.go:409-424`
- Plan-step ↔ task sync precedent:
  `hub/internal/server/handlers_tasks.go:309-329` (the coupling
  pattern D-3 generalizes)
- `_TaskTile` widget: `lib/screens/projects/project_detail_screen.dart:960-1065`
- Personal note kind: `lib/services/notes/notes_db.dart:52`
- Related ADRs:
  ADR-024 (project detail chassis),
  ADR-025 (project steward accountability — provides the
  "assigner = parent_agent_id" attribution chain this ADR
  consumes),
  ADR-028 (host control CLI — sibling ADR from the same design
  session; unrelated content).

# Tasks as first-class — phased rollout

> **Type:** plan
> **Status:** Proposed (2026-05-16) — two phases, no work started; ADR-029 captures the locked decisions
> **Audience:** contributors
> **Last verified vs code:** v1.0.609-alpha

**TL;DR.** Close the four gaps that make the Tasks tab empty
when the project steward spawns a worker: spawn↔task linkage,
status auto-derivation, task audit, and the "todo" name
collision. Phase 1 is hub-side (schema, MCP surface, audit,
rename); Phase 2 is mobile-side (tile renders the triad,
detail screen surfaces linked spawn/session). Each phase is
independently shippable; Phase 1 closes the principal's
reported bug end-to-end, Phase 2 promotes the Tasks tab from a
checklist to the operational dashboard. Motivation is in
[discussions/tasks-as-first-class-primitive.md](../discussions/tasks-as-first-class-primitive.md);
locked decisions are in
[decisions/029-tasks-as-first-class-primitive.md](../decisions/029-tasks-as-first-class-primitive.md).

---

## 1. Phase order, summarized

| Phase | Ship | Approx LOC | Depends on |
|---|---|---|---|
| 1 | Hub: spawn↔task edge + status auto-derive + `tasks.delete` MCP + `cancelled` status + task audit + `NoteKind` rename + glossary | ~380 | — |
| 2 | Mobile: `_TaskTile` triad + task detail surfaces linked spawn/session + `cancelled` rendering | ~250 | Phase 1 schema |

Phase 1 closes the empty-Tasks-tab bug end-to-end (every
project-steward-dispatched worker materializes a task row).
Phase 2 makes the rendered tab carry the assignee + assigner +
time the user asked for.

---

## 2. Phase 1 — hub-side

### 2.1 Goal

When the project steward calls `agents.spawn` with a `task_id`
or inline `task: {…}`, a task row exists and is linked to the
new agent. Task status auto-flips to `in_progress` on spawn and
to `done` / `blocked` on the agent's terminal state. The steward
can also delete a task it created in error and mark a task
`cancelled` when the decision is to stop the work explicitly
(distinct from `done`). Every task mutation writes an
`audit_events` row. The "todo" name collision is gone from the
codebase.

### 2.2 Wedges

**W1. Schema — `agent_spawns.task_id` + task lifecycle columns (~60 LOC).**
- New migration `004X_tasks_spawn_lifecycle.up.sql`:
  ```sql
  ALTER TABLE agent_spawns ADD COLUMN task_id TEXT
    REFERENCES tasks(id) ON DELETE SET NULL;
  CREATE INDEX idx_agent_spawns_task ON agent_spawns(task_id)
    WHERE task_id IS NOT NULL;

  ALTER TABLE tasks ADD COLUMN started_at TEXT;
  ALTER TABLE tasks ADD COLUMN completed_at TEXT;
  ALTER TABLE tasks ADD COLUMN result_summary TEXT;
  ```
- Down migration drops both. golang-migrate single-transaction
  pattern.
- No backfill — pre-migration spawns leave `task_id` NULL,
  same as ad-hoc spawns going forward.

**W2. MCP — `agents.spawn` accepts `task_id` / `task` (~100 LOC).**
- Extend `agents.spawn` input schema in
  `hub/internal/hubmcpserver/tools.go:409-424` with optional
  `task_id` and `task` fields. Mutual exclusion enforced
  server-side (return 400 if both set).
- Hub handler (`hub/internal/server/handlers_agents.go`
  `spawnAgent`) gains a pre-spawn block:
  - If `task_id` set:
    - Validate `task.project_id == spawn.project_id`; 400 on
      mismatch (per ADR-029 D-2).
    - Validate `task.status NOT IN ('done','cancelled')`; **409
      with hint** to call `tasks.update status='in_progress'`
      first when the task is terminal (per ADR-029 D-2 "Spawn
      against a terminal task is rejected"). `blocked` is exempt
      — a new spawn is the canonical unblock path.
  - If `task` inline:
    - Validate `task.title` non-empty (mirror the existing
      `tasks.create` precondition).
    - Validate `project_id` exists in the team via
      `validateProjectInTeam` (mirrors `request_project_steward`).
    - INSERT a tasks row in the same transaction, populating
      `assignee_id = new_agent.id`,
      `created_by_id = parent_agent_id` (NULL when the caller is
      principal-kind per ADR-029 D-2), `status = 'in_progress'`,
      `started_at = now`, `priority` defaulted to `med` if absent.
  - Stamp `agent_spawns.task_id` with the resolved id (either
    branch).
- Tool description updated to call out the mutual exclusion, the
  409-on-terminal rule, and the parent_agent_id-as-assigner
  semantics with the NULL-when-principal carve-out.

**W2.5. `tasks.delete` MCP wrapper + `cancelled` status (~40 LOC).**
- Add a `tasks.delete` MCP tool in
  `hub/internal/hubmcpserver/tools.go` alongside the existing
  `tasks.{list,get,create,update}` cluster:
  - Inputs: `project_id` (required), `task` (required, the task
    id).
  - Calls `DELETE /v1/teams/{team}/projects/{p}/tasks/{id}`
    (REST handler exists today).
  - Description calls out the difference from
    `tasks.update status='cancelled'`: delete drops the row;
    cancelled keeps it for the audit trail and renders muted in
    mobile.
- Add `"tasks.delete": TierRoutine` to
  `hub/internal/server/tiers.go` — matches the rest of the task
  cluster.
- Add `cancelled` to the documented task status vocabulary.
  Schema today is `TEXT NOT NULL DEFAULT 'todo'` with no CHECK
  constraint, so no migration is required — only the
  documentation + comments in `handlers_tasks.go` (the
  block at lines 27-37 + `Status string` doc comments) and the
  glossary `### task` entry need to list `todo / in_progress /
  blocked / done / cancelled`.
- Auto-derive (W3 below) MUST NOT transition INTO `cancelled` —
  it is an explicit human / steward override only. From
  `cancelled`, no auto-derive transition occurs either (the row
  is terminal until a manual `tasks.update` reopens it).
- Mobile (Phase 2 W8) renders `cancelled` muted with a
  strikethrough title; this hint lands in the Phase 2 wedge
  but the status itself ships in Phase 1.

**W3. Status auto-derive on agent terminal transitions (~100 LOC).**
- `handlePatchAgent` (`handlers_agents.go:238-360`) already has
  the terminal-status branch (status → terminated/crashed/failed
  pauses sessions and revokes tokens). Extend it: for the task
  linked to the **most recent** spawn for the patched agent
  (`SELECT task_id FROM agent_spawns WHERE child_agent_id=?
  ORDER BY spawned_at DESC LIMIT 1`) AND the task's current
  status is `in_progress`:
  - terminated (any cause, including steward-initiated
    `agents.terminate`) → `task.status = 'done'`,
    `completed_at = now`. Per ADR-029 D-3 "terminated → done
    regardless of cause"; steward overrides to `cancelled` via
    `tasks.update` after the terminate when the work was stopped
    rather than completed.
  - crashed / failed → `task.status = 'blocked'`.
- The **most-recent-spawn rule** (ADR-029 D-3) means a worker
  that crashes and is replaced doesn't have its old spawn
  driving the task. Older spawns stay in the audit chain but
  the auto-derive walks only the latest.
- The flip happens at spawn time (W2) not when the agent first
  reports `running`. Per ADR-029 D-3 "flip-on-spawn", this is
  deterministic and the crashed-before-running case routes
  through this W3 transition to `blocked` naturally.
- Manual override path stays: `tasks.update status=…` from the
  MCP/REST surface wins; auto-derive defers until the next
  agent-side transition (per ADR-029 D-3). `cancelled` is
  terminal — once a task is cancelled, no auto-derive transition
  fires until a manual `tasks.update` reopens the row.
- Generalize: extract `deriveTaskStatusFromAgent` so both the
  PATCH handler and any future code path (e.g. the explicit
  `task.complete` event from D-3) call into one helper.

**W4. Audit every task mutation — six sites (~50 LOC).**
- `handleCreateTask` (`handlers_tasks.go:75-`) writes
  `recordAudit(team, "task.create", "task", id, summary,
  meta={source})`. Source is `ad_hoc` for direct API calls,
  `plan` when called from plan-step materialization,
  `spawn` when called from W2's inline-create path.
- `handleUpdateTask` (`handlers_tasks.go:174-`) splits its
  current single UPDATE into:
  - If `status` changed → `recordAudit(action="task.status",
    meta={from, to, source: 'principal' | 'steward' | 'auto'})`.
    Source resolved from the caller's identity (token kind +
    agent kind).
  - Any other field changed → `recordAudit(action="task.update",
    meta={changed_fields: [...]})`.
- `handleDeleteTask` writes `task.delete`.
- `syncPlanStepTaskStatus` auto-flip (`handlers_tasks.go:314-`)
  writes `task.status` with `source='plan_step'`.
- W3's auto-derive writes `task.status` with `source='spawn'`.

**W5. `NoteKind.todo` → `NoteKind.reminder` (~50 LOC).**
- Rename in `lib/services/notes/notes_db.dart:52`:
  `enum NoteKind { note, reminder }`. Update
  `_kindFromString` / `_kindToString` to read both `todo`
  (legacy on-device data) and `reminder` (new). Write only
  `reminder` going forward.
- Update `note_editor_screen.dart:178-181` ChoiceChip label
  from `'Todo'` to `'Reminder'`.
- One-shot migration in `NotesDb.open`: `UPDATE notes SET
  kind='reminder' WHERE kind='todo'`. Runs once on first
  v1.0.610 open; idempotent.
- Audit any other reference to `NoteKind.todo` in the codebase
  via grep; touch every site.

**W6. Glossary entries (~25 LOC of markdown).**
- Extend the existing `### task` entry in
  `docs/reference/glossary.md`:
  - Update the status vocabulary line to read
    `todo / in_progress / blocked / done / cancelled`.
  - Add a brief note that `cancelled` is human/steward-explicit
    only; auto-derive never produces it.
  - Extend *Distinguish from* line to reference the resolved
    note-kind collision.
- Add `### note` entry: personal scratch, device-local,
  `note` / `reminder` kinds, never synced; cross-link to
  `notes_db.dart`. *Distinguish from:* **task**.
- Add `### todo` entry that is the disambiguation pointer:
  "Two distinct senses in the codebase. The hub primitive is
  **task with status='todo'**; the local primitive (renamed in
  v1.0.610) was `NoteKind.todo`, now `NoteKind.reminder`."

**W7. Lifecycle test scenario (~30 LOC of doc).**
- Add Scenario 30 to `docs/how-to/test-steward-lifecycle.md`:
  project steward spawns a worker with inline `task: {…}` →
  confirm task appears on Tasks tab with status=in_progress,
  assignee=worker, assigner=steward, started_at populated;
  terminate the worker cleanly → task auto-flips to done with
  completed_at and an audit row.
- Extend Scenario 30 with two coda checks: (a) `tasks.delete`
  via MCP drops the row + writes a `task.delete` audit row;
  (b) `tasks.update status='cancelled'` is accepted, written as
  `task.status` audit with `to='cancelled'`, and the row's
  status stays `cancelled` across a subsequent agent terminal
  transition (auto-derive does NOT overwrite it).

### 2.3 Acceptance

- `agents.spawn` with inline `task` creates a tasks row in the
  same transaction; mutual exclusion with `task_id` returns 400.
- `agents.spawn task_id=<terminal>` returns 409 when the linked
  task is `done` or `cancelled`; `blocked` is allowed.
- Principal-direct `tasks.create` or `agents.spawn` with no
  parent_agent_id stores `created_by_id = NULL`; mobile renders
  "you" when the viewer is the principal, "unknown" otherwise.
- `tasks.delete` MCP tool drops the row and writes an audit row.
- `tasks.update status='cancelled'` is accepted; `cancelled` is
  terminal (auto-derive doesn't touch it).
- Auto-derive transitions on agent terminal status flip the
  most-recent-spawn's linked task and emit audit. Older spawns
  for the same task stay in `agent_spawns` but don't drive
  status.
- Steward-initiated terminate auto-derives task to `done` (per
  D-3); cancel-on-stop requires an explicit
  `tasks.update status='cancelled'` follow-up.
- `audit_events` shows one row per task lifecycle event,
  including the auto-flips from plan-step sync and the spawn
  path.
- Mobile note editor shows "Reminder" instead of "Todo"; existing
  device notes are migrated on first open.
- Glossary lints clean (`lint-glossary.sh`).
- Lifecycle test Scenario 30 walks the happy path.

---

## 3. Phase 2 — mobile

### 3.1 Goal

The Tasks tab renders the assignee + assigner + relative time on
every tile. Tapping a task opens a detail screen that surfaces
the linked spawn (agent feed), the linked session (chat), and an
audit timeline filtered to the task.

### 3.2 Wedges

**W8. `_TaskTile` renders the triad (~80 LOC Flutter).**
- Edit `lib/screens/projects/project_detail_screen.dart:960-1065`.
- Add an attribution row beneath the title/preview block:
  - Assignee chip: handle from `task['assignee_id']` resolved
    against `hub.agents` → handle + colored status pip
    (running=green, idle=cyan, blocked=red, terminated=muted).
  - Assigner line: small "by @x" using `task['created_by_id']`.
  - Relative timestamp: `started_at` → "started 3m ago" /
    `completed_at` → "done 1h ago" / neither → no timestamp.
- Preserve the priority dot + plan-icon (no regression).
- Information density stays within the current vertical budget
  — attribution replaces a previously-empty row of chrome.
- `cancelled` rendering: title gets a strikethrough +
  `textMuted` color; the time line reads "cancelled 1h ago"
  using `updated_at` since `completed_at` is unset for the
  cancelled path. `done` and `blocked` keep their existing
  treatment.

**W9. Task detail screen surfaces linked work (~120 LOC Flutter).**
- Extend `lib/screens/projects/task_detail_screen.dart`:
  - Header block: title + body + assignee + assigner + time.
  - "Doing this work" pane: linked spawn → agent feed embedded
    (same widget as `SessionChatScreen`'s `AgentFeed` with
    sessionId scoped). If no linked spawn, show "ad-hoc" hint.
  - "Conversation" pane: linked session (via spawn → session
    join) → tap opens `SessionChatScreen`.
  - "History" pane: audit timeline filtered to
    `target_kind='task' AND target_id=<id>` via existing
    `auditEventsProvider`.
- Tap-handlers preserve the v1.0.499 redesign principles
  (one canonical chat surface, no duplicate session list of 1).

**W10. List query joins linked agent state (~30 LOC).**
- Hub `handleListTasks` (`handlers_tasks.go:112-`) extends its
  SELECT with two LEFT JOINs against `agents`:
  - On `tasks.assignee_id` → returns `assignee_handle` +
    `assignee_status` (running / idle / blocked / terminated).
  - On `tasks.created_by_id` → returns `assigner_handle`.
- Both joins are LEFT (assignee or assigner may be NULL per
  ADR-029 D-2 principal-direct path).
- Mobile reads these denormalized fields directly — avoids the
  N+1 lookup against `hub.agents` in `_TaskTile.build`.

**W11. Pull-to-refresh wiring (~20 LOC Flutter).**
- Tasks tab gains pull-to-refresh that invalidates the task
  list provider and `auditEventsProvider`. Same pattern as
  v1.0.603 fix for the Agents tab.

**W12. Lifecycle test scenario (~30 LOC of doc).**
- Add Scenario 31: open project detail → Tasks tab → tap a
  task → confirm task detail shows assignee/assigner/time +
  linked spawn's agent feed + audit timeline.

### 3.3 Acceptance

- Tasks tab tiles render assignee handle + status pip,
  assigner attribution, and relative timestamp.
- Tap → task detail with linked agent feed embedded, link to
  session chat, and filtered audit timeline.
- No N+1 in the list path (denormalized in `handleListTasks`).
- Pull-to-refresh works.
- Lifecycle test Scenario 31 walks the rendered flow.

---

## 4. Open follow-ups (not in this plan)

- **Due dates + notification plumbing.** `tasks.due_at` +
  notification trigger. Builds on v1.0.323+ notification surface.
- **Task templates.** "Spawn-with-task-template" for repeating
  workflows. Could become a fifth template kind alongside
  agent / prompt / plan / project / policy.
- **Multi-assignee.** Pair-on-task. Defer.
- **Personal-task sync.** Promote `NoteKind.reminder` to a
  hub-side personal task primitive. Per `notes_db.dart` header,
  v2 — needs a conflict-resolution story across devices.
- **Labels.** Industry-standard but not pulling weight for an
  agent-native MVP.

## 5. Status forward-links

- ADR: [decisions/029-tasks-as-first-class-primitive.md](../decisions/029-tasks-as-first-class-primitive.md)
- Discussion: [discussions/tasks-as-first-class-primitive.md](../discussions/tasks-as-first-class-primitive.md)
- Related ADRs: ADR-024 (project detail chassis), ADR-025
  (project steward accountability)

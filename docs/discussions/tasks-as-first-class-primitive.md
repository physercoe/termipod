# Tasks as the first-class primitive for steward-dispatched work

> **Type:** discussion
> **Status:** Open (2026-05-16) — opened from the conversation that started "Tasks tab is empty when project steward spawns a worker". D-1 through D-7 ratified in ADR-029 Phase 1 (v1.0.610-alpha). D-8 added post-implementation when the principal asked "the worker doesn't receive the task body — what's the actual info-flow?". Resolves once mobile Phase 2 ships and the rendered notification lands.
> **Audience:** principal · contributors · reviewers
> **Last verified vs code:** v1.0.610-alpha

**TL;DR.** When the project steward spawns a worker today, the
worker shows in the Agents tab but the Tasks tab stays empty —
the spawn path never touches `tasks`. The schema already carries
`assignee_id` and `created_by_id` (the assigner), but no spawn /
session / attention edges to make the task surface the
operational dashboard. Plus task mutations are entirely silent
(zero audit rows). Plus "todo" is overloaded: `tasks.status='todo'`
on the hub vs `NoteKind.todo` in the local per-device sqflite
note store. This doc records the first-principles review of what
a task primitive needs to carry, the industry-practice
comparators, the four concrete gaps in the current design, and
the design options for closing each.

---

## 1. Why this came up

The principal asked the project steward to dispatch a worker for
a task on a specific project. The steward called
`agents.spawn` — the worker appeared on the Agents tab — but the
Tasks tab stayed empty. The principal then asked: tasks should
carry assignee and assigner, not just progress and priority.

Tracing the code surfaced four interlocking gaps, not one:

1. **Spawn ↔ task linkage is missing.** `agents.spawn` never
   touches `tasks`. Worker exists, task row doesn't.
2. **Mobile doesn't render assignee/assigner.** Both columns
   already exist in the schema; the `_TaskTile` widget just
   doesn't read them.
3. **Task mutations are not audited.** Zero `recordAudit(…)`
   calls in `handlers_tasks.go`. The activity feed has no record
   of task lifecycle.
4. **"Todo" is overloaded.** Two distinct primitives share the
   word: a project-bound `tasks.status='todo'` on the hub, and a
   local-only `NoteKind.todo` on the device. No glossary entry
   distinguishes them.

This doc walks each, with the industry-practice frame.

---

## 2. First principles — what a task primitive must carry

A task is "a unit of intent that someone owns to completion."
That definition forces three orthogonal axes:

1. **Identity / structure** — what is this, scoped to what,
   related to what.
   - title, body, project, parent_task, subtasks, milestone,
     plan_step, labels
2. **Ownership / authorship** — who decided this, who's doing
   it, who's watching.
   - assigner (`created_by`), assignee, observers
3. **State / time / outcome** — lifecycle phase, when each phase
   happened, what came out.
   - status, priority, created_at, started_at, due_at,
     completed_at, result_summary, blockers

Anything beyond these three axes (labels, observers, custom
fields, due-date reminders) is product polish. The triad is the
minimum schema that lets a task system carry intent through to
completion with an auditable trail.

### What "agent-native" adds

A normal task system stops at the triad above and lets humans
flip status manually. An agent-native system has a fourth axis:

4. **Linkage to the work in flight** — what spawn / session /
   attention is doing the work right now.

This is the load-bearing delta. Without it, "Tasks" and "Agents"
are two unconnected lists rendering the same underlying work
twice (or — as in our bug — once and missing once). With it,
Tasks becomes the canonical project view ("what we said we'd do
and where each piece stands") and Agents becomes a process-level
drill-down.

---

## 3. Industry-practice review

The triad above is the consensus convergence point. Where tools
differ is the trade-off between rigidity and flexibility:

| Tool | Shape | Key design choice |
|---|---|---|
| **Linear** | Strong state machine (Backlog → Todo → In Progress → Done → Canceled), single assignee, auto-generated unique ID per issue | **Activity log is first-class** — every state change, comment, link is timestamped + attributed. Tasks-without-history is not a valid state. |
| **Jira** | Extensible (custom fields, multi-assignee plugins, workflow rules) | Heavy; lets enterprises model anything but burns ergonomics. We don't need this. |
| **GitHub Issues** | Minimal: assignees (multiple), open/closed, labels. Closure is linked via PRs (`Closes #N`) | **Closure links to the work product**, not to a manual flip. Closest to what an agent-native system wants. |
| **Asana** | Section-based, multi-assignee, dependencies | Heavy. Section organization is a UX concern we can defer. |

For our system:

- **Linear's activity log** maps cleanly to our `audit_events`
  table — but we don't write task rows to it today. Closing that
  gap turns the activity feed into a real task history.
- **GitHub's "closure links to work product"** is exactly what
  the agent-native triad #4 above gives us: the spawn / session
  IS the work product, and the task's terminal state derives
  from it.
- **Linear's single-assignee + auto-ID** is the right MVP
  shape. Multi-assignee and labels are post-MVP polish.

---

## 4. Current design — where each axis lands

Schema today (`hub/migrations/0001_initial.up.sql:139-153` +
`0020_tasks_plan_step_id` + `0021_tasks_priority`):

| Axis | Have | Missing | Verdict |
|---|---|---|---|
| Identity | id, project_id, parent_task_id, title, body_md, milestone_id, plan_step_id | labels | Labels are nice-to-have; defer |
| Ownership | assignee_id, **created_by_id** (the assigner) | observers | Both load-bearing fields exist; mobile just doesn't render them |
| State/time | status (todo/in_progress/done), priority (low/med/high/urgent), created_at, updated_at | **started_at**, **completed_at**, due_at, **result_summary** | Started/completed are required for the auto-derive story; result_summary is the GitHub-issue-closure analog |
| Linkage | parent_task_id, plan_step_id | **spawn_id**, **session_id**, attention_id | The agent-native delta; without it the Tasks tab is decorative |

Bold = on the critical path for closing the principal's bug.

MCP surface (`hub/internal/hubmcpserver/tools.go:712-805`):

- `tasks.list`, `tasks.create`, `tasks.get`, `tasks.update`
  exist and accept the existing schema columns.
- **`agents.spawn` has no task field at all.** `tools.go:409-424`
  takes child_handle, kind, spawn_spec_yaml, host_id,
  parent_agent_id, worktree_path, budget_cents, mode,
  project_id — but nothing that points at a task. So even if a
  steward knew to set up the task first, there's no MCP edge to
  attach the spawn to it.

Audit (`hub/internal/server/handlers_tasks.go`):

- **Zero `recordAudit` calls.** `handlers_projects.go` has 4
  (`project.create`, `project.phase_set`, `project.archive`,
  `project.update`). Tasks: silent.
- Worse: `syncPlanStepTaskStatus` (`handlers_tasks.go:314-328`)
  auto-flips task status when a plan-step transitions. The flip
  is doubly silent (no audit, no attribution), so the principal
  sees status change in the UI with no "who" answer.

---

## 5. The "todo" glossary collision

Two distinct primitives share the word in the codebase today:

| Concept | Lives | Storage | Sync? | Visibility |
|---|---|---|---|---|
| **Project task with `status='todo'`** | `tasks` table on hub | hub SQLite | Yes — team-shared | Project Tasks tab |
| **Personal Note with `kind='todo'`** | `lib/services/notes/notes_db.dart` | local sqflite, per-device | **No** — explicitly device-only (file header line 8: "Sync to the hub is intentionally out of scope for v1") | Me-page note editor only |

A third surface — `_UrgentTasksSection`
(`me_screen.dart:729-`) is a *view* onto hub tasks, not separate
storage. Reads from `urgentTasksProvider`.

Why this matters:

- An agent reading "todo" in logs / docs / chat must resolve
  *which* primitive is meant. Without a glossary entry, the only
  cue is context — which is fragile and bug-producing.
- The two lifecycles are fundamentally different: task = shared
  team work, note = personal reminder. UI work that conflates
  them ("show all my todos") risks merging into a useless list.
- Future sync of personal notes (the file comment leaves this
  open for v2) would force the name collision into a deeper
  data model decision.

Resolution choices:

- **R1. Rename `NoteKind.todo` → `NoteKind.reminder`.**
  Concrete naming break; one file changes (`notes_db.dart`),
  one screen string changes (`note_editor_screen.dart`).
  Eliminates the collision at source. **Recommended.**
- **R2. Keep both names; add a glossary entry pinning them.**
  Cheaper but the next reader still trips. Accept only if R1
  is too disruptive.
- **R3. Promote personal notes-with-`done`-flag to a "personal
  task" first-class primitive synced to the hub.** Out of scope
  for MVP — file comment explicitly defers this.

Either R1 or R2 gets a glossary entry. R1 is cleaner.

---

## 6. Design choices to close the four gaps

### 6.1 Spawn ↔ task linkage — three patterns

When the project steward spawns a worker to do something, a task
row should always exist for that work. Three patterns:

- **A1. Two-call (Linear/Jira shape).** Steward calls
  `tasks.create` then `agents.spawn task_id=...`. Pros: clean
  separation, task lifecycle independent. Cons: two MCP calls;
  stewards forget step 1; common-case ergonomics suffer.
- **A2. Spawn auto-creates the task.** `agents.spawn` gains
  inline `task: {title, body_md, priority}`; hub creates the
  task row + spawn row in one transaction; stamps
  `task.assignee_id = new_agent.id`,
  `task.created_by_id = parent_agent_id`,
  `task.status = 'in_progress'`,
  `task.started_at = now`. Pros: one call. Cons: tasks-from-spawn
  vs. tasks-from-UI are subtly different shapes; risk of
  taskboard noise.
- **A3. Both — A2 is sugar for A1.** Spawn accepts EITHER
  `task_id` (use existing) OR `task: {...}` (inline-create);
  default (neither) = ad-hoc fire-and-forget with no task row.
  Pros: clean canonical path + ergonomics + escape hatch for
  quick probes. Cons: slight MCP surface bloat.

**Recommended: A3.** Mirrors how `gh issue create` works
(independent) and `gh pr create --linked-issue` works (linked).
Preserves the ad-hoc fire-and-forget for steward exploration.

### 6.2 Status auto-derivation

Status transitions should stop being a manual flip:

- spawn created → `status='in_progress'`, `started_at=now`
- agent terminates clean → `status='done'`, `completed_at=now`
- agent crashes / failed → `status='blocked'`
- agent emits a `task.complete` event → optional explicit
  completion with `result_summary`
- steward / principal calls `tasks.update status=done` →
  manual override always wins (the principal might mark "done
  by inspection")

Plan-step linkage (`syncPlanStepTaskStatus`) is the precedent.
We generalize it to spawn linkage and emit audit on each flip.

### 6.3 Audit on every task mutation

Five sites need `recordAudit`:

- `tasks.create` → `action=task.create`
- `tasks.update` (status flip) → `action=task.status`
- `tasks.update` (other fields) → `action=task.update`
- `tasks.delete` → `action=task.delete`
- `syncPlanStepTaskStatus` auto-flip → `action=task.status`
  with `meta.source='plan_step'`

Spawn-side auto-create (per §6.1) also writes
`action=task.create` with `meta.source='spawn'`.

Now the activity feed has full task history with attribution.

### 6.4 Mobile renders the triad

Per-tile content (`_TaskTile` at
`project_detail_screen.dart:960-1065`) gains:

- **Assignee handle + status pip** (running/idle/blocked color)
- **Assigner handle** (smaller, "by @x")
- **Started / completed timestamp** (relative, "started 3m ago")

Tap → task detail with linked spawn (agent feed) + linked
session (chat) + audit timeline. Tasks tab becomes the
operational dashboard rather than a checklist.

### 6.5 Information-flow edges (added post-Phase-1, 2026-05-16)

Once Phase 1 shipped the schema + audit + auto-derive, the principal
asked: *"the steward spawns a worker for a task and the worker doesn't
receive anything — what's the actual flow?"* Tracing it revealed the
ADR locked the **state** of a task but left the **edges** (how the
task content reaches the worker, how completion reaches the steward)
underspecified. Both edges existed only as conventions on top of
`a2a.invoke`, not as guaranteed properties of the linkage.

**The down-edge (steward → worker) has two channels, not one.**
Engines like claude-code distinguish:
- *Standing context* — files read at startup as system-prompt-like
  references (CLAUDE.md). The worker can re-read these any time.
- *Turn-1 input* — a user message that triggers the first turn.
  Without one, the worker boots, reads CLAUDE.md, and **waits**.

Putting `body_md` only in CLAUDE.md gives the worker context but no
trigger; putting it only in a posted input gives a trigger but no
re-readable reference. Both channels ship together: a `## Task`
section in CLAUDE.md (standing) + a `producer='user' kind='input.text'`
event injected right after the spawn commits (trigger).

**The up-edge (worker → steward) is a system event, not A2A.**
Considered three shapes:

1. **Worker calls `a2a.invoke @parent.steward` manually** — current
   convention. Easy to forget; not enforced. Workers without
   discoverable assigner cards (rare but possible) can't deliver.
2. **Hub auto-fires A2A** to the assigner's card. Cleaner
   architecturally but requires a sender identity. Auto-derive on
   agent terminate has no sender (worker is dead, hub has no
   agent_id); A2A would need a synthetic system-actor.
3. **Hub injects a `producer='system'` event** into the assigner's
   session, same wire shape as `system.mode_changed` and the
   `agents.fanout` first-input. No card lookup, no synthetic sender,
   renders inline in the existing chat surface.

Option 3 was the choice. A2A remains the path for ad-hoc
mid-conversation back-channel; the lifecycle edge is system-driven.

**The worker close-out verb.** `tasks.update` already covered the
mechanics — but a worker calling it as a close-out has to remember
to set `status='done'` AND `result_summary='...'` together. Adding
`tasks.complete` is sugar: one verb, one purpose, with a description
that steers workers to it. `tasks.update` stays for everything else
(partial edits, blocked / cancelled, re-opens).

**Why not also auto-fire on `task.create` or `in_progress`.** The
assigner just created the task or just spawned the worker — they
know. The notification edge is for transitions the assigner cannot
infer from its own actions: terminal flips.

These are D-8 in the ADR; the wedges are W2.6 (CLAUDE.md), W2.7
(first input), W2.8 (`tasks.complete` + `result_summary`), W2.9
(`task.notify` event).

---

## 7. Resolved decisions — summary

These flip from "discussion" to "locked" once ADR-029 is
Accepted. Bullets here pre-mirror the ADR-029 D-N's.

- **D-1.** Tasks are the first-class primitive for steward-
  dispatched work. Every spawn that represents a unit of project
  work either creates or links a task; ad-hoc fire-and-forget
  spawns remain valid but explicit.
- **D-2.** A3 shape: `agents.spawn` accepts `task_id` (existing)
  OR `task` (inline-create). Hub creates the spawn↔task edge in
  one transaction. **`created_by_id` is NULL when caller is
  principal** (mobile renders "you" / "unknown"); **spawn against
  a terminal task (`done` / `cancelled`) returns 409** with a
  reopen-first hint.
- **D-3.** Status auto-derives from linked spawn/agent lifecycle;
  manual override always wins. **Flip-on-spawn** (not
  flip-on-running) for determinism. **Most-recent-spawn drives**
  the task status when a task has multiple linked spawns.
  **`terminated` → `done` regardless of cause** — steward who
  wants `cancelled` instead calls `tasks.update` after the
  terminate.
- **D-4.** Audit every task mutation (create, status, update,
  delete) and every auto-flip (plan-step sync, spawn-side
  derivation).
- **D-5.** Rename `NoteKind.todo` → `NoteKind.reminder` and add
  a glossary entry pinning the two senses. Eliminate the
  collision at source.
- **D-6.** Mobile `_TaskTile` renders the triad (assignee +
  assigner + time); task detail screen surfaces linked
  spawn/session/audit.
- **D-7.** MCP lifecycle is rounded out — add `tasks.delete`
  wrapper (REST exists, MCP missing) and an explicit
  `cancelled` terminal status (auto-derive never produces it).
  Schema needs no migration (the status column has no CHECK
  constraint); the addition is docs/glossary/mobile rendering.
- **D-8.** Information-flow edges are first-class. **Down**: task
  body lands in CLAUDE.md (`## Task`) as standing context AND in a
  `producer='user'` event as the first-turn trigger. **Up**:
  terminal status flips post a `kind='task.notify' producer='system'`
  event into the assigner's active session — no polling required.
  Worker close-out is `tasks.complete summary='...'` (sugar over
  `tasks.update`), populating the previously-unwritten
  `result_summary` column. Added post-Phase-1 after the
  worker-doesn't-receive-anything gap surfaced; wedges W2.6–W2.9.

---

## 8. Open questions parked for later

- **Multi-assignee.** Some workflows want pair-on-task. Defer
  until a real use case (Linear ships single-assignee MVP for
  the same reason).
- **Labels.** Industry-standard but pay no rent in an
  agent-native system at MVP. Defer.
- **Due dates + reminders.** The notification surface exists
  (v1.0.323+); plumbing due_at into it is a follow-up wedge.
- **Task templates.** "Spawn-with-task-template" for repeating
  workflows (nightly probe, weekly review). Out of scope; could
  become a fifth template kind alongside agent / prompt / plan /
  project / policy.
- **Personal-task sync (R3 in §5).** Per the
  `notes_db.dart` header, deferred until v2 — needs a
  conflict-resolution story across devices.

---

## 9. Status — links forward

- ADR: [decisions/029-tasks-as-first-class-primitive.md](../decisions/029-tasks-as-first-class-primitive.md) (Proposed; D-1–D-7 locked)
- Plan: [plans/tasks-first-class-rollout.md](../plans/tasks-first-class-rollout.md) (Proposed, 2 phases)
- This discussion flips to Resolved once Phase 1 ships and the
  glossary entries land.

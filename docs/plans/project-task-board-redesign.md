# Project & task board redesign — master-detail desktop, in-review lifecycle, agent-aware DnD

> **Type:** plan
> **Status:** In progress — **W1 SHIPPED** (master-detail board, rich cards,
> stretchy columns, resizable split + tri-pane transcript preview per §6.4);
> **W2 SHIPPED** (`in_review` lifecycle end-to-end: hub derivation + clients +
> unknown-status fallback + ADR-029 D-8); **W3 SHIPPED** (agent-aware DnD +
> assign-on-drop + filters/search/view-tabs, desktop); W4–W5 open. Decisions §6.
> **Audience:** contributors, maintainer
> **Last verified vs code:** W1+W2 shipped 2026-07-23; W3 shipped 2026-07-24

**TL;DR.** The desktop Projects area under-serves widescreen: the tasks
kanban is five fixed 220px columns with dead space to their right, task cards
show two fields, and the detail modal lacks everything mobile already shows
(assign action, transcript link, result summary, timestamps, audit). The hub
data model is *ahead* of the reference products — task statuses are
agent-derived via `agent_spawns.task_id`, and projects carry phase gates
(deliverables + acceptance criteria + auto-advance) — so this is a rendering
gap, not a model gap. This plan borrows Vibe Kanban's task-execution loop
(status board, **In-review lifecycle**, master-detail, attempts) and the
cloud-agent review flow, in five wedges. Companion to the agent-transcript
redesign plan (`docs/plans/agent-transcript-redesign.md`, merged `c97c522c`
— its P5 "Session Changes rollup" future item lands here as W5).

---

## 1. Background — the gap, honestly stated

**Mobile is ahead of desktop on this surface** (unusual for this codebase):

- Mobile project list cards: status dot, attention badge, phase pill,
  open-AC chip, progress bar, children attention rollups
  (`lib/screens/projects/projects_screen.dart:1108-1248`).
- Mobile project detail: 5-page PageView (Overview/Activity/Agents/Tasks/
  Files, `project_detail_screen.dart:321-337`); tasks view = status-chip
  filters + priority menu + grouped-by-status sections (`:355-732`); task
  tile = priority dot, live assignee pip, attribution, relative time
  (`:734-902`); task detail = status/priority pickers, **worker-session Open
  button**, audit timeline (`task_detail_screen.dart:198-275`).

**Desktop is the immature one** (`desktop/src/surfaces/`):

- `TasksTab` (`ProjectBoard.tsx:537-593`): 5 columns **fixed at 220px**
  (`05-transcript-boards.css:1046-1051`) inside `min-width: min-content` —
  on a wide monitor the columns never stretch and the right half of the
  region is dead space. Cards render **title + assignee_handle only**
  (`:569-583`). No DnD, no filters, no search, no sort.
- `TaskDetail` modal (`TaskDetail.tsx:19-107`): title, body, status/priority
  pickers, read-only assignee line. **No assign-agent action, no
  spawn/transcript link, no result_summary, no timestamps, no audit trail** —
  all of which mobile has.
- Left nav (`ProjectsSurface.tsx:61-97`): flat rows of `dot + name + phase`.
  No attention badges, no progress, no children rollups.
- Everything lives in one center region of `MissionLayout`; the only
  project-adjacent second pane is the *global* AttentionDock. No
  master-detail anywhere in the Projects area.

**But the hub model is secretly ahead of the reference products:**

- Task statuses `todo|in_progress|blocked|done|cancelled` are **agent-derived**
  (`hub/internal/server/handlers_tasks.go:24-31`,
  `deriveTaskStatusFromAgent` in `handlers_agents.go`): spawn flips
  todo→in_progress; agent crash/failed→blocked; agent terminated **splits on
  `result_summary`** — with a summary → done, without → **cancelled** (the
  v1.0.619 rule: an abandoned task must not look completed); cancelled is
  human-only, and `blocked`/`cancelled` are **never overwritten** by
  auto-derive (v1.0.628: the worker's verdict outlives operator cleanup).
  Agent link = `agent_spawns.task_id` (1:N) with denormalized
  `assignee_handle/status`, `started_at`, `completed_at`, `result_summary`
  (ADR-029 W10).
- Projects carry **phase gates** — deliverables with ratify/send-back and
  acceptance criteria with auto phase-advance (ADR-044,
  `adaptive-project-lifecycle.md`) — a completion model richer than anything
  below.

## 2. Reference designs

### 2.1. Vibe Kanban (BloopAI/vibe-kanban, 27.5k★, open source)

> Note: the project announced sunset upstream; the automagik/forge fork
> carries it on. We borrow design, not code — unaffected.

![Vibe Kanban board + task detail panel](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/vibe-kanban/board-master-detail.png)

- **Status board**: `To do / In progress / In review / Done` columns; tabs
  `Active | All | Backlog | Cancelled`; per-column quick-add; cards show ID,
  title, description snippet, assignee, **live attempt indicator**, age.
- **In-review lifecycle**: a task **auto-moves to In review when the agent
  completes** — work is done *when reviewed*, not when the agent stops
  (`docs/core-features/reviewing-code-changes.mdx`).
- **Master-detail**: selecting a card slides open a **right detail panel**
  (branch, setup script, agent conversation, actions) — the board stays
  visible. No modal.
- **Attempts** (`docs/core-features/new-task-attempts.mdx`): one task → many
  agent sessions; new attempt for a fresh restart, a *different agent*, or a
  different base.
- **Review loop**: diff interface with line-specific comments sent back to
  the agent as feedback.
- **Widescreen workspace**: transcript | diff | git+notes rail + composer
  visible at once (below) — the model for what a desktop workbench should
  look like.

![Vibe Kanban widescreen workspace](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/vibe-kanban/workspace-widescreen.png)

### 2.2. Cloud-agent task managers (Claude Code on the web, Codex cloud)

Both shape agent work as *prompt → run → **needs-review (diff+logs)** →
shipped* — the same "done when reviewed" lifecycle with a diff-first Review
surface (Claude: diff view → comments → PR, auto-fix on CI failure; Codex:
terminal logs + test output as verifiable evidence). This reinforces W2/W5.

### 2.3. What NOT to borrow

- VK's workspace-as-sandbox (branch + dev server + preview browser):
  termipod's hosts/workdirs are real machines — its model is stronger.
- VK's GitHub-PR-centric completion: termipod's phase gates
  (deliverables/criteria/auto-advance) are the richer completion model —
  keep them as the differentiator.
- A mobile kanban board: mobile's list + status-group sections is the right
  phone pattern (VK has no mobile board either).

## 3. Goals / non-goals

### Goals

- G1. Desktop uses widescreen: master-detail board, stretchy columns, no dead
  space.
- G2. Task cards/detail reach mobile parity and then some: live assignee
  status, transcript link, result summary, timestamps, audit.
- G3. **In-review lifecycle**: agent termination → `in_review`; human accept
  → `done`. Done means reviewed.
- G4. DnD that knows the agent semantics (drag to in_progress = assign/spawn;
  agent-owned states read-only).
- G5. Attempts as first-class UX on the existing 1:N spawn link.

### Non-goals

- Changing the phase-gate model (deliverables/criteria/auto-advance) — it
  stays the differentiator.
- GitHub-PR completion flows, sandbox/worktree isolation, dev-server preview.
- Mobile information-architecture changes (list pattern stays).
- Hub schema changes beyond the `in_review` status (W2) — everything else
  renders existing fields.

## 4. The design

**Status model (W2).** `todo | in_progress | blocked | in_review | done |
cancelled`. Auto-derivation updated: agent terminated **with a
`result_summary`** → `in_review` (was `done`); terminated **without** one
stays → `cancelled` **unchanged** (v1.0.619 — an abandoned task has nothing
to review and must not enter the review queue); crash/failed → `blocked`
unchanged; and the never-overwrite guards for `blocked`/`cancelled`
(v1.0.628) must survive the change intact. Human accept → `done`
(optionally with note); send-back → `in_progress` with a note posted to the
assignee session. `cancelled` stays human-only. Board columns: the five
active states; cancelled moves to a tab filter (VK's Active|All|Cancelled
pattern).

**Desktop master-detail (W1).** `TasksTab` becomes a split: board left
(~45%, columns `flex: 1 1 200px` — stretch to fill, no fixed 220px), selected
task's **detail panel right** (replaces the `TaskDetail` modal on ≥1100px;
modal stays below). Detail panel content = mobile parity+: status/priority
pickers, assignee row with live status pip + **Open transcript** (assignee
session), `result_summary`, started/completed timestamps, audit timeline,
markdown body. Ultrawide (≥1600px): optional third region previewing the
assignee's live transcript (VK workspace layout).

**Rich cards (W1).** Priority dot, title, 1-line body, live assignee pip +
handle, result-summary snippet when present, relative time, blocked
indicator, cancelled strikethrough — the mobile tile's content, desktop
density.

**DnD with agent semantics (W3).** Drag between columns PATCHes status
*only where the transition is human-owned*: into `in_progress` opens the
assign/spawn sheet (agent picker, then spawn with `task_id` — the status
flips via the existing derivation, not the PATCH); `in_review`→`done` =
accept; `blocked` cards are drag-disabled (agent-owned); cancelled is a drop
target with confirm. Filters (status chips + priority menu) + search box in
the board toolbar — mobile parity.

**Attempts (W4).** "New attempt" action on a task (overflow + detail panel):
opens the assign sheet with agent picker; each spawn on the task = an
attempt, listed in the detail panel's audit section with agent/result/age.
Read-only framing over the existing 1:N `agent_spawns.task_id`.

**Left nav parity (W4).** Desktop project rows gain: attention badge, phase
pill, progress bar (the phase-weighted `/v1/insights` metric —
`(phases_done + current_phase_AC_ratio) / phases_total`, NOT a task count),
children attention rollups — the mobile `_ProjectListCard` content, same
hub payload.

**Mobile deltas (W2/W4 only).** `in_review` in the status chips + task tile;
accept/send-back actions on the task detail (mirroring the deliverable
send-back UX it already has); "New attempt" in the overflow. List pattern
unchanged.

## 5. Wedges

- **W1 — Desktop master-detail + rich cards + stretchy columns. SHIPPED
  (direct-to-main).** Surfaces: `ProjectBoard.tsx` (TasksTab split + `TaskCard`
  + `useMinWidth`), `TaskDetail.tsx` (shared `TaskDetailBody` for modal + panel,
  full content + `relTime`/`pipClass`/`firstLine` helpers),
  `05-transcript-boards.css` (column flex `1 1 200px`, card styles, split/panel/
  transcript). Columns stretch (dropped `min-width:min-content`); detail opens
  inline ≥1100px, modal below. **§6.4 done:** the split is user-resizable
  (`usePanelWidth` + `ResizeHandle`, mirroring MissionLayout's right-dock rail,
  persisted at `termipod.taskboard.panelW`), and a **tri-pane live-transcript
  preview** of the assignee session (`AgentTranscript`) engages automatically at
  ≥1600px when the task has an assignee. Desktop-only; no hub change. Verified:
  tsc clean, `lint-desktop-tokens` clean (65 baseline), vite build green.
  Device-test (widescreen ≥1600px, resize drag, transcript height) is the
  director's.
- **W2 — `in_review` lifecycle end-to-end. SHIPPED (direct-to-main).** No
  schema migration (task status has no CHECK constraint, `handlers_tasks.go:24`;
  vocabulary lives in handlers). Landed clients-first per the rollout order:
  - **Clients (understand `in_review` + unknown fallback, deploy first):**
    desktop `COLUMNS`/`STATUSES` + `kanban.in_review` (en+zh) + an
    unknown-status trailing-column fallback (any status the client doesn't
    know renders in its own column, not dropped — closes the count-vs-list
    #61 class for every status); accept/send-back buttons on the detail panel.
    Mobile: `taskStatusLabel` + `taskStatusInReview` ARB (en+zh), filter
    chips, grouped `order` + append-unknown bucket, `_StateRow._statuses`
    picker. (Mobile accept/send-back ride the existing status picker — a
    dedicated button pair is a polish follow-up.)
  - **Hub (flip derivation):** `deriveTaskStatusFromAgent` — terminated **with**
    `result_summary` → `in_review` (was `done`); **without** → `cancelled`
    unchanged; crash/failed → `blocked`; `in_review` **added to the
    never-overwrite guard** (a later abandoned/crashed attempt can't erase a
    pending-review verdict). `completed_at` stamped on `in_review`, cleared on
    send-back/reopen. `notifyTaskAssigner` + `taskOutcomeInputBody` gain
    `in_review` (steward woken to review). `deriveDigestOutcome`
    (`digest_store.go`) query + ordering gain `in_review` (finished work no
    longer vanishes from digests). Spawn gate `handlers_agents.go:1351` already
    allows `in_review` (only `done`/`cancelled` reject) — verified, no change.
    accept (→`done`)/send-back (→`in_progress`) ride the REST PATCH path (any
    status accepted). Docs: **ADR-029 D-8** records the semantics.
  - **Deferred to a follow-up:** the `proposePermittedTaskStatuses` send-back
    extension — send-back → `in_progress` is non-terminal and would mis-stamp
    `completed_at` through the propose apply path (which assumes terminal);
    governed stewards send back via `tasks.update`, and the UI via REST PATCH.
    The send-back **note into the assignee session** is W5's feedback loop.
  - Verified: hub `go test ./internal/server/` green; desktop tsc + tokens +
    vite build green; mobile CI-verified (no local Flutter).
- **W3 — DnD-with-assign + filters/search (desktop). SHIPPED
  (direct-to-main).** Desktop-only; no hub change (the spawn `task_id` field
  already existed, `handlers_agents.go:714`). What landed:
  - **Agent-aware DnD** on the kanban. Cards are HTML5-`draggable` except
    `blocked` (agent-owned — the crash/failed verdict isn't human-re-routable,
    §6.3); columns are drop targets with an accent drag-over affordance.
    `onDragEnd` clears drag state on cancel (Esc / drop outside) so no
    highlight lingers.
  - **Drop routing per the derivation table** (`onDropStatus`): into
    `in_progress` → **never a raw PATCH**, opens the assign picker and the
    spawn flips the status via hub derivation (§6.3 — explicit beats magic);
    into `cancelled` → `window.confirm` then PATCH (terminal + human-only);
    `todo`/`in_review`/`done`/unknown → plain human PATCH (dropping an
    `in_review` card on `done` = accept). Same-column drop is a no-op.
  - **Assign sheet reuse:** `AgentSpawn` gained optional `taskId` / `taskTitle`
    / `presetProjectId` / `onSpawned` props — in assign mode it sends `task_id`
    (mutually exclusive with the inline `task`), locks the project, and shows
    the task title read-only. The desktop client's `spawnAgent` body gained
    `task_id`. Also reachable non-DnD via an **Assign agent** action on an
    unassigned task's detail panel (`TaskDetailBody` `onAssign`).
  - **Board toolbar (mobile parity):** `Active | All | Cancelled` view tabs
    (cancelled hides behind its own tab, VK pattern), a title+body search box,
    and a priority filter menu — all pure client-side filters over the polled
    task list; the unknown-status fallback columns ride alongside active/all.
  - Verified: tsc clean, `lint-desktop-tokens` clean (65 baseline), vite build
    green. DnD device-test (native drag on WebView2, confirm dialogs) is the
    director's.
- **W4 — Attempts framing + left-nav parity (desktop).** Detail panel
  attempts section from spawn history; "New attempt" action; left-nav card
  content port.
- **W5 — Review-feedback loop.** Per-task Changes rollup + line comments →
  composer feedback into the assignee session. **Shared wedge with
  `agent-transcript-redesign.md` P5** — land there or here, once.

Each wedge is its own PR; W1 is independent and can ship first; W2 touches
hub + both clients and should land behind the status migration.

## 6. Decisions (maintainer, 2026-07-23)

1. **`in_review` is a new status**, not a flag on `done` — it matches VK and
   reads better on boards. The touched-surface list is W2's hub bullet
   (derivation, `apply_task_set_status` vocabulary, digest query, spawn
   gate); existing `done` rows stay `done`.
2. **Send-back target**: `in_progress` when the assignee session is alive
   (re-engage with the note), else `todo` for re-assignment.
3. **DnD into `in_progress`**: always open the agent picker — explicit
   beats magic for spawning compute.
4. **Master-detail breakpoints**: user-resizable split (mirror
   MissionLayout's resizable rails); tri-pane engages automatically at
   ≥1600px.
5. **W5 home**: THIS plan carries the review-feedback implementation; the
   transcript plan's P5 "Session Changes rollup" stays a cross-link (it is
   "recorded, not scheduled" there, and merged that way in `c97c522c`).

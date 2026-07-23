# Project & task board redesign — master-detail desktop, in-review lifecycle, agent-aware DnD

> **Type:** plan
> **Status:** Draft — for maintainer review
> **Audience:** contributors, maintainer
> **Last verified vs code:** main @ `c9a247d8`, 2026-07-23

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
redesign plan (`docs/plans/agent-transcript-redesign.md`, in flight as PR
#363 — its P5 review-feedback wedge lands here as W5).

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
  (`hub/internal/server/handlers_tasks.go:24-31`): spawn flips
  todo→in_progress, agent crash→blocked, agent terminated→done, cancelled is
  human-only. Agent link = `agent_spawns.task_id` (1:N) with denormalized
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
cancelled`. Auto-derivation updated: agent terminated → `in_review` (was
`done`); human accept → `done` (optionally with note); send-back →
`in_progress` with a note posted to the assignee session. `cancelled` stays
human-only. Board columns: the five active states; cancelled moves to a tab
filter (VK's Active|All|Cancelled pattern).

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
pill, progress bar (closed/total), children attention rollups — the mobile
`_ProjectListCard` content, same hub payload.

**Mobile deltas (W2/W4 only).** `in_review` in the status chips + task tile;
accept/send-back actions on the task detail (mirroring the deliverable
send-back UX it already has); "New attempt" in the overflow. List pattern
unchanged.

## 5. Wedges

- **W1 — Desktop master-detail + rich cards + stretchy columns.** Surfaces:
  `ProjectBoard.tsx` (TasksTab split), `TaskDetail.tsx` (panel-ize + full
  content), `05-transcript-boards.css` (column flex, card styles). The
  visible widescreen win. Desktop-only; no hub change.
- **W2 — `in_review` lifecycle end-to-end.** Hub: `handlers_tasks.go`
  derivation (terminated→in_review), accept/send-back endpoints or PATCH
  transitions + notes; migration for the new enum value; existing `done`
  tasks untouched. Clients: status pickers, board column, mobile chips +
  detail actions. Docs: ADR-029 semantics update note.
- **W3 — DnD-with-assign + filters/search (desktop).** HTML5 drag events on
  cards/columns; transition guard per the derivation table; assign sheet
  reuse (`AgentSpawn`); toolbar chips + search.
- **W4 — Attempts framing + left-nav parity (desktop).** Detail panel
  attempts section from spawn history; "New attempt" action; left-nav card
  content port.
- **W5 — Review-feedback loop.** Per-task Changes rollup + line comments →
  composer feedback into the assignee session. **Shared wedge with
  `agent-transcript-redesign.md` P5** — land there or here, once.

Each wedge is its own PR; W1 is independent and can ship first; W2 touches
hub + both clients and should land behind the status migration.

## 6. Open questions for the maintainer

1. **`in_review` as a new status vs. a flag on `done`** — a new enum value is
   cleaner for boards/filters but touches the derivation table, mobile
   filters, and every status switch in the codebase; a `reviewed` flag keeps
   the enum stable. Proposal: new status (matches VK, reads better on
   boards), with a migration note for existing `done` rows (they stay done).
2. **Send-back target**: does send-back return the task to `in_progress`
   (re-engaging the same assignee session with the note) or to `todo`
   (re-assignment)? Proposal: `in_progress` when the assignee session is
   alive, else `todo`.
3. **DnD into `in_progress`**: always open the agent picker, or default to
   the project steward with the picker behind long-press/alt-drop? Proposal:
   always open the picker (explicit beats magic for spawning compute).
4. **Master-detail breakpoints**: ≥1100px split / ≥1600px tri-pane — sane
   defaults, or should the split be user-resizable (MissionLayout already has
   resizable rails to mirror)? Proposal: resizable split, tri-pane automatic.
5. **W5 home**: land review-feedback here as W5, or keep it solely in the
   transcript plan's P5 and cross-link? Proposal: one implementation,
   whichever PR lands first carries it.

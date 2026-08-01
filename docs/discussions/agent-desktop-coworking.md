# The agent as first-class desktop co-worker

> **Type:** discussion
> **Status:** Resolved (2026-08-01) — investigation + design across the
> whole desktop. The durable contract is
> [ADR-064](../decisions/064-agent-desktop-coworking-contract.md); the
> build order is
> [agent-desktop-coworking.md](../plans/agent-desktop-coworking.md).
> Supersedes the "AI (later)" bullet of
> [author-agent-assist-and-diagrams.md](author-agent-assist-and-diagrams.md)
> §3.3; adopts
> [desktop-compare-wall-and-decisions.md](../plans/desktop-compare-wall-and-decisions.md)
> as its Compare/Record leg.
> **Audience:** principal · contributors
> **Last verified vs code:** 2026.731 main (`91153552`)

**TL;DR.** The director's ask, in two rounds: first *"the assistant
(kimiweb, Companion) should help the user draw"* across every Author
kind; then *"the AI agent is a first-class user of the TermiPod desktop
like a human user"* across the **local-work tabs** — Read, Author,
Inspect, Compare, Replay, Record — operating on any tab except Terminal
and Settings, with Fleet/Projects staying hub-native distributed work.
The audit below (every tab, every entity, every existing agent path)
shows the desktop is closer than it looks: Read's entities already have
full hub CRUD tools and a reactive renderer; Replay's entities are
hub-complete on REST but have **zero MCP tools**; Author needs the
`author_*` surface designed here; Compare and Record have no entities
yet but a Proposed plan already specifies them; and four of twelve focus
policy rows reserve fields that are never populated — an agent literally
cannot see which paper the user is reading. Resolution: **the agent
operates on the *entities* each tab renders — plus pointing and a new
consented navigation verb — never on the pixels** (ADR-062's "the click
is the user's" survives intact); entity surfaces live on the side that
owns the entity; consent extends the existing `read | annotate | action`
class taxonomy; and every write obeys one like-a-user contract:
validated, live, undoable, attributed, revertible.

---

## 1. The ask and the frame

"First-class user" cannot mean synthetic clicks: ADR-062 D-5 draws the
actuation line deliberately ("the agent directs attention; the click is
the user's" — `uiRefFocus.ts:1-14`), the browser bridge's 8 actuating
tools stop at the webview boundary, and the hub's `desktop_ui_invoke`
classes are observe + annotate only (`mcp_desktop_ui.go:66-108`). What a
human user *actually does* in the local-work tabs decomposes into four
verbs the architecture can grant an agent honestly:

1. **See** what's on screen (focus snapshot, screenshots — shipped, but
   partially blind, §4.1);
2. **Point** at things (`ui_highlight` — shipped);
3. **Go** somewhere / open something (nothing exists on desktop —
   `mobile_navigate` exists for mobile, `native_tools.go` misc block; a
   `ui://…` chip is user-clicked and deliberately never opens a tab,
   `uiRefFocus.ts:71-76`);
4. **Make and change things** — the entities each tab renders
   (documents, references, annotations, comparisons, decision records,
   datasets). This is where the tabs differ, and where the design work
   is.

Fleet and Projects need none of this: agents already participate there
*as hub citizens* — the full `agents_* / projects_* / tasks_* /
deliverables_* / plans_*` catalog — and the desktop is a viewer over the
same rows. Terminal and Settings are excluded by design (§3.8).

## 2. Prior art

**next-ai-draw-io** (Apache-2.0, studied at `6e65394`): chat-driven
draw.io authoring. The borrowable core is its diagram-logic layer — the
LLM emits bare `mxCell` lists (wrapper injected, `lib/utils.ts:326-371`),
ID-based edit ops with cascade (`:498-731`), a 26-rule auto-fixer
(`:985-1632`), validation errors returned as tool errors so the model
self-corrects, current-canvas-XML-as-authoritative-context
(`route.ts:445-459`), 31 vendored shape-library files, and prompt-taught
layout/edge-routing rules (`system-prompts.ts:77-187`). Its
`packages/mcp-server` (an external agent editing the canvas over a local
bridge) is our architecture, minus our consent/audit/scope machinery.

**In-house:** `ui_highlight` is the one store an agent already writes
live, with attribution and TTL (`agentHighlight.ts`); the Read
companion's `insert()` appends agent text to a reference's notes on a
user click (`ReadSurface.tsx:2117-2124`); the hub reference tools can
already draw PDF highlights at exact coordinates
(`native_tools.go:283-356`) — and the pull leg of annotation sync proves
an external store write repaints the PDF instantly
(`annotationSync.ts:151`, `PdfCanvas.tsx:1335-1339`).

## 3. The desktop, tab by tab

Job registry: `workbench.ts:14-73` — fleet, projects, read (J1), author
(J2), debug/Inspect (J3), compare (J5), replay (J8), record (J6),
terminal, settings. Policy table: `ui_policy.ts:155-176`.

### 3.1 Author (J2) — designed in this round

One store (`Doc {id, kind, title, body, spec?}` — `documents.ts:31-42`,
localStorage + optional linked file, **no revision history** `:14-15`):

| kind | built on | reacts to external body write? | undo | malformed write |
|---|---|---|---|---|
| `diagram` | draw.io iframe, `drawio://` (`DiagramEditor.tsx:127`) | **no** — load-once (`:76-99`) | vendor | undefined |
| `canvas` | React Flow + JSON Canvas 1.0 (`state/canvas.ts`) | **no** (`CanvasEditor.tsx:341-345`) | 100-cap board stack (`canvas.ts:287-318`) | read-only lock — safe (`canvas.ts:214-233`) |
| `excalidraw` ("Sketch") | `@excalidraw/excalidraw` | **no** — "uncontrolled after mount" (`ExcalidrawEditor.tsx:81-82`) | vendor | **blank scene** |
| `figure` | CodeMirror + 6-renderer registry (`figures.ts:14`) | **yes** (`FigureEditor.tsx:219-223`) — the model | CM | error strip, last SVG kept |
| `table` | dep-free grid (`TableEditor.tsx:19-24`) | **no** (`:29`) | 50-cap | **silently emptied** (`table.ts:40-50`) |
| `markdown` | CodeMirror/Milkdown | **yes** (`MarkdownEditor.tsx:283-298`) | CM/PM | n/a |

The companion sends structured kinds' *titles only* and registers no
insert for them (`AuthorSurface.tsx:342-366`); `ui_get_focus` exposes
`document.{id,title}` (`ui_policy.ts:163`); the drawio iframe is
invisible to `browser_*` tools. Useful latent primitives:
`applyBoardString` (`CanvasEditor.tsx:598-610`), the held-but-unused
Excalidraw API ref (`:73`), `createHistory` (generic bounded history
over strings).

### 3.2 Read (J1) — hub-complete entities, blind and pull-only client

Entities: references, collections, tags, notes, PDF/EPUB/image
**annotations**, bookmarks, open reader/web/note tabs. State:
`useLibrary` + `useAnnotations` (zustand + localStorage, **fully
reactive** to external writes), attachments' bytes host-local by the
data-ownership law (`handlers_references.go:15-19`).

- **Agent reach today (hub): full CRUD.** `reference_list/get/create/
  update/delete` + `reference_annotation_list/create/update/delete`
  (`native_tools.go:193-356`), worker-eligible, including geometric PDF
  highlights. In-app: the companion `insert()`→notes path (user-clicked).
- **The catch:** hub→desktop is a *manual Sync button* only
  (`ReadSurface.tsx:2195-2219`) with **local-wins** merge
  (`librarySync.ts:8-19`) — an agent's write is invisible until the user
  syncs, and clobbered if the local row is dirty. Notes are whole-field
  replace (an agent update can eat the reader's paragraph). No library
  undo (contrast the 50-deep annotation undo, `annotations.ts:203-212`).
  `Annotation.author` round-trips but is never rendered distinctly.
- **And the agent is blind:** the `read` policy row reserves
  `tabs.*`/`tab.*` but `assembleRawFocus` never fills them — open tabs
  live in component `useState` (`ReadSurface.tsx:1792`), so
  `ui_get_focus` during reading returns `{surface:"read"}` and nothing
  else (`uiContext.ts:29-33`). Bookmarks have no entity and no tool.

### 3.3 Inspect / debug (J3) — all local, read-only, no write paths

Tabs (code/diff/log/model/graph/megraph/modgraph/archgraph —
`inspect.ts:22`) over pinned roots; checkpoints parsed header-only in
main (`ipc/checkpointfile.ts`), arch cards/schematics/FLOPS/VRAM all
**derived in-memory** (`state/checkpoint.ts`, `archSchematic.ts`,
`flops.ts`, `vram.ts`); nothing hub-synced, no file watcher, content
read once per tab activate. There is **no save/write-back path in the
surface at all**. Agent reach: `inspect_tabs[].{kind,path}` +
`inspect.path` via focus; `inspect.selection` is reserved but never
assembled (`ui_policy.ts:305-307`); a `ui://debug?file=` chip focuses an
already-open tab and deliberately never opens one (`uiRefFocus.ts:71-76`,
pinned by test). Pure helpers an agent-facing read could reuse:
`archNodeDetails` (`archSchematic.ts:356`), `classifyArch`
(`checkpoint.ts:642`).

### 3.4 Compare (J5) — no entities yet; the plan exists

196 lines, first cut: project picker → run multi-select → overlaid
metric charts; picks in component `useState` (`CompareSurface.tsx:33-34`),
**zero persistence, zero store, zero agent reach**; the `compare.left/
right` policy fields are reserved, never filled.
[desktop-compare-wall-and-decisions.md](../plans/desktop-compare-wall-and-decisions.md)
(Proposed) already specifies the `compareWall` store, the compare UIRef,
and agent verbs (`runs_list`, `run_metrics`, `run_config_diff`,
`run_provenance`) shipping *inside* the first wedges — "a UIRef is only
a join key if the entity behind it is agent-addressable".

### 3.5 Record (J6) — decision capture, not recording

An ADR-shaped form (title/context/decision/consequences → markdown)
appended to a device-local draft (`RecordSurface.tsx:34`,
`useJsonDraft` → localStorage; **not reactive**, not a store). No hub
entity, no tool, no UIRef. The compare-wall plan's Lane B specifies its
graduation: a hub `records` table with `kind (decision|finding)`,
`status (proposed|accepted|superseded)`, `created_by_kind (user|agent)`,
evidence links, and MCP verbs where **agents create `proposed` records
only — acceptance is the director's click**. That is the cleanest
co-working write model in the repo. Bug found: the policy row reserves
`record.dataset_id` (`ui_policy.ts:171`) — a leftover from an
episode-recording notion of J6 that was never built; the shipped surface
has no dataset.

### 3.6 Replay (J8) — hub-complete entities, zero MCP tools

Hub owns the `datasets` row + folded digest (migration 0068; episodes/
series stay host-proxied, never stored — the data-ownership law); REST
is complete (`server.go:724-738`: list/register/get/patch/delete/
refresh/episodes/series/export). Desktop-local: selection, page offset,
open episode, playback cursor, hidden channels, remote-media routing
(`replayRemoteStore.ts`). **The entire family has no MCP surface** — an
agent cannot list, register, refresh, or read a dataset; the only
indirect touch is `runs_update {dataset_id}`. Focus publishes
`replay.dataset_id` but not the reserved `episode_id`/`cursor`; the
replay UIRef (`ui://replay?dataset_id&project_id&episode_id`) already
resolves on user click (`uiRefFocus.ts:44-64`) — `cursor` parses but has
no consumer.

### 3.7 Fleet + Projects — hub-native; nothing to build here

Both surfaces render hub rows (`useAgents/useHosts/useProjects`) and
publish only ids to focus. Agents already have first-class CRUD across
agents/hosts/projects/tasks/deliverables/criteria/plans/schedules/
documents/reviews (79 authority + 40 native tools, ADR-033 registries).
The desktop co-working design must **not** duplicate any of it.

### 3.8 Terminal + Settings — excluded, and why that is load-bearing

Terminal panes are handles onto the user's own authenticated shells and
PTYs (`terminal/store.ts:1-8`); an agent typing there actuates with the
user's authority — the exact ADR-062 D-5 line — and the sanctioned path
for agent shell work is a spawned agent session. Scrollback is "never
emitted" by policy. Settings/Vault is the consent surface itself: it is
where every agent capability is granted, and its policy row refuses
snapshot fields and capture (`ui_policy.ts:173-175`) — an agent that
could operate Settings could grant itself capabilities. Excluding both
is the invariant the rest of the model stands on.

## 4. The consolidated gaps

1. **Blind focus** — reserved-but-unpopulated fields: Read `tabs.*`,
   `inspect.selection`, `compare.left/right`, `replay.episode_id/
   cursor`, `record.*` (`uiContext.ts:29-33`, `ui_policy.ts:291-319`).
   Root cause: that state lives in component `useState`, not stores.
2. **No navigation verb** — agents can point but never open/go;
   `mobile_navigate` has no desktop twin; UIRef chips are user-only.
3. **Author** — no document tool; load-once editors; destructive
   parses; no rollback (§3.1).
4. **Read** — no push channel; local-wins merge clobbers agent writes;
   whole-field notes replace; no library undo; attribution unrendered;
   bookmarks orphaned.
5. **Replay** — hub tools missing entirely; desktop refetch-only.
6. **Compare/Record** — entities don't exist yet (plan Proposed).
7. **No token-level streaming over MCP** — atomic tool calls; the
   150 ms live-draw effect does not transfer.

## 5. Options considered

**O1 — operate the pixels vs operate the entities.** Synthetic
click/type into the workbench (the browser-tools model extended inward)
would make every surface "operable" at once — and would breach ADR-062
D-5, bypass per-entity validation, and be unauditable at the entity
level. Rejected. The agent operates **entities**; the UI renders them;
pointing and navigation cover attention. (This is also why Author's
drawio iframe stays invisible to `browser_eval`.)

**O2 — where entity surfaces live.** One desktop mega-surface for
everything would duplicate the hub's existing CRUD (Read references,
Fleet/Projects) behind a second consent regime. Chosen rule: **the tool
lives where the entity lives** — desktop-owned entities (Author docs,
Inspect tabs, compare wall arrangement) get bridge surfaces; hub-owned
entities (references, datasets, records) get hub MCP tools; a surface is
never duplicated on both sides. The desktop's job for hub entities is
*transport* (push/refetch) and *rendering* (attribution), not tools.

**O3 — write authorization (Author + other desktop writes).** Extending
the action bearer token grants `browser_*` actuation too and breaks the
kimi relay's env-less read-pin. Chosen: tools reachable on the read
token; every desktop-entity write gated by an **approval card granting a
per-(agent, document/target, session) lease** — precedent
`ui_screenshot`'s per-call card (`uicapture_host.ts:122-140`) and the
reserved `desktopGrantKind` namespace (`mcp_desktop_ui.go:118-131`).
Hub-entity writes keep the hub's own governance (tiers, roles,
`propose`, worker allow-list) — no second gate invented.

**O4 — navigation consent.** `desktop_open` is attention actuation
(changes what the user sees) but mutates nothing. Slotting it as a
fourth hub-side class beside `read | annotate | action` — immediate
route, sharing-toggle + per-surface policy bit + rate limit + audit,
user-dismissable like highlights — mirrors exactly how `annotate` was
carved (`mcp_desktop_ui.go:70-85`). A card per navigation would make
"take me there" unusable; a policy bit per surface keeps Settings/vault
unreachable structurally.

**O5 — live apply into open editors.** Remount-on-write is universal
but destroys undo/viewport; surgical per-editor adapters half-exist.
Chosen: surgical, with remount as an interim, stated in the tool result.

**O6 — direct file edits** (host agent edits the linked file on disk).
Rejected as primary (no watcher, no validation, invisible to open
tabs); a watcher/reload affordance is a complement, and also serves
human external edits.

## 6. Resolution

Pinned as [ADR-064](../decisions/064-agent-desktop-coworking-contract.md):
**D1** the co-worker frame — entities + pointing + navigation, never
synthetic input; local-work tabs in scope, Fleet/Projects hub-native,
Terminal/Settings/Vault excluded; **D2** tools live where entities live;
**D3** the consent ladder — `read | annotate | navigate | write` with
desktop writes card+leased and hub writes hub-governed, bearer scope
never a substitute; **D4** the like-a-user apply contract + the
write-not-blind rule (a surface's focus fields ship before its write
verbs); **D5** validate-before-commit, native grammars, vendored
knowledge under NOTICE. Build order in
[agent-desktop-coworking.md](../plans/agent-desktop-coworking.md):
Author surface + adapters and focus truthfulness first, then navigation
+ Replay hub tools, then Read transport/merge and the Compare/Record
adoption. The no-streaming gap (§4.7) is accepted — progressive
multi-call applies approximate it; true live-draw is an argument for the
L3 local-service leg of
[desktop-companion-vision-parity.md](../plans/desktop-companion-vision-parity.md).

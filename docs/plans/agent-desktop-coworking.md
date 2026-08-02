# Agent desktop co-working — surfaces, adapters, transport

> **Type:** plan
> **Status:** Proposed (2026-08-02) — ADR-064 accepted 2026-08-02, so
> the contract is settled and principal review is done; fleet
> implementation in four waves, W1 next per issue #494
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** 2026.731 main (`91153552`) — anchors from
> the authoring audits
> **Freshness:** contract

**TL;DR.** Execute
[ADR-064](../decisions/064-agent-desktop-coworking-contract.md) across
the local-work tabs. Lanes: **A/B/C/D** the Author co-authoring surface
(`author_*` tools, per-editor live adapters, vendored knowledge, op
modes); **G** focus truthfulness (populate every reserved policy field —
the write-not-blind prerequisite); **H** the `navigate` class
(`desktop_open` over the UIRef grammar); **J** Replay's missing hub
tools (`datasets_*`/episodes/series); **I** Read co-reading (push
transport, merge, append semantics, attribution, undo); **K** adopt
[desktop-compare-wall-and-decisions.md](desktop-compare-wall-and-decisions.md)
for Compare/Record; **E/F** assistant parity + optional file watcher.
Fleet/Projects need nothing (hub-native); Terminal/Settings/Vault stay
excluded. Investigation record:
[agent-desktop-coworking.md](../discussions/agent-desktop-coworking.md).

---

## 1. Lane A — the `author_*` surface (bridge + RPC + consent)

- **A1 — tool definitions.** `author_read {document_id?}` (omitted id =
  active doc; returns `{id, kind, title, spec?, body, updated_at,
  file_path?}` + a doc list), `author_apply {document_id, mode:
  'replace'|'ops'|'append', body?|operations?, reason?}`,
  `author_render {document_id, format:'svg'|'png'}`, `author_guide
  {kind, topic?}`. All join `READ_TOOLS` (`browserbridge.ts:389`) under
  a new `AUTHOR_TOOL_NAMES` set gated like `UI_TOOL_NAMES` (`:474`,
  catalog filter `:1477`); `author_apply` + `author_read` join
  `DESKTOP_ACTION_TOOL_NAMES` (`:484`) for action-class audit + hub
  mirroring; `tunnelClassForTool` (`:1346`) → `'desktop'`. Per-kind
  rules ride in tool descriptions (the reference app's
  `display_diagram` description ports near-verbatim).
- **A2 — renderer author-bridge RPC.** Main has no DOMParser: a
  correlation-id request/response pair over the `bridge:event` plumbing
  (`events.ts:36-39`); main provider = new `McpServerDeps` field
  (`:626-650`) wired via the reverse-registered setter pattern
  (`browserbridge_host.ts:327-345`); the renderer service owns parse,
  validate, apply, snapshot, editor refresh; `redactBridgeArgs`
  (`:221-246`) gains `body`/`operations`.
- **A3 — consent card + lease (the D3 write class).** First
  `author_apply` per (agent, document, session) raises the
  `desktop_action` card (`uicapture_host.ts:122-140` shape) offering
  *allow once / allow this document this session*; grants live in a
  main-side lease table in the reserved `desktopGrantKind` namespace
  (`mcp_desktop_ui.go:118-131`), cleared on toggle-off (join the
  `uiContext.ts:69` cascade), session end, per-run revoke
  (`browserbridge.ts:1378-1380`).
- **A4 — degrade honestly.** Result states `applied_live` |
  `applied_store_only` | `applied_via_remount` (interim until the
  kind's B-lane adapter ships).
- **A5 — the table silent-empty guard ships WITH `author_apply`, not
  with B4.** `parseTable` (`table.ts:40-50`) catches a JSON parse
  failure and returns `emptyTable()` — unparseable input silently
  becomes a blank table. That is exactly the class
  [ADR-064](../decisions/064-agent-desktop-coworking-contract.md) D5
  bans, and it is live in the code today. Harmless while it is
  read-only (a human sees the blank grid and re-opens the file), fatal
  the moment an agent's malformed commit routes through it: the write
  reports success and the user's rows are gone, with no error anywhere.
  B4 already names the fix — `isTableBody` (`table.ts:57-64`) before
  commit — but B4 is **W2** and `author_apply` is **W1**, so as
  sequenced there is one wave in which the verb exists and the guard
  does not. Close it in W1: either pull the `isTableBody` check forward
  (two lines, the predicate already exists) or have `author_apply`
  refuse `kind === 'table'` until B4 lands. Refusing is the safer
  default if the wave is tight — a refused write costs a retry, a
  silent one costs the document. Compare `canvas.ts:87`, which gets
  this right already: an unrecognized body opens **read-only and is
  never serialized back**. That is the shape every kind's parse path
  should have.

## 2. Lane B — Author per-editor adapters + safety net

Every adapter registers in one place: `state/liveApply.ts`, a
document-keyed registry of "apply this body to the live editor". Lane
A's `author_apply` calls it first and reports which A4 rung it landed
on — `applied_live` when a target took the body, `applied_store_only`
when none is registered (the document is not open, or its kind has no
adapter yet), `rejected` when a target refused. B3–B5 are new rows in
the same registry, not new plumbing.

- **B1 — diagram.** Post-init write path into the draw.io iframe
  (`{action:'load'|'merge', xml}`); pin `ev.origin ===
  'drawio://localhost'` and drop the `'*'` targetOrigin
  (`DiagramEditor.tsx:80,91`).
  - *As built:* only `load` is wired. `merge` is draw.io's additive
    action and belongs with D1's `mode:'ops'` — exposing it under
    `mode:'replace'` would let an agent append to a diagram while
    believing it replaced one. The origin pin is safe because
    `drawio://` is registered with `standard: true`
    (`electron/src/schemes.ts:27-29`), so the frame reports a real
    tuple origin rather than `"null"`.
- **B2 — canvas.** Expose `applyBoardString`
  (`CanvasEditor.tsx:598-610`); `pushHistory` first so **Cmd+Z undoes
  the agent**; reject parses that yield `readOnly`.
  - *As built:* two refusals, not one — a body whose parse yields
    `readOnly`, **and** a board that is itself read-only. A user who
    cannot edit a board by hand must not have an agent edit it for
    them.
- **B3 — excalidraw.** `updateScene` via the held API ref
  (`ExcalidrawEditor.tsx:73`); validate scene shape first.
- **B4 — table.** Add the `useEffect([value])` reconcile
  (`TableEditor.tsx:29`); `isTableBody` (`table.ts:57-64`) before
  commit — the silent-empty class (`table.ts:40-50`) must be
  unreachable from agent input.
- **B5 — figure / markdown.** Already reactive; figure applies dry-run
  `renderFigure`; markdown `append` reuses the insert semantics
  (`AuthorSurface.tsx:352-366`).
- **B6 — snapshot ring + attribution + revert.** Bounded per-document
  pre-apply ring reusing `createHistory` (`canvas.ts:287-318`, generic
  over strings); agent-edit chip on the doc tab (attribution per
  `AgentHighlightOverlay.tsx:38-42`) with one-click revert; the ring
  also covers vendor-undo kinds (diagram, excalidraw).
  - *As built* (`state/agentEdits.ts`): **not** `createHistory`. That
    primitive carries a `future` stack and a `redo`, and redo is wrong
    here — after a user reverts an agent's write, redo would re-apply
    the change they just rejected, one keystroke from the button that
    rejected it. What is shared with the canvas is the shape, not the
    implementation.
  - Revert writes the store **and** replays through `liveApply`: the
    store alone is enough for kinds that re-render from `body`, but a
    vendor editor owns its state once mounted and would keep showing
    the agent's version over a store that no longer holds it.

## 3. Lane C — vendored knowledge (Apache-2.0, NOTICE)

- **C1** — port next-ai-draw-io `lib/utils.ts` subset (validate /
  autoFixXml / applyDiagramOperations / wrapWithMxFile) into a renderer
  lib, license header + NOTICE; keep their test cases.
  - *As built* (`state/drawioXml.ts`): the validator, its six helper
    checks and `wrapWithMxFile`, plus a `prepareDiagramBody` that wraps
    THEN validates (validating the raw input passes a bare cell list
    that collides with the scaffold once wrapped).
  - **`autoFixXml` was NOT ported, on merit.** Its last-resort loop
    deletes `mxCell` elements one at a time until the document parses,
    reporting it only as a line in a `fixes` array. That is right
    upstream — an LLM generating a diagram from scratch loses nothing
    real — and wrong here, where `author_apply` writes into a document
    the user owns: it would delete their shapes and report success.
    Same class as the `parseTable` hole A5 exists to close. Repair
    belongs with lane A/D, where each of the 26 fixes can be judged and
    any that drops content becomes a refusal.
  - `applyDiagramOperations` is D1's `mode:'ops'` — **W2**, not here.
  - *No test cases to keep:* upstream ships no unit tests for
    `lib/utils.ts`, only Playwright e2e specs. Written fresh (30).
  - **The validator must run RENDERER-side** (which A2 already implies).
    Its DOM half catches a self-closing nested `mxCell` that the regex
    sweep structurally cannot — without a DOMParser that body validates
    clean.
- **C2** — the 31 shape-library files behind `author_guide
  {kind:'diagram', topic:<library>}` (lazy; full ~180 KB vs top-set —
  reviewer's call).
- **C3** — guides for the rest: figure registry samples
  (`figures.ts:36` — already "agent starters"), JSON Canvas +
  `x-termipod` fields (`canvas.ts:51-92`), minimal Excalidraw element
  set, `TableData` schema, diagram layout/edge-routing rules.

## 4. Lane D — Author op modes beyond replace

- **D1** — diagram `mode:'ops'` (ID-based add/update/delete + cascade).
- **D2** — canvas node/edge ops mapped onto `Board`.
- **D3** — excalidraw/table stay replace-only until demand exists;
  the cap is stated in the tool description.

## 5. Lane G — focus truthfulness (write-not-blind prerequisite)

Populate every reserved-but-empty policy field
(`assembleRawFocus` — `ui_policy.ts:291-319`; gap list
`uiContext.ts:29-33`):

- **G1 — Read tabs.** Lift `ReadSurface`'s tab strip
  (`ReadSurface.tsx:1792-1888`) into a `useReadTabs` store; emit
  `tabs.*`/`tab.*` (kind/title/url/path) per the existing allowlist
  (`ui_policy.ts:158-162`). This is the single highest-leverage Read
  gap — without it an agent cannot learn which paper is open.
- **G2 — Inspect selection.** Publish `inspect.selection` (allowlisted
  at `:165`, never assembled).
- **G3 — Replay.** Emit reserved `replay.episode_id` + `replay.cursor`
  (open episode + player cursor live in `EpisodePlayer` `useState`
  `:64,87-89` — mirror into `useReplay`).
- **G4 — Compare.** Ships with lane K's `compareWall` store (the plan
  already specifies `compare.left/right` + the compare UIRef).
- **G5 — Record.** Fix the wrong reserved field: `record.dataset_id`
  (`ui_policy.ts:171`) describes an episode-recording surface that was
  never built; replace with `record.record_id` when lane K's records
  land (coordinated policy + `UiRefRecord` + plan-matrix erratum).

## 6. Lane H — the `navigate` class (`desktop_open`)

- **H1** — hub-side: add `desktop_open` to `desktop_ui_invoke`'s
  classes as `navigate` (`mcp_desktop_ui.go:66-108` gains a fourth
  list) — immediate route, audited, rate-limited; desktop-side: a new
  per-surface `navigate` policy column in `UI_POLICY` (default allow
  for the six local-work rows, structurally absent for
  terminal/settings/vault) — the `annotate` carve-out repeated.
- **H2** — the address space is the existing UIRef grammar
  (`uiRef.ts:10-12`); execution reuses `focusUiRef`/`focusEntity`
  (`uiRefFocus.ts:42-79`) with two consumer extensions: `replay.cursor`
  seek and `debug` open-if-under-a-pinned-root (today `ui://debug?file=`
  only focuses already-open tabs `:71-76` — agent-initiated open of a
  root-contained file is the "open this checkpoint for me" verb;
  non-root paths still refuse). A visible dismissable toast attributes
  every navigation ("<agent> opened …" + undo-focus), mirroring
  highlight attribution.
- **H3** — `inspect_open` is NOT a separate tool — `desktop_open` with
  a `ui://debug?file=…&kind=…` ref covers it; likewise
  `ui://read?url=…` opens a web tab (the one Read entity agents can
  drive but not open — `ReadSurface.tsx:1865`), gated by the same
  navigate bit.

## 7. Lane J — Replay hub tools (the catalog gap)

Add to the ADR-033 registries (nothing here exists today —
`server.go:724-738` is REST-complete, MCP-empty):

- **J1** — reads: `datasets_list {project?}`, `datasets_get`,
  `dataset_episodes_list {dataset, offset?, limit?}` (windowed, caps
  surfaced — honesty invariants of `ReplaySurface.tsx:30-34` carry to
  the tool results), `episode_series_get {dataset, episode, channels?}`.
- **J2** — writes: `datasets_register {project, host, root_path,
  source, format?}` (idempotent per the identity index),
  `datasets_refresh`, `datasets_update {name?, env_ref?}` (the only
  patchable fields — `handlers_datasets.go:301-308`), tier routine;
  `dataset_export_rrd` wrapping the existing host job
  (`hostjobs.KindDatasetExportRRD`).
- **J3** — desktop transport: invalidate `['datasets']` queries on hub
  events (or a shorter stale window) so agent-registered datasets
  appear without a tab switch; `environments_list` read tool if the
  registry warrants it at review.

## 8. Lane I — Read co-reading (transport + merge + parity)

Hub tools exist (`native_tools.go:193-356`); the work is client-side:

- **I1 — push channel.** Hub→desktop propagation for references +
  annotations: SSE topic or post-write invalidation triggering the
  existing pull leg (`syncLibrary`/`syncAnnotations` — today
  button-only, `ReadSurface.tsx:2195-2219`); annotation writes repaint
  live already (`annotationSync.ts:151`, `PdfCanvas.tsx:1335`).
- **I2 — merge policy.** Replace local-wins (`librarySync.ts:8-19`)
  with the timestamped field merge its header defers; an agent write
  must survive a dirty local row it doesn't conflict with.
- **I3 — append semantics for notes.** `reference_update {notes}` is
  whole-field replace — add `notes_append` (hub tool or mode) so
  summarize-into-notes cannot clobber the reader's paragraph.
- **I4 — attribution + undo.** Render `Annotation.author`
  (`annotations.ts:54`) distinctly for agent-authored highlights;
  extend the 50-deep undo discipline (`annotations.ts:203-212` —
  restored rows re-dirty so an undo is never lost to sync) to library
  writes.
- **I5 — bookmarks (optional).** The cheapest "agent saves a source
  for you" write: a small hub entity + tool pair, or defer.

## 9. Lane K — Compare + Record: adopt the existing plan

[desktop-compare-wall-and-decisions.md](desktop-compare-wall-and-decisions.md)
(Proposed) **is** this design's Compare/Record leg — `compareWall`
store + compare UIRef + `runs_list`/`run_metrics`/`run_config_diff`/
`run_provenance` shipping inside A1/A2 (its lane A), and the hub
`records` table with propose-only agent writes (`created_by_kind`,
`status: proposed → accepted` on the director's click) (its lane B).
This plan adds only: **K1** the `navigate` bit + focus fields land with
those stores (G4/G5); **K2** an optional `compare_arrange` bridge write
(agent curates the wall — desktop-owned arrangement, so D3's card+lease
applies) after the store exists.

## 10. Lane E — assistant parity · Lane F — file watcher

- **E1** — companion `build()` for structured kinds includes body when
  small or a "call `author_read`" hint (`AuthorSurface.tsx:342-351`).
- **E2** — verify kimiweb (read-token relay → card → lease → apply) and
  hub-remote (tunnel `'desktop'`, hub-leg approval per ADR-059)
  end-to-end.
- **E3** — `ui_get_focus` field set grows only via lane G — bodies flow
  through audited tools, never ambient snapshots.
- **F1** — optional: watch `filePath`-linked docs and offer reload on
  external change (agents on hosts and humans in other apps both become
  visible; complement to, not substitute for, the tool path).

## 11. Sequencing

**W1** = A1–A5 + B1 + B2 + B6 + C1 + **G1–G3** → Author
diagram/canvas co-authoring end-to-end, agents no longer blind on
Read/Inspect/Replay. A5 is in W1 by necessity, not by size: it is the
one item here that closes a data-loss window the wave itself opens.
**W2** = B3–B5 + D1 + C2 + C3 + `author_render` + **H1–H3** + **J1–J2**
→ all Author kinds; navigation; Replay agent-reachable.
**W3** = **I1–I4** + J3 + K (as its own plan's waves) + D2 + E1–E3.
**W4** = I5 + F1 + polish + the K2 arrange write if demand holds.

## 12. Review anchors

- **Security:** body/entity disclosure rides the sharing toggle + audit
  (reads too, not just writes); lease revocation cascades like
  highlights (`uiContext.ts:69`); `navigate` structurally cannot
  address terminal/settings/vault (absent column, not a false bit);
  write verbs never annotated read-only (ADR-063 D5); the drawio
  iframe and workbench stay invisible to `browser_*`/CDP; renderer IPC
  payloads re-narrowed on receipt (`agentHighlight.ts:56-78`
  discipline); bodies redacted from audit args.
- **Contract tests:** malformed apply leaves the doc byte-identical
  (table first — its parser is destructive for user input by design);
  per-editor parity test pins external-apply-visible-without-remount;
  Go/TS focus-field fixture keeps policy rows and assemblers in sync
  (the reserved-but-unpopulated class that motivated lane G).
- **Honesty:** windowed/capped tool results surface their caps
  (Replay's invariants); `applied_*` degradation states are exercised
  in tests, not just documented.
- CI blind spot: desktop node tests run manually
  (`node --test src/state/*.test.ts src/ssh/*.test.ts` + electron
  suite) per wedge.

## 13. Acceptance

A user in any local-work tab can ask either assistant arm for help and
the agent can (a) see what they see (focus fields truthful per
surface), (b) point at it, (c) bring them somewhere on request (with
attribution + dismiss), and (d) make the change through the owning
entity surface — landing live, undoable, attributed, revertible;
malformed writes refuse with the validator's diagnosis and change
nothing. Specifically: Author kinds all co-authorable (Cmd+Z/revert
undoes the agent); an agent can register + refresh + read a Replay
dataset and walk the user to `ui://replay?…&episode_id=…&cursor=…`; an
agent summary lands in a reference's notes without clobbering the
reader's text and appears without pressing Sync; agent PDF highlights
render attributed; a proposed decision record awaits the director's
accept; Compare picks are agent-readable once the wall store lands.
kimiweb (read token) succeeds through card + lease; an action-token
agent without a lease is refused; toggle-off revokes leases and hides
every `author_*`/navigate capability; Terminal, Settings, and Vault are
unreachable by construction in every test above.

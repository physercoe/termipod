# Author — shell cleanup, outline pane, canvas v2

> **Type:** plan
> **Status:** W1 + W2 + W3 shipped (2026-07-23) — W3 interaction layer pending device verification
> **Audience:** contributors · principal
> **Last verified vs code:** desktop 2026.722.1327 (on main)
>
> Feeds the J2 Author surface ([desktop-workbench-jobs.md](desktop-workbench-jobs.md));
> executes the standing J4/JSON-Canvas recommendation of
> [research-app-product-landscape.md](../discussions/research-app-product-landscape.md) §4.1
> and respects the tldraw license rejection in
> [figure-and-diagram-tooling-landscape.md](../discussions/figure-and-diagram-tooling-landscape.md).

**TL;DR.** Three independently shippable wedges, from four director asks.
**W1 — shell**: the left pane's "Open" section is redundant with the tab strip
— delete it; the left pane becomes **workspace-only**, and the open-doc
chips (kind icon, dirty ●, draft badge, active highlight) move onto the
workspace rows and the tabs; the header's "☰ Files" toggle becomes a small
fold chevron on the pane itself; the six flat "New X" header buttons collapse
into **one categorized "New ▾" menu**, leaving a clean `[New ▾] [Open] [Save]
… [Assistant]` action bar. **W2 — outline**: the markdown editor gains a
**right-hand foldable outline pane** (Obsidian-style) by reusing the existing
shared `MarkdownOutline` rail, extended to drive the CodeMirror source pane
(jump-to-line) as well as the rendered preview. **W3 — canvas v2**: the
hand-rolled canvas editor is rebuilt on **React Flow** (`@xyflow/react`, MIT —
tldraw stays rejected on license) and the body/on-disk format moves to
**JSON Canvas 1.0** (Obsidian-interoperable `.canvas`), keeping the
Zettelkasten differentiator (cards wired to the J1 reference library, typed
edges, backlink inspector) while gaining resize, multi-select, real edge
anchoring, minimap, groups, and undo. §5 records the **figure-spec roster**
the New ▾ menu surfaces: six rows shipped, **bpmn-js not implemented**
(license-held), LikeC4 deferred, and **Typst missing** — with the
post-Tauri route (vault-style Rust→WASM in the Electron main process) that a
future Typst wedge should start from.

---

## 0. Problem

- **Left pane (`AuthorNav.tsx`) duplicates the tab strip.** Its "Open" section
  lists exactly the docs the tab strip above the editor already shows —
  same title, same dirty dot, same click-to-focus. Only three affordances are
  unique to it (rename, save-draft-to-workspace via context menu / drag,
  close), and all three belong on the tabs. Meanwhile the workspace tree
  below it renders bare filenames: no kind icon, no marker for "this file is
  open / dirty / active", so the one section with unique information is the
  visually poorest.
- **The header action bar is a flat list of ten buttons**: `☰ Files · + New
  document · New diagram · New canvas · New table · New sketch · New figure ▾
  · Open file · Save · Assistant` — every new document kind added one more
  button (Excalidraw was the sixth), and the Files toggle is a labeled button
  for what is really a sidebar collapse. It reads as clutter and won't scale
  to the next kind.
- **The markdown editor has no outline.** For long documents (reports, plans)
  there is no way to see structure or jump to a section — the Read surface's
  markdown reader and the note tab both already have the shared
  `MarkdownOutline` rail (`ui/MarkdownOutline.tsx`, #322), but the *editor*,
  where long documents are actually written, does not.
- **The canvas editor is too simple to think on.** `ui/CanvasEditor.tsx` is
  hand-rolled: fixed 210-px-wide cards, no resize, no multi-select/marquee, no
  undo, single straight-line edges anchored to a fixed point near the card
  top, no groups/colors, a plain `<select>` to add a reference card. And its
  body format is private JSON (`{cards, edges}`) squatting on the `.canvas`
  extension that the wider ecosystem (Obsidian, Kinopio, Flowchart Fun) uses
  for **JSON Canvas 1.0** — worse than non-interoperable: opening a real
  Obsidian `.canvas` from the workspace tree parses as an *empty board*
  (`parseBoard` tolerates malformed input), and the next mutation **overwrites
  the user's Obsidian file** with `{"cards":[],"edges":[]}`. That is a live
  data-loss foot-gun, not just a missing feature.

Non-goals: no change to the diagram (draw.io), table, figure, or Excalidraw
editors; no hub/mobile involvement (documents stay device-local/file-linked);
no WYSIWYG-editor replacement (Milkdown Crepe stays); no bundle-at-boot growth
(every new library loads as a lazy chunk, the Excalidraw precedent).

## 1. Decisions

### W1 — shell: workspace-only left pane + categorized action bar

1. **Delete the "Open" section; the left pane is the workspace tree.** The
   unique affordances move to the **tab strip**, which gains a context menu
   (reusing `ui/ContextMenu`'s `useContextMenu`, as CanvasEditor does — not a
   third hand-rolled menu):
   - **Rename** (drafts and file-backed docs alike — file-backed rename calls
     the existing `workspace_rename` + `repointDoc` path when the file is
     inside the workspace, else just retitles);
   - **Save to workspace** (drafts only, `folder` open — the existing
     `saveDraftToWorkspace`);
   - **Reveal in folder** (file-backed only, `revealPath`);
   - **Close** (the existing two-step confirm).
   Draft **tabs** become draggable with the existing
   `application/x-termipod-doc` payload; the workspace section's drop handler
   already materializes them — drag-a-tab-to-the-tree replaces
   drag-an-open-row-to-the-tree.
2. **The open-doc chips move into the workspace rows** (the director's "reuse
   the icon/chip on the workspace row"). Each file row gets:
   - a **kind icon** from a pure `ext → IconName` map colocated with
     `TEXT_EXT` (markdown/diagram/canvas/table/figure/sketch/code/other — an
     extension map, not `kindForFile`, which needs file content for `.json`
     sniffing; `.json` shows the generic doc icon);
   - an **open indicator**: when some doc's `filePath` matches the row's path,
     the row renders emphasized with the standard dirty ● when that doc is
     dirty, and an `active` highlight when it is the active tab (click already
     focuses instead of re-opening — `openFile` handles that today).
   Directory rows keep their chevron; inert (non-`TEXT_EXT`) rows keep the
   muted style.
3. **The Files header button becomes pane furniture.** The `☰ Files` action
   is deleted from the header. The workspace pane header gets a fold chevron
   (the `MarkdownOutline`/`read-fold` pattern), and when folded a slim edge
   button (`mdreader-outline-show` pattern) re-opens it. Persistence keys
   (`termipod.author.showNav`, `.navW`) are unchanged.
4. **One categorized "New ▾" menu replaces the six New buttons.** The
   existing `author-figbtn` dropdown generalizes:
   - primary click = new **Document** (markdown, the overwhelmingly common
     case);
   - the chevron opens a grouped menu: **Write** (Document) · **Data**
     (Table) · **Draw** (Diagram (draw.io), Sketch (Excalidraw), Board
     (canvas)) · **Figure** (the registry rows, **driven by `FIGURES`** so a
     future row appears with zero menu changes — today: Mermaid / Graphviz /
     Vega-Lite / nomnoml / WaveDrom / ECharts; see §5 for the roster status —
     bpmn-js is NOT shipped and must not be listed while license-held).
   All entries call the existing `createDoc(kind, spec?)` (workspace
   materialization + `NEW_BASE` naming unchanged). The resulting header is
   `[New ▾] [Open] [Save] … [Assistant]`; Save additionally shows the active
   doc's dirty ● so the state that used to live only in the doc-bar meta is
   visible where the action is.
5. **i18n**: dropped strings (`author.files`, `author.navOpen`,
   `author.navNoOpen`, …) removed; new menu/group/tab-menu strings added
   en + zh.

### W2 — outline: a right-hand foldable nav for the markdown editor

1. **Reuse `MarkdownOutline`, don't fork it.** Additive extensions to the
   shared component (its two existing callers are unaffected):
   - `side?: 'left' | 'right'` (default `'left'`) — flips the fold chevron,
     the resize handle's edge, and which side the collapsed show-button sits
     on;
   - `Head` gains `line: number` (the heading's source line —
     `extractHeadings` already walks lines; it just doesn't record the index);
   - `onJump?: (h: Head) => void` — when set, replaces the built-in
     `bodyRef.querySelector(#slug).scrollIntoView` so the caller can route a
     jump per view mode.
2. **The source editor learns to jump.** `MarkdownEditorHandle` (CodeMirror)
   gains `revealLine(line: number)`: set the selection to that line's start,
   `scrollIntoView` centered, focus the editor. This is the same
   imperative-handle pattern the formatting buttons already use.
3. **Per-mode routing** inside `AuthorSurface`'s `Editor`:
   - `edit` → `revealLine`;
   - `read` → scroll the preview pane (its `<Markdown>` gets
     `headingIds` — currently off in Author, already on in the Read
     surface's reader);
   - `split` → both (editor jump + preview scroll);
   - `wysiwyg` → best-effort: query the Milkdown host for `h1–h6`, match by
     slugified text content, `scrollIntoView` (Crepe stamps no ids; a miss is
     a no-op).
4. **Placement & state**: the rail renders to the **right** of the editor
   body (Obsidian's side), fold state and width persisted
   (`termipod.author.outlineOpen` / `.outlineW`); the shared "hide when ≤ 1
   heading" rule stands. The outline recomputes from the debounced preview
   body (the existing 250 ms `useDebounced`), not per keystroke.

### W3 — canvas v2: JSON Canvas 1.0 body + React Flow interaction layer

1. **Format: adopt JSON Canvas 1.0** (jsoncanvas.org, MIT — the standing
   §4.1 recommendation) as the body *and* on-disk `.canvas` format:
   - note card → `text` node (markdown `text`, real `width`/`height`);
   - reference card → `link` node with `url: "termipod://ref/<id>"` plus a
     namespaced `"x-termipod": {"refId": …}` — a foreign app shows a link
     card, we resolve the live library reference;
   - group → `group` node (label + background per spec);
   - typed edge → spec edge with `fromSide`/`toSide`, `label` set to the
     edge type's display text, and `"x-termipod": {"edgeType": …}` as the
     machine discriminator — an export degrades gracefully to a labeled
     edge in Obsidian.
   **Legacy migration**: `parseCanvas` accepts both shapes — a body with a
   `nodes` array is JSON Canvas; a body with a `cards` array is the legacy
   format, converted on parse (cards get `width: 210` and a default height).
   Serialization always writes JSON Canvas, so legacy localStorage bodies and
   `.canvas` files upgrade on first save. **An unrecognized body is opened
   read-only with a notice instead of as an empty board** — that kills the
   overwrite-an-unknown-file foot-gun outright, including for future spec
   versions.
2. **Engine: React Flow** (`@xyflow/react`, MIT). tldraw remains rejected
   (proprietary SDK license — watermark/fee, per the landscape); BlockSuite's
   Edgeless is legally embeddable but churny (monorepo-internal, web
   components) and brings a whole block model we don't need. React Flow is
   the boring middle: MIT, React-native custom node/edge rendering (our card
   components survive), controlled state (we own the model and its
   serialization), and it ships precisely the interaction layer we lack —
   pan/zoom, drag, **marquee multi-select + group drag**, **NodeResizer**,
   **side-anchored edges** (handles on all four sides map 1:1 to JSON
   Canvas `fromSide`/`toSide`), snap grid, minimap + fit-view controls,
   delete-key handling, edge labels. It is already in the project's orbit
   (LikeC4's renderer is built on it). Loaded as a **lazy chunk** — 
   `CanvasEditor` is currently a direct import in `AuthorSurface`; it becomes
   `lazy()` like TableEditor/FigureEditor/ExcalidrawEditor, so the dependency
   never lands in the boot chunk.
3. **What stays ours** (the J4 differentiator: "cards must *be* library
   refs", desktop-workbench-jobs): the reference-card content rendered from
   the live `useLibrary` store, the inspector with backlinks
   (incoming/outgoing typed edges), edge-type cycling, the context-menu
   add-note-here, add-reference. The inspector and toolbar carry over with
   cosmetic changes only.
4. **New capabilities in W3 scope**: card resize (persisted `width`/`height`);
   marquee multi-select, multi-drag, delete-key; side-anchored edges with
   visible labels; minimap + zoom controls + fit-view (replaces "Reset
   view"); groups (create from selection, drag-into/out-of); **undo/redo**
   (a bounded snapshot stack over the board model — mutations are already
   funneled through one `mutate()`; Ctrl/Cmd+Z / Shift+Z, suppressed while
   focus is in a text input so CodeMirror/textarea history is untouched).
   Node colors (the spec's 6 presets) ride along cheaply on the card menu.
5. **Compatibility**: `kindForFile('canvas')`, `extForKind`, TEXT_EXT, the
   agent-companion posture (read/assist-only for canvas), and the Save/Open
   round-trip are unchanged — the body is still a JSON string in `Doc.body`.

## 2. W1 — shell

| Piece | Change |
|---|---|
| `src/surfaces/AuthorNav.tsx` | delete the Open section + its context menu/rename state; add `ext → icon` map beside `TEXT_EXT`; file rows get kind icon + open/dirty/active markers (from `useDocuments`); pane-header fold chevron + collapsed edge button (state lifted to `AuthorSurface`'s existing `showNav`). |
| `src/surfaces/AuthorSurface.tsx` | header: drop the Files button and the five standalone New buttons; add the grouped **New ▾** menu (generalizing `author-figbtn`); Save button shows dirty ●; tab strip: `useContextMenu` menu (rename / save-to-workspace / reveal / close), draft tabs draggable with `application/x-termipod-doc`. |
| `src/styles/partials/*` | nav-row markers, fold button, New-menu groups, tab-menu; delete dead Open-section rules. |
| i18n | new: menu groups, tab-menu items, fold tooltips; removed: `author.files`, `author.navOpen`, `author.navNoOpen`, draft-row strings that move to tabs — en + zh. |
| E2E | the Excalidraw smoke's Author navigation (create-doc path) updates to the New ▾ menu; add a smoke: open workspace-less Author → New ▾ → Document → tab appears; fold/unfold the pane. |

**Acceptance (W1).** The left pane shows only the workspace tree (plus its
header row); every affordance the Open section had is reachable from the tab
strip (rename a draft, save it to the workspace by menu *and* by dragging the
tab into the tree, reveal a file-backed doc, close with confirm). A file that
is open renders in the tree with its kind icon, an open marker, dirty ● while
unsaved, and highlight when active; clicking it focuses the existing tab. The
header shows exactly `New ▾ · Open · Save · Assistant`; every document kind
(incl. each figure spec) is creatable from the New menu and still
materializes into an open workspace folder. The pane folds to a slim edge
button and restores, state persisted across restarts.

## 3. W2 — outline

| Piece | Change |
|---|---|
| `src/ui/MarkdownOutline.tsx` | additive: `side` prop, `Head.line`, `onJump`, optional fold-persistence key. Existing callers (MarkdownReader, NoteTab) unchanged. |
| `src/ui/MarkdownEditor.tsx` | `revealLine(line)` on `MarkdownEditorHandle`. |
| `src/surfaces/AuthorSurface.tsx` (`Editor`) | right-hand rail wired per view mode (edit/split/read/wysiwyg routing per §1); preview `<Markdown>` gains `headingIds`; headings extracted from the debounced body. |
| i18n | outline label/tooltips reuse the existing `read.mdOutline`/`read.collapse` keys where sensible; any new keys en + zh. |
| E2E | Author smoke: type a two-heading doc, outline appears on the right, clicking the second heading moves the CodeMirror selection (edit mode) and scrolls the preview (split). |

**Acceptance (W2).** In a markdown doc with ≥ 2 headings the outline renders
on the right in all four view modes; clicking an entry jumps the source
editor to that heading's line (edit/split) and scrolls the preview
(read/split); wysiwyg jump is best-effort. The rail folds/unfolds and its
width and fold state persist. Docs with ≤ 1 heading show no rail. The Read
surface's reader and note outline behave exactly as before.

## 4. W3 — canvas v2

| Piece | Change |
|---|---|
| `src/state/canvas.ts` | JSON Canvas 1.0 model + `parseCanvas` (spec / legacy-convert / unknown→read-only) + `serializeCanvas`; typed-edge and ref-card mapping via `x-termipod`; bounded undo stack helper. |
| `src/ui/CanvasEditor.tsx` | rebuilt on `@xyflow/react`: custom node components (note = markdown-lite card w/ NodeResizer; ref = library card resolved live; group), 4-side handles, typed/labeled edges, marquee/multi-drag/delete-key, minimap + controls, context menus, colors; inspector + toolbar carried over. |
| `src/surfaces/AuthorSurface.tsx` | `CanvasEditor` becomes a `lazy()` chunk. |
| `package.json` | `@xyflow/react` (MIT) + its stylesheet imported inside the lazy chunk. |
| i18n | new canvas strings (groups, colors, undo, read-only notice) en + zh. |
| E2E | Author smoke: new Board → add two notes → connect → assert nodes/edge in the serialized body (`nodes`/`edges` per JSON Canvas); reload → board restores. |

**Acceptance (W3).** A board created in TermiPod opens in Obsidian as a
sensible canvas (text nodes, labeled edges, groups); an Obsidian `.canvas`
opened from the workspace tree renders its text/link/group nodes and labeled
edges, round-trips without losing fields we don't model (unknown node types
render as inert cards, preserved on save), and is **never** silently
overwritten with an empty board. Legacy TermiPod boards (localStorage and
`.canvas` files) open converted with nothing lost. Cards resize; marquee
select + group drag + delete-key work; edges anchor to card sides and show
their type; minimap/fit-view present; undo/redo works and never steals
Ctrl/Cmd+Z from a focused text input. Reference cards still render live
library metadata and the backlink inspector works as before. The boot chunk
does not grow (React Flow loads only when a board opens).

## 5. Figure-spec roster — shipped, held, and what's left

The New ▾ menu (W1) surfaces the figure registry, so the plan records where
the roster actually stands (per [figure-renderer-registry.md](figure-renderer-registry.md)
and the [figure/diagram landscape](../discussions/figure-and-diagram-tooling-landscape.md)):

- **Shipped registry rows (6)** — Mermaid, Graphviz, Vega-Lite (Phase A);
  nomnoml, WaveDrom, ECharts (Phase B). All are pure `src → SVG` renderers in
  lazy chunks. Shipped editor *kinds* beside them: draw.io `diagram`,
  Excalidraw `excalidraw` (figure-plan Phase C), `canvas` (W3 here).
- **bpmn-js — NOT implemented, held.** Phase C candidate gated on the
  "Powered by bpmn.io" watermark license (figure plan §4). Nothing in this
  plan may reference it as available; the registry-driven menu simply won't
  list it. Unblocks only via the license decision, not engineering.
- **LikeC4 — not a row, deferred.** The 2026-07-22 spike found no pure
  `dsl → SVG` path (its own export runs a headless browser), so it moved to a
  Phase C editor-mount candidate. No change here.
- **Typst — the missing one.** There is no Typst anywhere in the app today,
  while the research landscape names it the intended math/typesetting and
  PDF-export engine (research-app-product-landscape §9, catalog row 15).
  That recommendation predates the Electron migration — it said "compile
  natively in the **Tauri** core", a route that no longer exists (M3.4
  retired the Rust shell). The Electron-era path is the **vault precedent**:
  a small Rust crate wrapping the Apache-2.0 `typst` + `typst-svg` crates,
  built to a wasm-bindgen **nodejs** target, loaded in the Electron main
  process, exposed as a `typst_render {src} → svg` command. On top of that:
  1. a **`typst` figure registry row** (body `.typ`, math + cetz figures) —
     the registry's renderer contract is already `src → Promise<SVG>` and is
     transport-agnostic, so an IPC-backed renderer fits without registry
     changes (this is the cheap wedge, and what makes Typst appear in the
     New ▾ Figure group automatically);
  2. the full **PDF/report export pipeline** (fonts, Universe packages,
     hayagriva CSL-JSON→BibLaTeX shim) — deliberately **its own future
     plan**; it's an export engine, not a figure spec, and the crate API's
     minor-release churn wants the isolation boundary designed properly.

  **Neither is in W1–W3 scope.** This section exists so the roster answer
  lives somewhere findable and the Typst wedge starts from the right
  (post-Tauri) architecture when it's picked up.

## 6. Sequencing & risk

- **W1 ∥ W2 ∥ W3** — no shared code beyond `AuthorSurface` render glue (small,
  mergeable). W1 and W2 are small; W3 is the big one. Suggested order
  W1 → W2 → W3 only because the shell cleanup makes device-verifying the
  rest pleasanter.
- **Riskiest: the W3 rebuild.** Mitigations: the model/serialization is ours
  (React Flow is render+interaction only, swappable the way webview is
  contained in BrowserView); the legacy converter is pure and reviewable in
  isolation; read-only-on-unknown means a bug in detection can't destroy a
  file. The old editor stays in the tree until W3 acceptance passes on a
  device, then is deleted in the same PR that flips the import.
- **Preservation over round-trip** (W3): unknown JSON Canvas fields/nodes
  must be carried through parse→serialize untouched (keep the raw node
  objects, patch known fields) — that is what makes "opens in Obsidian, comes
  back unharmed" true, and it's the part a naive `interface Board` rewrite
  would silently break.
- **W1 removes affordances from one place as it adds them to another** — the
  tab context menu must land in the *same commit* as the Open-section
  deletion, or drafts temporarily lose rename/materialize.
- **Wysiwyg outline jump is best-effort by design** (Crepe stamps no heading
  ids); acceptance treats a miss as a no-op, not a failure.
- **Bundle**: `@xyflow/react` is a few hundred KB pre-gzip — same class as
  TableEditor/Excalidraw, and like them it ships only in a lazy chunk;
  verify in the Vite output as the figure-plan review did.

## 7. Open questions (not blockers)

1. A keyboard shortcut for the nav-pane fold — Ctrl/Cmd+B is taken (bold);
   Ctrl/Cmd+Shift+E (VS Code's explorer) is free. Decide with the director.
2. Should the ref-card picker upgrade from a `<select>` to a search popover
   (type-ahead over the library)? Cheap with the existing library store;
   lean yes, inside W3.
3. JSON Canvas `file` nodes: v2 renders them as inert path cards (preserved
   on save). Wiring click-to-open into the workspace/Read surfaces — and
   `subpath` heading/block views (§4.1's "cards as views over notes") — is a
   natural follow-up wedge, out of scope here.
4. Drafts are visible only as tabs after W1. If that proves too hidden, a
   collapsed "Drafts" chip in the workspace pane header (shown only when
   drafts exist) is a 20-line follow-up; the plan keeps the director's "left
   pane is just workspace" literally until then.
5. Canvas "sections as agent context" (Heptabase pattern) and live-query
   cards (Logseq pattern) from the landscape §4.2 — noted for a later
   canvas-intelligence wedge once groups exist.
6. Should the New ▾ menu's primary click be configurable (last-used kind vs
   always Document)? Default: always Document.

## Related

- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) — J2 Author / J4
  Canvas job definitions (canvas cards must be library refs).
- [research-app-product-landscape.md](../discussions/research-app-product-landscape.md)
  — §4.1 JSON Canvas adoption (this plan executes it), §4.2 canvas-leader
  patterns (future wedges).
- [figure-and-diagram-tooling-landscape.md](../discussions/figure-and-diagram-tooling-landscape.md)
  — tldraw license rejection; Excalidraw/licensing precedents.
- [figure-renderer-registry.md](figure-renderer-registry.md) — the figure
  specs the New ▾ menu groups; the lazy-chunk + editor-mount precedents.

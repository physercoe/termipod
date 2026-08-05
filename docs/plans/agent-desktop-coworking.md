# Agent desktop co-working — surfaces, adapters, transport

> **Type:** plan
> **Status:** In flight (2026-08-04) — ADR-064 accepted 2026-08-02, so
> the contract is settled and principal review is done. W1 merged
> 2026-08-04 (#503–#508, restored by #515); W2 is part-landed — J1–J2
> (#513), B3–B5 (#516), D1, `author_render` — with C2/C3 and H1–H3 to
> go per issue #494
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
  - *As built:* **two tools in W1, three now.** `author_read` +
    `author_apply` shipped first; `author_render` was already W2 in §11
    and landed there; `author_guide` followed it to W2 because its whole
    content is C2 + C3, both W2 — a guide verb with nothing to answer is
    a catalog entry that teaches an agent to stop asking. `mode` was
    `'replace' | 'append'` in W1, with `'ops'` refused **by name**
    (`INVALID_PARAMS`) rather than falling back to replace, which would
    commit an operation list as the document body; **D1 added `'ops'`**
    and the by-name refusal now covers any fourth spelling.
  - *As built (D1):* the schema marks **neither `body` nor `operations`
    required**, and the xor is enforced in the handler instead.
    Expressing "one or the other" in JSON Schema needs `oneOf`, and a
    strict client that cannot compose it drops the whole tool rather
    than the constraint — while a handler refusal can also say which
    argument this mode wanted. Sending both is refused rather than
    resolved: honouring one silently picks for a model that has not
    decided, and the argument we would have to drop is a whole document.
  - *As built:* the gate set is now `DESKTOP_GATED_TOOL_NAMES`
    (`UI_TOOL_NAMES ∪ AUTHOR_TOOL_NAMES`) and the catalog filter +
    `tunnelClassForTool` read from it, so the next lane that adds a
    desktop tool cannot forget either site.
  - **`author_read` did NOT join `DESKTOP_ACTION_TOOL_NAMES`.** That set
    does two jobs — "audit on every leg" and "`readOnlyHint: false`" —
    and `author_read` needs the first while the second would be a lie
    (ADR-063 D5 is explicit that annotating a read as a mutation is the
    one direction of that hint which can cause harm). Split into
    `DESKTOP_AUDITED_TOOL_NAMES` ⊃ `DESKTOP_ACTION_TOOL_NAMES`; the
    audit gate reads the superset, the annotation reads the subset.
    Hub-leg reads stay ring-only as before — the hub routed them.
  - *As built (`author_render`, W2):* it renders **figure**, **excalidraw**
    and **diagram**; `markdown`/`table` are text and `canvas` has no
    exporter (D2's work). Four decisions worth carrying:
    - **It is a READ, and takes no card**, in both registries
      (`desktopUIReadTools` hub-side, out of
      `DESKTOP_ACTION_TOOL_NAMES` desktop-side, `readOnlyHint: true`).
      The resemblance to `ui_screenshot` is superficial and the
      difference is structural: a screenshot is a frame of the user's
      whole SCREEN — hence ADR-062 D-3's surface table and D-4's
      per-call card with no session grant — while this draws ONE
      document from its own body, and the caller could already have that
      document's source from `author_read` under the same toggle. It
      discloses strictly less than the read does. Audited on every leg,
      like `author_read`, for the same reason.
    - **Every kind is drawn by its OWN renderer** — `renderFigure` for
      figures, the Excalidraw exporters for scenes, draw.io's embed
      `export` for diagrams. Same judgement as B5's dry run: a second
      renderer agrees with the user's screen until it does not, and an
      agent shown a picture nobody else can see is worse off than one
      shown nothing.
    - **One rasterizer.** SVG is what every path produces and PNG is
      that SVG through `svgToPngBase64`, which moved out of
      `FigureEditor` into `state/renderDocHost.ts` so the picture an
      agent gets and the file the export button writes are the same
      function. Excalidraw is the exception, and on merit: its own PNG
      exporter resolves embedded image files and fonts that an SVG round
      trip through a canvas silently drops. Agent renders are 1× where
      the export button is 2× — a retina PNG is four times the tokens
      for detail nothing downstream reads.
    - **A diagram needs the document open**, because draw.io owns the
      model. That is checked BEFORE the render (`hasLiveRender`) rather
      than inferred from its failure: "no editor is open" has a recovery
      the agent can name to the user and "the editor could not draw
      this" does not. New registry `state/liveRender.ts`, sibling of
      `liveApply` rather than a method on it — `figure` renders without
      taking a live write and `canvas` takes one without rendering, so
      one registry would make every adapter declare a capability it does
      not have.
  - *Known limits:* the draw.io export leg is **unexercised** — it needs
    a machine with a display and an installed draw.io. The `export` reply
    carries no correlation id (verified against drawio.com/doc/faq/embed-mode),
    so one export is in flight per editor at a time and a second caller is
    told to retry rather than being handed the first one's answer. Output
    is capped at 1 MiB of base64: an image lands inline in the agent's
    context and stays there, so past that the honest answer is "ask for
    svg".
- **A2 — renderer author-bridge RPC.** Main has no DOMParser: a
  correlation-id request/response pair over the `bridge:event` plumbing
  (`events.ts:36-39`); main provider = new `McpServerDeps` field
  (`:626-650`) wired via the reverse-registered setter pattern
  (`browserbridge_host.ts:327-345`); the renderer service owns parse,
  validate, apply, snapshot, editor refresh; `redactBridgeArgs`
  (`:221-246`) gains `body`/`operations`.
  - *As built:* three ops, not two — `read`, `apply`, and a pre-flight
    `resolve`. The approval card has to NAME the document, the document
    is a fact only the renderer holds, and naming one must not disclose
    it: `resolve` returns id/kind/title/path and no body. Order is
    resolve → card → apply.
  - *As built:* the request order is the wedge's safety property, so it
    lives in `executeAuthorRequest` (`state/authorBridge.ts`) with the
    three stores INJECTED, and `authorBridgeHost.ts` is a 30-line binder.
    The store write is step 6, after the live editor gets its say — so a
    body the editor refuses leaves the document byte-identical, and the
    ring records the pre-write body before the store moves. Both
    orderings are pinned by tests and both mutations die.
  - *As built:* `redactBridgeArgs` also clips `reason` (agent-authored,
    unbounded upstream) and redacts `operations` **before** lane D
    accepts the mode — an argument this module does not implement still
    reaches the audit ring.
- **A3 — consent card + lease (the D3 write class).** First
  `author_apply` per (agent, document, session) raises the
  `desktop_action` card (`uicapture_host.ts:122-140` shape) offering
  *allow once / allow this document this session*; grants live in a
  main-side lease table in the reserved `desktopGrantKind` namespace
  (`mcp_desktop_ui.go:118-131`), cleared on toggle-off (join the
  `uiContext.ts:69` cascade), session end, per-run revoke
  (`browserbridge.ts:1378-1380`).
  - **The lease could not live in `desktopGrantKind` — that namespace is
    the wrong SHAPE, not merely the wrong scope.** `bridgeGrants` keys by
    (kind, team, host, agent); there is no document in the key. A grant
    minted from "allow this document for this session" would therefore
    silence the card for every OTHER document the user opens: they would
    have said *edit my draft* and been taken to mean *edit anything I
    open*. So the lease is the desktop's own, keyed (agent, document),
    in `electron/src/author.ts`; and hub-side `desktopUIGrantable` now
    **refuses `author_apply` a grant outright**, which makes every
    relayed apply carded per call with the document in the card's args.
    `desktopGrantKind` stays reserved for a future grant key that
    carries a subject.
  - *As built:* cleared on toggle-off (`desktopui.ts`), on
    Remote-driving revoke (`browserbridge_host.ts`), and with the
    process. Never persisted — a lease is a convenience within one
    sitting, not a permission.
  - **The sharing toggle's own copy was a promise this wedge broke.** It
    said *"Never shared: message bodies, vault contents, settings
    values"* while describing a snapshot of "ids, paths and URLs only" —
    and `author_read` returns the text of the user's documents under the
    same switch. Consent that names the wrong thing is not consent, so
    `assistant.uiContextToggle` / `assistant.uiContextBlurb` were
    rewritten in BOTH dicts to enumerate all four capabilities the
    toggle now grants, including that edits are carded per document and
    revertible. Worth carrying forward: **a lane that widens what a
    toggle grants owns that toggle's sentence.**
- **A4 — degrade honestly.** Result states `applied_live` |
  `applied_store_only` | `applied_via_remount` (interim until the
  kind's B-lane adapter ships).
  - *As built:* **two rungs, not three.** `applied_via_remount` is not
    implemented and not reported — nothing remounts an editor, and a
    state nobody can produce is a promise in a tool description rather
    than a result.
  - *As built:* the rung is decided by `applyStateFor` — a registered
    live target that took the body, else the kind. `rendersFromBody` is
    true for `markdown` (`ui/MarkdownEditor.tsx` diffs an external
    `value` into the CodeMirror doc; its own comment names the
    agent-insert case) and `figure` (`surfaces/FigureEditor.tsx` does the
    same), false for `table` (parses `value` in a `useState` INITIALIZER,
    no effect — B4), `excalidraw` (B3) and the two kinds that have
    adapters. Each answer is cited in the source, because a wrong `true`
    tells the user to look at a screen that did not change.
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
  - *As built:* the `canvas.ts:87` shape, ported — `TableData` gains
    `readOnly`, `parseTable` sets it on any body it could not read, and
    `TableEditor` refuses every write while it is set (with a banner
    saying why). `isTableBody` was NOT pulled forward: it answers "is
    this a table?", which is the open-time sniff, not "did this body
    survive parsing", which is what the write path needs.
  - **The wave-tight framing was wrong, and so was "harmless".** This
    is not a hole `author_apply` would open — it is live today with no
    agent involved. `TableEditor.mutate` serializes on EVERY change, so
    ONE click on a table document whose body failed to parse wrote the
    blank grid over it and the original was gone. The grid is not
    read-only, so "a human sees the blank grid and re-opens the file"
    does not hold.
  - **A second mouth of the same hole**, found while fixing the first:
    `bodyToFile` lowers a table through `parseTable` on the CSV path,
    so an unreadable body silently exported a zero-row file over
    whatever the user picked in the save dialog. Now refuses
    (`tableBodyToCsv`). The `.json` path stays byte-verbatim — it is
    the one operation that can still round-trip the user's bytes back
    out of the app.

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
  - *As built:* the scene rules moved to a dependency-free
    `state/excalidrawScene.ts` (`parseExcalidrawScene` / `openScene`), so
    `node --test` drives them without the vendor chunk;
    `isExcalidrawBody` is now defined as `parseExcalidrawScene(…) !== null`
    rather than re-stating the checks. `captureUpdate:
    CaptureUpdateAction.IMMEDIATELY` is the load-bearing argument — the
    parameter defaults to `EVENTUALLY`, which does NOT make the update its
    own undo step, so without it the user has an agent's drawing and no
    keystroke back to theirs. `appState` is deliberately not applied
    (presentation: pushing it would move the user's camera as a side effect
    of an edit), and `addFiles` runs before `updateScene` so a malformed
    file entry throws before the scene changes.
  - **It also closed A5's hole in this editor.** `kindForFile` routes a
    `.excalidraw` file on its extension without reading the content
    (`documents.ts:122-124`), and the old reader coerced any JSON into a
    scene — so a corrupt or foreign file opened as a BLANK canvas and the
    first `onChange` serialized that blank over it. Same class as the
    table hole A5 exists to close, found by writing B3's predicate. The
    editor now opens read-only with the A5 notice and suppresses both
    persistence and export.
- **B4 — table.** Add the `useEffect([value])` reconcile
  (`TableEditor.tsx:29`); `isTableBody` (`table.ts:57-64`) before
  commit — the silent-empty class (`table.ts:40-50`) must be
  unreachable from agent input.
  - *As built:* the reconcile ships with an **echo guard** the plan line
    does not mention and cannot work without — the grid re-emits its own
    body on every mutation, so `value` changes for two different reasons
    and re-parsing the echo would rebuild the row/column identities the
    user is typing into, caret included. The decision is
    `reconcileExternalTable` in `state/table.ts`, pure and tested.
  - **`isTableBody` before commit was NOT added, on merit.** A5 already
    made `parseTable` return `readOnly` for a body it cannot read, and
    `validateAuthorBody` refuses `INVALID_TABLE` before an agent write
    reaches the editor at all — a third check in `mutate` could not change
    any answer, and an unreachable guard is indistinguishable from dead
    code to the next reader. What B4 *did* need was a guard the plan could
    not have foreseen, because nothing external could change the grid
    before it: a read-only placeholder must never go on the undo stack,
    since `undo` serializes whatever it pops. That is the same
    silent-empty class arriving through undo rather than through a click.
  - `rendersFromBody('table')` becomes **true**, which moves the A4 rung
    for tables from `applied_store_only` to `applied_live`. The defensive
    `rejected` arm of `applyStateFor` was made explicit at the same time:
    it used to answer conservatively only by the accident that no
    body-reactive kind had a live target.
- **B5 — figure / markdown.** Already reactive; figure applies dry-run
  `renderFigure`; markdown `append` reuses the insert semantics
  (`AuthorSurface.tsx:352-366`).
  - *As built:* markdown `append` shipped with lane A
    (`composeAuthorBody`), so B5 is the figure dry run. It cannot live in
    `validateAuthorBody` — that function is pure and synchronous by
    design, and a renderer is neither — so `executeAuthorRequest` became
    **async** and the dry run is step 4's second half, injected through
    `AuthorIO.renderFigure`. The host binds the same `renderFigure` the
    figure pane uses, so an agent's body is judged by exactly what the
    user's screen will run rather than by a second, kinder validator.
  - Ordered after the no-op check (re-rendering a body the document
    already has cannot change the answer, and mermaid/vega are not cheap)
    and before the editor call. A figure with no `spec` has no renderer to
    ask and is accepted — absence of a judge is not a verdict.
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
  - *As built* (`state/drawioOps.ts`): the grammar, the id-match rule,
    the cascade and the root-cell protection are upstream's
    (`applyDiagramOperations`); the implementation is not a port, for
    two reasons that share a root — our caller writes into a document
    the USER owns, where upstream's writes into one an LLM just
    generated.
    - **All-or-nothing.** Upstream applies what it can and returns the
      failures alongside the result, so a five-op batch with one bad id
      still writes four changes. Here the first failing op aborts the
      batch and `doc.body` is never touched: a partially applied batch
      is a document the user did not ask for, and "some errors" is not
      what an agent needs to hear about it. Same judgement C1 made
      about `autoFixXml`.
    - **Not a DOM round trip.** `XMLSerializer` rewrites attribute
      order, self-closing form, the declaration and whitespace, so a
      one-cell edit comes back differing on nearly every line — into
      the user's linked file, the revert diff and whatever VCS they
      keep it under. This edits the document as TEXT at element
      granularity, so every byte outside the named cells survives.
      That also makes the module DOM-free, which is why the piece
      deciding what gets deleted from a user's diagram has unit tests
      at all: `DOMParser` does not exist under `node --test`.
    - The DOM still gets the last word — the composed body goes back
      through `validateAuthorBody` → `prepareDiagramBody` →
      `validateMxCell`, so ops are not a way around the validation the
      replace path gets.
  - *Three rules upstream's context did not need:*
    **(a)** a multi-page `<mxfile>` is refused rather than half-served
    — draw.io scopes ids per page, so `querySelector('root')` silently
    edits page one and reports success for a cell the user is looking
    at on page three; **(b)** `<object>`-wrapped cells are addressable,
    because indexing `mxCell` only makes every cell with custom
    properties invisible, and "not found" for a box on screen is the
    least useful refusal available; **(c)** a delete naming a cell an
    earlier op in the same batch already cascaded away is a no-op,
    while one naming an id that never existed is an error — upstream
    skips every miss silently and so reports success for a typo.
  - *Also:* deleting a cell takes the whitespace it sat on but not a
    comment above it; the CASCADE is reported to the agent rather than
    `console.log`ged, because deleting one box can remove six cells;
    the approval card counts CHANGES for this mode, since 340 bytes of
    op payload reads as a trivial edit next to a rewrite's byte count.
  - `merge` (B1's deferred draw.io action) stays unused: it can only
    add, so delete-with-cascade cannot be expressed with it. Ops
    resolve to a complete body and ride B1's existing `load`.
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
  - *As built:* `assembleRawFocus` takes the wall's own `selected` +
    `baseline` (not a re-derivation — `useCompareWall` is the one
    visible-runs state every panel reads, so the snapshot cannot disagree
    with the screen) and publishes `{left, right}` **only when exactly two
    runs are selected**, baseline first. The wall is an N-run surface and
    this block has two fields: at three runs a pair would be two thirds of
    the truth with nothing saying so, and an agent cannot tell a truncated
    pair from a real one. Baseline-left because every delta reads "other
    minus baseline" — publishing screen order would invert the direction of
    every number an agent then computes. The whole selection is A6's `wall`
    UIRef, noted there.
  - *Also:* the publisher's stated rule — every store `sourcesNow()` reads
    must be subscribed to `tick()` — is now executable
    (`ui_context_publisher.test.ts`). It was a comment, and its failure
    mode is invisible to every other test: the field assembles, projects,
    and then reaches an agent late or never depending on unrelated UI
    activity.
- **G5 — Record.** Fix the wrong reserved field: `record.dataset_id`
  (`ui_policy.ts:171`) describes an episode-recording surface that was
  never built; replace with `record.record_id` when lane K's records
  land (coordinated policy + `UiRefRecord` + plan-matrix erratum).
  - *As built — the rename only, and that is the whole of G5 today.*
    `record.dataset_id` → `record.record_id` across the policy row, the
    `UiRefRecord` type and the coverage test's fixtures. It stays a
    DECLARED gap: **B2** closes it, not this wedge. The Record surface
    still writes device-local drafts (`useJsonDraft('records')`), and
    `record_id: "rec1754…"` would be a handle no tool can dereference —
    the hub `records` entity B1 shipped uses ULIDs and has no `record_*`
    verbs yet (B3). ADR-062 D-2 is explicit that a UIRef is only a join key
    if the entity behind it is agent-addressable, so publishing the draft
    id would have satisfied the coverage test while telling an agent
    something it cannot act on.
- **G6 — the four gaps G1–G5 did not name.** The coverage invariant
  added with G1–G3 (`electron/src/ui_policy.test.ts`, "every
  allowlisted field is assemblable, or is a DECLARED gap") found that
  the reserved-but-unpopulated set was larger than this lane's audit:
  `agent.session_id` (`focus.fleet.selection` carries `{type,id,name}`
  only), `project.task_id` (the selection names a project or a host,
  never a task), `terminal.agent_id` (`useTerminals` knows the pane,
  not its owner), and `kimiweb.url` (main-side; never reaches the
  renderer publisher). Each is declared in `DECLARED_GAPS` with its
  reason, so they are now visible rather than silent — closing them is
  a follow-up wedge, not a W1 blocker. `tabs.path`/`tab.path` are in
  that list too but are **not** a gap to close: a Read tab addresses
  bytes by reference id, and `FocusSources` omits the field by type so
  no caller can invent one.

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
  - *As built:* four tools in `hubmcpserver/tools_datasets.go`, each
    with its `spec()` + `toolMeta` row (the catalog/spec/meta trio).
    Two names deviate from the line above, both deliberately:
    - `episode_series_get` → **`dataset_episode_series`**. Nothing
      referenced the old spelling (the family did not exist), and the
      `dataset` prefix keeps all four in one place for an agent
      scanning `tools/list`. The shape matches the catalog's existing
      sub-resource reads — `run_metrics`, `plan_steps_list` — where the
      entity leads and the sub-resource follows.
    - `channels?` → **`features?`**. In the reader these are two
      different levels: a *feature* (`observation.state`) contains
      *channels* (the scalar tracks inside it — a joint, a gripper).
      The selector picks features; naming it `channels` would have
      addressed the wrong level and disagreed with the REST query
      param and the host verb, which both spell it `features`.
  - *Also as built:* `datasets_list` reduces each row's digest to its
    headline counts and points at `datasets_get` for the whole thing —
    the `documents_list`/`documents_get` split, for the same reason (a
    full digest is a page of per-feature stats per dataset). `read:
    false` distinguishes a root nobody has folded from a dataset with
    zero episodes. `runs_get`'s `SeeAlso` gained `datasets_get`:
    `runs.dataset_id` is the only link between the two halves, and
    without that pointer nothing in the catalog led to this family.
  - *Removed as shadowed:* a closure-side negative-`episode` guard.
    Mutation-testing it survived — the schema's `minimum: 0` is
    enforced on both dispatch paths before any closure runs, so the
    check could not change an answer. The empty-string guards stay:
    `required` accepts `""`, so those do fire.
  - *Known limit, not introduced here:* `hubClient`'s HTTP timeout is
    30s while the hub's own dataset verb timeout is 60s, so a very slow
    host read fails on the MCP client's clock first. Narrowing the
    window (`limit`, `features`) is the workaround; giving the proxied
    reads their own budget is a J2/W3 call.
- **J2** — writes: `datasets_register {project, host, root_path,
  source, format?}` (idempotent per the identity index),
  `datasets_refresh`, `datasets_update {name?, env_ref?}` (the only
  patchable fields — `handlers_datasets.go:301-308`), tier routine;
  `dataset_export_rrd` wrapping the existing host job
  (`hostjobs.KindDatasetExportRRD`).
  - *As built:* `datasets_register`, `datasets_refresh`,
    `datasets_update`, `dataset_export_rrd` — all routine-tier, plus one
    tool this line did not ask for and the lane needs:
    **`dataset_export_status`**. `dataset_export_rrd` is submit-only by
    design (ADR-058 §3 — decoding every frame does not fit in a
    request); it returns a `command_id` polled at
    `GET /commands/{cmd}`, and there was no MCP tool for that endpoint.
    Shipping the submit alone would have handed an agent a receipt it
    could not redeem. The poll tool is **narrowed to export jobs**: the
    endpoint serves every host command in the team, so answering only
    `dataset_export_rrd` kinds keeps it from becoming a window onto work
    the caller never submitted (`args` for other kinds describes exactly
    that).
  - *Deviations from the line above:* `format?` is not a register
    argument — `datasets_register` takes `name?`/`env_ref?` instead, and
    `format` is derived by the host on refresh, never set by a caller.
    `{project, host}` are spelled `project_id`/`host_id` per §4's own
    rule (a short key addresses the row a tool targets; a context id
    keeps its `_id`), matching `runs_create`.
  - *Not exposed:* `datasets_delete`. De-registering nulls
    `runs.dataset_id` on every run that pointed at the dataset, and
    nothing in this lane needs it — an unwanted row is inert, so the
    expensive-to-undo verb stays out until something asks for it.
  - *Client change:* `hubClient.do` gained a status-returning sibling
    (`doStatus`). Registration is idempotent, and 201-created vs
    200-joined-an-existing-row is the answer the caller asked for; it
    was invisible while every tool discarded the status code.
  - *Posture worth a reviewer's eye:* the write half is
    worker-eligible. Registration is the marginal one — it lets an agent
    assert "a dataset lives at this path on that host", and a bad root
    is inert (the host verbs only read under `<root>/meta` and refuse
    escapes, so the most a guessed path reveals is whether a LeRobot
    dataset sits there). That oracle already exists for anything with a
    shell on the host, which is most workers.
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
→ all Author kinds; navigation; Replay agent-reachable. *J1–J2 merged
2026-08-04 (#513); B3–B5 followed (#516); then D1 and `author_render`.
Remaining: C2, C3, H1–H3.*
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
- CI blind spot: desktop **frontend** node tests run manually
  (`node --test src/state/*.test.ts src/ui/*.test.ts src/ssh/*.test.ts
  src/terminal/*.test.ts`) per wedge. The **electron** package's suite
  DOES run in CI (`desktop.yml` → `npm test`), and two lanes deliberately
  put their load-bearing rules on that side of the line: lane A's consent
  policy, lease and wording live in the electron-free
  `electron/src/author.ts`, and lane G's focus tests live in
  `electron/src/ui_policy.test.ts` so the coverage invariant holds the
  reserved-field class on every PR. The rules a reviewer most wants a
  machine watching are the ones a machine watches.

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

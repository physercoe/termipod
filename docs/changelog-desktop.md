# Desktop Changelog

> **Type:** reference
> **Status:** Current (2026-07-23)
> **Audience:** contributors, operators
> **Last verified vs code:** desktop 2026.723.247 / electron-v2026.723.247

**TL;DR.** Append-only record of what shipped in each **desktop workbench**
release. One section per version, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/) — Added / Changed / Fixed /
Deprecated / Removed. Entries link back to the release commit for forensic
detail.

The desktop app (ADR-050/052/055 — React + TypeScript control plane on an
Electron shell) has its **own version scheme**, independent of the mobile/hub
lane recorded in [`changelog.md`](changelog.md) (`v1.0.x`). Release lane:

- **Electron desktop** — `electron-v*` prerelease tags (ADR-055). The
  `electron-latest` feed is the go-live switch.

The former **Tauri desktop** lane (`desktop-v*` tags) was retired at the M3.4
cutover (2026-07-22); its releases remain in this record below.

**Version scheme.** From `2026.722.211` onward the desktop uses date-based
**CalVer `YYYY.MMDD.HHMM`** (UTC build time — e.g. `2026.722.211` = 2026-07-22
02:11): the version shows the build date/time directly. It is a valid,
monotonically-increasing semver (`> 0.3.87`), so the electron-updater chain from
older `0.3.x` installs is uninterrupted. Earlier releases used sequential semver
(`0.1.0` → `0.3.87`).

Every desktop release appends here — this is the desktop counterpart of the
mobile changelog. Reconstructed records before 0.3.31 are terser (the earliest
point releases carried only a version bump); detail improves from 0.3.31 on.

This complements:
- [`roadmap.md`](roadmap.md) — current focus and Now/Next/Later view
- [`plans/desktop-electron-migration.md`](plans/desktop-electron-migration.md) — the M0–M4 Electron migration plan
- [`decisions/`](decisions/) — append-only ADRs (ADR-050 workbench, ADR-051 tokens, ADR-052 vault, ADR-053 references, ADR-055 Electron)

---

## Unreleased

### Added

- **Inspect (J3) surface — W1.** The Debug tab is rebuilt from a paste textarea
  into a tabbed inspector (and renamed **Debug → Inspect**; the `debug` id is
  unchanged). Ships the shell + a **CodeView** (CodeMirror 6, read-only by
  default with an edit toggle, lazily-loaded language modes, search, fold,
  go-to-line, soft-wrap, copy), a **stack-trace lens** (Python/Rust/Go/JS —
  `file:line` chips jump to the source), and **run-scratch** (run a
  python/bash/node scratch; its stderr feeds the lens). Sources: paste + local
  file. Diff/log/model tabs open with a "coming next" placard (W2/W3/W4).
- **Inspect sources — workspace / SFTP / hub.** An **Open ▾** menu opens files
  from the Author workspace, a remote host over **SFTP** (pick a saved
  connection → browse directories), and a **hub project's docs** (pick a
  project → doc), alongside the local-file picker.
- **Inspect symbol outline.** A code tab gains a right-hand **outline** rail of
  its functions/classes/methods/types (tree-sitter, 12 languages); clicking a
  symbol jumps the editor to its line. Grammars load on demand, fully offline.
- **Inspect diffs — W2.** Two diff viewers, each a lazy chunk. **Patch review**:
  a `.patch`/`.diff` file (or a pasted patch — a scratch that sniffs as a patch
  offers **View as diff**) renders GitHub-style, one collapsible card per file
  with split/unified + wrap toggles and add/delete/rename/binary status
  (`@git-diff-view/react`). **Two-blob compare**: a **Compare ▾** action pits the
  active tab against another open tab or any file (workspace / SFTP / hub /
  local) in an editor-grade side-by-side merge with collapsed unchanged regions
  and bounded-cost diffing (`@codemirror/merge`).
- **Inspect logs — W3.** A **virtualized ANSI log viewer** built for 100 MB+
  training/CI logs (its own lazy chunk — react-virtuoso + anser). A local `.log`
  file is read through a **main-process line index** (`log_open`/`slice`/`search`
  /`stat`/`close`) that does fd reads and never slurps the whole file over IPC;
  a pasted log (a scratch that sniffs as one offers **View as log**) or a remote/
  hub slice renders from memory through the same UI. Features: **follow/tail**
  mode, an **error/warn quick-filter**, **regex search** with a hit rail +
  prev/next, and a **step/epoch marker** jump list. ANSI colours re-map onto the
  theme's terminal tokens (256-palette/truecolour pass through).
- **Inspect models — W4 core.** A **checkpoint inspector** for `.safetensors` and
  `.gguf`, parsed **header-only in the main process** (`checkpoint_inspect` —
  never the tensor bytes; a multi-GB checkpoint is safe). A local model file opens
  to a summary strip (format, total params, file size, dtype histogram), an
  **architecture card** — family + block template (dense-GQA / MoE / MLA / MLA+MoE)
  + component chips (GQA/MLA/MoE/RoPE/RMSNorm/SwiGLU…) with a provenance badge —
  read from an HF `config.json` sidecar (safetensors) or the gguf metadata (an
  honest *recipe-by-name*, not a traced forward pass), a collapsible **namespace
  tree** of tensor names with per-subtree param rollups, and a virtualized
  **tensor table** (name/dtype/shape/params, filterable). safetensors is an
  in-house header parser; gguf uses `@huggingface/gguf`.
- **Inspect models — ONNX (W4 remainder).** The checkpoint inspector now also
  reads `.onnx` graphs. Parsed in the main process with `protobufjs` against a
  minimal schema that **skips the embedded weight bytes** (only graph +
  initializer metadata is decoded); files over 256 MiB with embedded weights are
  refused with a typed error (export with external data files instead). The
  initializers populate the tensor table and namespace tree; the node/op mix
  shows as an **operator summary** above the card. (Also fixes a latent case
  where a config-less safetensors could show a bogus "Unknown / Dense decoder"
  card.) The Model Explorer graph remains a later W4 slice.
- **Inspect models — VRAM estimator (W4b).** The model view now answers "will it
  fit on this host?" with a live estimate: **weights** (exact, params × serving
  precision), **KV cache**, and a rough **activation** term, driven by
  precision / batch / context chips. The KV term is architecture-aware — GQA uses
  the KV-head count, and **MLA** (DeepSeek-family) uses the compressed latent
  (`kv_lora_rank` + rope), which is dramatically smaller; when the latent rank is
  unknown it declines to guess rather than overestimate. Honestly labelled
  approximate (framework overhead/fragmentation are on top). Pure TypeScript;
  the arithmetic is unit-tested against Llama-3-8B (GQA) and DeepSeek-V2 (MLA).
- **Inspect models — layer collapse (W4b).** The namespace tree now folds
  structurally-identical indexed layers into a single **× N** group (aggregate
  params on the header; expand to see one member), so a 61-layer model reads as
  `layers → [0–60] ×61` instead of 61 near-identical subtrees. A "Collapse
  repeats" toggle turns it off. Grouping is by structural signature, so a
  heterogeneous stack (e.g. a few dense layers then MoE layers) splits into
  separate groups, and nested repeats (MoE experts) collapse too.
- **Inspect graphs — Graphviz DOT viewer (W4).** A new **graph** tab kind renders
  Graphviz **DOT** as a pan/zoomable SVG via a WebAssembly Graphviz engine
  (`@hpcc-js/wasm-graphviz`, fully offline). Open a `.dot`/`.gv` file (a DVC dag,
  a saved graph), or paste a `digraph {…}` scratch and hit **View as graph**;
  zoom (wheel/±), pan (drag), fit, copy SVG. This is the shared render substrate
  the code2flow call-graph and torchview model-tracer emit into.
- **Inspect — trace a model graph (W4 Tier 1).** A Python tab gains a **Trace
  model graph** action: a form (entry expression, input shape, depth) + a **venue
  picker** — local Python or a saved SSH host — with a free-text interpreter
  **preset** (`/opt/venv/bin/python`, `conda run -n rl python`,
  `docker exec -i box python`, `uv run python`) and a **Detect** button that
  probes it for torch + torchview. On run, a vendored helper is piped to the
  interpreter's stdin and traces the module **weightlessly on the meta device**
  (torchview — no weights, memory, or GPU), returning a DOT graph rendered in the
  new graph viewer. Requires torch + torchview on the chosen venue (the model
  file's repo must be importable there).
- **Inspect — static call graph (W4).** A **py/js/rb/php** tab gains a **Call
  graph** action: a form (target files/dirs, language or auto-detect) reusing the
  tracer's **venue picker + interpreter preset + Detect** probe. On run, a vendored
  **code2flow** helper (piped over the same `trace_run` IPC locally, `ssh_exec`
  remotely) emits a static call graph — functions as nodes, calls as edges — as
  DOT rendered in the graph viewer. Requires code2flow on the chosen venue (plus
  Acorn / the Parser gem / PHP-Parser for JS / Ruby / PHP; Python needs nothing
  extra); it errors gracefully if the package isn't found.
- **Inspect models — ONNX operator graph, View as graph (W4).** The ONNX parse now
  retains the operator graph (nodes + input/output tensor names, capped at 6000
  nodes, still header/metadata-only — no bytes), and an ONNX model tab gains a
  **View as graph** button that renders the compute graph in the DOT viewer:
  operators as nodes, edges wired by data flow (a producer's output tensor feeding
  a consumer's input; weight/initializer inputs are marked constant, not edges).
  Under the hood the graph is built as a **Model Explorer `GraphCollection`** — the
  schema pinned verbatim to `ai-edge-model-explorer-visualizer` — so the richer
  WebGL graph element is a drop-in renderer swap.
- **Inspect models — interactive Model Explorer graph (W4).** An **Interactive
  graph** button on the model tab opens the model in Google's **Model Explorer**
  WebGL visualizer (`<model-explorer-visualizer>`) — hierarchical, collapsible,
  GPU-rendered, fed the ONNX operator graph (real nodes + edges) or, for
  safetensors/GGUF, the weight namespace hierarchy. The 2.5 MB element + its layout
  web worker + font textures are **self-hosted** (`/model-explorer/*`, a per-build
  sync script; never in the boot bundle — loaded on first open) and served
  same-origin under the `app://` scheme, so it works fully offline with no CSP
  change. A new `megraph` inspect-tab kind carries it. (The WebGL render is
  device-verified.)
- **Inspect — module graph with code sync (W4b).** A **Module graph** action on a
  file-backed Python tab reads the modeling file's class hierarchy (a stdlib-`ast`
  helper on the file's venue — any python3, no torch) and renders an interactive
  **class-composition graph** (React Flow + elkjs): one card per class with its
  bases and submodules, edges for composition (incl. the element class inside
  `nn.ModuleList([...])`) and in-file inheritance. **Clicking a class scrolls the
  code tab to its definition** — the code sync. React Flow + elkjs ride their own
  lazy chunk (never the boot bundle). (The interactive render is device-verified.)
- **Inspect — traced op graph, Trace tier 2 (torch.export).** The Trace-model-graph
  form gains a **Graph** toggle: *Architecture (torchview)* — the existing weightless
  box diagram → DOT viewer — or **Traced ops (torch.export)**, which meta-device-
  exports the model and renders its **traced ATen operator graph** (real nodes/edges,
  per-op shapes, module namespaces) in the interactive Model Explorer. Same venue /
  interpreter picker; Detect probes torch only. (The export runs on the chosen torch
  venue; the render is device-verified.)

## 2026.723.247 — 2026-07-23 · Electron

**Author workbench overhaul (`docs/plans/author-shell-outline-and-canvas.md`):
workspace-only left nav + one categorized New ▾ menu (W1), an Obsidian-style
right-hand markdown outline (W2), and the canvas rebuilt on React Flow with a
JSON Canvas 1.0 (Obsidian-interoperable) body (W3). Read: web-tab bookmarks +
Discover results that survive a tab switch.** Prerelease cut for device testing.

### Added
- **Author · canvas v2 (W3)**: the canvas board is rebuilt on **React Flow**
  (`@xyflow/react`, MIT, lazy-loaded) and its body / on-disk `.canvas` format is
  now **JSON Canvas 1.0** (jsoncanvas.org) — so a board round-trips with Obsidian
  and other JSON Canvas apps. Note cards → `text` nodes, reference cards →
  `link` nodes (`termipod://ref/<id>` + a namespaced `x-termipod.refId`), typed
  edges → labeled edges (`x-termipod.edgeType`); the Zettelkasten wiring (live
  library reference cards, typed edges, backlink inspector) carries over. New
  capabilities: card **resize**, marquee **multi-select** + multi-drag +
  delete-key, **side-anchored** edges, **minimap** + zoom controls + fit-view,
  **groups**, node **colors**, and **undo/redo** (Cmd/Ctrl+Z, suppressed while a
  text field is focused). A **legacy `{cards,edges}` board auto-converts** on
  open (upgraded on first save), and an **unrecognized `.canvas` opens read-only**
  with a notice instead of being overwritten with an empty board — closing the
  data-loss foot-gun. Unknown JSON Canvas fields/node types are preserved through
  a round-trip. *(The interaction layer is device-verified separately.)*
- **Author · markdown outline (W2)**: the markdown editor gains a foldable
  **right-hand outline** (Obsidian-style), reusing the shared `MarkdownOutline`
  rail. Clicking a heading jumps the **source editor** to its line (edit/split),
  scrolls the **preview** to it (read/split), and is best-effort in wysiwyg; the
  rail hides at ≤ 1 heading, and its width + fold state persist. The Read
  surface's reader and note outlines are unchanged.
- **Author · shell cleanup (W1)**: the six standalone "New X" header buttons
  collapse into one categorized **New ▾** menu (Write / Data / Draw / Figure, the
  figure rows driven by the renderer registry), leaving a clean
  `New ▾ · Open · Save · Assistant` bar; the Save button now shows the active
  doc's dirty ●. The left pane is **workspace-only** — the redundant "Open"
  section is gone; its affordances (rename, save-draft-to-workspace, reveal,
  close) moved onto the **tab strip** as a right-click menu, and draft tabs are
  draggable onto the tree to materialize them. Each workspace file that is open
  now echoes its tab's kind icon, dirty ●, and active highlight, and every file
  row gains a kind icon. The pane folds to a slim edge button (state persisted).
  See [`plans/author-shell-outline-and-canvas.md`](plans/author-shell-outline-and-canvas.md).
- **Read · web-tab bookmarks**: the in-app browser bar gains a star that
  bookmarks (or un-bookmarks) the current page, and the start (empty-tab / "Open
  link") page lists the saved sites for one-click reopening. Bookmarks persist
  across restarts (localStorage).

### Fixed
- **Canvas: opening a board no longer marks it dirty**: React Flow reports a
  measurement-only `dimensions` change for every node right after mount; the
  editor serialized on it, rewriting the body of any file-backed `.canvas` the
  moment it opened (dirty ● with no edit — the Excalidraw #315-class bug).
  Measurement events (and inspector backlink selection, which also re-emitted)
  no longer persist; only real mutations do.
- **Canvas: top-level JSON Canvas fields survive a round-trip**: unknown fields
  *inside* nodes/edges were preserved, but unknown fields at the document's top
  level (a future spec version's extras) were dropped by parse→serialize. The
  parsed root object now rides along and every save writes it back.
- **Canvas: resize and Clear are undoable**: a NodeResizer drag never pushed an
  undo snapshot (Cmd/Ctrl+Z skipped straight past it), and Clear bypassed
  history entirely — undo after Clear restored a stale board, losing the edits
  since the last snapshotted mutation. Both now snapshot before mutating.
- **Discover results survive a tab switch**: the Discover pane unmounts when you
  switch to Library mode or open a reader/web tab, which cleared the last search.
  The query + results now live in a module store, so returning to Discover
  restores exactly what was there (session-scoped; not persisted to disk).

## 2026.722.1327 — 2026-07-22 · Electron

**Read: real `<webview>` browser tab + open-access PDF download (incl. downloads
started inside a web tab). Author: Excalidraw sketch editor (figure-plan Phase
C). Native right-click Copy for the EPUB reader, note images, and rendered
figures.** Prerelease cut for device testing.

### Added
- **Read · real in-app browser tab** (read-web-tabs plan W1): the web tab is now
  an Electron `<webview>` guest, not a sandboxed `<iframe>` — a real top-level
  frame, so `X-Frame-Options`/`frame-ancestors` no longer refuse it and arXiv,
  publisher landing pages, GitHub, and Scholar actually load (the iframe was a
  bounce page for nearly every site). Guests run in an isolated, **persistent**
  `persist:webtab` partition (cookies/logins survive restart) with no preload
  bridge, popup-denied, http(s)-only navigation, and permissions denied except
  fullscreen — all enforced main-side in `electron/src/webtab.ts`. The nav bar
  drives the guest's **real** history (back/forward/reload/address), and the tab
  re-titles from the page title. New **"Open link"** header button and a
  tab-strip **"+"** open a blank tab with an autofocused address bar (a web tab
  no longer needs a reference link); **Ctrl/Cmd+L** focuses the address bar. A
  `webtab` proxy connection and a Settings → Network **"Clear web-tab browsing
  data"** button close the proxy/privacy loops. The old `frame_check` preflight +
  "refused" panel are removed (replaced by a real `did-fail-load` error pane).
- **Read · download open-access PDF into the library** (read-web-tabs plan W2):
  wherever a reference or discovery result carries an open-access `pdfUrl`, one
  click streams it (proxy-aware, 200 MB cap, typed "not a PDF" error for paywall
  landing pages) straight into the managed-attachment layout and records it on
  the reference — **Download PDF** on the Inspector Info + Read tabs and **Add +
  PDF** on a Discover card. Idempotent via a new `Attachment.srcUrl` (a matching
  URL renders an inert "Downloaded"). The download core (`ipc/download.ts`) is a
  pure, unit-tested module. **W2b** — a file downloaded *inside* a web tab is
  paused and offered to the Read surface: with a reference selected, a chooser
  bar attaches it to that reference or saves it to disk; with none selected it
  saves straight to disk.
- **Author · Excalidraw sketch editor** (figure-plan Phase C): a freeform
  hand-drawn sketch surface as a new document kind (`excalidraw`), beside
  `canvas`/`table`/`figure`. Body is the ecosystem-standard `.excalidraw` JSON
  (agent-authorable); Export SVG/PNG. `@excalidraw/excalidraw` loads as a lazy
  chunk (never at boot) with **self-hosted fonts** (offline-first — no esm.sh
  CDN fetch; fonts copied into the build by `scripts/sync-excalidraw-assets.mjs`
  and served from `/excalidraw-assets/`). New-doc "Sketch" button (en + zh),
  `sketch` icon, `.excalidraw`/`.json`-sniff file round-trip. E2E smoke pins the
  lazy-mount + offline-asset-path config.

### Fixed
- **Web tabs enforce http(s)-only at the request layer**: the guest's
  `will-navigate` policy does not fire for programmatic loads — which is
  exactly how the address bar navigates (`webview.loadURL`) — so a typed
  `file:`/custom-scheme URL loaded in the guest. The `persist:webtab` session
  now cancels any non-http(s) top-frame request (`webRequest.onBeforeRequest`),
  closing the loadURL, `src`, and server-redirect paths alike.
- **Switching between two web tabs no longer shows the wrong page**: the
  `BrowserView` rendered unkeyed at a stable JSX position, so React reused one
  component instance (and one guest) across different web tabs. Now keyed by
  tab id.
- **A web tab remembers where you were**: the guest's real navigations are
  written back to the tab (`onNavigate` → `tab.url`), so switching away and
  back resumes the **last** page instead of the URL the tab was opened with —
  a "+" new tab previously snapped all the way back to the empty start state.
  (In-memory page state — scroll, form fields — is still not preserved across
  tab switches; cookies/logins persist via the partition as before.)
- **`.excalidraw` files are reopenable**: the extension was missing from both
  openability allowlists, so a workspace-saved sketch showed in the file tree
  but was click-inert, and the Open dialog filtered it out — breaking the
  Phase C save/reopen round-trip. Added to `AuthorNav`'s `TEXT_EXT` and the
  `doc_open` dialog's `TEXT_EXTS` (which also gained the missed Phase B
  `nomnoml`).
- **Sketch docs no longer re-dirty after save-then-close**: the debounced
  Excalidraw persist kept its flush callback armed after it ran, so unmounting
  the editor re-wrote an identical body and marked a just-saved doc dirty
  again. Flushes are now consume-once; the unmount flush only fires when a
  write is genuinely pending.
- **Right-click Copy now reaches the EPUB reader, note images, and rendered
  figures**: the native context-menu fallback (added when Electron replaced
  WebView2's built-in menu) only covered editable fields and text selections in
  the top document, so three surfaces had no Copy at all. The EPUB reader
  renders in an iframe whose `contextmenu` never reached the window listener —
  it now forwards its own (Copy for a selection; Copy image for a book image).
  A note attachment `<img>` gets **Copy image** via `copyImageAt`. A rendered
  figure (`.figure-preview`/`.md-figure` — mermaid/graphviz/vega-lite/echarts/…)
  is inline SVG, which Chromium's own "Copy image" can't target, so it is
  rasterized to PNG in the renderer and offered as **Copy image**.

### Notes
- Figure-plan Phase B **LikeC4 spike** resolved: no headless `dsl → SVG` path
  (its CLI export runs a headless browser), so it is a Phase C editor-mount
  candidate, not a registry row. **bpmn-js** remains held on its license gate.

## 2026.722.818 — 2026-07-22 · Electron

**M4 Chromium paydown (ADR-055 §6/§7) — post device-test of 2026.722.331.**

### Added
- **E2E test harness** (§7 row 14): Playwright drives the real Electron app under
  xvfb in CI (`desktop.yml` → `e2e` job; specs in `desktop/electron/e2e/`).
  Beyond the boot/bridge/secure-context smoke, it covers three flows: **terminal**
  (a real node-pty round-trip — the CI job rebuilds node-pty for the Electron ABI
  — plus a UI open-local-shell → xterm-mounts check), **draw.io** (`drawio_status`
  round-trip), and **figure export** (SVG→canvas→PNG rasterization). This is the
  gate that lets the remaining guard-deletions be verified against real Chromium,
  not by faith.

### Changed
- **Binary IPC — base64 → raw bytes** (§7 rows 4/5): the file-bytes channels
  (storage/attachment read+write, local file read+write, SFTP read+write) and
  voice PCM frames now cross the IPC bridge as `Uint8Array`/`Buffer` (structured
  clone) instead of base64 strings — no 33% inflation or encode/decode per
  payload, felt in file transfer and voice. PTY + SSH shell data were already
  bytes. An E2E test pins the attachment write→read byte round-trip both
  directions; SFTP/voice ride on device verification (no SSH server / recogniser
  in CI).
- **sizedSvg WebKit shim removed** (§6 row 3, test-first): figure PNG export no
  longer injects explicit `width`/`height` into the rendered SVG — that worked
  around WebKit reporting `naturalWidth === 0` for viewBox-only SVGs. Chromium
  rasterizes them via `drawImage` with explicit dest dims; the helper is now
  `svgSize`, returning just the canvas dimensions. An E2E test pins the capability
  (a viewBox-only SVG rasterizes to real pixels).
- **blob-iframe guard-deletion** (§6 row 2, test-first): corrected the stale
  WebView2 rationale comments in the reader/artifact viewers to the real,
  shell-agnostic reason (the pdf.js/epub.js canvas pipeline is kept because it
  gives a text layer + reflow zoom, not because a retired shell refused
  blob-iframes). An E2E test pins the capability those comments referenced — a
  same-origin `blob:` iframe loads and stays scriptable (what the HTML reader's
  zoom needs; what WebView2 refused).
- **Unique ids now use `crypto.randomUUID`** (canvas boards, library items,
  annotations, file transfers) — the renderer serves from the secure `app://`
  origin, so the monotonic-counter fallbacks written for the non-secure
  `tauri://` scheme are gone (§7 row 12).
- **Vite build target pinned to `chrome120`** (§7 row 13): the app runs on the
  Chromium the Electron shell bundles, so esbuild keeps modern syntax instead of
  down-levelling it — the entry chunk drops ~44 kB (2,583 → 2,539 kB).

### Fixed
- **`proxyFetch` no longer bypasses a configured proxy on request failure**: the
  direct-fetch fallback was meant for "undici module missing" but its `catch`
  also wrapped the proxied request itself, so any network/proxy error silently
  retried the request over a DIRECT connection — leaking deliberately-proxied
  sync/download traffic and masking a down proxy as working. Live request errors
  now propagate; only an unloadable undici or an unusable proxy string degrades
  to direct.

## 2026.722.331 — 2026-07-22 · Electron

**Tauri lane retired (ADR-055 M3.4).**

### Removed
- **The Tauri shell is gone.** Deleted `desktop/src-tauri/` (the 5.4k-line Rust
  core), the `desktop-release.yml` / `desktop-v*` release lane, and the `tauri`
  CI job. Removed the `@tauri-apps/api`, `@tauri-apps/plugin-process`,
  `@tauri-apps/plugin-updater`, and `@tauri-apps/cli` frontend dependencies.
- **Tauri→Electron updater handoff** (`state/handoff.ts` + the Settings handoff
  prompt) removed — the small Tauri install base migrates by manual download.

### Changed
- **Frontend bridge is Electron + browser only.** `src/bridge/` no longer
  imports the Tauri SDK; `ShellKind` narrows to `electron | browser` and the IPC
  / event / updater types are defined locally to match the Electron preload.
  Removed the now-dead Tauri branches from the hub transport, SSE reader,
  discovery HTTP, and the draw.io scheme mapping (all take the direct-`fetch` /
  Electron path).
- **Packaged-bundle icons** moved from `src-tauri/icons/` to `electron/assets/`
  (`icon.icns` / `icon.ico` / `icon.png`), rewired in `electron-builder.yml`.
- **`src/ssh/tauri.ts` renamed to `src/ssh/native.ts`** (+ its docstring): the
  SSH/SFTP bridge routes through the Electron main process, not a Tauri core.

## 2026.722.252 — 2026-07-22 · Electron

**Sync-down connection refresh + graceful update check.**

### Fixed
- **SSH connections now appear right after a vault sync-down** (they previously
  stayed empty until an app restart). The always-mounted terminal panel that
  hosts the connections nav re-reads the list on a new `termipod:vault-imported`
  broadcast instead of only at mount.
- **Update check no longer errors when the update feed isn't published yet.** A
  404 on the (not-yet-promoted) `electron-latest` feed is treated as
  "up-to-date" rather than surfacing "Update failed: … Cannot find channel".

## 2026.722.211 — 2026-07-22 · Electron

**Date-based version scheme (CalVer).**

### Changed
- Desktop versions are now **`YYYY.MMDD.HHMM`** (UTC build time) instead of
  sequential semver — the version shows the build date/time directly. Still a
  valid, increasing semver (`> 0.3.87`), so auto-update from older `0.3.x`
  installs is uninterrupted. Applies to both the `desktop-v*` (Tauri) and
  `electron-v*` (Electron) lanes.

## 0.3.87 — 2026-07-22 · Electron

**Windows Electron fixes — vault sync, native right-click, proxy.** First
paydown pass after the M3.1 packaging turned green.

### Fixed
- **Vault sync-down on Windows** no longer fails with
  `ERR_UNSUPPORTED_ESM_URL_SCHEME` ("protocol 'd:'"). The vault crypto WASM is
  now loaded via a `file://` URL — a bare `D:\…` path is rejected by Node's ESM
  loader (the drive letter reads as a URL scheme). This had broken *every*
  `vault_*` operation on the Windows Electron build (sync, recovery-restore,
  opening migrated secrets).
- **System-proxy detection** now uses Chromium's `session.resolveProxy` (Windows
  registry / WPAD / PAC + macOS system config), not env vars alone — a proxy set
  through Windows Settings was previously invisible.

### Added
- **Native right-click menu** (Cut / Copy / Paste / Select-All) for editable
  fields and text selections. Chromium ships no default menu (WebView2 did); a
  renderer fallback defers to in-app custom menus so there are no double menus.
- **Configured proxy is now applied** to the WebDAV / folder / S3 / Zotero /
  draw.io transports (via an undici `ProxyAgent`), not just detected.

## 0.3.86 — 2026-07-21 · Tauri (M0) + Electron (M3.1 prerelease)

**Electron migration M0 + figure-renderer registry.** The final Tauri feature
release before the Electron shell takes over; all M0 work is behavior-neutral
under Tauri. An `electron-v0.3.86` prerelease also shipped here — the first
green three-OS Electron packaging build (installers + update feed).

### Added
- **M0.1 runtime-agnostic shell bridge** (ADR-055) — every native `invoke`/
  `listen` funnels through one seam; the Tauri SDK becomes a lazy chunk.
- **M0.2 migration data egress** — `termipod.*` localStorage snapshots to
  `state-v1.json` so user data survives the WebView2→Chromium profile change.
- **M0.3 updater handoff hook** — a dormant path to offer the Electron installer.
- **Author figure-renderer registry** — Mermaid, Graphviz, Vega-Lite (Phase A);
  nomnoml, WaveDrom, ECharts (Phase B).

### Fixed
- M0 review fixes: hub REST/SSE proxy made Tauri-specific via `shellKind`,
  updater shell-guards, egress close-flush; figure open-dialog exts + export
  cancel toast + renderer-cache eviction; session runtime config in the Info tab.

## 0.3.85 — 2026-07-21 · Tauri

**Real transcript tail + session digest.**

### Added
- Session-scoped digest; an agent config/runtime **Info** tab.
### Fixed
- Load the real transcript tail; window-load insight jumps reach unloaded turns.

## 0.3.84 — 2026-07-21 · Tauri

**Transcript session scope + Cancel semantics.**

### Fixed
- Session-scoped feed; composer shows **Cancel** (not kill/Stop); ordinal-keyed
  insight navigation.

## 0.3.83 — 2026-07-20 · Tauri

**Transcript mobile-parity fixes (#332).**

### Fixed
- Composer shows **Send** when idle, **Stop** only mid-turn; insight turn-jump
  and noise filter brought to mobile parity.

## 0.3.82 — 2026-07-20 · Tauri

**Transcript visual redesign (#332).**

### Added
- De-chromed feed, tool-call summaries, code-copy, scroll pill; running-state /
  Stop control, lifecycle overflow, hover actions, timestamps, skeletons, clamp,
  fully i18n'd chrome.

## 0.3.81 — 2026-07-20 · Tauri

**Transcript insight-jump accuracy.**

### Fixed
- Quiescence-based reveal (retired the hydration pin), reserved image box (#331);
  accurate insight-turn jumps via item-height estimate (#349).

## 0.3.80 — 2026-07-20 · Tauri

**ConPTY scrollback fix + epic-tail merges.**

### Fixed
- Tell xterm it's on ConPTY (Windows scrollback pollution).
### Added
- Vault TOTP UI, SSH-key fingerprints + ed25519 keygen; PDF fit-page, 90°
  rotation, hand/pan, ink-drag preview; page memo & preview-debounce perf; more
  modals onto `ui/Modal`; connect-phase terminal UX + SSH split-duplicate.

## 0.3.79 — 2026-07-19 · Tauri

**Reader zoom, EPUB + image annotations, terminal renderer.**

### Added
- Freeform annotations on the image viewer (area + ink); EPUB highlights
  (CFI-anchored) + color palette, underline, notes; zoom for markdown/text/html;
  GPU renderer ladder behind a Rust platform gate (#333); hub kimi-code-ts engine
  family + ACP `configOptions`→mode/model (#335/#336).
### Fixed
- Transcript opens at the last page with no visible scroll (#331); EPUB links
  clickable/jumping (#321).

## 0.3.78 — 2026-07-19 · Tauri

### Fixed
- PDF link/annotation overlays no longer messy while scrolling (#321/#311).

## 0.3.77 — 2026-07-19 · Tauri

**Epic-tail burn-down (a11y, perf, vault, terminal, EPUB).**

### Added
- EPUB reading themes (default/sepia/night); vault recovery-hint prompt +
  reveal toggle; terminal unread-activity dot; table-editor structural undo.
### Fixed
- Human titles + annotation-editor dialog semantics; keyboard-operable context
  menus; stabilized `useT()` identity to unblock memoization.

## 0.3.76 — 2026-07-19 · Tauri

**Epic backlog sweep.**

### Added
- Unified modal layer (`Modal` primitive, dialog semantics, Esc fix); vault
  session lock + autolock, recovery-code copy; design-token governance
  (phantom-token fix + forward-only ratchet); a11y pass (tabs, aria-sort,
  keyboard resize, live regions).
### Fixed
- Annotation undo, empty-`.md` state, `hostOf` dedup (#322); PDF annotations no
  longer block page text/links; library-table virtualization + PDF offscreen
  un-render (#311). Token ratchet no longer counts issue-refs as hex colours.

## 0.3.75 — 2026-07-19 · Tauri

### Fixed
- EPUB CSP fix (blank/flicker); right-click menus on editors & list panes.

## 0.3.74 — 2026-07-19 · Tauri

**Review-backlog sweep** (SSE/SFTP/contrast, toasts, keyboard, a11y, terminal,
vault, PDF, voice, perf, modals) + CSP/secret-cache.

### Added
- Shared modal a11y (focus trap/restore, scroll lock) (#313); voice recording
  HUD + multiline composer + persistent drafts (#323); vault password generator
  + strength meter, clipboard auto-clear (#320); terminal scrollback, clickable
  links, font zoom, find count (#319); keyboard operability + job shortcuts
  (#312); transient toast channel (#315).
### Fixed
- `--accent-text` AA token for light theme + global z-index scale; SFTP overwrite
  confirm (#314); SSE residuals (no 4xx retry, sanitized error body) (#310.4);
  CSP lockdown + clear secret cache on disconnect/switch (#325, #329).

## 0.3.73 — 2026-07-19 · Tauri

**Terminal geometry + SSH host-key TOFU, PTY crash-proofing, in-app prompts.**

### Fixed
- Terminal geometry, PTY mutex-poison crash, SSH host-key TOFU, session leaks
  (#330/#326/#327/#324); WCAG AA contrast + theme FOUC (#317); consistent
  destructive-action confirms (#314); retired `window.prompt` for `PromptModal`
  (#313.3); vault sync-down triggers one macOS keychain prompt, not ~20.

## 0.3.72 — 2026-07-18 · Tauri

### Fixed
- Terminal right-dock shrink (`min-width:0`); Author blank-space menu; show all
  collections.

## 0.3.71 — 2026-07-18 · Tauri

**Read rail split + tag filter; Author file-tree ops.**

### Added
- Tag-pane filter; resizable collection/tag panes in the Read rail; grouped
  terminal connections; Author file-tree operations.
### Fixed
- Terminal scrollbar overlap + resize hygiene; invisible markdown outline;
  Read-tab tag/collection context menus.

## 0.3.70 — 2026-07-18 · Tauri

### Added
- Live N/M file progress on the status-bar sync chips.
### Fixed
- Hide internal Zotero tags (automatic + `/unread`); kimi terminal truncation
  root cause (`letterSpacing`) + resize-loop guard.

## 0.3.69 — 2026-07-18 · Tauri

### Fixed
- One settled fit per resize; native scrollbar gutter; size Windows keychain
  secret chunks in **bytes**, not chars.

## 0.3.68 — 2026-07-18 · Tauri

### Added
- Vault Read-S3 in the TermiPod tab; richer sync status (last time + machine);
  hub records the machine that last pushed the vault.
### Fixed
- Kimi right-edge truncation + resize splash.

## 0.3.67 — 2026-07-17 · Tauri

**Network proxy tab + status-bar chips.**

### Added
- **Network** settings tab — per-connection HTTP proxy for every outbound
  connection; terminal count in the status bar; local agent moves to the
  terminal dock; right-side dock.
### Fixed
- Terminal login-shell PATH, web-font fit (kimi truncation).

## 0.3.66 — 2026-07-17 · Tauri

### Added
- Background Zotero sync + status-bar indicator, neutral "Sync files" label,
  storage-picker start dir.
### Fixed
- Differentiate workspace vs library sync indicators in the status bar.

## 0.3.65 — 2026-07-17 · Tauri

### Added
- S3 backend for Zotero attachment sync.

## 0.3.64 — 2026-07-17 · Tauri

**Author agent panel + workspace background sync.**

### Added
- Run workspace WebDAV/S3 sync as a background job; draft drag-to-workspace +
  right-click; terminal local agent; Author panel for all doc kinds; @-mentions.
### Fixed
- New doc/diagram lands in the open workspace folder; pin live feed to last msg
  through full settle (not a fixed window).

## 0.3.63 — 2026-07-17 · Tauri

### Fixed
- Per-tab Focus selection; transcript lands at last msg on remount; clickable
  Fleet search hits; moveable/resizable Sessions dialog.

## 0.3.62 — 2026-07-17 · Tauri

### Added
- Sessions scope grouping, real titles, right-click rename.
### Fixed
- Transcript lands at the true bottom on open.

## 0.3.61 — 2026-07-17 · Tauri

**Virtualized transcript feed (react-virtuoso).**

### Added
- Virtualized, measured transcript feed; Sessions search, status filter,
  grouping, richer rows; jump to the agent from a Fleet search hit.
### Fixed
- Settle-then-reveal on open; foldable Insight nav.

## 0.3.60 — 2026-07-17 · Tauri

### Fixed
- Defer history render so opening the transcript stays smooth.

## 0.3.59 — 2026-07-16 · Tauri

### Fixed
- Hold the transcript tail as cards hydrate (stop the scroll drift).

## 0.3.58 — 2026-07-16 · Tauri

### Fixed
- Batch secret deletes to cut the macOS keychain prompt storm.

## 0.3.57 — 2026-07-16 · Tauri

**Project documents + deliverable viewing.**

### Added
- **Documents** tab on the project board; view deliverable component content
  (docs + artifacts).
### Fixed
- Bind a steward instead of 422 on start when unbound.
### Changed
- Instant transcript tail paint + background history, sticky bottom (perf).

## 0.3.56 — 2026-07-16 · Tauri

### Added
- Back control to return from a drill-down to the project board; resizable dock,
  Fleet Spawn button, Me→History; foldable/resizable Fleet+Projects nav with
  kind + role subtabs.

## 0.3.55 — 2026-07-16 · Tauri

### Changed
- Split **Projects** into a dedicated tab; the fleet becomes the ops roster.

## 0.3.54 — 2026-07-16 · Tauri

### Changed
- Table canonical on-disk format is now JSON (lossless).

## 0.3.53 — 2026-07-16 · Tauri

### Added
- Canvas & table round-trip as real files; canvas + table/database as Author
  document kinds; soft 64 KB size nudge on large vault items.
### Changed
- Fold the Updates tab into About.

## 0.3.52 — 2026-07-15 · Tauri

### Added
- Vault **TermiPod** tab — app-integration secrets in the vault; S3 backend for
  Author workspace sync.

## 0.3.51 — 2026-07-15 · Tauri

**Workspace WebDAV sync + vault env/scripts + confirm audit.**

### Added
- WebDAV workspace sync (Obsidian-vault style); vault config/env + runnable
  script item types.
### Changed
- Split the 7k-line `app.css` into ordered partials.
### Fixed
- Confirm all destructive actions (audit).

## 0.3.50 — 2026-07-15 · Tauri

### Fixed
- Consolidate secrets into one keychain item — end the macOS prompt storm.

## 0.3.49 — 2026-07-15 · Tauri

### Added
- Vault mini-1Password item manager + generic items in sync.
### Removed
- Command-blocks (OSC-133) — buggy shell integration.

## 0.3.48 — 2026-07-15 · Tauri

### Fixed
- Load `IdentityFile` keys on SSH-config import; editable hosts; Vault settings.

## 0.3.47 — 2026-07-15 · Tauri

**Two-pane SFTP transfer + Account-first settings.**

### Added
- Two-pane local↔remote file transfer; import `~/.ssh/config`; Account-first,
  categorized Settings with an About section.
### Fixed
- Chunk large keychain secrets.

## 0.3.46 — 2026-07-15 · Tauri

### Changed
- Hub identity moved to top-left; terminal redesign.

## 0.3.45 — 2026-07-15 · Tauri

### Changed
- Terminal & Settings become top-level tabs; dropped the titlebar row.
### Fixed
- WYSIWYG toolbar contrast; annotation→hub sync; note-in-tab.

## 0.3.44 — 2026-07-14 · Tauri

**WebDAV file sync + Milkdown WYSIWYG + note-image de-inline.**

### Added
- Milkdown WYSIWYG editor for notes + Author (Layer 3); inline image preview in
  the Markdown source editor (Layer 2); de-inline note images to managed
  attachments (Layer 1); Zotero-compatible WebDAV file sync for storage.
### Fixed
- Resizable + readable outline/TOC in markdown & EPUB readers.

## 0.3.43 — 2026-07-14 · Tauri

### Fixed
- EPUB pane width (real cause: container flex) + markdown outline chrome.

## 0.3.42 — 2026-07-14 · Tauri

### Fixed
- EPUB width (3rd pass), markdown math delimiters + headings outline; quick-open
  button + row indicator show the attachment's actual kind.

## 0.3.41 — 2026-07-13 · Tauri

### Fixed
- EPUB width, note-screenshot render, markdown math/width, library context menu.

## 0.3.40 — 2026-07-13 · Tauri

**PDF screenshots, annotation tags, markdown notes.**

### Added
- Markdown notes + screenshots-into-notes + export (Phase C); annotation tags
  distinct from the comment (Phase B); PDF area screenshot — copy/save image
  (Phase A).

## 0.3.39 — 2026-07-13 · Tauri

### Fixed
- Render markdown attachments as formatted, not raw; draggable Settings,
  attach-remove confirm, reader open button, EPUB width.

## 0.3.38 — 2026-07-13 · Tauri

### Added
- Manage attachments — add/remove, multiple per item.
### Fixed
- Instant ref-link jump + robust dest-page resolution.

## 0.3.37 — 2026-07-13 · Tauri

**Reader polish.**

### Added
- Copy in the context menu, visible links, zebra rows; centered annotation
  tools, editable zoom.
### Fixed
- Removed the redundant PDF title row; modal backdrop z-index (settings scroll +
  read-header bleed).

## 0.3.36 — 2026-07-13 · Tauri

### Added
- PDF viewer polish — right-click menu, split view, larger toolbar, auto-collapse;
  Annotations tab in the PDF left panel (Zotero-style list).

## 0.3.35 — 2026-07-13 · Tauri

**PDF annotations (highlight/underline/note/area/ink).**

### Added
- PDF annotation rendering + tools in the reader (ADR-053 consumer); hub PDF
  annotations as child records of a reference (migration, #308).
### Fixed
- "Show in folder" opens the right path on Windows (normalize separators).

## 0.3.34 — 2026-07-13 · Tauri

### Added
- PDF left panel — Outline + Thumbnails tabs (Zotero-style).
### Fixed
- PDF TOC resize (real root cause) + robust jump + reveal-file button.

## 0.3.33 — 2026-07-13 · Tauri

### Added
- Local agent in a PTY (ConPTY on Windows) — first native runner slice.

## 0.3.32 — 2026-07-13 · Tauri

### Changed
- Unify iconography + tokenize type — app-wide consistency pass.

## 0.3.31 — 2026-07-13 · Tauri

### Fixed
- Pane resize works on Windows (WebView2); true cited-by total; accurate PDF TOC
  jumps; attachment info.

## 0.3.30 — 2026-07-12 · Tauri

**Author CodeMirror 6 + PDF reader overhaul.**

### Added
- Author markdown editor overhaul — CodeMirror 6 (#6); PDF reader — resizable
  TOC, real search highlight, ref links, +notes fix, assistant tab (#1–#5).

## 0.3.29 — 2026-07-12 · Tauri

### Added
- Assistant can drive a local agent, not only a hub agent (#4); Author
  file/workspace tree nav (#2); Read renders EPUB/image/video/audio/text, not
  just PDF.
### Fixed
- Meta/Enrich blank-app crash; draw.war local-file install.

## 0.3.28 — 2026-07-12 · Tauri

**Reference library ↔ hub sync + pdf.js reader + draw.io.**

### Added
- Offline draw.io diagram editor (#2); library ↔ hub Reference entity sync (#4);
  store reference enrichment (hub migration 0063, #306); pdf.js render + text
  layer (selectable text, in-PDF find, copy-to-notes) + navigation/TOC; library
  scraper (citation graph, journal metrics, code/data links); `AgentCompanion`
  panel.
### Fixed
- In-app browser new-tab links.

## 0.3.27 — 2026-07-12 · Tauri

### Added
- Collapsible left rail + right inspector in Read; multi-source discovery (6
  providers) + a real in-app browser window.
### Fixed
- Semantic Scholar rate-limit — retry-with-backoff + optional API key.

## 0.3.26 — 2026-07-12 · Tauri

### Added
- Author J2 — multiple document tabs + on-disk file save/open.
### Fixed
- PDF blocked on WebView2; delete-confirm not showing (Read J1).

## 0.3.25 — 2026-07-12 · Tauri

**Tabbed reader + sortable library.**

### Added
- In-app browser tabs, multiple PDF tabs, sortable library columns; dedicated
  PDF reader.
### Fixed
- Fixed table layout + colgroup (columns ellipsis instead of overflowing);
  persist Zotero storage link; delete-confirm; external-link handling.

## 0.3.24 — 2026-07-12 · Tauri

### Fixed
- Read-body editor went read-only after one character.

## 0.3.23 — 2026-07-12 · Tauri

### Added
- Hub reference-library entity — REST + MCP CRUD (ADR-053, #305).
### Fixed
- Make Zotero PDFs reachable — header button + re-import backfill.

## 0.3.22 — 2026-07-12 · Tauri

### Added
- Read tab — full Zotero fields + resizable panes; open Zotero PDF attachments
  from a linked storage folder.

## 0.3.21 — 2026-07-11 · Tauri

### Added
- J4 **Canvas** — native spatial-thinking board.

## 0.3.20 — 2026-07-11 · Tauri

### Added
- Host detail surface + Zotero library import (J1); Fleet navigator split into
  kind sections.

## 0.3.19 — 2026-07-10 · Tauri

### Fixed
- Active tabs/rows invisible — wrong accent-token pairing.

## 0.3.18 — 2026-07-10 · Tauri

### Changed
- Scope control-plane actions to the Fleet tab.

## 0.3.17 — 2026-07-10 · Tauri

### Added
- Pro SVG job icons; Read tab as a reference library.

## 0.3.16 — 2026-07-10 · Tauri

### Added
- Workbench sidebar — J1–J6 jobs as distinct tabs.

## 0.3.15 — 2026-07-08 · Tauri

### Added
- Live SFTP transfer progress bar.

## 0.3.14 — 2026-07-08 · Tauri

### Fixed
- Don't inject the bash OSC-133 script into `cmd.exe` / PowerShell.

## 0.3.13 — 2026-07-08 · Tauri

### Fixed
- Local PTY — async commands + gated reader (Windows black screen + freeze).

## 0.3.12 — 2026-07-08 · Tauri

### Fixed
- Drop the WebGL renderer — black terminal + freeze on Windows WebView2.

## 0.3.11 — 2026-07-08 · Tauri

### Added
- Persistent terminal dock — local PTY + tabs + OSC-133 blocks.
### Fixed
- Bind the `pty_resize` mutex guard to a local (drop-order borrow).

## 0.3.5 – 0.3.10 — 2026-07-06 → 2026-07-07 · Tauri

Point releases stabilizing the post-0.3.4 UI redesign and the SSH/tmux terminal
(device-test fixes, terminal session-lifecycle fix, deferred-set handling).
Records are version-bump only; see git log for the underlying commits.

## 0.3.4 — 2026-07-06 · Tauri

### Changed
- Elevated UI design language.

## 0.3.3 — 2026-07-06 · Tauri

### Added
- Run charts/media, run+plan edit, criteria create, deliverable send-back.

## 0.3.2 — 2026-07-06 · Tauri

### Added
- Project detail parity — criteria, files, activity, deliverable detail, hero;
  run detail (clickable runs) + project Agents tab; transcript Live filter +
  Insight turn/error navigation.

## 0.3.1 — 2026-07-06 · Tauri

### Fixed
- Register the OS credential store — keychain "No default store" on add-hub.

## 0.3.0 — 2026-07-06 · Tauri

**Governance depth + Phase 4 breadth.**

### Added
- Governance — templates + engine-families read tabs; insights, docs, me,
  search, ratify, run/plan create; governed create paths; project/team channels
  chat; task create + Sessions surface; hub profiles + offline cache; vault UI
  (create/sync/restore) + zero-knowledge vault crypto (Rust port); saved SSH
  connections + key store; OS keychain + SSH key introspection; composer
  attachments (images/files/multimodal); digest dashboard; rich transcript
  rendering (per-kind cards + tool pairing).

## 0.2.2 — 2026-07-06 · Tauri

### Fixed
- Route the GitHub updater through the corporate proxy.

## 0.2.1 — 2026-07-06 · Tauri

### Fixed
- Runs/plans are team-scoped with `?project=` (was 404 on the nested path).

## 0.2.0 — 2026-07-06 · Tauri

### Added
- In-app auto-updater (Tauri updater plugin, signed GitHub releases).

## 0.1.1 — 2026-07-06 · Tauri

### Fixed
- Route hub REST+SSE through the Rust core (CORS); render the shell offline;
  correct release-name expansion + align app version.

## 0.1.0 — 2026-07-05 · Tauri

**First testable build.** The initial desktop control-plane shell (ADR-050/052).

### Added
- WS1 shared DTCG design-token pipeline (ADR-051); WS2 control-plane shell
  (React+TS + Tauri v2 Rust core); WS3 fleet navigator + WS4 transcript reader;
  WS5 always-visible approvals dock; WS6 projects + tasks kanban; WS7 team
  governance + operator admin cockpit; WS8 Tauri installers (Linux/macOS/Windows)
  via CI; Settings + light/dark themes + en/zh i18n; personal-SSH breakglass
  terminal; cross-device zero-knowledge SSH key-vault sync (ADR-052 D-4).

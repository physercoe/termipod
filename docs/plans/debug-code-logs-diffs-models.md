# Inspect tab (né Debug) — code, logs, diffs & model inspectors (J3 round 2)

> **Type:** plan
> **Status:** In progress (2026-07-23) — **W1 (+ sources + tree-sitter outline),
> W2 (diffs), W3 (logs) and W4 core (checkpoint inspector) SHIPPED.** J3 is a tabbed inspector: shell + **CodeView** (CM6,
> lazy modes, search/fold/go-to-line/wrap/copy, `revealLine`) + **stack-trace
> lens** (Python/Rust/Go/JS `file:line` jumps) + **run-scratch** + a right-hand
> **tree-sitter symbol outline** (12 langs). Sources: paste/local/workspace/SFTP/
> hub. **W2:** a **patch viewer** (`@git-diff-view/react`) + **two-blob compare**
> (`@codemirror/merge`), both lazy. **W3:** a **virtualized ANSI log viewer**
> (react-virtuoso + anser) over a **main-process line index** (`log_*` — fd reads,
> never whole-file): follow/tail, warn-filter, regex search + hit rail, marker
> jumps. **W4 core:** a **checkpoint inspector** (`.safetensors`/`.gguf`, header-only
> `checkpoint_inspect` in main — never tensor bytes) → summary + HF/gguf **architecture
> card** (dense-GQA/MoE/MLA) + tree + tensor table; **ONNX** too (protobufjs, op-mix).
> Tab renamed (§0a). Graphs: tracer T1+T2 (torch.export→ME), code2flow, ONNX→graph, Model
> Explorer, W4b module graph — **W4 + Tier 2 COMPLETE** (WebGL/RF/torch device-test);
> **§7a fixtures SHIPPED** (stdlib gens; unit tests pin them — e2e wiring pending).
> **Audience:** principal · contributors
> **Last verified vs code:** W1–W3 + W4 core/ONNX + tracer T1/T2 + call-graph + graphs + ME + W4b + §7a fixtures

**TL;DR.** J3 Debug today is a paste-textarea piped through the Markdown
highlighter (`surfaces/DebugSurface.tsx`, 57 lines). The director's ask: the tab
serves **algorithm/code design · view · analysis · debug, including
model/architecture design & view**. This plan rebuilds it as a **tabbed
inspector surface** over four viewer kinds — **code** (CodeMirror 6 + a
tree-sitter symbol outline + stack-trace lens + run-scratch), **diff**
(GitHub-grade patch review + editor-grade two-blob compare), **log** (a
virtualized ANSI viewer with a main-process line index, built for 100 MB+
training logs), and **model** (a checkpoint inspector parsing
safetensors/GGUF/ONNX headers in the main process, an embedded Model Explorer
graph, and a **code→graph tracer**: weightless meta-device torchview/
`torch.export` runs over SSH or the local script runner turn a `model.py`
into an architecture graph). Sources: paste, local file, workspace file, **remote file
over the existing SFTP stack**, hub project doc. One deliberate reversal:
**no Monaco** — CodeMirror 6 (already shipped for markdown) carries the whole
surface. Profiling viewers (speedscope/Perfetto) and in-app algorithm stepping
(pyodide) are scoped out to round 3 with the route recorded.

---

## 0. Problem

1. **The surface is a toy.** `DebugSurface.tsx` is a `useDraft('debug')`
   textarea → fenced-block `<Markdown>` highlight with a language select and a
   line count. No files, no diffs, no logs beyond what survives a paste into
   `localStorage`, no model formats. The J3 derivation
   ([desktop-research-surface.md](../discussions/desktop-research-surface.md)
   §3: "reading diffs, stack traces, huge logs, jumping `file:line`,
   correlating a failure against the code that produced it — the director
   *understands and decides*, the agent fixes") is essentially unserved.
2. **The scope grew.** The director now names **model/architecture view &
   design** as part of J3. The standing landscape docs have *zero* coverage of
   model-graph/checkpoint tooling — this plan's research fills that gap.
3. **The standing posture is stale.** [desktop-workbench-jobs.md](desktop-workbench-jobs.md)
   §4.4 and [research-tooling-landscape.md](../discussions/research-tooling-landscape.md)
   §3.2 say "EMBED Monaco + MonacoDiffEditor". That call predates
   `@codemirror/merge`'s 2025–26 maturation and ignores that the app now ships
   the full CM6 stack. Two editor frameworks in one bundle, for a read-mostly
   surface, is the wrong trade (§1).
4. **What J3 is NOT** (adjacent surfaces already own these): agent-transcript
   debugging (Fleet/J7 — shipped, [agent-transcript-debug-and-header-parity.md](agent-transcript-debug-and-header-parity.md)),
   terminals (the Terminal tab), metric comparison (J5 Compare).

## 0a. Naming — the tab is **Inspect** (label-only rename)

"Debug" undersold and misled: the tab has no debugger (no breakpoints, no
stepping), and most of what it serves — reading a checkpoint's layer tree,
skimming a training log, reviewing a diff — isn't debugging. Every wedge below
is literally an *inspector*, and the rail is a row of job verbs (Read · Author
· **Inspect** · Compare · Record); "inspect to understand and decide" is
exactly the J3 persona (the agent fixes). zh label: **检视**.

**Constraint: rename the label, not the id.** `JobId 'debug'` is persisted in
`termipod.workbench.job`, baked into i18n key names (`job.debug`, `debug.*`)
and `useDraft('debug')` — changing it would break restored state for zero
user-visible gain. Change only the en/zh strings for `job.debug` /
`job.debug.hint` (and any surface titles this plan adds); the `J3` tag and all
internal identifiers stay. Docs may keep "J3" as the stable handle.

## 1. Substrate decision — CodeMirror 6, not Monaco (supersedes §3.2/§4.4)

**Decision: the Debug surface is built entirely on CodeMirror 6.** Monaco is
not added. Rationale, from the 2026-07 research pass:

- **We already ship CM6** (`@codemirror/state|view|commands|language`,
  `@lezer/highlight` — the markdown editor). Monaco would be a second editor
  stack: second theming system (hex-in-JS vs our CSS vars), second worker
  regime, a global model registry that punishes the many-small-viewers layout
  an inspector surface wants.
- **The read-mostly case favors CM6 per byte.** Monaco ≈ 2.4 MB of shipped JS
  (Sourcegraph's measurement — it was 40% of their search page; their
  migration writeup, and Replit's, list exactly our concerns). CM6 read-only
  viewing of 100k+-line files is in-spec (the official huge-doc demo renders
  millions of lines); `@codemirror/language-data` gives 150+ lazily-loaded
  languages — nothing comparable exists for Monaco without shipping everything.
- **Monaco's diff advantage closed.** `@codemirror/merge` 6.12 (MIT, 183 KB)
  now does side-by-side *and* unified views, collapses unchanged regions,
  inline diffs, and bounded-cost diffing (`scanLimit`/`timeout`) for huge
  inputs.
- **Monaco 0.56's tree-shakeable ESM shipped days ago** — unproven, and
  `@monaco-editor/react` still defaults to CDN loading (an offline-app
  foot-gun). Not a reason to take the second stack now.

What we give up: LSP-grade IntelliSense (a *writing* feature — agents write;
the director reads) and MonacoDiffEditor's moves-detection. Acceptable.
**Implementation must amend the posture rows** in
`research-tooling-landscape.md` §3.2 and `desktop-workbench-jobs.md` §4.4 to
point here.

Structure beyond highlighting comes from **web-tree-sitter** (MIT, ~200 KB core
WASM) + prebuilt grammars from `@vscode/tree-sitter-wasm` (MIT; python 0.5 MB,
ts 1.4 MB, go 0.2 MB, rust 1.1 MB — lazily fetched assets, never in the boot
chunk): symbol outline, structural folding, enclosing-scope selection.
Cross-file go-to-definition is LSP territory — explicitly out of scope (a
main-process language server is a possible round-4 item, not promised).

## 2. W1 — Inspector shell, CodeView, traces, run-scratch

**Shell.** `DebugSurface` becomes a tab-strip surface (the ReadSurface idiom):
each tab = `{id, kind, source, title}` where `kind ∈ code | diff | log | model`
(dispatch on extension + content sniff, mirroring `state/documents.ts
kindForFile`) and `source ∈ paste | local | workspace | remote | hub`. Session
tab list persists (`termipod.debug.tabs`, metadata only — file content re-reads
on restore; a paste tab keeps today's `useDraft('debug')` storage so nothing
regresses). Open affordances in the header:

- **Paste/scratch** — the round-1 behavior, now one tab kind among several.
- **Open file…** — new `debug_open` Electron dialog command (in
  `ipc/docfile.ts`'s family) with code/log/patch/model filters — `doc_open`'s
  `TEXT_EXTS` (docfile.ts:17) is document-shaped and stays untouched. Text
  reads go through `doc_read` (strict UTF-8 — correct for code; logs and
  checkpoints have their own paths, §4/§5).
- **Workspace file** — reuse `state/workspaceFiles.ts readWorkspaceFile`; the
  Author workspace tree is the finding UI, a "Open in Debug" context-menu item
  is the bridge (small, additive).
- **Remote file (SFTP)** — the existing `ssh/native.ts` stack: `sshConnect` →
  `sftpList`/`sftpRead` (native.ts:56–61) behind a host-picker reusing saved
  connections. Desktop-only (`isShell()` gate).
- **Hub project doc** — `client.getProjectDocText(projectId, path)`
  (hub/client.ts:512) with `listProjectDocs` as the tree.

**`ui/CodeView.tsx` (new).** The workhorse: CM6, read-only by default with an
edit toggle, language from `@codemirror/language-data` (lazy modes — Vite
code-splits them automatically), `@codemirror/search` panel, fold gutter,
go-to-line, wrap toggle, copy, `revealLine()` handle (the Author-outline
precedent). Theme via the CSS-var approach in `ui/MarkdownEditor.tsx:57`. Do
**not** generalize `MarkdownEditor` — it is md-specific by design; CodeView is
a sibling, sharing only CSS.

**Symbol outline.** Right-hand foldable rail, same UX/keys pattern as the
Author outline (`termipod.debug.outlineOpen/.outlineW`): a new
`ui/CodeOutline.tsx` fed by web-tree-sitter symbols (functions/classes/methods
via per-language `tags.scm`-style queries), click → `revealLine`. Grammar WASMs
load on demand per language; unsupported language → rail hides (the ≤1-heading
rule's analog).

**Stack-trace lens.** Hand-rolled parsers — no viable OSS component exists;
Python (`File "x.py", line N, in f`), Rust panics, Go goroutine dumps, JS
stacks are ~200 lines of regex total (unit-testable pure functions). Any
code/log tab with a detected trace gets a frames panel: collapsed
`site-packages`/stdlib frames, ordered innermost-first, each frame a
`file:line` chip. Chip click resolves in order — workspace root → local
absolute path → active SFTP session cwd — and opens a CodeView tab at that
line. This is the "file:line jumps" target the jobs plan named.

**Run-scratch.** For `python`/`bash`/`node` paste tabs: a **Run** button wired
to the existing `state/scriptRun.ts runScript` (local `execFile`, 120 s
timeout, 256 KiB output clamp — surface those limits honestly in the output
pane). Output renders below the editor; stderr feeds the stack-trace lens, so
*paste algorithm → run → click the failing frame* works end-to-end with zero
new execution infrastructure. Desktop-only.

## 3. W2 — Diffs

**Shipped 2026-07-23** (desktop `2026.723.247`+): both tiers below. Patch
splitting is `state/patch.ts` (git + bare-unified + `Index:`, one `<DiffView>`
per file); the viewers are `ui/PatchDiffView.tsx` (+ `@git-diff-view` vendor CSS
mapped to our `--diff-*--` tokens) and `ui/TwoBlobCompare.tsx` (sharing
CodeView's theme via the new `ui/codeTheme.ts`). A scratch that sniffs as a
patch offers **View as diff** / **View source**; **Compare ▾** compares the
active tab against another open tab or any source. The cheap "Diff workspace"
follow-on (`git diff` via `script_run`) and the hub linkage below are **not**
built (deferred / blocked on hub schema).

Two tiers (they solve different jobs):

- **Patch review** — `.patch`/`.diff` files (and pasted patches, sniffed):
  **`@git-diff-view/react`** (MIT, active — v0.1.7 July 2026; shiki-capable,
  Web-Worker diff computation, template mode for 10k+-line patches).
  GitHub-style multi-file split/unified rendering. Pre-1.0 churn is the known
  risk; the Apache-2.0 fallback is `@pierre/diffs`. Lazy chunk.
- **Two-blob compare** — "Compare with…" action on any tab (pick another tab
  or file): **`@codemirror/merge`** on top of CodeView — editor-grade diffing
  (search within, fold, chunk navigation), `scanLimit`/`timeout` set so
  pathological pairs degrade gracefully. Near-free atop the CM6 stack.

Cheap follow-on (in scope if trivial, else drop): "Diff workspace" — run
`git -C <workspace> diff` via `script_run` and open the result as a patch tab.

**Hub linkage — recorded, not built.** Verified 2026-07-23: the hub stores
plans + plan-steps (`spec_json`, `input/output_refs_json` — opaque) and
transcripts, but has **no diff/patch/hunk storage or endpoint anywhere**. The
landscape's "plan-step↔diff-hunk linkage" J3 differentiator
([research-app-product-landscape.md](../discussions/research-app-product-landscape.md)
§13.4) therefore needs hub schema work first — a separate plan; this surface
is where that linkage will eventually render.

## 4. W3 — Logs at scale

**Shipped 2026-07-23.** Built as specified below: a main-process line index
(`ipc/logfile.ts` + the pure `ipc/logindex.ts`, `node --test`) exposing
`log_open`/`log_slice`/`log_search`/`log_stat`/`log_close`; `ui/LogView.tsx` on
react-virtuoso + anser over a windowed line cache (a local file uses the index —
`state/logModel.ts` `IndexedLogModel`; paste/remote/hub render from an in-memory
`MemoryLogModel`); follow/tail, error-warn quick-filter, regex search with a hit
rail + prev/next, step/epoch marker jump list. ANSI colours re-map onto the
`--color-terminal-*` tokens (`state/ansiSpans.ts`, kept out of the boot bundle;
256-palette/truecolour pass through). **Deferred:** remote *live* tail (a hidden
PTY — the Terminal tab covers the interactive case, open question 4); indexing
remote/workspace files (only local files use the fd index today — remote/hub
slices render in memory); the checked-in `fixtures/inspect/log/` device-test
files (the E2E writes its own temp log).

**Build, don't buy** (the buy option, `@melloware/react-logviewer`, is MPL-2.0
and validates the same architecture — kept as fallback only). The substrate
verdict from research: at 100 MB+, neither CodeMirror (needless contenteditable
machinery, ANSI impedance) nor xterm.js (terminal emulator — reflow and
scrollback fight random access + search UX) is right. The winning pattern:

- **Main-process line index** — new `ipc/logfile.ts` command family
  (allowlist pattern, `dispatch.ts:43`): `log_open {path} → {id, size, lines}`
  builds an incremental line-offset index (fd reads, never whole-file);
  `log_slice {id, from, count} → lines`; `log_search {id, pattern, max} →
  [{line, col}]` running async over the raw buffer; `log_close {id}`.
  Unit-tested with `node --test` (the `ipc/download.ts` precedent).
- **Renderer** — `ui/LogView.tsx` on **react-virtuoso** (already a dep; the
  jump/settle logic in `surfaces/AgentTranscript.tsx` is the in-repo
  precedent) + **anser** (MIT, 40 KB) for ANSI→tokens.
- **Features**: follow mode (tail), error/warn quick-filter, regex search with
  a hit rail, jump-to-line, **step/epoch marker detection** (configurable
  regex, default matches `step|epoch|iter \d+` shapes) → a jump list, so "go
  to the loss spike around step 40k" is one click from J5.
- **Remote logs**: quick slice via `sshExec("tail -n 2000 …")` into a log tab;
  full file via `sftpRead` → temp file → `log_open`. **Live remote tail is
  deferred** (needs a hidden PTY session; the Terminal tab covers the
  interactive case today — open question 4).

## 5. W4 — Model & architecture inspector

**W4 core SHIPPED 2026-07-23.** Delivered: header-only checkpoint parsing in the
main process (`ipc/checkpoint.ts` pure parsers — safetensors hand-parser +
`@huggingface/gguf`, `node --test`; `ipc/checkpointfile.ts` `checkpoint_inspect`
handler — **never `localfs_read`**), and `ui/ModelView.tsx` (lazy chunk):
summary strip (params/size/dtype histogram), the HF-`config.json`/gguf
**architecture card** (`state/checkpoint.ts` `classifyArch` → family +
dense-GQA/MoE/MLA/MLA+MoE template + component chips + `config|gguf|tensors`
provenance badge), the namespace tree (`buildTree`, per-subtree param rollups),
and the virtualized tensor table. Local files only (remote/hub checkpoints
deferred). **ONNX SHIPPED 2026-07-23** (see the bullet below). **Not yet built
(later W4 slices):** the Model Explorer graph view, the code→graph tracer
(torchview/`torch.export`), code2flow, and all of W4b (§4b: ×N repeat-collapse,
VRAM estimator, AST code-sync, elkjs). The rest of this section is the
still-pending design for those.

The research gap this plan fills. Two findings frame it: **(a)** the `netron`
npm package is an **unrelated abandoned project** — a supply-chain-style trap;
real Netron (MIT, excellent format breadth) is embeddable only by vendoring its
`source/` tree behind an iframe, undocumented and version-pinned — held as a
spike, not round 2. **(b)** Google **Model Explorer**'s visualizer ships as a
real npm custom element (`ai-edge-model-explorer-visualizer`, Apache-2.0,
7.1 MB unpacked, React demo upstream) — WebGL-instanced rendering built for
50k-node graphs, **fully offline once `worker.js` + `static_files/` are
self-hosted** (our `scripts/sync-excalidraw-assets.mjs` is the asset-sync
precedent).

**Checkpoint inspection (the round-2 core).** New tab kind `model` for
`.safetensors`/`.gguf`/`.onnx`. Parsing lives in the **main process** — a new
`ipc/checkpoint.ts` with `checkpoint_inspect {path} → {format, metadata,
tensors: [{name, dtype, shape, params}]}` returning small JSON, never tensor
bytes. Critically, **do not use `localfs_read`** (it reads whole files over IPC
with no size cap — a multi-GB `.safetensors` would OOM the renderer):

- **safetensors** — in-house parser, ~50 lines: 8-byte LE u64 header length +
  UTF-8 JSON `{name: {dtype, shape, data_offsets}}` + `__metadata__`; fd-read
  the header bytes only. (npm's `safetensors` package is 2023-dead junk —
  write it, with fixture-file unit tests.)
- **GGUF** — **`@huggingface/gguf`** (MIT, 367 KB, actively maintained —
  v0.4.3 July 2026): typed metadata + `tensorInfos`, reads locally in Node.
- **ONNX** — **SHIPPED 2026-07-23.** `protobufjs` (runtime `protobuf.parse`, no
  build step) against a **minimal vendored schema** inlined in `checkpoint.ts`
  that deliberately omits `raw_data` + the `*_data` bulk fields, so the decoder
  *skips* embedded weight bytes (verified: a 2 MiB `raw_data` decodes with the
  field absent). Field numbers pinned to `onnx.in.proto` (verified 2026-07-23).
  Input capped at 256 MiB with a typed "too large — re-export with external data"
  error (embedded-weights only; external-data models stay small). We parse the
  graph + initializer metadata: the initializers become the tensor table, and
  the node/op mix becomes an **operator summary** (`ops: op_type→count`, a chip
  row above the arch card). `parseOnnx` in `ipc/checkpoint.ts`; `node --test`
  round-trips a protobufjs-encoded fixture (incl. the skipped `raw_data`); the
  E2E smoke round-trips through the **bundled** `main.cjs`. (Chosen over the
  plan's original build-time `protobufjs-cli` static module: same result, no
  generated artifact, no CI build-chain change; protobufjs stays main-side —
  788 KB `main.cjs`, absent from the renderer bundle.) The `classifyArch` gguf
  path was also hardened to require real `general.architecture`, so ONNX /
  config-less safetensors no longer emit a bogus "Unknown / Dense decoder" card.
- **PyTorch `.pt`/`.pth`** — **not round 2**: it's ZIP + pickle with no
  maintained JS parser. Recorded routes: vendor Netron's pickle VM, or a Rust
  crate → wasm-bindgen (nodejs) in main — the vault-wasm precedent
  (`ipc/vault.ts:38` computed-path dynamic import). Until then the UI says so
  and points at the SSH-side path below.

**Inspector UI**: summary strip (file size, total/trainable params, dtype
histogram, quant types for GGUF) · **namespace tree** (split tensor names on
`.` → collapsible `model.layers.N…` hierarchy with per-subtree param counts) ·
virtualized tensor table (name/dtype/shape/params, filterable).

**HF `config.json` sidecar (zero-install architecture card).** When a
`config.json` sits beside the checkpoint (the HF layout), parse it in the
renderer and show an architecture card: family (`architectures[0]`), layer
count, hidden/head/KV-head dims, vocab, context length — and render the
nominal block diagram purely from config, no Python. **The template library
must cover the dominant open families by name**: Llama/Mistral (dense GQA
decoder), Qwen2/3 (dense + MoE), DeepSeek V2/V3/R1 (**MLA** attention + MoE
with shared experts — a structurally different diagram), Kimi K2
(DeepSeek-V3-family MoE), Gemma, Mixtral. These reduce to ~three block
templates (dense-GQA · classic MoE · MLA+MoE) plus component chips (RoPE
variant, RMSNorm, SwiGLU, GQA vs MLA, experts/top-k/shared-experts), keyed
off `model_type`/`architectures`. Tensor names corroborate the template even
without a config (`experts.N.` ⇒ MoE, `kv_a_proj`/`q_a_proj` ⇒ MLA) — the
namespace tree and the card should agree or say why not.
Honest labelling required: this is the *recipe by name*, not traced truth —
custom forward code, patched attention, or adapters are invisible to it; the
code→graph tracer below is the ground-truth path. (GGUF needs no sidecar —
its own metadata carries the same fields.)

**DOT render substrate — SHIPPED 2026-07-23.** The WASM-graphviz render path
that both producers below target is built and standalone-useful: a new **graph**
tab kind (`state/dotGraph.ts` `renderDot` via `@hpcc-js/wasm-graphviz` — the wasm
is inlined in the package, so no asset self-hosting, just the CSP's existing
`wasm-unsafe-eval`; `ui/DotGraphView.tsx` pan/zoom SVG, lazy chunk) renders any
`.dot`/`.gv` file or pasted `digraph {…}` (sniffed by `looksLikeDot`, **View as
graph**). `node --test` covers the sniff + a real DOT→SVG render. The code2flow
call-graph and the torchview tracer (both needing a Python venue) now only have
to *produce* DOT and hand it here.

**Graph view.** ⚠️ First step **DONE**: the exact `GraphCollection` JSON schema is
**pinned verbatim** to `ai-edge-model-explorer-visualizer` v0.1.2's
`common/input_graph.ts` + `common/types.ts` (fetched from source, verified
2026-07-23) — `GraphCollection → Graph → GraphNode{namespace, incomingEdges,
inputs/outputsMetadata, attrs}`, `IncomingEdge{sourceNodeId, sourceNodeOutputId,
targetNodeInputId}`, `MetadataItem`, `KeyValue`.

**ONNX → graph SHIPPED 2026-07-23.** The ONNX parse now retains the operator graph
(`checkpoint.ts` `OnnxGraphData`: nodes + input/output tensor *names*, capped at
6000 nodes, metadata-only — no bytes). `state/modelGraph.ts` (pure, `node --test`)
converts it to a schema-faithful `GraphCollection` — index-based node ids, edges
wired by matching a producer's output tensor name to a consumer's input, namespace
from the node name's path, initializer inputs flagged `const` in `inputsMetadata` —
and a `graphCollectionToDot` bridge renders it **now** in the existing DOT viewer
(a **View as graph** button on an ONNX model tab). The `GraphCollection` is the
exact input the WebGL element will consume, so that element becomes a pure renderer
swap.

**Model Explorer WebGL element WIRED 2026-07-23** (device-test pending). The
`<model-explorer-visualizer>` custom element (`ai-edge-model-explorer-visualizer`
v0.1.2) is **self-hosted** — `scripts/sync-model-explorer-assets.mjs` copies
`main_browser.js` + `worker.js` + `static_files/*` → `public/model-explorer/`
(gitignored, per-build; tree-sitter precedent), wired into `dev`/`build`.
`state/modelExplorer.ts` injects the IIFE script once and points its globals
(`window.modelExplorer.workerScriptPath` = `/model-explorer/worker.js`,
`assetFilesBaseUrl` = `/model-explorer/static_files`) — set **after** the script runs,
since the IIFE resets `window.modelExplorer = {}` last. `ui/ModelExplorerView.tsx`
(lazy chunk — the 2.5 MB element is NEVER in the boot bundle) re-inspects the
checkpoint, builds the `GraphCollection` (ONNX op graph via `onnxToGraphCollection`,
else the weight namespace hierarchy via `checkpointToGraphCollection`), and mounts the
element. A new **`megraph`** inspect-tab kind (local-only, carries the checkpoint path)
+ an **Interactive graph** button on the model tab. **CSP needs no change** —
`script-src 'self' 'unsafe-eval'` + `worker-src 'self' blob:` + `img-src 'self'` (font
PNGs) already cover same-origin `app://` assets. **On-device verification pending**
(WebGL + the layout web worker render only in a real renderer): confirm the worker
loads same-origin under `app://`, WASM-in-worker isn't blocked by CSP, and the font
textures resolve — the parts a headless build can't exercise.

**Inspect from code — PyTorch `model.py` → graph (director's ask).**
**Tier 1 SHIPPED 2026-07-23** (the trace itself needs a torch venue; the plumbing
is verified). A Python tab's **Trace model graph** action opens a form (entry
expression, input shape, depth) + a **venue picker** (local `trace_run` IPC / a
saved SSH host via `ssh_exec`) with a per-venue interpreter **preset** and a
**Detect** probe. The vendored torchview helper (`state/traceCore.ts`
`TORCHVIEW_HELPER`) is piped to the interpreter's **stdin** (params via env, never
interpolated), runs `draw_graph(..., device='meta')`, and prints DOT wrapped in
sentinels (`extractDot` survives interpreter warnings / SSH stderr-folding); the
DOT renders in the graph viewer. `electron/src/ipc/trace.ts` `trace_run` (spawn +
stdin + multi-word argv-split, 120 s cap) is `node --test`-verified against the
real `python3`; `traceCore.ts` (extract + remote-command assembly) is unit-tested;
the helpers `py_compile` clean. **Tier 2 SHIPPED 2026-07-23** (device-test pending):
the Trace form gains a **Graph** toggle — *Architecture (torchview)* [Tier 1] vs
*Traced ops (torch.export)* [Tier 2]. Tier 2 runs `torch.export.export(model, args,
strict=False)` on the meta device and walks the FX graph (`state/traceExportCore.ts`
`TORCH_EXPORT_HELPER`, all `node.meta` reads guarded) into a flat node list —
namespace from `nn_module_stack`, edges from `all_input_nodes`, shapes from
`node.meta['val']`. `exportToGraphCollection` ([[modelGraph]]) turns that into the
Model Explorer `GraphCollection`, which opens in the **`megraph`** tab (a `paste`
body carrying the collection, vs the checkpoint-path variant). Detect probes **torch
only** (torchview not needed). Pure core `node --test`-verified; the export itself
needs a torch venue (device-test — torch ≥ 2.1).

No mature tool statically parses `nn.Module` source into a graph (verified — an
AST can't resolve config-driven layer construction in general; the scoped
exception for known HF modeling files is W4b's span-extraction below). The ecosystem
answer is **weightless tracing**: execute the definition on the `meta` device,
so even LLM-scale models graph with **no weights, no memory, no GPU**. Two
tiers, both empirically verified against torch 2.13:

- **Tier 1 (default): torchview** (MIT; sole extra dep is the pure-Python
  `graphviz` package). `draw_graph(model, input_size=…, device='meta',
  depth=…, expand_nested=True)` → `.visual_graph.source` is a **DOT string,
  produced with no graphviz binaries installed** → rendered client-side by
  the already-shipped `@hpcc-js/wasm-graphviz`. Module-hierarchy view with a
  depth knob — how humans read architectures.
- **Tier 2 (opt-in "deep graph"): `torch.export`** (`strict=False` is the
  2026 default; meta-instantiated model + meta example inputs export fine) +
  Model Explorer's `PytorchExportedProgramAdapterImpl` → JSON on stdout →
  the same embedded visualizer as checkpoints. Op-level ATen graph with
  shapes/dtypes. ⚠️ Two pins for the helper: the adapter module name carries
  an upstream typo (`pytorch_exported_program_adater_impl`) and is internal
  API — pin `ai-edge-model-explorer`'s version; and its `print_tensor` calls
  `.cpu()` on meta constants (crashes) — ship the known 5-line monkeypatch
  (the serverless-JSON pattern is what ExecuTorch's `visualize_with_clusters`
  does officially).

**UX**: a `.py` code tab gets a **Trace model graph** action → small form
(entry expression, e.g. `Model(dim=512)`; input shape; tier) → venue picker:
**SSH host** (`sshExec`) or **local Python** (`script_run`, 120 s cap
surfaced). GPU hosts rarely have torch on bare `python` — it lives in a
venv/conda env/docker container — so the venue is **host + interpreter
preset**: a persisted per-host free-text command (`/opt/venv/bin/python`,
`conda run -n rl python`, `docker exec -i trainbox python`, `uv run …`), with
a **Detect** action that probes candidates via `-c "import torch, torchview"`
and marks the usable ones. The vendored helper script is **piped to that
command over stdin**, which works uniformly across all of the above; stdout
is DOT or Model Explorer JSON; failures (missing torch/torchview, import
errors) render as the script's stderr in the output pane. **Import-locality
rule** (honest constraint): the model file's repo must be importable on the
chosen venue — a tab opened over SFTP traces on that host against its remote
path (cwd = a user-settable repo root, default the file's directory); a
local file traces on the local venue. We do not copy single files to a
remote host — their imports wouldn't follow. **torchlens** (Apache-2.0, active)
is deliberately NOT used here — it requires a *real* forward pass (it captures
activations); it's recorded for a future "inspect a running model" story.
**torch.fx `symbolic_trace`** is superseded (fails on shape-dependent control
flow — verified).

**Algorithm code → call graph (non-NN)**: **code2flow** (MIT, active,
Python/JS/Ruby/PHP, zero extra Python deps) emits DOT statically — same
WASM-graphviz render path. **SHIPPED 2026-07-23** (the code2flow run needs a
venue with the package; the plumbing is verified). A py/js/rb/php tab's **Call
graph** action opens a form (targets, language/auto-detect) + the **same venue
picker + interpreter preset + Detect** the tracer uses. The vendored helper
(`state/callGraphCore.ts` `CODE2FLOW_HELPER`) writes DOT to a temp `.gv` (no `dot`
binary), reads it back, and prints it wrapped in the tracer's DOT sentinels; it is
piped to the **reused generic `trace_run` IPC** locally (params via `C2F_*` env,
never interpolated) or `ssh_exec` remotely. `state/callGraphCore.ts` (helper +
remote-command assembly, sharing `traceCore.ts`'s `base64ShellCommand`) is
`node --test`-verified; it gracefully errors when code2flow isn't installed on the
chosen venue. pyan3 is GPL-2.0 — not bundled; staticfg/py2cfg (per-function CFGs,
Apache-2.0) are dormant — skip.

**Architecture *design*** — the research verdict is that this space is dead
open-source (ENNUI/Fabrik/PlotNeuralNet dead or GPL; no 2025–26 entrant).
Decision: **design does not get a bespoke Debug-tab editor**. The route is the
Author canvas (React Flow is already a dep; JSON-spec block palette →
PyTorch-skeleton codegen, likely agent-assisted) — recorded as a direction for
a future Author-side plan (open question 5). Debug stays view/inspect.

### W4b — HF source reader (reconciles issue #362, "LLMForge design study")

[Issue #362](https://github.com/physercoe/termipod/issues/362) proposes a
visual model-architecture reader for HF `modeling_*.py` (drill-down graph +
code sync + VRAM estimator). **Adopted into W4 as a follow-on wedge — not a
new surface** (its open Q4): the three-pane layout it borrows (module tree ·
canvas · code) maps 1:1 onto W4's namespace tree, graph view, and CodeView,
inside the Inspect tab's `model` kind. Sequenced **after W4 core** (checkpoint
tables + config card ship first; W4b builds on both). What it adds, and the
issue-vs-plan reconciliation:

- **Adopted — ×N repeat-collapse** — **SHIPPED 2026-07-23** (namespace-tree
  form). Structurally-identical numeric-indexed siblings fold into one `× N`
  node with the aggregate param count on the header; expand shows one member's
  structure (`state/checkpoint.ts` `collapseRepeats`, grouped by a structural
  signature, recursive so MoE `experts.0…N` collapse too; a "Collapse repeats"
  toggle in `ui/ModelView.tsx`, default on; `node --test`). Because it groups by
  signature, a heterogeneous stack (a few dense layers then MoE layers) splits
  into separate groups rather than force-merging — surfacing the architecture.
  The richer "stacked floating child cards" drill-down is the graph/canvas
  view's job (W4b elkjs), not the tree's.
- **Adopted — provenance badges**: every displayed number carries
  `verified` (parsed from checkpoint/AST) or `approximate` (inferred from
  config) — the config card's "recipe, not truth" caveat promoted to per-value
  UI, from day one.
- **Adopted — VRAM estimator** — **SHIPPED 2026-07-23.** Pure-TS arithmetic
  (`state/vram.ts`, `node --test` against Llama-3-8B GQA + DeepSeek-V2 MLA):
  weights (params × serving precision) + KV cache + a rough activation term,
  with live precision/batch/context chips (`VramCard` in `ui/ModelView.tsx`).
  The KV term is family-aware — GQA uses the KV-head count; **MLA** uses the
  compressed latent (`kv_lora_rank` + `qk_rope_head_dim`), a large reduction —
  and it declines to size MLA when the rank is unknown rather than fall back to
  the dense formula (which would massively overestimate). Labelled approximate
  (framework overhead/fragmentation on top). A per-host GPU-memory hint from the
  connections store is a later add.
- **Adopted (scoped) — AST worker for code sync** — **SHIPPED 2026-07-23.** A
  stdlib-only Python `ast` helper (`state/moduleAstCore.ts` `MODULE_AST_HELPER`,
  run over the tracer's generic `trace_run` IPC / `ssh_exec` — any python3 venue,
  no torch) extracts each class's bases, `[lineno, end_lineno]` span, and submodule
  composition (`self.x = Cls(…)`, incl. the local element class inside
  `nn.ModuleList([Block(…)])`). `buildModuleGraph` turns it into a class graph
  (composition + local-inheritance edges; external types like `nn.Linear` stay node
  metadata). A **Module graph** action on a file-backed Python tab opens a `modgraph`
  tab (`ui/ModuleGraphView.tsx`, lazy — React Flow + elkjs); **clicking a class card
  scrolls the modeling file's code tab to its line** (the code-sync). `forward()`
  dataflow stays approximate — the measured truth is the meta-device tracer. Pure
  core `node --test`-verified end-to-end against the real python3.
- **Corrected — the issue's optional `torch.fx` trace**: stale;
  `symbolic_trace` fails on shape-dependent control flow (verified, §above).
  The measured tier is `torch.export` on meta tensors, already specified.
- **Engine (its open Q2)** — **SHIPPED 2026-07-23** (`ui/ModuleGraphView.tsx`):
  **React Flow + elkjs**. React Flow was already a dependency (canvas), so only
  `elkjs` (0.9.3, ~1.4 MB — its own lazy chunk, verified NOT in boot) is new; its
  `layered` layout positions the class cards, RF renders + pans/zooms them (drag &
  manual-connect disabled — it's a read view). The three-backend split holds:
  **RF+elk = the AST class reader** (tens of nodes), **Model Explorer = deep traced/
  ONNX graphs**, **WASM graphviz = torchview/code2flow DOT**. The interactive RF
  render + code-sync verify on-device; the AST extraction + graph build are
  headlessly `node --test`-covered.
- **Family order (its open Q3)**: as the config-card list above —
  llama/qwen/deepseek first (deepseek exercises MLA+MoE; the issue's own
  screenshots are deepseek).
- **Visual language**: per the issue's own stance — borrow patterns, not
  palette; categorical node colors as dark-theme tints
  (`color-mix(… 12–16%, var(--surface))` from existing token hues), keeping
  the single-accent discipline otherwise.

## 6. Round 3+ (recorded, explicitly out of scope)

1. **Flamegraphs** — bundle **speedscope**'s self-contained release build
   (MIT) in an iframe; covers py-spy/pyinstrument/Austin/pprof/Chrome-CPU
   profiles with zero infra. The natural first profiling wedge.
2. **Big traces** — self-hosted **Perfetto UI** iframe (Apache-2.0; iframe +
   postMessage is the sanctioned embedding — the Flutter DevTools pattern).
   This is the *deprecated TensorBoard-profiler plugin*'s official successor
   for PyTorch `trace.json`; chrome://tracing dies ~200 MB. One-time
   build-from-source cost.
3. **memray / torch-memory reports** — self-contained HTMLs; display as opaque
   documents (webview), don't integrate. (memray cannot emit speedscope
   format — confirmed unimplemented upstream.)
4. **In-app algorithm stepping** — pyodide-core (MPL-2.0, 6.8 MB compressed,
   offline-hostable) + `sys.settrace` → a stepping UI. A feature project, not
   a component drop-in. Python Tutor is GPL + moribund — rejected.
5. **Netron breadth fallback** — vendor `source/` behind an iframe for the
   long-tail formats; needs a spike against v9.x internals.
6. **Hub run logs** — the hub has **no** `/runs/{id}/logs` endpoint today; if
   runs grow log capture, LogView is the renderer.
7. **Training-pipeline & RL visualization** — posture recorded (director's
   ask): no dominant embeddable tool exists; the ecosystem splits it and so
   do we. Curves (loss/reward/KL/entropy) → **J5 Compare** on hub metrics —
   RL debugging in practice is curve-reading, J5 not J3. Pipeline-as-DAG
   (data→SFT→RL stages, actor/critic/rollout topology) → authored, not
   extracted: Author canvas/mermaid; where a DVC repo exists, `dvc dag
   --dot-` renders through the same WASM-graphviz path as code2flow.
   Rollout/sample inspection (generations vs rewards — the real RL debugging
   surface) → no open component; a future J5/J3 wedge on hub run tables.
   System/step-time traces → Perfetto (item 2).

## 7. Sequencing & risk

**W1 → (W2 ∥ W3 ∥ W4).** W1 lands the tab shell + CodeView that every other
wedge mounts into; after it, the three wedges are independent lazy chunks and
can be parallel Opus sessions. Within W4: checkpoint tables before graph view.

- **Bundle discipline (review anchor):** `AppShell` imports surfaces eagerly
  (AppShell.tsx:14–27) — every new dep (`language-data` modes, tree-sitter,
  `git-diff-view`, `merge`, `gguf`, Model Explorer) must sit behind
  `React.lazy`/dynamic `import()` *inside* DebugSurface subviews
  (AuthorSurface.tsx:28–42 precedent). Verify the boot chunk in Vite output.
- **IPC discipline (review anchor):** no whole-file reads for logs/checkpoints
  — everything through the new indexed/header-only commands; `node --test`
  coverage for `logfile.ts` + `checkpoint.ts` parsers with binary fixtures.
- **Browser degrade build:** no bridge → local/SFTP/run/log/checkpoint
  affordances hidden (`isShell()`); paste, hub docs, pasted diffs still work.
- **i18n:** en + zh for every string (single-file dict, both maps).
- **Riskiest item:** Model Explorer's JSON schema + asset self-hosting (W4
  graph) — schema-pinning is step one, and the checkpoint inspector is useful
  without the graph if the element disappoints.
- **Known-unknowns flagged in research:** `@git-diff-view` pre-1.0 API churn;
  tree-sitter grammar WASM sizes acceptable but per-language (lazy-fetch
  only); CM6 long-single-line pathology (minified JS) — guard with a
  wrap-off + truncation notice.

## 7a. Device-test example files (ship with the implementation)

Each wedge lands with **example files a device tester can open in two clicks**
— no torch install, no GPU host required to smoke the surface. One shared
folder, `desktop/electron/e2e/fixtures/inspect/`, used by BOTH the e2e specs
and manual device testing (small checked-in files; anything big is a
generator script, never a committed blob):

- **code/** — `sample.py` (a small module with a deliberate bug + a
  `traceback.txt` from running it), `panic.rs.txt` (Rust panic), `panic.go.txt`
  (goroutine dump), `stack.js.txt` — the four stack-trace-lens parsers, each
  with a frame that resolves into `sample.py` so chip-click-jumps are
  testable; `algo.py` for the run-scratch button (prints, then raises — so
  stdout AND the stderr→trace-lens path both exercise).
- **diff/** — `multi-file.patch` (≥3 files: add + delete + modify, one binary
  marker, one rename) for the patch viewer; `left.txt`/`right.txt` for the
  two-blob compare.
- **log/** — `train-small.log` (~200 lines, ANSI-colored, embedded
  `step N`/`epoch N` markers, a WARN and a Python traceback mid-file) checked
  in; `gen-train-log.py <MB>` generator (stdlib-only) to synthesize the
  100 MB+ case on device for the virtualization/search/index gates.
- **model/** — `tiny.safetensors` (~20 KB, a handful of tensors with
  `model.layers.{0,1}.…` namespacing so the tree/×N grouping shows) — plus
  `gen-fixtures.py` (stdlib-only for ALL THREE formats: safetensors is 8-byte
  header-len + JSON, GGUF v3 is a documented LE header, and ONNX is hand-encoded
  protobuf wire format — no torch, no pips; committed outputs stay, each
  <50 KB); `config-llama.json`, `config-deepseek.json`
  (MLA+MoE) — the two card templates + VRAM-estimator families;
  `truncated.safetensors` (header cut mid-JSON) and `not-a-model.bin` for the
  typed-error paths; `toy_model.py` (self-contained `nn.Module`, no external
  imports) as the tracer's entry-expression demo on a torch venue.

The unit tests reuse the binary fixtures directly (`fixtures.test.ts` runs the
`checkpoint.ts` + `logindex.ts` parsers over the committed files under
`node --test`), so device testing and CI pin identical inputs. The e2e smokes
still paste content inline — pointing them at these files needs native
file-dialog mocking, a follow-up when an e2e touches the open-by-path flow.

## 8. Open questions

1. **Tab persistence depth** — metadata-only restore (re-read files on
   activate) vs content snapshots for paste tabs (current `useDraft` only
   covers one). Proposed: metadata-only + keep the single scratch draft.
2. **Trace-chip resolution UX** when a path resolves nowhere — offer "locate
   under workspace…" picker, or silently disable the chip?
3. **Step-marker regex** — settings-configurable per project, or a fixed
   default until asked?
4. **Live remote tail** — hidden PTY session in LogView vs "open in Terminal
   tab" handoff. Proposed: handoff now, revisit after W3 usage.
5. **Architecture-design canvas** — where does the React-Flow block-palette →
   codegen editor live (Author doc kind? figure spec?), and does codegen go
   through an agent? Needs its own plan.
7. **Trace-form ergonomics** — the entry expression + input shape prompt is
   the honest minimum for code→graph; should the helper also auto-detect a
   sole `nn.Module` subclass with a no-arg constructor and pre-fill? And
   should per-file form values persist (`termipod.debug.trace.<hash>`)?
6. **Hub diff storage** — schema for plan-step↔hunk linkage (the §13.4
   differentiator); blocked on hub-side design, separate plan.

## Related

- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) — J3's round-1 shell
  and the posture row this plan supersedes (§4.4).
- [research-tooling-landscape.md](../discussions/research-tooling-landscape.md)
  — §3.2 (Monaco row superseded), §3.4 observability.
- [research-app-product-landscape.md](../discussions/research-app-product-landscape.md)
  — §13.4 non-coder diff review, the eventual hub-linked target.
- [desktop-research-surface.md](../discussions/desktop-research-surface.md) —
  the J3 derivation (§3).
- [author-shell-outline-and-canvas.md](author-shell-outline-and-canvas.md) —
  the outline-rail UX pattern W1 mirrors; the React-Flow canvas the
  architecture-design direction builds on.
- [agent-transcript-debug-and-header-parity.md](agent-transcript-debug-and-header-parity.md)
  — transcript debugging lives in J7, not here.

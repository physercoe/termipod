# Inspect tab (né Debug) — code, logs, diffs & model inspectors (J3 round 2)

> **Type:** plan
> **Status:** Proposed (2026-07-23) — replaces the J3 round-1 paste box with a
> multi-source inspector suite, and **renames the tab Debug → Inspect**
> (director decision; see §0a). Supersedes the "EMBED Monaco" posture for J3
> (see §1 — the 2026 evidence points the other way). Deep-research findings
> (licenses/embeddability verified against npm + upstream repos 2026-07-23)
> are inlined per wedge.
> **Audience:** principal · contributors
> **Last verified vs code:** desktop 2026.722.1327 @ `e734a2c0`

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
- **ONNX** — protobufjs + the current `onnx.proto3` compiled at build time via
  `protobufjs-cli` (the `onnx-proto` npm package lags, 2022). Cap input at
  256 MiB with a typed "weights-embedded model too large" error — big models
  keep weights in external data files; we parse graph + initializer metadata.
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

**Graph view**: the Model Explorer custom element as a lazy chunk, fed
(a) the synthesized namespace hierarchy for safetensors/GGUF (its JSON format
is namespace-hierarchical — exactly this shape), (b) the real node/edge graph
for ONNX. ⚠️ First implementation step: **pin the exact `GraphCollection` JSON
schema from the package's TS types** — research could only verify it
second-hand (the wiki page was unfetchable).

**Inspect from code — PyTorch `model.py` → graph (director's ask).** No
mature tool statically parses `nn.Module` source into a graph (verified — an
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
WASM-graphviz render path, as a "Call graph" action on code tabs, gracefully
erroring if the CLI isn't installed on the chosen venue. pyan3 is GPL-2.0 —
not bundled; staticfg/py2cfg (per-function CFGs, Apache-2.0) are dormant —
skip.

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

- **Adopted — ×N repeat-collapse**: N identical decoder layers render as ONE
  framed card (`× 61`, aggregate param badge) with drill-down as stacked
  floating child cards. This becomes the default rendering for the
  **templated-family view** — Model Explorer's namespace collapsing can't do
  it (it renders `layers.0…layers.N` as siblings).
- **Adopted — provenance badges**: every displayed number carries
  `verified` (parsed from checkpoint/AST) or `approximate` (inferred from
  config) — the config card's "recipe, not truth" caveat promoted to per-value
  UI, from day one.
- **Adopted — VRAM estimator**: pure-TS arithmetic from `config.json` per
  family (weights × dtype + KV cache — where the **MLA templates matter**, MLA
  compresses KV — + activation estimate), with live batch/context chips.
  Answers "will it fit on this host?" in-app; a per-host GPU-memory hint can
  come from the connections store later.
- **Adopted (scoped) — AST worker for code sync**: a stdlib-only Python
  worker (same **interpreter-preset venues** as the tracer — its open Q1)
  parses the modeling file's AST for the class hierarchy + **source spans**,
  so graph-node clicks scroll CodeView and back. This does NOT contradict the
  "no static parsing" verdict above: for *known, regular HF modeling files*
  the AST yields structure and spans reliably; `forward()` dataflow stays
  approximate and is **flagged as such** — measured truth remains the
  meta-device tracer. IR JSON cached per (model, version).
- **Corrected — the issue's optional `torch.fx` trace**: stale;
  `symbolic_trace` fails on shape-dependent control flow (verified, §above).
  The measured tier is `torch.export` on meta tensors, already specified.
- **Engine (its open Q2)**: **React Flow + elkjs** for this view — React Flow
  is already a dependency (canvas W3), so only `elkjs` (~1.4 MB, lazy chunk)
  is new; its compound/nested layout fits the drill-down cards. This does NOT
  replace the other two backends — RF chokes past ~1–2k nodes (the issue
  concedes this), so the split is: **RF+elk = templated/AST reader** (tens of
  visible nodes thanks to ×N), **Model Explorer = deep traced ATen graphs**,
  **WASM graphviz = torchview DOT**.
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
- **model/** — `tiny.safetensors` (~10 KB, a handful of tensors with
  `model.layers.{0,1}.…` namespacing so the tree/×N grouping shows) — plus
  `gen-fixtures.py` (stdlib-only, writes the safetensors by hand — the format
  is 8-byte header-len + JSON; no torch needed) that also emits `tiny.gguf`
  and `tiny.onnx` where those need a lib (`gguf`, `onnx` pips; committed
  outputs stay if <50 KB each); `config-llama.json`, `config-deepseek.json`
  (MLA+MoE) — the two card templates + VRAM-estimator families;
  `truncated.safetensors` (header cut mid-JSON) and `not-a-model.bin` for the
  typed-error paths; `toy_model.py` (self-contained `nn.Module`, no external
  imports) as the tracer's entry-expression demo on a torch venue.

The e2e smokes open these same files, so device testing and CI pin identical
inputs; the unit tests for `checkpoint.ts`/`logfile.ts` reuse the binary
fixtures directly (`node --test` reads them from the same folder).

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

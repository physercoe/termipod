# Desktop Changelog

> **Type:** reference
> **Status:** Current (2026-07-30)
> **Audience:** contributors, operators
> **Last verified vs code:** desktop 2026.730.1242 / electron-v2026.730.1242-alpha

**TL;DR.** Append-only record of what shipped in each **desktop workbench**
release. One section per version, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/) — Added / Changed / Fixed /
Deprecated / Removed. Entries link back to the release commit for forensic
detail.

The desktop app (ADR-050/052/055 — React + TypeScript control plane on an
Electron shell) has its **own version scheme**, independent of the
mobile/hub/host lanes recorded in [`changelog.md`](changelog.md). Release lane:

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

## 2026.730.1242 — 2026-07-30

**J8 Replay lands end to end, architecture diagrams go hybrid-aware, agents can
drive a browser, and the shell splits in two.** `electron-v2026.730.1242-alpha`
(unsigned alpha channel). The largest desktop cut so far — 52 commits since
2026.727.938 — because five plans reached their shipping wedges at once.

### Added
- **J8 Replay — the whole job.** A dataset library rail with a folded digest
  card and a paged episodes table (#448); **Open in Replay** from an Inspect
  `meta/info.json` row (#454); the **episode player** — channel plots against a
  shared cursor (#459) and a multi-camera video grid over a range-serving media
  scheme (#462); a **URDF reader with forward kinematics** plus a robot manifest
  (#465) driving a **3D pose panel** off the episode cursor (#466); the **Rerun
  companion** with a launch policy and manager (#469), an **Export to Rerun**
  action (J8 W4b-1), and a remote export fetched over the director's own SSH
  session (J8 W4b-2); and a run's episodes one step from the player (#468).
  Remote roots read video over SFTP through the SSH-forward wedge.
- **Architecture graph, hybrid-aware.** The arch card classifies hybrid
  linear-attention models (#430) and sizes their KV cache honestly with a
  KV-cache-per-token class (#431); the schematic lays out heterogeneous stacks
  (#432) and renders them as a pattern strip with nested groups (#434); panels
  zoom, carry an annotation layer, and export to SVG/PNG (#435); and an
  architecture can be **compared against another model's config** over a pure
  `diffArchCards` (#433, #436).
- **Agent browser bridge (W1–W3).** MCP-driven webtabs an agent can read
  (#471), then act in — action tools behind a per-spawn opt-in with an audit
  trail (#472) — and finally drive remotely, hub-relayed, with approval cards
  (#474).
- **Split pane (S1–S3).** One pinned secondary surface beside the primary,
  opened by Alt-clicking the activity rail or `Mod+\`, swapped with
  `Mod+Shift+\`, resized by a draggable divider. The focus snapshot describes
  both panes, so an agent asked about "this" resolves against the pane the user
  is actually in.
- **UI context + pointing (D1–D2).** A `ui_get_focus` MCP tool — off by
  default — lets an agent on this host ask what the director is looking at
  (#476), and an annotation overlay lets the director point at it in return
  (#477).
- **Env profiles + sealed secrets on the desktop.** A picker in the spawn sheet
  (#400) and a management UI in Settings (#401); env-profile secrets are sealed
  to the target host at spawn behind a host-key trust dialog (#412), with a
  re-trust flow when a host's key changes.
- **Session teleport.** Move a paused session to another host from the desktop
  (#423), with secret-bearing sessions re-sealed to the target rather than
  refused (#425).
- **SSH parity with mobile.** Jump hosts (`ProxyJump`) and SOCKS5 proxies for
  connections, parsed from and written back to `ssh_config` on import/export.
- **App-level assistant dock** with a status-bar chip, rebindable shortcuts, a
  full ⌘K palette, an author filter, and "compare from roots/repos" (#411,
  #464); Insight boards for projects and hosts.
- **Issues drawer on the run report** (#438), and an environment chip on the
  episode player header plus an `env_ref` row on the run detail (#478).

### Fixed
- **The vault detail pane carried the previous item's secret across a
  selection change** — a real disclosure risk: switching entries could show one
  item's secret under another's name.
- **Terminal right-click owns its context menu**, with honest paste behaviour
  and OSC 52 support, instead of the shell's menu winning.
- **kimi-code config rows that don't exist are dropped** and the plugins path is
  corrected; the Inspect schema pane falls back rather than blanking, and the
  archgraph menu moved onto the shared primitive (#390 review).
- **`ui_get_focus`'s description names the split fields** — it still called
  `surface` "the active surface" after S3 made it the primary pane, which would
  have sent an agent's "this/here" to the wrong half.
- **The Tauri keychain migration read is skipped under `TERMIPOD_E2E`** (#473),
  so the e2e suite stops touching a real keychain.

## 2026.727.938 — 2026-07-27

**Inspect VLA/policy configs, roots context menus, Windows kimi-web fix.**
`electron-v2026.727.938-alpha` (unsigned alpha channel).

### Added
- **LeRobot policy / VLA config support** in the model inspector. A policy
  `config.json` (`pi0`/`pi05`/`pi0fast`/`smolvla`/`vla_jepa`/`act`/…) is a
  different schema from a transformers config — it keys on `type` +
  `input_features`/`output_features` and names its backbone rather than inlining
  it — so it previously rendered nothing. It now gets a dedicated **policy card**
  (sensor I/O, action horizon, named backbone(s), inlined head dims), with an
  honest note that param/VRAM/FLOPS need the backbone's own config.
- **Context menus in the Inspect roots panel** — right-click a root (Diff working
  tree / Rename / Refresh / Copy path / Remove) or the blank panel (Add folder /
  Refresh all / Collapse all).

### Changed
- **Inspect root rows are static** — the hover-revealed action buttons (which
  caused a layout shift on cursor-over) are gone; actions live in the right-click
  menu.
- The **"External UI" notice** on the kimi web panel is now dismissable (persists
  per-device).

### Fixed
- **kimi web on Windows** no longer fails with `'kimi.cmd' is not recognized`
  (previously surfaced as garbled cp936 text). The launcher is now resolved to an
  absolute path against the recovered PATH before spawning; a missing install
  gives a clean English error.

## 2026.727.807 — 2026-07-27

**Inspect model view — FLOPS estimator, config-key robustness, unified panes.**
The config-only model view gains a compute/throughput estimator beside the VRAM
card, reads the newest 2026 model configs correctly, and folds source / schematic
/ parameters into one in-place pane switcher. `electron-v2026.727.807-alpha`
(unsigned alpha channel).

### Added

- **FLOPS / throughput estimator.** A new card beside the VRAM estimator answers
  "how fast will this run, and how long is a training step on this GPU?" — the
  textbook `2·N` (inference) / `6·N` (training-step) matmul FLOPs per token on the
  model's **active** parameter count (MoE fires only its top-k experts), plus the
  causal-attention term that grows with context². GPU presets carry verified dense
  peak TFLOP/s (A100 / H100 / H200 / B200 / RTX 4090) alongside a custom rate,
  compute precision, MFU and device-count controls; inference shows prefill time +
  decode latency, training shows step time + throughput + tokens/day. Pure
  arithmetic, unit-tested (`state/flops.ts`).

### Changed

- **Config view — one in-place `Parameters / Schema / Source` switcher.** The
  config-only model view no longer spawns a fresh schematic **tab** on every
  click (and the two identically-named "View architecture" buttons are gone). The
  three representations are now panes toggled by a segmented control; the entry
  button from a raw JSON tab is renamed **Analyze model**.
- **Interactive architecture schematic.** Was drag-only. Click a block to open a
  detail panel with that block's full config facts (heads / KV heads / head_dim,
  MLA ranks, expert counts, norm epsilon, vocab, tied-embedding); right-click for
  a context menu (copy details / fit to view); click empty canvas to dismiss.

### Fixed

- **Newest 2026 model configs read correctly.** Config readers were brittle to
  key-name evolution: multimodal wrappers nest the language-model config under
  `text_config` (Mistral-Small-3.2, Gemma-3, Llama-4, **Kimi-K2.7-Code**,
  **Qwen3.6**), and **DeepSeek-V4** dropped `kv_lora_rank` (expressing the
  compressed MLA KV via `num_key_value_heads` + `head_dim`) — both cases produced
  a "KV cache needs layer/attention dims" hint on models whose config *does* carry
  the dims. A parse-time `normalizeModelConfig` flattens the nested LM config, and
  the MLA latent falls back to `kvHeads × head_dim` when the explicit rank is
  absent. Verified against live HF configs for DeepSeek-V4-Pro, GLM-5.2,
  Kimi-K2.7-Code and Qwen3.6; family-name mapping extended to the new
  `model_type`s. Regression-tested (`state/checkpoint.ts`, `state/vram.ts`).

## 2026.727.432 — 2026-07-27

**Inspect model view + embedded web UX.** The config-only model view gains a
config-derived architecture schematic and a training-aware VRAM estimator, and
embedded `<webview>` panels (kimiweb + the Read web tab) get a real right-click
menu. `electron-v2026.727.432-alpha` (unsigned alpha channel).

### Added

- **VRAM estimator — training mode + wider precision/context.** The model
  view's VRAM card gains an **Inference / Training** toggle. Training sums the
  standard mixed-precision terms — weights + gradients (compute precision) +
  optimizer states (AdamW / 8-bit Adam / SGD, fp32 master + moments) + the
  backward activation stash (Megatron formula, with a **gradient-checkpointing**
  toggle that collapses it to the per-layer input). Context options extend to
  **256K** and **1M**; precision adds **fp4** and **int8** (selection now tracks
  the dtype label, since fp8/int8 and fp4/int4 share a byte cost). Pure
  arithmetic, unit-tested (`state/vram.ts`).
- **Config-only architecture schematic (Inspect §5a follow-on).** The
  config-only model view (an HF `config.json` from any source) gains a **View
  architecture** button that renders a paper-style transformer block diagram
  synthesized from the config alone — token embedding → a dashed **×N** decoder
  block {norm → attention → norm → MLP/MoE, with residual skips} → final norm →
  LM head — with colour-coded component cards, GQA/MHA/**MLA** attention labels,
  and MoE expert counts. Rendered via React Flow (reusing the module-graph
  pattern; own lazy chunk), a new `archgraph` tab kind. A `config.json` cannot
  yield a true compute/tensor graph (no weights, no ONNX export); this is the
  honest schematic the config *can* describe. Block-diagram spec is pure and
  unit-tested (`state/archSchematic.ts`).

### Fixed

- **Right-click context menu in embedded web views (kimiweb + the Read web
  tab).** A `<webview>` guest runs in its own process, so its right-click
  `context-menu` fires in the main process and never reached the renderer's
  document-level menu fallback (`src/nativeContextMenu.ts`) — the kimiweb panel
  and the Read browser tab had **no** context menu at all (no Copy/Paste). A
  main-side handler (`electron/src/webtab.ts`) now builds a native menu for the
  guest — Cut/Copy/Paste/Select-all over an editable field or a live selection,
  Copy image over a raster image, and Open-in-browser / Copy-link-address over a
  link — with actions targeting the guest's own `webContents` and labels pushed
  from the renderer i18n (`ctx.*`, re-synced on language change). Menu shape is
  unit-tested (`webtab_policy.test.ts`).

## 2026.727.206 — 2026-07-27

**Inspect round 3 — project trees.** The Inspect tab graduates from opening
single files to browsing whole projects across six source kinds, with a
content-search + read-only git lens and a config-only architecture/params
view. `electron-v2026.727.206-alpha` (unsigned alpha channel).

### Added

- **Inspect tab — forge e2e coverage (round 3, §5.9).** The GitHub/HF forge read
  path (ref-pinning → tree fold → lazy 2 MB-capped blob read) now has an
  end-to-end test: the forge base URL is overridable via `localStorage` so a
  Playwright loopback server stands in for the API, and `forge_fetch` allows
  plain-http only to loopback **and only under the e2e harness**
  (`TERMIPOD_E2E=1`) — production stays https-only (the policy is extracted to
  `forgepolicy.ts` and unit-tested both ways). Closes the last
  "recorded, not built" coverage gap on shipped forge code.

- **Inspect tab — analytic params + VRAM in the config-only view (round 3, §5a
  follow-up).** The config-only architecture view now shows an **estimated
  parameter count** computed from `config.json` alone and feeds it into the
  existing MLA-aware VRAM estimator, so a weightless HF release gets a params +
  VRAM readout too. Covers the mainstream open-source decoder shapes —
  dense / GQA / **MLA** attention (DeepSeek-V2/V3), dense / **MoE** FFN (per-
  expert + router + shared experts, with `first_k_dense_replace` mixed stacks),
  gated or legacy non-gated MLP, tied/untied embeddings. Validated against
  Llama-3-8B ≈ 8.0B, Mixtral-8×7B ≈ 46.7B, DeepSeek-V3 ≈ 671B, GPT-2 ≈ 124M, and
  the newest influential MoE releases — Qwen3-235B-A22B ≈ 235B, Kimi K2 ≈ 1.0T,
  GLM-4.5 — which reuse the same field conventions. Legacy configs that omit
  the FFN width (gpt2's `n_inner: null`, falcon/bloom) get the transformers
  4·hidden default, and `multi_query` / `num_kv_heads` spellings of MQA/GQA are
  honoured, so the classics estimate from their *real* HF configs. Badged as an
  estimate (error-tolerant by design).

- **Inspect tab — local git lens (round 3, T4b).** A pinned local root that is a
  git repo shows its **branch + dirty count** on the root row (read-only, via
  system `git status`; hidden when git is absent). When there are changes, a
  **Diff working tree** action opens the `git diff` output in the existing patch
  viewer. No staging, commit, or history — the inspector stays read-only.
- **Inspect tab — content search over local roots (round 3, T4a).** Each pinned
  local root gains a collapsible **Search contents** box: literal or regex search
  over the whole tree, streamed in the main process with hard caps (≤500 hits,
  ≤20k files, ≤1 MB per file; binary files and `node_modules`/`.git`/… skipped;
  every cap surfaced). A `path:line` hit opens the file and scrolls to the line.
  Remote / hub / forge roots keep the name filter only (no recursive remote
  walk).
- **Inspect tab — config-only architecture view (round 3, §5a).** A `config.json`
  opened from *any* source (local, workspace, remote, hub, GitHub, Hugging Face)
  that parses as a transformers config gains a **View architecture** action: it
  renders the same family / block-template / component-chip card as a checkpoint,
  from the config alone — no weights read. A weightless HF model release is now
  fully describable. When a sibling `model.safetensors.index.json` is readable
  from the same source, its tensor-name map corroborates MoE/MLA and its
  `total_size` gives the weights figure. (Analytic params/VRAM from config math
  is recorded for a later slice.)
- **Inspect tab — Hugging Face repo roots (round 3, T3b).** The add-repo dialog
  gains a GitHub / Hugging Face selector: paste a `huggingface.co/org/model` URL
  (or `org/model@rev`) and browse the model repo the same way. The revision pins
  to a commit SHA; the tree is fetched with pagination (capped, banner on
  overflow); text files (config/tokenizer/README) open through the same 2 MB-cap
  reader over `resolve/{sha}/{path}`, and an HF token in the vault unlocks gated
  repos. Weight files are listed but open to the honest too-large placard (a
  config-only architecture view lands next).
- **Inspect tab — GitHub repo roots (round 3, T3a).** Point Inspect at a GitHub
  repo URL (or `owner/repo[@ref]`) and read it. "From GitHub repo…" resolves the
  ref to an immutable commit SHA, fetches the tree once, and pins it as a tree
  root you browse and open files from — every read uses the SHA, so a moving
  branch can't tear the tree. Blobs are capped at 2 MB (a larger file shows a
  placard instead of downloading); an optional access token is stored in the
  **vault** (never `localStorage`, keyed to the forge host) to raise the rate
  limit and reach private repos, with the rate-limit reset surfaced on a 403. In
  the desktop shell every request goes through the proxy-aware `forge_fetch`
  main-process bridge; the plain-browser build fetches the CORS-open API
  directly. Hugging Face repos and a config-only architecture view follow in the
  next wedges.
- **Inspect tab — remote & hub project-tree roots (round 3, T2).** The tree pane
  now pins two more kinds of root beside a local folder: a **remote directory**
  over an existing SSH connection (browse it in the Remote picker, "Pin this
  folder as a root"; one cached SFTP session per host; a failed connect shows a
  retry row without blocking other roots) and a **hub project's docs** (one flat
  fetch folded into the tree, "Pin this project as a root" in the Hub picker —
  and it works in the plain-browser build too). Per-root filter matches the
  source: recursive index (local), the full flat list (hub), or the folders
  you've opened (remote, no recursive remote walk). A checkpoint / model-def
  opened from a remote root keeps its graph + tracer; remote *checkpoints* stay
  on the honest local-only note (a header-fetch follow-on).
- **Inspect tab — project trees (round 3, T1).** The Inspect tab could only open
  *single files*; it now has a **tree pane** for pinning and browsing whole
  folders. "Open folder…" (or the pane's ＋) pins a local root; expand it lazily
  (one directory listed per click), open any file into a viewer tab (compare
  mode included), rename/refresh/remove roots, and filter a root by name (a
  bounded recursive index — hidden files included, `node_modules`/`.git`/… never
  descended, every cap surfaced rather than implied). The pane is resizable and
  foldable, and it feeds features that were starved of a project root: a
  stack-trace frame now resolves against every pinned root, and the model-graph
  tracer defaults its repo-root to the innermost pinned root containing the
  file. Checkpoint / model-graph tabs opened from the Author *workspace* picker
  now inspect too (the gate was "picked via the native dialog"; it is now "the
  path is local"). Remote/hub/GitHub/Hugging-Face roots follow in later wedges.
  Plan: [`plans/inspect-project-trees.md`](plans/inspect-project-trees.md).

### Fixed

- **Canvas note cards can now be dragged.** A note card's body is a full-bleed
  `textarea` carrying React Flow's `nodrag` class (so typing never starts a
  drag), which left the card with no draggable surface at all — only reference
  cards (which have a header) could be moved. Note cards now carry a small drag
  grip strip at the top edge.
- **Canvas right-click context menu.** Right-clicking empty canvas offers "Add
  note here" (dropped at the cursor); right-clicking a card offers recolor +
  delete. The right mouse button is now reserved for this menu (middle-button
  drag still pans).
- **Kimi web now launches on Windows.** The spawn failed with `'kimi.cmd' is not
  recognized …` because a GUI-launched app inherits a minimal/stale PATH that
  misses where `kimi` is installed. The backing-server spawn now rebuilds PATH
  from the platform's source of truth — the login-shell PATH on macOS/Linux and
  the user+machine registry `Environment\Path` on Windows — plus the well-known
  kimi/npm-global bin dirs, before launching.

### Changed

- **Kimi web moved from a terminal sub-tab to an assistant-panel local agent.**
  It's a separate entity, parallel to the local shell and remote SSH — not a
  view of a terminal session. In the assistant panel, source **Local** now has a
  picker: *Terminal CLI* (launch any engine CLI into the terminal dock) or a
  web-UI agent (currently **Kimi web**; opencode and others slot in as registry
  rows later). The terminal's session view drops its web sub-tabs.
- **The attention dock folds.** Like the left nav, the right-hand attention
  panel now has a collapse toggle in its header; folded, it leaves a thin
  re-open rail. The open/closed state persists per tab (Fleet / Projects).

## 2026.724.405 — 2026-07-24 · Electron (alpha)

> **Alpha — unsigned, device-test build.** Re-cut of `2026.724.305` to produce
> the renamed `Desktop-*` installers; not signed or notarized and not for
> distribution (macOS Gatekeeper will quarantine it).

### Changed

- **Installer artifacts renamed `Desktop-<ver>-*`.** The packaged files drop the
  `TermiPod-` prefix and carry the component name instead (`Desktop-<ver>-mac.dmg`,
  `Desktop-Setup-<ver>.exe`, `Desktop-<ver>.AppImage`/`.deb`), matching the
  per-component release-lane split (Hub/Host/Mobile/Desktop). The app's own
  `productName` (window title, macOS app name) stays **TermiPod** — only the
  release file names change; electron-updater's `latest*.yml` regenerates in
  lockstep so the update feed is unaffected.

## 2026.724.305 — 2026-07-24 · Electron (alpha)

> **Alpha — unsigned, device-test build.** Cut to exercise the task-board
> redesign and the Inspect (J3) surface on real devices; not signed or
> notarized and not for distribution (macOS Gatekeeper will quarantine it). The
> signed counterpart ships on a `-beta` tag.

### Added

- **Projects task board — master-detail + rich cards (W1).** The desktop Tasks
  kanban becomes a master-detail split: columns stretch to fill the width (no
  more fixed-220px dead space), cards show priority, a body snippet, live
  assignee pip, result summary and age, and selecting one opens a full detail
  panel (status/priority, assignee + open-transcript, result, timestamps,
  markdown body) inline ≥1100px (modal below). The split is user-resizable and,
  ≥1600px, a third pane previews the assignee's live transcript.
- **Task `in_review` lifecycle (W2).** A new status between blocked and done: an
  agent that terminates with a result now lands the task in **In review**
  (done-when-reviewed), and the detail panel gains **Accept** (→done) /
  **Send back** actions. Any status the client doesn't recognise renders in its
  own trailing column instead of vanishing.
- **Agent-aware kanban drag-and-drop + board filters (W3).** Drag a card between
  columns: into **In progress** opens the agent picker and the spawn drives the
  status (not a raw edit); **Cancelled** confirms; blocked cards (agent-owned)
  can't be dragged. Plus an `Active | All | Cancelled` view tab, a title/body
  search box, and a priority filter.
- **Per-task attempts + project-nav parity (W4).** The task detail lists a
  task's **attempts** — every agent spawned for it, newest first, each linking
  to its transcript — with a **New attempt** action. The Projects left nav rows
  gain the mobile card content: status dot, attention badge (with a
  parent←children rollup), phase pill, open-AC chip, and a phase-weighted
  progress bar, all fed by one team-scope insights read.
- **Task review-feedback loop (W5).** A feedback composer on the task detail
  posts the reviewer's note straight into the **assignee agent's session**;
  **Send back** delivers the note and re-opens the task to re-engage a live
  worker.
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
- **Inspect device-test fixtures (plan §7a).** `e2e/fixtures/inspect/` ships the
  two-click smoke set: buggy `sample.py` + its captured traceback and Rust/Go/JS
  stack fixtures (the four lens parsers), a multi-file patch + two-blob pair, an
  ANSI training log (+ stdlib `gen-train-log.py` for the 100 MB device gates), and
  hand-written `tiny.{safetensors,gguf,onnx}` + malformed cases + family configs +
  `toy_model.py` (stdlib `gen-fixtures.py` regenerates all three formats — no
  torch, no pips). `fixtures.test.ts` runs the real parsers over the committed
  files, so CI pins the same bytes a device tester opens. Also: `log_slice` now
  clamps its line count like `log_search`, and `kimi_k2` joins the architecture-
  card family names.

### Changed

- **Release workflow — alpha / beta channels.** `desktop-electron-release.yml`
  now builds two channels by tag suffix (or the `channel` dispatch input):
  **`electron-v<ver>-alpha`** is **unsigned** — it skips cert import and
  notarization, so a build finishes in minutes instead of waiting ~55 min on
  Apple's notary service, for **device testing** (Gatekeeper quarantines it);
  **`electron-v<ver>-beta`** is **signed + notarized**. The release notes state
  the channel; both remain prereleases.
- **Task auto-derivation flips to `in_review`.** When a worker terminates with a
  result summary the hub now derives **in_review** (was `done`) — completion is
  a human accept, not the agent stopping. The never-overwrite guard extends to
  `in_review`, and notify/digest surfaces learn the status (ADR-029 D-8).

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

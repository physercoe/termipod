# Inspect tab round 3 — project trees: folders, remote dirs, hub projects & GitHub repos

> **Type:** plan
> **Status:** Proposed (2026-07-24)
> **Audience:** principal · contributors
> **Last verified vs code:** origin/main `9baaa07f`

**TL;DR.** The Inspect tab (J3 round 2,
[debug-code-logs-diffs-models.md](debug-code-logs-diffs-models.md)) opens
**single files** — five sources, one file per pick, no way to open *a
directory, a project, or a repo* and move around inside it. This plan adds a
**project-tree pane**: pin roots (a local folder, a remote SFTP directory, a
hub project's docs, a **GitHub repo or Hugging Face model at a ref**),
browse them lazily, open
files into the existing viewer tabs, filter by name, search by content, and
feed the roots into the machinery that already wants them (stack-trace
resolution, the W4 tracer's repo-root cwd, two-blob compare). A weightless
HF release (config/metadata only) gets a real **architecture view**: the
shipped `buildArchCard` classifier is pure TS over `config.json` — §5a adds
the missing entry gate, from any source. Zero new npm
dependencies until the optional git lens; every listing is lazy and capped
(the standing "no uncapped IPC reads" anchor). Sequencing: **T1 local trees →
(T2 remote/hub ∥ T3 forges + §5a config-only views) → T4 search & git
lens**, each an independent wedge on the shipped shell.

---

## 0. Problem — what "cannot inspect a folder/repo" is concretely

Verified against `origin/main 9baaa07f`:

1. **The open model is file-only by construction.**
   `state/inspect.ts` — `InspectSource = 'paste'|'local'|'workspace'|'remote'|'hub'`;
   an `InspectTab` locates exactly one readable blob. Every entry path agrees:
   - `debug_open` (`ipc/docfile.ts:41`) shows a native **file** dialog — no
     `openDirectory`.
   - `WorkspacePicker` (`surfaces/InspectOpen.tsx:51`) is a flat filtered
     *file list* over an eager recursive `workspace_list` — which skips hidden
     files and `SKIP_DIRS` and stops at depth 8 / 5 000 entries
     (`ipc/workspace.ts:18–23`) because it was built for `@`-mentions, not
     inspection.
   - `RemotePicker` *can browse* directories over SFTP but can only *pick a
     file* — the browsing state (connection + cwd) is thrown away with the
     modal; re-opening starts from `.` again.
   - `HubPicker` filters directories **out** (`InspectOpen.tsx:237`,
     `!bool(x,'is_dir')`) and shows a flat path list.
   - There is **no GitHub / URL source at all** — nothing in the desktop app
     fetches `github.com` today.
2. **The absence ripples beyond opening.** Three shipped features are
   root-hungry and starved:
   - **Stack-trace lens**: `resolveFrame` (`DebugSurface.tsx:792`) tries only
     the absolute path, the Author workspace folder, and the origin tab's own
     directory. A traceback from any *other* checkout — precisely the thing
     you're inspecting — dead-ends at the "not found" toast.
   - **W4 tracer import-locality**: the trace form wants a *repo root* cwd and
     currently defaults to the file's directory with a per-file manual
     override (plan §5) — an opened project root is the natural default.
   - **Two-blob compare**: side B comes from the same single-file pickers;
     comparing two files of a repo means two full picker round-trips.
3. **Multi-file reading is the director's actual J3 workload.** The J3
   derivation ("correlating a failure against the code that produced it") is
   a *project*-shaped activity: follow an import, glance at a sibling config,
   check the repo's README — currently each hop is a fresh modal.

**Non-goals** (posture, recorded): Inspect stays **read-only** — no editing,
no git write operations, no checkout/branch switching; the workspace mutation
IPCs (`workspace_new_file`/`rename`/`delete`) stay Author-only. This is an
inspector, not an IDE — "open in Terminal / hand to an agent" remains the
mutation path.

## 1. Substrate — what ships today that this plan reuses

The wedges below are mostly *wiring*, because the substrate exists:

| Need | Existing piece | Notes |
| --- | --- | --- |
| Lazy per-dir local listing | `localfs_list` (`ipc/localfs.ts`) | non-recursive, hidden files **included**, 10 k cap, dirs-first sort — exactly the tree-expand shape |
| Folder picker dialog | `workspace_pick_folder` (`ipc/workspace.ts:66`) | `openDirectory` native dialog, reused verbatim |
| Lazy per-dir remote listing | `sftpBrowse` (`state/inspectSources.ts:55`) | cached one-session-per-connection; the RemotePicker already walks with it |
| Hub project file tree | `listProjectDocs` → `[{path, is_dir, …}]` (`hub/client.ts:529`) | flat list with dir rows — a client-side fold yields the tree; **no hub change needed** |
| Read-one-file, per source | `readSource`/`readFrom` (`state/inspectSources.ts`) | the tree only *locates*; opening reuses the lazy-read tab model unchanged |
| Kind dispatch + dedupe-on-open | `kindForInspectFile`, `useInspect.open` existing-tab match | a tree click is just `pick()` with a path |
| Resizable side pane | `usePanelWidth`/`ResizeHandle` (task-board §6.4) | same component pair as the task detail panel |
| Proxy-aware main-process HTTP | `fetchWith` (`ipc/net.ts`) | the GitHub venue in shell mode |
| Secret storage | vault (`state/vaultItems.ts`) | GitHub token — never `localStorage` |
| e2e HTTP stand-in | PR #373 webtab e2e loopback server | pattern for GitHub-API e2e without the network |

## 2. Model — roots beside tabs, not inside them

A **root** is a pinned, browsable origin; it is *not* a tab and *not* an
`InspectTab` field. New store `state/inspectRoots.ts`:

```ts
export interface InspectRoot {
  id: string;
  source: 'local' | 'remote' | 'hub' | 'github' | 'hf';
  label: string;          // basename / repo@ref; user-renamable
  path?: string;          // local abs root · remote abs/rel dir · hub '' (docs root)
  hostId?: string;        // remote: connection id
  projectId?: string;     // hub: project id
  repo?: { id: string; ref: string; sha: string };  // github: id 'owner/repo' · hf: model id
}
```

- Persisted under a **new** key `termipod.inspect.roots` (metadata only —
  never tree contents). `termipod.debug.tabs` and its `Persisted` shape are
  untouched, keeping the §0a persisted-state compat rule intact.
- Tree node state (expanded dirs, loaded listings) is **in-memory only** —
  every expansion is a fresh lazy list, so a stale tree is one collapse away
  from truth and nothing unbounded is ever persisted.
- Tabs opened *from* a tree are ordinary tabs of the root's source (`local`,
  `remote`, `hub`, and new `github`) — close the root, tabs live on.

**Two `InspectSource` additions**: `'github'` and `'hf'`, with a matching
optional `repo?: {id, ref, sha}` field on `InspectTab` **and** `InspectRef`
(compare sides), and forge arms in `readFrom`. Everything else
(persistence, dedupe, lazy activate-read, compare) generalizes for free.
*Forward-compat note:* a build predating this plan that restores a
forge-sourced tab errors at read time ("source … unsupported") — an
honest error placard, no crash; acceptable.

## 3. T1 — Tree pane + local folder roots

The shell wedge; everything later hangs off it.

1. **Pane.** A collapsible left pane inside `inspect-shell` (before the tab
   column), resizable via `usePanelWidth` (key `termipod.inspect.treeW`,
   clamp ~200–420 px), hidden entirely when there are no roots (today's
   layout is the zero-roots rendering — no regression). Toggle button in the
   surface actions row.
2. **Add a local root.** "Open folder…" in the Open menu →
   `workspace_pick_folder`. If an Author workspace folder is set, offer it as
   a one-click suggestion row (not auto-pinned — open question 1). Roots get
   a context row: rename · refresh (collapse-all) · remove.
3. **Expand-on-demand.** Directory click → `localfs_list` for that directory
   only. Render its `MAX_ENTRIES` truncation honestly ("… N shown, listing
   capped"). Hidden files are **shown** (`.github/`, `.gitignore`, `.env.example`
   are inspection targets — deliberate divergence from `workspace_list`);
   known-heavy dirs (`node_modules`, `.git`, `target`, …the `SKIP_DIRS` set)
   are listed but tagged and never auto-expanded.
4. **Open = `pick()`.** File click routes through the existing pick path:
   kind via `kindForInspectFile(ext, '')`, dedupe via the store's
   existing-tab match, **compare mode included** — when `cmpBase` is armed a
   tree click becomes side B, making "compare two files of one repo" a
   two-click flow.
5. **Name filter.** A filter box per root; matching walks with a **bounded
   background name-index**: reuse the `workspace_list` recursive walk shape
   but as a new `tree_index` IPC — caps (depth 12, 20 000 entries), *includes*
   hidden files, skips `SKIP_DIRS`, returns `rel` paths only. Index built on
   first filter keystroke, cached per root until refresh, truncation surfaced.
   (Not reusing `workspace_list` itself: its hidden-file skip and 5 000-entry
   cap are wrong for inspection, and Author's `@`-mention contract shouldn't
   inherit inspection's caps.)
6. **Feed the starved features.**
   - `resolveFrame` candidate list gains **every local root path** (after the
     workspace folder, before the origin-tab dir) — stack traces from a
     pinned checkout now resolve.
   - The W4 trace form's repo-root default becomes the innermost pinned local
     root containing the file (fall back to the file's dir as today).
7. **Model inspectors work unchanged** — a deliberate consequence of tree
   tabs being ordinary `source:'local'` tabs: a repo's `model.py` gets the
   module graph (W4b) and the torch tracer, and an in-repo
   `.safetensors`/`.gguf`/`.onnx` opens straight into the checkpoint
   inspector (`checkpoint_inspect` is by-local-path). Fix in passing: the
   model/ME-graph tabs gate on `source === 'local'` where they mean "the
   path is local" — a checkpoint opened via the *workspace* picker hits the
   local-only placard today despite having a local absolute path; widen the
   gate to `local | workspace`.
8. **e2e**: real temp directory fixture — no native-dialog mocking needed
   (the §7a recorded gap does not apply; roots can be seeded through the
   store for tests).

## 4. T2 — Remote (SFTP) and hub-project roots

Same tree, two more source arms; no new IPC.

- **Model inspectors over remote roots**: model-def `.py` files keep the
  module graph and tracer (both already run on the remote venue via
  `ssh_exec`, and the pinned root supplies the tracer's repo-root cwd on
  that host); remote *checkpoints* stay on the honest local-only placard —
  the SFTP header-fetch follow-on recorded in round 2 is unchanged by this
  plan, not delivered by it.
- **Remote root** = connection + start directory. In the tree, expansion goes
  through `sftpBrowse` (session cached per connection as today; a failed
  connect renders an error row with retry — never blocks other roots). The
  existing RemotePicker gains a "Pin this folder as root" action on its crumb
  row, so browse-then-pin is one flow. Name filter: **loaded nodes only**
  (no remote recursive walk — cap discipline; recorded limitation).
- **Hub root** = one project's `docs_root`. Fetch the flat
  `listProjectDocs` list once per expand-refresh, fold paths client-side into
  a tree (dir rows exist in the payload). Name filter over the full flat list
  (it's already complete — the one source where filter is exact for free).
- **Browser-degrade build:** local + remote roots hidden behind `isShell()`
  (matching the current Open menu); hub roots (and T3 forge roots) work in
  the plain browser.

## 5. T3 — GitHub & Hugging Face Hub repos (new `github` / `hf` sources)

The headline ask: point Inspect at a repo URL and read it. Two forges, one
source-module seam; GitHub is spec'd in full below, HF Hub differs only where
noted (§5.1).

1. **Add-root dialog.** Accepts `https://github.com/{owner}/{repo}`,
   `…/tree/{ref}[/{subpath}]`, or shorthand `owner/repo[@ref]`. No ref →
   the repo's default branch (from `GET /repos/{owner}/{repo}`).
2. **Ref pinning.** At add/refresh time resolve the ref to a **commit SHA**
   and store both — the tree and every blob read use the SHA, so a root is an
   immutable snapshot (a moving branch can't tear the tree mid-read); the
   root row shows `ref @ shortsha` with a refresh action to re-resolve.
3. **Tree fetch.** One `GET /repos/{o}/{r}/git/trees/{sha}?recursive=1` per
   root (single round-trip, ≤100 k entries / 7 MB server-side). Fold client-
   side; display cap 50 k entries with a truncation banner. If the API sets
   `truncated: true`, degrade to per-directory expansion via
   `GET /repos/{o}/{r}/contents/{path}?ref={sha}` — same lazy shape as local.
4. **Blob reads.** `readFrom` `'github'` arm: `GET …/contents/{path}?ref={sha}`
   with `Accept: application/vnd.github.raw+json` (works for public and
   private with one code path). **Size cap ~2 MB** — larger files render a
   "too large — N MB" placard instead of fetching (the "no uncapped reads"
   anchor applied to the network). In-memory blob cache per root (tab
   re-activate re-reads are frequent), dropped with the root.
5. **Fetch venue.** Shell: a thin `gh_fetch` main-process IPC over the
   existing proxy-aware `fetchWith` (`ipc/net.ts`) — GitHub must honour the
   app proxy like every other outbound transport (the ADR-055 M4 paydown
   lesson). Browser build: direct renderer `fetch` — `api.github.com` and the
   raw content endpoints are CORS-open, so the degrade path genuinely works.
6. **Auth (optional).** A GitHub token (classic or fine-grained PAT) stored
   in the **vault**, never `localStorage`; sent as `Authorization: Bearer`
   when present. Unauthenticated works for public repos at 60 req/h — the
   per-root fetch pattern (1 tree call + 1 blob per open) sits comfortably
   inside that; surface the rate-limit reset time on a 403/429 rather than
   retrying. Private repos require the token; 404-with-no-token hints at it.
7. **Everything downstream is free — except execution-backed viewers.**
   GitHub-sourced tabs get CodeView, outline, and diff/log kinds by
   extension; the model inspectors do **not** apply (no venue to run the
   AST/tracer helpers on, no local path for `checkpoint_inspect` — checkpoint
   blobs also exceed the 2 MB cap by construction). One cheap follow-on,
   recorded not built: the W4b AST helper parses a *single file*, so
   materializing one `.py` blob to a temp file and running the local venue
   would light up the module graph for GitHub model defs. Beyond that, — notably — **two-blob
   compare against a local file**: pin the repo at a PR head ref, compare
   `repo:file` ↔ `local:file`. (Cross-*ref* compare = pin the same repo twice
   at two refs; adequate, recorded as such.)
8. **Out of scope, recorded:** GitLab/Gitea/Bitbucket (different API shapes;
   the source module isolates the seam), PR/issue metadata, commit history
   browsing, git clone (no local git dependency, no write surface, no
   credential helper integration — the API path needs none of it).
9. **e2e**: API base URL overridable (env/setting) → Playwright loopback
   stand-in serving canned tree/blob JSON (the PR #373 webtab e2e pattern);
   pins URL-parsing, SHA-pinning, truncation, and the size-cap placard
   without network.

### 5.1 Hugging Face Hub roots (`hf` source)

Models are released on HF, not GitHub — an HF repo root is the same shape
with different endpoints, and it is what makes §5a's config-only
architecture view land where the configs actually live:

- **Add-root**: `https://huggingface.co/{owner}/{name}` (or `hf.co/…`,
  `…/tree/{rev}` forms); the dialog's forge selector disambiguates bare
  `owner/name` shorthand. Datasets/spaces URLs rejected for now (recorded).
- **Listing**: `GET huggingface.co/api/models/{id}/tree/{rev}?recursive=true`
  (paginated; same display cap + truncation banner as GitHub); revision
  pinned to the resolved commit SHA at add/refresh, like item 2 above.
- **Blob reads**: `GET huggingface.co/{id}/resolve/{sha}/{path}` — same 2 MB
  cap; weight files (`.safetensors`/`.bin`/`.gguf`/`.pt`) are listed with
  their sizes but are **never fetched** — they render an info row (name,
  size, and the §5a index/config-derived facts), not a download.
- **Auth**: HF token in the vault (gated/private repos); anonymous works for
  public repos. CORS-open on both API and resolve endpoints, so the
  browser-degrade build works like GitHub's.
- Same venue split (`gh_fetch` IPC generalizes to a forge fetch), same e2e
  loopback stand-in, same immutable-snapshot semantics.

### 5a. Config-only architecture view (any source — the no-weights HF case)

An HF release is fully describable **without its weights**: `config.json`
carries the architecture, and `model.safetensors.index.json` carries the
full tensor-name map + `total_size`. The classifier for this **already
ships**: `buildArchCard` (`state/checkpoint.ts:139`) is pure TS over a
parsed HF config (provenance `'config'`), and tensor-name corroboration
(`'tensors'`) needs only *names*. What's missing is the entry gate — today
the card is only reachable *through* `checkpoint_inspect` on a local weights
file (ModelView reads the sidecar config beside a checkpoint, never the
reverse), so a weightless HF repo shows a plain JSON code tab.

1. **Detection.** A `code`-kind JSON tab whose body parses and looks like a
   transformers config (`model_type` or `architectures` present) gets a
   runbar action "View architecture" (mirror of the existing paste-tab
   "View source" flip). No auto-hijack — `config.json` is a generic name.
2. **Rendering.** ModelView grows a **config-only mode** (no
   `CheckpointInfo`): ArchCardView + component chips + provenance badges as
   today, an explicit "config-only — no weights read" note, and **no**
   tensor tree/table/histogram. Pure renderer work, zero new IPC — and
   therefore available from **every** source: local, workspace, remote, hub,
   `github`, `hf` (it only needs the text `readSource` already returns).
3. **Index corroboration.** When the sibling
   `model.safetensors.index.json` is readable from the same source, feed its
   `weight_map` keys to the existing tensor-name inference (MoE/MLA
   corroboration exactly as the checkpoint path does) and show
   `total_size` as the weights figure.
4. **Params/VRAM (optional slice).** An analytic param count from config
   math feeding the existing MLA-aware `estimateVram`, badged approximate;
   cut first if the wedge runs long.
5. **Recorded follow-on, not built:** true remote checkpoint headers via
   ranged GET (safetensors' 8-byte-length + JSON header makes a two-request
   header-only fetch possible on HF `resolve/` URLs) — this is the same
   "remote header-fetch" follow-on round 2 recorded for SFTP, now with an
   HTTP venue; it would light the full tensor table for HF roots.

## 6. T4 — Content search + local git lens

1. **Content search** (local roots first). New main-process IPC
   `tree_search`: literal or regex over a root, streamed directory walk with
   hard caps — ≤500 hits, ≤1 MB per file, binary sniff (NUL in first 8 KB) →
   skip, `SKIP_DIRS` skipped by default (toggle), bounded total scan (~20 k
   files) with an honest "search capped" banner. Results panel under the tree:
   `rel:line match` rows → click opens the tab and drives the existing
   `reveal` scroll-to-line mechanism. Remote/hub/GitHub roots: name filter
   only for now (hub could search server-side later; GitHub code-search API
   needs auth and differs semantically — both recorded, not built).
2. **Local git lens** (stretch — cut first if the wedge runs long). If a
   local root contains `.git`: show branch + dirty-count on the root row
   (`git status --porcelain=v2 --branch` via a small `git_info` IPC using
   `execFile`; **system git required, feature hidden when absent** — no
   bundled git, no libgit2 dependency). One action: "Diff working tree" →
   `git diff` output opened as a standard `diff`-kind tab in the existing
   patch viewer. No staging, no commit, no log walking — the read-only
   posture (§0) holds. This is the wedge that serves "inspect the repo an
   agent is working in right now".

## 7. Sequencing & review anchors

**T1 → (T2 ∥ T3) → T4.** T1 lands the pane + root store every other wedge
mounts into; T2 and T3 are independent source arms; T4 rides the T1 walk
machinery. Each is one Opus-session wedge with tests.

- **Cap discipline (review anchor):** every listing/walk/fetch in this plan
  is lazy and capped, and every cap is *surfaced in the UI* when hit
  (truncation rows/banners — silent truncation reads as "covered
  everything"). No recursive eager reads over IPC or network; `tree_index` /
  `tree_search` caps covered by `node --test` with fixture trees.
- **Zero new npm deps** through T1–T3 (tree pane is plain DOM + existing
  ui components; GitHub is `fetch`). T4 adds none either (`execFile` + stdlib
  walk). Anything heavier (react-arborist, isomorphic-git) is rejected —
  bundle discipline anchor from round 2 stands.
- **Store/type ripple check (review anchor):** the `InspectSource` union
  gains exactly two members; grep every `switch`/conditional over `source`
  (`inspectSources.readFrom`, `refOfTab`, pickers, persistence) — a missed
  arm must fail to an error placard, not a silent wrong venue.
- **Secrets:** GitHub/HF tokens in the vault only; tokens never appear in a
  persisted root, a tab, a log line, or an error message.
- **Persisted-state compat (§0a):** new LS keys only (`termipod.inspect.roots`,
  `termipod.inspect.treeW`); `termipod.debug.tabs` shape unchanged; new
  optional tab fields are additive.
- **i18n:** en + zh for every string, both dicts.
- **Browser degrade:** hub + GitHub roots functional; local/remote hidden
  via `isShell()`.
- **Riskiest item:** GitHub API edge cases (truncated trees, rate limits,
  redirects on renamed repos, submodule tree entries — render as inert rows).
  The e2e stand-in server pins the handled set; unhandled shapes must fail
  visibly, not hang the pane.

## 8. Open questions

1. **Author workspace auto-root** — auto-pin the workspace folder as a
   standing root, or keep it one click behind the suggestion row? Proposed:
   suggestion row only (roots are the user's working set, not app state).
2. **GitHub blob cache venue** — in-memory per root (proposed) vs IndexedDB
   persistence for offline re-reads. Proposed: memory only; a root refresh
   is cheap and offline-GitHub is not a real J3 posture.
3. **`SKIP_DIRS` in content search** — is a toggle enough, or should the
   skip set be settings-configurable (mirrors the step-marker-regex open
   question from round 2)? Proposed: fixed set + toggle until asked.
4. **Tree ↔ tab coupling** — should activating a tab reveal+highlight it in
   its root's tree (auto-expand ancestors)? Cheap for local/hub/github,
   costly for remote (chain of `sftpBrowse`). Proposed: local-only in T1,
   others on demand.
5. **Hub docs search** — server-side content search over `docs_root` would
   also serve mobile; belongs to a hub plan if wanted (cross-ref, not built
   here).

## Related

- [debug-code-logs-diffs-models.md](debug-code-logs-diffs-models.md) — J3
  round 2: the shell, viewers, sources, and anchors this plan extends; its §6
  round-3 list (profiling/tracing) is orthogonal and unaffected.
- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) — J3 derivation.
- [read-web-tabs-and-pdf-attachments.md](read-web-tabs-and-pdf-attachments.md)
  — the webtab e2e loopback-server pattern T3 reuses.

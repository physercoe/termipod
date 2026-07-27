# J8 Replay — datasets, episodes & multimodal rollout analysis (embodied pilot, round 1)

> **Type:** plan
> **Status:** Draft (2026-07-27) — for fleet implementation
> **Audience:** principal · contributors
> **Last verified vs code:** origin/main `f3d4a0f8`
> **Parents:** [`embodied-ai-research-workbench.md`](../discussions/embodied-ai-research-workbench.md)
> (director-directed pilot domain + the corrected viewer postures, §5/§8) ·
> [`embodied-ai-tooling-landscape.md`](../discussions/embodied-ai-tooling-landscape.md)
> (deep register; Rerun-not-deep-embeddable correction §3.5) ·
> [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) ·
> [desktop-workbench-jobs.md](desktop-workbench-jobs.md) (the J1–J7 shell this
> adds a job to). This plan **schedules the first surface slice** of the
> embodied pilot; simulator launch/monitor adapters (Isaac Lab etc.) are a
> sibling wedge, not this doc.

**TL;DR.** Embodied research runs on two first-class entities TermiPod does not
yet model: the **dataset** (episodes of synchronized multimodal streams —
multi-cam video, depth, proprioception, actions, language) and the **episode /
rollout trace**. Runs are *already* first-class (hub `runs` + trackio/wandb/TB
polling + `RunDetail` + the J5 comparison wall) — but they stop at scalars,
images and histograms; nothing can open a LeRobot dataset, scrub an episode, or
watch what a policy actually did. This plan adds **J8 Replay**, a new
activity-bar job (decision rationale §1): a **dataset library** (hub-registered
roots + ADR-038-style dataset digests), an **episodes table** with per-episode
stats, and an **episode player** built in the postures the landscape docs
already fixed — BUILD the deep-embedded panel (synced `<video>` grid + timeline
+ action/state channel plots), EMBED three.js + `urdf-loader` for 3D pose,
INTEGRATE Rerun as a launchable companion web panel (a `webtab_policy`-registry
row, the kimi-web precedent), INTEROP LeRobot v2.1/v3.0 as the primary format.
Discovery flows through the surfaces that already exist: an Inspect tree
recognizes `meta/info.json` and offers **Open in Replay** (the §5a
config→ArchCard gate pattern); a run's eval outputs link to replayable
episodes. Local-first, remote via the SSH-forward follow-up — the P0/kimi-web
precedent, deliberately repeated.

Sequencing: **W1 dataset entity + library/episodes table → W2 episode player
(video+plots) → W3 3D pose panel ∥ W4 Rerun companion → W5 runs↔episodes
linkage**. Each wedge independently shippable.

---

## 0. Problem — what "no tool for embodied data" is concretely

A researcher on this stack today can: watch training scalars live
(`RunDetail` Charts — `desktop/src/surfaces/RunDetail.tsx`, hub
`/metrics`+`/system_metrics` polled host-side by
`hub/internal/hostrunner/metrics_poll.go` from trackio/wandb/TB), compare runs
on the J5 wall, and open a policy's `config.json` into the Inspect arch view —
which since #392 even recognizes LeRobot policy families (pi0, smolvla, act…)
and estimates params/FLOPS. What they **cannot** do, anywhere in the product:

- See that a dataset exists. A LeRobot root on a GPU box (or HF) is just a
  directory in an Inspect tree; nothing knows it has 50k episodes at 30 fps
  with 3 cameras and a 7-DoF action space.
- Open an episode. There is no player: no synchronized multi-cam scrub, no
  action/state channel plot under a timeline, no 3D pose view. `RunDetail`
  Media shows logged *images*; rollout *videos* and trajectories have no home.
- Answer the questions that drive the work: is this demo clean? where does the
  policy diverge from the demonstration? what did camera 2 see at t=3.1s when
  the grasp failed? which episodes are outliers in action distribution?
- Trace an eval run's rollouts back to watchable episodes, or a dataset to the
  runs trained on it (provenance — `research-material-data-model.md` already
  reserves `robot.episode`, `rollout_video`, `trajectory` element types).

The strategic layer is **already decided** (director, 2026-07-05, the two
discussion docs): embodied AI is the pilot; Rerun's web viewer is
whole-app-in-a-div (no plugin API, SDK↔viewer lock-step) so it is a
**companion, not the architecture's center**; bespoke deep-embedded panels
build on three.js + `urdf-loader`/Viser/Meshcat; LeRobot Parquet+MP4 is the
primary dataset target; MCAP is the raw-log substrate. What's missing is the
surface that instantiates those decisions. This plan is that surface.

## 1. Decision — a new job (J8 Replay), not an Inspect extension

The desktop shell is an activity bar of **jobs** — interaction models, not
entities (`desktop-workbench-jobs.md` §1: Fleet/J7 + J1 Read · J2 Author · J3
Inspect · J4 Canvas · J5 Compare · J6 Record). Placement follows from that
organizing principle:

- **J3 Inspect's interaction model is static-artifact reading**: pick a file
  in a tree → a viewer tab (CodeMirror, diff, config arch view). Everything
  Inspect gained across three rounds — trees, forges, params, FLOPS — is a
  refinement of *look at a file*. Episodes are not read; they are **played**:
  the core primitive is a timeline scrubbed across synchronized streams, plus
  aggregate stats over an episode population. Different verbs, different
  layout (timeline + multi-pane viewers vs. tabs of text), different data
  path (windowed time-series + video vs. capped file reads).
- **Precedent inside the product**: models went to Inspect *because a config
  is a static file*; runs went to the hub + `RunDetail` + a dedicated J5 job
  *because monitoring/comparison is its own interaction model*. Episodes are
  the runs-shaped case, not the config-shaped case.
- **Well-tested practice outside**: every tool in this space ships the
  episode/multimodal viewer as its own surface (Rerun's viewer, Foxglove's
  layouts, LeRobot's dataset visualizer page, W&B's run workspace) — none
  buries it in a file browser.
- **Cohesion**: the new job shares almost nothing with Inspect's viewers and
  needs primitives no current surface has (sync'd video grid, timeline,
  channel plots, 3D canvas). Bolting it onto J3 — already the largest job,
  three rounds deep — couples unrelated models and makes both worse.

**But discovery stays where users already are.** Inspect trees *detect*
dataset roots and hand off (§4, the `meta/info.json` gate — exactly the §5a
"config.json from any source → ArchCard" pattern); `RunDetail`/ProjectBoard
link eval rollouts into the player (§8). J8 is the destination, not a silo.

**Name: `Replay`** (J8). Rationale: the job family is verb-named (Read,
Author, Inspect, Compare, Record); the genuinely new interaction this job
introduces is *replaying time-synchronized robot experience*, and the word is
the domain's own (LeRobot/Rerun both speak of replaying episodes). Runner-up
`Data` (entity-first, matches Fleet) rejected as under-describing the player
that is the tab's center; `Episodes` rejected as excluding future live-stream
viewing. Known term overlap: the transcript "Insight mode raw-wire replay"
(P5, [transcript-insight-issues.md](transcript-insight-issues.md) §6) is an
internal mode of a different surface (agent transcripts); acceptable — this
tab never says "replay" about transcripts and vice versa.

## 2. Substrate — what ships today that this plan reuses

- **Runs are first-class**: hub `runs` entity (project-scoped, `PATCH`-able
  status), `/metrics` `/system_metrics` `/images` `/histograms` + outputs +
  config; host-side `metricsPollLoop` (scheme-dispatched readers: trackio,
  wandb, TensorBoard); `RunDetail` on both clients; J5 Compare wall.
- **Inspect roots/trees substrate** (J3 round 3): local/SFTP/hub/GitHub/HF
  roots, lazy + capped listings, `InspectSource` registry, the §5a
  entry-gate pattern, context menus (#393).
- **Web-panel plumbing** (transcript P0): `webtab_policy.ts` partition
  **allowlist with per-partition policy** (kimiweb: non-persistent partition,
  loopback-pinned by hostname, enforced at `onBeforeRequest` +
  `will-navigate` + `will-attach-webview`); WebPanel start/stop lifecycle
  (refcount fix `daacf013`); decision #2 recorded the internals as
  **registry-shaped so "another agent web UI is one registry row later"** —
  the Rerun companion is that row (§7).
- **Digest discipline** (ADR-038/045): entity digests folded/backfilled
  hub-side; `RunReport`/dashboard consumption pattern.
- **Elements/provenance**: `research-material-data-model.md` reserves
  `robot.episode`, `rollout_video`, `trajectory`, `3d_asset` element types —
  index on hub, bytes stay on the box, fetched on demand.
- **Landscape postures** (fixed upstream of this plan): bespoke 3D EMBED =
  three.js + `urdf-loader` (Apache-2, Foxglove's own 3D panel lineage);
  Viser/Meshcat as follow-ons; Rerun INTEGRATE (iframe companion / `.rrd`);
  Foxglove INTEROP-only (closed since 2.0); MCAP substrate; charts EMBED =
  Plotly.js/ECharts/Vega-Lite.
- **LeRobot format facts** (verified 2026-07): both generations are marked by
  `meta/info.json` (`codebase_version`). **v2.1**: one parquet + one mp4 per
  episode per camera; JSON/JSONL metadata (`meta/episodes.jsonl`,
  `meta/tasks.jsonl`, `meta/stats.json` / `episodes_stats.jsonl`) — readable
  with the Go stdlib. **v3.0**: many episodes per parquet/mp4 file
  (`data/chunk-*/file-*.parquet`), episode metadata itself parquet
  (`meta/episodes/chunk-*/file-*.parquet`) carrying lengths/tasks/**offsets**;
  episode boundaries resolved through metadata, not filenames.

## 3. Model — two entities, one digest, provenance edges

- **`Dataset`** (hub, project-scoped like runs): `{id, project_id, host_id,
  root_path, source (local|sftp|hf), format (lerobot_v2|lerobot_v3|…),
  registered_at, digest}`. Registration is explicit (an "Open in Replay" /
  "Register dataset" act), not a crawler — the no-surprise-scans posture.
- **Dataset digest** (ADR-038 shape, computed host-side at registration and
  on demand-refresh, stored hub-side): episode count, total frames/duration,
  fps, camera streams (names, resolutions), state/action dims + feature
  names, task/instruction strings (deduped, capped), per-feature min/max/
  mean/std (LeRobot ships these in `stats.json` — fold, don't recompute),
  episode-length histogram. Cheap for v2.1 (pure JSON); v3.0 needs a parquet
  metadata read (§4).
- **`Episode`** is **not materialized hub-side** (BUILD-index posture, INTEROP
  bytes): the episodes *table* is served from the host on demand (windowed,
  capped) keyed by `(dataset_id, episode_index)`; an episode becomes a hub
  row only when something references it (an eval rollout, a J6 record, an
  element) — then it's a `robot.episode` element pointing at
  `(dataset, index)`, bytes on the box.
- **Edges**: `run.dataset_id` (sniffed from run config where present,
  settable), eval run → produced episodes (W5), J6 records → episodes/datasets
  (existing element provenance).

## 4. W1 — Dataset entity, library rail, episodes table (BUILD, no player yet)

**Hub**: `datasets` table + REST CRUD (list by project, register, refresh
digest, delete-index-only); digest blob as above. **Hostrunner**: a
`datasetmeta` reader package — format sniff (`meta/info.json` →
`codebase_version`), v2.1 JSON/JSONL parse (stdlib), v3.0 episode-metadata
parquet via a Go parquet reader (e.g. `parquet-go` — Apache-2; scoped to
*metadata* files in W1, data files stay untouched); returns the digest +
windowed episode listings `{index, length, task, duration}`. All reads capped
(the standing no-uncapped-reads anchor); unknown `codebase_version` → typed
"unsupported format" with the version string surfaced.

**Desktop**: the J8 activity-bar entry + shell (`state/workbench.ts` gains the
job; rail = **dataset library** grouped by project/host, centre = the selected
dataset: a header card rendering the digest (streams, dims, fps, counts,
length histogram sparkline — `ui/Sparkline` exists) over a **virtualized
episodes table** (index · length · duration · task; windowed fetch). No video
yet — W1 is the entity + navigation skeleton, valuable alone (today you
cannot even *see* a dataset).

**Inspect handoff**: tree rows matching `<root>/meta/info.json` (local + SFTP
roots first) get an **Open in Replay** context-menu action (#393's menu
machinery) → registers (idempotent) + jumps to J8 with the dataset selected.
Mirror of §5a's "a config.json from any source flips to the ArchCard".

## 5. W2 — Episode player: synced video + channel plots (BUILD, local-first)

The centre of the job. Selecting an episode opens the **player**: a multi-cam
`<video>` grid over a shared **timeline** with frame-accurate scrub, and
under it **channel plots** (action/state features vs. time; toggleable per
feature; hover = cursor sync with the timeline). This alone reaches parity
with LeRobot's own online visualizer (video + plots — no 3D), which is the
ecosystem's proof that this slice is the highest-value 80%.

**Data path (local-first)**: for v2.1, per-episode mp4s stream through a
capped custom media protocol in the Electron main process (range-request
support — `<video>` needs seeks; same privileged-IPC discipline as the
existing viewers); action/state series decode host-side (`datasetmeta` gains
a windowed series endpoint: `(dataset, episode, features, stride)` →
downsampled JSON, capped points — the metrics-downsample precedent from
`metrics_poll`). v3.0 adds offset-resolution through the episode metadata
(one mp4/parquet holds many episodes — the protocol slices by byte range
from the offsets; verify range math against the migration doc at
implementation). Remote (SFTP/hub) datasets: **deferred to the SSH-forward
follow-up** — the P0/kimi-web decision repeated deliberately; the UI shows
the honest "local datasets only (remote coming)" empty state rather than a
slow path.

**Charts**: uPlot-or-existing-Sparkline for W2 (tiny, fast, sufficient for
scrub-sync); the landscape's Plotly/ECharts choice stays reserved for J5-wall
work — don't spend a big chart dep on a cursor-synced strip chart.

## 6. W3 — 3D pose panel (EMBED: three.js + `urdf-loader`)

A third pane in the player (toggleable): the robot's articulated pose driven
by the state/action series at the timeline cursor. URDF source: a per-dataset
setting (path/URL; LeRobot `info.json` names the robot type — map the common
ones to bundled URDFs where licensing allows, else user-supplied path
through the existing roots machinery). Point clouds / scene geometry are
**out of round 1** (formats vary too much; Rerun companion covers them, §7).
This is the landscape's "reference base" EMBED and the first deep-embedded
3D primitive the design system gains — keep it a self-contained panel
component (`ReplayPose3D`) so J5/J7 can reuse it later (live robot pose was
its original register row).

## 7. W4 — Rerun companion panel (INTEGRATE, a registry row)

For everything the bespoke player deliberately doesn't do (point clouds,
depth, transforms, free multimodal layout): **Open in Rerun** on an episode.
Host side: a small exporter invokes the LeRobot→Rerun bridge (LeRobot ships
rerun-based visualization; pin the working path at implementation) to
produce an `.rrd` (or `rerun --serve-web` for live view — `.rrd`-first: it's
a file, cacheable, no long-lived process). Desktop side: a **`rerunweb`
partition registry row** in `webtab_policy.ts` — non-persistent,
loopback-pinned, mainFrame-enforced, lifecycle refcounted — hosting the
Rerun web viewer iframe loading that `.rrd`. This is exactly the "another
web UI is one registry row" promise of transcript-P0 decision #2, and the
posture the deep survey fixed (companion iframe, **not** deep embed — no
plugin API, SDK↔viewer lock-step; pin the Rerun version pair in one place).
Remote episodes ride the same SSH-forward follow-up as W2.

## 8. W5 — Runs ↔ episodes: eval rollouts become watchable

- `run.dataset_id`: sniff from run config keys where present (LeRobot/openpi
  launch configs name the dataset repo/path); editable on `RunDetail` Config.
- Eval-type runs (benchmark eval is a run-type per the register) that write
  rollout episodes register their output dir as an **ephemeral dataset**
  (same format sniff) — `RunDetail` gains an **Episodes** view (beside
  Charts/Media) listing them; row → J8 player with run context in the
  header ("rollout 14 of eval run X · ckpt 3000").
- The J5 wall's "synchronized multi-seed video-grid" (register BUILD-moat
  row) becomes buildable on the player's video-sync primitive — **recorded
  here, scheduled with J5's next round**, not this plan.

## 9. Sequencing & review anchors

**Order:** W1 → W2 → (W3 ∥ W4) → W5. Each wedge lands whole (entity +
UI + tests) before the next.

- **No uncapped reads** (J3's standing anchor): every host read is windowed/
  capped/stride-downsampled — episode listings, series, media ranges; caps
  surfaced in the UI footer (no-silent-caps).
- **Partition discipline** (P0 anchor, my find): the `rerunweb` row must NOT
  relax `webtab`'s policy; per-partition policy only; non-persistent;
  loopback pinned by hostname; `will-attach-webview` allowlist extended, not
  bypassed. Any e2e-only relaxation keyed to `TERMIPOD_E2E` (the
  forge-e2e/bug-class precedent).
- **WebPanel lifecycle**: start/stop refcount in the same effect's cleanup
  (the `daacf013` class — no unmount-only stop).
- **Fixtures from real artifacts** (the gpt2 `n_inner:null` class): dataset
  fixtures must be *real* `meta/` trees from small public LeRobot v2.1 and
  v3.0 datasets (hand-built fixtures hid the null-field bug before). Include
  a v3.0 multi-episode-per-file offset case.
- **Digest incremental==brute** where a refresh path exists; digest schema
  versioned from day one (ADR-038 precedent).
- **Parity posture**: J8 is desktop-only like J1–J6 (ADR-050); the *mobile
  slice* is deliberately tiny — `RunDetail` mobile may link rollout videos as
  plain media later; record it, don't build it. State this in the doc so the
  parity-miss reflex doesn't force a phone player.
- **i18n en+zh parity** for every new string (repo release discipline).
- **Format version honesty**: unknown `codebase_version` → explicit
  unsupported notice, never a silent partial parse.

## 10. Recorded, not scheduled

- Remote datasets over the **SSH-forward** wedge (shared with kimi-web P0
  follow-up — one mechanism, two consumers).
- **HF-remote LeRobot browsing** (digest from Hub metadata without download;
  the Inspect `hf` root already lists the tree — ranged parquet reads are the
  open question).
- RLDS/TFRecord + robomimic HDF5 + **MCAP/rosbag** adapters (posture: thin
  read-only; MCAP is also the raw-log substrate for live streams later).
- **Manipulation-analysis boards** (the BUILD moat: action-distribution
  outliers, success-by-condition, real-to-sim overlay, failure clustering) —
  needs the player + eval linkage shipped first; own plan.
- **MuJoCo-WASM checkpoint re-sim**; **Viser/Meshcat** panel alternatives;
  Gaussian-splat scenes.
- Live sim/robot streams over A2A (Rerun sink relay / Foxglove WS protocol)
  — depends on the sim-run adapter sibling plan.
- Dataset **curation** ops (filter/split/export) — J8 is read/analyze only
  in round 1.
- J5 multi-seed video grid (on W2's sync primitive).

## 11. Open questions

1. **Video path for v3.0 chunked mp4s** — byte-range slicing vs. host-side
   remux per episode; decide on real files at W2 (range math from episode
   offsets is the risk).
2. **URDF sourcing** — bundle the common OSS robot descriptions (SO-100,
   ALOHA, Franka…) vs. always user-supplied; licensing check per model.
3. **Rerun exporter shape** — depend on LeRobot's visualize path (Python on
   host) vs. a minimal Go/rust `.rrd` writer; version lock-step management.
4. **Dataset digest refresh trigger** — manual-only vs. on-open staleness
   check (mtime of `meta/`); start manual (`Refresh` action), revisit.
5. **Tab icon/position** — after J6 Record or grouped with J5 Compare
   (analysis cluster)? Cosmetic; director's call at implementation.

## Related

- [`embodied-ai-research-workbench.md`](../discussions/embodied-ai-research-workbench.md)
  — pilot directive, viewer postures, register (§8).
- [`embodied-ai-tooling-landscape.md`](../discussions/embodied-ai-tooling-landscape.md)
  — deep survey; Rerun embeddability correction (§3.5); six-layer register (§4).
- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) — the J1–J7 shell.
- [debug-code-logs-diffs-models.md](debug-code-logs-diffs-models.md) — J3
  Inspect (handoff source; the §5a gate pattern; roots substrate).
- [agent-transcript-redesign.md](agent-transcript-redesign.md) — P0 web-panel
  partition registry precedent (decision #2) + SSH-forward follow-up.
- [`research-material-data-model.md`](../discussions/research-material-data-model.md)
  — `robot.episode` / `rollout_video` / `trajectory` element types.

# J8 Replay — datasets, episodes & multimodal rollout analysis (embodied pilot, round 1)

> **Type:** plan
> **Status:** In progress (2026-07-29) — W1, W2, W3 and W5 complete;
> W4 half-landed (W4a); W4b rides the
> [ADR-058](../decisions/058-host-job-surface.md) job surface — W4b-1
> waits only on it, W4b-2 additionally on blob lifetime
> ([ADR-061](../decisions/061-blob-lifetime.md)) (§7)
> **Audience:** principal · contributors
> **Last verified vs code:** origin/main `cb54991a` (W1–W3, W5 complete)
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
linkage**. Each wedge independently shippable. W1, W2, W3 and W5 have
shipped, as has W4's desktop half; its `.rrd` export is a typed
`host_commands` kind executed detached per
[ADR-058](../decisions/058-host-job-surface.md)'s job surface — the
same-machine case waits only on that surface; the remote fetch
additionally on blob lifetime
([ADR-061](../decisions/061-blob-lifetime.md)) (§7).

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
- **Reserved from day one — `env_ref`** (decision, 2026-07-27): runs,
  datasets and the episode element carry an opaque string
  `env_ref = "family:env_id@version"` (nullable, unvalidated). The
  Environment entity itself is a separate plan
  ([environments-and-embodiments.md](environments-and-embodiments.md));
  reserving the field now means provenance accumulates before the registry
  exists and later *resolves* into rows instead of being backfilled by
  guesswork. W1 writes it where cheaply derivable (LeRobot `info.json`
  robot/task hints); everything else leaves it null.

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

### W1 as shipped (2026-07-29)

W1 is three subsystems plus a handoff, so it landed as four wedges:

| | | |
|---|---|---|
| **W1a** | #446 | `hub/internal/hostrunner/datasetmeta/` — format sniff, v2.1 + v3.0 readers, digest fold, windowed episodes |
| **W1b** | #447 | hub `datasets` entity (migration 0068) + REST CRUD + `host.dataset_digest` / `host.dataset_episodes` verbs |
| **W1c** | #448 | desktop **J8 Replay** job — library rail, digest card, paged episodes table, inline register form |
| **W1d** | #454 | the Inspect handoff — a per-file context menu, "Open in Replay" on a `meta/info.json` row |

**W1d was bigger than the plan implied, and it does less than the plan
asked.** §4 assumed #393's menu machinery covered it; `InspectTree` in fact
had only *root-level* and *panel-level* context menus, with no per-file row
menu at all, so building that was most of the wedge.

And "registers (idempotent) + jumps to J8 with the dataset selected" turned
out not to be honest. A dataset row is keyed `(project, host, root_path)`
and Inspect has **neither** key:

- **No project.** Only a hub root carries one. Registering into whichever
  project sorts first would not merely be arbitrary — it would defeat the
  idempotent register, creating a second row instead of finding the
  existing one.
- **No host, not even for a remote root that looks like it knows one.**
  `InspectRoot.hostId` is an SSH *connection* id (`state/connections.ts`
  stores breakglass credentials with no hub-host field), while
  `datasets.host_id` is a foreign key into `hosts`. Same word, different
  entity; passing one as the other writes a dangling reference. The first
  cut did exactly that and type-checked perfectly.

So the handoff carries a **location**, and Replay resolves it: a unique
match in the current project is selected, anything else opens the register
form prefilled. Ambiguity belongs in the form — whose host select is the
answer — not in a coin flip between two machines' datasets. The inline
register form stays as the escape hatch for a root not open in a tree.

### What the real LeRobot fixtures corrected

The plan's §2 format notes were right about layout and wrong about
prevalence, and the details that actually break readers were not
predictable from the docs. All of the below came from pinned real `meta/`
trees (`datasetmeta/testdata/fetch-fixtures.sh`):

- **v3.0 is the dominant format now**, not the newer edge case. Every
  canonical `lerobot/*` dataset on HF `main` is v3.0. Usefully, both
  generations survive as *tags on the same repo*, which is what makes the
  cross-generation tests below possible at all.
- **`names` has three shapes in a single file** — `null`, a list, and an
  object (`{"motors": [...]}`). A `[]string` field fails the whole
  `info.json` parse on the third. Feature dimension therefore comes from
  `shape`, never `len(names)`: `names` is null on every scalar feature.
- **A v3.0 column named `data/chunk_index` is one flat name containing a
  slash**, not a nested group. Reading it as a path finds nothing — and
  finding nothing is indistinguishable from "this dataset has no offsets".
  Repeated columns are the exception: `["tasks", "list", "element"]`.
- **`tasks.parquet`'s task-string column name is not stable.** Curated
  `lerobot/*` datasets leave it in the pandas artifact
  `__index_level_0__`; a freshly recorded community dataset names it
  `task`. Curated-only fixtures would have hardcoded the first and broken
  on most real datasets.
- **v2.1 ships no `meta/stats.json`** — that file arrives with v3.0. The
  dataset-level statistics have to be folded from
  `meta/episodes_stats.jsonl` (count-weighted mean; standard deviation
  recovered from the second moment). Checked rather than assumed: folding
  a dataset's v2.1 stats reproduces the *same dataset's* v3.0
  `stats.json` across all 10 features to 2.5e-9 — float round-off.

That last one is the wedge's strongest test, and it is not an A==B
tautology: different files, different formats, different decoders, no
shared code below the comparison.

**Toolchain constraint:** `parquet-go` is pinned to **v0.25.0**. v0.26.0
is the first release whose go directive is 1.24.9, and `hub/go.mod` plus
both CI workflows are on 1.23. Raising it needs the toolchain bump first.

### Amendments to §11's decisions

- **Decision #4 (manual refresh + staleness indicator) is half-shipped.**
  W1b stores `fingerprint_json` (files, bytes, newest mtime) at fold time,
  but nothing re-stats to compare it, so there is no "digest may be stale"
  hint yet. The UI shows `as of <ts>` plus a manual Refresh, which is
  honest but is not the decision. Wiring the comparison needs one more
  cheap host round-trip and belongs with W2.
- **Decision #1 (host-side per-episode extraction) is unverified.** The
  v3.0 episode metadata does carry what it needs —
  `dataset_from_index`/`dataset_to_index` for rows and per-video
  `from_timestamp`/`to_timestamp` for seconds — but whether episodes start
  on keyframes, which is what makes `ffmpeg -ss/-to -c copy` near-free,
  still has to be checked against real v3 video files at W2.

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

*Remote follow-up SHIPPED 2026-07-29 for hub-host datasets*: digest/episodes/
series already rode the hub tunnel; the missing half was video bytes, which
now stream over a live SSH terminal session's SFTP channel via a second media
flavour (`termipod-media://sftp/?s=<session>&p=<path>` — same allowlist and
range arithmetic; one SFTP channel per range request). The player grows a
per-dataset "Video source" picker (this machine | any saved connection with
an open ssh tab; the CONNECTION id persists, sessions are resolved live).
Ranged SFTP reads, not a port forward — a port has nothing serving media;
the `ssh_forward_start/stop` primitive shipped alongside for the kimi-web
consumer. Still open: datasets with NO host-runner (`source: 'sftp'`) keep
their honest 501 — series decode needs `datasetmeta` (parquet), which stays
host-side by design.

**Charts**: uPlot-or-existing-Sparkline for W2 (tiny, fast, sufficient for
scrub-sync); the landscape's Plotly/ECharts choice stays reserved for J5-wall
work — don't spend a big chart dep on a cursor-synced strip chart.

### W2 as shipped (2026-07-29) — plots, not yet video

W2 splits along the same seam as W1 — reader, transport, surface — plus the
media protocol, which is a wedge of its own:

| | | |
|---|---|---|
| **W2a** | #457 | `datasetmeta.ReadSeries` — per-episode channels from the data parquet, decimated |
| **W2b** | #458 | `host.dataset_series` + `GET /datasets/{id}/episodes/{n}/series` |
| **W2c** | #459 | the player's plots: channel lanes, shared cursor, feature toggles |
| **W2d** | #461 · #462 | uniform video slices; the `termipod-media://` range scheme; the camera grid |

The plots shipped first because they are the half that can be *verified*
here — the geometry is a pure module with 18 assertions, while a video pane
is a thing you have to look at.

**Decision #1 is retired, not implemented.** It assumed host-side `ffmpeg
-ss/-to -c copy` extraction and flagged the keyframe question as the thing
to check before building. Measured on the real mp4s (parsed box by box —
no ffprobe on the build host): the v3.0 file carries **220 sync samples
across 440 frames**, a keyframe every 0.4s, every second frame at 5 fps.
A seek to an episode start therefore costs at most one extra decoded
frame, so cutting the clip out server-side would buy nothing and cost a
dependency, a temp file and a copy. W2d serves the file over a
range-supporting scheme and lets each `<video>` seek instead.

Worth recording precisely, because it is the part that would have bitten:
episode boundaries *do* land on keyframes in these fixtures, but only
because every episode length is even and keyframes fall on even frames.
That is arithmetic, not a format guarantee — a dataset with odd episode
lengths would have broken a stream-copy cut. Seeking is robust to that
case, which is the better reason to prefer it.

**What the real data files corrected** (the pinned fixtures gained
`data/`, ~23 KB):

- **`data_path` is a template, and the generations spell their
  placeholders differently**: v2.1 `{episode_chunk:03d}` /
  `{episode_index:06d}`, v3.0 `{chunk_index:03d}` / `{file_index:03d}`.
  The *video* templates also differ in directory **order**
  (`videos/chunk-XXX/{key}/…` vs `videos/{key}/chunk-XXX/…`) — W2d needs
  that. So the template is expanded, never reimplemented per generation,
  and an unknown placeholder is an error rather than an empty string: a
  substituted blank builds a plausible path that misses, and a miss reads
  as "this episode has no data file".
- **`observation.state` is a parquet `list<float>`**, so its channels split
  on the *repetition level*, not on `info.json`'s declared shape. Trusting
  the shape lets a file whose rows disagree with it interleave two joints
  into one plot.
- **`timestamp` is already episode-relative in both generations** — it
  resets to 0 at each boundary (verified at rows 39/40 of the shared v3.0
  file). Had it been absolute, scrubbing episode 1 would have put the
  cursor 8s into a 6s clip.
- **Nulls are NaN, not 0**, end to end: the host emits NaN, JSON carries it
  as null, and the plot *breaks the line* rather than drawing through the
  gap. A line over a hole asserts readings nobody took.

**Decisions this settled:**

- Downsampling is **decimation**, never averaging. A joint-angle plot is
  read for its extremes, and averaging erases exactly the spikes it is
  opened to find. A decimated page says so and still reports the episode's
  real frame count.
- The y-range is shared **per feature**, not per channel: seven joints of
  one arm are the same physical quantity, and per-channel normalization
  draws a motionless joint exactly like a sweeping one.

**W2d also unified the two generations' video metadata.** v3.0 reads a
slice from its episode table; v2.1 has one file per episode and records
nothing, so its slice is *derived* as `[0, length/fps)` of the templated
path. Both now report the same shape — a path plus a range — and the
player never branches on `codebase_version`. The video path templates
differ in directory **order**, not just placeholder names
(`videos/chunk-XXX/{key}/…` vs `videos/{key}/chunk-XXX/…`), which is why
one expansion driven by info.json beats two hardcoded layouts.

**Security posture of the media scheme:** the handler attaches to
`defaultSession` only, and every `<webview>` guest runs in an isolated
partition with no handler for the scheme — so untrusted remote content
cannot reach it. Within the app's own renderer it is the privilege
`localfs_read` already grants; what is added is streaming, not reach. A
media-extension allowlist, normalization before the extension check, a
non-file stat refusal, and caps on file size and single-range length
bound the rest.

**Still unseen:** no video has been watched and no plot has been looked
at. Every claim above is asserted by a test; the *look* is not.

## 6. W3 — 3D pose panel (EMBED: three.js + `urdf-loader`)

A third pane in the player (toggleable): the robot's articulated pose driven
by the state/action series at the timeline cursor. URDF source (decision,
2026-07-27): **a manifest, not a bundle** — a registry-row manifest mapping
LeRobot `robot_type` → known OSS robot descriptions (SO-100/101, ALOHA,
Koch, Franka…; license checked per entry), fetched + cached through the
existing forge machinery (GitHub roots), user-supplied path as fallback.
Bundling would bloat installers and freeze licensing; the manifest is also
the **embodiment registry's first form**
([environments-and-embodiments.md](environments-and-embodiments.md) E1). Point clouds / scene geometry are
**out of round 1** (formats vary too much; Rerun companion covers them, §7).
This is the landscape's "reference base" EMBED and the first deep-embedded
3D primitive the design system gains — keep it a self-contained panel
component (`ReplayPose3D`) so J5/J7 can reuse it later (live robot pose was
its original register row).


### W3 as shipped (2026-07-29)

| | | |
|---|---|---|
| **W3a** | #465 | `state/urdf.ts` (XML + kinematics) · `state/robotManifest.ts` (the registry) |
| **W3b** | #466 | `ReplayPose3D` — three.js, the forge fetch, the honesty notes |

**`urdf-loader` was not used, and this section's "EMBED three.js +
`urdf-loader`" is amended accordingly.** That package's value is loading
the meshes a URDF points at, through three's `LoadingManager` — plain XHR.
Every outbound request in this app goes through the proxy-aware forge IPC
instead, and `readForgeBlob` decodes UTF-8, so binary STL/DAE is not
reachable by that route at all. Meshes are therefore out of round 1, which
leaves the joint tree — and `URDFLoader.parse` needs a DOM, so its arm of
the pipeline would be untestable under `node --test`, on a surface nobody
can look at. three.js stays: it is the reusable 3D primitive J5/J7 were
promised, and the panel renders a **kinematic skeleton**, which it says on
screen rather than letting a wireframe pass for a render.

**What the real robot descriptions and datasets corrected:**

- **`robot_type` is unreliable and often the literal `unknown`** — on
  datasets that plainly are an SO-100 (`maximilienroberti/so100_test`).
  And `lerobot/svla_so101_pickplace` is an SO-101 declaring
  `so100_follower`. So the manifest follows the declaration and offers a
  **picker** when nothing matches; it never guesses from a repo name.
- **File order is not channel order.** `so101_new_calib.urdf` declares its
  joints **gripper-first**, exactly reversed from the motor table. A
  positional fallback trusting file order drives the arm backwards and
  still animates convincingly, so the manifest carries an explicit
  `jointOrder`.
- **SO-ARM channels are normalized, not radians**: body joints are
  `RANGE_M100_100` and the gripper `RANGE_0_100`
  (`lerobot/robots/so_follower/so_follower.py`, corroborated by two
  datasets whose gripper channel never goes negative). Read as symmetric,
  a fully closed gripper draws half open. The pose is labelled
  **approximate** because the mapping uses the description's joint limits,
  not this robot's calibration — which the dataset does not carry.
- **Channels exceed their nominal range** (one shipped SO-101 dataset
  peaks at 123 on a [-100,100] channel), so the solver clamps to the URDF
  limit and *names the joints it clamped* rather than swallowing it.
- **`observation.state` drives the pose, never `action`.** They differ by
  tracking error, which is the thing a replay is watched for; driving from
  `action` would draw what the policy asked for and label it what the robot
  did.

**A fixture that agreed with the bug.** Six exact link-position assertions
passed against a deliberately reversed rpy composition order, because every
one of SO-100's joint origins rotates about a single axis and a single-axis
rotation composes identically either way. SO-101's multi-axis origins catch
it. Real fixtures are necessary, not sufficient — the discriminating case
has to be chosen, not assumed.

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


### W4 as half-shipped (2026-07-29) — W4a landed, W4b blocked

**W4a** (#469) is the desktop half this section describes: a `rerunweb`
partition row in `webtab_policy.ts`, a pure `rerun_policy.ts` (argv, viewer
URL, recording-path rules), and a `rerun.ts` manager in `kimiweb.ts`'s
shape. Every flag was read off rerun's own CLI definition
(`crates/top/rerun/src/commands/entrypoint.rs`) and the viewer URL off
`crates/top/re_sdk/src/web_viewer.rs`.

The finding worth carrying: **rerun's `WebViewerConfig.bind_ip` defaults to
`0.0.0.0`**. A `--serve-web` launched without an explicit
`--bind 127.0.0.1` publishes the robot's episodes, video and all, to every
machine on the network, and looks perfectly fine to whoever launched it.
That is why the argv is built in a tested module rather than inline at the
spawn. The `rerunweb` row is deliberately identical to `kimiweb`'s and not
looser — the "another web UI is one registry row" promise only holds if a
new row cannot quietly widen the policy.

**W4b — the `.rrd` export — rides a typed `host_commands` kind.** The
exporter is `python -m lerobot.scripts.lerobot_dataset_viz --repo-id …
--root … --episode-index N --save 1 --output-dir …`, which writes
`{repo_id with / → _}_episode_{n}.rrd` and decodes every frame of the
episode.

An earlier revision of this section recorded W4b as blocked on "an async
host job (submit → poll → fetch) that the dataset verb surface does not
have." **That was wrong**, and the mistake is worth keeping: there are
**two** host channels and only one was checked.

| channel | bound | used by |
|---|---|---|
| `tunnel.enqueueHostVerb` — request/response | 60 s (`handlers_datasets.go:40`) | dataset digest · episodes · series |
| `host_commands` pull queue + `awaitHostCommand` | 15 min (`handlers_teleport.go:35-36`) | teleport pack/unpack |

`awaitHostCommand` (`handlers_teleport.go:331`) enqueues a typed kind and
polls `status`/`result_json`/`error` on a 500 ms tick — submit → poll →
fetch, already built. It already supports a command with no agent
(`awaitHostCommand(ctx, targetHost, "", …)`, line 266), which is what a
dataset export is. Bulk bytes already have a home too:
`hub/internal/handoff` is "deliberately transport-only: it moves an opaque
byte stream and knows nothing about tar, git, or engine layouts"
(`transport.go:12-15`) — written that way so a second caller could reuse
it.

[ADR-057](../decisions/057-session-teleport.md) also settles the *shape*,
and rules against the general-job-surface option this section used to
offer: it rejects wiring the dormant `plan_executor` because "a general
host-exec primitive is a far larger security surface than four typed
teleport verbs." The export is therefore a **third typed kind** beside
the two teleport kinds — not a job framework.
[ADR-058](../decisions/058-host-job-surface.md), written independently
against the same substrate, converged on exactly that shape with one
correction this section originally missed: the kind must **not** run as
an inline case in `runCommand`'s switch the way teleport's do —
`tickCommands` shares the single main-loop goroutine with the
spawn/reconcile/idle ticks (`runner.go:411-414`), so a
fifteen-minute export inline would starve pause/resume/teleport *and*
spawn launches for its whole duration (teleport accepted that cost for
a rare deliberate op; an episodes table is browsed). Job kinds dispatch
to ADR-058's detached single-flight executor, with `progress_json`
heartbeat, `job_cancel`, and restart reconciliation.

Two sub-wedges:

- **W4b-1 — same machine, no transport.** A `dataset_episode_export` kind
  returning `{path, sha256, bytes}`; the hub polls with
  `awaitHostCommand`; the desktop opens the returned absolute path
  directly, which `isRecordingPath` already gates (absolute + `.rrd`).
  Zero bytes cross the hub. The exporter's `--output-dir` must be confined
  to a host-side cache dir, because the path it returns becomes a process
  argument. This is the wedge that gives W4a's IPC handlers a caller.
- **W4b-2 — remote hub-host.** The export job runs fine on the remote
  host (same `host_commands` queue); the question is only how the
  produced `.rrd` reaches the desktop. Two candidate transports, decided
  in the wedge per ADR-058 §4: the handoff chunk path through the blob
  store — which is blocked on a **blob lifetime** answer,
  [ADR-061](../decisions/061-blob-lifetime.md), because ADR-057 D-3's
  accepted "linger" was priced for teleport (rare, deliberate) and an
  episodes table is *browsed*: a multi-camera `.rrd` is tens of
  megabytes, so that transport at browsing frequency writes hundreds of
  megabytes of otherwise-permanent bytes into the hub, against this very
  feature's own stated rule that "bulk data does not live on the hub"
  (`handlers_datasets.go:17-25`) — or a fetch over the user's live SSH
  session via the now-landed forward/SFTP primitives (`be796b3e`), which
  moves zero bytes through the hub at all. Distinct from both: hostless
  `source:'sftp'` datasets have no host-runner, so they cannot export at
  all — that is the recorded 501 posture
  (`handlers_datasets.go:528-536`), not a transport gap.

Either sub-wedge needs the host to actually *have* the pinned
`(lerobot, rerun-sdk)` pair, advertised as a capability the way
`checkHostSupportsFamily` (`handlers_teleport.go:371`) checks an engine
family. Without it the failure mode is a fifteen-minute poll ending in a
Python traceback.

Until W4b-1 lands, W4a's IPC handlers have **no caller**, which is stated
rather than dressed up: the panel can host a recording, and nothing yet
produces one. And no rerun process has ever been started against W4a's
code (`rerun_policy.ts` says so in its own header), so even W4b-1 ends at
"produces a file and points the panel at it" — whether the viewer renders
is unverified until someone runs it.

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


### W5 as shipped (2026-07-29)

| | | |
|---|---|---|
| **W5a** | #467 | migration 0069 `runs.dataset_id` · `GET /runs/{run}/dataset_hint` |
| **W5b** | #468 | RunDetail's **Episodes** view, and the by-id jump into the player |

The sniff **proposes and never writes**: a config key that merely looks
like a dataset is a guess, and a wrong edge sends someone to watch the
wrong robot and believe what they see. Key names came off LeRobot's config
classes (`TrainPipelineConfig.dataset` is a `DatasetConfig` with
`repo_id: str` and `root: str | None`), which corrected four assumptions:

- trackio and wandb both **flatten** nested config to dotted keys, so both
  shapes must be read — otherwise every tracker-logged run silently
  produces no hint, which looks exactly like "no dataset".
- `dataset.root` outranks `dataset.repo_id`: a root is a **location**,
  which is what `datasets` is keyed by. A repo id matches a path's last
  **two** segments, because `$HF_LEROBOT_HOME/lerobot/pusht` is where the
  cache lands `lerobot/pusht` and one segment would confuse `alice/pusht`
  with `bob/pusht`.
- `dataset.repo_id` is legitimately a **list**; one column cannot hold
  several, so the first is proposed as a starting point.
- `eval.recording_repo_id` is where an eval **writes** its rollouts — the
  field that makes "watch what this eval actually did" possible at all.

`policy.repo_id` is the **model** and sits one key away. A deny list was
written for it and then removed: every lookup is a fully-qualified path, so
nothing matches a bare trailing name and the key is unreachable rather than
merely rejected. Dead defensive code reads as a safety net and would be
trusted by whoever later adds a bare key.

The column carries **no foreign key** — SQLite cannot add one with
`ALTER TABLE`, following 0005's note — so the scope rule lives in the
handler (a dataset must be in the run's own project) and the delete handler
clears the runs pointing at a dataset it removes. A dangling id reads
downstream as "this run has a dataset" right up until the episodes fail to
load.

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

## 11. Decisions (2026-07-27 — resolved from the draft's open questions)

1. **v3.0 video path — host-side per-episode extraction with a capped LRU
   cache, not client byte-range slicing.** Raw ranges into a concatenated
   mp4 aren't playable at arbitrary episode boundaries (`<video>` needs a
   valid container; seeking needs keyframe alignment). `ffmpeg -ss/-to -c
   copy` is near-free when episodes start on keyframes — **verify on real
   v3 files at W2**; if they don't, re-encode the head GOP only. Keeps the
   privileged Electron media protocol dumb (serve a local file with
   ranges). Recorded fallback: MSE/fMP4 remux in the renderer.
2. **URDF sourcing — manifest, not bundle** (folded into §6; seeds the
   embodiment registry).
3. **Rerun exporter — INTEGRATE LeRobot's own Python/rerun path; no
   bespoke `.rrd` writer.** A custom writer inherits the SDK↔viewer
   lock-step problem the deep survey warned about, doubled (tracking
   rerun's format AND LeRobot's internals). Host-side pinned venv/uvx with
   one recorded `(lerobot, rerun-sdk)` version pair; the `rerunweb` panel
   loads the matching viewer. Exporter missing → soft-degrade notice (the
   #394 missing-kimi-binary pattern).
4. **Digest refresh — manual, with a one-syscall staleness indicator.**
   Store `meta/` mtime+hash at fold time; on open, a stat-only check
   drives "digest may be stale — Refresh". No auto-refresh (no-surprise-
   scans posture; v3 parquet refolds aren't free). Same honesty pattern as
   RunReportCard's "as of ts · live".
5. **Tab position — between J5 Compare and J6 Record.** The rail reads as
   the lifecycle: Read/Author → Inspect → Canvas → Compare (runs) →
   **Replay** (episodes) → Record (conclusions); the two analysis jobs sit
   adjacent. Director may override at implementation (cosmetic).

## Related

- [environments-and-embodiments.md](environments-and-embodiments.md) — the
  Environment/Site entity + embodiment registry this plan reserves fields
  for (`env_ref`, §3; URDF manifest, §6).
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

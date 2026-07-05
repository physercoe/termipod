# Embodied-AI tooling landscape

> **Type:** discussion
> **Status:** Open (2026-07-05) — deep companion to
> [`embodied-ai-research-workbench.md`](embodied-ai-research-workbench.md) and
> the desktop-workbench decision [ADR-050](../decisions/050-desktop-workbench-delivery-model.md).
> A thorough survey (six parallel scans, current mid-2026) of the robot-manipulation
> research stack, per director request to "cover all the important elements."
> **Audience:** contributors · maintainers · principal
> **Last verified vs code:** v1.0.820

**TL;DR.** A comprehensive map of the embodied-AI / manipulation toolchain across
six layers — **simulators · benchmarks/eval · data-formats-&-teleop · policy/VLA
models · viz/debug/analysis · assets-&-orchestration** — with a
build/embed/integrate/interop posture per tool for our local-first web-tech
workbench. The synthesized answer: **simulate nothing, embed the viewers, integrate
the launchers, interop the formats — and BUILD only the fleet-native layer** the
field has *no* product for: a generic sim-run digest-normalizer, the **multi-run
comparison wall (incl. a synchronized multi-seed video-grid)**, the
**manipulation-analysis views** (action-distribution, success-by-condition,
real-to-sim overlay, VLA attention, failure-mode clustering), a **`robot.episode`
element + provenance schema** (a field-wide gap per ISO/WD 26264-1), a **governed
compute-dispatch driver** over OSMO/SkyPilot/Slurm, and an **eval-result schema**
(no HELM-equivalent exists for manipulation). Three convergences frame it all:
**Newton** (NVIDIA+DeepMind+Disney physics kernel), **RLDS↔LeRobot v3** (episode
semantics), and **MCAP** (the open logging/interchange substrate). NVIDIA's
**OSMO** — itself now an agentic orchestrator integrating Claude Code/Codex/Cursor —
validates the fleet-control thesis and marks where *not* to duplicate.

---

## 1. Purpose and method

The director set embodied AI / robot manipulation as the pilot field
([ADR-050](../decisions/050-desktop-workbench-delivery-model.md) amendment) and
asked for a thorough survey of the specialized stack. Six parallel landscape
scans (simulators; benchmarks/eval; datasets/formats/teleop; policy/VLA models;
viz/debug/analysis; assets/orchestration), each current to mid-2026, distilled
here into one register. Postures: **BUILD** (fleet-native, no product exists) ·
**EMBED** (a web/JS component) · **INTEGRATE** (external tool/service via
API/CLI) · **INTEROP** (speak a format, own nothing).

## 2. The embodied-AI research lifecycle

| Stage | Owned today by | Posture |
|---|---|---|
| Author robot + scene (URDF/MJCF/USD) | manual + converters | INTEROP |
| Simulate / train (GPU-parallel RL/IL) | Isaac Lab, MuJoCo Playground, ManiSkill3 | INTEGRATE (adapter) |
| Learn a policy (RL / IL / VLA) | rsl_rl…; LeRobot; π0/OpenVLA/GR00T | INTEGRATE |
| Collect demos (teleop) | ALOHA/GELLO/SO-100/UMI | INTEROP (→ episode element) |
| Store data (episodes) | LeRobot v3 · RLDS · MCAP | INTEROP + **BUILD** (index) |
| Track metrics live | W&B/TensorBoard | **BUILD** (on hub digest) |
| Compare runs / sweeps | *(nothing does this well)* | **BUILD** (comparison wall) |
| Visualize scene / rollout | Rerun, Meshcat, three.js+urdf-loader | EMBED / INTEGRATE |
| Analyze (action-dist, real-to-sim, attention, failures) | *(bespoke research code)* | **BUILD** (the moat) |
| Benchmark / eval | LIBERO, SIMPLER, ManiSkill, RoboArena | **BUILD** (result schema) + INTEROP |
| Generate assets/scenes/synthetic data | RoboCasa, Infinigen, Holodeck, Cosmos | INTEGRATE (dispatch) |
| Orchestrate compute | OSMO, SkyPilot, dstack, Ray, Slurm | INTEGRATE (governed driver) |

The BUILD concentration is unmistakable — **tracking, comparison, analysis, the
episode index, and governed dispatch**: exactly the fleet-native layer no
simulator, viewer, or tracker provides.

## 3. The six layers

### 3.1 Simulators & physics engines

| Sim | Focus | OSS/license | Headless output tree | Web viewer | Posture |
|---|---|---|---|---|---|
| **Isaac Lab** (PhysX5/Newton) | manip+loco (lead) | BSD-3 (+Isaac Sim deps, NVIDIA GPU) | `logs/<lib>/<task>/<ts>/{ckpt,videos,tfevents}` ✓ | Rerun `.rrd` sink | **INTEGRATE** (pilot) |
| Isaac Sim/Omniverse | general + synthetic data | Apache-2 core + Omniverse EULA | Replicator `rgb/seg/bbox` ✓ | WebRTC stream | INTEGRATE |
| **MuJoCo Playground** (MJX/Warp) | loco+manip | Apache-2 | `logs/<Env>-<ts>/{ckpt,tfevents}` ✓ | MuJoCo-WASM | **INTEGRATE** (near-ideal) |
| MuJoCo core / MJX | general | Apache-2 | DIY | MuJoCo-WASM (`@mujoco/mujoco`) | INTEGRATE + EMBED(viewer) |
| **ManiSkill3** (SAPIEN) | manip, 30k FPS | Apache-2 | `runs/…` + HDF5 traj ✓ | none | **INTEGRATE** |
| robosuite / RoboCasa365 | manip (household) | MIT | `demo.hdf5` ✓ | (MuJoCo-WASM) | INTEGRATE + INTEROP(HDF5) |
| BEHAVIOR-1K/OmniGibson | long-horizon household | open code, NVIDIA EULA/GPU | `outputs/…`,`eval_logs/` ✓ | WebRTC | INTEGRATE (rides Isaac adapter) |
| Genesis | general | Apache-2 (v1.0 May'26) | no fixed tree | none | INTEGRATE (needs harness) |
| Drake | contact-rich manip | BSD-3 | script-defined | **Meshcat** (best find) | INTEGRATE + **EMBED**(viewer) |
| Gazebo / Webots | nav / education | Apache-2 | tlog / controller | gzweb / Web Streaming | INTEGRATE |
| CoppeliaSim | manip (RLBench) | EDU non-commercial / Pro paid | script | none | INTEGRATE (license gate) |
| Isaac Gym (legacy) | — | closed, **RTX-50 dead** | — | — | **migrate → Isaac Lab** |
| NVIDIA Cosmos | *world model, not a sim* | OpenMDW weights | MP4 (no metrics loop) | `<video>` | INTEROP |
| **Newton** (engine) | cross-cutting kernel | Apache-2, Linux Foundation | swappable `--impl` flag | early native | rides inside INTEGRATEs |

**Findings.** (a) **Newton is the 2026 headline** — NVIDIA + DeepMind + Disney,
Linux-Foundation, Warp-based, *bundles a MuJoCo-Warp solver*; Isaac Lab 3.0 and
MuJoCo Playground both expose it as a swappable backend with **zero change to CLI
or output-tree shape** → the NVIDIA and MuJoCo stacks are merging. (b) **Isaac Lab
and MuJoCo Playground are the two control-plane-friendly sims** — identical
deterministic `<ts>/{checkpoints,tfevents,videos}` shape, one `--headless` flag;
build the generic adapter against that shape and it covers ManiSkill3 /
robosuite / OmniGibson too. (c) **Isaac Gym is hard-dead** (missing Blackwell
kernels). (d) **Licensing gates to track:** CoppeliaSim EDU bars commercial use;
the whole NVIDIA stack requires an NVIDIA GPU + Omniverse EULA. (e) **No simulator
ships a run-comparison dashboard** — reconfirms the wall is BUILD.

### 3.2 Benchmarks & evaluation

| Benchmark | Type | Metric/artifact | Posture |
|---|---|---|---|
| **LIBERO** | sim (MuJoCo) | per-suite + avg success — *de facto VLA eval since 2024* | INTEROP |
| **SIMPLER** | **real-to-sim** | success (visual-matching + variant-agg), real-correlated | **INTEGRATE** |
| ManiSkill3 (bench) | sim, GPU | success @ high FPS, RGBD/pointcloud | INTEGRATE |
| robomimic/robosuite | sim | success on PH/MH/MG splits (IL baseline) | INTEROP |
| Meta-World / RLBench / CALVIN | sim | per-task / chain-length | INTEROP |
| BEHAVIOR-1K / RoboCasa365 | sim (long-horizon) | partial/Q-score; Challenge leaderboard | INTEROP/INTEGRATE |
| Colosseum / GemBench | sim (perturbation axes) | success **degradation curves** | INTEROP |
| **RoboArena** | real, crowdsourced | double-blind pairwise → **Elo** | INTEROP (leaderboard) |
| **AutoEval / RoboChallenge** | real, 24/7 cells | success + rollout **video**, job-queue API | **INTEGRATE** |
| Open-X eval protocol | real+sim | 100 trials/skill binary | INTEROP |
| D4RL → **Minari** | offline RL (loco) | Farama standard | INTEROP (low pri) |

**Findings.** (a) **There is no HELM / lm-eval-harness for manipulation** — every
benchmark ships bespoke output; standardization exists one layer *down* (training
data), not for results → the eval-**result** schema is a genuine BUILD. (b)
**LIBERO** is the de facto VLA eval (no official leaderboard). (c) **Real-to-sim
is the 2026 scaling trend** (SIMPLER proved sim-success correlates with real);
RoboArena/RobotArena∞ distribute it. (d) **Eval-as-a-service** (AutoEval,
RoboChallenge) is architecturally *our own run+digest model with a physical policy
as executor* — the API-shaped ones are first-class INTEGRATE run-types. (e) **No
tool renders a synchronized multi-run/multi-seed video-grid** — a clear BUILD gap
on the comparison wall.

### 3.3 Datasets, episode formats & teleop

| Format/dataset | Storage | Web-friendly? | Posture |
|---|---|---|---|
| **LeRobot v3** | Parquet + MP4 + relational `meta/episodes` index | **yes** (`StreamingLeRobotDataset`) | INTEROP + EMBED(reader) |
| **RLDS / TFDS** | TFRecord | no (local FS) | INTEROP (VLA-pipeline export) |
| **MCAP** | indexed, seekable, schema-embedded | **yes** (browser-playable) | **INTEROP** (adopt as replay substrate) |
| ROS bag / rosbag2 | SQLite/MCAP | via MCAP | INTEROP (convert on ingest) |
| HDF5 (robomimic/Minari/RH20T) | hierarchical binary | **no** (no video codec, weak range-read) | INTEROP (legacy import) |
| zarr (Diffusion Policy/UMI) | chunked arrays | object-store friendly | INTEROP |
| Robo-DM | EBML (70× smaller claim) | — | watch |
| Datasets: OXE/RT-X · DROID · BridgeData V2 · RoboMIND · AgiBot World · RH20T | RLDS/LeRobot mirrors | — | INTEROP (link+cache) |
| Teleop: ALOHA · GELLO · SO-100/101 · UMI · Open-TeleVision · DexCap · AnyTeleop | → HDF5/LeRobot | — | INTEROP (→ episode element) |

**Findings.** (a) **Episode semantics have converged even though bytes haven't** —
episode = ordered frames `{observation(cams/depth/proprio), action, reward?, ts,
is_terminal}` + metadata; **RLDS** (TF/JAX) and **LeRobot v3** (PyTorch/HF) are the
two that matter, bridged by `lerobot convert`. (b) **LeRobot v3 already decouples
shard boundaries from episode boundaries via a relational index — structurally
identical to our hub-index/host-bytes law.** (c) **MCAP is the winning open
logging container** (ROS2 default, seekable, browser-playable, now imported by
Rerun) — adopt it as the raw-session substrate; distill LeRobot exports on demand.
(d) **HDF5 is a poor web fit** and the field has migrated off it. (e) **Provenance
is a field-wide gap** — "Data Standards for Humanoid Robotics" + **ISO/WD 26264-1**
argue physical-coherence metadata (frames, calibration, units, sync, body/action/
task/scene relationships) must be preserved; no dataset ships it fully → our
element schema adds value. (f) **Failure-as-data is now first-class** (AgiBot
`error_cause`/`restorable`) → the episode element carries an outcome, not a filter.

**The `robot.episode` element** (fits [research-material-data-model.md](research-material-data-model.md)):
hub-owned **index** = provenance (producing run / teleop-session / rollout id;
`collection_method`∈{teleop,scripted,policy_rollout,human_video}; embodiment or
**rig_id** so robot-free capture is valid; source dataset; host owning bytes;
outcome∈{success,failure,recovered}; task/language; fps) + physical-coherence
(frames/calibration/units) + a modality/schema descriptor + a relational
shard-offset index — *never bytes*. Host-owned **bytes** = (1) the raw session as
**MCAP** (the "open the session" artifact, streamed to a browser viewer) and (2) a
derived **LeRobot v3** training export produced on demand (RLDS via `lerobot
convert` for external VLA consumers) — one source of truth, no third copy.

### 3.4 Policy models & VLA platforms

| Item | Open? | Runs via | Logs to | Posture |
|---|---|---|---|---|
| **LeRobot** (meta-framework) | Apache-2 | `lerobot train --policy.type=X` (dataclass cfg) | W&B / local ckpt | **INTEGRATE** (primary) |
| **π0 / π0.5** (openpi) | weights+code | `uv run scripts/train.py --config=…` | W&B | **INTEGRATE** (#1) |
| **OpenVLA / -OFT** | weights+code | `torchrun finetune.py` (LoRA) | W&B/TB, ckpt@20k | INTEGRATE |
| **NVIDIA GR00T N1.5/1.7** | weights+code | `gr00t_finetune.py` or `lerobot --policy.type=gr00t` | W&B+TB, `checkpoint-N/` | INTEGRATE |
| SmolVLA (LeRobot) | weights+code | `lerobot train --policy.type=smolvla` | W&B | INTEGRATE (cheap first target) |
| RDT-1B / CogACT / Octo | weights+code | per-repo | W&B | INTEROP (secondary) |
| ACT / Diffusion Policy / 3D-DP | code | via LeRobot / robomimic | W&B media (rollout mp4) | INTEGRATE via LeRobot |
| rsl_rl / rl_games / skrl (Isaac RL) | OSS | `isaaclab.sh -p train.py` | TensorBoard | INTEGRATE (loco/low-level) |
| TorchRL / CleanRL / SB3 / RLlib | OSS | per-lib | TB/W&B | INTEROP (general RL) |
| RT-2 / Gemini Robotics / Helix | closed/API | — | — | INTEROP/watch (not launch targets) |

**Findings.** (a) **LeRobot has become the meta-framework** — one `lerobot
train`/`record`/`eval` CLI, one dataset format, one checkpoint convention wrapping
ACT / Diffusion Policy / π0 / SmolVLA / GR00T → **one adapter reaches most of the
field**. (b) **π0/π0.5, OpenVLA-OFT, GR00T N1.5/1.7 are the dominant open VLA
stacks**, all sharing the same recipe (cross-embodiment pretrain → few-GPU finetune
on 1–100 hrs demos). (c) **Config-as-dataclass + `--wandb.enable`/TensorBoard is
universal** — no framework invented its own tracker → none needs BUILD; normalize
step/loss/eval-success/checkpoint/rollout-video into `agent_turns`/digest/OTLP,
INTEROP W&B/TB as the fine-grained system of record. (d) **Eval is a second
job-kind** (LIBERO/SimplerEnv sweeps over checkpoints) sharing our Run/Plan
primitives. (e) Closed models (RT-2, Gemini Robotics, Helix) are eval baselines /
API clients, never training-launch targets.

### 3.5 Visualization, debug & analysis

| Tool | Open? | Deep-embeddable in *our* UI? | Posture |
|---|---|---|---|
| **Rerun** | MIT/Apache | **No** — whole-app-in-a-`<div>`, no plugin/custom-element API, SDK↔viewer lock-step | **INTEGRATE** (iframe/companion, `.rrd`/MCAP) |
| **Foxglove** | **closed (2.0)**, account-gated, self-host Enterprise | embed paywalled | **INTEROP only** (MCAP + open WS protocol) |
| Lichtblick | MPL-2 (Foxglove fork) | app/source | INTEROP / harvest source |
| **three.js + urdf-loader** | Apache-2 (JPL) | **yes** — Foxglove's own 3D panel uses it | **EMBED** (reference base) |
| **Viser** (nerfstudio) | Apache-2 | **yes, by design** — React + r3f, URDF + joint control, GUI | **EMBED** (strongest long-term) |
| Meshcat | MIT | **yes** — `new MeshCat.Viewer(div)`, Drake's viewer | EMBED |
| MuJoCo-WASM | Apache-2 | yes (COOP/COEP) | EMBED (physics replay) |
| Gaussian-splat (Spark / SuperSplat) | OSS | yes | EMBED (splat scenes) |
| USD-for-web | experimental, noncommercial | friction | not yet |
| PlotJuggler / RViz2 | OSS (Qt) | no | INTEROP / BUILD web equiv |
| LeRobot dataset visualizer | OSS (React/three.js) | components | INTEGRATE (dataset-side) |
| Embedding Atlas / WizMap | OSS (WebGL) | yes | EMBED (latent viz) |

**Findings.** (a) **Rerun is *not* deep-embeddable** (whole-app-in-a-div, no plugin
API, Rust-only extensions, version lock-step) and **Foxglove went closed** →
neither can supply design-system-native panels interleaved with control-plane
widgets. (b) **MCAP is the winning open interop substrate** (self-describing,
seekable, ROS2 default, imported by Rerun); the **Foxglove WebSocket protocol** is
open and third-party-implementable for live telemetry. (c) **Build the 3D/render
panels on three.js + urdf-loader / Viser / Meshcat** (design-system-native,
deep-embeddable), Rerun as a launchable *companion* for general multimodal replay —
not the architecture's center. (d) **The manipulation-analysis views are BUILD —
this is the moat:** action-distribution plots, success-rate-by-condition
dashboards, real-to-sim overlays, VLA attention/saliency, failure-mode clustering
have **no reusable product** anywhere; they extend the comparison wall directly.
Dataset-side views lean on the LeRobot visualizer (INTEGRATE) and embedding viz on
Embedding Atlas/WizMap (EMBED).

### 3.6 Assets, scenes & orchestration

| Item | Role | Posture |
|---|---|---|
| **OpenUSD** | scene/interchange layer (NVIDIA orbit) | INTEROP |
| **URDF / MJCF / SDF** | robot/scene authoring formats (+ edge converters) | INTEROP |
| Objaverse-XL / PartNet-Mobility / YCB / GSO | object/scene banks | INTEROP (link+cache) |
| GraspNet-1B / ACRONYM | grasp datasets | INTEROP (as elements) |
| RoboCasa365 / Infinigen / Holodeck | generative sim-ready scenes | INTEGRATE (dispatch job) |
| **NVIDIA Cosmos + Physical-AI Data Factory** | synthetic data / domain randomization | INTEGRATE (dispatch + track) |
| **NVIDIA OSMO** | agentic robot-learning orchestrator (integrates Claude Code/Codex/Cursor) | **INTEGRATE** (backend, not rival) |
| SkyPilot / dstack / Ray / Slurm | compute dispatch substrates | INTEROP (backends) |

**Findings.** (a) **USD wins as the scene/interchange layer, not a description
replacement** — URDF/MJCF stay authoring formats; converters translate at the
edges (ingest URDF, target USD for large scenes). (b) Object/grasp datasets are
mature and INTEROP (pure link+cache). (c) **Generative scene + synthetic-data
creation** (RoboCasa365, Infinigen, Holodeck, Cosmos Data Factory) is a
dispatchable, trackable job pattern. (d) **OSMO is the key adjacency** — NVIDIA's
own orchestrator now integrates Claude Code/Codex/Cursor so agents "reason about
pipelines, inspect GPU capacity, and act"; SkyPilot/dstack are repositioning as
"agent skills" too. This *validates* the fleet-control thesis and defines where
not to duplicate: **BUILD a governed dispatch driver** (parallel to
`hub/internal/drivers`) that shells to OSMO/SkyPilot/Slurm and tracks jobs as
Runs + attention_items + audit_events + Deliverables — the wedge is cross-host /
cross-engine **governance + the mobile continuum** OSMO lacks, not a rival
scheduler.

## 4. The synthesized register

| Capability | Posture | Concretely |
|---|---|---|
| Simulate / train run | **INTEGRATE + BUILD(thin)** | one host-runner adapter on the shared `<ts>/{ckpt,tfevents,videos}` shape → covers Isaac Lab / MuJoCo Playground / ManiSkill3 / robosuite / OmniGibson |
| Policy training/finetune | **INTEGRATE** | YAML launch templates (agentfamilies pattern) for LeRobot / openpi(π0) / GR00T / OpenVLA / Isaac RL |
| Metric tracking | **BUILD + INTEROP** | fold step/loss/eval/ckpt/video into digest; W&B/TB as fine-grained record |
| **Multi-run comparison wall** | **BUILD** | success/reward curves + config diff + **synchronized multi-seed video-grid** |
| Scene / robot 3D panel | **EMBED** | three.js + urdf-loader / Viser / Meshcat |
| Multimodal replay | **INTEGRATE** | Rerun companion (iframe/`.rrd`), fed by Isaac Lab sink over A2A |
| Live-log / raw session | **INTEROP** | MCAP (+ Foxglove WS protocol); convert ROS bag on ingest |
| Checkpoint re-sim | **EMBED** | MuJoCo-WASM |
| **Manipulation analysis** | **BUILD (moat)** | action-distribution · success-by-condition · real-to-sim overlay · VLA attention/saliency · failure-mode clustering |
| Latent/embedding viz | **EMBED** | Embedding Atlas / WizMap |
| **Episode / trajectory** | **BUILD(index) + INTEROP(bytes)** | `robot.episode` element; MCAP raw + LeRobot v3 export |
| **Benchmark eval** | **BUILD(schema) + INTEROP/INTEGRATE** | eval-result schema + per-benchmark parsers; SIMPLER/ManiSkill3/AutoEval as run-types |
| Datasets / assets / grasps | **INTEROP** | LeRobot/RLDS; Objaverse/PartNet/YCB/GraspNet — link+cache |
| Robot/scene formats | **INTEROP** | URDF/MJCF authoring, USD scenes |
| Generative / synthetic data | **INTEGRATE** | RoboCasa/Infinigen/Holodeck/Cosmos as dispatch jobs |
| Compute orchestration | **INTEGRATE + BUILD(driver)** | governed dispatch over OSMO/SkyPilot/dstack/Slurm/Ray |
| Robotics connector-pack | **BUILD (YAML)** | LeRobot / Isaac Lab / π0 / GR00T / ManiSkill launch tools as MCP skills |

## 5. What we BUILD — the fleet-native moat

1. **Generic sim-run digest-normalizer** — one adapter on the convergent output
   tree covers the whole simulator field.
2. **Multi-run comparison wall** (ADR-050 headline) + the **synchronized
   multi-seed/multi-run video-grid** no tool provides.
3. **Manipulation-analysis views** — action-distribution, success-by-condition,
   real-to-sim overlay, VLA attention, failure-mode clustering (no product exists).
4. **`robot.episode` element + provenance/physical-coherence schema** — fills the
   ISO/WD 26264-1 gap; MCAP raw + LeRobot export as the two byte views.
5. **Governed compute-dispatch driver** over OSMO/SkyPilot/dstack/Slurm — Runs +
   attention + audit + Deliverables.
6. **Eval-result schema** (`benchmark·suite/task·seed·success/score·axis·video·run_id`)
   + thin per-benchmark parsers.
7. **Robotics connector-pack** (launch tools as MCP skills).

Everything else is EMBED (viewers/plots), INTEGRATE (simulators/policies/
orchestrators/eval-services), or INTEROP (formats/datasets). **We simulate
nothing, train nothing, and store no bytes we don't own** — the value is
fleet-scale comparison, analysis, provenance, and governance *across* engines and
hosts.

## 6. Cross-cutting findings

- **Three convergences** de-risk integration: **Newton** (physics kernel under
  both NVIDIA and MuJoCo), **RLDS↔LeRobot v3** (episode semantics), **MCAP** (the
  open log/interchange substrate). Bet on these, hedge with MCAP.
- **OSMO validates the thesis** — NVIDIA is building agent-driven fleet control for
  robot-learning compute; our differentiation is governance + multi-engine +
  multi-host + mobile, not scheduling.
- **The comparison/analysis layer is universally missing** — every survey
  independently reached "no tool does multi-run comparison / manipulation analysis"
  → the moat is exactly ADR-050's headline BUILD.
- **Provenance is an open standards gap** (ISO/WD 26264-1) — the element schema is
  a genuine contribution, not a me-too.

## 7. Correction to the earlier workbench doc

The first-pass [`embodied-ai-research-workbench.md`](embodied-ai-research-workbench.md)
called the **Rerun web viewer the primary EMBED**. This deeper survey corrects
that: Rerun is **INTEGRATE** (whole-app iframe/companion; no design-system-native
panel path), and the primary **EMBED** base for bespoke panels is **three.js +
urdf-loader / Viser / Meshcat**, with **MCAP** as the interop substrate and
**Foxglove INTEROP-only** (now closed). That doc's §5/§8 are updated to match.

## 8. Open questions

1. **Adapter contract** — one generic "sim-run" adapter vs. per-stack (NVIDIA /
   JAX-MuJoCo / SAPIEN) adapters; how much of the `logs/` tree to normalize vs.
   pointer-and-fetch.
2. **Rerun-over-A2A** relay for NAT'd GPU boxes; auth/latency through the tunnel.
3. **MCAP in the hub** — adopt as the raw-session store format; ROS-bag ingest.
4. **Which analysis views first** — success-by-condition + video-grid (highest
   value) vs. VLA attention (hardest).
5. **Eval-result schema shape** — extend Deliverable/Artifact vs. a new table.
6. **Pilot demo** — locomotion vs. manipulation vs. VLA finetune (drives which
   sims/policies/datasets to wire first).

## 9. Sources (representative, mid-2026)

- **Simulators:** Isaac Lab / Isaac Sim docs; Newton (newton-physics, LF press);
  MuJoCo + Playground; ManiSkill; robosuite/RoboCasa; Genesis; Drake/Meshcat;
  Gazebo/Webots; Isaac Gym deprecation.
- **Benchmarks:** LIBERO; SIMPLER (real2sim-eval); RoboArena; AutoEval;
  RoboChallenge; ManiSkill3; Colosseum; Open-X; Minari.
- **Data/formats/teleop:** LeRobotDataset v3; Open-X/RT-X; DROID; MCAP spec;
  rosbag2; ALOHA/GELLO/SO-101/UMI/Open-TeleVision/DexCap; ISO/WD 26264-1; Robo-DM.
- **Policy/VLA:** LeRobot; openpi (π0); OpenVLA-OFT; Isaac-GR00T N1.5/1.7; SmolVLA;
  RDT; robomimic; Isaac Lab RL scripts.
- **Viz:** Rerun (embed-web, extend); Foxglove pricing/embed + Lichtblick; MCAP;
  Foxglove WS protocol; urdf-loaders; Viser; Meshcat; MuJoCo-WASM; Spark/SuperSplat;
  LeRobot dataset visualizer; vla-evaluation-harness.
- **Assets/orchestration:** OpenUSD/URDF/MJCF; Newton converters; Objaverse-XL;
  PartNet-Mobility; YCB; GraspNet; ACRONYM; RoboCasa; Infinigen; Holodeck; Cosmos
  Data Factory; NVIDIA OSMO; SkyPilot; dstack.

## Related

- [`embodied-ai-research-workbench.md`](embodied-ai-research-workbench.md) — the
  workbench surface this landscape informs (Rerun correction applied there).
- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — the desktop
  delivery decision; robotics is its pilot.
- [`research-material-data-model.md`](research-material-data-model.md) — the
  `robot.episode`/`trajectory`/`3d_asset` element types.
- [`research-tooling-landscape.md`](research-tooling-landscape.md) — the general
  research register this parallels for the robotics domain.

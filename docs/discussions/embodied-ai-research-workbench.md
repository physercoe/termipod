# Embodied-AI research workbench

> **Type:** discussion
> **Status:** Open (2026-07-05) — director directive: the **first/pilot research
> field** for the desktop workbench is embodied AI / robotics, and the workbench
> must interoperate with **Isaac Lab** and other simulators. Companion to
> [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) and the
> [research-material data model](research-material-data-model.md).
> **Audience:** contributors · maintainers · principal
> **Last verified vs code:** v1.0.820

**TL;DR.** Pick **embodied AI / robotics** as the pilot domain and the workbench
gets a concrete, high-leverage shape — because the load-bearing insight is that
**an Isaac Lab training run is just a "run."** It launches headlessly, writes a
deterministic `logs/<lib>/<task>/<ts>/{checkpoints,videos,tfevents}` tree, and
streams scalar metrics — so a **host-runner adapter** normalizes it into the
existing digest/`agent_turns` model exactly as it tails a Claude Code session,
and the fleet/dispatch/compute-consent/comparison machinery is reused unchanged.
What embodied AI *adds* is a set of **artifact viewers**, almost all **EMBED**:
**Rerun** (Isaac Lab ships a built-in Rerun sink; the Rerun web viewer embeds
directly) for live sessions + trajectories, three.js + `urdf-loader` for live
robot pose, MuJoCo-WASM for in-browser checkpoint re-sim, and `<video>` for
rollouts. Datasets interop via **LeRobot** (Parquet+MP4, the 2026 standard). The
only genuine BUILD is the Isaac Lab launch/normalize adapter + the Rerun-over-A2A
relay + a thin "robotics run" digest. TermiPod's differentiator: it is the fleet
**control plane + governance** that OSMO/Foxglove/Rerun deliberately don't ship.

---

## 1. Why embodied AI, and the load-bearing insight

Embodied-AI / robotics research (RL for manipulation & locomotion, imitation
learning, VLA foundation models) is a strong pilot: it is GPU-fleet-native
(exactly TermiPod's topology — a VPS steward delegating to NAT'd GPU boxes over
A2A), its artifacts are rich (3D scenes, rollout videos, trajectories, curves),
and its tooling is converging on formats we can integrate cleanly. It is also
distinct from Claude Science's bio-first focus — a domain where a vendor-neutral,
multi-engine, multi-host control plane has room.

The insight that makes it cheap: **a simulator training run maps onto the run
primitive we already have.** Isaac Lab (and the RL libraries it drives) launch
headless, write a **deterministic output tree**, and stream metrics — the same
shape the host-runner already tails for an LLM agent session. So we reuse:
dispatch (host-runner), governed compute-consent (the `compute_plan` primitive,
[research-tooling-landscape.md](research-tooling-landscape.md) §3.4), tracking +
the **multi-run comparison wall** ([ADR-050](../decisions/050-desktop-workbench-delivery-model.md)),
and provenance/elements (the [data model](research-material-data-model.md)). The
*new* work is narrow and mostly embedding mature viewers.

## 2. The lifecycle and its artifacts

The loop: author a robot (URDF/MJCF) + scene (USD/SDF) → launch a headless,
GPU-vectorized sim (thousands of parallel envs) running a policy-learning algo
(PPO/SAC via rsl_rl / rl_games / skrl / SB3 / TorchRL) → the trainer streams
scalars (reward, success rate, loss) to TensorBoard/W&B and periodically writes
**checkpoints** + short **rollout videos** → inspect learning curves, watch
rollouts, step through **episodes** to diagnose failures → evaluate **sim-to-real**
(gap plots) → package successful demonstrations (teleop or policy rollouts) into
shareable **datasets** (LeRobot Parquet+MP4) for imitation / VLA training.

Five artifact kinds the workbench must render: **3D scenes/robots** (USD/URDF,
static + live-articulated), **rollout videos** (MP4, often multi-camera),
**episode/trajectory viewers** (frame-stepped observation/action/reward), scalar
**learning curves**, and **sim-to-real comparison** plots. Each is a
ResearchElement type (§7).

## 3. Simulators

| Simulator | Headless launch | Outputs | Web-viewer path | Posture |
|---|---|---|---|---|
| **Isaac Lab** | `./isaaclab.sh -p .../train.py --task … --headless --video --enable_cameras` | deterministic `logs/<lib>/<task>/<ts>/{checkpoints,videos,tfevents}` | **built-in Rerun sink** (`RerunVisualizerCfg`) | **INTEGRATE** (pilot) |
| Isaac Sim / Omniverse | Kit app / Python on USD | USD renders; WebRTC stream | WebRTC live stream | INTEROP (assets) |
| MuJoCo + MJX | `MUJOCO_GL=egl python train.py` | Orbax/Flax checkpoints | **MuJoCo-WASM** in-browser re-sim | EMBED (viewer) |
| SAPIEN / ManiSkill | `python -m mani_skill… --num_envs N` | `runs/…` + TB | — | INTEROP |
| Habitat | `habitat-baselines` (Hydra) | per-cfg checkpoint/TB dirs | — | INTEROP |
| Genesis / Brax | pure-Python script | ad hoc (no enforced tree yet) | — | watch (pre-GA) |
| Gazebo | `gz sim -s -r world.sdf` | rosbags (not an RL trainer) | Foxglove / rosbridge | INTEROP |
| IsaacGym (legacy) | deprecated, archive-only | — | — | migrate → Isaac Lab |

**Isaac Lab is the standout control-plane-friendly simulator** — one CLI, a
deterministic output tree, native `--headless --video`, and three RL libs
(rsl_rl / rl_games / skrl) that all write TensorBoard events + checkpoints into
predictable per-run dirs (pollable, no stdout-scraping). **IsaacGym is fully
deprecated**; Isaac Lab is the only viable NVIDIA-stack target. Genesis/Brax are
high-upside but pre-GA (no enforced output convention) — watch, don't build
against yet.

## 4. Integration path — launch & monitor

1. **Dispatch.** The steward proposes a `compute_plan`; on approval the
   **host-runner on the GPU box** launches Isaac Lab headless (same executor
   pattern that spawns agents), under the governed compute-consent flow.
2. **Normalize.** A host-runner **Isaac Lab adapter** watches the deterministic
   `logs/` tree and normalizes it into TermiPod's **run digest** — checkpoints,
   video paths, TensorBoard scalars, and a Rerun-recording pointer become a thin
   **"robotics run" digest** beside the agent-run digest. Metrics flow into the
   tracking + comparison-wall substrate (a natural extension of the OTLP export).
3. **Live view — the highest-leverage integration:** Isaac Lab **already ships a
   built-in Rerun visualizer** (`RerunVisualizerCfg`, gRPC/web port). Point the
   job's Rerun sink through the **existing A2A reverse tunnel** (so a NAT'd GPU
   box reaches the director's desktop/mobile) and **embed the Rerun web viewer** —
   near-zero custom viewer code.

This is the same "remote host-runner launches a job, tails structured output, UI
renders live" shape TermiPod already runs for LLM sessions — the robotics case is
a new *adapter + digest*, not a new architecture.

## 5. Web-embeddable viewers

| Viewer | Handles | Embeddable? | License | Posture |
|---|---|---|---|---|
| **Rerun web viewer** | images, point clouds, transforms, scalars, tensors, text | **Yes** — `@rerun-io/web-viewer`(+React) or iframe; live gRPC | MIT/Apache-2 | **EMBED** (primary) |
| **`urdf-loader`** (three.js) | live-articulated URDF robots (`setJointValue`) | Yes — production-proven (rosbridge-driven) | Apache-2 (ex-JPL) | EMBED (live-pose widget) |
| **MuJoCo-WASM** | full client-side MuJoCo physics | Yes — official `@mujoco/mujoco` npm | Apache-2 | EMBED (checkpoint re-sim) |
| `<model-viewer>` | static glTF/GLB | partial (no live joints) | Apache-2 | EMBED (static preview) |
| HTML5 `<video>` | rollout MP4 | trivial | — | EMBED (rollouts) |
| Babylon.js | general 3D | partial (no URDF ecosystem) | Apache-2 | skip |

**Rerun.io is directly web-embeddable** (official npm packages or iframe,
MIT/Apache-2) and is *already* Isaac Lab's built-in sink — so it is the primary
live-session + trajectory panel. `urdf-loader` backs a lightweight bespoke
live-pose widget when Rerun's full UI is too heavy. **MuJoCo-WASM** (official
Google DeepMind, real `mj_step` in-browser) enables a differentiating
"re-simulate this checkpoint" feature no other engine offers. **Foxglove and
Rerun are the closest existing robotics-workbench products** — treat as
complementary embeds / positioning references, not something to out-build.

## 6. Datasets & formats (interop)

- **LeRobot (HuggingFace) Parquet+MP4** is the de facto 2026 trajectory/episode
  standard (58k+ Hub datasets; ALOHA/GELLO/SO-100 teleop + pi0/pi0.5 VLA training
  land here) — the **primary import target**, ahead of legacy RLDS/HDF5.
- Thin read-only adapters for **RLDS/TFRecord** (Open-X-Embodiment / DROID / RT-X)
  and **robomimic HDF5**.
- **URDF** import + **USD** import/export for Isaac Sim scenes, leaning on Isaac
  Sim's mature bidirectional URDF/MJCF↔USD converters (ingest URDF, target USD —
  USD is winning for large composable scenes).

## 7. Artifacts as research elements

Robotics artifacts are **ResearchElement types**
([data model](research-material-data-model.md) §3): `rollout_video`, `trajectory`,
`3d_asset` (plus the generic `chart` for curves). So a rollout video or an
ablation table from an Isaac Lab run is decomposed, indexed (bytes stay on the
GPU box, fetched on demand), and **recomposable** into a report/paper with full
provenance back to the run + checkpoint that produced it. The claim/finding
element type carries the sim-to-real results that feed comparison tables.

## 8. Register (robotics rows for the landscape)

| Capability | Posture | Concretely |
|---|---|---|
| Simulator launch/monitor | **BUILD (thin) + INTEGRATE** | host-runner **Isaac Lab adapter** normalizing `logs/` → robotics-run digest |
| Live sim visualization | **EMBED** | **Rerun web viewer** fed by Isaac Lab's built-in Rerun sink over A2A |
| Live robot pose | **EMBED** | three.js + `urdf-loader` |
| Checkpoint re-simulation | **EMBED** | MuJoCo-WASM (`@mujoco/mujoco`) |
| Rollout playback | **EMBED** | HTML5 `<video>` gallery keyed to run+checkpoint |
| Learning curves / sim2real | **EMBED** | the plotting layer (Plotly/Vega-Lite) on digest scalars |
| Trajectory/episode data | **INTEROP** | LeRobot Parquet+MP4 (primary); RLDS/HDF5 legacy adapters |
| Robot/scene assets | **INTEROP** | URDF import, USD import/export |
| Fleet-scale sim orchestration | **INTEGRATE** | **NVIDIA OSMO** (now Apache-2 OSS, CLI/YAML, *no UI of its own*) |
| Embodied-AI connector pack | **BUILD (YAML)** | Isaac Lab / MuJoCo / LeRobot as MCP tools/skills |

## 9. Positioning

**NVIDIA OSMO is now Apache-2.0 OSS** and is emerging as the multi-GPU-box
orchestrator for Isaac Lab/Isaac Sim fleets — but it is CLI/YAML-first with **no
monitoring UI of its own**, exactly the gap a TermiPod control-plane cockpit fills
on top. Rerun/Foxglove own the *viewer* layer (embed them); OSMO owns *fleet
job orchestration* (integrate it); **TermiPod owns the layer none of them do** —
the governed, multi-engine, multi-host director cockpit with provenance, the
comparison wall, the compose-a-paper materials store, and the mobile↔desktop
continuum. We do not out-build the viewer, the dataset format, the RL trainer, or
the scheduler.

## 10. Open questions

1. **Rerun-over-A2A relay** — gRPC/WebSocket bridging a NAT'd GPU box's Rerun sink
   to the desktop; latency + auth through the existing tunnel.
2. **Robotics-run digest shape** — how much of the `logs/` tree to normalize vs.
   pointer-and-fetch; checkpoint/video as elements from the start.
3. **Adapter surface** — Isaac Lab first; how generic to make the "simulator run"
   adapter for MuJoCo/ManiSkill later (per-sim adapter vs. a config contract).
4. **OSMO vs. our own dispatch** — when multi-box sim training arrives, integrate
   OSMO or extend the host-runner adapters directly.
5. **Pilot scope** — locomotion vs. manipulation vs. VLA as the concrete first
   demo (drives which datasets/sims to wire first).

## Related

- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — the workbench
  this pilot instantiates.
- [`research-tooling-landscape.md`](research-tooling-landscape.md) — the
  build/embed/integrate register (compute §3.4, viz §3.6) this extends.
- [`research-material-data-model.md`](research-material-data-model.md) — robotics
  artifacts as element types (rollout-video / trajectory / 3d-asset).
- [`spine/blueprint.md`](../spine/blueprint.md) — the A2A tunnel to NAT'd GPU
  boxes that the Rerun relay rides.

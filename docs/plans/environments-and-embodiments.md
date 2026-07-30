# Environments & embodiments — the scene/task/site entity and the robot registry

> **Type:** plan
> **Status:** In flight (2026-07-30) — model settled (director discussion,
> 2026-07-27); **E0 shipped** (datasets in J8 W1, runs + episodes 2026-07-30),
> **E1 shipped** as frontend data; E2 is the next wedge.
> **Audience:** principal · contributors
> **Last verified vs code:** origin/main `9a676cff`
> **Parents:** [replay-datasets-episodes.md](replay-datasets-episodes.md)
> (J8 — reserves `env_ref` §3 and the URDF manifest §6 for this plan) ·
> [`embodied-ai-research-workbench.md`](../discussions/embodied-ai-research-workbench.md)
> · [`embodied-ai-tooling-landscape.md`](../discussions/embodied-ai-tooling-landscape.md)
> (physical-coherence finding §3.3e; `rig_id`; USD-for-web "not yet" §3.5) ·
> [`research-material-data-model.md`](../discussions/research-material-data-model.md).

**TL;DR.** Every research question on this stack has the environment as its
second axis — a run is *policy × environment*, generalization is performance
*across* env axes, sim2real is an *edge between* a real site and its sim twin,
and the manipulation-analysis boards (the landscape's BUILD moat:
success-by-condition, failure clustering, real-to-sim overlay) are **group-bys
over environment dimensions**. Yet TermiPod has no Environment entity: the
episode element carries instance-level physical-coherence metadata
(frames/calibration/units, `rig_id`) with **no referent to point at**. This
plan adds one, deliberately small: an **`Environment` entity** modelling
*identity, not semantics* (family, env_id, version, content hash, pointers),
with **real lab sites as first-class environments** (family `real-site`,
registered at calibration time, team-scoped), a **twin edge** for sim2real, a
**two-layer condition vocabulary** (a tiny reserved key set + free-form
opaque tags), and an **embodiment registry** whose first form is J8's URDF
manifest. UI: **no new tab** — a second library in J8 Replay's rail
(`Datasets | Environments`) plus a detail view; Inspect keeps viewing the
source files; the sim-run adapter (sibling plan) consumes the registry as its
launch picker.

Sequencing: **E0 reservations (ships inside J8 W1) → E1 embodiment
manifest/registry (ships with J8 W3) → E2 Environment entity + library →
E3 real sites + twin edges → E4 launch-picker integration** (blocked on the
sim-run adapter sibling).

---

## 0. Problem — metadata without a referent

The deep survey's provenance finding (§3.3e): the field preserves
body/action/task/scene *relationships* nowhere, and "our element schema adds
value" by carrying them. The episode element accordingly records calibration,
frames, units, `rig_id`, task strings — all **instance-level facts about an
environment that has no identity in the system**. Consequences:

- The moat boards have no grouping key: "success by lighting condition
  across env versions" is unwritable without env identity + conditions.
- Eval claims recorded in J6 can't pin the environment version they were
  measured in — and benchmarks version env ids (`PickCube-v1`) precisely
  because env drift silently invalidates comparison.
- Sim2real is unrepresentable: nothing can say "this real bench ↔ that USD
  scene", so real-to-sim overlay has no data model to stand on.

## 1. The model — four things wearing one word

Gym's env-id conflates them; this plan splits them (decision, 2026-07-27):

1. **Scene** — the world's content (geometry, objects, lighting, physics
   materials): files (USD/MJCF/URDF-world; scan/splat for real). Hashable.
2. **Task** — the purpose imposed on a scene: goal, success criterion,
   reward, termination. Code + config, not geometry.
3. **Instance / condition** — one concrete draw: placements, seed,
   randomization variant. **Per-episode data, never a registry row.**
4. **Embodiment / rig** — which robot (or handheld rig) is placed in it.

### The `Environment` entity

`{id, team_id, family (isaac-lab | maniskill | mujoco | real-site | …),
env_id/task name, version, content_hash, embodiment_ref, scene_refs
(asset links — Objaverse/YCB stay INTEROP link+cache), config (physics,
randomization spec), task_ref (file/code pointer + format tag),
success_desc (one line, human), twin_of (nullable env id)}`

**Decision — identity, not semantics.** The entity answers "is this the
*same* task/scene, and which version?", never "what is the task?". Rewards,
goal predicates and success criteria are code; schema-tizing them fails for
half the backends (BDDL/PDDL only ever worked inside their own ecosystems).
`task_ref` is a pointer + format tag so a future viewer can *render* a
BDDL/PDDL spec — pointer, not parse. The only machine-readable task-adjacent
field stays the episode **outcome enum** (success/failure/recovered — the
AgiBot failure-as-data posture, already in the element schema).

**Decision — real sites are environments, team-scoped.** A lab bench is
family `real-site`: its "config" is the calibration bundle (intrinsics/
extrinsics), workspace bounds, fixture/object inventory; scans/splats/photos
attach as `scene_refs`. **Team-scoped, referenced by projects** — a physical
bench outlives any one project, and two projects sharing it must share its
calibration history and twin edge. (Sim environments may stay project-scoped;
the entity carries `team_id` either way — moving scope later is the painful
migration this decision avoids.)

**Twin edge.** `env.twin_of` links a real site to its sim counterpart (and
back). This is what makes sim2real *queryable* and gives real-to-sim overlay
(render the twin scene through the calibration-posed camera over the real
video) a data model — the calibration is already in the episode element.

### Conditions — two layers, no taxonomy registry (decision)

- **Layer 1 — reserved keys** (typed, documented, default group-bys for the
  boards): `seed`, `variant`, `lighting`, `layout`, `object_set`,
  `distractors`, `split`.
- **Layer 2 — free-form** `conditions: map[string]string` on the episode
  element; any key is group-by-able by string equality.

No per-project condition schemas, no taxonomy registry — premature until
cross-project meta-analysis is real (recorded, §5). **Guard (review
anchor):** boards treat unknown keys *and values* as opaque strings — never
parse semantics out of `"bright-left"` (the `mixed_id_shape` lesson
generalized to vocabularies).

### Embodiment registry

`{id, name, robot_type aliases (LeRobot `info.json` values), description
source (URDF/MJCF ref via forge machinery), license, dof/joint summary}` —
**first form = J8's URDF manifest** (replay plan §6): a registry-row mapping
`robot_type` → known OSS descriptions (SO-100/101, ALOHA, Koch, Franka…),
license-checked per entry, fetched+cached through the existing GitHub-root
forge path, user-supplied fallback. Three consumers from day one: J8 W3's
pose panel, `Environment.embodiment_ref`, and Inspect's policy arch view
(config → robot link).

## 2. UI — no new tab

- **J8 Replay rail** gains a second library section: `Datasets |
  Environments` (envs grouped sim-families first, sites last). Detail view:
  scene preview, asset list, task pointer + success line, config, linked
  runs/datasets/episodes, twin link, version history.
- **Scene preview constraints** (survey §3.5 upheld): three.js for
  URDF/MJCF; **USD via host-side glTF export or rendered thumbnail** —
  USD-for-web stays "not yet" (experimental, noncommercial friction);
  splats (Spark/SuperSplat, EMBED) for real sites. Don't fight USD in the
  browser.
- **Player integration**: episode header gets an env context chip
  (env@version · variant · seed) resolving through `env_ref`.
- **Inspect** keeps file-level viewing of scene/task sources (a `.usd` /
  `.xml` / `.urdf` in a tree is a static artifact — J3's job), with the
  same handoff gate pattern if "Open as environment" proves wanted.
- **Sim-run adapter** (sibling plan) consumes the registry as its launch
  picker — choosing an env for a training/eval run stops being a free-text
  config field (E4).

## 3. Wedges

- **E0 — reservations (inside J8 W1, no entity yet):** opaque `env_ref =
  "family:env_id@version"` (nullable, unvalidated) on runs, datasets, and
  the episode element; J8 W1 writes it where cheaply derivable, else null.
  Data accumulates before the registry exists and later *resolves* into
  rows instead of being backfilled by guesswork.
- **E1 — embodiment manifest → registry (with J8 W3):** ship the manifest
  as data (registry-row shape from day one); promote to hub entity when E2
  lands. License vetting per entry recorded in the manifest itself.
- **E2 — Environment entity + library:** hub CRUD (team/project scoping per
  §1), `env_ref` resolution (string → row match by `family:env_id@version`,
  surfaced as "unresolved" chip when no row), J8 rail section + detail
  view, sim-family preview paths.
- **E3 — real sites + twins:** "register/refresh site from calibration
  bundle" primitive (the bundle is the registration moment — decision:
  manual-but-assisted, no ambient discovery); recalibration bumps version
  (content hash), twin edges persist across versions; splat/scan attach.
- **E4 — launch-picker integration:** blocked on the sim-run adapter
  sibling plan; the registry is its env parameter source.

### E0 as shipped (datasets 2026-07-29, runs + episodes 2026-07-30)

Datasets got theirs with the entity itself (migration `0068_datasets`,
`env_ref TEXT NOT NULL DEFAULT ''`, derived host-side as
`lerobot:<robot_type>` where `meta/info.json` names one — `datasetmeta`'s
`Info.envRef`). The other two legs landed together, and neither is quite what
this section promised.

**Runs — a column, and deliberately nothing that fills it.** Migration
`0072_runs_env_ref` adds the same-shaped column; it is settable at
`POST /runs`, patchable at `PATCH /runs/{run}`, returned by list and get, and
carried by the `runs_create` / `runs_update` MCP schemas. §3 says "J8 W1 writes
it where cheaply derivable" — for runs, **nothing is**. The one signal within
reach is the linked dataset's handle, and taking it would be wrong exactly
where env identity earns its keep: a dataset's `env_ref` says where its DATA
was collected, while an eval run rolls out somewhere that may differ. So the
write stays an explicit act, the posture W5 already took for `dataset_id`
itself, and a test pins it (`TestRunEnvRef_IsNotInferredFromTheLinkedDataset`).

**Episodes — there was nothing to add a column to.** §3 names "the episode
element", but the element store is a *discussion-level* model
([`research-material-data-model.md`](../discussions/research-material-data-model.md));
there is no `elements` table in `hub/migrations`, and per ADR-060 an episode
becomes a hub row only when something references it. Both halves of that are
still unbuilt, so E0's episode leg landed as:

- an **override slot** on the host-served episode row
  (`datasetmeta.Episode.EnvRef`), emitted only when an episode's own metadata
  names an environment different from its dataset's — which neither LeRobot
  generation records, so today it is always absent. Stamping the dataset's
  handle onto every row would ship one string 50k times to repeat what the
  dataset already answered;
- the **resolution** that makes the slot meaningful, `episode.env_ref ||
  dataset.env_ref` (`replayDigest.episodeEnvRef`), because a consumer reading
  the row directly would report "no environment" for every episode that has
  one;
- §2's **env chip** on the episode player header, showing the handle verbatim.
  E0 has no registry to resolve against, so there is no "unresolved" state to
  show yet — that arrives with E2 and must never become a hard error.

Where it shows: the run overview in desktop **RunDetail** (a row that renders
only when set, so nearly every run looks unchanged) and the **episode player
header**. There is still no UI that *writes* an `env_ref` — agents set it
through `runs_update` / `datasets` PATCH, humans through the API.

**Still open from E0:** the durable episode reference (element or eval-rollout
row) carries the column when that entity exists; until then an episode's
environment is its dataset's. Mobile shows no `env_ref` anywhere — it reads
runs untyped, so the field arrives but is unrendered.

## 4. Review anchors

- **Opaque-conditions guard** (§1): no semantic parsing of condition
  values anywhere — boards, digests, filters.
- **Scope honesty**: sites team-scoped from the first migration; no
  project-scoped interim (the migration this plan exists to avoid).
- **`env_ref` is unvalidated by design** in E0 — no format enforcement
  beyond the string shape; validation arrives with E2 resolution (typed
  "unresolved", never a hard error on old rows).
- **No uncapped reads**: preview exports, calibration bundles, manifests —
  all capped/fetch-on-demand; bytes stay on the box (hub-index/host-bytes
  law).
- **Real fixtures**: E2/E3 tests use a real calibration bundle shape and a
  real `robot_type` value set from public LeRobot datasets (the
  hand-built-fixture bug class).
- **i18n en+zh parity** for every new string.

## 5. Recorded, not scheduled

- BDDL/PDDL task-spec *viewer* (render the `task_ref` pointer).
- Condition taxonomy registry / cross-project meta-analysis vocabulary.
- USD-in-browser (revisit only if USD-for-web licensing/maturity flips).
- Env **version diff UI** (config/scene delta between versions).
- Real-to-sim **overlay** itself (the moat board — needs J8 player + E3
  twins shipped; scheduled with the manipulation-analysis plan).
- Generative scene pipelines (RoboCasa/Infinigen/Holodeck/Cosmos) as
  dispatch jobs producing registered environments.

## 6. Open questions (genuinely remaining)

1. **Calibration bundle format** — standardize on one tool's output
   (which?) or sniff the common few; decides E3's "assisted" ergonomics.
2. **Manifest license vetting** — one-time per entry at authoring, or
   re-checked in CI? (Entries are pointers; upstream relicensing is rare
   but real.)
3. **`env_ref` derivation coverage** — how much can J8 W1 actually derive
   from LeRobot `info.json` (robot yes; task/scene identity is weaker);
   measure on real datasets before promising resolution rates.

## Related

- [replay-datasets-episodes.md](replay-datasets-episodes.md) — J8; carries
  E0 (`env_ref`) and E1's first form (URDF manifest).
- [`embodied-ai-research-workbench.md`](../discussions/embodied-ai-research-workbench.md)
  §10 — parent open questions; this plan resolves the entity side.
- [`embodied-ai-tooling-landscape.md`](../discussions/embodied-ai-tooling-landscape.md)
  — physical-coherence finding, `rig_id`, USD-for-web posture, asset
  INTEROP rows.
- [`research-material-data-model.md`](../discussions/research-material-data-model.md)
  — element types; the episode element this plan gives a referent to.

# Compare wall + run-linked decisions — building the moat surfaces (J5/J6)

> **Type:** plan
> **Status:** In flight (2026-08-02) — two lanes: Lane A (comparison wall,
> wedges A1–A6) and Lane B (decision records, wedges B1–B3). Lanes are
> independent; A1 and B1 can start in parallel. **A1–A3 shipped 2026-08-02**
> (wall state, runs table, extremes, config comparer, smoothing, group-by +
> seed aggregation, `run_metrics`, `run_config_diff`) **and B1** (the
> `records` entity); A4–A6, B2 and B3 unstarted.
> Executes the strategic call in
> `desktop-design-review.md` §4.1 ("cap reader investment, put the next big
> block into J5/J6") now that the data substrate those surfaces were waiting
> on has shipped. **2026-08-01:** adopted as the Compare/Record leg (lane K)
> of [agent-desktop-coworking.md](agent-desktop-coworking.md) / ADR-064 —
> agent verbs here now carry that contract's consent + attribution posture.
> **Audience:** principal · contributors
> **Last verified vs code:** 2026.730.1242 (origin/main `2bc604cc`)

**TL;DR.** The two surfaces both landscape docs identify as the moat — the
run-comparison wall (J5, "the headline BUILD — no embeddable OSS component
exists") and decision capture with provenance (J6, "the weakest cluster in
the market — no product links decisions to the runs that motivated them") —
are still the July-2026 first cuts: `CompareSurface.tsx` is 196 lines,
`RecordSurface.tsx` is 108 lines of device-local JSON. Meanwhile the substrate
excuse has expired: runs carry `config_json`, `seed`, `parent_run_id`,
`dataset_id`; the hub serves per-run metrics plus config/system-metrics/alerts
digests; the host-runner reads tfevents natively; and J8 shipped datasets,
episodes, and the embedded Rerun viewer. Lane A composes the best-in-class
comparison grammar (`research-app-product-landscape.md` §5.2) over that
substrate; Lane B makes decisions/findings hub entities whose evidence links
runs, episodes, and references — the join no competitor has. Both lanes ship
their agent-facing MCP tools with the UI (ADR-062 D-2: a UIRef is only a join
key if the entity behind it is agent-addressable).

## 1. Context and grounding

What exists at `de201ca9`:

- **Runs**: `handlers_runs.go` — `id, project_id, agent_id, config_json,
  seed, status, started/finished_at, trackio_host_id/run_uri, parent_run_id,
  dataset_id`. Fork lineage and seed grouping need **zero schema work**.
- **Metrics**: `/runs/{id}/metrics` (step-indexed curves, `last_value`),
  plus the trackio sibling digests in `handlers_run_extras.go` (`/config`,
  `/system_metrics`, `/alerts`), histograms, images. Host side:
  `hostrunner/tbreader/` parses tfevents; `metrics_poll.go` ships digests up.
- **CompareSurface** (196 LoC): project picker → run multi-select → per-metric
  overlay charts + last-value table. Its own header comment promises "next
  rounds add the config-diff panel" — this plan is those rounds.
- **RecordSurface** (108 LoC): ADR-shaped form appending to a device-local
  `useJsonDraft` log. Its header states the target this plan implements:
  "records eventually link to the runs that justify them."
- **J8**: datasets/episodes/series in the hub, `EpisodePlayer`, `rerunweb`
  partition, Export-to-Rerun. Eval episodes are addressable entities.
- **Prior art in-repo**: `run-detail-ui.md` (mobile run detail, Done) chose
  the heuristic-headline-metrics + live-poll grammar and deferred
  "parent-diff" — Lane A is where that deferral lands, on the wide screen.
- **Mined patterns**: landscape §5.2 table (W&B baseline deltas, ClearML
  diff-only comparer + extremes table, Comet "what changed" triad, Aim
  group-by/faceting, Neptune fork lineage), §5.3 (TRI STEP: small-n success
  rates need CIs by default), §6 (schema-on-tag decisions, provenance as the
  differentiator), and `governed-actions-and-propose-verb.md` for Lane B's
  propose/accept posture.

## 2. Goals / non-goals

**Goals**

1. One wall state (visible runs, filter, baseline, smoothing, x-axis) driving
   *all* panels — table, charts, comparer.
2. Answer "what changed between these two runs?" and "which config mattered?"
   without leaving the surface.
3. Robotics-honest statistics: success rates carry binomial CIs; episodes sit
   beside curves.
4. Decisions/findings as team-scoped hub entities with live links to the
   runs/episodes/references that justify them, writable by agents under the
   propose verb, renderable as jump-chips everywhere.
5. Agent parity: everything the wall and the record log can show, an agent can
   query via MCP tools; compare/record UIRefs dereference (ADR-062 D-2).

**Non-goals**

- No sweep orchestration (Optuna dashboard embed stays deferred as its own
  EMBED; the wall renders what exists).
- No W&B-style hosted reports; the hub digest remains the narrative channel.
- No schema-on-tag grafting in Author docs in this plan (landscape §6.1) —
  Lane B ships the entity + explicit capture; grafting `#decision` onto
  arbitrary cards is a follow-on once block IDs exist.
- No new metric ingestion formats (tfevents + trackio digests are the
  contract; a wandb-shim client is separate work).

## 3. Design — Lane A: the comparison wall

### 3.1 Wall state (A1)

One zustand store (`state/compareWall.ts`), URL-less deep-linking via UIRef
(3.4): `{ projectId, selected: string[], baseline: string | null, filter,
smoothing: number, xAxis: 'step' | 'relative', groupBy: string | null }`.
Persisted per project. All panels subscribe to this store — the §5.2 rule
("one visible-runs state drives all panels") is an architecture constraint,
not a feature.

- *As built (A1):* `projectId` sits BESIDE the view rather than inside it —
  it is the persistence key, and a project id nested in the thing it keys
  can disagree with it. The blob is `{projectId, byProject}`, capped at 20
  projects (a wall view is tiny, but the map would otherwise be
  append-only for the life of the install).
- *As built (A1):* `CompareSurface` no longer computes an "effective
  project" of its own; it resolves one INTO the store. The two-state
  version would have rendered project A's remembered runs beside project
  B's run list, each half correct and the screen wrong.
- *As built (A1):* every setter routes through one `edit()` that re-heals
  the cross-field invariants (a baseline is always a member of `selected`;
  smoothing is clamped; an unknown `xAxis` falls back) and returns its
  INPUT object on a no-op, so identity is the store's "nothing changed"
  signal and a keystroke that changes nothing writes nothing.

### 3.2 Panels (A1–A3)

- **Runs table** (A1): today's checkbox list grows filter-as-you-type over
  id/status/config keys, a **baseline pin** (star toggle; baseline gets
  distinct curve styling), and Δ-vs-baseline columns for each summary metric
  (`last_value` deltas, colored by sign; W&B's biggest 2025–26 comparison
  investment, per the landscape).
  - *As built:* the filter narrows the **rail, never the wall** — a run you
    selected and then typed past keeps its curve, because hiding a
    comparison as a side effect of searching for something else is a
    silent edit to the thing being compared. `config_json` already rides
    the run list, so config filtering costs no extra request.
  - *As built:* pinning a run that is not selected SELECTS it. The
    alternative is a star click that appears to do nothing (the invariant
    would drop a baseline that is off the wall).
  - *As built:* Δ cells are coloured by **direction, not valence** — new
    `--delta-up`/`--delta-down` semantic tokens rather than `--ok`/
    `--danger`. Whether up is good depends on the metric (loss down,
    reward up) and nothing on this surface knows which, so green/red would
    render a guess as a fact. A metric a run never logged shows `—`, never
    a zero delta: those read identically and mean opposite things.
  - *As built:* `ChartView` gained per-series `color` + `dashed`. The
    baseline's curve is the dashed one; the explicit colour also closes a
    latent hole in #322's single-palette promise — the renderer coloured
    by position in the SERIES array, the wall by position in the
    SELECTION, and the two diverge the moment a selected run has no points
    for one metric.
- **Extremes table** (A2): per metric × run, `last/min/max` with
  best-per-row highlighting (ClearML). Min/max come from the points already
  shipped; no hub change.
  - *As built:* min/max render as a sub-line inside the summary cell, not as
    a separate table — N runs × 3 numbers as columns is unreadable past
    three runs, and the extremes belong beside the value they qualify. The
    line is suppressed when `min === max` (a flat or single-point curve),
    where it would only add noise.
  - *As built:* there is **no best-per-row highlighting**, deliberately.
    "Best" needs a direction and nothing on this surface knows whether a
    metric wants to go up or down — the same reason Δ is coloured by sign
    rather than by goodness. Declaring `eval/success = 0.2` the winner of
    its row because it is the largest would be a confident lie. A6's
    eval-metric convention (or `_provenance.eval_metrics`, §7 Q1) is what
    would make direction knowable; until then the row shows the numbers and
    lets the reader rank them.
  - *As built:* `last` prefers the hub's own `last_value` (authoritative
    even when the shipped points were downsampled) and falls back to the
    last point; min/max come from the points, so they describe the curve
    that is actually drawn.
- **Diff-only run comparer** (A2): pick 2–N runs → flattened `config_json` ∪
  `/config` digest keys, identical rows hidden by default ("show identical"
  toggle), next-diff navigation. Pure function over data the desktop already
  fetches — and the reason it must be a pure function is testability (§5).
  - *As built:* the two sources are **unioned with the logged digest
    winning**, and the keys where they disagree are surfaced as a count
    rather than silently resolved. "What we said we would run" vs "what
    ran" is a provenance finding, and A4's triad is where it gets a proper
    home; swallowing it here would have hidden the more interesting half.
  - *As built:* an **absent** key counts as a difference. Two runs where
    only one sets `resume_from` differ, even though only one has anything
    to show — hiding that row would hide the actual difference between the
    runs.
  - *As built:* **no next-diff navigation.** It exists to skip over
    identical rows, and the diff-only default already removes them; adding
    a jump control for a list that is by default all-diffs would be a
    button for a problem the default solved. If "show identical" over a
    Hydra config proves unreadable in practice, that is when it earns its
    place.
- **Smoothing + x-axis** (A2): EMA ghost line (raw at low opacity — the
  TensorBoard/W&B muscle memory) and step/relative x-switch. Wall-clock
  x-axis is **deferred**: metric points carry `step` only; adding per-point
  timestamps is a tbreader/digest change to make deliberately, not en passant.
  - *As built:* the EMA is **debiased** (`acc / (1 - weight^n)`), which is
    the part a re-derivation gets wrong: a plain EMA seeded at zero drags
    the head of the curve down, so a loss appears to start far below where
    it did. `ChartView` gained `opacity` + `legendHidden` so the raw ghost
    rides under its smoothed line as the same run, not a second one.
  - *As built:* `relative` means **steps since each run's own first point**
    — the honest reading of the data we have, and the one that makes a run
    resumed at step 5000 comparable with one started from scratch. Wall-clock
    stays deferred exactly as written above.
- **Group-by + seed aggregation** (A3): group runs by a chosen config key →
  one color per group, member curves thin, group mean bold with a ±band; the
  `seed` column exists precisely for this. Facet-per-group is a stretch goal
  within A3.
  - *As built:* the picker offers only the keys that actually **vary** across
    the selection (it reads A2's row model), because grouping by a key every
    run shares is the ungrouped chart with extra steps. `seed` — a run
    column, not a config leaf — rides the same namespace under `#seed`, a
    spelling no flattened config can produce.
  - *As built:* the mean is taken on the **union of the members' steps**,
    with each member linearly interpolated onto it and **never
    extrapolated**. Members of a group log at different steps (every 100 vs
    every 128 is enough), so a mean taken only where samples coincide would
    be an interleaving of raw curves wearing the word "mean"; a member that
    stopped early simply drops out, and each aggregated point carries `n`.
  - *As built:* the band is mean ∓ one **sample** standard deviation, and a
    single-member group draws **no band at all** — a zero-width ribbon would
    suggest a spread that was never measured.
  - *As built:* under grouping, smoothing is applied to each member
    **before** aggregating. Smoothing only the mean would draw a bold line
    that wanders outside its own band, since the two would then describe
    different data.
  - *As built:* `ChartView` gained `bands` (a filled envelope, with the
    y-domain widened to contain it) and per-series `width`. Facet-per-group
    was NOT built — the stretch goal stays a stretch goal.

### 3.3 "What changed?" triad (A4) and lineage (A5)

- **Config diff** ships in A2. **Git + environment diff** need capture at
  spawn: the host-runner records `git_sha`, `git_dirty` (boolean + short
  stat), and an environment fingerprint (packages hash; the env-profile id it
  launched under) into the run's `/config` digest under a reserved
  `_provenance` key — additive, no schema migration, same data-ownership law
  (digest in hub, bytes on host). The comparer then renders the triad:
  config diff · code state diff · env diff (Comet/ClearML's highest-value
  run-detail idea, per the landscape).
- **Fork lineage** (A5): `parent_run_id` rendered as one continuous curve
  with a fork marker at the child's first step (Neptune's checkpoint-restart
  grammar). Table groups children under parents behind a disclosure.

### 3.4 Success rates + episodes (A6) and UIRef

- Metrics matching the eval convention (name prefix `eval/`, values in
  {0,1} or a `success` summary) get **Wilson-interval CIs by default** —
  small-n success rates without CIs mislead (TRI STEP; borrow methods, never
  code — its license is non-commercial). Rendered as bars-with-whiskers on
  the wall and beside the `EpisodePlayer` when `dataset_id` links a run to
  its eval episodes: curves left, episodes right, one selection.
- **Compare UIRef** (ADR-062 §3.0 pattern):
  `{ "surface": "compare", "wall": { "project_id": "…", "runs": ["…"],
  "baseline": "…", "metric": "…" } }` — emitted in the focus snapshot,
  accepted by ref-chips (a chip in a transcript focuses the wall with that
  exact state), copyable from the palette ("Copy compare ref").
  - *State after G4:* the focus snapshot carries the policy row's existing
    two-field `compare` block — `{left, right}`, baseline first — and
    publishes it **only when exactly two runs are selected**, because two
    fields cannot state a three-run wall without silently truncating it.
    So A6's job here is the `wall` block itself (the whole selection, the
    project, the metric) plus the accept/copy legs; when it lands, decide
    whether `compare.left/right` stays as the two-run shorthand or retires
    into it.

### 3.5 Agent surface (ships inside A1/A2, not after)

Audit which run verbs agents already have; add the missing read tools so the
wall's queries are also agent queries: `runs_list(project, filter)`,
`run_metrics(run)`, `run_config_diff(runs[])` (returns exactly the comparer's
row model), `run_provenance(run)`. Purpose-built beats generic (landscape
§7.1); the comparer's pure functions are reused server-side or client-side —
one row model, two consumers.

- *As built (A1):* the audit found `runs_list` and `runs_get` in the
  authority registry and **no** `run_metrics` in either registry, so A1
  ships `run_metrics` (catalog + spec + `toolMeta` row; `runs_get` now
  points at it). `run_config_diff` and `run_provenance` stay with A2 —
  one needs the comparer's row model, the other needs the host-runner's
  `_provenance` capture, and neither exists yet.
- *As built (A1):* `runs_list` was NOT given a free-text `filter`. It
  already takes `project`, and its rows already carry `status` +
  `config_json` — the wall's filter is a view over exactly those rows, so
  an agent filters what it already has. A server-side text filter would be
  a second, drifting definition of "matches".
- *As built (A2):* `run_config_diff` ships with the comparer, returning
  `{runs, rows:[{key, values, identical}], differing, conflicts}` — the row
  model the panel renders, with `null` where the desktop has `undefined`.
  The promise that these are ONE row model is enforced by a **shared
  fixture**: `hub/internal/hubmcpserver/testdata/config_diff_fixture.json`
  is read by the Go test AND by `compareRuns.test.ts`, so changing a
  flattening rule in either language fails the other's suite. That is the
  only thing that keeps two implementations of one contract honest — and
  the rules it pins are finer than they look: dotted paths, absent ≠ empty,
  and **JavaScript's** number→string (decimal in `[1e-6, 1e21)`, exponent
  without leading zeros), because a row model that disagrees on `1e-7` vs
  `1e-07` is not one row model.
- *As built (A2):* the tool is capped at 8 runs — each id costs two hub
  round-trips, and the cap is stated in the schema (`maxItems`) so an agent
  is refused by the contract rather than by a surprise.
- *Found while auditing:* `run.metrics.read` is named in two agent prompts,
  two bundled templates, `roles.yaml worker.allow` and ADR-016's scope
  manifest — a capability with no tool behind it (already flagged in
  `agent-tool-ergonomics-rollout.md`). The prompts and templates now name
  `run_metrics`, which exists. `roles.yaml`'s dead entry and the ADR are
  left alone: registry tools never reach the manifest (`authorizeMCPCall`
  consults the spec's `WorkerEligible` first), and an Accepted ADR is
  immutable.

## 4. Design — Lane B: decision records

### 4.1 Entity (B1)

Hub table `records` (team-scoped via project, ADR-numbered decision optional):

```
id, project_id, kind ('decision'|'finding'), title, body_md,
status ('proposed'|'accepted'|'superseded'), supersedes_id,
created_by_kind ('user'|'agent'), created_by_id, origin_session_id,
links_json [ {kind:'run'|'episode'|'dataset'|'reference'|'doc', id, note} ],
created_at, updated_at   -- UTC, as everything
```

REST + store follow the reference_items pattern (ADR-053). Evidence links are
**typed ids, not URLs** — the same shape as UIRef entity fields, so a link
renders as a jump-chip and an agent dereferences it with its existing tools.

- *As built (B1):* migration `0074_records`, five REST routes, the typed
  client methods, and an OpenAPI + glossary entry. Three rules carry the
  entity and each of them is a refusal:
  - **Provenance comes from the token.** `created_by_kind` /
    `created_by_id` / `origin_session_id` are derived from the
    authenticated caller and ignored if a body sends them; an agent's
    record is created `proposed` even when the body claims `accepted`
    (the propose-verb posture, §4.3, enforced at the entity rather than
    left to the tool). An agent whose handle no longer resolves is still
    recorded as an AGENT — falling back to "user" would misattribute the
    write to the director.
  - **Status is history.** `proposed → accepted` is the only patchable
    transition; `superseded` is not a setting but a consequence.
    `POST /records/{id}/supersede` creates the successor and the edge,
    and the predecessor is retired **only when that successor is
    accepted** — a proposal must not retire the decision it hopes to
    replace. A superseded record is closed for edits.
  - **Evidence is a closed vocabulary.** `{kind,id,note}` with kind ∈
    {run, episode, dataset, reference, doc}, and the test for adding one
    is that the hub can already dereference it: a link kind with no
    dereference is a dead chip. An empty `id` is refused — evidence is a
    typed id, not a note.
- *As built (B1):* deletion is allowed only while a record is `proposed`
  (dismissing a proposal); accepted and superseded records answer 409 and
  name the supersede route. The plan's "delete only device-local drafts"
  reads the same way once the drafts are hub proposals.
- *Done in G4/G5:* `record.dataset_id` is now `record.record_id`, renamed
  in the same change that populated `compare.*` (both live in
  `ui_policy.ts` and in one bidirectional CI-run test). The field is still
  a **declared gap**, and B2 is what closes it: Record writes device-local
  drafts whose ids dereference through no tool, and a UIRef whose entity is
  not agent-addressable is not a join key (ADR-062 D-2). Publishing a draft
  id would have been a handle that resolves nowhere.

### 4.2 Surface (B2)

RecordSurface v2: hub-backed list + detail; the ADR-shaped form keeps its
title/context/decision/consequences grammar but gains an **evidence strip**
(pick runs from the wall selection, the open episode, a library reference);
status chips with supersede action (creates the successor stub and the
`supersedes` edge — Tana's "commands on tags," minus the tags). Device-local
records from v1 get a one-shot import. Deleting stays possible only for
device-local drafts; hub records supersede instead (decision history is the
point).

### 4.3 Agent verbs (B3)

MCP tools `record_propose`, `records_list`, `records_get`,
`record_supersede`. Agents create **proposed** records only — acceptance is
the director's click (the propose-verb posture from
`governed-actions-and-propose-verb.md`; no new attention-card kind needed,
the record list badges proposals). Provenance fields are derived from the
authenticated agent identity server-side, never from the body (the F-08
lesson: attribution comes from the token). A briefing/steward agent that
closes an experiment loop can then write "finding: lr=3e-4 beats 1e-3,
evidence: runs X,Y, episodes Z" — and the record is *checkable*, every chip
jumps.

## 5. Testing

- Pure-function tests (desktop, `node --test`): config flatten + diff-only
  row model (nested/absent/type-changed keys), EMA smoothing, Wilson
  interval, group-by aggregation (mean/band with missing steps), fork-curve
  stitching. These are the "silently corrupt data" class the design review
  said to cover first.
- Hub tests: records CRUD + status transitions (proposed→accepted,
  supersede chains, refuse delete of accepted), links_json validation
  (unknown kinds refused), agent-proposed provenance derived from token,
  team-scope on every handler (the `runInTeam` pattern).
- Provenance capture: host-runner test that a spawned run's `/config` digest
  carries `_provenance` with git sha/dirty + env fingerprint; absent
  gracefully for non-git workdirs.
- Wall state: persistence round-trip; UIRef emit/accept round-trip (chip →
  exact wall state).

## 6. Risks

- **Chart scalability**: overlaying many runs × many points in `ChartView` —
  the hub digests are already downsampled (bounded points per curve), so the
  wall inherits that bound; if a future raw-series path lands, server-side
  LTTB (landscape §5.1) becomes the mitigation, not client heroics.
- **Config diff explosion**: deeply nested configs (Hydra) flatten to
  hundreds of keys; diff-only default + key-prefix collapsing keeps it
  readable. Test with a real Isaac Lab config.
- **Records scope creep toward a document system**: the entity is a log with
  links, not a wiki — `body_md` is small, evidence is chips, authoring
  happens in J2. The non-goals above are the fence.
- **Two lanes touching the same surfaces as the split-pane plan** — land
  `desktop-shell-split-pane.md` S1 first; the wall-beside-episodes and
  record-beside-wall postures are the payoff and will shake out pane-width
  issues early.

## 7. Open questions

1. Eval-metric convention: is `eval/`-prefix + boolean values enough to
   detect success-rate series, or should the run config declare them
   (`_provenance.eval_metrics`)? Lean: declare, with the prefix as fallback.
2. Should accepted records mirror into the project digest automatically
   ("decisions this week"), or stay pull-only until digest noise is assessed?
   Lean: mirror — provenance-bearing decisions are exactly what a PI wants in
   the weekly narrative.
3. Numbering: do accepted `decision` records get a monotonic per-project
   number (ADR discipline) or stay id-addressed? Lean: number them; humans
   cite numbers.

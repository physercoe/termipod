# Compare wall + run-linked decisions — building the moat surfaces (J5/J6)

> **Type:** plan
> **Status:** Proposed (2026-07-30) — two lanes: Lane A (comparison wall,
> wedges A1–A6) and Lane B (decision records, wedges B1–B3). Lanes are
> independent; A1 and B1 can start in parallel. Executes the strategic call in
> `desktop-design-review.md` §4.1 ("cap reader investment, put the next big
> block into J5/J6") now that the data substrate those surfaces were waiting
> on has shipped.
> **Audience:** principal · contributors
> **Last verified vs code:** 2026.727.206-alpha (origin/main `de201ca9`)

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

### 3.2 Panels (A1–A3)

- **Runs table** (A1): today's checkbox list grows filter-as-you-type over
  id/status/config keys, a **baseline pin** (star toggle; baseline gets
  distinct curve styling), and Δ-vs-baseline columns for each summary metric
  (`last_value` deltas, colored by sign; W&B's biggest 2025–26 comparison
  investment, per the landscape).
- **Extremes table** (A2): per metric × run, `last/min/max` with
  best-per-row highlighting (ClearML). Min/max come from the points already
  shipped; no hub change.
- **Diff-only run comparer** (A2): pick 2–N runs → flattened `config_json` ∪
  `/config` digest keys, identical rows hidden by default ("show identical"
  toggle), next-diff navigation. Pure function over data the desktop already
  fetches — and the reason it must be a pure function is testability (§5).
- **Smoothing + x-axis** (A2): EMA ghost line (raw at low opacity — the
  TensorBoard/W&B muscle memory) and step/relative x-switch. Wall-clock
  x-axis is **deferred**: metric points carry `step` only; adding per-point
  timestamps is a tbreader/digest change to make deliberately, not en passant.
- **Group-by + seed aggregation** (A3): group runs by a chosen config key →
  one color per group, member curves thin, group mean bold with a ±band; the
  `seed` column exists precisely for this. Facet-per-group is a stretch goal
  within A3.

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

### 3.5 Agent surface (ships inside A1/A2, not after)

Audit which run verbs agents already have; add the missing read tools so the
wall's queries are also agent queries: `runs_list(project, filter)`,
`run_metrics(run)`, `run_config_diff(runs[])` (returns exactly the comparer's
row model), `run_provenance(run)`. Purpose-built beats generic (landscape
§7.1); the comparer's pure functions are reused server-side or client-side —
one row model, two consumers.

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

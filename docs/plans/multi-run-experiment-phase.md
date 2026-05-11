---
name: Multi-run experiment phase
description: Turn the research lifecycle's experiment phase into the multi-run shape it always implied — N parallel runs with distinct configs feeding one aggregate metric-chart and one experiment-results deliverable. Drop `sweep_compare` (subsumed by `experiment_dash` + multi-series chart) and retire the legacy `--shape ablation` seed plus its `ablation-sweep` / `benchmark-comparison` templates.
---

# Multi-run experiment phase

> **Type:** plan
> **Status:** Proposed (2026-05-11)
> **Audience:** principal · contributors
> **Last verified vs code:** v1.0.504

**TL;DR.** The lifecycle research template encodes an experiment phase
that produces *one* run, *one* checkpoint, *one* eval chart. Real
research projects sweep over configs (model size × optimizer × seed);
the chassis and the metric-chart viewer already support that natively
(`experiment_dash` hero + multi-series `MetricChartBody`). This plan
brings the lifecycle into line with reality: `experiment-results`
becomes an N-run deliverable, the seed produces 3 runs by default, and
the `sweep_compare` hero (a legacy from before
[artifact-type-registry](artifact-type-registry.md) wave 2) gets
folded into `experiment_dash`. The legacy `--shape ablation` seed and
its `ablation-sweep` / `benchmark-comparison` templates are retired —
the lifecycle shape covers the case, and keeping two demo shapes alive
costs more than it explains.

## Goal

After this plan ships:

- The `research.v1` template's experiment phase declares
  `experiment-results` as a deliverable with N runs + per-run
  artifacts plus one aggregate metric-chart artifact. The deliverable
  schema is still ONE deliverable per project; the multiplicity lives
  inside `components`.
- `seed-demo --shape lifecycle` seeds 3 runs into the experiment phase
  with distinct configs (`n_embd ∈ {128, 256, 384}` × `optimizer = lion`,
  iters=1000) and ONE aggregate metric-chart with 3 series. Per-run
  per-checkpoint artifacts still attach so reviewers see the breadth.
- `experiment_dash` is the only experiment hero. Its in-hero
  metric-chart embed (v1.0.503) now shows the 3-series comparison
  inline; the per-series legend makes the sweep story visible from
  the project overview.
- `sweep_compare` is removed: hero file, registry slot, dispatch,
  hub-side enum, mobile widget tests. `SweepScatter` widget goes with
  it (only caller).
- The `--shape ablation` CLI flag, `seed_demo.go` ablation seeding,
  `ablation-sweep.yaml` template, and `benchmark-comparison.yaml`
  template are retired. `seed_demo.go` shrinks to its still-reachable
  callers (or is deleted in full if none survive).
- Documentation that refers to the ablation shape — blueprint P4,
  release-testing how-to, demo-script plan, local-dev-environment
  how-to — is updated to point at lifecycle.
- ADR-024 (project detail chassis) gains an amendment recording the
  hero list change.

## Non-goals

- Sweeps as a first-class concept across the rest of the system (e.g.
  Workspaces, multi-project sweep dashboards). The N-run shape lives
  inside one project's experiment phase only — that's the demo path.
- Resurrecting `benchmark-comparison` as a separate template. If
  comparing two models on one benchmark becomes load-bearing again, a
  new template can be authored; today there is no caller and no doc
  guidance that requires it.
- Run-level paging / pagination in the deliverable viewer. N is
  small (≤ 5 in the demo). If real users push N higher we can revisit.
- Mock-trainer changes. The lifecycle seed produces deterministic
  synthetic data; mock-trainer is for the legacy ablation flow and
  will be deprecated alongside `--shape ablation` (or kept as a
  hostrunner-side tool, decided in W4).

## Wedges

Five small wedges, each independently testable. Each wedge ships with
its own version bump + changelog entry.

### W1 — template: multi-run experiment-results

Source: `hub/templates/projects/research.v1.yaml`

- Replace the single `run` component with a repeatable shape. Two
  options on the table:

  a. **Inline N entries.** Components list grows from
     `[document, artifact×2, run]` to
     `[document, artifact-aggregate, artifact×N×2, run×N]`. The
     template still names them statically (seed maps `ablation-sweep-run`
     → `ablation-sweep-run-{1..3}`).

  b. **Multiplicity in the YAML.** Add a `count` / `template_count`
     hint per component. Seed + hub honor the hint. More work; less
     repetition in the YAML.

  **Pick a.** The template stays declarative + readable; the
  multiplicity is the seed's job. Templates can grow `count` later if
  a real workflow asks for it.

- Add an `aggregate-eval-chart` component (`kind: artifact`,
  `ref: eval-results-aggregated`) ahead of the per-run charts.
  This is the multi-series metric-chart the `experiment_dash` embed
  picks up.

- Update the `best-metric-threshold` criterion comment to note the
  metric comes from the aggregated chart (max across runs).

Acceptance:
- Hub `init_test.go` still verifies the template loads with the new
  shape; updated assertions for the multi-run component set.
- No mobile changes; the deliverable viewer already iterates components
  generically.

### W2 — seed: 3 runs + aggregated chart

Source: `hub/internal/server/seed_demo_lifecycle.go`

- Replace the single `run` + single `evalArt` + single `ckptArt` with
  loops over the 3 sweep configs declared in the template's parameters
  (`model_sizes: [128, 256, 384]`, `optimizers: [lion]`).
- For each `(size, optimizer)` pair:
  - `seedRun(c, "completed", {n_embd: size, optimizer: optimizer, iters: 1000})`
  - `seedArtifact(c, "external-blob", "best-checkpoint-step1000-n{size}.pt", ...)`
  - `seedMetricChartArtifact(c, "eval-results-n{size}.json")` with a
    per-run series (one series, points scaled per size: smaller models
    plateau lower, larger ones reach ~0.88).
- Add a NEW `seedAggregateMetricChartArtifact(c, "eval-results-aggregated.json")`
  that emits ONE artifact with 3 series, one per `(size, optimizer)`
  pair, using the brand palette. Body shape is identical to v1.0.502's
  `MetricChartBody` (already multi-series capable).
- Update `experiment-results` deliverable components to attach the
  aggregate chart first, then the N per-run charts, then the N
  per-run checkpoints, then the N run rows, then the existing
  bundle/canvas/pdf/png artifacts.
- Component `ord` ordering preserves a stable scan: aggregate-chart,
  per-run-charts, checkpoints, runs, bundle, canvas, pdf, png, commit.

Acceptance:
- Run `hub-server seed-demo --shape lifecycle` end-to-end; observe
  3 runs + 1 aggregated chart row + 3 per-run chart rows.
- Mobile `experiment_dash` hero embed shows the aggregated 3-series
  chart inline (the embed already picks the newest metric-chart by
  `created_at`; aggregate is inserted first so it stays newest? actually
  inserted last so it IS newest — adjust seed ord to insert aggregate
  *after* per-run charts; otherwise widget picks a per-run chart).
- The fullscreen `ArtifactMetricChartViewerScreen` renders all 3
  series with distinct colors + legend.

Open Q (W2-Q1): is "aggregate as newest" robust enough, or do we want
to gate the embed picker on filename / mime / hint? Decision before
implementation: stay with newest-first ordering plus a deterministic
seed insert order — no new gating logic — and revisit if real agents
end up producing aggregates that aren't newest.

### W3 — drop `sweep_compare` hero

Files:
- `lib/screens/projects/overview_widgets/sweep_compare.dart` — delete
- `lib/widgets/sweep_scatter.dart` — delete (only caller is the hero)
- `lib/screens/projects/overview_widgets/registry.dart` — remove
  `sweep_compare` from `kKnownOverviewWidgets`, `kOverviewWidgetSpecs`,
  and `buildOverviewWidget` dispatch
- `lib/screens/projects/project_detail_screen.dart` — drop comment
  reference at line 1176
- `test/widgets/overview_widgets_registry_test.dart` — remove
  `'sweep_compare'` from the slug list; add a regression guard mirroring
  v1.0.502's `portfolio_header` guard
- `hub/internal/server/init.go` — remove `sweep_compare` from
  `validOverviewWidgets`
- `hub/internal/server/init_test.go` — drop the rows in
  `defaultTemplateOverviewWidget` and the `valid widget` map; the
  template→hero map shrinks accordingly

Acceptance: `flutter analyze` clean; `go vet ./...` clean; mobile
registry test enumerates exactly the post-removal set; the chassis
fallback still returns `task_milestone_list` when a (legacy DB row)
declares `sweep_compare`.

### W4 — retire `--shape ablation` + legacy templates

Files:
- `hub/cmd/hub-server/main.go` — drop the `--shape ablation` branch
  + its description text; if `--shape` becomes single-valued, remove
  the flag entirely and run `seed-demo` directly as lifecycle. Decide
  in implementation; safer is to keep the flag with `lifecycle` as the
  only valid value, since the docs reference the syntax.
- `hub/internal/server/seed_demo.go` — delete (no other caller after
  the flag goes). Confirm by grepping for `ResetDemo` + `SeedDemoResult`
  callers. Anything left moves to `seed_demo_lifecycle.go` and is
  renamed.
- `hub/templates/projects/ablation-sweep.yaml` — delete
- `hub/templates/projects/benchmark-comparison.yaml` — delete
- `hub/templates/prompts/steward.v1.md` §"Decomposition recipe:
  benchmark-comparison" + §"ablation-sweep" — delete or rewrite to
  point at the lifecycle's experiment phase
- `hub/internal/server/init_test.go` — remove `ablation-sweep` +
  `benchmark-comparison` from `templateNames` and the parameter-key
  assertions
- `hub/internal/server/seed_demo_test.go` — delete or rewrite to
  drive the lifecycle seed path. The current test asserts on
  `template_id='ablation-sweep'` which won't exist
- `hub/internal/server/handlers_projects_test.go` — replace
  `template_id='ablation-sweep'` with `template_id='research.v1'`
  (the test exercises template→hero resolution; lifecycle covers it)
- `hub/cmd/mock-trainer/main.go` — flag default for `--project`
  changes from `ablation-sweep-demo` to … (decide in W4: either
  retire mock-trainer alongside the ablation shape, or keep it as a
  hostrunner tool with a generic default like `demo-project`).

Acceptance: `go test ./hub/...` clean. `seed-demo --help` no longer
documents ablation. `hub-server` startup still seeds the lifecycle
template; default team init unaffected.

Open Q (W4-Q1): keep `mock-trainer` or retire it? It's a real
hostrunner-side tool that some how-tos reference. Recommend keeping
it as a generic trainer simulator and updating its docs; deletion is
post-MVP scope.

### W5 — docs + ADR amendment

Files:
- `docs/decisions/024-project-detail-chassis.md` — add an "Amended
  2026-05-..." block recording the removal of `sweep_compare` from
  the closed-set hero list and the rationale (subsumed by
  `experiment_dash` + multi-series chart). No new ADR; this is a
  consequence amendment, the original decision still stands.
- `docs/reference/project-detail-chassis.md` — drop the
  `sweep_compare` row from the hero table.
- `docs/spine/blueprint.md` P4.1 — update the "ablation-sweep
  shipped … benchmark comparison deferred" text to reflect the
  lifecycle shape as the canonical multi-run path.
- `docs/plans/demo-script.md` — rewrite the ablation references to
  use the lifecycle seed. The script is canonical for the demo arc;
  it must match the seed.
- `docs/how-to/release-testing.md` — update the `seed-demo` examples
  + `mock-trainer` flags (or remove the mock-trainer section pending
  W4-Q1).
- `docs/how-to/local-dev-environment.md` §"The full recipe" — update
  the ablation-sweep loop reference.
- `docs/how-to/test-steward-lifecycle.md` Scenario 4 — replace the
  single-run expectation with the 3-run shape; update screenshots/
  observations.
- `docs/changelog.md` — single entry per shipping wedge (W1-W4 each
  ship their own bump; W5 piggybacks on W4 since docs ship with the
  code they describe).

Acceptance: `lint-docs.sh` clean; `lint-glossary.sh` clean; OpenAPI
spec validation still passes (no API surface changes).

## Order + dependencies

Strict order: W1 → W2 → W3 → W4 → W5. Each depends on the previous
landing because the test fixtures keep mutating until W4 lands.

- W1 + W2 can be one commit (template + seed are tightly coupled; the
  seed reaches into the template's component refs).
- W3 must wait for W2 because the post-W2 `experiment_dash` embed is
  what subsumes the `sweep_compare` use case; deleting the hero before
  the multi-series embed lands would regress the demo.
- W4 must wait for W3 because the test fixtures use
  `template_id='ablation-sweep'` to drive `sweep_compare` resolution.
- W5 ships with W4 (docs match what's in code after W4).

Estimate: W1+W2 = 1 day, W3 = 0.5 day, W4 = 0.5 day, W5 = 0.5 day.
Realistic 2-3 commits over 1-2 sessions.

## Open questions

- **OQ-1** (W2-Q1): aggregate chart selection — "newest by created_at"
  vs. explicit "primary" hint. **Locked**: stay with newest-first +
  deterministic seed insert order; revisit if agents produce
  non-aggregate-newest chart artifacts in the field.
- **OQ-2** (W4-Q1): retire `mock-trainer` or keep it? **Decision in
  W4 implementation**; default: keep as generic trainer simulator,
  retitle docs.
- **OQ-3**: should the multi-run shape gain a per-run sub-row inside
  the deliverable viewer (collapsed by default)? Today the components
  list shows N runs as N flat rows. Defer to a follow-up wedge if
  testers complain about the flat list at N=3.

## Risk + reversibility

- **Risk:** the legacy ablation path is referenced in some docs +
  test fixtures we didn't enumerate. Mitigation: W4 ends with a
  full-repo grep for `ablation-sweep` + `benchmark-comparison` +
  `sweep_compare` before commit; any straggler is patched in the
  same commit.
- **Reversibility:** W1+W2 are append-only template/seed changes —
  revert the diff to roll back. W3 deletes the hero; recovery via
  git revert of the W3 commit. W4 deletes a template + a test +
  flag; recovery via git revert. None of the changes touch DB
  schema or alter the wire shape of existing endpoints.

## Out of scope for this plan

- Multi-run-aware Insights aggregations.
- Engine-level sweep orchestration (the agent firing N runs vs. the
  seed faking them).
- UI for declaring sweep dimensions from the mobile (the steward + a
  hand-edited template still drive multiplicity today).

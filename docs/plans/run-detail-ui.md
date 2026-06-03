# Run detail UI — multi-view design

> **Type:** plan
> **Status:** Proposed (2026-06-03) — design only, not yet built. Director chose
> the **`View ▾` switcher** model with a glance **Overview**; **Outputs** is its
> own view, distinct from **Media**. Open questions resolved (see Decisions):
> live-poll metrics+alerts while running, heuristic headline metrics, sample-
> ordinal system x-axis, always-show empty views, sparklines-only at ≤150 pts,
> Delete-run action, searchable config (parent-diff later), run→agent link,
> trackio-only extras.
> **Audience:** contributors
> **Last verified vs code:** v1.0.796-alpha

**TL;DR.** The run-detail screen now has many kinds of data to show — identity,
config (hyperparameters), scalar metrics, system (GPU/CPU) metrics, alerts,
images, distributions, dashboards, artifacts, summary, metadata. Today it is one
long `ListView`, which buries the *"is it healthy and how's it going?"* answer.
Reshape it into a **`View ▾` switcher** (the same pattern the session surfaces
use) with five focused views — **Overview · Charts · Media · Outputs · Config** —
backed by an `IndexedStack` so each view keeps its scroll state. The Overview is
the glance: status, an **alerts banner**, **headline metric tiles**, and **config
highlights**. Hub-side data already ships (run-extras, v1.0.796); this plan is
the mobile surfacing.

## Why

The hub now stores, per run: scalar metric digests (`run_metrics`), system
metrics (`run_system_metrics`), config (`run_config`), alerts (`run_alerts`),
images (`run_images`), histograms (`run_histograms`), external dashboard URIs,
and artifacts. `RunDetailScreen` (`lib/screens/projects/runs_screen.dart`)
renders all of it as a single scroll. Adding config + system + alerts to that
scroll makes the important signals (health, headline numbers) hard to find under
heavy content (image scrubbers, dozens of sparklines).

**Director decisions (2026-06-03):**
- Layout = **`View ▾` switcher** (consistent with sessions / agent sheets).
- Overview glance features = **alerts banner + headline metric tiles + config
  highlights** (System health is NOT in the glance — it lives in Charts).
- **Outputs (artifacts) is a dedicated view**, separate from **Media** (images +
  distributions). Media = visual training artefacts; Outputs = produced files.

## Decisions (open questions resolved, 2026-06-03)

1. **Live updates.** While `status=running`, **poll `metrics` + `alerts` every
   ~20–30 s** (the two signals that change); config / media / outputs load once
   (pull-to-refresh re-fetches all). Stop polling on a terminal status.
2. **Headline metric selection.** A **heuristic** for now (priority list
   loss / accuracy / lr, then first N); user-pinned/starred metrics are a later
   enhancement.
3. **System x-axis.** **Keep the sample ordinal** (don't thread timestamps into
   the digest yet).
4. **Empty views.** **Always show** every view in `View ▾`; an empty one renders
   a quiet empty state (menu shape stays stable run-to-run).
5. **Chart depth.** **Sparklines only** for now (no tap-to-zoom chart). Bump the
   host-runner downsample cap to **≤150 points** (`Runner.MetricsMaxPoints`
   100 → 150, `hub/internal/hostrunner/runner.go:149`).
6. **Run actions (`⋮`).** Add **Delete run** now (confirm dialog →
   `DELETE /v1/teams/{team}/runs/{run}`, the existing `handleDeleteRun`; the
   mobile client needs a new `deleteRun`). Rename / re-attach deferred.
7. **Config rendering.** Flat **searchable key/value** (flattened dotted keys)
   now. A **"vs parent" diff** (fetch the `parent_run_id`'s `run_config`,
   highlight only differing keys old→new, "show only differences" toggle —
   matching W&B / MLflow compare) is a **follow-up**, not the first build.
8. **Run ↔ agent cross-link.** When a run has `agent_id` **and that agent still
   exists** (not deleted/archived — resolve against the hub agents list), the
   Overview shows an **"Open agent →"** link into the agent's Insight surface.
   Omit the link when the agent is gone.
9. **Tracker parity.** Accept **trackio-only** config / system / alerts for now
   (they ride `metrics.RunExtras`; wandb / TensorBoard runs simply omit those
   sections). Revisit wandb/TB extras later.

## Target shape

```
Run detail (phone)
  ┌─ header: [● status] <run name>        [View ▾]  [⚲ attach] [⚑ complete] [⋮]
  │  View ▾ → Overview · Charts · Media · Outputs · Config   (IndexedStack)
  │  ⋮ → Delete run                       (every view always shown, empty-stated)
  │
  │  [Overview]   ← the glance
  │    status strip: running · 2h14m · pi-box · @trainer
  │    ⚠ alerts banner (count + most-severe; tap → expand)
  │    headline metric tiles: loss 1.23 · acc 0.71 · lr 3e-4 (mini-sparks)
  │    config highlights: model=nanoGPT · batch=64 · lr=3e-4 …  → Config
  │    summary (markdown)
  │
  │  [Charts]   scalar metrics (grouped) · System (GPU/CPU) · Dashboards (links)
  │  [Media]    images (scrubbers) · distributions (histograms)
  │  [Outputs]  artifacts (checkpoints / files) → all via ArtifactsScreen
  │  [Config]   hyperparameters (searchable key/value) · metadata JSON
```

## Views

### Overview — the glance
Answers "is it healthy and how's it going?" before any scroll.
1. **Status strip** — `RunStatusChip` + status word, duration (live-ticking
   while running), host, agent, started/completed. One or two compact rows
   (reuse `_metaRow`, plus a duration that updates for live runs).
2. **Alerts banner** — only when `run_alerts` is non-empty. A level-coloured
   container: icon + "N alerts" + the most severe/recent alert (title + step).
   Tap expands the full list inline (level-coloured rows: error red, warn amber,
   info blue). No alerts → omit.
3. **Headline metric tiles** — a wrap/row of stat tiles for the key scalar
   metrics: name + `last_value` (large) + a mini-sparkline. Pick the first ~4 by
   a priority list (loss / *loss* / accuracy / acc / lr / learning_rate first,
   then remaining alphabetical); "See all → Charts". Values come straight from
   the digest rows (`last_value` / `points`).
4. **Config highlights** — chips for a curated hyperparameter subset
   (model / batch|batch_size / lr|learning_rate / steps|max_steps / epochs /
   seed); fallback to the first few keys. "See all → Config".
5. **Summary** — the run's markdown summary (`_SummaryBody`) when present.
6. **Open agent →** — when `agent_id` is set and that agent still exists, a link
   into the producing agent's Insight surface (decision 8).

### Charts
- **Metrics** — all scalar metric sparklines, grouped (reuse `_groupMetrics`,
  `_MetricSparklineTile`, `_MetricGroupTile`).
- **System** — `run_system_metrics` sparklines under a "System (GPU/CPU)"
  subheader. Same tile widgets; the x-axis is a sample ordinal (not a training
  step), so the tile's step label is suppressed/relabelled for this section.
- **Dashboards** — external tracker links (`_MetricURITile`, `_metricUris()`).

### Media
- **Images** — image series scrubbers (`_ImageSeriesTile`, `_groupImages`).
- **Distributions** — histogram series (`HistogramSeriesTile`,
  `_groupHistograms`).
- Empty (no images/histograms) → a quiet empty state.

### Outputs
- **Artifacts** — the run's produced files/checkpoints (`_RunArtifactTile`),
  first N + "View all → `ArtifactsScreen`". Dedicated view because outputs are a
  distinct concern from the diagnostic visuals in Media.

### Config
- **Hyperparameters** — full `run_config` JSON as a **searchable** key/value
  list (flatten nested objects to dotted keys; a filter field, since configs can
  be 50+ keys). A **"vs parent" diff** is a follow-up (decision 7).
- **Metadata** — the raw `metadata_json`, collapsible (reuse the pretty-printed
  JSON block).

## Data + client work

Extend `_RunDetailScreenState._load()` to also fetch the three new digests in
its parallel `Future.wait`, and add the Dart client reads mirroring
`getRunMetricsCached`:

- `runs_api.dart`: `getRunConfig` / `getRunSystemMetrics` / `getRunAlerts` +
  `…Cached` variants, hitting the v1.0.796 endpoints
  (`GET /v1/teams/{team}/runs/{run}/{config,system_metrics,alerts}`); plus
  **`deleteRun`** (`DELETE /v1/teams/{team}/runs/{run}`) for the `⋮` action.
- Pull-to-refresh re-runs `_load` (already present). A **live refresh timer**
  (~20–30 s) re-fetches `metrics` + `alerts` only while `status=running`
  (decision 1).

Hub work is minimal: the digest endpoints + storage shipped in `c805ea3`
(migration 0051). The only host-side tweak is bumping the metrics downsample cap
to ≤150 points (decision 5) — a one-line `Runner.MetricsMaxPoints` default
change.

## Reuse / new widgets

**Reuse:** `RunStatusChip`, `_MetricSparklineTile`, `_MetricGroupTile`,
`_groupMetrics`, `_ImageSeriesTile`, `HistogramSeriesTile`, `_MetricURITile`,
`_RunArtifactTile`, `_SummaryBody`, `_metaRow`, `_sectionLabel`,
`ArtifactsScreen`.

**New:**
- `RunView` enum + a lightweight `View ▾` switcher. Prefer extracting a generic
  `ViewSwitcher` from `session_header.dart`'s `View ▾` (currently
  session-flavored) so runs and sessions share it; otherwise a local
  PopupMenu/SegmentedButton + `IndexedStack`.
- `RunAlertsBanner` + alert row (level colour).
- `MetricStatTile` — the Overview headline tile (name + value + mini-spark);
  factor the mini-sparkline out of `_MetricSparklineTile`.
- `ConfigKeyValueList` — searchable, flattens nested JSON to dotted keys.

## Phasing (each phone-device-tested by the director)

- **P1 — client reads + host tweak.** `getRunConfig/SystemMetrics/Alerts`
  (+cached) and `deleteRun` in `runs_api.dart`; `_load` fetches the digests.
  Bump `Runner.MetricsMaxPoints` 100 → 150 (Go, locally testable). (CI
  `flutter analyze` + `go test`.)
- **P2 — view scaffold.** Header `View ▾` + `IndexedStack` (every view always
  present); `⋮ → Delete run`. Move today's sections into **Charts / Media /
  Outputs / Config** unchanged + empty states. No data loss, just relocation.
- **P3 — Overview.** Status strip + alerts banner + headline metric tiles +
  config highlights + summary + run→agent link + the live-poll timer.
- **P4 — fill the views.** System subsection in Charts; full alerts list; Config
  search; Media/Outputs polish.
- **Later (not scheduled).** Config "vs parent" diff; user-pinned headline
  metrics; tap-to-zoom charts; wandb/TB extras.

## Risks

- **Empty runs.** A run with no tracker attached must still render (Overview =
  status + summary; other views show quiet empty states). Gate every section on
  its data.
- **System x-axis.** `run_system_metrics` points are sample-ordinal, not steps —
  the shared sparkline tile must not imply a training step there.
- **Config size.** 50+ keys → the Config view needs search + flattening, not a
  raw dump.
- **No local Flutter.** Every phase gates on the director's device-test; client
  reads carry the CI `flutter analyze` check.

## Related

- [`reference/database-schema.md`](../reference/database-schema.md) — run digest
  tables (incl. `run_config` / `run_system_metrics` / `run_alerts`).
- [`reference/openapi.yaml`](../reference/openapi.yaml) — the run-extras
  endpoints.
- [`how-to/surface-run-metrics.md`](../how-to/surface-run-metrics.md) — getting a
  run's metrics to appear.
- [`spine/information-architecture.md`](../spine/information-architecture.md) §
  entity×surface — Run detail's home.
- Trackio driver + run-extras: see the run-extras hub stack (v1.0.796).

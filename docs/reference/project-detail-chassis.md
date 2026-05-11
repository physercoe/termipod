# Project detail chassis — A+B+C catalog + contract

> **Type:** reference
> **Status:** Current (2026-05-11)
> **Audience:** contributors
> **Last verified vs code:** v1.0.485

**TL;DR.** The project detail screen's Overview tab is an A+B+C
chassis: a kind-pluggable header, a phase-swapped hero widget, and
a phase-filtered tile strip. Both the hero registry and the tile
slug enum are **closed sets** — adding either is an APK change.
Per-project tile composition is **open data** through a JSON column
+ on-device editor. This doc is the catalog (which heroes / tiles
exist today) + the contract (what each must implement) + the
how-to (adding a new one). The design rationale is locked in
[ADR-024](../decisions/024-project-detail-chassis.md).

## 1. Layout

```
parent Scaffold body
├── AttentionBanner (conditional — open attention items)
├── ParentBreadcrumb (conditional — sub-projects)
├── PhaseRibbon  ← chassis-level, tap a past phase to see its summary
├── PillBar      ← Overview · Activity · Agents · Tasks · Files
└── PageView (the 5 tab bodies)
    └── Overview tab body
        ├── PortfolioHeader (A)   ← always-on, kind-pluggable
        │    └─ Goal + Show-details + StewardStrip + AttentionPill
        ├── PHASE HERO (B)         ← phase-swapped, template-selected
        ├── ShortcutTileStrip (C)  ← phase-filtered, per-project override
        ├── InsightsPanel          ← ADR-022 Tier-1 metrics
        └── "Details" ExpansionTile ← collapsed metadata + Archive (W2)
```

Files:

| Layer | File |
|---|---|
| Chassis | `lib/screens/projects/project_detail_screen.dart` (`_OverviewView`) |
| Header A | `lib/screens/projects/overview_widgets/portfolio_header.dart` (goal-kind) · `lib/screens/projects/overview_widgets/workspace_overview.dart` (standing-kind) |
| Hero B (dispatch) | `lib/screens/projects/overview_widgets/registry.dart` (`buildOverviewWidget`) |
| Hero B (widgets) | `lib/screens/projects/overview_widgets/research_phase_heroes.dart`, `task_milestone_list.dart`, `sweep_compare.dart`, `recent_artifacts.dart`, `children_status.dart` |
| Tile strip C | `lib/widgets/shortcut_tile_strip.dart` |
| Tile editor | `PhaseTileEditorSheet` in `lib/widgets/shortcut_tile_strip.dart` |

## 2. Hero registry — catalog

Closed enum: `kKnownOverviewWidgets` in `overview_widgets/registry.dart`.

| Slug | Class | What it renders | Data sources | Used by |
|---|---|---|---|---|
| `idea_conversation` | `IdeaConversationHero` | Memo-pad-style surface for exploratory idea capture; suggested next prompts | `documents` (memo kind), recent agent events | research idea phase |
| `deliverable_focus` | `DeliverableFocusHero` | The one ratifiable deliverable for this phase + components + AC pip + ratify CTA | `deliverables`, `deliverable_components`, `acceptance_criteria` | research lit-review + method phases |
| `experiment_dash` | `ExperimentDashHero` | Live aggregate over runs: sparkline of best metric, sweep summary, recent run chips | `runs`, `run_metrics`, `sweep_summary` | research experiment phase |
| `paper_acceptance` | `PaperAcceptanceHero` | Manuscript section list with status pips + AC progress + acceptance gate | `documents` (paper draft), `acceptance_criteria` (kind=gate) | research paper phase |
| `recent_artifacts` | `RecentArtifactsHero` | Top-N artifacts stream, kind-filtered chips | `artifacts` | Artifact-centric goal projects (template-declared) |
| `recent_firings_list` | `RecentFiringsList` | Top-N schedule firings + cadence info | `schedules`, `firings` | Workspaces (kind=standing — chassis default) |
| `task_milestone_list` | `TaskMilestoneListHero` | Mini-kanban (3 columns: Open / In progress / Done), tap row → task edit sheet | `tasks` | Generic goal projects (chassis default) |
| `children_status` | `ChildrenStatusHero` | List of sub-projects with status pill each | `projects` filtered by `parent_id` | Parent-of-children projects |
| `sweep_compare` | `SweepCompareHero` | Scatter / parallel-coords across runs in a sweep | `sweep_summary`, `runs` | Hyperparam-sweep projects |

The previously-defined `portfolio_header` slug is **NOT in this
list** — `PortfolioHeader` is the chassis-A header, not a hero.
Reusing the name as a hero slug confused the boundary; dropped in
ADR-024 D2.

> **Hero redesign moratorium until artifact-type-registry W1 lands.**
> Per ADR-024 follow-up sequencing, hero consolidation /
> redesign waits on the typed-artifact registry. New artifact
> kinds (`canvas-app`, `tabular`, `pdf`, `code-bundle`, …) will
> drive what existing heroes need to absorb. Redesigning heroes
> against today's free-form `artifacts.kind` would force a second
> rewrite once kinds lock.

**Hero archetype taxonomy** (for thinking about whether a new hero is
needed):

1. **Conversation / memo** — chat-or-memo surface (`idea_conversation`)
2. **Ratifiable deliverable** — bundle + ACs + ratify (`deliverable_focus`)
3. **Live aggregate dashboard** — streaming metrics rollup
   (`experiment_dash`)
4. **Milestone / acceptance gate** — fixed goal + progress
   (`paper_acceptance`)
5. **Recent items list** — top-N entity stream (`recent_artifacts`,
   `recent_firings_list`)
6. **Task kanban** — mini board (`task_milestone_list`)
7. **Parent hierarchy** — tree of children (`children_status`)
8. **Cross-run comparison** — sweep scatter / parallel coords
   (`sweep_compare`)

8 archetypes, 9 heroes (recent_X is split into two by entity).

## 3. Tile registry — catalog

Closed enum: `TileSlug` in `shortcut_tile_strip.dart:20`.

| Slug | Routes to | Backing entity | Default in research template? |
|---|---|---|---|
| `outputs` | `ArtifactsScreen` | `artifacts` table | yes — experiment, paper |
| `documents` | `DocumentsScreen` | `documents` table | yes — every phase |
| `experiments` | `RunsScreen` | `runs` table | yes — experiment |
| `plans` | `PlansScreen` | `plans` table | yes — method |
| `references` | `StructuredDeliverableViewer` (lit-review) or `DocumentsScreen` fallback | lit-review deliverable / tabular citations (post-W3) | yes — lit-review, method |
| `schedules` | `SchedulesScreen` | `schedules` table | no — user-addable |
| `assets` | `_AssetsHostScreen` (BlobsSection) | `blobs` table | no — user-addable |
| `discussion` | `ProjectChannelsListScreen` | `channels` table | no — user-addable (via editor since v1.0.485 dropped AppBar icon) |
| `risks` | `_StubScreen` | none (post-MVP per principal 2026-05-11) | no — post-MVP |

**Routes from tap — gotchas:**

- `references` does an async resolve: looks for a ratified
  lit-review deliverable; if present, opens
  `StructuredDeliverableViewer`. Otherwise falls back to
  `DocumentsScreen` with a snackbar. Post-W3 of
  artifact-type-registry, this becomes an `ArtifactsByKindScreen`
  filtered to `kind=tabular, schema=citation`.
- `outputs` vs `assets` — both list blob-like things but from
  different tables (`artifacts` vs `blobs`). Naming overlap is
  **post-MVP** per principal 2026-05-11 — no churn justified at
  MVP scale.

## 4. `OverviewContext` contract

Every hero widget takes one constructor argument:

```dart
class OverviewContext {
  final Map<String, dynamic> project;
  const OverviewContext({required this.project});
  String get projectId => (project['id'] ?? '').toString();
}
```

Read off the project map (no Project model class yet):

- `id`, `kind`, `phase`, `template_id`, `goal`, `status`,
  `phase_history`, `phase_tile_overrides`, `phase_tiles_template`,
  `overview_widget`, `created_at`, `docs_root`.

**Forbidden:** taking typed entity rows (deliverables, runs,
artifacts, ACs) through the constructor. Heroes **fetch their own
data** via Riverpod providers, keyed off `projectId`. Reasons:

1. Heroes are swapped on phase advance — passing pre-fetched data
   in would re-load constantly.
2. Heroes mount/unmount independently — they manage their own
   loading + error states.
3. The chassis stays uncoupled from any specific entity schema.

## 5. Resolution chains

### Hero resolution

```
projects.overview_widget                                    ← per-project (D10 mechanism deferred)
  → template_yaml.phase_specs[<phase>].overview_widget      ← per-phase template selection
    → template_yaml.default_overview_widget                 ← template-level default
      → chassis default:
          kind=standing → 'recent_firings_list'
          kind=goal     → 'task_milestone_list'
```

Function: `buildOverviewWidget(kind, ctx)` in
`overview_widgets/registry.dart`. Unknown wire values render an
`_UnknownOverviewHero` placeholder — visible-failure preferred
over silent-degrade-to-default so the user gets a "update the app"
hint.

Per-project hero override (D10) is deferred. When the wedge ships,
this chain inserts a 1st step reading
`projects.overview_widget_overrides_json[<phase>]`.

### Tile resolution

```
projects.phase_tile_overrides_json[<phase>]                 ← per-project, per-phase override
  → template_yaml.phase_specs[<phase>].tiles                ← per-phase template selection
    → hardcoded safety-net (`_researchPhaseTiles[<phase>]`) ← chassis Dart fallback for research template
      → chassis default `[outputs, documents]`              ← absolute fallback
```

Function: `resolveTilesForPhase(...)` in `shortcut_tile_strip.dart`.
Unknown slugs are silently dropped at `_slugFromString`.

User edits go through `PhaseTileEditorSheet` (drag-to-reorder +
checkbox add/remove + Reset). Steward edits go through
`projects.update(phase_tile_overrides=...)`.

## 6. How to add a new hero

Checklist:

1. **Pick an archetype.** Does it match an existing one in §2?
   Reuse if yes — composition-is-data is cheaper than a new slug.
2. **Add the slug** to `kKnownOverviewWidgets` (Dart Set) AND
   `validOverviewWidgets` (Go enum in `hub/internal/server/init.go`).
   These two sets must stay in sync.
3. **Add the widget class** in
   `lib/screens/projects/overview_widgets/`. Constructor takes
   `OverviewContext`. Fetch own data via providers.
4. **Add a case in `buildOverviewWidget`** (`registry.dart`).
5. **Document the hero** in this reference §2 — slug, class, what
   it renders, data sources, who uses it.
6. **Update [template-yaml-schema.md](template-yaml-schema.md) §9**
   so template authors know the slug exists.
7. **Tests** — at minimum: the registry dispatches to the new
   class when the slug is selected.
8. If the hero needs to render a new artifact kind, the
   artifact-kind landing in
   [artifact-type-registry.md](../plans/artifact-type-registry.md)
   is a prerequisite — heroes don't author new artifact kinds.

## 7. How to add a new tile

Checklist:

1. **Make sure the list-screen exists.** A tile is a route to a
   list-screen. If the screen doesn't exist, build it first.
   Stub-tiles (like the current `risks`) are a code smell.
2. **Add the slug** to `TileSlug` enum AND `_slugFromString`
   parser AND `tileSpecFor` (label + icon + subtitle).
3. **Add the route** in the `_open` switch in
   `shortcut_tile_strip.dart` — `case TileSlug.X: page = XScreen(...);`.
4. **Document the tile** in this reference §3.
5. **Update [template-yaml-schema.md](template-yaml-schema.md) §10**
   so template authors can list the new slug.
6. **Optional**: if the tile should appear by default in some
   research-template phase, add to `_researchPhaseTiles` map in
   `shortcut_tile_strip.dart` (safety-net for rollouts where the
   hub doesn't yet ship the template tiles field). Templates that
   list it in YAML supersede the safety-net.
7. **Tests** — `tileSpecFor` returns non-empty label + subtitle;
   `_slugFromString` parses the new slug; `resolveTilesForPhase`
   respects override / template / safety-net for the new slug.

## 8. What's per-project vs per-template

| Axis | Per-project override? | Per-template select? | Chassis default? |
|---|---|---|---|
| Phase | ✓ via `projects.phase` + `phase_history` | n/a — phases are template-defined | n/a |
| Status | ✓ via `projects.status` | n/a | `active` |
| Tile composition | ✓ via `phase_tile_overrides_json` (D6) | ✓ via `phase_specs.tiles` | `[outputs, documents]` |
| Hero widget | ⚠ D10 — principle locked, mechanism deferred | ✓ via `phase_specs.overview_widget` | `task_milestone_list` (goal) / `recent_firings_list` (standing) |
| Header A type | n/a — by `kind` only | n/a | `PortfolioHeader` (goal) / `WorkspaceHeader` (standing) |
| Metadata rows visibility | n/a — always collapsed | n/a | collapsed |
| Phase ribbon | always-on | n/a | always-on |
| Tab pills | always-5 fixed | n/a | Overview · Activity · Agents · Tasks · Files |

## 9. Follow-up wedges — ordered

Per ADR-024 sequencing locked 2026-05-11. Read together with the
ADR's full text.

| # | Wave | Wedge | Depends on |
|---|---|---|---|
| 1 | first | D10 mechanism: `projects.overview_widget_overrides_json` + hero picker in `PhaseTileEditorSheet` + steward MCP path | ADR-024 chassis lock (this doc) |
| 2 | first | `DeliverablesScreen` + `AcceptanceCriteriaScreen` → then add `deliverables` + `acceptance_criteria` slugs to `TileSlug` enum | screens land before slugs (don't ship stubs) |
| 3 | second | artifact-type-registry W1–W6 — typed kind chassis + viewers (tabular / pdf / image / code-bundle / canvas-app / …) | wave 1 done so heroes are stable while kinds land |
| 4 | third | Hero consolidation / redesign — driven by wave 2 (e.g. `idea_conversation` absorbing canvas-app; `experiment_dash` rendering histograms) | wave 2 typed kinds locked |
| 5 | post-MVP | `outputs` vs `assets` naming resolution | deferred indefinitely |
| 6 | post-MVP | Workspace cross-project Insights | deferred — standing-kind today filtered out |

**Don't redesign heroes before kinds.** Doing so means each hero
update happens against free-form `artifacts.kind` strings, pre-empts
the closed-set benefit, and forces a second rewrite once kinds lock.

## 10. Related docs

- **Design rationale:** [ADR-024](../decisions/024-project-detail-chassis.md)
- **Template-side schema:** [template-yaml-schema.md](template-yaml-schema.md) §9, §10
- **Concrete template:** [research-template-spec.md](research-template-spec.md)
- **Tile editor mechanism:** [lifecycle-walkthrough-followups.md](../plans/lifecycle-walkthrough-followups.md) W5/W6
- **Layout chrome cleanup (v1.0.485):** [project-overview-attention-redesign.md](../plans/project-overview-attention-redesign.md)
- **Overlay surface (sibling):** [ADR-023](../decisions/023-agent-driven-mobile-ui.md)
- **Insights surface (sibling, below chassis):** [ADR-022](../decisions/022-observability-surfaces.md)
- **Artifact kinds the heroes render:** [artifact-type-registry.md](../plans/artifact-type-registry.md)

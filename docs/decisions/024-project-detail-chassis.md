# 024. Project detail chassis — A+B+C layered Overview, closed hero/tile registries, per-project overrides

> **Type:** decision
> **Status:** Accepted (2026-05-11; chassis live since v1.0.358; tile editor v1.0.484; D10 hero override mechanism shipped wave 1; D4 ribbon → compact PhaseBadge v1.0.500)
> **Audience:** contributors
> **Last verified vs code:** v1.0.486

**TL;DR.** The project detail page's Overview tab has been an A+B+C
chassis since lifecycle W4 — fixed `PortfolioHeader`, a phase-swapped
hero, and a phase-filtered tile strip. Code comments point at "IA
§6.2" for the design, but IA §6.2 doesn't actually describe it; the
chassis is load-bearing in five callers and one editor sheet but
implicit in docs. This ADR locks the chassis as ten decisions
(D1–D10), grounds each in shipped code, and is the cross-reference
target for the new chassis catalog at
[`reference/project-detail-chassis.md`](../reference/project-detail-chassis.md).
The two closed registries (heroes + tiles) cost an APK rebuild per
addition, so the lock matters.

## Context

### The chassis as it stands

Five surfaces stack on a project detail screen:

```
┌─ AppBar: ← Project Name · ⋮ ────────────────────────┐
│ PhaseBadge  [Method · 3/5 ›]                       │ chassis-level (v1.0.500)
│ PillBar  Overview · Activity · Agents · Tasks · Files │ tabs
├── tab body: Overview ──────────────────────────────┤
│  [AttentionBanner if any]                          │
│  PortfolioHeader (A)         ← always-on, kind-pluggable
│  ── divider ──                                     │
│  PHASE HERO (B)               ← swapped by template_yaml.overview_widget
│  ── divider ──                                     │
│  ShortcutTileStrip (C)        ← phase-filtered, per-project overrideable
│  ── divider ──                                     │
│  InsightsPanel               ← ADR-022 D3 Tier-1 metrics
│  ▾ Details                   ← collapsed metadata + Archive (v1.0.485 W2)
└────────────────────────────────────────────────────┘
```

PhaseBadge and PillBar sit at the Scaffold body level — visible
across all five tabs. PortfolioHeader / Hero / TileStrip / metadata
expander live only on the Overview tab. The AppBar's title row holds
`name + ProjectKindChip`; Edit, Template-YAML, and New-sub-project
all live in the `⋮` overflow (v1.0.500 consolidation — was three
separate IconButtons before).

### What was missing in docs before this ADR

- IA spine §6.2 lists project sub-surfaces (Runs / Reviews / Docs /
  …) but doesn't describe the A+B+C chassis.
- `reference/template-yaml-schema.md` §9 mentions the
  overview-widget registry at *schema* level but doesn't catalog
  which slugs exist, what each renders, or the contract.
- `plans/lifecycle-walkthrough-followups.md` W5/W6 documents the
  per-project tile-override mechanism but at plan level, not as a
  locked chassis decision.
- `plans/project-overview-attention-redesign.md` reshapes the
  Overview body chrome (drop Discussion icon, collapse metadata)
  but explicitly punts the chassis lock to this ADR.
- ADR-023 covers the overlay; its `mobile.navigate` URI router
  pushes users *into* the chassis but doesn't define it.

The chassis is referenced by code in five files (`project_detail_screen.dart`,
`shortcut_tile_strip.dart`, `overview_widgets/registry.dart`,
`overview_widgets/portfolio_header.dart`, `overview_widgets/workspace_overview.dart`)
and one editor (`PhaseTileEditorSheet`). It needs to be locked
because every entry in the hero registry or tile enum is an APK
rebuild — extension is closed-set, composition is open data.

## Decision

### D1. Three-layer Overview body: header + hero + tile strip.

The Overview tab body is **A (header) + B (hero) + C (tile strip)**,
in that order. Below them is the chassis-shared `InsightsPanel`
(ADR-022 D3) and a collapsed metadata expander (W2 of
project-overview-attention-redesign).

| Layer | Role | Visual weight | Examples |
|---|---|---|---|
| A — Header | Orientation: goal + status + phase progress | High; always above-fold | `PortfolioHeader` (goal-kind), `WorkspaceHeader` (standing-kind) |
| B — Hero | Focus: the *one thing* the user came to do for this phase | Highest; takes the most vertical real estate | `deliverable_focus`, `experiment_dash`, `paper_acceptance`, … |
| C — TileStrip | Exploration: shortcut routes to peripheral list-screens | Compact; below hero | `Outputs`, `Documents`, `Plans`, `References`, … |

The principle: **hero = content, tile = navigation.** Hero
renders the focus entity inline; tile is a route button.
Industry parallel: GitHub repo overview = README (hero) +
sidebar links to Issues/PRs (tiles). Linear project = status
header + main board + sub-page links.

### D2. Hero registry is a closed mobile-side enum.

Heroes are widgets that render rich phase-specific content. Adding
one is **an APK change** (Dart enum + widget class + registry
dispatch + tests). The hub doesn't define the set; the template
YAML can only *select* from existing slugs.

**Locked 9-hero MVP set:**

| Slug | Archetype | Used for |
|---|---|---|
| `idea_conversation` | Memo / chat surface | Research idea phase |
| `deliverable_focus` | Ratifiable deliverable with components + ACs + ratify CTA | Research lit-review + method phases |
| `experiment_dash` | Live aggregate dashboard (sparklines / sweep summary) | Research experiment phase |
| `paper_acceptance` | Milestone manuscript + AC progress + submit gate | Research paper phase |
| `recent_artifacts` | Top-N artifacts stream | Artifact-centric goal projects |
| `recent_firings_list` | Top-N schedule firings | Workspaces (standing kind) |
| `task_milestone_list` | Mini-kanban over project tasks | Generic goal-projects (chassis default) |
| `children_status` | Tree of sub-projects with status | Parent-of-children projects |
| `sweep_compare` | Cross-run scatter / parallel coords | Hyperparam sweep projects |

The previously-listed `portfolio_header` slug is **dropped from
the hero registry** — `PortfolioHeader` is chassis-A, not a hero;
reusing the name as a "no-hero fallback" was confusing. A null /
empty hero is the correct fallback when no slug applies.

Hero consolidation candidates noted but not collapsed: `recent_artifacts`
+ `recent_firings_list` (same archetype, different filter — keep
separate while distinct callers exist).

### D3. Tile registry is a closed mobile-side enum.

Same closure rule as heroes. Adding a tile = APK change.

**Locked 11-tile MVP set** (9 at lock + 2 added wave 1):

| Slug | Backing entity | Surfaced by default in research template? |
|---|---|---|
| `outputs` | `artifacts` table | yes (experiment, paper) |
| `documents` | `documents` table | yes (every phase) |
| `experiments` | `runs` table | yes (experiment) |
| `plans` | `plans` table | yes (method) |
| `references` | lit-review deliverable / tabular citations | yes (lit-review, method); see artifact-type-registry W3 reclassification |
| `assets` | `blobs` table | no — user-addable via editor |
| `schedules` | `schedules` table | no — user-addable |
| `discussion` | `channels` table | no — user-addable |
| `deliverables` | `deliverables` table | no — user-addable (wave 1, ADR-024 follow-up) |
| `acceptance_criteria` | `acceptance_criteria` table | no — user-addable (wave 1, ADR-024 follow-up) |
| `risks` | none (stub) | no — post-MVP per principal 2026-05-11 |

All slugs stay in the closed enum even when unused-by-default.
Removing one would break users who added it via `PhaseTileEditorSheet`.

### D4. Phase indicator is chassis-level, not part of Overview body.

A compact `PhaseBadge` sits above the `PillBar` in
`project_detail_screen.dart` (`lib/widgets/phase_badge.dart`).
Visible across all 5 tabs. Renders as `[current_phase · n/total ›]`
— a single pill instead of the original full-row ribbon. Tap
expands a bottom sheet that hosts the underlying `PhaseRibbon`, so
per-phase navigation (past-phase summary via `_openPhaseSummary`)
is still one extra tap away.

History: original chassis (v1.0.358) used `PhaseRibbon` inline at
~56px height. v1.0.500 swapped it for the compact badge after
testers reported the ribbon ate too much vertical chrome for a
metadata cue. The ribbon widget survives, reused inside the sheet
so the navigation flow is identical — only the resting-state size
changed. Pattern reference: Linear / Jira / Notion status badge.

Reversibility: easy (swap `PhaseBadge` back to inline `PhaseRibbon`
in `_ProjectDetailScreenState.build`). The bottom sheet pattern is
the more reversible variant of "compact metadata, expand-on-tap"
which gets ubiquitous use across the app's chrome.

### D5. Hero contract: takes `OverviewContext`, fetches own data.

Every hero widget constructor takes one argument:
`OverviewContext { project: Map<String, dynamic> }`. The widget
reads `projectId`, `phase`, `template_id`, `goal` etc. off the
project map and **fetches its own data via Riverpod providers**.

Heroes are **stateless across phase advances** — the chassis
swaps to a new widget on phase change; the new hero loads its own
data. No "remember scroll position across phase advance" — that's
explicitly out of scope.

Forbidden: passing typed entity rows (deliverables, runs, ACs)
into hero constructors. The hero fetches what it needs.

### D6. Per-project tile override via `projects.phase_tile_overrides_json`.

Migration 0037 (v1.0.484). JSON map of `{phase: [slug, ...]}`. PATCHable
through the projects API (steward via `projects.update`, user via
`PhaseTileEditorSheet`). When set for a phase, the override
**replaces** the default — composition is the data, vocabulary is
the code.

Empty list `[]` means "no tiles for this phase" — valid and
distinct from "no override, fall through."

### D7. Tile resolution chain: override → template → safety-net → default.

Resolved in `resolveTilesForPhase()` in `shortcut_tile_strip.dart`:

1. **Per-project override** — `projects.phase_tile_overrides_json[<phase>]`
2. **Template YAML** — `phase_specs[<phase>].tiles`
3. **Hardcoded Dart safety-net** — `_researchPhaseTiles[<phase>]`
   for the well-known research template phases (kept during
   rollout so older hub installs still render right)
4. **Chassis default** — `[outputs, documents]`

Unknown slugs are silently dropped at parse time
(`_slugFromString` returns null). The closed enum is the
vocabulary guarantee.

### D8. Header A is kind-pluggable, not template-pluggable.

`PortfolioHeader` for `kind=goal` projects, `WorkspaceHeader` for
`kind=standing`. Selection is by project kind, not template — every
goal project gets PortfolioHeader regardless of template, every
workspace gets WorkspaceHeader.

This is **distinct from hero pluggability** (D2): the header is
domain-agnostic across templates; the hero is domain-specific.
Two separate axes.

### D9. Below-divider metadata + Archive collapse to "Details" expander.

Shipped in W2 of project-overview-attention-redesign (v1.0.485).

The PortfolioHeader (which has Goal + Show-details for status/budget/tasks)
and the Hero are the focus pair above the fold. Outer metadata
rows (Name/Kind/Status/Goal/Steward template/On-create template/ID/Docs root/Created)
and the destructive Archive button live in a single `ExpansionTile`
titled "Details," default collapsed.

The "above-fold focus chain" is: AttentionBanner → PortfolioHeader →
Hero → TileStrip → InsightsPanel. "Details" is exploration-tier;
out of the F-pattern.

### D10. Heroes are overrideable per-phase per-project (shipped wave 1).

Mirrors D6 for symmetry. Tiles being overrideable but heroes not
is an inconsistency that cuts against ADR-023's steward-drives-UI
principle (the steward should be able to swap a hero just as it
swaps tiles).

**Mechanism shipped wave 1** (chassis-followup, migration 0038):

- Column `projects.overview_widget_overrides_json` mirrors
  `phase_tile_overrides_json`. Map `{phase: slug}`.
- Resolution chain: per-project override → template YAML
  `phase_specs[<phase>].overview_widget` → template
  `default_overview_widget` → chassis default
  (`task_milestone_list` for goal, `recent_firings_list` for
  standing). Implemented in
  `Server.resolveOverviewWidget(templateID, phase, overrides)`.
- Hero picker (ChoiceChip Wrap) added to `PhaseTileEditorSheet`
  alongside the tile composition editor; same Save/Reset bundle.
- Steward MCP path: `projects.update(overview_widget_overrides=...)`
  via the existing project PATCH handler.

Override risk: a hero may show empty/misleading state if its
data assumption doesn't match the phase (e.g., `experiment_dash`
on a project with no runs). Acceptable — the hero already
handles empty state when phases haven't produced data yet.

## Consequences

### Positive

- **Three orthogonal axes** (kind / phase / per-project override)
  give templates expressive room while keeping the chassis closed.
- **Closed registries match the IA spine's "Tier-1 surface, finite
  vocabulary" principle** — every new hero or tile is a deliberate
  APK ship, not a YAML one-liner that can ride along with template
  edits.
- **Composition-is-data** allows steward + user to tune which tiles
  surface per phase without code changes (per D6, D7).
- **Hero contract (D5) keeps heroes uncoupled** from data shape —
  heroes can be developed in parallel without sharing data schemas
  across the constructor boundary.
- **`PhaseBadge` always-visible (D4)** keeps "where am I?" answered
  on every tab in a single line of chrome; past-phase summary is
  two taps (expand sheet → tap phase chip) instead of one. Net win
  on small phones where the ribbon ate too much above-fold space.

### Negative / costs

- **APK rebuild per registry change** — adding `deliverables` as a
  tile or a new domain-template hero is a code change. Acceptable;
  this is the price of closed-set safety.
- **`portfolio_header` removal as a hero slug** breaks any template
  YAML that named it explicitly. Mitigation: the chassis falls
  through to the chassis default; no template currently uses the
  slug as primary anyway.
- **Hero picker exposes the closed `kKnownOverviewWidgets` set** to
  the director. Some slugs may render misleadingly when picked
  outside their canonical phase (e.g. `experiment_dash` on idea).
  Reset to template default restores the canonical hero.

### Reversibility

| Decision | Reversible? | Cost to reverse |
|---|---|---|
| D1 three-layer body | Yes | Re-collapse hero into header; mostly a UI shuffle |
| D2 closed hero enum | Yes; tighter than open registry | Loosen to runtime registry — risks unknown slugs |
| D3 closed tile enum | Yes | Same — closure is the safer default |
| D4 chassis-level PhaseBadge | Yes | Swap back to inline `PhaseRibbon` or relocate to Overview only; loses compact-orientation cue on other tabs |
| D5 hero contract | Yes if no hero takes data via constructor yet | Allowed pattern would proliferate; reverse early |
| D6 per-project tile override | Hard once users have edits saved | Migration drops the column; users lose their compositions |
| D7 resolution chain | Yes; chain is internal to one function | Reorder by editing `resolveTilesForPhase` |
| D8 kind-pluggable header | Yes | Collapse to a single shared header — loses standing-kind affordances |
| D9 metadata collapse | Yes | Re-inline metadata; visual regression to v1.0.484 state |
| D10 hero override principle | Yes if mechanism never ships | Hard once `overview_widget_overrides_json` exists |

## References

### Internal

- [`reference/project-detail-chassis.md`](../reference/project-detail-chassis.md)
  — catalog + contract + how-to-add-a-hero / how-to-add-a-tile
- [`plans/project-overview-attention-redesign.md`](../plans/project-overview-attention-redesign.md)
  — W1+W2+W3 chrome cleanup shipped v1.0.485 (the proximate cause
  of this ADR)
- [`plans/lifecycle-walkthrough-followups.md`](../plans/lifecycle-walkthrough-followups.md)
  W5/W6 — the per-project tile-override mechanism that v1.0.484
  shipped
- [`reference/template-yaml-schema.md`](../reference/template-yaml-schema.md)
  §9, §10 — template-side schema for `overview_widget` and `tiles`
- [`spine/information-architecture.md`](../spine/information-architecture.md)
  §6.2 — updated by this ADR to point at the chassis reference
- [`decisions/023-agent-driven-mobile-ui.md`](023-agent-driven-mobile-ui.md)
  — overlay chassis; D7 (URI as API) drives why heroes need
  override (D10)
- [`decisions/022-observability-surfaces.md`](022-observability-surfaces.md)
  — `InsightsPanel` (the sibling Tier-1 metrics surface below the
  TileStrip) lives in its own scope-parameterized world

### Code

- `lib/screens/projects/project_detail_screen.dart` — chassis
  assembly (`_OverviewView` builds A+B+C+InsightsPanel+expander)
- `lib/widgets/shortcut_tile_strip.dart` — `TileSlug` enum, slug→spec
  mapping, `resolveTilesForPhase`, `PhaseTileEditorSheet`
- `lib/screens/projects/overview_widgets/registry.dart` —
  `kKnownOverviewWidgets`, `buildOverviewWidget`,
  `normalizeOverviewWidget`
- `lib/screens/projects/overview_widgets/portfolio_header.dart`
- `lib/screens/projects/overview_widgets/workspace_overview.dart`
- `hub/migrations/0037_project_tile_overrides.up.sql`
- `hub/internal/server/template_hydration.go` — `phaseTemplateTiles`,
  `phaseOverviewWidget`

### Follow-up wedges (ordered)

Principal-directed sequencing 2026-05-11. The chassis is locked
here; the wedges below sit on top of it.

1. **D10 hero overrideability mechanism** — ✅ shipped wave 1.
   Migration `0038_project_overview_widget_overrides`, parallel
   resolution chain in `resolveOverviewWidget(_, _, overrides)`,
   hero picker (ChoiceChip Wrap) added to `PhaseTileEditorSheet`,
   steward MCP path via existing `projects.update` PATCH.
2. **`deliverables` tile + `acceptance_criteria` tile** — ✅
   shipped wave 1. `DeliverablesScreen` +
   `AcceptanceCriteriaScreen` plus slug additions to the closed
   `TileSlug` enum (`deliverables`, `acceptanceCriteria`). 11-slug
   set now locked.
3. **artifact-type-registry W1–W7** — ✅ shipped second wave
   (v1.0.489–497, 2026-05-11). 11-kind closed set + per-kind
   viewers (pdf, tabular, image, code-bundle, audio, video)
   + cross-engine multimodal input (W7 — pdf cross-engine,
   audio/video Gemini-only). The eleventh kind (`canvas-app`)
   gained its sandboxed WebView viewer via the **canvas-viewer
   plan** (v1.0.498, AFM-V1 shared multi-file body schema).
   `docs/plans/artifact-type-registry.md` and
   `docs/plans/canvas-viewer.md` both `Status: Done`.
4. **Hero consolidation / redesign** — third wave, driven by
   wave 2. New artifact kinds will push hero design: e.g.
   `idea_conversation` may absorb `canvas-app` artifacts for
   richer ideation; `experiment_dash` may need the histogram
   schema from `metric-chart`. Whether `recent_artifacts` and
   `recent_firings_list` collapse, whether a new
   `artifact_workbench` hero is warranted — both questions only
   answerable now that wave 2's registry has landed. **Don't
   redesign heroes before kinds.** (Wave 2 now landed, so this
   wedge is unblocked.)
5. **`outputs` vs `assets` rename** — post-MVP per principal
   2026-05-11. Naming overlap noted but no churn justified at
   MVP scale; revisit if a third blob-tile candidate appears.
6. **Workspace cross-project Insights** — post-MVP. Today
   `kind=standing` is filtered out of `by_project[]` (W3 of
   project-overview-attention-redesign); workspaces need their
   own rollup surface eventually.

The ordering matters: trying to redesign heroes before the kind
registry exists means each hero redesign happens against
free-form `artifacts.kind` strings — pre-empts the closed-set
benefit and forces breaking changes once kinds are locked.

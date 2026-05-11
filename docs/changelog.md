# Changelog

> **Type:** reference
> **Status:** Current (2026-05-11)
> **Audience:** contributors, operators
> **Last verified vs code:** v1.0.492

**TL;DR.** Append-only record of what shipped in each tagged release.
One section per version, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/) — Added / Changed /
Fixed / Deprecated / Removed / Security. Entries link to the commit
or PR for forensic detail.

This complements:
- `roadmap.md` — current focus and Now/Next/Later view
- `decisions/` — append-only ADRs for architectural choices
- Git tag annotations — short-form release notes per tag

History before v1.0.280 lives in git log only. The active-development
arc starts at v1.0.280 (steward sessions soft-delete + agent-identity
binding). Seed entries prior to that are in
[`#earlier-history`](#earlier-history) below.

---

## v1.0.492-alpha — 2026-05-11

Wave 2 W4 — image attach on the steward overlay composer.
Multimodal landing for the floating chat surface. ADR-021's existing
hub validator + per-driver wire-mapping already handle the bytes;
this wedge wires the affordance into the smaller composer.

### Added

- **`lib/widgets/image_attach/composer_image_attach.dart`** —
  shared helpers extracted from `agent_compose.dart`:
  - `pickAndCompressImage()` returning a `ComposerImageAttachment`
    (mime + base64-encoded data)
  - `ComposerImageThumbnailStrip` widget (horizontal × strip)
  - `resolveCanAttachImages()` capability gate (visible-for-tests)
  - `kMaxImagesPerTurn` / `kMaxImageBytes` / etc. constants
- **Paperclip + thumbnails on the steward overlay chat**
  (`steward_overlay_chat.dart`):
  - `_ChatInputState` now owns `_pendingImages` + `_attaching` +
    `_attachError` alongside the existing IME-stable controller.
  - `_ChatInputSlot` becomes a `ConsumerWidget` and watches
    `agentId` via `.select` so the slot only rebuilds the once
    when the overlay binds an agent (SSE traffic doesn't reach it).
  - Parent state's `_resolveCapabilityIfNeeded(agentId)` joins the
    family registry to set `_canAttachImages`; the flag flows down
    to `_ChatInput`.
- **`sendUserMessage(text, {images})`** on
  `StewardOverlayController` — new entry point that lifts the text
  body to nullable and forwards `images` through to
  `HubClient.postAgentInput`. `sendUserText(text)` retained as a
  back-compat shim so snippet chips don't break.

### Changed

- `agent_compose.dart` refactored to import the shared helpers
  instead of carrying its own copies of the pick+compress, mime
  map, and thumbnail strip. Net diff: ~120 LOC moved, behaviour
  unchanged.

### Notes

- No hub or engine-driver work was needed — ADR-021 W4.1/W4.2-W4.5
  already shipped the validator + per-driver mapping. Plan W4 had
  some scope overlap with that prior wedge; the artifact-creation
  pathway it proposed was redundant given the existing inline
  base64 pipeline works.
- Capability gate still respects `prompt_image[mode]`: gemini M2
  (exec-per-turn) keeps the affordance hidden because the W4.5
  strip-and-warn fallback isn't an invitation to send.

---

## v1.0.491-alpha — 2026-05-11

Wave 2 W3 — Tabular viewer + References tile reclassification.
Second user-visible viewer on the wave 2 closed-set chassis,
landed alongside the seed change that puts a structured References
component on every ratified lit-review deliverable.

### Added

- **`lib/widgets/artifact_viewers/tabular_viewer.dart`** —
  `ArtifactTabularViewer` (Riverpod consumer) +
  `ArtifactTabularViewerScreen`. Resolves `blob:sha256/<sha>` URIs
  through `HubClient.downloadBlobCached`, parses JSON (top-level
  list-of-objects OR `{rows: [...]}`), renders a `DataTable` with
  empty / error / unsupported-scheme states. Schema discovery via
  MIME's `schema=` param (Q6 option (a)) — known schemas (today:
  `citation`) pick a canonical column order, unknown schemas derive
  from the union of keys in the first 8 rows.
- **`lib/screens/artifacts/artifacts_by_kind_screen.dart`** —
  project-scoped artifact list filtered by closed-set kind +
  optional schema. Used by the References tile; reusable for other
  kind-targeted views as wave 2 progresses.
- **Citation seed** — `seed_demo_lifecycle.go` gains
  `demoCitations()` (8 deterministic rows) +
  `seedCitationArtifact()` that writes the bytes through
  `insertDemoBlob` (when `dataRoot` is set) and emits a real
  `blob:sha256/<sha>` URI with MIME
  `application/json; schema=citation`. Every ratified lit-review
  deliverable gains a 2nd component (`{kind: artifact, refID:
  citationArt.id, ord: 1}`).
- **`test/widgets/tabular_viewer_test.dart`** — unsupported-uri
  error path + screen-scaffold smoke test.

### Changed

- **`SeedLifecycleDemo(ctx, db, dataRoot)`** — signature gains
  `dataRoot string`. Empty string preserves the old mock-URI
  behaviour for tests that don't care about renderable citations;
  the `seed-demo --shape lifecycle` CLI passes the real data root
  so citations resolve through the hub blob endpoint.
- **`_openReferences` (shortcut_tile_strip.dart)** — now tries
  `listArtifactsCached(kind=tabular)` first and routes to
  `ArtifactsByKindScreen(kind=tabular, schema=citation,
  title=References)` when a citation-shaped row exists. Falls back
  to the existing StructuredDeliverableViewer / DocumentsScreen
  ladder when nothing matches.
- **Artifact detail launcher** — `_ArtifactViewerLauncher` in
  `artifacts_screen.dart` extracted into a `switch (spec.kind)`;
  pdf and tabular kinds get distinct launcher buttons; remaining
  MVP kinds wait for W4–W6.

### Notes

- Schema discovery deliberately stops at MIME params today (Q6
  option (a)). Escalate to option (c) (a `artifact_schema_id`
  column) only if domain-specific viewers proliferate.
- Inline-edit on table cells (Q7) remains out of scope — the
  viewer is read-only.

---

## v1.0.490-alpha — 2026-05-11

Wave 2 W2 — PDF viewer for `pdf`-kind artifacts. First user-visible
viewer on the wave 2 closed-set chassis.

### Added

- **`pdfrx ^2.3.3`** dep — PDFium-backed Flutter PDF lib (Q5 in plan).
  Built-in pinch zoom, text search/selection, outline, password
  support. ~2 MB APK cost.
- **`lib/widgets/artifact_viewers/pdf_viewer.dart`** — `ArtifactPdfViewer`
  (Riverpod consumer; resolves `blob:sha256/<sha>` URIs via
  `HubClient.downloadBlobCached`, then renders via `PdfViewer.data`)
  + `ArtifactPdfViewerScreen` (fullscreen route — keeps the pinch-zoom
  gesture from fighting the artifact detail sheet's vertical drag).
- **`_ArtifactViewerLauncher`** in `artifacts_screen.dart` — dispatches
  on `artifactKindSpecFor(row['kind']).kind`; for `ArtifactKind.pdf`
  surfaces an "Open PDF" outlined button below the title. Other kinds
  render no launcher today (W3+ extends the dispatcher).

### Notes

- Non-`blob:sha256/` URI schemes (seed mock data, external HTTPS,
  raw filesystem paths) show an explicit "unsupported uri scheme"
  card rather than crashing. The hub blob endpoint is the only
  load-bearing path today.
- Down-stack: `HubClient.downloadBlobCached` already handles auth +
  on-disk content-addressed caching; the viewer is a thin wrapper
  over existing infrastructure.

---

## v1.0.489-alpha — 2026-05-11

Wave 2 W1 — artifact-type-registry closed-set chassis. Lifts
`artifacts.kind` from a free-form string into the 11-entry MVP
vocabulary defined in
`docs/plans/artifact-type-registry.md`. No new viewers yet; this
wedge is the chassis the W2–W6 viewers will dispatch on.

### Added

- **`hub/internal/server/artifact_kinds.go`** — closed-set registry.
  `validArtifactKinds` (11 entries: `prose-document`, `code-bundle`,
  `tabular`, `image`, `audio`, `video`, `pdf`, `diagram`,
  `canvas-app`, `external-blob`, `metric-chart`) is the wire
  vocabulary; `backfillLegacyArtifactKind` maps the pre-W1
  free-form values (`checkpoint`/`dataset`/`other`/`eval_curve`/
  `log`/`report`/`figure`/`sample`) onto the new set so MCP clients
  still in flight survive a tester cycle.
- **`hub/migrations/0039_artifacts_kind_check.{up,down}.sql`** —
  documentation + backfill `UPDATE` pass. No CHECK constraint
  (Q3 resolved in plan against DB-level enforcement so new kinds
  don't require a forward migration each time); the down migration
  is a documented no-op because the remap is lossy.
- **`lib/models/artifact_kinds.dart`** — Dart enum mirroring the hub
  registry, plus `ArtifactKindSpec` (label / icon / mime hint /
  colour role) and `artifactKindSpecFor(slug)` with legacy-alias
  remapping and `externalBlob` fallback so the UI always has
  something to render.
- **`test/models/artifact_kinds_test.dart`** — round-trip + alias
  + fallback test coverage.
- **`hub/internal/server/handlers_artifacts_test.go`** —
  `TestCreateArtifact_ClosedKindSet` covers every MVP kind (201),
  a bogus kind (400), and every legacy alias round-tripping to the
  remapped MVP kind.

### Changed

- `handleCreateArtifact` now rejects unknown kinds with 400 unless
  they live in `validArtifactKinds` or the legacy alias map; legacy
  values are silently remapped + the artifact stores the new slug.
- `seed_demo_lifecycle.go` emits `external-blob` (was `checkpoint`)
  and `metric-chart` (was `eval_curve`) so demo data ships under
  the closed set.
- `ArtifactKindChip` (artifacts_screen.dart) now dispatches through
  `artifactKindSpecFor`, so legacy cached rows render with the
  remapped label/colour and new kinds (pdf, tabular, image, code…)
  pick up sensible defaults instead of the muted `?` fallback.
- Filter pills on the Artifacts screen swap to the closed MVP set
  (`prose-document`, `tabular`, `image`, `pdf`, `metric-chart`,
  `code-bundle`, `external-blob`) — what new agents will emit.

### Deprecated

- The free-form `checkpoint`/`eval_curve`/`log`/`dataset`/`report`/
  `figure`/`sample`/`other` kind strings are accepted only as
  legacy aliases. Migrate emitters to the MVP set; the alias bridge
  will be removed in a later wedge once the next tester cycle
  confirms no live emitter relies on it.

---

## v1.0.488-alpha — 2026-05-11

Projects list filter / sort AppBar affordance. Common-case default
(active + recent) plus quick "needs me" toggle and name / created
alternates.

### Added

- **AppBar filter icon** (`Icons.filter_list`) on the Projects screen,
  between the team-overview Insights icon and Refresh. Tap opens a
  modal bottom sheet with three sections:
  - **Status**: SegmentedButton `Active` (default — hides archived) /
    `All` / `Archived`
  - **Needs me**: switch — show only projects with open attention
    or open AC
  - **Sort**: SegmentedButton `Recent` (default — uses insights
    `last_activity` with `created_at` fallback) / `Name A-Z` / `Created`
- **Active-filter indicator**: small primary-color dot on the icon
  when the filter is non-default, so a power-user setup is
  immediately visible at a glance.
- **Persisted preference**: SharedPreferences key
  `projects_list_filter_v1` survives app restarts. Reset link in the
  sheet clears to defaults.
- **Filter-aware empty state**: the projects-list empty message now
  differentiates "no projects yet" from "no projects match the
  current filter" so a filtered user doesn't think the list vanished.

### Changed

- `_ProjectsTab.build` applies the filter before partitioning into
  goals / workspaces, so the sub-project flatten and the kind split
  both honor the user's pick.

---

## v1.0.487-alpha — 2026-05-11

UI polish pass on project surfaces before wave 2. No new schema, no new
endpoints — re-shape what the existing `/v1/insights?team_id=X`
payload renders into.

### Changed

- **Project list rows: drop redundant kind chip.** The `[PROJECT]` /
  `[WORKSPACE]` leading chip on each row was redundant — the section
  header above each list (`PROJECTS` / `WORKSPACES`) already declares
  it. `ProjectKindChip` retained for project-detail use; only the
  list-row leading is gone.
- **Project list rows: 3-line card for goal projects.** Sources
  current phase, progress, open-AC count from
  `/v1/insights?team_id=X`'s `by_project[]` (no extra round-trip;
  same data the Insights icon already pulls).
  - Line 1: name · status dot · attention badge
  - Line 2: phase pill · "N open AC" chip (or "no open AC")
  - Line 3: progress bar + percentage
  - Parent-with-children rows append "N sub-projects" below the bar.
  - Workspaces, lifecycle-disabled projects, and goal projects that
    haven't been seen by Insights yet fall back to the existing
    two-line tile (no kind chip).
- **Team Insights page redesigned.** Pivoted from per-project card
  list (now redundant with the inline list rows) to a team-level
  aggregate dashboard:
  - **Summary tiles**: Active / Open AC / Open attention / Live <24h
  - **Phase distribution**: horizontal bar chart by current_phase
  - **Activity recency buckets**: <24h / <7d / >7d / idle
  - **Most recent · top 5**: tap → project detail
  - **Top agents · by event volume**: leaderboard from existing
    `by_agent[]` (sorted by `tokens_in`, fallback to event/tool counts)

---

## v1.0.486-alpha — 2026-05-11

Chassis follow-up wave 1 — D10 hero override mechanism + deliverables +
acceptance-criteria tiles. Per ADR-024 follow-up ordering:
[`docs/decisions/024-project-detail-chassis.md`](decisions/024-project-detail-chassis.md)
§Follow-up wedges (ordered).

### Added

- **Hero overrideability per-phase per-project (D10).** New migration
  `0038_project_overview_widget_overrides` adds
  `projects.overview_widget_overrides_json` (mirrors the v1.0.484
  tile-override column). `Server.resolveOverviewWidget` consults the
  override map first, then per-phase template YAML, then template
  default, then chassis default. Wire payload gains
  `overview_widget_overrides` (raw user map) and
  `overview_widget_template` (per-phase template-side map for the
  picker's Reset affordance). PATCH `projects` accepts
  `overview_widget_overrides`. Closes the ADR-023 inconsistency where
  the steward could swap tiles but not heroes.
- **Hero picker in `PhaseTileEditorSheet`.** ChoiceChip Wrap above
  the tile-composition section. Picks from the closed
  `kKnownOverviewWidgets` set with `overviewWidgetSpecFor` labels.
  Save bundles the tile + hero override into one PATCH; Reset
  clears both per-phase overrides.
- **`deliverables` tile slug** + `DeliverablesScreen`. Project-scoped
  flat list grouped by phase; tap → existing
  `StructuredDeliverableViewer`. Unblocks lifecycle-walkthrough W7-W8
  by making deliverables reachable without going through a phase
  chip.
- **`acceptance_criteria` tile slug** + `AcceptanceCriteriaScreen`.
  Project-scoped flat list grouped by phase with state filter
  (all/pending/met/failed/waived); tap → parent deliverable viewer.

### Changed

- **Closed `TileSlug` enum now 11 slugs** (was 9 at ADR-024 lock).
  Added `deliverables` + `acceptanceCriteria`. Wire format accepts
  `deliverables`, `acceptance_criteria`, `acceptance-criteria`,
  `criteria` as aliases for the AC slug.
- **`resolveOverviewWidget` signature** now takes an `overrides
  map[string]string` parameter. Empty/nil falls through to the
  prior template-side resolution. All three call sites
  (`handleListProjects`, `handleGetProject`, `handleCreateProject`)
  updated.

### Documentation

- ADR-024 D10 + Follow-up Wedges sections updated: wave 1 marked
  shipped, 11-slug locked set. Status block bumped to v1.0.486.
- `reference/project-detail-chassis.md` resolution chain rewritten
  (override → template phase → template default → chassis default),
  §8 per-project-vs-per-template matrix updated, §9 ordering table
  gains Status column.

---

## v1.0.485-alpha — 2026-05-11

Project overview attention redesign — W1+W2+W3. Plan:
[`docs/plans/project-overview-attention-redesign.md`](plans/project-overview-attention-redesign.md).

### Changed

- **Discussion AppBar icon dropped from project detail.** Was a
  redundant fourth navigation surface alongside the 5 tab pills, the
  AppBar Insights icon (deferred), and the in-Overview tile strip.
  Discussion remains reachable via the `TileSlug.discussion` tile,
  added to the current phase composition through the v1.0.484
  per-project `PhaseTileEditorSheet`. The Activity tab continues to
  cover the "what's been said?" use case for event-level feed.
  `lib/screens/projects/project_detail_screen.dart`.
- **Outer metadata rows + Archive action now collapsed by default**
  behind a "Details" `ExpansionTile` at the bottom of the Overview
  tab. The PortfolioHeader (goal, status, budget, task progress) and
  the InsightsPanel above the divider stay inline; only the
  rarely-accessed Name/Kind/Status/Goal/Steward template/On-create
  template/ID/Docs root/Created list and the destructive Archive
  CTA fold under the expander. F-pattern preserved: eye lands on
  banner → header → hero → tiles → metrics, then "Details" if needed.
  `lib/screens/projects/project_detail_screen.dart`.

### Added

- **Cross-project Insights surface — `/v1/insights?team_id=X` now
  returns `by_project[]`.** One row per goal-kind, non-archived
  project in the team: `{project_id, name, current_phase, status,
  progress, open_attention, open_criteria, last_activity}`. Sort:
  `last_activity` desc. Server-side hard cap 100 rows. Workspaces
  (`kind='standing'`) and archived projects filtered out per Q3 of
  the plan. `progress` follows the weighted formula
  `(phases_done + current_phase_AC_ratio) / phases_total` (Q2 (c)),
  smooth-monotonic across phase advances. Field is omitted from
  non-team scopes.
  `hub/internal/server/handlers_insights.go`,
  `hub/internal/server/handlers_insights_scope_test.go`.
- **Team overview AppBar icon on Projects list** → new
  `TeamOverviewInsightsScreen`. Renders one card per project with
  name, phase chip, status pill, progress bar (% derived from the
  weighted formula above), attention badge, open-criteria badge,
  and relative-time last-activity. Tap → opens project detail
  (looks up the full project map off `hubProvider.projects`).
  `lib/screens/projects/projects_screen.dart`,
  `lib/screens/insights/team_overview_insights_screen.dart`.

### Background

The project detail Overview tab had accumulated six vertical
regions (attention banner / PortfolioHeader / phase hero / tile
strip / InsightsPanel / metadata+Archive) plus AppBar icons for
Discussion + Template-YAML plus 5 tab pills plus chassis
PhaseRibbon — twelve interaction zones competing for above-fold
attention. Applied the three attention principles from the prior
design discussion (Orient → Focus → Explore): drop one redundant
navigation surface (W1), demote rarely-accessed metadata to a
collapsible footer (W2), and promote the missing cross-project
surface to its proper home on the Projects list AppBar (W3).
Risks register stays explicitly post-MVP — the closed `TileSlug`
enum keeps `risks` but no template surfaces it and no
implementation work lands here.

---

## v1.0.484-alpha — 2026-05-11

Lifecycle-walkthrough follow-ups batch (W1–W6). Plan:
[`docs/plans/lifecycle-walkthrough-followups.md`](plans/lifecycle-walkthrough-followups.md).

### Fixed

- **Plans / Schedules tiles now scope to the current project.** Tapping
  Plans from `research-method-demo` was dumping the team-wide list (5
  plans, one per seeded project) because `shortcut_tile_strip.dart`
  pushed `const PlansScreen()` with no project context. Both screens
  now accept a `projectId` constructor arg; the tile entry passes it.
  Filter sheets still let the user broaden to team-wide.
  `lib/screens/projects/plans_screen.dart`,
  `lib/screens/projects/schedules_screen.dart`,
  `lib/widgets/shortcut_tile_strip.dart`.

### Changed

- **Seed-demo `--shape lifecycle` plan_steps now use schema-valid kinds**
  (`agent_spawn` / `llm_call` / `shell` / `human_decision`) instead of
  the placeholder `agent_driven` that mirrored the phase ribbon. Each
  project now seeds realistic per-phase work — research-method-demo,
  for instance, has step kinds spanning `human_decision` (scope
  ratification), `agent_spawn` (lit-reviewer + critic), `llm_call`
  (draft method), and ends with a pending human_decision for
  ratification. Test coverage in `seed_demo_lifecycle_test.go` asserts
  every seeded kind is in `planStepKinds`. Phase progression itself
  still lives on `projects.phase` + `phase_history`, where it belongs.
- **Seed-demo `--shape lifecycle` now seeds project-scoped tasks too.**
  Each of the five demo projects gets 2–5 kanban tasks in mixed
  states (`todo` / `in_progress` / `done`), some with subtasks via
  `parent_task_id`. The Tasks tab on project detail is no longer
  empty during walkthrough QA.

### Added

- **`docs/reference/glossary.md` §10b — Project lifecycle entities.**
  Canonical entries + relationship arrows for project / phase / plan /
  plan-step / task / document / deliverable / acceptance criterion.
  Resolves the plan-vs-phase confusion the v1.0.482 walkthrough QA
  surfaced.
- **`steward-lifecycle-walkthrough.md` Scenario 0 — project conjuration.**
  New head-of-arc scenario where the steward creates the project from
  template via `projects.create` + `mobile.navigate`. Mirrors §11 of
  the agent-driven-mobile-ui discussion doc. Companion how-to also
  updated.
- **Configurable per-phase tile composition.** New column
  `projects.phase_tile_overrides_json` (migration 0037) holds a
  `{phase: [slug...]}` map. The hub also serves the template's YAML
  default at `phase_tiles_template` on the project payload. Mobile's
  `resolveTilesForPhase` resolves project override → template YAML →
  hardcoded safety-net → chassis default. No APK rebuild needed to
  change which tiles surface on which phase; the closed `TileSlug`
  vocabulary stays APK-bound, only the *composition* is data.
  `lib/widgets/shortcut_tile_strip.dart`,
  `hub/internal/server/handlers_projects.go`,
  `hub/internal/server/template_hydration.go`,
  `hub/migrations/0037_*.sql`.
- **On-device tile editor.** Trailing "Customize shortcuts for this
  phase" row on the tile strip opens a modal sheet — checkbox + drag
  reorder over the full `TileSlug` vocabulary; saves via PATCH
  `phase_tile_overrides`. A "Reset" button clears the per-project
  override and falls back to the template default. Both the steward
  (`projects.update` MCP tool) and the user (this sheet) write to the
  same `phase_tile_overrides_json` field.
  `lib/widgets/shortcut_tile_strip.dart` (PhaseTileEditorSheet).
- **Research template `phase_specs[idea].tiles = [Documents]`.** Idea
  phase is conversation-first by spec, but the steward routinely
  creates idea memos there; the Documents tile gives the director a
  path to find them. Replaces v1.0.483's hardcoded-in-Dart workaround
  with a template-driven override.

---

## v1.0.483-alpha — 2026-05-11

### Fixed

- **General steward sessions no longer bucket under "Detached".**
  `isStewardHandle()` deliberately excludes `@steward` so spawn /
  collision-check sites treat the team concierge as separate; the
  Sessions screen reused that predicate when building `liveStewardIds`,
  which caused any session whose `current_agent_id` pointed at the
  general steward to fall through to the orphan branch. The Sessions
  screen now widens its check to `isStewardHandle(h) ||
  isGeneralStewardHandle(h)` — the predicate's other call sites are
  unchanged. `lib/screens/sessions/sessions_screen.dart`.
- **Documents tile now shows on the idea phase Overview.** The research
  template marked idea as "conversation-first" with `tiles: []`, but
  scenario 3 of the lifecycle walkthrough creates idea memos via
  `documents.create` — the document landed in the DB and the director
  had no UI path to find it. Added `TileSlug.documents` to the idea
  phase in both the spec (`docs/reference/research-template-spec.md`
  §3) and the renderer (`lib/widgets/shortcut_tile_strip.dart`).
- **Steward overlay no longer spams "stream errored: connection closed".**
  After a turn ends, the SSE goes idle and mobile carriers / reverse
  proxies typically reap the TCP socket within ~60–90s. The overlay
  controller used to post a system note on every reconnect cycle; now
  it (a) suppresses notes for known idle-drop signatures (matching the
  heuristic `agent_events_provider` / `agent_feed` already use) and
  (b) defers real-error notes by 3s so a fast reconnect heals
  invisibly. Server-side ping cadence also dropped from 15s → 5s
  on both agent-events and channel-events streams to give NATs /
  proxies more frequent activity to count.
- **Snippets manage page now has an Add action + the 3 starter chips
  are editable.** The page wraps `SnippetsScreen` (a Vault embedded
  body widget without its own Add button), so pushing it as a route
  from the overlay's Edit chip surfaced a read-only-looking list. The
  3 chip-strip defaults were also in-memory constants that never
  entered the snippet store. Both are fixed: the manage page AppBar
  carries an Add action that opens `SnippetEditDialog` pre-filled
  with `category=steward`, and the 3 starter chips moved into
  `SnippetPresets` as a `steward` profile — they now render in the
  manage page with the existing preset-tile machinery (tap to edit,
  swipe to delete, restore-chip to revert overrides).

### Changed

- **Server SSE ping cadence: 15s → 5s** (`handlers_agent_events.go`,
  `handlers_stream.go`). Shorter cadence keeps mobile carrier NATs /
  reverse proxies from reaping quiet streams between turns.

---

## v1.0.482-alpha — 2026-05-10

### Changed

- **Edit chip on the overlay no longer auto-collapses the panel.**
  v1.0.481 made the Edit chip dismiss the panel before pushing the
  snippets manager. The principal flagged it as inconsistent with
  ADR-023 D1 (persistent overlay across all routes); `mobile.navigate`-
  driven pushes don't auto-collapse, so a chip-driven push shouldn't
  either. The panel is also draggable / resizable / opacity-tunable —
  user can move it themselves if it covers the destination. Reverted
  the auto-close + the `onCloseRequested` plumbing through
  StewardOverlayChips. Note kept on `_ManageChip` documenting that
  `_openFullSession` (header "Open in new" button) IS the intended
  exception: it opens the steward's full session transcript = same
  conversation as the panel, leaving both open is redundant.

  v1.0.481's Scaffold wrapper for `SnippetsScreen` (the actual fix
  for yellow underlines / can't-scroll / incomplete chrome) stays
  in place.

---

## v1.0.481-alpha — 2026-05-10

### Fixed

- **Snippets manager renders correctly when opened from the
  overlay's Edit chip.** v1.0.479 added a trailing "Edit" chip on
  the steward-overlay chip strip that pushed `SnippetsScreen` as
  a `MaterialPageRoute`. `SnippetsScreen` was authored as an
  *embedded* widget for the Vault page (`vault_screen.dart`):
  it returns a bare `Column`, no Material ancestor, no scroll
  view, no AppBar / system-inset padding. Pushed directly as a
  route, the user saw yellow "missing Material" double-underlines
  on every Text, no ability to scroll past the visible viewport,
  and an incomplete page chrome. Compounded by the overlay panel
  staying expanded on top, since the chip's onTap didn't dismiss
  the panel — the destination route rendered behind the panel.
  Fix layered:
  - New `_SnippetsManagePage` wrapper inside
    `steward_overlay_chips.dart` provides the missing Scaffold +
    AppBar + `SafeArea` + `SingleChildScrollView` so the
    embedded `SnippetsScreen` body has a real route chrome to
    sit in.
  - `StewardOverlayChips` accepts an optional
    `onCloseRequested` callback; the chat surface plumbs its
    own `onCloseRequested` (= `_ExpandedPanel.onClose`)
    through, and the Edit chip calls it before pushing so the
    panel collapses first.
  No new MCP tools, no new screens — purely a hosting fix.

---

## v1.0.480-alpha — 2026-05-10

Follow-up to v1.0.479. The QA report on issue 4 was sharper than
v1.0.479 read it: "there is keyboard for input but not my input
method." A keyboard DID attach — it just wasn't the user's CJK
IME. v1.0.479's puck-hide + keyboard-shift work address tap
hit-testing and IME-covered-panel layout, but neither addresses
the IME-mode bug.

### Fixed

- **CJK / non-Latin IMEs now engage on the overlay chat input.**
  The TextField was setting `autocorrect: false` and
  `enableSuggestions: false` (added in v1.0.471 as belt-and-
  suspenders for the deleted-text-returning bug; v1.0.472 fixed
  that bug architecturally via rebuild-scope isolation, so the
  flags were no longer load-bearing). Android maps them to
  `TYPE_TEXT_FLAG_NO_AUTO_CORRECT` / `TYPE_TEXT_FLAG_NO_SUGGESTIONS`;
  CJK IMEs (Sogou, Gboard-CN, Baidu, Mozc, native Japanese /
  Korean stacks) treat no-suggestions as a hard signal to fall
  back to Latin-only mode because the suggestion strip IS their
  candidate display. The user's selected IME would attach but
  refuse to engage its composition pipeline. Both flags are now
  removed; the v1.0.472 isolation continues to keep the input
  subtree out of the SSE rebuild scope.

---

## v1.0.479-alpha — 2026-05-10

Four-issue QA fix on top of v1.0.478. Each was an independently
visible regression once the user exercised the overlay end-to-end.

### Fixed

- **`mobile.intent` events stamped with `session_id`.** v1.0.474's
  W1 backfill added a session-filtered SSE subscription (`?session=`)
  to scope the overlay to the steward's current session. The hub's
  `handleStreamAgentEvents` filter drops any event whose
  `session_id` doesn't match. `handleMobileIntent` published the
  event with `agent_id` + `team_id` but never `session_id`, so the
  filter dropped every navigation intent → the past-tense pill
  never rendered, the URI never dispatched, the user only saw the
  steward's text reply ("done — opened your projects") with no
  side effect. Fix: `lookupSessionForAgent(stewardID)` and stamp
  the result on the bus envelope, matching the pattern
  `handlePostAgentEvent` already uses for text frames. New test
  `TestMobileIntent_StampsSessionID` locks the contract.
- **SSE auto-reconnect on stream close/error.** The controller used
  to attach the SSE subscription once at bootstrap and append a
  system message on `onError` / `onDone`, leaving the panel dead
  until the user manually reopened it. New behaviour:
  exponential backoff (1s → 16s capped) reconnect with the last
  observed seq as the resume cursor, single user-visible system
  note per reconnect cycle, full reset of the backoff once a
  fresh frame arrives. Resolves the QA report "stream errored
  saying connection closed while the turn is ended."
- **"Open in new" header button now opens the full session.** The
  panel's BuildContext sits OUTSIDE the inner Navigator (overlay
  is mounted via `MaterialApp.builder`, which wraps the Navigator
  widget). `Navigator.of(context)` from there couldn't resolve
  the inner Navigator. Switched to the shared
  `overlayNavigatorKeyProvider` — same pattern the live
  `_dispatchIntentLive` path already uses — which IS the
  `MaterialApp.navigatorKey` override.
- **Snippet "Edit" entry-point added to the chip strip.** Trailing
  pencil chip pushes `SnippetsScreen` via the same overlay
  navigator key. Without this, users could not see / add / edit
  steward-tagged snippets — the chip strip only ever showed the
  three built-in defaults.
- **System IME now appears when tapping the chat input.** Two
  fixes layered:
  - **Puck hidden while panel expanded.** The puck (56×56)
    floated at a Stack position that overlapped the bottom-right
    chat surface (chips + input + send). Stack paints later
    children on top → puck ate taps on the input region → tap
    collapsed the panel via the puck's `onTap` instead of
    focusing the TextField → IME never attached. Hide the puck
    when expanded so the panel owns its own hit-testing surface.
  - **Keyboard-aware panel shift.** When `MediaQuery.viewInsets.
    bottom > 0` and the panel bottom would go behind the
    keyboard, shift the rect up by the overlap (+12px breathing
    room). Non-persistent — snaps back to the saved rect when
    IME closes. The overlay isn't inside a Scaffold so
    `resizeToAvoidBottomInset` doesn't apply; this is the manual
    equivalent.

### Test

- New `TestMobileIntent_StampsSessionID` asserts the bus envelope
  carries `session_id` so the SSE filter passes the event.
  All five existing mobile-intent tests still pass.

---

## v1.0.478-alpha — 2026-05-10

CI fix on top of v1.0.477 — the v1.0.477 tag's Android build
failed `flutter analyze` because the overlay migration used
`ref.listenManual`, which doesn't exist on Riverpod 3.x's `Ref`
(only on `WidgetRef`). The Notifier-side equivalent doesn't
compose cleanly with the async-resolved-key pattern this
overlay needs without a non-trivial restructure (split-provider
shape: separate FutureProvider for `(agentId, sessionId)`
resolution + family-keyed Notifier for the events listener).

This release ships v1.0.477's WORKING parts (the
`agentEventsProvider` infrastructure file) and reverts the
broken overlay migration. v1.0.476's overlay controller code
is restored verbatim.

### Reverted from v1.0.477
- `StewardOverlayController` migration to the shared provider.
  Overlay continues to own its own SSE subscription + backfill
  + reconnect logic for now.

### Retained from v1.0.477 (still good)
- `lib/providers/agent_events_provider.dart` — the
  `NotifierProvider.autoDispose.family<AgentEventsKey>` shared
  data layer. Sits in the codebase ready for consumers; today
  has no callers but P2 (AgentFeed migration, post-MVP) and
  future surfaces will plug in.

### Notes
- Tag v1.0.477-alpha exists in the repo but its release build
  did not produce an APK. v1.0.478 is the canonical successor.
- The overlay's pre-existing capabilities (no cache-only first
  paint; no reconnect-with-backoff) remain pre-existing —
  they'd have come along for free via the migration if the
  Riverpod 3.x lifecycle had cooperated. Migrating the overlay
  cleanly needs a split-provider refactor that is out of scope
  for the CI fix; tracked as a follow-up under the same plan
  doc.
- The provider file itself adds to `flutter analyze`'s noise as
  defines-but-no-callers, but Dart doesn't error on unused
  public symbols.

---

## v1.0.477-alpha — 2026-05-10 (broken — see v1.0.478)

Build of v1.0.477's tag failed `flutter analyze`; no APK
produced. See v1.0.478 above for the corrected release shape
and the retained / reverted breakdown.

---

## v1.0.476-alpha — 2026-05-10

Compact-mode rework of the steward overlay (Option A from the
"compact vs duplicate" architectural review). The overlay was
rendering essentially the same content as the Sessions screen —
just with chat-bubble styling instead of full-fidelity cards.
This reframes it as the recent-directive-context surface: shorter
window, action-aware rendering, and a clear pivot to the full
session for everything else.

### Changed
- **Rolling message cap dropped from 100 → 20** (`_overlayMessageCap`).
  The Sessions screen owns the full transcript; the overlay's job
  is the last ~10 turns of recent directive context, not a parallel
  log.
- **`mobile.intent` events now render on cold-open replay** as
  past-tense pills ("Steward → Insights · 14:32"). Reverses the
  v1.0.474 B5 decision — those are the most informative directive
  signal and skipping them on replay was the wrong call. The pill
  shape uses `OverlayIntentAction{verb, target, uri}` which is
  action-aware (defaults to navigation `→` for v1; future create /
  edit / write actions get the right verb without a model change).
  Tap a pill to re-fire the URI.
- **Long steward replies truncate at 240 chars** with a "open full
  session for the rest" italic suffix. Keeps the overlay's
  directive purpose obvious — it's not a transcript.
- **Live-vs-replay split for `mobile.intent`** clarified in the
  controller. `_eventToMessage` produces the chat bubble for both
  live and replay paths (single source of truth for shape);
  `_dispatchIntentLive` runs ONLY on live SSE — handles the actual
  navigation + snackbar without re-appending a message.

### Added
- **"Open full session" icon** in the panel header. Pushes
  `SessionChatScreen` for the steward's current session, then
  collapses the overlay so the user can scroll the full transcript
  unobstructed. Disabled (greyed) until backfill resolves agentId
  + sessionId.
- **Pending-attention badge** in the panel header. Counts attention
  items where `agent_id == steward_agent_id` and status is `open`
  / `pending`. Tap jumps to the Me tab + collapses the overlay.
  Hidden when 0. Sourced directly from `hubProvider.attention`,
  not duplicated.

### Notes
- The full transcript / attention-detail / approval-decide flows
  remain on their dedicated screens. The overlay only links into
  them — no data duplication.
- The agent_events SSE subscription is still owned independently
  by the overlay controller (Option B from the review — sharing
  the data source with `agent_feed.dart` — is a cleanup wedge for
  later, not bundled here).

---

## v1.0.475-alpha — 2026-05-10

W2 + W3 of the overlay-history-and-snippets plan. Closes the
plan's three-workband bundle.

### Added
- **Quick-action chip strip above the chat input** (W3). User
  snippets with `category == 'steward'` (B1) render first in
  insertion order, followed by three built-in defaults so the
  row is non-empty on cold install: "Show insights", "What's
  blocked?", "Open my projects". Tap fires the snippet body
  through the same `sendUserText` path the input uses; the bubble
  appears via the SSE round-trip (W2 path). Defaults are visually
  muted so users can tell which they can replace by editing
  their snippets.
  (`lib/widgets/steward_overlay/steward_overlay_chips.dart`,
  `lib/widgets/steward_overlay/steward_overlay_chat.dart`)

### Changed
- **User input renders as user bubbles via SSE round-trip** (W2 —
  Option A). The hub already publishes user input as `kind ==
  'input.text'` with `producer == 'user'` on the same agent bus
  the steward output flows through; we now demux those frames in
  `_handleEvent` and `_hydrateFromEvents` (cold-open backfill)
  via a single `_eventToMessage` folder. Live and replay paths
  produce identical bubble shapes — no dedup, no risk of
  divergence between cold-open render and live typing.
- **Local pre-echo dropped from `sendUserText`.** The user's bubble
  no longer appears synchronously on tap-send; instead it arrives
  ~100-300 ms later when the SSE echo lands. Trade-off documented
  in the controller (Option A vs Option B in the plan). The send
  button's existing spinner state covers the latency window. If
  QA flags the lag, swap to id-based dedup (~30 LOC).

### Notes
- The chip strip is a sibling of the input + messages region —
  watches `snippetsProvider` only, so SSE events don't trigger
  rebuilds on the chip subtree.
- Built-in default snippet ids are prefixed `_overlay_default_*`
  to prevent collision with user snippets.
- W4 polish (visual / accessibility / haptics) from the plan is
  optional and not bundled here.

---

## v1.0.474-alpha — 2026-05-10

W1 of the overlay-history-and-snippets plan.

### Added
- **Overlay chat backfills the last 50 events on cold open.** Per
  the plan's W1 + B1–B6 decisions: pull through
  `listAgentEventsCached` (mirrors `agent_feed.dart`'s pattern),
  filter both the backfill AND the live `streamAgentEvents` to
  the resolved session id (B3), reverse from seq DESC tail order
  to ASC for chat display, render `kind=='text'` frames as
  steward bubbles. The `agentId` field stays null — the panel's
  spinner stays up — until backfill completes (B6), so users
  don't see an empty chat flash before content appears.
  (`lib/widgets/steward_overlay/steward_overlay_controller.dart`)

### Changed
- **`mobile.intent` events skipped on backfill replay** (B5).
  Live navigation events still render the snackbar + system note
  + dispatch the route, but historical ones are dropped — they're
  transient logs and re-rendering them as if the steward is
  navigating *now* would be confusing.
- **`streamAgentEvents` now subscribes with `sinceSeq` cursor
  derived from the backfill** so the hub doesn't replay frames
  the panel already shows.

### Notes
- Backfill failure is non-fatal: if both network and cache miss,
  the panel proceeds with empty messages + a system note ("Could
  not load history: …") so the user can still chat live.
- Cache stale fallback also surfaces as a system note ("Showing
  cached history (offline)").
- W2 (user-input rendering) and W3 (snippet chips) are still
  upcoming in the same wedge.

---

## v1.0.473-alpha — 2026-05-10

### Fixed
- **System IME (Gboard) now attaches to the steward overlay text
  field.** v1.0.471 added `autofillHints: const []` as one of three
  belt-and-suspenders flags meant to harden the input against
  predictive-restore. The empty list (rather than `null`, the
  default) signals Android's `AutofillManager` that this field is
  *managed by autofill but has no hints* — which on some
  Android+Gboard combinations causes the IME to refuse to attach.
  Visible bug in v1.0.472: tapping the input did nothing; no
  keyboard appeared. Audit shows none of the other deterministic-
  typing inputs in the codebase (`compose_bar` direct mode,
  `hub_bootstrap`, `templates`) set `autofillHints`; we shouldn't
  either. Dropped the line and added a do-not-restore comment.
  `autocorrect: false` and `enableSuggestions: false` stay — they
  match the rest of the codebase and don't suppress IME attach.
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`)

The rebuild-scope fix from v1.0.472 (split `_MessagesRegion` /
`_ChatInputSlot` siblings) remains the primary correctness
mechanism; the IME flags now serve only as cosmetic alignment with
the rest of the app.

---

## v1.0.472-alpha — 2026-05-10

The architectural fix the v1.0.471 IME workaround was masking.

### Changed
- **Steward overlay chat: rebuild scope tightened to the messages
  region only.** Before, `_StewardOverlayChatState` was a
  ConsumerStatefulWidget whose `build()` watched
  `stewardOverlayControllerProvider` directly, so every SSE event
  (text chunks, tool calls, system frames) rebuilt the entire
  Column — including the `_ChatInput` subtree. Even with stable
  controller + focus node, the rebuild traversal triggered
  EditableText's `_updateRemoteEditingValueIfNeeded` IME poke,
  which GBoard interpreted as a composition reset and rebounded
  by re-pushing its cached predictive word. That was the real
  root cause of "deleted text returns when retyping."
  Restructured into three sibling pieces:
  - `_StewardOverlayChatState.build()` returns a `const Column` —
    no `ref.watch`, never rebuilds on SSE events.
  - `_MessagesRegion` (new ConsumerStatefulWidget) is the *only*
    widget that watches the provider; loading / error / empty /
    list branches all live here.
  - `_ChatInputSlot` (new const StatelessWidget) is a pure sibling
    of `_MessagesRegion` — its subtree is structurally untouched
    by SSE traffic, so the IME never gets poked outside of actual
    user input.
  Net effect: IME state is now genuinely orthogonal to network
  state, which is how it should have been from the start. The
  `autocorrect: false` / `enableSuggestions: false` /
  `autofillHints: const []` flags from v1.0.471 are kept as
  belt-and-suspenders — they are no longer load-bearing for the
  bug fix, but they keep the overlay's typing feel consistent
  with the rest of the app's deterministic-input pattern
  (compose_bar direct mode, hub_bootstrap, templates).
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`)

---

## v1.0.471-alpha — 2026-05-10

Steward overlay text-input bug, finally root-caused. Plus two
pieces of design work that the principal asked be documented
rather than implemented inline.

### Fixed
- **Old / history input no longer reappears in the steward overlay
  text field as the user retypes.** v1.0.467 extracted the input
  to its own non-Consumer `_ChatInput` State which fixed the
  cursor-jump-to-end symptom, but it never disabled the IME's own
  predictive-restore path — so deleted characters were still
  re-pushed by GBoard after each IME detach / re-attach (which
  happens on every SSE event because the chat parent watches a
  high-frequency Riverpod provider). Mirrors what the rest of the
  codebase already does for inputs that need deterministic typing
  (`compose_bar`, `hub_bootstrap`, `templates`):
  - `autocorrect: false`
  - `enableSuggestions: false`
  - `autofillHints: const []`
  Also moved the `FocusNode` to be owned by the input's State so
  it isn't re-minted on each parent rebuild — a stable focus node
  reduces the IME attach/detach churn that was triggering the
  predictive-restore in the first place.
  (`lib/widgets/steward_overlay/steward_overlay_chat.dart`)

### Added — design work, not implementation
- `docs/discussions/agent-driven-mobile-ui.md §13` —
  floating-surface capacity model. Locks the recommendation that
  multi-conversation goes through **one shell + multi-conversation
  list inside** (Pattern B) rather than N independent pucks.
  Reasons: SSE bandwidth, drag/resize state, attention budget,
  and clean URI-router fit. Pattern A (N-pucks) is rejected;
  Pattern C (edge-dock) is a deferred cosmetic. Adds Q15 to the
  ADR-023 question set.
- `docs/plans/overlay-history-and-snippets.md` (new) — wedge plan
  bundling three QA gaps the principal flagged together: (W1)
  cold-start panel is empty until new SSE events arrive, (W2)
  user's own prior prompts never render even when steward output
  does, (W3) no quick-action chip strip above the input. ~250 LOC
  mobile-only, no hub change. Mirrors `agent_feed.dart`'s
  `listAgentEvents` + `streamAgentEvents(sinceSeq=...)` pattern
  for backfill. Hub already publishes user input as `kind =
  input.text` with `producer = user` (verified at
  `handlers_agent_input.go:378-407`); the wedge teaches the
  overlay controller to render that.

---

## v1.0.470-alpha — 2026-05-10

Two QA fixes after v1.0.469.

### Fixed
- **No more yellow double underlines under text in the steward
  overlay panel.** The expanded panel was a bare `Container`,
  with no `Material` ancestor — Flutter's classic "missing Material"
  debug hint draws yellow underlines under every Text in that
  scope. The overlay is mounted via `MaterialApp.builder`, which
  sits OUTSIDE the Navigator's Material/Scaffold scope, so the
  fix has to be local: wrap the panel column in
  `Material(type: MaterialType.transparency)` so descendants
  inherit a DefaultTextStyle without changing any pixel of the
  panel's appearance.
  (`lib/widgets/steward_overlay/steward_overlay.dart`)
- **Steward setup sheet no longer auto-pops.** The W4 first-run
  experience used to auto-trigger `showSpawnStewardSheet` from
  `_maybeShowBootstrap` whenever the Projects screen saw a
  configured team with online hosts and no steward. v1.0.468 added
  a `staleSince != null` guard but the sheet still popped once the
  cache hydrated as empty. Principal: stop auto-popping entirely.
  Removed both auto-trigger paths (initState postFrame +
  `ref.listen` on `hubProvider`); spawning the steward is now a
  fully manual gesture (the spawn sheet is still reachable from
  every previous manual entry point — `Replace steward`,
  Sessions screen, etc.).
  (`lib/screens/projects/projects_screen.dart`)

### Removed
- `_maybeShowBootstrap`, `_bootstrapAttempted` flag, and the
  `ref.listen<AsyncValue<HubState>>` block in
  `_ProjectsScreenState`. The `bootstrapDismissedKey` helper
  itself stays — it's still used by `confirmAndRecreateSteward`
  to clear the Skip flag on explicit recreate.

---

## v1.0.469-alpha — 2026-05-10

Steward overlay is now **non-modal** — a deliberate UX shift in
response to v1.0.468 QA. Principal report: tapping the Me bottom-
nav tab or scrolling the underlying page collapsed the panel
instead of doing what they wanted. Root cause was the full-screen
`Positioned.fill` barrier with `onTap: _collapse` and a translucent
black scrim — that's the *modal sheet* pattern, wrong for a
floating chat that's meant to coexist with whatever surface the
user is reading.

### Changed

- **Panel coexists with underlying page.** Removed the tap-to-
  close barrier and the scrim. Tap → bottom nav switches tabs.
  Scroll → page scrolls. The panel just floats on top with its
  border + drop shadow as the elevation cue.
- **Dismissal is explicit only.** Tap the X button in the panel
  header, or tap the puck — both still work. There's no longer
  any "tap outside to close" affordance because there's no
  outside-to-close concept in non-modal floating chat (Slack /
  Discord / iOS PiP all behave the same way).

This unblocks the original use case from the discussion doc §2:
"the steward is *above* the app, sharing the user's context, so
the user-to-system information loop runs at superfluid efficiency"
— with the modal barrier the loop was anything but.

---

## v1.0.468-alpha — 2026-05-10

Two QA items from v1.0.467 testing:

### Fixed

- **Spurious steward-bootstrap sheet on app open.** The Projects
  tab's `_maybeShowBootstrap` listener fires on every hub-state
  transition. On cold start the first transition is the
  cache-hydration event with `staleSince != null` — and the cached
  agents list may not reflect the *current* live steward (the user
  could have spawned one after the last cache write). Bootstrap
  spuriously fired before the network refresh landed, popping a
  "set up your steward" sheet over an already-running steward. The
  listener now skips while `staleSince != null` and re-evaluates on
  the next event after refreshAll succeeds (and clears stale).

### Added

- **Panel opacity slider** in Settings → Experimental → Steward
  overlay. Configurable 50–100% (default 85%); applies to the panel
  background only so chat text and inputs stay fully readable while
  the underlying page peeks through. Wrapping in `Opacity()` was
  rejected — it would fade messages too, wrong for a chat surface.
  New persisted key:
  `settings_steward_overlay_panel_opacity` (double 0.5..1.0).

---

## v1.0.467-alpha — 2026-05-10

Steward overlay input-box fixes from v1.0.466 QA. Two symptoms,
same root cause:

1. Deleting some text and retyping caused the deleted text to
   come back automatically.
2. Tapping a new cursor location and then typing made the cursor
   jump to the end.

Both are signatures of the `TextEditingController` being reset by
parent rebuilds. The chat widget watches
`stewardOverlayControllerProvider` (which emits on every SSE
event), and the controller previously lived in that watching
State — every steward event reached down through the TextField
subtree, occasionally re-applying a stale value.

### Fixed

- **TextField subtree is now isolated.** The input box moved out
  of `_StewardOverlayChatState` (which watches Riverpod) into its
  own `_ChatInput` `StatefulWidget` with its own
  `TextEditingController`. The chat parent's rebuilds no longer
  cascade into the input — only the input's own `setState` calls
  rebuild it.
- **Stable widget key** (`ValueKey('steward-overlay-chat-input')`)
  on `_ChatInput` so its State survives even if the parent's tree
  shape ever changes.
- **Send-clear timing.** Previously the controller was cleared
  AFTER the network round-trip, which wiped any text the user
  typed during the send. Now the controller clears immediately,
  and on send-failure the original text is restored only if the
  user hasn't started typing something new.
- **Multiline keyboard hints.** Added `keyboardType:
  TextInputType.multiline` and `textInputAction:
  TextInputAction.newline` so the IME treats the field as a
  multi-line composer instead of single-line text.

### Removed

- `_Composer` StatelessWidget (folded into `_ChatInput`).

---

## v1.0.466-alpha — 2026-05-10

Steward overlay layout customisation. Principal QA: the prototype's
fixed-size, bottom-anchored panel covers content the user is trying
to read while talking to the steward. The whole point of the
overlay is "see related info while directing the steward" — a
non-movable panel breaks that.

### Added

- **Drag the panel.** Panel header is now a drag handle (drag
  indicator icon + grab cursor on web). Drag from anywhere on the
  header bar except the close button to reposition the panel.
- **Resize the panel.** Bottom-right corner resize grip with a
  diagonal arrow icon. Drag to resize width + height; clamped to
  260×200 minimum and the viewport size.
- **Layout persists across app restarts.** Puck position + panel
  rect now survive via `shared_preferences`; previously the
  position reset to the bottom-right corner every cold start. New
  keys: `settings_steward_overlay_{puck_x,puck_y,panel_left,
  panel_top,panel_width,panel_height}` (all double, all null until
  first user customisation).
- **Settings → Experimental → Steward overlay toggle** (default
  on). Disable to hide the puck entirely; the controller no longer
  starts when disabled, freeing the SSE subscription too.

### Changed

- `_StewardOverlayHost` (in `main.dart`) now gates overlay mount on
  the new `stewardOverlayEnabled` setting in addition to hub config
  presence.

---

## v1.0.465-alpha — 2026-05-10

Agent-driven mobile UI prototype — first round of QA fixes from the
v1.0.464 test run. The principal reported that asking the steward
"take me to insights" produced no visible response or navigation in
the overlay — only a tool_call card in the session transcript.
Two root causes plus diagnostic improvements so the next test
surfaces what's happening directly in the panel.

### Fixed

- **Overlay chat now renders steward text replies.** `_extractText`
  in `steward_overlay_controller.dart` was reading `evt['body']` —
  but the hub's agent-events bus envelope publishes the assistant
  payload under `evt['payload']` (claude-sdk text frames carry
  `{"text": "...", "message_id": "..."}`). The body field never
  existed; every text reply was being silently dropped.
- **`mobile.intent` failure modes are now visible.** Every silent
  `return` in `_dispatchIntent` (empty URI, unparseable URI,
  navigator-not-ready) now appends a system message to the chat
  panel so the user can tell *which* path dropped the intent
  instead of seeing nothing.
- **SSE stream death is visible.** `onError` and `onDone` on the
  steward stream subscription now append a system message so a
  silent disconnect doesn't look like the steward simply not
  responding.

### Added

- `kDebugMode` console print of every incoming SSE frame
  (`[steward-overlay] evt kind=… keys=…`) so logcat reveals which
  events the overlay sees during diagnosis.

---

## v1.0.464-alpha — 2026-05-10

Agent-driven mobile UI prototype — first-stage spike. The steward
can now navigate the user's app to any v1 destination via a
persistent floating overlay; user talks (text + system-IME voice),
steward listens + responds + navigates. Read-only verbs only —
edits, approvals, ratifications still require manual taps. Validates
the architectural calls in
[`discussions/agent-driven-mobile-ui.md`](discussions/agent-driven-mobile-ui.md):
URI-shaped public API, shared-state model, the multiplex-screen
metaphor.

### Added

- **Persistent floating overlay** (`lib/widgets/steward_overlay/`).
  Draggable steward puck mounted at the app root via
  `MaterialApp.builder`; expands to half-height chat panel on tap.
  Survives Navigator pushes/pops + tab switches. Hides itself when
  the hub isn't configured.
- **`mobile.navigate(uri)` MCP tool** — new `mobile.*` tool family.
  Steward emits `termipod://...` URIs; hub publishes
  `mobile.intent` events on the general steward's bus channel;
  mobile dispatches via `navigateToUri`.
- **`POST /v1/teams/{team}/mobile/intent`** endpoint — validates
  URI scheme (`termipod://` / `muxpod://`), publishes the SSE
  event, records an `audit_events` row so steward-driven
  navigations are reviewable. 5 hub tests cover publish + audit
  + bad scheme + no-steward + missing-uri paths.
- **URI router** (`lib/services/deep_link/uri_router.dart`) —
  single dispatcher for both legacy `DeepLinkService` cold/warm
  links and the new steward intents. v1 grammar: top-level tabs
  (projects/activity/me/hosts/settings), project detail, session
  chat, agent detail, insights with scope qualifier.
- **Steward chat embedded in overlay**
  (`steward_overlay_chat.dart` + `steward_overlay_controller.dart`).
  Lazily ensures the general steward, subscribes to its SSE
  stream, demultiplexes text frames vs `mobile.intent` events.
  System-row + snackbar feedback ("Steward → \<label>") on every
  navigation; visible even when the chat panel is collapsed
  (puck-only).
- **`steward.general.v1` template** updated — exposes
  `mobile.navigate` in `default_capabilities`; bundled prompt
  gains a "Driving the mobile app" section listing the URI
  grammar + when to invoke navigate.
- **How-to test doc** —
  [`docs/how-to/test-agent-driven-prototype.md`](how-to/test-agent-driven-prototype.md).
  10 numbered scenarios with expected behaviour + failure-mode
  signatures so QA can report issues precisely.

### Changed

- `MyApp` accepts the navigator key as a constructor param; main
  exposes it via `overlayNavigatorKeyProvider` so the overlay
  controller can dispatch routes from the SSE listener
  (independent of widget-tree context).

### Documents

- New how-to: [test-agent-driven-prototype.md](how-to/test-agent-driven-prototype.md).
- The discussion doc at
  [`discussions/agent-driven-mobile-ui.md`](discussions/agent-driven-mobile-ui.md)
  is referenced by the prototype but stays Open — ADR-023 will
  resolve once the prototype findings come in.

### Lessons

- The package-level navigator key + provider override pattern
  cleanly hands a stable key to both `MaterialApp` and any
  controller that needs to push routes from outside the widget
  tree. Beats per-widget GlobalKeys juggled through callbacks.

---

## v1.0.463-alpha — 2026-05-09

Steward Insights wedge — surfaces an aggregate view of every live
steward (general + domain) and adds a time-range picker to the
fullscreen Insights view. Closes the "where do I see steward usage"
question raised after v1.0.462: the Persistent Steward Card jumps
straight into chat, so per-agent insights for stewards previously
required navigating to the Sessions screen, finding the right row,
and opening Agent Detail — none of which scaled past one steward.

### Added

- **Sessions AppBar → Insights icon.** Pushes a fullscreen
  `InsightsScreen` scoped to `team_stewards`. Aggregates every agent
  whose handle matches the steward predicate (`steward`, `*-steward`,
  or `@steward`) on the active team. One destination, all stewards.
- **`/v1/insights?team_id=X&kind=steward` qualifier.** Narrows the
  team-scoped aggregator to steward-handle agents. Response echoes
  `scope.kind = "team_stewards"` so the body is self-describing.
  `kind` is silently ignored on non-team scopes (hub-side
  `insights_scope.go`).
- **`by_agent` breakdown dimension on `/v1/insights`.** New top-level
  array with one row per agent in the scope (excluding agent scope,
  where the breakdown is degenerate). Each row carries
  `agent_id` / `handle` / `engine` / `status` / token totals /
  `turns` / `errors`. Sorted by `tokens_in` desc.
- **`InsightsByAgentSection` widget.** Renders the new dimension
  inside any non-agent-scope `InsightsScreen`. Steward rows get a
  small concierge badge; tap a row → drills into that single agent's
  fullscreen Insights view.
- **Time-range picker on `InsightsScreen`.** ChoiceChip row at the
  top — 24h / 7d / 30d. Selecting a chip re-keys the
  `insightsProvider` family (since/until are now part of
  `InsightsScope`'s identity), so each window has its own snapshot
  cache row that persists across screen revisits.

### Changed

- `InsightsScope` is now a value object that includes `since` /
  `until` plus the existing kind+id pair. Equality + hashCode were
  updated so the family provider's cache key includes the time
  window. `withWindow()` returns a copy with new bounds.
- `InsightsScreen` is now a `ConsumerStatefulWidget` (was
  `ConsumerWidget`). Holds the selected range + the "frozen now"
  timestamp so the family-provider cache key stays stable across
  rebuilds inside one chip view.

### Documents

- ADR-022 §D3 amended to call out `team_stewards` as a sub-qualifier
  on team scope (not a sixth top-level scope kind). Phase 2 plan
  status unchanged — this wedge ships post-MVP-completion.
- `api-overview.md` §3.13 updated to document `kind=steward` and
  the `by_agent` dimension.

### Lessons

- The package-level `hubInsightsCache` is shared across tests in one
  `go test` invocation — adjacent tests that hit the same
  `(scope_kind, scope_id, since, until)` key can see each other's
  bodies on systems with sub-nanosecond clock collisions. Added
  `resetInsightsCache()` test helper; new tests call it at start.

---

## v1.0.444 → v1.0.462-alpha — 2026-05-09

Observability work — ADR-022 + insights phases 1 + 2. Closed the
"how much have I spent / is the hub OK / where in the lifecycle is
this project" gap that the v1.0.440 device test surfaced.

### Added
- **`/v1/hub/stats` endpoint** (v1.0.444, ADR-022 D2). Hub-self
  observability: machine block (OS / CPU / RAM / kernel), DB block
  (per-table rows + bytes via `dbstat` virtual table when available,
  schema_version, WAL size), live block (active agents, open
  sessions, SSE subscribers). 30s row-count cache. Mobile renders a
  Hub group at the top of the Hosts tab + a fullscreen Hub Detail
  screen.
- **`/v1/insights` endpoint** with project scope (v1.0.449, ADR-022
  D3). Tier-1 dimensions: spend (tokens in/out, cache read/create),
  latency (p50/p95 of `turn.result.duration_ms`, linear
  interpolation), errors (failed turns, open attention),
  concurrency (active agents, open sessions, turns/min). Token
  rollups via `by_engine` + `by_model`. 30s response cache.
  Migration `0036_agent_events_project_id` adds `project_id` column
  + composite `(project_id, ts)` index + AFTER INSERT trigger that
  stamps from `sessions(scope_kind='project')` so the seven
  existing INSERT call sites stay untouched.
- **A2A relay throughput** in `/v1/hub/stats` (v1.0.456). 30s × 1s
  rolling window for aggregate + per-destination bytes/sec; `Begin`
  / `Record` / `Dropped` instrumentation on `handleRelay`. Mobile
  Hub Detail gains an A2A RELAY section.
- **`/v1/insights` multi-scope** — project / team / agent / engine /
  host (v1.0.457). Each scope has its own per-table SQL fragment in
  `insights_scope.go`. user_id parked: ADR-005's principal/director
  model has no users table at MVP. Mobile gains a typed
  `InsightsScope` value object; `getInsights` takes named-arg scope
  params and throws synchronously on >1.
- **Fullscreen `InsightsScreen`** (v1.0.458, ADR-022 D7). Activity
  tab AppBar gains an Insights icon that opens the screen with
  project scope (when project filter is set) or team scope.
- **Me tab Stats card** (v1.0.459). Today's tokens + Δ% vs prior 7d
  average via two-window read; tap → fullscreen team-scoped
  Insights.
- **Agent Detail Insights tab** + **Host Detail Insights button**
  (v1.0.460). Agent Detail's existing 3-tab controller grew to 4
  (embedded panel for agent-scoped tiles); Host Detail gained an
  Insights button that pops the sheet and pushes the fullscreen
  view scoped to host.
- **Tier-2 drilldowns** on `InsightsScreen` (v1.0.461 + v1.0.462):
  - Engine + model breakdown — share bars, tokens/turn ratio,
    sorted by tokens descending.
  - Multi-host distribution — per-host agent count + capability
    fingerprint; hides on degenerate scopes / single-host.
  - Tool-call efficiency — `tools` block (tool_calls excluding
    streaming `tool_call_update`, tools/turn, approval rate from
    `EXISTS json_each(decisions_json) → approve` walk). Mobile
    color-codes the rate (green ≥85%, warning ≥50%, error
    otherwise).
  - Lifecycle flow (project scope only) — `lifecycle` block with
    phase timeline (trailing phase runs to `now()`), ratification
    rate, criterion pass-rate, stuck count from
    `acceptance_criteria.state='failed'`. Mobile renders a
    timeline with a current-phase dot + rate bars + inline warning
    when stuck > 0.

### Changed
- **`agent_events` schema** — `project_id TEXT` column added by
  migration `0036`; existing rows backfilled from
  `sessions(scope_kind='project')`. New events stamped via AFTER
  INSERT trigger so the seven existing INSERT call sites need no
  edits.

### Deprecated / Deferred
- **W5e unit economics** ($/session, $/deliverable, $/attention)
  needs a pricing table (token×$ per model). ADR-022 marks pricing
  post-MVP; current token-based metrics are the MVP proxy.
- **W5f snippet usage telemetry** needs new instrumentation — the
  action bar fires the `snippet` action without emitting an event.
- **W6 p95 alert + materialized rollup** — fires on production load
  that doesn't exist yet; the trigger-deferred design *is* the
  design. Reopen when first real deployment crosses the p95 > 1s
  threshold.

### Documents
- **ADR-022** observability surfaces — locked 7 design decisions
  (Activity ≠ Insights, hub stats is purpose-built, scope-
  parameterized insights, agent_events.project_id column,
  rollups post-MVP, cache-first per ADR-006, six entry points + one
  fullscreen view).
- `plans/insights-phase-1.md` flipped to **Done**.
- `plans/insights-phase-2.md` flipped to **Done — MVP scope**;
  W5e/W5f/W6 marked deferred post-MVP with rationale.

### Lessons (architectural)
- Scope filter `SessionsClause` must prefix columns with `s.` so
  the same fragment slots into a JOIN with `attention_items`
  (which also has `scope_kind`/`scope_id`) without ambiguity.
- Time-bucket rate windows: `>=` cutoff vs `>` matters — `>` clips
  to 29s and biases the rate ~3% low.
- `SUM(CASE …)` returns NULL on zero rows; always wrap in
  `COALESCE` when scanning into a fixed Go type.
- `*Type` + `omitempty` on optional response fields — keeps the
  contract explicit ("the field IS sometimes absent") rather than
  emitting zeroed structs that look like real data.
- Dispatcher loops calling methods that block on the remote peer
  must run those calls in goroutines — synchronous dispatch
  deadlocks when the next event is what would unblock the previous
  one. (See v1.0.454 `InputRouter` fix; `feedback_input_router_dispatch_async.md`.)
- For `attention_items` decision walking, `EXISTS (SELECT 1 FROM
  json_each(...) WHERE json_extract(value, '$.decision') =
  'approve')` is the readable SQLite idiom — much cleaner than
  LIKE-substring or `$[#-1]` indexing.

---

## v1.0.443-alpha — 2026-05-09

### Changed
- **`tool_call_update` and non-`end_turn` `turn.result` now visible by
  default in the transcript** (correcting the v1.0.442 verbose-only
  approach). Reasoning: v1.0.442 hid both kinds behind the debug
  toggle, but the user reported they expected these wire frames in
  the normal log so they could trace approval-flow state. New rules:
  - `tool_call_update` shows standalone only when its parent
    `tool_call` is hidden by a gate (`request_approval`,
    `request_select`, `request_help`, `request_decision`,
    `permission_prompt`) — that's the case where the standalone card
    is the only place to see the wire result. For non-gated tools
    the update keeps folding into the parent card to avoid
    duplicating the latest status pill.
  - `turn.result` shows when `stop_reason != end_turn`. Cancelled /
    error / max-token / refused turns become inline cards (e.g. the
    cancelled in-flight prompt that gets replaced by an
    attention_reply). Clean `end_turn` boundaries stay silent so
    every reply doesn't add a "turn ended" card.
- **`input.attention_reply` card now leads with the rendered prompt
  text the agent received** (e.g. `[reply to approval_request
  01KR5CT6] Approved.`), not just the structured decision fields.
  Mobile ports `formatAttentionReplyText` (Go: `driver_stdio.go`) as
  `renderAttentionReplyText` (Dart) so the transcript matches
  exactly what the engine sees on the wire. Cross-language contract
  pinned by `attention_reply_render_test.dart` (Dart) +
  `TestFormatAttentionReplyText` (Go) — same input table, same
  expected outputs.

---

## v1.0.442-alpha — 2026-05-09

### Fixed
- **`input.attention_reply` rendered as raw JSON in the transcript.**
  The widget had handlers for `input.text` / `input.cancel` /
  `input.approval` but fell through to `_jsonPretty` on the
  attention-reply kind, so the principal's decision card read like a
  config dump. New `_inputAttentionReplyBody` shows decision / kind /
  option_id / reply / reason / request_id as a clean key-value
  block.

### Changed
- **`tool_call_update` and `turn.result` demoted from unconditional
  hide → verbose-gated.** They still drive folding (parent tool_call
  card status pill) and the telemetry strip, but the wire frames
  themselves are now revealed by the top-right debug chip alongside
  the existing `lifecycle / raw / system` reveals. This makes the
  request_approval gate's tool_call_update inspectable (it carries
  the attention_id + severity payload the inline approval card was
  built from) and surfaces the orphan `stopReason=cancelled` frame
  that the driver's attention_reply path produces by design. Verbose
  chip tooltip updated to "wire frames" so the surface is
  discoverable. Also added compact renderers for both kinds
  (`_toolCallUpdateBody`, `_turnResultBody`).

---

## v1.0.441-alpha — 2026-05-09

### Fixed
- **Cancel-button overlay stuck on after gemini ACP approval flow
  ended.** The ACP driver's `attention_reply` Input writes a fresh
  `session/prompt` with the rendered approval text — the only way to
  push a turn-based decision back to gemini-cli. That new prompt
  cancels the in-flight prompt that originally raised the attention
  (gemini replies `stopReason=cancelled` on the old id), and the new
  prompt then runs to `end_turn` normally. The bug: the
  `attention_reply` branch in `driver_acp.go` discarded the new
  prompt's response and never called `postTurnResult`, so mobile
  never saw the live `end_turn`. The orphan cancel + the streaming
  `partial:true` chunks left the busy walker glued to "turn in
  progress" and the cancel-button overlay stayed on long after the
  agent's final reply. Driver now mirrors the `text` branch:
  `postTurnResult(res)` after a successful attention-reply prompt.
  Test extended to assert the `turn.result(end_turn)` event is
  posted.

### Added
- **Batch ops on the Sessions list.** Long-press a session tile (or
  use the AppBar "Select…" menu) to enter multi-select mode. The
  AppBar swaps to a count badge + Select-all / Cancel actions; tiles
  render checkboxes; a bottom action bar exposes Archive (gated to
  archive-eligible rows) and Delete (gated to all-archived rows; hub
  refuses non-archived deletes). `SessionsNotifier.bulkArchive` /
  `bulkDelete` run sequentially with a single refresh at the end, and
  return per-id failures so the SnackBar can summarise instead of
  bursting one toast per failure.

### Changed
- **Mode/Model picker moved from inline strip → AppBar icon.** The
  ADR-021 W2.5 chip strip used to render above every transcript,
  costing a row of vertical real estate even on engines that never
  re-advertise mode/model after handshake. The picker now hangs off
  the SessionChatScreen AppBar as a single `tune` icon — tap opens
  one bottom-sheet showing both Mode and Model sections (whichever
  the agent advertised). Tooltip surfaces the current values so the
  current state is still glanceable without opening the sheet.
  `AgentFeed` exposes the picker payload via a new
  `onModeModelChanged` callback; the inline `_ModeModelStrip` is
  retired.

---

## v1.0.440-alpha — 2026-05-09

### Fixed
- **Image-attach button always hidden on mobile (ADR-021 Phase 4
  reachability bug).** AgentCompose's `_resolveImageAttachAffordance`
  read `agent['driving_mode']` from the `getAgent` response, but the
  hub serialises that field as `mode` (see `agentOut.Mode` in
  `hub/internal/server/handlers_agents.go`). Field mismatch made
  `drivingMode` always null → `resolveCanAttachImages` defaulted to
  `M4` → all families have `prompt_image['M4'] == false` → button
  hidden for every agent regardless of capability. Compose now reads
  `agent['mode'] ?? agent['driving_mode']` so the gate works on
  current hubs and stays forward-compat if a future payload reverts
  the field name.
- **Mode/Model picker chips never showed for ACP agents on cold
  start.** gemini-cli (and the ACP spec) returns the available
  mode/model lists *and* the current ids in the `session/new` /
  `session/load` response — NOT as `current_mode_update` /
  `current_model_update` notifications. The driver cached the lists
  locally for `set_mode` / `set_model` validation but never surfaced
  them to mobile, so `modeModelStateFromEvents` had nothing to walk
  and `_ModeModelStrip` rendered empty. Driver now emits a synthetic
  `kind=system, producer=system` event after handshake with the
  top-level `currentModeId / availableModes / currentModelId /
  availableModels` shape (matches the runtime notification path so
  the mobile reducer joins both transparently).
- **Stream-dropped banner appeared on idle network drops.** dart:io
  surfaces `HttpException: Connection closed before full body
  received` and similar on Android-doze / carrier-NAT-timeout /
  proxy-idle reaps — the SSE reconnect logic recovers transparently,
  but the banner pushed noise to a user with nothing to act on. The
  reconnect path's banner gate now suppresses the well-known idle
  signatures (`connection closed`, `connection reset`,
  `connection abort`, `connection terminated`, `before full body
  received`, `stream closed`); genuine connectivity loss
  (network-unreachable / DNS) still surfaces the banner.

---

## v1.0.439-alpha — 2026-05-09

### Fixed
- **ACP driver missing `attention_reply` Input handler.** When the
  principal approved a `request_approval` MCP attention on mobile,
  the hub's `/decide` resolved the DB row and posted
  `input.attention_reply` correctly, but the ACPDriver's `Input`
  switch had no case for `attention_reply` — the InputRouter call
  fell through to the default arm and returned `unsupported input
  kind "attention_reply"`. The agent's wake-up turn never reached
  gemini-cli, so the principal saw their decision card in the feed
  but the agent stayed idle waiting for them. Driver now mirrors
  the stdio + exec-resume pattern: render the structured payload
  via `formatAttentionReplyText` and dispatch as a fresh
  `session/prompt`. ACP needs none of the parked-JSON-RPC branch
  the codex appserver carries — `permission_prompt` on this driver
  goes through the dedicated `Input("approval")` path that responds
  on the original `session/request_permission` RPC.

### Added
- **Per-card fold/collapse toggle on every transcript card.**
  AgentEventCard gains a chevron in the header (next to the copy
  affordance) that collapses the card to a single-line preview;
  the whole header row is also a tap target so thumbs don't have
  to aim. Default is expanded for every kind so the existing
  transcript shape is unchanged on first render. Previously only
  `tool_call` and `tool_result` had built-in collapse behaviour;
  thoughts, approval-request cards, plans, diffs, system rows
  etc. now share the same affordance. Preview text reuses
  `_copyTextFor`'s output so what you see when collapsed is what
  you'd get on copy.

---

## v1.0.438-alpha — 2026-05-09

### Fixed
- **Duplicate transcript bubbles after gemini-cli M1 resume.** A
  resumed session showed the previous turn's `agent_thought_chunk`
  and `agent_message_chunk` rendered a second time in the feed,
  duplicating cached content. Root cause: the M1 driver's
  `replayActive` window closed the moment the `session/load` response
  arrived, but gemini-cli@0.41.2 emits the final burst of historical
  `session/update` notifications AFTER the response (last turn's
  trailing chunks land ~50µs to ~100ms after the load reply, on the
  same connection). Those trailing frames went out without
  `replay: true`, so mobile's W1.3 dedupe couldn't recognize them as
  already-cached and rendered them as live. Fix keeps the replay
  window open until the operator's first `Input()` (text / cancel /
  approval / attach / set_mode / set_model) — autonomous agent
  emissions in the gap between `session/load` and a user action are
  by definition either historical replay (deduped on content key) or
  capability-state (`available_commands_update`,
  `current_mode_update`, `current_model_update` — already routed
  through the no-replay-tag system path). Updates
  `TestACPDriver_TagsReplayEvents` to cover the trailing-history and
  post-Input cases.

---

## v1.0.437-alpha — 2026-05-09

### Fixed
- **ACP approval card rendered as already-cancelled on resume.** A
  `session/request_permission` arriving on a freshly-resumed gemini-cli
  M1 session showed `decided: cancel` before the user tapped anything.
  Root cause: the driver surfaced the agent's raw JSON-RPC `id` as the
  externally-visible `request_id` in the `approval_request` agent_event.
  Each spawn of the agent (including resume after pause) restarts
  gemini's outbound id counter from a low number, so id=`0` from the
  current spawn collided with id=`0` from a previous spawn that had
  been cancelled. Mobile's `resolvedApprovals` map is keyed by
  `request_id` and persists across spawns via the agent_events history;
  the colliding id made the new card render as already-decided. Fix
  namespaces the externally-visible `request_id` with a per-spawn
  nonce + monotonic counter (`<UnixNano>-<n>`), keeping the agent's
  raw JSON-RPC `id` only in the internal `pendingPerm` map so we still
  respond on the correct RPC. Codex and claude were unaffected
  (codex uses hub-generated `attention.ID`, claude uses
  Anthropic-generated `tool_use_id`s — both globally unique by
  construction). Regression test
  `TestACPDriver_PermissionRequestIDUniquePerSpawn` asserts two
  consecutive spawns each emitting id=`0` produce distinct request_ids.

---

## v1.0.435-alpha — 2026-05-08

### Added
- **Mobile image-attach UI (ADR-021 W4.6).** Closes Phase 4 of the
  ACP capability surface plan. AgentCompose gains an attach button
  (paperclip / image icon) gated on `family.prompt_image[mode]`:
  - claude-code on M1/M2 → engaged
  - codex on M1/M2 → engaged
  - gemini-cli on M1 (--acp) → engaged
  - gemini-cli on M2 (exec-per-turn) → hidden (the driver-side
    W4.5 strip-and-warn is a fallback for forwarded payloads, not
    an invitation to send them)
  Tap → image_picker → ImageConverter (1024px max edge / 70% JPEG)
  → base64 → enqueued on the composer. Up to 3 thumbnails render
  in a horizontal strip above the text field with × removal taps.
  Pre-flight cap matches the hub W4.1 validator (5 MiB decoded).
  On send, the queued images ride alongside the text body in
  `postAgentInput(images: …)`. Body becomes optional when at least
  one image is queued — image-only turns are first-class. Family
  registry endpoint now serves `prompt_image` and
  `runtime_mode_switch` so the mobile gate has the same view the
  hub does. Top-level `resolveCanAttachImages` helper exposed via
  `@visibleForTesting` for the gate-decision unit test.

---

## v1.0.434-alpha — 2026-05-08

### Added
- **gemini-exec image strip + warn (ADR-021 W4.5).** ExecResumeDriver
  now intercepts `payload["images"]` on text input. gemini's
  exec-per-turn argv (`gemini -p "<text>"`) has no inline-image
  affordance, so the driver:
  - emits a `kind=system` event with engine=`gemini-exec`,
    reason="…no inline image support — switch to gemini --acp (M1)
    for multimodal turns…", dropped=count, so the principal sees
    why the attachment didn't reach the model and what to do
    about it,
  - lets the text portion proceed normally as `gemini -p <body>`.
  Image-only inputs (no body) emit the warning and then return the
  existing missing-body error so the principal isn't left thinking
  silence = success.

---

## v1.0.433-alpha — 2026-05-08

### Added
- **ACP image content blocks (ADR-021 W4.4).** ACPDriver's `text`
  Input branch now lowers `payload["images"]` entries to ACP shape
  `{type:"image", mimeType, data}` and leads them in the
  `session/prompt.params.prompt` array; the text block (if any)
  trails. promptCapabilities.image is now lifted from the agent's
  `initialize` response into a tri-state cache: absent → permitted
  (forward-compat with agents that omit the field), explicit
  `false` → strip + emit a `kind=system` warning event, explicit
  `true` → forward as-is. When images are stripped and there's no
  body left, the call returns a typed error so the operator
  notices instead of dispatching an empty turn. Image-only inputs
  (no body) are accepted when capability allows.

---

## v1.0.432-alpha — 2026-05-08

### Added
- **Codex image content blocks (ADR-021 W4.3).** AppServerDriver's
  `startTurn` now takes an `images []imageInput` arg and lowers
  each entry to OpenAI responses-API shape
  `{type:"input_image", image_url:"data:<mime>;base64,<b64>"}`.
  Image blocks lead the `turn/start.params.input` array; the
  `{type:"text"}` block (if any) trails so the model sees the
  imagery before the question. Image-only inputs (no body)
  produce a single image block. attention_reply path passes
  `nil` images — replies remain text-only by design.

---

## v1.0.431-alpha — 2026-05-08

### Added
- **Claude image content blocks (ADR-021 W4.2).** StdioDriver's
  `buildStreamJSONInputFrame` text branch now produces a content
  array. Image inputs from `payload["images"]` lower to Anthropic's
  stream-json shape `{type:"image", source:{type:"base64",
  media_type, data}}` and lead the array; the text block (if any)
  comes last so the model reads the question after seeing the
  imagery. Image-only inputs (no body) are accepted. Hub-side
  validation (W4.1) already enforced mime/size/count caps so the
  driver trusts the payload shape. Shared
  `extractImageInputs(payload)` helper extracted to
  `image_inputs.go` so W4.3 (codex) and W4.4 (ACP) reuse the same
  type-assertion ladder.

---

## v1.0.430-alpha — 2026-05-08

### Added
- **Hub input contract for `images: []` (ADR-021 W4.1).** Opens Phase 4
  of the ACP capability surface plan. `POST /agents/{id}/input` accepts
  an optional `images: [{mime_type, data}]` array alongside `body`.
  Validation: mime allowlist (`image/png` / `image/jpeg` / `image/webp`
  / `image/gif`), well-formed base64, ≤5 MiB decoded per image, ≤3
  images per request. Caps are the lower bound across our engines so
  any accepted payload is acceptable to every content-array driver.
  Plumbed onto `payload_json["images"]` verbatim — per-driver shape
  mapping (Anthropic image_source / OpenAI input_image / ACP
  prompt-array) lands in W4.2–W4.4. Drivers that don't know about
  images ignore the field, so text-only turns remain backward-
  compatible. UI surface (composer attach branch) lands in W4.6.
  HubClient gains an `images` named parameter on `postAgentInput`;
  consumers don't yet send it.

---

## v1.0.424-alpha — 2026-05-08

### Added
- **Mobile mode + model picker UI (ADR-021 W2.5).** Closes Phase 2 of
  the ACP capability surface plan. AgentFeed now renders a small
  ActionChip strip above the message list when the active agent has
  advertised mode and/or model state via system notifications
  (`currentModeId` / `availableModes` / `currentModelId` /
  `availableModels`). Tap → bottom-sheet picker → `postAgentInput`
  with `set_mode` / `set_model`. The wire payload is engine-neutral;
  the hub's `runtime_mode_switch` table (W2.1) routes per-driver:
  gemini M1 RPC → instant; claude/codex respawn → ~3-5s with the
  transcript intact via the engine_session_id resume cursor; gemini
  exec-per-turn → applies on the next prompt.

### Changed
- `HubClient.postAgentInput` gains `modeId` / `modelId` named
  parameters mirroring the new hub input contract.

## v1.0.423-alpha — 2026-05-08

### Added
- **NextTurnMode / NextTurnModel for gemini-exec (ADR-021 W2.4).**
  Lights up the `per_turn_argv` route declared by W2.1. ExecResumeDriver
  gains `Input("set_mode")` / `Input("set_model")` cases that stash the
  override on `nextTurnMode` / `nextTurnModel`; the next `runTurn`
  consumes the slot and splices `--approval-mode <id>` / `--model <id>`
  into argv. One-shot semantics by design (sticky behavior is a
  follow-up wedge): an absent override falls through to the rendered
  cmd's existing flags. When the mode override fires, the legacy
  `--yolo` flag is suppressed for that turn so `--approval-mode` wins.

## v1.0.422-alpha — 2026-05-08

### Added
- **Respawn-with-mutated-spec for claude/codex (ADR-021 W2.3).** Lights
  up the `respawn` route declared by W2.1. New helper
  `respawnWithSpecMutation` reads the active session's
  `spawn_spec_yaml`, surgically swaps the per-engine flag (claude:
  `--model` / `--permission-mode`; codex: `--model` / `--approval-policy`)
  via a yaml.v3 Node-API mutator that preserves all other fields
  byte-for-byte, splices the engine_session_id resume cursor (ADR-014
  for claude, W1.2 for ACP), enqueues a host-runner terminate, and
  calls `DoSpawn` with the existing `SessionID` so the prior agent is
  swapped inside one tx. Transcript continuity rides on the session
  row; the picker selection lands as a fresh `--model` argv on the
  new pane.
- New `mutateBackendCmdFlag(specYAML, flag, newValue)` returns
  `errFlagNotInCmd` when the rendered cmd doesn't carry the target
  flag — surfaced as 422 by the input handler so mobile shows
  "this template doesn't expose <flag>" rather than a silent no-op.

### Changed
- `POST /agents/{id}/input` `set_mode`/`set_model` on a respawn-route
  family no longer returns 501; happy path now responds 202 and lands
  a real respawn. Failure modes map to typed 422s
  (`errUnknownFamilyField`, `errFlagNotInCmd`).

## v1.0.421-alpha — 2026-05-08

### Added
- **ACP `session/set_mode` + `session/set_model` driver dispatch
  (ADR-021 W2.2).** ACPDriver caches the agent's `availableModes` /
  `availableModels` id sets at session/new (and session/load) time
  and exposes two new Input kinds — `set_mode { mode_id }` and
  `set_model { model_id }`. Each validates the requested id against
  the cached set before dispatching the matching ACP RPC, so a typo
  fails locally without burning a round trip. An agent that didn't
  advertise modes/models at handshake gets a typed
  "did not advertise modes/models" error rather than a silent no-op.
  W2.1's hub routing already emits these as input.set_mode /
  input.set_model events for gemini-cli M1 (route=rpc); the driver
  picks them up via the existing InputRouter polling loop.

## v1.0.420-alpha — 2026-05-08

### Added
- **`runtime_mode_switch` family declaration + hub routing (ADR-021
  W2.1).** Opens Phase 2 of the ACP capability surface plan. Each
  `agent_families.yaml` entry declares one of `rpc | respawn |
  per_turn_argv | unsupported` per driving_mode (M1/M2/M4) — keyed by
  mode rather than per-family because gemini-cli supports both M1
  (rpc) and M2 exec-per-turn (per_turn_argv) and a single string
  couldn't disambiguate. `POST /agents/{id}/input` accepts new kinds
  `set_mode` (with `mode_id`) and `set_model` (with `model_id`); the
  handler resolves `(family, driving_mode)` against the
  runtime_mode_switch table and dispatches: rpc/per_turn_argv → emit
  input event for driver pickup (handlers ship in W2.2/W2.4);
  respawn → call `respawnWithSpecMutation` helper (stub returns 501
  until W2.3 lands the real string-edit + pause/spawn orchestration);
  unsupported → 422. Mobile sends one shape; only the wire path
  varies per engine.
- **Family declarations:** `claude-code` = respawn (M1 + M2);
  `gemini-cli` = rpc (M1) / per_turn_argv (M2); `codex` = respawn
  (M1 + M2). M4 is unsupported across the board (tmux pane scrape
  has no model concept).

### Changed
- `agentfamilies.Family` gains a `runtime_mode_switch map[string]string`
  field; mirrored on the wire shape `AgentFamilyFromHub` so probe
  sweeps see the same declaration the hub-server consults.

## v1.0.413-alpha — 2026-05-08

### Added
- **ACP `authenticate` after `initialize` (ADR-021 W1.4).** Closes
  Phase 1 of the ACP capability surface plan. ACPDriver now lifts
  `authMethods` from the initialize response and, when non-empty,
  dispatches `authenticate(methodId=...)` before `session/new` /
  `session/load`. Selection precedence: explicit
  `SpawnSpec.AuthMethod` (steward template) → family default
  (`agent_families.yaml`'s `default_auth_method`) → first
  non-interactive method in the agent's advertised list. Empty
  `authMethods` is treated as pre-authenticated and skipped.
- **`gemini-cli` family default = `oauth-personal`.** Targets the
  single-user-developer case (`gemini auth` once on the host caches
  tokens at `~/.gemini/oauth_creds.json`; the daemon reuses them
  without opening a browser). Service-account / shared-host
  deployments override via `auth_method: gemini-api-key` in the
  steward template.
- **`attention_request` agent_event for auth failures.** When
  authenticate returns rpc-error, only-interactive methods are
  available with no preference, an explicit `auth_method` doesn't
  match the agent's advertised list, or the call hits
  `AuthTimeout` (default 30s), the driver emits a typed
  `attention_request` event with `kind: auth_required`,
  the configured method, the available method options, and a
  remediation hint, then fails Start. Surface for principal-level
  resolution (run `gemini auth`, set `GEMINI_API_KEY`, or override
  the steward template) without silent infinite hangs.

### Tests
- 6 new ACPDriver tests cover: skip when no methods, explicit
  preference wins, first-non-interactive fallback, attention on
  interactive-only, attention on rpc failure, attention on
  preference-not-in-advertised-list typo.
- 2 new launch_m1 tests pin the resolution precedence (spec
  override beats family default; family default applies when spec
  is empty).

---

## v1.0.412-alpha — 2026-05-08

### Added
- **Mobile dedupe for `replay:true` events (ADR-021 W1.3).** First
  APK-touching wedge of the ACP capability surface plan. The
  AgentFeed renderer now filters incoming SSE events flagged
  `replay: true` by `agentEventReplayKey`, dropping any whose
  content-stable key matches an event already in the cached
  transcript. Without this, a session/load resume re-renders every
  prior turn under the new agent's stream, doubling the visible
  transcript. Keys are content-based (text body, tool_call_id,
  request_id) because hub-side ids and seqs differ between the
  dead agent's original event and the resumed agent's replay.
- **`agentEventReplayKey` + `agentEventIsReplay` helpers** — exported
  via `@visibleForTesting` so the dedupe contract has a unit-test
  pin (`test/widgets/agent_feed_replay_dedupe_test.dart`). Keying
  by kind: text/thought → length-prefixed body; tool_call →
  tool_call_id; tool_call_update → tool_call_id + status;
  approval_request → request_id. Other kinds (raw, lifecycle,
  system, plan, diff) pass through replay unchanged — better
  to duplicate than to drop on a fragile match.

---

## v1.0.411-alpha — 2026-05-08

### Added
- **ACP `session/load` on respawn (ADR-021 W1.2).** When the hub
  resumes a gemini-cli session that has a captured engine cursor,
  it now injects `resume_session_id: <id>` into the rendered
  `spawn_spec_yaml`. `SpawnSpec.ResumeSessionID` plumbs the value
  through `launch_m1.go` to `ACPDriver.ResumeSessionID`. On
  handshake, the driver caches `agentCapabilities.loadSession`
  from the `initialize` response; when both the cursor is set AND
  the agent advertises load support, it calls `session/load`
  instead of `session/new`. On load failure (stale cursor, agent
  doesn't actually implement the method), the driver logs a
  warning and falls back to `session/new` so the operator still
  gets a session — fresh, but usable.
- **Replay event tagging.** Session/update notifications streamed
  by the agent during `session/load` (the historical-turn replay)
  are tagged `replay: true` in their event payloads via the new
  `tagIfReplay` helper. Live notifications after Start completes
  are unaffected. Mobile-side dedupe (W1.3) consumes this flag.
- **`spliceACPResume` helper.** Sibling to `spliceClaudeResume` —
  yaml.v3-Node-based top-level field injection so the cursor
  flows through the same template-derived YAML pipeline as
  claude's `--resume` cmd splice. Defensive: empty cursor →
  no-op, idempotent, replaces a stale prior id.

### Tests
- 4 new ACPDriver tests cover load-when-capable, fallback when
  loadSession unsupported, fallback on rpc-error, and replay
  tagging round-trip.
- 4 new `spliceACPResume` shape tests + 1 end-to-end resume test
  (`TestSessions_ResumeThreadsACPCursor`) pin the gemini-cli
  resume path mirror of the claude resume pin.

---

## v1.0.410-alpha — 2026-05-08

### Added
- **ACP `session.init` event for engine-side cursor capture
  (ADR-021 W1.1).** `ACPDriver.Start()` now emits a dedicated
  `session.init` agent event with `producer=agent` after the ACP
  `session/new` handshake completes. The hub's engine-neutral
  `captureEngineSessionID` (gate: `kind=session.init &&
  producer=agent`) lifts the gemini sessionId into
  `sessions.engine_session_id` — same column claude already uses
  per ADR-014. No migration; column existed since 0033. This is
  the prerequisite for W1.2 (`session/load` on respawn): without
  the cursor in the database, there is nothing to splice on
  resume. Tests cover the driver-side emission and the hub-side
  capture for `kind=gemini-cli` agents.

---

## v1.0.349-alpha+1 — 2026-04-30 (docs/tooling, no app rebuild)

### Added
- **Glossary** ([`docs/reference/glossary.md`](reference/glossary.md))
  — canonical defs for every project-specific term that has more
  than one possible meaning. ~50 entries across 11 domains
  (Sessions, Agents, Engines, Hosts, Events, Attention, UI,
  Protocols, Storage, Process). Each entry has a one-line def, an
  optional *Distinguish from:* line, and a link to its canonical
  concept doc. §12 indexes the "easy to confuse with" pairs for
  fast disambiguation. Trigger: 200K LOC of accumulated drift +
  the 2026-04-30 claude-code resume bug, which surfaced because
  *session* meant two different things in two adjacent layers and
  nothing pinned the boundary.
- **doc-spec §7 — term-consistency contract.** Codifies the rules:
  first-use linking to glossary, no new term without an entry in
  the same commit, qualifier required when ambiguous. CI lint
  enforces #1 and #2; #3 is review discipline.
- **CI lint** (`scripts/lint-glossary.sh`). Four checks: glossary
  structure (no orphan headings), §12 index integrity, spelling-
  variant drift detection across all docs (with code-context
  filtering so `hub/internal/hostrunner` package paths don't
  false-flag), and a warning-level new-term gate. Wired into
  `.github/workflows/ci.yml` alongside the existing
  `lint-docs.sh`.
- **PR template** gains a "Term consistency" section pointing at
  the glossary contract and the local lint command.
- **Tester / end-user UI guide**
  ([`docs/how-to/report-an-issue.md`](how-to/report-an-issue.md))
  — bug-report template + annotated ASCII layouts of every major
  screen + UI vocabulary (AppBar, BottomNav, BottomSheet, Card,
  Chip, ListTile, FAB, TabBar, …) + verb glossary (tap vs
  long-press vs swipe) + common confusion points (Resume vs Fork,
  agent vs engine, status chip colours). Parallel artifact to the
  engineering glossary, audience: testers and normal users.

### Changed
- **doc-spec.md** restructured: §7 is the new term-consistency
  contract; §8 (was §7) is the contract for new docs; §9 (was §8)
  lists CI lints; §10/§11 (open questions / references)
  renumbered.
- **Two real prose drift fixes** caught by the new lint:
  `host runner` → `host-runner` in
  `discussions/transcript-ux-comparison.md` and
  `plans/agent-state-and-identity.md`.
- **`discussions/transcript-source-of-truth.md`** status block
  forwarded to ADR-014 (the operation-log framing this discussion
  rests on); broken auto-memory cross-link replaced with a memory
  reference (not a doc link).
- **`docs/README.md`** index gains pointers to glossary +
  report-an-issue.

---

## v1.0.349-alpha — 2026-04-30

### Fixed
- **Claude-code resume actually resumes** ([ADR-014](decisions/014-claude-code-resume-cursor.md)).
  Pre-v1.0.349, tapping Resume on a paused claude-code session
  spawned a fresh engine session every time — same hub transcript
  window, brand-new claude conversation cursor. The CLI flag exists
  (`claude --resume <session_id>`); the hub just never threaded it.
  Surfaced from device-test feedback on v1.0.348-alpha.

  Three pieces, one wedge:
  - **Migration `0033`** adds `sessions.engine_session_id TEXT`.
    Engine-neutral column — claude calls it `session_id`, gemini
    calls it `session_id`, codex calls it `threadId`; all three
    can land their cursors here as their capture paths get wired.
  - **Capture path** (`captureEngineSessionID` in
    `handlers_sessions.go`). The `POST /agents/{id}/events`
    handler watches for `kind=session.init && producer=agent`
    frames, lifts `payload.session_id` from claude's stream-json
    `system/init` (already extracted by `StdioDriver.legacyTranslate`
    at `driver_stdio.go:295`), and `UPDATE`s the live session row.
    Best-effort — capture failure can't fail the event insert; the
    worst case is a cold-start resume, the pre-ADR-014 baseline.
    `kind=text` events that happen to carry session_id are
    explicitly ignored, as are `producer=user` echoes.
  - **Splice path** (`spliceClaudeResume` in `resume_splice.go`).
    `handleResumeSession` reads `engine_session_id` alongside
    `spawn_spec_yaml`. When the dead agent's `kind=claude-code`
    and a cursor exists, the helper walks the spec's yaml.v3 node
    tree to `backend.cmd`, strips any prior `--resume <other>`
    pair, and splices `--resume <id>` directly after the `claude`
    binary token. The handler passes the rewritten spec to
    `DoSpawn` but never `UPDATE`s `sessions.spawn_spec_yaml`, so
    successive resumes always splice from a clean cmd.

  Codex (`AppServerDriver.ResumeThreadID`) and gemini
  (`ExecResumeDriver.SetResumeSessionID`) already have the
  driver-side resume plumbing; both are still waiting on hub-side
  capture paths to feed them. Tracked as ADR-014 OQ-1 / OQ-2.

  11 resume-cursor tests: 7 splice unit tests (basic shape,
  idempotence, prior-id replacement, non-claude passthrough, empty
  inputs, malformed yaml, missing key, absolute path bin) + 3
  capture + 2 end-to-end resume tests proving
  `agent_spawns.spawn_spec_yaml` carries `--resume <id>` after a
  warm resume and stays clean after a cold one + 1 fork guard
  (`TestSessions_ForkDoesNotInheritEngineSessionID`) pinning the
  fork-is-cold-start invariant so a future "helpfully" inheriting
  change fails loudly at CI rather than mid-conversation.

### Added (continued)
- **Hub transcript is the operation log** ([ADR-014](decisions/014-claude-code-resume-cursor.md) OQ-4 input-side).
  The three engines all ship interactive commands that mutate
  engine-side context without emitting any frame back: claude's
  `/compact` `/clear` `/rewind`, gemini's `/compress` `/clear`. The
  engine's view of the conversation silently diverges from the
  hub's `agent_events` log — same `engine_session_id`, smaller or
  differently-shaped context. Without observability the operator
  scrolls back through what *looks* like a continuous transcript
  and gets surprising agent answers grounded in a context that no
  longer matches what they're reading.

  v1.0.349 ships the input-side observable. The hub's input route
  watches `kind=text` bodies for a leading per-engine slash command
  and, on match, emits a follow-up typed `agent_event` row with
  `producer=system` and `kind ∈ {context.compacted, context.cleared,
  context.rewound}`. Mobile renders these as inline operation chips
  so the transcript reads "[user] /compact → [system] context
  compacted" — same hub session, same `engine_session_id`, but the
  marker pins where the engine view diverged.

  Per-engine vocabulary in
  `hub/internal/server/context_mutation.go`:
  - claude-code: `/compact`, `/clear`, `/rewind`
  - gemini-cli: `/compress`, `/clear`
  - codex: TBD — slash vocabulary not yet audited; emission is a
    no-op until ADR-014 OQ-4b lands

  Engine-*emitted* mutations (e.g. claude's auto-compact when the
  context window fills) still aren't observable — those need the
  engine's stream to surface the event, which is option α deferred
  in `discussions/fork-and-engine-context-mutations.md`.

  10 new tests: 5 detector unit tests (per-engine vocab, leading-
  slash discipline, case sensitivity, unknown-engine no-op) + 5
  end-to-end input-route tests proving the marker lands at
  `seq=N+1` after the input.text row, that plain text emits no
  marker, that non-text input kinds (answer, etc.) skip the
  detector even when their body looks slash-y, and that codex
  agents stay silent until their vocabulary is audited.

### Changed
- **ADR-014 expanded** with the fork-is-cold-start section, the
  hub-vs-engine session boundary (cursor inheritance forbidden),
  and four open questions for follow-up wedges:
  OQ-1 codex `threadId` capture, OQ-2 gemini cross-restart cursor
  feeder, OQ-3 reconcile-driven respawn, **OQ-4 engine-side
  context mutations** (claude `/compact` `/clear` `/rewind`,
  gemini `/compress` — the hub today doesn't observe these and
  the engine's view of the conversation drifts from the hub's
  `agent_events` log without any marker frame), and OQ-5 fork
  productisation. Cross-linked to a new
  [`discussions/fork-and-engine-context-mutations.md`](discussions/fork-and-engine-context-mutations.md)
  that maps the design space across both axes (fork carryover +
  mutation observability) for the next wedge to start from.
- **`docs/decisions/README.md`** index gains rows for ADR-013 and
  ADR-014 — the prior wedge's index update was missed in v1.0.348.

---

## v1.0.348-alpha — 2026-04-29

### Added
- **Gemini integration via exec-per-turn-with-resume** ([ADR-013](decisions/013-gemini-exec-per-turn.md)).
  Third engine alongside claude-code (M2 stream-json) and codex
  (M2 app-server JSON-RPC). gemini-cli has no `app-server`
  equivalent, but headless mode now emits a stable `session_id`
  (PR [#14504](https://github.com/google-gemini/gemini-cli/pull/14504),
  Dec 2025) and accepts `--resume <UUID>` for cross-process session
  continuity. Wedge shipped as slices 1-6, all in this release:
  - **Slice 1:** ADR-013 written; ADR-011 D6 + ADR-012 D6 cross-link
    the per-engine `permission_prompt` matrix.
  - **Slice 2:** gemini-cli frame profile in `agent_families.yaml`
    — top-level `type`-keyed dispatch (init/message/tool_use/
    tool_result/error/result) into the same typed agent_event
    vocabulary claude/codex emit. M2 added to supports. No
    evaluator extension needed (unlike codex's dotted-path
    matchesAll).
  - **Slice 3:** `driver_exec_resume.go` is the spawn-per-turn
    driver. Captures `session_id` from the first `init` event,
    threads `--resume <UUID>` through every subsequent argv;
    `SetResumeSessionID` seeds the cursor on host-runner restart.
    `launch_m2` short-circuits family=gemini-cli before the
    long-running spawn machinery — exec-per-turn doesn't anchor a
    pane (PaneID=""), the bin is resolved via `exec.LookPath`, and
    a `CommandBuilder` injection seam keeps tests off real exec.
  - **Slice 4:** `permission_prompt` is unsupported on gemini
    (ADR-013 D4 — gemini has no in-stream approval gate). Driver
    rejects `attention_reply` with `kind=permission_prompt` as a
    defense-in-depth check. Reference + discussion docs grew the
    per-engine matrix (Claude sync, Codex turn-based, Gemini
    unsupported). Stewards self-route through `request_approval`.
  - **Slice 5:** per-family MCP config materializer adds
    `<workdir>/.gemini/settings.json` (JSON, stdio command+env shape
    matching claude's `.mcp.json` — gemini-cli's `mcpServers`
    schema accepts it identically). 0o600 inside .gemini/ 0o700.
    No CODEX_HOME-style env trick needed; gemini reads project-
    scoped settings.json automatically.
  - **Slice 6:** `agents.steward.gemini.v1` template + prompt ship
    in the embedded fs. Spawn cmd is bin-only (`gemini`) — the
    driver appends `-p <text> --output-format stream-json
    --resume <UUID> --yolo` per turn, ADR-013 D7. Prompt grows a
    "Decisions that need approval" section since gemini has no
    engine-side gate.

  15 new tests cover every wire-format contract: 7 driver tests
  (first-turn argv, second-turn --resume threading, rehydration,
  Stop interrupting in-flight Wait, permission_prompt rejection,
  nil CommandBuilder), 4 MCP-config tests (wire shape, escapes,
  perms, dispatcher branch isolation), 3 frame-profile tests
  (corpus, payload fields, embedded), 1 embedded-template test.
  Slice 7 (cross-vendor `request_help` smoke against live codex +
  live gemini binaries) remains unfunded and gated on a test host
  with both binaries installed — same gate as ADR-012 slice 7.

### Changed
- **Roadmap "Now" gains the gemini wedge** as Done; verifying on
  device next. The "Next" entry "Gemini exec-per-turn driver"
  collapses into the cross-vendor smoke (slice 7 × 2) — codex and
  gemini share the integration-smoke gate.

---

## v1.0.347-alpha — 2026-04-29

### Added
- **Codex integration via app-server JSON-RPC** ([ADR-012](decisions/012-codex-app-server-integration.md)).
  Codex CLI joins claude-code as a first-class engine; the hub
  drives `codex app-server --listen stdio://` over a long-lived
  JSON-RPC pipe rather than `codex exec --json` per turn. Wedge
  shipped as slices 1-6:
  - **Slice 2 (v1.0.343):** frame profile in `agent_families.yaml`
    translates app-server's thread/turn/item lifecycle plus
    telemetry into the same typed agent_event vocabulary
    claude uses. `matchesAll` grew dotted-path support
    (`params.item.type: agentMessage`) for one-method-many-types
    dispatch.
  - **Slice 3 (v1.0.344):** `driver_appserver.go` is the JSON-RPC
    client + thread manager. Handshake is initialize → initialized
    notification → thread/start (or thread/resume <id>); Input(text)
    maps to turn/start; the Driver interface is the launch_m2
    return type so codex and claude both fit.
  - **Slice 4 (v1.0.345):** approval bridge. Codex's
    `item/commandExecution/requestApproval` and siblings POST an
    `attention_items` row (kind=permission_prompt) and park the
    JSON-RPC request id locally; `dispatchAttentionReply` fires for
    permission_prompt too, and the driver's `Input("attention_reply")`
    looks up the parked id and writes the per-method JSON-RPC
    response on /decide resolution. Vendor-neutral equivalent of
    Claude's permission_prompt without the canUseTool sync limit.
  - **Slice 5 (v1.0.346):** per-family MCP config materializer.
    Claude keeps `.mcp.json`; codex writes `.codex/config.toml`
    (TOML, hand-formatted, no library dep). Token at 0o600.
  - **Slice 6 (v1.0.347):** `agents.steward.codex.v1` template +
    prompt ship in the embedded fs. Spawn cmd
    `CODEX_HOME=.codex codex app-server --listen stdio://` bypasses
    codex's trusted-projects gate.
- **Decision history on Me page.** Clock icon opens recent resolved
  attentions; tap into one to see the per-decision audit trail
  (timestamp, decider, verdict, reason/body/option) on the detail
  screen.

### Changed
- **Permission_prompt is now per-engine, not per-architecture.**
  Sync on Claude (canUseTool contract); turn-based on Codex
  (app-server deferrable JSON-RPC). ADR-011 D6's
  bridge-mediated-stdio post-MVP wedge is now Claude-only by
  construction (ADR-012 D7).
- **Me filter chip "Approvals" → "Requests"** since the bucket
  spans approval_request, select, help_request, template_proposal —
  none of which are pure approve/deny.

### Fixed
- **Resume preserves transcript.** Stopping an active session and
  resuming it minted a new agent and the chat opened empty — the
  list/SSE endpoints AND'd `agent_id = ?` even when `session=<id>`
  was provided. Now session=<id> scopes by session_id (with team
  auth), orders by ts, and the mobile feed dedupes by event id +
  paginates with a new `before_ts` cursor since per-agent seq is
  unusable as a cross-agent total order.
- **Stream-dropped banner on idle close cycles.** SSE onDone with
  no error is an idle artifact (proxy keepalive, mobile carrier),
  not a real drop. Banner now fires only on onError.
- **Rate-limit countdown rendering "1540333567h"** when Anthropic
  shipped resetsAt as a microsecond-precision integer. Unit
  heuristic now handles seconds / ms / µs / ns plus a 7-day
  sanity bound so any future unit confusion drops the tile.

## v1.0.338-alpha — 2026-04-29

### Changed
- request_approval / request_select / request_help converted from
  long-poll to turn-based delivery. The MCP call now returns
  immediately with `{id, status: "awaiting_response"}`; the agent
  ends its turn per the updated tool description. The principal's
  reply lands as a fresh user turn (`input.attention_reply` agent
  event, `producer="user"`) when /decide resolves the attention.
  Removes the 10-minute timeout, the connection-pinned wait, and
  the failure mode where a reply 12 minutes after the question was
  silently dropped. Persistence moves from the open HTTP connection
  to the conversation history — a 3-day-later reply still wakes
  the agent. permission_prompt is unchanged: it stays sync because
  Claude's canUseTool protocol has no "deferred" branch (vendor
  contract limitation, not a design choice).
- handleDecideAttention fans out the resolution to the originating
  agent via a new `dispatchAttentionReply` helper. Target lookup is
  attention.session_id → sessions.current_agent_id; if the session
  was resumed since the request was raised, the new agent (which
  inherits the conversation context) receives the reply. Best-
  effort: a fan-out hiccup doesn't roll back the /decide.
- StdioDriver gains a new input kind `attention_reply` that produces
  a user-text turn (NOT a tool_result, since the original tool call
  has already returned). Format per attention kind:
    approval → "Approved" / "Rejected. Reason: <reason>"
    select   → "Selected: <option>"
    help     → "<body>" verbatim or "Dismissed without reply"
  Short correlation prefix `[reply to <kind> <id-prefix>]` so the
  agent can match replies to multiple in-flight requests.
- `agent_input` HTTP handler accepts the new `attention_reply` kind
  for completeness (so an operator can wake an agent from CLI in a
  pinch); server-side fan-out from /decide is the primary producer.

### Removed
- `requestSelectTimeout` and `requestHelpTimeout` constants (10
  minutes each). No replacement — turn-based delivery has no time
  bound.
- The long-poll branches and timeout-handling code in mcpRequestSelect
  and mcpRequestHelp.

### Tests
- TestRequestHelp_ReturnsAwaitingResponseImmediately: pins the
  synchronous return contract (1s upper bound, fail-fast on a long-
  poll regression).
- TestDecide_HelpRequestFansOutAttentionReply: end-to-end — agent
  asks → user decides → input.attention_reply event posted to the
  agent with the principal's body verbatim.
- TestMCP_RequestSelect_TurnBasedRoundTrip: replaces the prior
  `_StoresOptionsAndLongPolls` test; covers the new return shape +
  decide behavior.
- TestStdioDriver_InputFrames: 3 new subtests for attention_reply
  formatting (help_request approve, select approve, approval_request
  reject).

### Docs
- docs/reference/attention-kinds.md §5 rewritten as
  "Resolution semantics — turn-based delivery" with a worked round-
  trip diagram, per-kind /decide payloads, per-kind user-turn text
  format, and a "Why turn-based, not long-poll" rationale section.
  permission_prompt called out as the principled exception.

## v1.0.337-alpha — 2026-04-29

### Added
- "Open project" button on the approval-detail Origin section, next to
  "Open in chat". Visible when the attention has a project pointer
  (project_id column or scope_kind='project' + scope_id). Routes to
  ProjectDetailScreen using the cached project row from hub state.
- Scroll-to-event-id on session chat: SessionChatScreen + AgentFeed
  gain an `initialSeq` parameter. After the cold-open backfill, the
  feed scrolls to and briefly highlights (2px primary-tinted border,
  ~1.2s) the event whose seq matches. Used by approval-detail's
  "Open in chat" button so the principal lands at the agent's turn
  that raised the request, not at the generic tail.
  Implementation: GlobalKey on the matched AgentEventCard +
  Scrollable.ensureVisible — works with non-uniform row heights
  without a positioned-list dependency. Falls back to tail scroll
  when the seq isn't in the loaded page (older than 200 newest).
  Auto tail-follow disables on a successful jump so subsequent SSE
  events don't yank the user back to the bottom mid-read.
- Host info on host detail: OS, arch, kernel, CPU count, total
  memory, hostname now render as named rows on the host detail
  sheet (Hosts tab → tap host). Sourced from a new
  `capabilities.host` field on the host-runner capabilities sweep.
  Host-runner probes once at startup (ProbeHostInfo) and re-attaches
  the cached pointer to every push so a hub mobile session always
  sees the static facts even if the runner restarted in the middle.
  Linux reads /proc/meminfo MemTotal; Darwin reads `sysctl hw.memsize`;
  kernel via `uname -r` on both. Memory rendered in GiB
  (10 GiB → "10 GiB", 0.5 GiB → "512 MiB"). Replaces the previous
  raw-JSON dump that wasn't readable in practice.
- Capabilities row on host detail rewritten as "Engines" with
  installed family + version joined by `·` (e.g.
  "claude-code 1.0.27 · codex 0.5.1"). Missing engines hidden so
  the sheet doesn't list every supported engine just to say "no".
- Tests: TestProbeHostInfo_PopulatesStaticFields pins OS/arch/CPU
  population and asserts memory is non-zero on Linux/Darwin where
  the probe path is reachable.

### Changed
- HostInfo struct embedded in Capabilities is JSON-optional
  (`omitempty`) for back-compat — old runners (pre-v1.0.337) emit no
  host field and the renderer hides those rows rather than showing
  unknowns.

## v1.0.336-alpha — 2026-04-29

### Added
- Approval detail screen now renders origin context: agent + session
  pointers ("Open in chat" jumps directly to the originating session's
  transcript), the last 10 transcript turns leading up to the request
  (filtered by session_id, capped by attention.created_at), and
  inline action controls that mirror the Me-page card. Resolving from
  the detail screen pops back to the Me page since the row drops off
  the open list.
- Server: request_approval / request_select / request_help all stamp
  attention_items.session_id at insert time via new
  Server.lookupAgentSession helper. Empty for system-originated
  attentions (budget, spawn approval) and pre-v1.0.336 rows; the
  detail screen degrades gracefully to a metadata-only view.
- New endpoint: GET /v1/teams/{team}/attention/{id}/context returns
  {session_id, agent_id, agent_handle, events: [...]} with newest-
  first transcript turns. Two tests pin the contract — full round
  trip from request_help and the no-session-pointer fallback.
- attentionOut now carries session_id; the list endpoint exposes it
  to mobile so the Me-page card can pre-decide whether the detail
  screen will have anything to render.

### Changed
- Inline action widgets (InlineApprovalActions, InlineHelpRequestActions)
  extracted from me_screen.dart to lib/screens/me/inline_actions.dart
  so the approval detail screen can reuse them without a circular
  import. Both gain an optional onResolved callback so the detail
  screen can pop after a successful decide; the Me-page card leaves
  it null and lets the row drop out of the open list on its own.
- approval_detail_screen.dart rewritten as a ConsumerStatefulWidget
  that fetches context on mount; the apologetic "actions will land
  here in a follow-up" footer is gone — actions are inline.

## v1.0.335-alpha — 2026-04-29

### Added
- New `help_request` attention kind — the third interaction shape,
  complementing `approval_request` (binary) and `select` (n-ary).
  Used when the agent needs free-text input from the principal:
  clarification, direction, opinion, or hand-back ("I'm stuck, take
  over"). MCP tool `request_help` parallels `request_approval` and
  `request_select`; payload carries `question`, optional `context`
  (agent's framing), and `mode` (`clarify` | `handoff`). The decide
  endpoint now accepts a `body` field; an approve on a help_request
  without a body is rejected (400) since the principal's reply *is*
  the answer. Long-poll surfaces the body to the agent verbatim,
  same shape as `request_select`'s option_id flow.
- `docs/reference/attention-kinds.md` — canonical authoring guide
  for picking between the three kinds. Decision tree by
  answer-space cardinality, anti-pattern table with what to use
  instead, worked examples for clarify and handoff modes. The MCP
  tool docstring on `request_help` carries the short form;
  contributors and AI agent maintainers consult this doc for the
  long form. Linked from `hub-agents.md`.
- Mobile `_HelpRequestActions` widget on the Me page renders a
  free-text composer (Send / Skip) when a help_request attention
  appears in the approvals list. Mode chip ("clarify" / "hand-back")
  surfaces the agent's framing; agent's `context` shows above the
  composer. The approval-detail screen footer copy is now
  kind-aware so it doesn't mislead help_request users with
  "Approve / Deny" instructions.

### Changed
- `request_select` is now explicitly tracked in `tiers.go` as
  `TierRoutine` (was relying on the `request_decision` alias entry).

## v1.0.334-alpha — 2026-04-29

### Fixed
- Steward auth tokens now revoke when the agent terminates. Each
  spawn mints a `kind='agent'` row in `auth_tokens` (the bearer the
  agent uses for `/mcp/{token}`); previously no path revoked it, so
  every spawn → terminate cycle left a still-valid token row, and
  pause/resume compounded it (one resume = one fresh token + one
  orphaned-but-live token). New `auth.RevokeAgentTokens(ctx, exec,
  agentID, now)` helper accepts either `*sql.DB` or `*sql.Tx`; called
  from `handlePatchAgent` when status flips to terminated/failed/
  crashed (covers UI terminate, host-runner ack, and the
  `shutdown_self` MCP path which lands here via host-runner) and
  from `handleSpawn`'s session-swap branch in the same tx so a
  rolled-back swap also rolls back the revoke. Idempotent on the
  `revoked_at IS NULL` clause.
- Mobile Auth screen (`tokens_screen.dart`) hides agent-kind rows.
  They're machine-issued + machine-revoked; surfacing them invited
  the operator to revoke a live agent's bearer (which would just
  look like a crash). The "New token" dialog also drops the `agent`
  kind chip — there's no human-issuance flow for agent tokens.

## v1.0.333-alpha — 2026-04-29

### Added
- ADR-010 Phase 1.6: `frame_translator` flag wired end-to-end. New
  `Family.FrameTranslator` field in `agent_families.yaml` selects
  the per-engine translator: `""` / `"legacy"` (default; today's
  hardcoded `legacyTranslate`), `"profile"` (data-driven
  `ApplyProfile` authoritative, legacy not invoked), `"both"`
  (profile authoritative + legacy in shadow with divergence logged
  via slog). Schema sidecar carries the enum so editor LSPs catch
  typos.
- Driver dispatch refactor: `StdioDriver.translate()` is now a
  3-way switch on `FrameTranslator`; the existing translator body
  moved verbatim into `legacyTranslate` and is reachable from both
  the default path and the "both" shadow run. `launch_m2.go`
  populates `FrameTranslator` + `FrameProfile` from the family
  registry at driver construction.
- `profile_diff.go`: extracted `DiffEvents` + `ParityIgnoreFields`
  + `capturingPoster` from the parity test into shared production
  code so the runtime "both"-mode divergence logging and the test
  parity diff use the same machinery and respect the same known-gap
  list. Misconfig (FrameTranslator set, FrameProfile nil) falls
  through to legacy with a warning rather than silently dropping
  events.
- 5 mode-dispatch tests: legacy default, profile-only, both with
  parity-clean frame (no warning), both with synthetic mismatched
  profile (warning fires with diff details), profile-mode misconfig
  fallback.

### Status
- ADR-010 Phase 1 is complete. The data-driven translator is
  shipped, parity-tested, flag-controllable, and dark by default.
  Phase 2 (canary → flip default → delete legacy) starts when the
  operator flips claude-code's `frame_translator: both` in their
  hub deploy and runs for a release window without divergence
  warnings.

## v1.0.332-alpha — 2026-04-29

### Added
- ADR-010 Phase 1.5: parity-test harness + seed corpus.
  `profile_parity_test.go` runs every frame in
  `testdata/profiles/claude-code/corpus.jsonl` through both
  translators (the legacy hardcoded `translate()` and the new
  data-driven `ApplyProfile`) and diffs the resulting agent_events
  by `(kind, producer, payload)`. Diff output is rule-level and
  agent-readable: which frame, which event index, which payload
  field, and what the legacy/profile values were. 13-frame seed
  corpus exercises every translate() branch (system.init / 3
  rate_limit shapes / task subtypes / assistant text+tool / user
  tool_result / result / error / unknown raw fallback).
- Grammar extension: `payload_expr: <expr>` for whole-payload
  passthrough. Used when the legacy translator emits the raw frame
  as payload (system fallback, error, deprecated completion alias)
  — three rules in the claude-code profile now use it. Mutually
  exclusive with `payload`; documented in
  `docs/reference/frame-profiles.md` §4 and the JSON Schema sidecar.
- `HUB_STREAM_DEBUG_DIR` env var: when set, the StdioDriver tees
  every raw stream-json line to `<dir>/<agent_id>.jsonl`. Operators
  use this to grow the corpus from real claude-code traffic — run
  the agent, copy interesting frames into the testdata directory,
  re-run the parity test.

### Changed
- Two known-gap fields documented as deliberate parity skips
  rather than profile bugs:
    - `by_model` — legacy normalizeTurnResult renames inner
      camelCase keys (inputTokens → input, etc.); v1 grammar has
      no map-iter construct.
    - `overage_disabled` — legacy derives a bool from
      `reason != nil`; v1 grammar has no bool-from-nullable
      predicate. Mobile reads `reason` directly.
  Adding to `parityIgnoreFields` is a deliberate policy decision;
  reviewers should read the comment before extending.

### Status
- ADR-010 Phase 1 is feature-complete (1.1 schema, 1.2 evaluator,
  1.3 translator, 1.4 profile + agent-readability artifacts, 1.5
  parity harness). Phase 1.6 (frame_translator flag) and Phase 2
  (canary → flip default) remain. Profile-driven translation is
  still dark — the legacy translator owns production traffic until
  the flag wires up.

## v1.0.331-alpha — 2026-04-29

### Removed
- `aider` retired from supported engines. Project decision: only
  cover dominant-vendor products (Anthropic claude-code, OpenAI
  codex, Google gemini-cli). Aider is a small open-source project
  that doesn't justify the per-engine maintenance cost. Touched:
  `agent_families.yaml` (entry deleted), `modes/resolver.go`
  (AgentKind comment), `lib/screens/team/agent_families_screen.dart`
  (defaults list), `families_test.go` /
  `spawn_mode_test.go` / `resolver_test.go` (test inputs swapped to
  `codex` where the test exercised cross-engine resolver behavior),
  `driver_stdio.go` comment, plus docs (discussion, plan, reference,
  hub-agents.md, steward-ux-fixes.md). ADR-010 §Context kept its
  decision-time mention of aider per ADR-immutability convention.

## v1.0.330-alpha — 2026-04-29

### Added (still dark — profile authored but legacy translator owns traffic)
- `hub/internal/agentfamilies/agent_families.yaml`: canonical
  claude-code `frame_profile` block. ~10 rules covering session.init
  (with camelCase/snake_case coalesce), all three rate_limit_event
  shape variants (flat / system-subtype / nested rate_limit_info),
  the system fallback, assistant multi-emit (content blocks +
  when_present-gated usage), user.tool_result filter, result →
  turn.result + completion (deprecated alias), and error. Each rule
  carries an inline `# ` comment naming the SDK release it was
  authored for so AI maintainers extending later have the
  upstream-shape lineage.
- `docs/reference/frame-profiles.md`: the agent-facing authoring
  reference. Grammar in BNF, dispatch semantics, scope rules, three
  worked input→output examples (rate_limit shape collapse, assistant
  multi-emit, system subtype hierarchy), common pitfalls calling out
  divergences from JSONata-style expectations. ~250 lines.
- `hub/internal/agentfamilies/agent_families.schema.json`: JSON
  Schema sidecar so editor LSPs (and AI editors) get autocomplete +
  inline validation while authoring overlays. yaml-language-server
  comment in the YAML wires it up automatically.
- `FrameProfile.Description` field — agent-facing prose header that
  states dispatch semantics + scope conventions inline so a fresh
  maintainer reading rule 17 sees the model without grep'ing the
  implementation.
- 7 smoke tests against the embedded profile covering every rule
  surface; full corpus diff test arrives in Phase 1.5.

### Changed
- `docs/plans/frame-profiles-migration.md` Phase 1.4 expanded with
  the five agent-native deliverables (description / reference /
  schema / inline comments / validator). New project memory entry
  `feedback_agent_native_design.md` captures "agent-native is a
  design principle" as a durable lesson — applies beyond frame
  profiles to any future declarative surface (action bar profiles,
  templates, attention-item options).

### Known parity gap
- `result.modelUsage` inner-key renaming (camelCase → snake_case in
  the `by_model` payload). The v1 grammar has no map-iter construct;
  by_model passes through verbatim. Tracked for grammar extension in
  Phase 1.5 once the parity diff surfaces the real shape.

## v1.0.329-alpha — 2026-04-29

### Added (dark code — not yet wired into live driver)
- `hub/internal/agentfamilies`: extended `Family` struct with optional
  `FrameProfile` (ADR-010 schema). New types `FrameProfile`, `Rule`,
  `Emit`. YAML round-trip test locks the wire shape so a rename
  surfaces immediately. Embedded families ship without profiles in
  v1; `FrameProfile == nil` is the steady state until Phase 1.4
  authors the claude-code profile.
- `hub/internal/hostrunner/profile_eval`: new package implementing
  the hand-rolled expression subset (D2 of ADR-010). Grammar:
  `$.path`, `$.path[N]`, `$$.outer.path`, `"literal"`, and
  `a || b || "default"` coalesce. ~150 LoC, zero third-party deps,
  full test coverage of nil propagation / outer scope / array
  indexing / malformed input.
- `hub/internal/hostrunner/profile_translate.go`: `ApplyProfile`
  evaluates a profile against a frame and returns the emitted events.
  Most-specific-match-wins dispatch: an init frame fires only the
  `{type: system, subtype: init}` rule, not the generic `{type:
  system}` fallback. Rules tied for specificity all fire (assistant's
  per-block + usage rules co-fire). When-present gates on a
  non-nil expression; gated rules suppress emit but don't trigger
  the raw fallback. No-match → `kind=raw` verbatim (D5).

This wedge is the load-bearing infrastructure for plan
`docs/plans/frame-profiles-migration.md` Phase 1. Phases 1.4–1.6
(claude-code profile + parity corpus + flag wiring) remain.

## v1.0.328-alpha — 2026-04-29

### Added
- `lib/widgets/agent_feed.dart`: inline answer card for the
  `AskUserQuestion` tool. claude-code emits a tool_call whose input
  carries `questions[].options[]`; the card renders the question +
  options as buttons and ships the picked label back as a
  `tool_result` so the agent can continue. Previously the prompt
  silently timed out, leaving a stale "looks like the question
  prompt was canceled" reply in the transcript.
- `hub/internal/server/handlers_agent_input.go` + `driver_stdio.go`:
  new `answer` input kind. Carved off `approval` because the agent
  expects a clean reply string, not a "decision: note" tuple — the
  driver wraps `body` in a `tool_result` keyed by `request_id` and
  ships it on stdin.

### Fixed
- `hub/internal/hostrunner/driver_stdio.go`:
  `translateRateLimit` now peeks into `rate_limit_info` (and
  `rateLimitInfo`) before reading status/window/resets-at fields.
  Recent claude-code SDK builds nest the actual rate-limit values
  under that sub-object; with the flat lookup the mobile telemetry
  strip stayed empty (window/status/resets-at all nil) every time
  the agent shouted about quota. Three shapes are now handled in
  one path: top-level fields (legacy), `system.subtype=rate_limit_event`
  (mid-versions), and the nested `rate_limit_info` (current).
  Regression test: `TestStdioDriver_RateLimitEventNestedInfo`.
- `lib/widgets/agent_feed.dart`: SSE re-subscribe no longer pops
  "Stream dropped" the moment a *clean* close happens. A clean close
  (`onDone`) after the agent finished a turn is normal — proxy idle
  timeout, mobile-network keepalive cycle, app suspend — and the
  reconnect either gets immediate replay or sits idle waiting on the
  next event. Banner now fires only on real `onError`, or after
  three consecutive empty close cycles, so a finished transcript
  doesn't surface a phantom error.

## v1.0.327-alpha — 2026-04-29

### Fixed
- `hub/migrations/0032_sessions_heal_orphan_active.up.sql`: one-shot
  migration that flips orphan-active sessions to `paused`. Bad data
  accumulated when an agent died via a code path that didn't auto-
  pause its sessions (the auto-pause was added in v1.0.326 but only
  fires through PATCH /agents/{id} status=terminated). Without this
  heal, the device-walkthrough showed sessions in the Detached group
  with a green "active" pill even though the agent was long gone.
  Regression test: `TestSessions_HealOrphanActive`.
- `lib/screens/sessions/sessions_screen.dart`: the Detached sessions
  group now treats every member as Previous and renders any
  `status=active|open` row as `paused` for display. Same rationale as
  the migration — the engine these rows pointed to is gone, so a
  green pill misleads the user. The bucket also auto-expands now
  (instead of starting collapsed) since Previous is the only content
  there. The chat AppBar's Stop action drops out when the attached
  agent isn't live in `hubProvider.agents`, mirroring the list-row
  defensive override.
- `lib/providers/sessions_provider.dart`: `resume()` and `fork()` now
  also call `hubProvider.refreshAll()` so a freshly-spawned steward
  shows up in the cached agents list immediately. Without this, the
  resumed/forked session got bucketed into the Detached group on the
  next render — its `current_agent_id` pointed at an agent the cache
  hadn't seen yet — until the user pulled-to-refresh.

### Changed
- `lib/screens/sessions/sessions_screen.dart`: per-row session menu
  now exposes a status-appropriate terminal action — Stop (active),
  Archive (paused). Previously the only way to kill a session was
  via the chat AppBar's Stop, which forced the user to enter the
  conversation first; archiving a paused session had no surface at
  all. Existing rename / fork-from-archive / delete entries are
  unchanged.
- `lib/screens/sessions/sessions_screen.dart`: Detached group is now
  default-expanded; previously the user had to tap "previous (N)"
  to see what was inside, which was confusing because for that
  group the previous list IS the entire group.

## v1.0.326-alpha — 2026-04-28

### Fixed
- `hub/internal/hostrunner/egress_proxy.go`: rewrite `req.Host` to
  upstream's host in the reverse-proxy Director. Without this, the
  agent's local `127.0.0.1:41825` Host header was forwarded upstream;
  Cloudflare-fronted hubs returned 403 because that hostname isn't a
  known CF zone. Regression test added.
- `hub/internal/hostrunner/driver_stdio.go`: also dispatch
  `type=system,subtype=rate_limit_event` to the rate-limit
  translator. Recent claude-code SDK versions wrap the signal under a
  `system` envelope; without the subtype branch the event was
  passed through as kind=`system` and the mobile telemetry strip
  never saw a `rate_limit` kind. Both shapes now feed the same
  helper.
- `lib/screens/projects/projects_screen.dart`: drop the
  Project/Workspace bottom-sheet picker that fronted the create FAB.
  The kind toggle inside `ProjectCreateSheet` already covers the
  same choice via a SegmentedButton, so the pre-pick was a redundant
  extra tap.
- `lib/widgets/agent_feed.dart` `_systemBody`: render claude-code's
  `task_started` / `task_updated` / `task_notification` system
  subtypes as one-liners (e.g. `Task updated · is_backgrounded=true`)
  instead of dumping the full envelope JSON.
- `hub/internal/server/handlers_agents.go`: extend the auto-pause
  rule to `terminated`. Previously only `crashed` and `failed`
  flipped the matching active session to `paused`, so a user who
  tapped Stop session ended up with a dead agent but a session that
  still claimed to be active — the chat AppBar kept offering Stop
  and the sessions list kept the row in the active bucket. Per
  ADR-009 D6 / the documented Stop-session contract. Existing
  test renamed/extended to cover all three terminal statuses.

### Changed
- `lib/widgets/agent_feed.dart`: jump-to-tail pill is now always
  visible while the user is scrolled away from the bottom (not just
  when new events arrive) and surfaces the current scroll position
  as a percentage. Tool-call cards gained a fold chevron in the name
  row that collapses the body to just the name + status pill, so
  noisy multi-step calls don't dominate the transcript.
- `hub/internal/server/handlers_sessions.go` `handleForkSession`:
  fork no longer auto-attaches to the team's live steward. A
  running steward agent is bound to its own active session via a
  single stream-json connection; pointing a second active session
  at it would race events between the two and silently strand the
  older conversation mid-turn. Fork now always lands the new
  session as `paused` with `current_agent_id` NULL by default, and
  the app drives a spawn (or replace-into-session) into it. An
  explicit `agent_id` parameter is still honoured for callers
  that genuinely have a session-less steward, but the server
  rejects (409) if that agent already owns an active session.
  Tests reworked: `TestSessions_ForkAlwaysUnattachedByDefault`
  asserts the no-auto-attach contract, and
  `TestSessions_ForkRejectsBusyAgent` covers the explicit-but-busy
  guard.
- `lib/screens/sessions/sessions_screen.dart` `_forkSession`:
  always opens the spawn-steward sheet bound to the new session id
  on a successful fork response with empty agent_id (now the
  default path), then navigates into the chat once the spawn
  lands. Replaces the prior misleading "no live steward to attach
  the fork to" error and the silent dual-attach race.
- `lib/screens/sessions/sessions_screen.dart`: the synthetic
  "(no live steward)" group on the Sessions page is renamed to
  "Detached sessions" with a sub-line explaining why the bucket
  exists ("Original steward gone — open to read, fork to continue
  with a fresh one").
- `lib/services/hub/open_steward_session.dart`: when a scope is
  passed but no scope-matching session exists for the live
  steward, open one in that scope instead of silently falling back
  to the steward's general/team session. Fixes the "tap project
  steward chip → land in team/general" routing surprise.
- `lib/screens/team/spawn_steward_sheet.dart`: cap sheet height at
  85% of the screen and wrap the content in a SingleChildScrollView
  so the Cancel/Start row stays reachable on short phones.
- `lib/screens/me/me_screen.dart`: replace the "My work" project
  strip with an "Active sessions" strip — sessions are what the
  principal is actively in the middle of, while the Projects tab
  already covers full project navigation. Each tile shows session
  title + scope (General / Project: <name> / Approving) + steward
  name; tap pushes `SessionChatScreen`. Strip is hidden when no
  active sessions exist. New `meActiveSessionsSection` arb key
  (en + zh); legacy `meMyWorkSection` key removed since nothing
  else referenced it.
- `lib/screens/team/spawn_steward_sheet.dart` + rename dialog in
  `sessions_screen.dart`: relabel the field as **Name** and accept
  the bare domain (`research`, `infra-east`); the app appends the
  `-steward` suffix internally via `normalizeStewardHandle` before
  submitting. The user no longer has to know about the suffix
  convention. Helper text now spells out the uniqueness scope —
  unique among **live stewards on this team**; stopping a steward
  frees the name for reuse. Stale description text dropped its
  `#hub-meta` reference and the "one agent" framing now that
  multi-steward is shipped.

## v1.0.316-alpha — 2026-04-28

### Added
- `scripts/lint-docs.sh` — enforces doc-spec status block,
  resolved-discussion forward links, cross-reference resolution, and
  stale-doc warning (Layer 1 of the anti-drift design).
- `.github/workflows/codeql.yml` — security/quality scanning on push
  and weekly cron.
- `.github/dependabot.yml` — weekly dep-update PRs for Flutter pub +
  Go modules + GitHub Actions.
- `.github/pull_request_template.md` — PR checklist mirroring
  doc-spec §7.
- `docs/changelog.md` (this file) — Keep-a-Changelog format.

### Changed
- `doc-spec.md` §7: documents the three CI rules and DISCUSSION
  resolution accepting both ADR and plan links.

## v1.0.315-alpha — 2026-04-28

### Changed
- `spine/sessions.md`: 14 "Tentative:" markers walked individually,
  marked Resolved (with version where known) or Open. Reading note
  added.
- `spine/blueprint.md` §9: per-bullet status indicators (✅/🟡) +
  ADR cross-links.
- `spine/information-architecture.md` §11: 7 wedges marked ✅ shipped
  with version range; final paragraph rewritten as archaeology.

## v1.0.314-alpha — 2026-04-28

### Changed
- `reference/coding-conventions.md`: rewritten first-principles —
  links to upstream (Effective Dart, `analysis_options.yaml`) instead
  of duplicating; project-specific deltas only; each rule justified
  by the bug it prevents.

### Fixed
- Memory body drift: `user_physercoe.md` (fork name + retired dev
  machine), `project_research_demo_focus.md` (P4 status),
  `project_steward_workband.md` (sequence completed).

## v1.0.313-alpha — 2026-04-28

### Added
- Status blocks on every remaining doc (21 files). Every doc in
  `docs/` now declares Type / Status / Audience / Last-verified at
  the top.
- `reference/ui-guidelines.md` rewritten for Flutter (was
  pre-rebrand React Native).

### Changed
- H1s renamed to match filenames where they had drifted
  (`Wedge memo: Transcript / approvals / quick-actions UX` →
  `Transcript / approvals / quick-actions UX — competitive scan`,
  etc.).

## v1.0.312-alpha — 2026-04-28

### Added
- `reference/coding-conventions.md` rewritten for Flutter/Dart + Go
  (was pre-rebrand React Native).

### Changed
- 4 spine docs gain formal status blocks.
- 3 resolved discussions linked to their ADRs.

## v1.0.311-alpha — 2026-04-27

### Added
- 8 retroactive ADRs in `docs/decisions/` covering shipped decisions:
  Candidate-A lock, MCP consolidation, A2A relay, single-steward MVP,
  owner-authority model, cache-first cold start, MCP-vs-A2A protocol
  roles, orchestrator-worker slice.
- `decisions/README.md` indexes them.

## v1.0.310-alpha — 2026-04-27

### Changed
- 26 doc files reorganized into 7-primitive layout: spine/,
  reference/, how-to/, decisions/, plans/, discussions/, tutorials/,
  archive/.
- Renames per naming spec: `ia-redesign.md` →
  `information-architecture.md`, `agent-harness.md` →
  `agent-lifecycle.md`, `steward-sessions.md` → `sessions.md`,
  `vocab-audit.md` → `vocabulary.md`, `hub-host-setup.md` →
  `install-host-runner.md`, `hub-mobile-test.md` →
  `install-hub-server.md`, `release-test-plan.md` →
  `release-testing.md`, `mock-demo-walkthrough.md` →
  `run-the-demo.md`, `monolith-refactor-plan.md` →
  `monolith-refactor.md`, `wedges/` → `plans/`.
- `spine/sessions.md` promoted out of DRAFT.

## v1.0.309-alpha — 2026-04-27

### Added
- `docs/README.md` — navigation index.
- `docs/roadmap.md` — vision + phases + Now/Next/Later.
- `docs/doc-spec.md` — contract every doc honors (7 primitives,
  status block spec, naming spec, lifecycle rules).

## v1.0.308-alpha — 2026-04-27

### Changed
- Steward composer: cancel button surfaces whenever agent is busy
  (regardless of field content). Tooltip varies by content.

## v1.0.307-alpha — 2026-04-27

### Changed
- Steward composer: cancel only on text+busy (predictive-input flow).
  `isAgentBusy` plumbed from `AgentFeed` via event-stream scan.

## v1.0.306-alpha — 2026-04-27

### Changed
- Steward composer: collapsed cancel onto send slot via text-empty
  heuristic; bolt long-press = save-as-snippet (mirrors action-bar
  pattern).

## v1.0.305-alpha — 2026-04-27

### Added
- Read-through caches for `getAgent`, `getRun`, `getPlan` +
  `listPlanSteps`, `getReview`, `listAgentFamilies` — every detail
  screen serves last-known data from cache.

## v1.0.304-alpha — 2026-04-27

### Added
- Cache-first cold start: `_loadConfig` reads SQLite snapshots
  synchronously into `HubState`; UI lights up before network refresh
  resolves. Pairs with v1.0.303's `refreshAll` schedule. (ADR-006)

## v1.0.303-alpha — 2026-04-27

### Fixed
- Empty Projects/Me/Hosts/Agents on cold start: `HubNotifier.build()`
  now schedules `Future.microtask(refreshAll)` whenever
  `_loadConfig()` returns a configured state.

## v1.0.302-alpha — 2026-04-27

### Changed
- Documentation pass: agent-protocol-roles.md, hub-agents.md,
  research-demo-gaps.md, steward-ux-fixes.md updated to reflect
  v1.0.298 MCP consolidation + W-UI completion.

## v1.0.301-alpha — 2026-04-27

### Fixed
- Drop unused `_statusColor` (CI lint, was unreferenced after v1.0.299
  refactor).

## v1.0.300-alpha — 2026-04-27

### Changed
- Steward composer matched to action-bar composer: fontSize 14,
  maxHeight 120 (unbounded lines), inline clear button, save-as-snippet
  button.

## v1.0.299-alpha — 2026-04-27

### Added
- Steward chat polish: syntax-highlighted code blocks via
  `flutter_highlight`, color-coded diff view with line gutter,
  per-tool icons on `tool_call` cards.

## v1.0.298-alpha — 2026-04-27

### Changed
- Single MCP service: `mcp_authority.go` reuses the hubmcpserver
  catalog in-process via chi-router transport. One `hub-mcp-bridge`
  symlink, one `.mcp.json` entry. (ADR-002)

## v1.0.297-alpha — 2026-04-27

### Changed
- *(Superseded by v1.0.298.)* Wired `hub-mcp-server` into spawn
  `.mcp.json` via host-runner multicall pattern.

## v1.0.296-alpha — 2026-04-27

### Added
- SOTA orchestrator-worker slice: `agents.fanout`, `agents.gather`,
  `reports.post` MCP tools + steward template recipe + worker_report
  v1 schema. (ADR-008)
- Mobile: per-host agents view.

## v1.0.295-alpha — 2026-04-26

### Changed
- Renamed `request_decision` → `request_select` MCP tool with
  back-compat alias. Start-session path for orphaned stewards.

## v1.0.294-alpha — 2026-04-26

### Changed
- Hide MCP gate `tool_call` cards in transcript; remove standalone
  Close-session action (close = terminate).

## v1.0.293-alpha — 2026-04-26

### Added
- Cache sessions list + channel events for offline.

## v1.0.292-alpha — 2026-04-26

### Fixed
- Cache `recentAuditProvider` for offline activity feed.

## v1.0.291-alpha — 2026-04-26

### Added
- Multi-steward wedges 2+3: hosts sort + agent rename.

## v1.0.290-alpha — 2026-04-26

### Added
- Multi-steward wedge 1: handle-suffix convention (`*-steward`),
  auto-open-session on spawn, domain steward templates
  (`steward.research`, `steward.infra`).

## v1.0.286-alpha — 2026-04-26

### Added
- Egress proxy in host-runner: in-process reverse proxy masks the
  hub URL from spawned agents (`.mcp.json` carries
  `127.0.0.1:41825/`, not the public hub).

## v1.0.285-alpha — 2026-04-26

### Added
- Tail-first paginated transcripts.
- Hub backup/restore via `hub-server backup` / `hub-server restore`.

## v1.0.281-alpha — 2026-04-26

### Changed
- Replace-steward keeps the session: engine swap continues the
  conversation. Sessions are durable across respawn.

## v1.0.280-alpha — 2026-04-26

### Added
- Soft-delete sessions + UI; documented agent-identity binding.

---

## Earlier history

Major work units shipped before v1.0.280, summarized:

- **v1.0.200–203** — Artifacts primitive (§6.6 end-to-end). Outputs
  is the 4th axis (Files/Outputs/Documents/Assets).
- **v1.0.208** — Offline snapshot cache: HubSnapshotCache +
  read-through + mutation invalidation + Settings clear (5 wedges).
- **v1.0.175–182** — IA redesign: 7 wedges (nav skeleton, host
  unification, Me tab, Projects tab, Activity tab, Team switcher,
  Steward surface).
- **v1.0.166–167** — Activity feed foundation: audit_events as the
  activity log; mutations call recordAudit; MCP `get_audit` exposes it.
- **v1.0.157** — A2A relay + tunnel for NAT'd GPU hosts.
- **v1.0.151–156** — MCP tool surface expansion to close P4.4 audit:
  `schedules.*`, `tasks.*`, `channels.create`, `projects.update`,
  `hosts.update_ssh_hint`.
- **v1.0.141–148** — Trackio metric digest (storage + poller +
  mobile sparkline).
- **v1.0.49** — Audit log: `audit_events` table + REST + mobile screen.
- **v1.0.27** — Rebrand from MuxPod to termipod.
- **v1.0.18** — File manager (Settings > Browse Files).
- **v1.0.17** — Compose drafts (Save as Snippet → drafts category).
- **v1.0.2** — Data Export/Import via DataPortService.

For any version not listed above, `git log v1.0.X-alpha` and
`git show v1.0.X-alpha` (tag annotation) are authoritative.

---

## Conventions

- **One section per tagged release**, newest first.
- **Categories** (Keep a Changelog): Added · Changed · Fixed ·
  Deprecated · Removed · Security. Omit unused categories.
- **Cross-references**: link to ADRs (`ADR-NNN` or
  `decisions/NNN-name.md`) when a change implements a decision.
- **Patch-level entries**: bug-fix-cadence releases roll up; the
  changelog records substantive changes, not every tag.
- **Append at top**: new entries go above `## v1.0.316-alpha`.
- **Don't rewrite history**: changelog is append-only (modulo typo
  fixes). Past entries are the historical record.

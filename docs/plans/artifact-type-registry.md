---
name: Artifact type registry
description: Lock the artifact-kind set behind a closed registry — typed content blobs (tabular / image / pdf / canvas-app / …) grounded in adopted industry taxonomies, with per-kind viewer + editor + agent-producibility matrix. Orthogonal to transcript rendering (Tier 1) and to tile composition.
---

# Artifact type registry

> **Type:** plan
> **Status:** Open (2026-05-11)
> **Audience:** principal · contributors
> **Last verified vs code:** v1.0.484

**TL;DR.** `artifacts.kind` is schemaless today (migration 0019
comment lists `checkpoint` / `eval_curve` / `log` / `dataset` /
`report` as examples; the column accepts any string). That made
sense when artifacts were ML-run outputs and the agent set the
kind. With the steward now creating + binding artifacts as
load-bearing project entities (lit-review citations, ablation
tables, generated figures, paper PDFs), the lack of a closed set
shows up as duplicate tiles (References vs Documents), missing
viewers (no PDF), and no place to land multimodal IO (image /
audio / video both as user input and agent output). This plan
locks a closed artifact-kind set grounded in adopted taxonomies,
specifies the viewer + editor + agent-IO matrix per kind, and
explicitly excludes "how the transcript renders these" (that's
the [Tier 1 plan](agent-artifact-rendering-tier-1.md)) and
"where they surface on the project page" (that's the closed
`TileSlug` enum + per-phase override chain).

## Goal

Close the artifact-kind axis. After this plan ships:

- `artifacts.kind` is validated against a closed set (CHECK
  constraint OR Go-side enum + handler whitelist — pick in W1).
- Each kind has a defined `(mime, mobile viewer, mobile editor,
  agent-producible, agent-consumable)` row.
- The References-vs-Documents tile overlap is resolved by
  reclassifying lit-review citations as `tabular` artifacts (or
  a dedicated `citation` kind — open question Q1 below).
- Multimodal user input (image / audio / video upload via mobile)
  has a typed landing spot.
- A research-paper artifact (PDF) renders inside the app.

## Non-goals

- **Transcript rendering.** How agent-emitted content lands inline
  in chat is the Tier 1 plan's scope (`svg` / `html` fences).
  This plan is about *typed entities stored under `artifacts`* —
  separate axis.
- **Tile composition.** Which artifact-kinds appear under which
  tile is determined by per-phase template YAML +
  `phase_tile_overrides` (already shipped v1.0.484). This plan
  doesn't add or remove tiles.
- **Sandbox decisions for Tier 2 canvas.** The interactive
  `canvas-app` kind lands in this plan as a *type slot*; the
  actual WebView + sandbox security work is a separate Tier 2
  plan that depends on this one.
- **Lineage / versioning.** Existing artifacts primitive already
  has versioning hooks; this plan doesn't touch them.

## Industry grounding — what's adopted

Rather than invent the kind taxonomy, ground against six adopted
patterns and triangulate:

| Source | Kinds | Notes |
|---|---|---|
| Claude Artifacts (Anthropic, 2024+) | `text/markdown`, `application/vnd.ant.code`, `text/html`, `image/svg+xml`, `application/vnd.ant.mermaid`, `application/vnd.ant.react` | MIME-flavored. Two interactive (`html`, `react`); rest declarative. |
| ChatGPT Canvas (OpenAI, 2024+) | document (rich-text) + code (with run/preview) | Two-mode. Heavy on the editor surface (Canvas is the editor, not just renderer). |
| MCP Resource content types (Anthropic, 2025-spec) | `text`, `image`, `audio`, `blob` | Four primitives by IO modality. Clean and minimal; doesn't say *what kind* of image. |
| Notion blocks (industry baseline) | `paragraph`, `heading`, `table`, `database`, `image`, `video`, `audio`, `pdf`, `embed`, `file`, `code`, `bookmark`, `equation`, … (~30) | Comprehensive. Relevant slice = media + structured-data blocks. |
| VS Code custom editors / Jupyter MIME bundles | `text/html`, `image/png`, `application/json`, `text/vnd.plotly.v1+json`, `application/vnd.jupyter.widget-view+json` | Multi-MIME-per-cell precedent — one artifact, multiple renderable views. |
| Cursor / Composer | File-set bundles (multi-file code with tree) | Code-as-artifact must be multi-file aware. |

**Triangulation rule.** A kind earns inclusion if ≥3 of the six
sources have a direct analog. The MVP set below survives that
filter.

## Proposed closed set (MVP)

Eleven kinds. Each row is a defensible primitive grounded in the
table above.

| # | kind | mime pattern | viewer (mobile) | editor (mobile) | agent produces | agent consumes |
|---|---|---|---|---|---|---|
| 1 | `prose-document` | `text/markdown` | `flutter_markdown` + Tier-1 fence registry | `markdown_section_editor` (already shipped) | yes | yes |
| 2 | `code-bundle` | `application/vnd.termipod.code+zip` or directory | syntax-highlighted file tree (`flutter_highlight`) | read-only MVP; edit deferred | yes | yes |
| 3 | `tabular` | `text/csv` or `application/json` (rows+schema) | new `TabularViewer` widget — paginated rows + column filters | inline-cell edit deferred (post-MVP) | yes | yes |
| 4 | `image` | `image/png`, `image/jpeg`, `image/svg+xml`, `image/webp` | `Image.network` / `flutter_svg` (already in tree) | crop / annotate deferred | yes | yes (multimodal) |
| 5 | `audio` | `audio/mp3`, `audio/wav`, `audio/m4a` | new `AudioPlayer` widget (just_audio) | trim deferred | future | yes (multimodal STT) |
| 6 | `video` | `video/mp4`, `video/webm` | new `VideoPlayer` widget (video_player) | trim deferred | future | yes (multimodal) |
| 7 | `pdf` | `application/pdf` | new `PdfViewer` widget (pdfx or flutter_pdfview) | n/a (render-only) | yes | yes |
| 8 | `diagram` | `image/svg+xml` (mermaid → svg server-side) | `flutter_svg` | n/a (regen via agent) | yes | no |
| 9 | `canvas-app` | `text/html` (sandboxed bundle) | `webview_flutter` in sandboxed page route (Tier 2 plan) | edit-html deferred | yes (Tier 2 only) | no |
| 10 | `dataset-ref` | URI-only metadata, no upload | label + click-to-open chip | n/a (registration only) | yes | yes |
| 11 | `metric-chart` | `application/vnd.termipod.metrics+json` (with `schema` discriminator) | dispatch viewer: line / histogram / scatter / heatmap / roc / pr / calibration | n/a | yes | yes |

**Notes on the set:**

- **`prose-document` overlap with `documents` table.** This is
  intentional: a `prose-document` *artifact* is the "blob-stored,
  immutable, versioned" form (e.g. a draft snapshot at a phase
  ratification); a row in `documents` is the "single editable
  body" form. Same content, different lifecycle. Open question
  Q2 below.
- **`tabular` covers References.** Citations are rows
  `{author, year, title, doi, notes}` against a citation schema.
  No separate `citation` kind needed if `tabular` carries the
  schema. (See Q1.)
- **`metric-chart` covers multiple chart schemas via one kind.**
  Discriminator on the wire (`schema ∈ {line, histogram, scatter,
  heatmap, roc, pr, calibration}`); one mobile viewer dispatches
  on schema to the right CustomPaint renderer. Matches the W&B /
  TensorBoard "one metric entity, typed by `_type`" pattern.
  Distinct from `tabular` because the agent's intent is to *plot*,
  not to *list rows* — separate kind so the viewer doesn't have
  to guess intent from MIME.
- **`metric-chart` is for snapshots; live training metrics are NOT
  artifacts.** See "Live-vs-artifact two-layer split" below.
- **`canvas-app` is reserved.** Lands as the type slot in W1
  (registry entry + storage path); the actual Tier 2 viewer is a
  separate plan that depends on this one.
- **Out of MVP:** `bookmark` / `embed` (just URLs — handled by
  Notes / Documents today), `equation` (renders via existing
  `flutter_math_fork` inside markdown), `whiteboard` / `mind-map`
  (Tier 3 future, no triangulation evidence).

## Live-vs-artifact two-layer split

Surfaced 2026-05-11 while auditing whether the proposed kinds
cover the run/experiment visualizations seed-demo produces. They
don't fully — and the reason is architectural, not a missing kind.

Run-time experiment data lives in **two distinct layers**:

| Layer | Storage | Lifecycle | Visualization origin | Is it an artifact? |
|---|---|---|---|---|
| **Live training metrics** | `run_metrics`, `run_histograms`, `sweep_summary` tables (hub) | Streamed incrementally by the run feeder during training | **Derived view** computed at read time over the typed schema | ❌ No |
| **Run-produced files** | `artifacts` table (hub, URI + MIME + size) | Static — agent or run script emits a finalized blob | **Stored blob** with a typed kind, rendered by a kind-specific viewer | ✅ Yes |

Seed-demo creates **both**:

- `run_metrics` rows for live curves (rendered by `_MultiSparklinePainter`
  in `runs_screen.dart`) and `run_histograms` rows for per-step
  distributions (rendered by `HistogramSeriesTile`). These are
  the live layer — never artifacts.
- `eval_curve`-kind artifact rows for the *exported snapshot* —
  the final curve as a PNG/JSON the run "ships." These ARE
  artifacts.

**The `metric-chart` artifact-kind covers the second case only.**
The first case (live metrics) is intentionally not migrated to
artifacts — that would force snapshot-on-every-step writes and
break the incremental streaming model. The split mirrors W&B
(live API + stored files), TensorBoard (event-files + saved
images), and MLflow (metric API + artifacts API).

**Practical implication for tile composition.** A phase like
`experiment` surfaces *both* layers in different places: the
phase hero (`experiment_dash`) reads `run_metrics` directly for
the live overview; the Outputs tile lists snapshot artifacts.
Don't try to unify; the split IS the architecture.

**Open question Q-new (deferred):** should `run_metrics` /
`run_histograms` / `sweep_summary` ever migrate to typed
artifacts? Arguments for: one storage axis, queryable by the
artifact-kinds endpoint. Arguments against: live streaming
semantics, write-amplification, breaks incremental-aggregation
performance. Current answer is **no** — they stay as their own
typed tables. Revisit only if a concrete user story requires
cross-axis querying.

## Wedges

### W1 — Closed-set chassis

**Scope.** Lock the kind set in hub:

- Pick the validation mechanism (CHECK constraint vs Go-side
  whitelist — open question Q3).
- Migration `0038_artifacts_kind_check.up.sql` adds the
  constraint (or backfill ALTER).
- `hub/internal/server/handlers_artifacts.go` rejects unknown
  kinds at create time with `400`.
- Backfill existing rows: comment-listed values (`checkpoint`,
  `eval_curve`, `log`, `dataset`, `report`) map to MVP kinds:
  - `checkpoint` → `code-bundle` (model weights are a bundle,
    arguably need a new `binary-blob` kind — see Q4)
  - `eval_curve` → `metric-series`
  - `log` → `prose-document`
  - `dataset` → `dataset-ref` (assume URI; if uploaded, →
    `tabular`)
  - `report` → `prose-document`
- New mobile constant `lib/models/artifact_kinds.dart` mirrors
  the hub list (closed enum + label/icon table per kind).

**Files touched:**
- `hub/migrations/0038_artifacts_kind_check.{up,down}.sql` — new.
- `hub/internal/server/handlers_artifacts.go` — handler whitelist + 400 on miss.
- `hub/internal/server/handlers_artifacts_test.go` — happy + reject cases.
- `lib/models/artifact_kinds.dart` — new. Enum + spec table.
- `test/models/artifact_kinds_test.dart` — new.

**Test plan:**
- Create artifact with `kind=tabular` → 201.
- Create artifact with `kind=bogus` → 400.
- Existing rows with `kind=eval_curve` still readable post-migration
  (mapped to `metric-series` via backfill).
- Mobile `artifact_kinds.dart` enum exhaustively covers every
  hub-accepted value (test enforces by reading the hub list).

**LOC estimate:** ~250 mobile + ~150 hub + ~120 migration.

### W2 — PDF viewer

**Scope.** First new-kind viewer. Adds `pdfx` (or
`flutter_pdfview`) to `pubspec.yaml`; new `PdfViewer` widget;
artifact-detail page routes `pdf` kind to it. Picked first because
research-paper phase needs it AND no existing viewer covers it.

**Open question Q5:** which lib. `pdfx` is pure Dart + Skia
(consistent across iOS/Android, ~2 MB). `flutter_pdfview` wraps
native (smaller bundle, platform-divergent behavior). Pick before
scaffold.

**Files touched:**
- `pubspec.yaml` — add dep.
- `lib/widgets/artifact_viewers/pdf_viewer.dart` — new.
- `lib/screens/artifacts/artifact_detail_screen.dart` — kind →
  viewer dispatch (skeleton from W1).
- `test/widgets/pdf_viewer_test.dart` — render test with a small
  test-fixture PDF.

**LOC estimate:** ~200 mobile.

### W3 — Tabular viewer + References reclassification

**Scope.** Lands the `tabular` kind viewer AND fixes the
References-vs-Documents tile overlap.

- New `TabularViewer` widget: paginated rows + column filters +
  empty/error states. Reads schema from the artifact's MIME
  params (e.g. `application/json; schema=citation`) OR a sibling
  `_schema.json` artifact (open question Q6).
- `seed_demo_lifecycle.go`: lit-review deliverables now seed a
  `tabular`-kind artifact (citations) instead of relying on a
  document component.
- `shortcut_tile_strip.dart`: `_openReferences` no longer falls
  back to DocumentsScreen — it opens an `ArtifactsByKindScreen`
  filtered to `kind=tabular, schema=citation` (or to the
  ratified lit-review deliverable's tabular component).
- Research template YAML: lit-review phase tile config explicitly
  names References-as-tabular-citation, removing the
  Documents/References look-alike.

**Files touched:**
- `lib/widgets/artifact_viewers/tabular_viewer.dart` — new.
- `lib/screens/artifacts/artifacts_by_kind_screen.dart` — new.
- `lib/widgets/shortcut_tile_strip.dart` — route fix.
- `hub/internal/server/seed_demo_lifecycle.go` — seed change.
- `hub/templates/projects/research.v1.yaml` — tile-spec adjust.
- `test/widgets/tabular_viewer_test.dart` — new.

**LOC estimate:** ~350 mobile + ~80 hub.

### W4 — Image + multimodal user input

**Scope.** First multimodal landing — user can attach an image
from the overlay chat composer or via `attach_artifact` MCP
tool; agent receives it as a multimodal input through the engine
driver. Pure plumbing wedge; the rendering is `Image.network`
plus the existing `image_picker`.

**Files touched:**
- `lib/widgets/steward_overlay/steward_overlay_panel.dart` —
  paperclip button on composer; routes through `image_picker`.
- `hub/internal/server/handlers_artifacts.go` — upload endpoint
  for `image` kind (multipart, mime detect, size cap).
- Engine drivers — pass multimodal input through to the wire
  format (`content: [{type: image, source: …}]`). Per-driver:
  Claude Code, codex, gemini all support; verify ACP.
- `test/screens/overlay_image_attach_test.dart` — new.

**LOC estimate:** ~400 mobile + ~250 hub + per-driver wiring.

### W5 — Code-bundle viewer (read-only)

**Scope.** Renders a `code-bundle` artifact as a syntax-highlighted
file tree. Read-only; editing is deferred. Useful for agent-emitted
scaffolds, ML run-script snapshots, paper LaTeX sources.

**LOC estimate:** ~300 mobile.

### W6 — Audio + video viewers

**Scope.** Adds `just_audio` + `video_player` deps; thin player
widgets behind the kind-dispatch chassis. Picked last because the
demo arc doesn't need them yet; they exist so the multimodal IO
slot has a real landing.

**LOC estimate:** ~250 mobile + 1 dep each.

## Total budget

- ~1750 LOC mobile + ~480 LOC hub + 1 migration.
- +~5 MB APK (pdf lib ~2 MB, audio+video ~1.5 MB, tabular/image
  trivial).
- ~2–3 working weeks. W1 must land first; W2–W6 parallelisable.

## Dependencies on other plans

- **Tier 1 plan** (`agent-artifact-rendering-tier-1.md`) —
  orthogonal but related; Tier 1's `html`/`svg` fences are
  inline-renderers for *agent text*. This plan's `tabular`/`pdf`/
  `image` viewers are *typed-entity viewers* on a separate route.
  No code dependency; Tier 1 can ship first or this plan can.
- **Tier 2 plan** (deferred — to be opened
  `agent-artifact-rendering-tier-2-canvas.md`) — depends on this
  plan's W1 (the `canvas-app` kind slot must exist).
- **Multimodal driver support** — W4 requires engine drivers to
  pass image content through. Currently un-audited; W4 includes
  the audit.

## Rollout

1. **W1 lands first** — closed set + validation + backfill. No new
   viewers yet. Read-only registry chassis.
2. **W2 (PDF) + W3 (Tabular) in parallel** — first user-visible
   wins. W3 also removes the References tile bug.
3. **W4 (image upload)** — unlocks multimodal demo arc.
4. **W5 (code-bundle)** — supports ML run-script + paper-source
   demos.
5. **W6 (audio + video)** — last; demo doesn't need it, but the
   IO slot exists.
6. **Bump alpha tag** once W1+W2+W3+W4 are merged. W5+W6 can ship
   on the next minor.
7. **Update steward template prompts** to advertise the new kinds
   (so the agent picks `tabular` for citations, not free-text).

## Test plan (cross-wedge)

- Manual: seed a fresh demo, navigate to method phase, tap
  References → see tabular citation viewer (not duplicate Documents).
- Manual: steward creates a PDF research-paper artifact → tap →
  view inline.
- Manual: attach image from overlay chat composer → agent receives
  multimodal input → reply references the image.
- Regression: existing demo data (`eval_curve` artifacts) still
  renders after backfill.

## Open questions

These need answers before the matching wedge starts.

### Blocking W1

**Q1 — `citation` as its own kind, or `tabular` with schema?**
Pros for separate `citation` kind: type-safe filter,
viewer-can-assume-schema, agent picks the right MCP tool by name.
Cons: kind explosion (every domain wants its own typed table —
ablations, references, datasheets, …). Recommend `tabular` with
schema-by-MIME-param; the schema slot is the type-extensibility
hook. Lock in W1.

**Q2 — `prose-document` artifact vs `documents` row.** Two
storage paths for the same content shape. Either:
- **(a)** `documents` table is the *editable working copy*;
  `prose-document` artifacts are *immutable snapshots* (e.g. at
  phase ratification). Migration adds an `origin_document_id`
  hint on the artifact row.
- **(b)** Collapse the two: `documents` becomes a view over
  `artifacts WHERE kind='prose-document'`. Big migration; rejects
  the v1.0.484 typed-document work.
- **(c)** Keep them distinct forever; `prose-document` artifact
  is for *agent-generated* prose, `documents` for *director-edited*
  prose.
Recommend (a). Lock in W1.

**Q3 — CHECK constraint vs Go-side whitelist.** CHECK is
declarative but fights forward migrations (each new kind = new
migration). Go whitelist is flexible but loses DB-level safety.
Recommend Go whitelist + a unit test that exercises every kind
through the create handler, with the migration adding only a
documenting comment. Lock in W1.

**Q4 — `binary-blob` for model weights?** Backfill maps
`checkpoint` to `code-bundle`, but weights aren't code. Either
add a 12th kind (`binary-blob` with size + mime) or accept that
weights are a `dataset-ref` (URI-only — they live on object
storage). Recommend `dataset-ref`. Lock in W1.

### Blocking W2

**Q5 — `pdfx` vs `flutter_pdfview`.** Pure-Dart vs native-wrap.
Native is ~5x smaller bundle but platform-divergent. Pure-Dart is
~2 MB but consistent. Recommend `pdfx` for the demo (consistency
> bundle size at this stage).

### Blocking W3

**Q6 — Schema discovery for `tabular`.** Three options:
- **(a)** MIME params: `application/json; schema=citation`. Tiny,
  but only one schema-id slot.
- **(b)** Sibling `_schema.json` artifact in the same deliverable
  component group. Composable, but two-row reads.
- **(c)** New `artifact_schema_id` column. Cleanest but adds DB
  schema for a forward-leaning need.
Recommend (a) for MVP, escalate to (c) if domain-specific
viewers proliferate.

**Q7 — Editable tabular cells.** Inline-edit-and-save is a
desktop-Notion staple. MVP = read-only. If the demo arc requires
edit (citations need annotation), promote to in-scope.

### Nice-to-have (not blocking)

**Q8 — Lineage hooks.** Should W1 add an `origin_kind` column so
backfilled rows preserve their old free-form kind for forensic
queries? Cheap to add now, hard later.

**Q9 — Per-kind retention.** Audio/video are huge; should kind
imply a retention policy (e.g. video TTL = 30 days)? Defer; mention
in plan for the future-self.

**Q-new — Live metrics migration.** Should `run_metrics` /
`run_histograms` / `sweep_summary` ever migrate to typed
artifacts? Captured in the "Live-vs-artifact two-layer split"
section above. Current answer: no. Open if cross-axis querying
becomes a concrete need.

**Q10 — APK split alignment.** The deferred voice-input plan
proposed `full`/`lite`. PDF + audio + video pulls ~5 MB; this is
the first place a split makes real sense.

## Status

Open — drafted 2026-05-11 alongside the surface-separation rule
in [discussion §12.8](../discussions/agent-driven-mobile-ui.md).
Depends on principal review of the kind list + the eleven open
questions above. No commits yet; W1 starts after principal
sign-off.

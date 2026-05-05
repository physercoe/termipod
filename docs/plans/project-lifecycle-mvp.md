# Project lifecycle MVP — implementation plan

> **Type:** plan
> **Status:** Draft (2026-05-05) — paused; gated on [`doc-uplift.md`](doc-uplift.md) P0+P1 + [`contributor-readiness.md`](contributor-readiness.md) shipping first
> **Audience:** contributors (hub backend, mobile, demo operators)
> **Last verified vs code:** v1.0.351

**TL;DR.** Implementation plan for the project-lifecycle work — the
demo-MVP scope decided 2026-05-05: **all wedges in (no cut)**, demo
doubles as a chassis-generality showcase via diversity-within-research
+ YAML-in-narration. Eight wedges (W1–W7 in scope; W8 deferred
post-MVP); single hub-side schema migration shipped at W1; mobile and
template work follow the dependency graph in §4. Each wedge's "done"
is tied to specific beats in
[`demo-script.md`](demo-script.md), so dress rehearsals during
implementation directly validate engineering progress. Estimated
effort: ~25–35 working days for a solo path; can compress to ~3
calendar weeks with two contributors taking parallel branches after
W1. Critical-path: W1 → W5a → W5b → W7 (paper deliverable rendering
is the demo's final gate).

---

## 1. Why this plan exists

The lifecycle work has been specified to engineering grade (A1–A6,
C1) but no plan has tied wedges to dependencies, schedules, or
acceptance. This file is that plan. An engineer or operator can
read this end-to-end and know: what to build, in what order, what
counts as done, what depends on what, what the demo-day go/no-go
checks are.

**This plan supersedes** the lifecycle-shaped sections of
[`research-demo-gaps.md`](research-demo-gaps.md) (the older P4
tracker). Where the older doc is still authoritative for non-lifecycle
items (host bootstrap, A2A relay, etc.), it stays.

> **Prerequisite — paused 2026-05-05.** Two doc audits on 2026-05-05
> identified gaps that the demo audience (reviewers + their AI
> agents inspecting the codebase) will see. Because the docs are
> part of the demo deliverable, this plan is paused until both
> sibling plans ship:
>
> - [`doc-uplift.md`](doc-uplift.md) **P0+P1** — system-design doc
>   axis (architecture / schema / API / flows / cross-cutting)
> - [`contributor-readiness.md`](contributor-readiness.md) **all 6
>   items** — contributor-experience axis (CONTRIBUTING /
>   CODE_OF_CONDUCT / SECURITY / issue templates / local dev env /
>   test running)
>
> Lifecycle engineering resumes after both gates close.

---

## 2. Scope + non-goals

### 2.1 In scope

- All chassis work for D1–D10 (per
  [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  §5)
- Hub schema for phases, deliverables, components, criteria
  (A1 — `reference/project-phase-schema.md`)
- Template YAML extensions (A2 — `reference/template-yaml-schema.md`)
- HTTP endpoints for the lifecycle (A3 — `reference/hub-api-deliverables.md`)
- Mobile Structured Document Viewer (A4) + Structured Deliverable
  Viewer (A5)
- Research template content (A6 — 5 phases, 4 section schemas, 5
  prompt overlays, worker hints)
- Project Detail page restructure: phase ribbon, goal de-burial,
  steward strip, channel demote, tile cut, Activity tab, YAML reveal
  affordance
- Audit event taxonomy extensions (15 new kinds per A1 §6)
- Mobile cache schema for the new payloads (6 new tables per A3 §11)
- Demo dress-rehearsal cadence (per C1 §9)

### 2.2 Out of scope (explicitly deferred)

- W8 tab restructure (Option A: Now / Talk / Plan / Library / Agents)
  — post-MVP per discussion §10
- Per-member stewards (F-1 follow-up; multi-user)
- Council ratification authority (D8 — rejected with 422 in MVP)
- Document section evolution / cross-template reuse (A2 §18.2)
- Custom gate handles (closed library for MVP per A2 §8.3)
- Template inheritance / composition
- Per-entity threading (Channel demote covers what's needed for MVP)
- SSE for steward state (A3 §10.2 — polling is fine for demo)
- Webhook outbound from phase advance / ratification
- Skeleton second template (rejected per §4.4 demo strategy)
- Director-authored ad-hoc criteria (template-declared only in MVP)
- Document version snapshots / diff viewer (per A1 §9.2)

### 2.3 Demo-shipping subset

All of §2.1 ships for the demo. Per the 2026-05-05 §B-block decision
("no cut, all wedges in") the chassis-generality argument requires
every primitive to be exercised on demo day. Trimming a wedge would
break a beat in C1.

---

## 3. Bridge-status recap

The bridge from discussion to plan (per the "what's the bridge to a
plan?" 2026-05-05 conversation):

| Bucket | Status | Output |
|---|---|---|
| A — Specs | ✅ Done | A1, A2, A3, A4, A5, A6 |
| B — Open Q's closed | ✅ Done | All 6 closed in 2026-05-05 |
| C — Demo definition | ✅ Done | C1 (`plans/demo-script.md`) |
| D — The plan itself | this doc | — |

---

## 4. Wedge sequence + dependency graph

```
                              ┌─────────────────────────┐
                              │   W1 — Phase chassis   │
                              │   (foundational)        │
                              └──────────┬──────────────┘
                                         │
              ┌──────────┬───────────────┼──────────────┬──────────┐
              ↓          ↓               ↓              ↓          │
         ┌────────┐ ┌────────┐    ┌──────────────┐ ┌────────┐    │
         │  W2    │ │  W3    │    │  W4 (tiles)  │ │  W5a   │    │
         │Activity│ │Goal +  │    │              │ │  Doc   │    │
         │+ chan- │ │steward │    │              │ │ Viewer │    │
         │nel    │ │strip + │    │              │ │        │    │
         │demote │ │YAML    │    │              │ │        │    │
         └────────┘ └────────┘    └──────────────┘ └───┬────┘    │
                                                       │          │
                                                       ↓          │
                                                 ┌──────────────┐ │
                                                 │  W5b — Deliv │ │
                                                 │  Viewer +    │◀┘
                                                 │  schema      │
                                                 └──────┬───────┘
                                                        │
                                                        ↓
                                                  ┌──────────┐
                                                  │   W6 —   │
                                                  │ Criteria │
                                                  └────┬─────┘
                                                       │
                                                       ↓
                                                  ┌──────────┐
                                                  │   W7 —   │
                                                  │ Research │
                                                  │ template │
                                                  │ content  │
                                                  └──────────┘
```

**Strict critical path:** W1 → W5a → W5b → W6 → W7 (~17–23 days).

**Parallelism:**
- After W1 ships, W2 / W3 / W4 / W5a can run in parallel if multiple
  contributors.
- Solo path: W1 → W3 → W2 → W4 → W5a → W5b → W6 → W7 (~25–35 days).
- Two-contributor path: contributor A on critical (W1 → W5a → W5b →
  W6 → W7); contributor B on cross-cuts (W2 → W3 → W4) → joins A
  for W7 polish.

---

## 5. Schema migration plan

Single migration shipped at W1's start. Includes everything from A1
even though only `projects.phase` is consumed by W1 itself — keeps
migration ordering simple and lets W5a/W5b ship without further DDL.

```sql
-- Migration NNNN — project lifecycle (W1 ship)
-- See reference/project-phase-schema.md for full schema spec

ALTER TABLE projects        ADD COLUMN phase           TEXT;
ALTER TABLE projects        ADD COLUMN phase_history   TEXT;
ALTER TABLE documents       ADD COLUMN kind            TEXT;
ALTER TABLE documents       ADD COLUMN schema_id       TEXT;

CREATE TABLE deliverables (...);
CREATE TABLE deliverable_components (...);
CREATE TABLE acceptance_criteria (...);

CREATE INDEX idx_deliverables_project_phase ...;
CREATE INDEX idx_deliv_comp_deliv ...;
CREATE INDEX idx_deliv_comp_ref ...;
CREATE INDEX idx_criteria_project_phase ...;
CREATE INDEX idx_criteria_deliv ...;
CREATE INDEX idx_documents_kind ...;

-- Audit event kinds registered at runtime (no DDL):
--   project.phase_set, project.phase_advanced, project.phase_reverted
--   deliverable.created, deliverable.updated, deliverable.ratified, deliverable.unratified
--   deliverable_component.added, deliverable_component.removed
--   document.section_authored, document.section_ratified
--   criterion.created, criterion.met, criterion.failed, criterion.waived
```

Also at W1 ship: register the 15 new audit kinds in the hub's
audit-action allowlist.

Backfill: all defaults are NULL (per A1 §7.2). No row touched in
existing tables beyond `ALTER`.

Rollback: not formally supported (forward-only). If discovered post-
ship, remediate with a follow-up migration.

---

## 6. Per-wedge specs

### 6.1 W1 — Phase chassis

**Goal.** Add the phase column, render the phase ribbon, wire phase
advance, no behavior change for legacy non-phased projects.

**Files (hub).**
- `hub/migrations/NNNN_lifecycle.sql` — single migration (§5)
- `hub/api/projects/phase.go` (new) — handlers for §3 of A3:
  GET /phase, POST /phase/advance, POST /phase
- `hub/internal/audit/kinds.go` — register 3 new kinds:
  `project.phase_set`, `project.phase_advanced`, `project.phase_reverted`
- `hub/internal/projects/lifecycle.go` (new) — phase advance
  precondition logic (criteria-met validation)
- `hub/api/projects/get.go` — extend project response shape with
  `phase` + `phase_history` fields

**Files (mobile).**
- `lib/widgets/phase_ribbon.dart` (new) — Material horizontal stepper
- `lib/screens/projects/project_detail_screen.dart` — host the ribbon
  above current Pill bar; tap → phase summary screen (W5b stub
  initially)
- `lib/services/hub/hub_client.dart` — `advancePhase()`, `getPhase()`
- `lib/providers/hub_provider.dart` — phase state in cached overview

**API surface added.**
- `GET  /v1/teams/{team}/projects/{project_id}/phase`
- `POST /v1/teams/{team}/projects/{project_id}/phase/advance`
- `POST /v1/teams/{team}/projects/{project_id}/phase`
  (admin / hydration)

**Acceptance criteria (tied to C1).**
- [ ] **C1 §6.2 beat 1:** New research project shows phase ribbon
      with 5 phases; Idea highlighted as current. No console errors,
      no analyze warnings.
- [ ] **C1 §6.2 beat 2:** Tap Idea phase → phase summary screen
      opens (stub for W5b, but routes correctly).
- [ ] **C1 §6.2 beat 3:** Phase advance via API succeeds when
      criteria met; emits `project.phase_advanced` audit event.
      Ribbon updates to show Lit-rev as current.
- [ ] Legacy projects (`phase=NULL`) render exactly as before — no
      ribbon, no Overview restructure.
- [ ] `POST /phase/advance` returns 409 with problem-detail when
      required criteria are pending.

**Open prep items resolved here.**
- C1 §11.3 (phase advance banner copy) — pinned to "✨ Phase ready
  to advance to {Phase}." with "[Advance phase]" / "[Not yet]"
  buttons per A5 §9.2.

**Effort.** 3–5 days.

**Dependencies.** None (foundational).

---

### 6.2 W2 — Activity surface + Channel demote

**Goal.** Wire `_ActivityView` into the pill bar (currently dead
code); demote Channel to AppBar Discussion icon (D10); add Activity-
snippet to Overview.

**Files (mobile).**
- `lib/screens/projects/project_detail_screen.dart` — pill bar
  changes (Channel out, Activity in); AppBar gains chat icon for
  Discussion; Overview gains Activity-snippet
- `_ActivityView` (already exists, line 319) — wire into pill bar
  children list; verify it filters by project_id correctly
- New widget: `lib/widgets/activity_snippet.dart` — last 5 events
  preview with "View all" deeplink to Activity tab
- AppBar Discussion icon → opens existing `project_channel_screen.dart`
  via `Navigator.push` (no schema change)

**Files (hub).**
- None substantive; existing `audit_events` infra already supports
  the new kinds added in W1 (and to be added in W5b/W6).

**Acceptance criteria.**
- [ ] **C1 §6.7:** Activity tab shows the project's audit events in
      chronological order; new lifecycle kinds (phase advance,
      deliverable ratify) render with sensible labels.
- [ ] Discussion icon in AppBar opens project channel; the existing
      channel content unchanged.
- [ ] Activity-snippet on Overview shows last 5 events; "View all"
      navigates to Activity tab.
- [ ] Pill bar after change: Overview · **Activity** · Agents · Tasks
      · Library *(or current "Files" name; rename optional)*.

**Effort.** 1–2 days.

**Dependencies.** W1 (so the new audit kinds appear in the feed).

---

### 6.3 W3 — Goal de-burial + Steward strip + YAML reveal

**Goal.** Promote goal text to a 2-line block; expand steward chip
into a strip with rich states + handoff indicator; collapse metadata
rows behind expander; add "View template YAML" affordance (resolves
C1 §11.1 prep gap).

**Files (mobile).**
- `lib/widgets/steward_strip.dart` (new) — replaces the chip in
  `portfolio_header.dart`; renders 7 states (per discussion §6.6) +
  handoff indicator (per §B.6)
- `lib/screens/projects/project_detail_screen.dart` Overview — goal
  block, steward strip, metadata collapsed
- New widget: `lib/widgets/template_yaml_sheet.dart` — modal sheet
  showing the project's template YAML (parsed from cached template
  spec)
- AppBar info icon → opens template YAML sheet (resolves C1 §11.1)

**Files (hub).**
- `hub/api/projects/steward_state.go` (new) — `GET /steward/state`
  endpoint per A3 §10.1
- Steward state aggregation: combines `agents` row state + recent
  `agent_events` to produce `current_action` + `handoff` payload

**API surface added.**
- `GET /v1/teams/{team}/projects/{project_id}/steward/state`

**Acceptance criteria.**
- [ ] **C1 §6.2 / §6.4 / §6.5:** YAML reveal sheets open and show
      research template + section schema + criterion specs at the
      three checkpoint moments. Sheets are dismissible; do not
      interrupt the demo flow.
- [ ] Steward strip shows `idle` / `working` / `awaiting-director`
      etc. states correctly during demo flow.
- [ ] Handoff indicator surfaces during a steward-to-steward A2A
      handoff (mocked test).
- [ ] Goal text visible without scroll; not duplicated in metadata
      rows.
- [ ] Metadata rows (8 rows from current Overview) collapsed behind
      "Show details" expander; expandable.

**Open prep items resolved here.**
- C1 §11.1 (YAML reveal affordance) — implemented as AppBar info
  icon → modal sheet.

**Effort.** 2–3 days.

**Dependencies.** W1 (phase context for next-action card; can stub
without W5b ready).

---

### 6.4 W4 — Tile cut + template-driven

**Goal.** Reduce 7 shortcut tiles to 3 default + phase-filtered set;
templates declare which tiles per phase.

**Files (mobile).**
- `lib/widgets/shortcut_tile_strip.dart` (new) — renders the
  template-declared tile set for the current phase
- `lib/screens/projects/project_detail_screen.dart` Overview — replace
  the 7 hard-coded ShortcutTiles with the strip widget
- Tile slug registry (A2 §11) — closed enum; widget routes per slug

**Files (hub).**
- Template loader already parses `tiles:` (covered by W7's template
  YAML); no further hub work.

**Acceptance criteria.**
- [ ] Initiation phase shows [References, Documents] tiles (research
      template's lit-review + method phases).
- [ ] Experiment phase shows [Outputs, Documents, Experiments].
- [ ] Paper phase shows [Outputs, Documents].
- [ ] Reviews tile is gone (banner serves it; gap #4 closed).
- [ ] Schedules / Plans / Assets tiles can be re-declared if the
      template wants them; they're not in the default research set
      per A6.

**Effort.** 1–2 days.

**Dependencies.** W1 (current phase known); W7 (research template
declares tile sets — but W4 can ship with a hardcoded research-template
mapping that W7 supersedes).

---

### 6.5 W5a — Structured Document Viewer

**Goal.** New mobile screen rendering typed structured documents
section-by-section; section state pips; section-targeted distillation
entry; manual edit with optimistic concurrency; section ratification;
plain-markdown fallback.

**Files (hub).**
- `hub/api/documents/get.go` — extend to return structured body when
  `kind != null`
- `hub/api/documents/sections.go` (new) — handlers for
  PATCH `/sections/{slug}`, POST `/sections/{slug}/status`,
  POST `/sections/{slug}/distill`
- `hub/internal/documents/section_state.go` — state machine:
  `empty → draft → ratified` with ratified→draft on edit (A4 §5.6)
- `hub/internal/audit/kinds.go` — register 2 new kinds:
  `document.section_authored`, `document.section_ratified`
- `hub/internal/agents/session_distillation.go` — extend distillation
  to write into a target section when `target = (document_id, section)`

**Files (mobile).**
- New screen: `lib/screens/documents/structured_document_viewer.dart`
  (section index)
- New screen: `lib/screens/documents/section_detail_screen.dart`
- `lib/widgets/section_state_pip.dart` (new) — 3-state visual encoding
- `lib/widgets/markdown_section_editor.dart` (new) — basic toolbar
  + textarea editor with markdown helper buttons
- `lib/services/hub/hub_client.dart` — section endpoints
- Cache table: `cache_documents_typed` in `HubSnapshotCache`
- Offline mutation queue: edit + ratify queued per A4 §10.3
- `lib/services/hub/open_steward_session.dart` — extend to accept
  section target

**API surface added.**
- `GET    /v1/teams/{team}/documents/{document_id}` (extended)
- `PATCH  /v1/teams/{team}/documents/{document_id}/sections/{slug}`
- `POST   /v1/teams/{team}/documents/{document_id}/sections/{slug}/status`
- `POST   /v1/teams/{team}/documents/{document_id}/sections/{slug}/distill`

**Acceptance criteria (C1-anchored).**
- [ ] **C1 §6.3:** Lit-review-doc rendered with 4 sections; state
      pips correct; tap empty Positioning section → "[Direct steward
      to draft]" empty-state card; tap → opens session; distillation
      updates body; ratify → ratified.
- [ ] **C1 §6.4:** Method-doc rendered with 7 sections; tap
      evaluation-plan (in-review) → section detail; ratify → ratified;
      auto-cascades evaluation-plan-ratified gate criterion.
- [ ] **C1 §6.5:** Experiment-report rendered; section authoring
      flow as above for Analysis section.
- [ ] **C1 §6.6:** Paper-draft rendered with 9 sections; abstract
      ratification flow.
- [ ] Plain-markdown documents (kind=NULL) bypass viewer; existing
      `doc_viewer_screen.dart` path unchanged.
- [ ] Schema-not-found case shows fallback banner per A4 §8.
- [ ] Manual edit returns 412 on stale `expected_last_authored_at`;
      conflict UI per A4 §5.6.
- [ ] Offline edit queues; reconciles on reconnect; clock glyph
      visible during pending sync.

**Open prep items resolved here.**
- C1 §11.2 (empty-state copy) — pinned: "This section hasn't been
  authored yet." + buttons "[Direct steward to draft]" / "[Write
  manually]".

**Effort.** 5–7 days.

**Dependencies.** W1 (schema migration shipped); A4 (spec).

---

### 6.6 W5b — Deliverables schema + Structured Deliverable Viewer

**Goal.** Wrap A4's document viewer with components panel + criteria
panel + ratification action; deliverable + component CRUD; composed-
overview endpoint; phase ribbon → deliverable navigation.

**Files (hub).**
- `hub/api/projects/deliverables.go` (new) — list, get, create,
  update, ratify, unratify (per A3 §4)
- `hub/api/projects/components.go` (new) — add, remove (per A3 §5)
- `hub/api/projects/overview.go` (new) — composed overview endpoint
  (A3 §9.1) + past-phase snapshot (A3 §9.2)
- `hub/internal/projects/template_hydration.go` (new) — on phase
  entry, instantiate deliverables + components + criteria from
  template spec
- `hub/internal/audit/kinds.go` — register 6 new kinds:
  `deliverable.created`, `deliverable.updated`, `deliverable.ratified`,
  `deliverable.unratified`, `deliverable_component.added`,
  `deliverable_component.removed`

**Files (mobile).**
- New screen: `lib/screens/deliverables/structured_deliverable_viewer.dart`
- New screen: `lib/screens/deliverables/phase_summary_screen.dart`
  (A5 §3, used when N>1 deliverables)
- Component cards: `lib/widgets/deliverable_components/document_card.dart`,
  `artifact_card.dart`, `run_card.dart`, `commit_card.dart`
- `lib/widgets/deliverable_state_pip.dart` (new) — 3-state pip matching
  A4's pattern but at deliverable scope (with `in-review` retained)
- `lib/services/hub/hub_client.dart` — deliverable + component
  endpoints + composed overview
- Cache tables: `cache_deliverables`, `cache_criteria` in
  `HubSnapshotCache`
- `lib/widgets/phase_ribbon.dart` (W1) — wire taps to push deliverable
  viewer or phase summary based on N

**API surface added.**
- `GET    /v1/teams/{team}/projects/{project_id}/deliverables`
- `GET    /v1/teams/{team}/projects/{project_id}/deliverables/{deliv_id}`
- `POST   /v1/teams/{team}/projects/{project_id}/deliverables`
- `PATCH  /v1/teams/{team}/projects/{project_id}/deliverables/{deliv_id}`
- `POST   /v1/teams/{team}/projects/{project_id}/deliverables/{deliv_id}/ratify`
- `POST   /v1/teams/{team}/projects/{project_id}/deliverables/{deliv_id}/unratify`
- `POST   /v1/teams/{team}/projects/{project_id}/deliverables/{deliv_id}/components`
- `DELETE /v1/teams/{team}/projects/{project_id}/deliverables/{deliv_id}/components/{comp_id}`
- `GET    /v1/teams/{team}/projects/{project_id}/overview`
- `GET    /v1/teams/{team}/projects/{project_id}/phases/{phase_id}/snapshot`

**Acceptance criteria.**
- [ ] **C1 §6.3:** Tap lit-review-doc deliverable card → A5 opens
      with document component card showing 4-section breakdown +
      criteria panel (lit-review-ratified pending, min-citations met).
- [ ] **C1 §6.4:** Tap method-doc deliverable → A5 with 1 component
      + 3 criteria; ratify deliverable → cascade.
- [ ] **C1 §6.5:** Tap experiment-results deliverable → A5 with **4
      components rendering (1 doc, 2 artifacts, 1 run)** + 4 criteria
      panel; this is the chassis-range showpiece; verify all card
      kinds render distinctly.
- [ ] **C1 §6.6:** Tap paper-draft deliverable → A5 with paper
      document + 9 sections.
- [ ] Composed overview endpoint feeds Project Detail in one fetch;
      ETag-versioned; revalidates per A3 §2.8.
- [ ] Phase advance banner (per A5 §9.2) appears after deliverable
      ratify; tap "Advance phase" calls API; ribbon updates.
- [ ] Past-phase snapshot opens read-only; action bar hidden;
      "(archived)" badge in AppBar.
- [ ] Template hydration: creating a research project instantiates
      Idea-phase deliverables (none) + criteria (1 text); advancing
      to lit-review instantiates lit-review-doc deliverable + sections
      empty + 2 criteria.

**Effort.** 5–7 days.

**Dependencies.** W1 (schema), W5a (document component routes to A4).

---

### 6.7 W6 — Acceptance criteria

**Goal.** Criterion CRUD API; per-kind rendering (text / metric /
gate); mark-met flows; ratify-prompt attention items on metric
firing; gate library implementations.

**Files (hub).**
- `hub/api/projects/criteria.go` (new) — list, get, create, mark-met,
  mark-failed, waive, update (per A3 §6)
- `hub/internal/criteria/evaluation.go` (new) — gate library
  implementations: `deliverable.ratified`, `all-sections-ratified`,
  `runs.completed-without-error`, `phase.has-no-open-attention`
- `hub/internal/criteria/metric_watcher.go` (new) — watches run
  events; when threshold met, marks criterion + posts ratify-prompt
  attention item
- `hub/internal/audit/kinds.go` — register 4 new kinds:
  `criterion.created`, `criterion.met`, `criterion.failed`,
  `criterion.waived`

**Files (mobile).**
- `lib/widgets/criterion_row.dart` (new) — per-kind rendering: text
  shows body.text; metric shows operator + threshold + current value
  (when pending); gate shows English template
- `lib/widgets/criterion_state_pip.dart` (new) — 4-state pip
  (`pending` / `met` / `failed` / `waived`)
- `lib/services/hub/hub_client.dart` — criterion endpoints
- `lib/screens/me/me_screen.dart` — render ratify-prompt attention
  items with deeplink to deliverable viewer's action bar

**API surface added.**
- `GET   /v1/teams/{team}/projects/{project_id}/criteria`
- `GET   /v1/teams/{team}/projects/{project_id}/criteria/{crit_id}`
- `POST  /v1/teams/{team}/projects/{project_id}/criteria`
- `PATCH /v1/teams/{team}/projects/{project_id}/criteria/{crit_id}`
- `POST  /v1/teams/{team}/projects/{project_id}/criteria/{crit_id}/mark-met`
- `POST  /v1/teams/{team}/projects/{project_id}/criteria/{crit_id}/mark-failed`
- `POST  /v1/teams/{team}/projects/{project_id}/criteria/{crit_id}/waive`

**Acceptance criteria.**
- [ ] **C1 §6.5:** When ablation-sweep run completes with
      perplexity ≥ threshold, hub auto-marks the metric criterion
      as `met` and posts a ratify-prompt attention item with
      deeplink to deliverable viewer.
- [ ] Director taps the attention item → lands on the experiment-
      results deliverable with action bar's Ratify button highlighted.
- [ ] Text criteria can be marked-met by director with `evidence_ref`.
- [ ] Gate criteria auto-fire on cascading events:
      `deliverable.ratified` event → criterion.met for any criterion
      using `deliverable.ratified` gate referencing that deliverable.
- [ ] Criteria render correctly per kind on the criteria panel
      (per A5 §8.2).
- [ ] Metric criterion shows current value vs threshold when pending.

**Effort.** 3–4 days.

**Dependencies.** W5b (criteria are panel-rendered inside A5);
W7 (template-declared gate references resolved via template).

---

### 6.8 W7 — Research template content

**Goal.** Author the research template YAML + 5 prompt overlay markdown
files. Template declares all 5 phases with their deliverables, section
schemas, criteria, transitions, widgets, tile sets, spawn policy, and
worker hints per A6.

**Files (hub).**
- `hub/templates/projects/research.v1.yaml` (new) — full content per
  A6 §3–§10
- `hub/templates/projects/prompts/research.idea.md` (new) — sketch
  per A6 §11
- `hub/templates/projects/prompts/research.lit-review.md` (new)
- `hub/templates/projects/prompts/research.method.md` (new)
- `hub/templates/projects/prompts/research.experiment.md` (new)
- `hub/templates/projects/prompts/research.paper.md` (new)
- `hub/internal/templates/loader.go` — extend if needed for new
  template format keys (worker_hints, etc. — see §6.8.1)

**Files (mobile).**
- None substantive (templates are server-side).
- New overview widget slugs registered in
  `lib/screens/projects/overview_widgets/registry.dart`:
  `idea_conversation`, `deliverable_focus`, `experiment_dash`,
  `paper_acceptance`. Each is a small widget composing existing
  primitives (steward strip, deliverable card preview, etc.).

**Acceptance criteria.**
- [ ] Hub starts cleanly with the new template loaded; logs no
      validation errors.
- [ ] Creating a project from the research template hydrates phase
      = `idea`, instantiates 1 text criterion (`scope-ratified`),
      no deliverables.
- [ ] Each phase advance hydrates that phase's deliverables +
      criteria per the template.
- [ ] All 4 section schemas render correctly in A4.
- [ ] Steward prompt overlay for the active phase is appended to
      the steward's base prompt at session open.
- [ ] **C1 end-to-end:** the full 12-min demo flow runs against the
      research template + mock-trainer harness without manual data
      munging beyond pre-seed.

**Effort.** 4–6 days (mostly content authoring; small loader changes).

**Dependencies.** W1, W5a, W5b, W6 (chassis must work to render the
content); A2 + A6 (specs).

#### 6.8.1 Two minor A2 schema gaps to close in W7

These were called out in A6 §13 follow-ups; small extensions, ship
inline with W7:

1. **`worker_hints:` top-level field** (A6 §8) — add to A2's allowed
   keys; loader parses + exposes via API; mobile doesn't consume
   directly but steward prompts can reference.
2. **`admin_only: true` flag on transitions** (A6 §10) — add to
   A2's transition spec; runtime gates the transition behind admin
   role check.

---

### 6.9 W8 — Tab restructure (Option A) — DEFERRED

Per discussion §10: optional, post-MVP. Schedule after the phase
model + two viewers stabilize and real usage data informs whether
the work-centric tab restructure (Now / Talk / Plan / Library /
Agents) is justified. **Not in this plan's scope.**

---

## 7. Test strategy

### 7.1 Unit tests

- **Hub:** existing pattern — table-driven Go tests for each new
  handler. Cover happy path + auth-fail + precondition-fail + 412
  conflict.
- **Mobile:** widget tests for new widgets (phase ribbon, steward
  strip, criterion row, state pips). Provider tests for cache
  + offline queue logic.
- Coverage target: matches existing project bar (~70% on changed
  files).

### 7.2 Integration tests

- Hub-mobile end-to-end via existing test harness. Create project
  → advance through 5 phases with real audit emission + cache
  refresh.
- Template hydration: create project → assert deliverables + criteria
  rows match template spec.
- Section-targeted distillation: open session with target → mock
  distill → assert section body + state updated.

### 7.3 Dress rehearsals

Per C1 §9, after every wedge ship:

| After ship | Run segments | Pass criteria |
|---|---|---|
| W1 | C1 §6.1, §6.2 | Phase ribbon + advance work; legacy projects unchanged |
| W2 | C1 §6.7 | Activity feed + Discussion icon work |
| W3 | C1 §6.2 YAML reveal #1 | Steward strip + YAML sheet open correctly |
| W4 | All phase Overviews | Tile sets vary per phase |
| W5a | C1 §6.3 (full) | Document viewer flow including direct steward + ratify |
| W5b | C1 §6.4, §6.5 | Deliverable viewer with all component kinds |
| W6 | C1 §6.5 metric criterion firing | Ratify-prompt lands on Me; deeplink works |
| W7 | C1 full 12-min run-through | All beats land within ±15s of timing target |

### 7.4 Demo-day -7 rehearsal

Full run on the actual demo phone + network. Records timings, flags
any drift from C1's 12-min target.

### 7.5 Demo-day -1 rehearsal

Full run with audience-side projector / screen-mirror confirmed
working.

---

## 8. Demo-day acceptance

Per C1 §10 (success criteria):

### 8.1 Director-persona checks (7)

- [ ] Cold open → first phase advance ≤ 90s
- [ ] Phase advance feels intentional (always explicit ratify)
- [ ] Section state pips recognizable at glance
- [ ] Steward direct sessions return ≤ 2s
- [ ] No on-stage configuration / settings modals
- [ ] Total runtime within ±15s of 12 min
- [ ] No console errors visible during the run

### 8.2 Reviewer-persona checks (7)

- [ ] All 3 YAML moments fire on schedule
- [ ] Each YAML reveal makes chassis-vs-template seam visible
- [ ] All 4 component kinds appear (commit absent in research demo
      is OK; narrator can mention)
- [ ] All 3 criterion kinds appear in Experiment phase
- [ ] Both viewers (A4 + A5) exercised in 4 phases each
- [ ] Director's gate-keeping role demonstrated explicitly
- [ ] Reviewer can answer "what changes for a different domain?"
      with "the YAML"

### 8.3 Recovery checks (3)

- [ ] Steward fail → demo continues per C1 §8.1
- [ ] Network flake → cached state + queued mutations work per C1 §8.3
- [ ] Beat slips >10s → narrator compensates by trimming a later beat

---

## 9. Risks + mitigations

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| 1 | Schema migration ships at W1 but W5a/W5b's columns sit unused for days; rollback complicates if W5a/b slip | Med | Single migration is intentional — simplifies ordering. Rollback would require a follow-up migration anyway. |
| 2 | Section-targeted distillation API + mock-session integration is the most novel piece; could regress existing session UX | Med | Land W5a behind a feature flag if needed; existing plain-markdown viewer stays the default until verified. |
| 3 | Composed-overview endpoint becomes a perf hotspot (every Project Detail navigation hits it) | Low-Med | ETag + Cache-Control:max-age=15 already specified (A3 §2.8); if hot, add hub-side cache layer post-ship. |
| 4 | Mock-trainer harness drifts from real GPU output shape, breaking demo when real GPU is used | Med | Dress-rehearse with mock-trainer + once with real GPU before demo-day -7. Both paths in scope. |
| 5 | Pre-seed data state diverges from the C1 §5 checklist between rehearsals | High | Automate pre-seed via a script that runs from C1's checklist; idempotent. Add to dress-rehearsal runbook. |
| 6 | YAML reveal sheets render unparseable / wrong content | Low | Snapshot tests on the 3 reveal payloads. |
| 7 | W7's prompt overlays produce poor steward output that breaks demo flow | Med | Iterate prompts during W7's 4–6 day window; mock-canned demo exchanges per C1 §8.1 contingency. |
| 8 | One contributor implementing solo-path stalls; calendar slips | Med-High | Two-contributor split (§4) recovers ~10 days; identify partner before W1 starts. |
| 9 | Acceptance criteria automation (gate library) misfires and falsely auto-marks criteria | Low-Med | Gate evaluation logic gets unit + integration tests; production logs ratify-prompt issuance for audit. |
| 10 | Demo phone hardware fails | Low | Backup phone with identical pre-seed; backup laptop with screen-mirror. |

---

## 10. Open follow-ups (post-MVP)

Captured here so they don't lose context once D1 ships. Not in MVP
scope; revisit when concrete need emerges.

1. W8 tab restructure (Option A) — schedule after MVP usage data.
2. Per-member stewards (F-1) — schedule when first second-member
   project arrives.
3. Council ratification authority (D8) — design when a real
   council use case emerges.
4. Document section evolution + cross-template reuse (A2 §18.2).
5. Custom gate handles beyond the closed library (A2 §18.3).
6. Template inheritance / composition.
7. Per-entity threading (replaces / extends Channel demote).
8. SSE for steward state (A3 §10.2) — replace polling.
9. Webhook outbound from phase advance.
10. Skeleton second template (e.g., feature-development) — ship if
    first non-research project arrives.
11. Director-authored ad-hoc criteria.
12. Document version snapshots / diff viewer.
13. PDF export of typed structured documents.
14. Inline section comments / redlines.
15. Concurrent multi-author live editing.

---

## 11. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — design discussion (D1–D10)
- [`reference/project-phase-schema.md`](../reference/project-phase-schema.md)
  — A1, hub schema
- [`reference/template-yaml-schema.md`](../reference/template-yaml-schema.md)
  — A2, template authoring
- [`reference/hub-api-deliverables.md`](../reference/hub-api-deliverables.md)
  — A3, HTTP surface
- [`reference/structured-document-viewer.md`](../reference/structured-document-viewer.md)
  — A4, mobile doc viewer
- [`reference/structured-deliverable-viewer.md`](../reference/structured-deliverable-viewer.md)
  — A5, mobile deliverable viewer
- [`reference/research-template-spec.md`](../reference/research-template-spec.md)
  — A6, research template content
- [`plans/demo-script.md`](demo-script.md) — C1, the 12-min walkthrough
  that anchors every wedge's acceptance criteria
- [`plans/research-demo-gaps.md`](research-demo-gaps.md) — older P4
  tracker; this plan supersedes its lifecycle-shaped sections
- [`reference/audit-events.md`](../reference/audit-events.md) — base
  audit taxonomy that the 15 new kinds extend
- [`reference/steward-templates.md`](../reference/steward-templates.md)
  — agent templates referenced by research template's worker_hints
- [`decisions/006-cache-first-cold-start.md`](../decisions/006-cache-first-cold-start.md)
  — cache-first strategy mobile honors throughout
- [`decisions/017-layered-stewards.md`](../decisions/017-layered-stewards.md)
  — general / project steward boundary informs §6 of discussion +
  W3's steward strip

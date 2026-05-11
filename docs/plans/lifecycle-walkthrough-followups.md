---
name: Lifecycle walkthrough follow-ups
description: Six wedges surfaced by v1.0.482 QA — Plans/Schedules tile scoping, seed-demo plan_steps rework + task seeding, plan/step/phase/task glossary lock-in, walkthrough W0 (project conjuration), data-driven phase_specs tiles + on-device editor.
---

# Lifecycle walkthrough follow-ups

> **Type:** plan
> **Status:** Open (2026-05-11)
> **Audience:** principal · contributors · QA
> **Last verified vs code:** v1.0.483

**TL;DR.** Six small-to-medium wedges surfaced during the v1.0.482
lifecycle walkthrough QA. They split into three concerns —
**fix-and-clarify** (W1–W3), **scenario completeness** (W4), and
**data-driven UI composition** (W5–W6). Together they remove the
"plans look like 5 projects" / "Documents tile missing" /
"can't create a project from the steward" gaps that block the
walkthrough from being a clean demo arc. Companion test script:
[`how-to/test-steward-lifecycle.md`](../how-to/test-steward-lifecycle.md)
(updated alongside W4).

---

## Why now

The v1.0.482 walkthrough QA (2026-05-10) surfaced five concrete
issues that all trace back to two underlying gaps:

1. **The seed-demo + UI are misaligned with the actual schema** —
   the Plans tile from project detail dumps the team-wide list
   because no `project_id` is passed; the seed-demo's `plan_steps`
   use an invalid `agent_driven` kind and mirror the phase ribbon
   instead of carrying real work units; tasks are never seeded;
   plan / step / phase / task / deliverable / AC have no glossary
   entry so the inconsistency reads as a UI bug.
2. **The tile vocabulary is APK-bound** — `_researchPhaseTiles` is
   a hardcoded Dart map. Adding Documents to the idea phase
   (v1.0.483) required a code edit + APK rebuild; neither the
   steward nor the user can shape tiles to a specific project
   without a release.

The walkthrough also doesn't exercise project **conjuration** — every
scenario assumes a pre-seeded project. The "Agent-driven mode (the
demo)" script in `discussions/agent-driven-mobile-ui.md` §11 has the
steward creating the project from a template as turn 2. Without that
in the walkthrough we test "steward can mutate existing state" but
not "steward can spin up the next project the principal asks for" —
which IS the demo claim.

## Status & known gaps

- v1.0.483 ships Documents-on-idea via hardcoded edit (interim).
- `phase_specs[<phase>].overview_widget` is already YAML-driven (W7,
  v1.0.359). Tiles are the natural next field along that axis.
- `projects.update` MCP tool exists and the steward already calls it
  — adding a `phase_tiles_overrides` field is additive, no new tool.
- `TileSlug` is a closed Dart enum (`shortcut_tile_strip.dart:19`);
  user / steward edits compose tiles from this fixed vocabulary, not
  arbitrary new types. Tier 2 / Tier 3 SDUI (per ADR-023 D8 +
  `discussions/agent-driven-mobile-ui.md` §12) stays post-MVP — this
  wedge ships the composition axis only.

---

## Wedges

### W1 — Plans / Schedules tiles scope by project

**Problem.** `shortcut_tile_strip.dart:269` pushes
`const PlansScreen()` with no project context. `PlansScreen` has a
`_projectFilter` field that defaults to `null` = team-wide; the tile
entry never sets it. Tapping Plans from `research-method-demo` dumps
all 5 plans (one per seeded project). Same bug class on the
Schedules tile (`const SchedulesScreen()` at line 268).

**Fix.** Add `String? projectId` constructor arg to `PlansScreen` and
`SchedulesScreen`. When non-empty, pre-apply as the initial
`_projectFilter`. Filter sheet still lets the user broaden to "all"
if they want; cache + load logic unchanged. Other tile entries
(Outputs, Documents, Experiments) already pass `projectId` and are
fine.

**Done criteria.**
- Plans tile from a project detail lists only that project's plans.
- Schedules tile same.
- Filter sheet still offers cross-project broadening.
- Existing team-wide Plans / Schedules entry points (AppBar Search →
  Plans, etc.) keep listing all projects (call sites that don't pass
  `projectId` see the prior behaviour).

**Acceptance.** Walkthrough scenario 6 (run + artifact) shows one
plan in research-method-demo, not five.

---

### W2 — Rework seed-demo `plan_steps` + seed tasks

**Problem A (plan_steps).** `seed_demo_lifecycle.go:912-921` seeds
one `plan_step` per phase tagged `kind='agent_driven'` with a status
mirroring phase progression. Two issues:
- `kind='agent_driven'` isn't in `planStepKinds`
  (`handlers_plans.go:27-34` — `agent_spawn | llm_call | shell |
  mcp_call | human_decision`). Validation doesn't fire on seed rows
  so this slipped in; a real `plans.create` MCP call would 400.
- Phase progression already has authoritative storage on
  `projects.phase` + `projects.phase_history` (migration 0034).
  Re-encoding it in `plan_steps` makes the plan look like "the plan
  = the phase ribbon," reinforces the plan-vs-phase confusion from
  W3, and leaves no room to show *actual* per-phase work.

**Fix A.** Replace `lifecyclePlanStep` with one that carries the
schema-valid kind + a real spec. For research-method-demo (phase=
method), e.g.:

| phase_idx | step_idx | kind | spec_json hint | status |
|---|---|---|---|---|
| 0 (idea) | 0 | human_decision | "ratify scope" | completed |
| 1 (lit-review) | 0 | agent_spawn | spawn lit-reviewer.v1 | completed |
| 1 | 1 | human_decision | ratify lit-review doc | completed |
| 2 (method) | 0 | llm_call | draft method proposal | completed |
| 2 | 1 | agent_spawn | spawn critic.v1 red-team | in_progress |
| 2 | 2 | human_decision | director ratifies method | pending |
| 3 (experiment) | 0..N | agent_spawn / llm_call / shell | (per-phase work) | pending |
| 4 (paper) | 0..N | agent_spawn / human_decision | (per-phase work) | pending |

Each of the five demo projects gets a step set sized to its phase
position. Past-phase steps are `completed`; current-phase steps are
mixed `completed` / `in_progress` / `pending` to exercise UI state
combinations.

**Problem B (no tasks).** Tasks aren't seeded at all. The Tasks tab
on project detail is empty during walkthrough QA, which both makes
the demo look incomplete and prevents scenario coverage of
task-related MCP tools.

**Fix B.** Seed 3–5 tasks per project in mixed states
(`todo` / `in_progress` / `done`), some with `parent_task_id` to
exercise the subtask hierarchy, some assigned to the project's
steward / worker agents. Mirror the phase realism: idea-phase
projects have 1–2 vague exploratory tasks; method/experiment phases
have more concrete tasks tied to the seeded plan_steps.

**Done criteria.**
- `flutter_test`-equivalent assertion in `seed_demo_lifecycle_test.go`:
  every seeded `plan_step.kind` ∈ `planStepKinds`.
- New assertion: every project has ≥ 1 task row after seed.
- Plans tile (post-W1) on research-method-demo shows realistic
  per-phase steps, not phase mirrors.

---

### W3 — Glossary lock-in: plan / step / phase / task / deliverable / AC

**Problem.** Six related entities; relationships are implicit in the
schema and inconsistent in the UI.
[`docs/reference/glossary.md`](../reference/glossary.md) is the
canonical source for collision-prone terms and `lint-glossary.sh`
enforces consistent usage across the rest of the doc tree. Adding the six entries here is a
prerequisite to retitling UI labels (post-MVP) and to keeping new
contributors from re-litigating the relationship.

**Fix.** Add six glossary entries:

- **Project** — top-level work container; owns phases, plans, tasks,
  deliverables, criteria.
- **Phase** — string column on `projects` (e.g. `idea` /
  `lit-review`); template-driven progression; phase history lives on
  `projects.phase_history`. Phases are NOT separate rows — they're a
  state value on the project.
- **Plan** — one execution-spec row per project (in practice; schema
  doesn't enforce uniqueness but seed + UI assume); `template_id` +
  `spec_json` describe the recipe. *Not* per-phase.
- **Plan-step** — work unit inside a plan; bucketed by `phase_idx` +
  `step_idx`; `kind` ∈ {agent_spawn, llm_call, shell, mcp_call,
  human_decision}. Phase-scoped via `phase_idx`, but the plan that
  owns it spans all phases.
- **Task** — project-scoped kanban entity; no phase column; can have
  a `parent_task_id` for subtasks; links to a `milestone_id`
  optionally. **Independent of plan** — tasks don't share rows with
  `plan_steps`.
- **Deliverable** — per-(project, phase) ratifiable artifact; carries
  `ratification_state` and `deliverable_components` (refs to
  documents / artifacts / runs / commits).
- **Acceptance criterion (AC)** — per-(project, phase, optional
  deliverable) row with `kind` ∈ {text, metric, gate} and `state` ∈
  {pending, met, failed, waived}.

Each entry includes a one-line "Relationship" rule:
- Project → Phases (current + history) → Deliverables (per phase) →
  ACs (per deliverable or per phase)
- Project → Plan → Plan-steps (phase-bucketed)
- Project → Tasks (un-phased)

**Done criteria.**
- `glossary.md` has all six entries with cross-references.
- `lint-glossary.sh` passes.
- Cross-link from `research-template-spec.md` §3 (phases) to the new
  glossary section.

---

### W4 — Walkthrough W0: project conjuration scenario

**Problem.** Every walkthrough scenario (W1–W7 in
`steward-lifecycle-walkthrough.md`) starts from a pre-seeded project.
We don't test the steward conjuring a new project from a template —
which is the load-bearing first turn of the "Agent-driven mode"
script in `discussions/agent-driven-mobile-ui.md` §11.

**Fix.** Insert **W0: project conjuration** at the head of the
walkthrough. Scenario shape:

1. Director (via overlay): *"set up a research project to compare
   X vs Y."*
2. Steward calls `projects.create({name, template_id: "research"})`
   — returns `<new_id>`.
3. Steward emits `mobile.navigate(uri="termipod://project/<new_id>")`.
4. Director's screen flips to the new project's Overview, parked at
   the `idea` phase, with the freshly-seeded `scope-ratified` AC
   visible in the chassis.
5. Steward calls `documents.create({project_id, phase: "idea",
   kind: "memo", body: ...})` — idea memo lands; visible via the
   new Documents tile (v1.0.483).
6. Director: *"advance to lit-review."* Steward calls
   `phase.advance({project_id, to: "lit-review"})`. Tiles + ribbon
   update.

Companion test doc gets a matching W0 step-by-step section
(Goal / Steps / Expected / Failure modes), and the existing W1–W7
become W1–W8 (or stay numbered with W0 prepended — pick during
implementation).

**Done criteria.**
- `steward-lifecycle-walkthrough.md` has W0 documented with the
  same Goal / Steps / Expected / Failure-modes shape.
- `test-steward-lifecycle.md` has a matching W0 section.
- Memory entry `project_steward_lifecycle_walkthrough.md` updated
  to reference W0.
- The full walkthrough is still ≤ 20 min on a fresh seed.

---

### W5 — Configurable `phase_specs[<phase>].tiles` end-to-end

**Problem.** Today's tile composition is hardcoded in
`_researchPhaseTiles` (`shortcut_tile_strip.dart:60-74`). Adding /
removing / reordering tiles requires a code edit + APK rebuild.
Neither the steward nor the user can shape tiles for a specific
project, even though `phase_specs[<phase>].overview_widget` is
already YAML-driven (W7, v1.0.359). Tiles are the natural next field
along the same axis.

**Fix.** Three-layer override chain:

1. **Project override** — new column
   `projects.phase_tiles_overrides_json` (TEXT, default `NULL`).
   Shape: `{"<phase>": ["documents", "outputs", ...]}`. PATCHable
   via `projects.update` MCP tool (steward) + new mobile sheet
   (user, W6).
2. **Template default** — extend `phaseSpecsHead` in
   `template_hydration.go` with a `Tiles []string` field. Hub
   serves `phase_specs[<phase>].tiles` as part of the project
   payload (new endpoint or inline on `GET /v1/teams/{team}/
   projects/{id}`).
3. **Chassis default** — `_chassisDefault = [outputs, documents]`
   stays in Dart as the final fallback.

Mobile resolution (in `resolveTilesForPhase`):
1. If project payload carries `phase_tiles_overrides[<phase>]` →
   parse + filter to known slugs → return.
2. Else if project payload carries
   `phase_specs[<phase>].tiles` → same.
3. Else fall back to hardcoded `_researchPhaseTiles` (kept as a
   safety net during rollout) → finally `_chassisDefault`.

`TileSlug` stays a closed Dart enum — unknown slug strings from
override / YAML are dropped during parse (matching the existing
`_slugFromString` semantics). This enforces the ADR-023 D8 contract:
the vocabulary is APK-bound, the composition is data.

**Done criteria.**
- Migration adds `phase_tiles_overrides_json` column.
- Template YAML reader picks up new `tiles:` field per phase.
- `projects.update` accepts `phase_tiles_overrides`.
- Mobile resolves override → YAML → chassis default in order.
- v1.0.483's hardcoded `'idea': [TileSlug.documents]` becomes a
  template YAML edit, not a Dart edit (or stays as Dart safety net).
- New unit test asserts the resolution order.

---

### W6 — On-device tile editor sheet

**Problem.** The user shouldn't have to ask the steward "add
Documents tile" to compose their own project shortcuts. Direct
mobile editing is faster + matches the chassis-config UX
principle (user IS director).

**Fix.** Pencil affordance on the Overview's tile strip header
("Shortcuts ✎"). Tap → modal sheet:

- Vertical list of all `TileSlug` enum values (closed vocabulary).
- Each row: checkbox (in/out of this phase's tile set) + drag
  handle (reorder). Drag uses the existing
  `ReorderableListView` Flutter primitive.
- Footer: "Reset to template" (drops the project override for this
  phase, falls back to YAML/chassis) + Save / Cancel.
- Save → optimistic PATCH `projects.phase_tiles_overrides[<phase>]`
  + revert on failure (same pattern as the rest of lifecycle UI).
- Default-template tiles are visually marked so the user knows what
  they're overriding vs. adding.

Steward-driven and user-driven editing share the storage — both
write the same `phase_tiles_overrides_json` field.

**Done criteria.**
- Tile strip carries a pencil icon (Edit) — opens the sheet.
- Sheet lists all `TileSlug` values, marks current selection,
  supports reorder + reset.
- Save → PATCH → tile strip refreshes optimistically.
- Reset-to-template restores hub default; project override clears.
- Widget test covers the sheet's checkbox + drag behaviour and the
  "no-change" path (no PATCH fired if the user opens + closes
  without changes).

---

## Test plan

- **W1.** Open Plans tile from research-method-demo → see 1 plan,
  not 5. Open via AppBar Search → still see all 5.
- **W2.** `seed-demo --shape lifecycle --reset && --shape lifecycle`
  → assert every `plan_step.kind` ∈ valid set; assert each project
  has ≥ 1 task row; manually scroll Plans tile + Tasks tab for
  realism.
- **W3.** Run `lint-glossary.sh` → green. Manually grep
  `docs/spine/*` `docs/reference/*` for `plan` / `phase` / `task`
  usage; check the prose still reads cleanly.
- **W4.** Run the new W0 scenario from a fresh seed — steward
  creates project, navigates, hydrates idea memo, advances phase.
- **W5.** Unit test for `resolveTilesForPhase` resolution order;
  manual: edit a project's YAML tiles, refresh on mobile, observe
  the change without rebuilding.
- **W6.** Open the editor sheet, toggle Documents off, save → tile
  disappears. Tap Reset → tile returns. Widget tests cover the
  state transitions.

## Open questions

- **Q1 — Should W5 also extend the steward-config template spec?**
  Today's `research-template-spec.md` §3 lists `tiles: [Documents]`
  as the YAML schema for idea phase. The new override layer adds a
  *project-level* override on top. Spec doesn't change shape; only
  the resolution chain documentation grows. Confirm during
  implementation.
- **Q2 — Where does the Edit pencil live on the tile strip?** Either
  inline as a small icon at the top-right of the strip, or as a
  trailing tile ("➕ Edit shortcuts"). Inline is less discoverable;
  trailing tile takes a row. Lean trailing-tile; revisit on QA.
- **Q3 — Should the tile editor sheet expose label / icon overrides?**
  Today the chip label + icon are owned by `tileSpecFor()` in the
  chassis. Per-project label overrides would need a richer JSON
  shape (`{slug, label?, icon?}` instead of `[slug, ...]`). Punt
  to a follow-up; ship slug-only composition first.

## Status

- **2026-05-11** — Plan drafted. Wedges scoped, dependencies
  identified, open questions captured. Awaiting principal review +
  implementation start.

## Related

- ADR-023 — agent-driven mobile UI (D8 = Tier 1 first for
  agent-conjured surfaces; this plan is the composition axis,
  Tier 1 stays in
  [`agent-artifact-rendering-tier-1.md`](agent-artifact-rendering-tier-1.md))
- [`steward-lifecycle-walkthrough.md`](steward-lifecycle-walkthrough.md)
  — W0 (W4 here) folds into the existing scenario list
- [`research-template-spec.md`](../reference/research-template-spec.md)
  §3 — phase tile mapping
- [`discussions/agent-driven-mobile-ui.md`](../discussions/agent-driven-mobile-ui.md)
  §11 (the demo script W0 mirrors) + §12 (Tier 1/2/3 tile vocab
  framing)

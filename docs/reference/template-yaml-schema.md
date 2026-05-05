# Project template YAML schema

> **Type:** reference
> **Status:** Draft (2026-05-05) — schema not yet shipped; pending plan + ADR
> **Audience:** contributors (hub backend, template authors)
> **Last verified vs code:** v1.0.351

**TL;DR.** Authoring contract for **project template YAMLs** — the
chassis-level declarations a project template uses to drive
phase-aware UI, deliverable specs, acceptance criteria, transitions,
overview widgets, and steward spawn policy. Distinct from
[`steward-templates.md`](steward-templates.md) (which declares
**agent** spawn shape). Project template YAMLs sit at
`hub/templates/projects/<id>.v1.yaml` (bundled) with overlay at
`<DataRoot>/teams/<team>/templates/projects/<id>.yaml`. Existing
`projects[is_template=1]` rows continue to be the user-facing
"instantiable template" registry; YAMLs add the chassis declarations
those rows reference via a new `spec_yaml_path` column. All
declarations are loaded at hub startup and validated against a
versioned schema; loading is fail-closed for bundled files (build-time
test) and fail-soft for overlays (warn + skip overlay, fall back to
bundled).

---

## 1. Why this reference / scope

The lifecycle work (D1–D10 in
[`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md))
introduced phases, deliverables, and acceptance criteria as chassis
primitives. Templates declare their *content*: which phases exist,
which deliverables each phase produces, what their components look
like, what acceptance criteria gate advancement, etc. This file is the
canonical schema.

**In scope:**
- File layout, naming, IDs
- Top-level YAML shape + every declaration block
- Per-phase deliverable + criterion + widget + tile bindings
- Phase transition rules (D4, D6)
- Document section schemas (D7) — inline within template for MVP
- Steward spawn policy (§6.5 of the discussion)
- Loading, caching, overlay resolution, hot-reload behavior
- Validation rules
- Versioning + format-bump policy

**Out of scope:**
- The existing `steward-templates.md` agent-template schema —
  separate file, distinct schema.
- Hub schema for `projects` / `deliverables` / etc. — see
  [`project-phase-schema.md`](project-phase-schema.md).
- Hub HTTP API surface — `hub-api-deliverables.md` (TBD).
- Mobile rendering of the declarations — viewer specs (TBD).
- The research template's specific contents — covered by
  `research-template-spec.md` (TBD).

---

## 2. Relation to existing `projects[is_template=1]`

Today, a "project template" is a row in `projects` with
`is_template=1`. The Project-Create flow shows these rows in a picker
(`lib/screens/projects/project_create_sheet.dart`), and instantiation
copies the row's `goal`, `template_id` reference, and
`parameters_json`. This continues to be the instantiable-template
registry.

The new YAML files supply **chassis-level declarations** that the
project-template row references:

```
projects (one row per template, is_template=1)
  ├─ id, name, goal, parameters_json, ...   (existing)
  └─ spec_yaml_path TEXT NULL               (NEW — points at a bundled
                                              or overlay YAML by id)
```

**Resolution:** when the hub loads, it walks
`hub/templates/projects/*.yaml` and team overlay paths, parses each,
keeps an in-memory `projectTemplateSpec[id]` map. When a project is
created from a template row, the hub looks up the row's
`spec_yaml_path` to load chassis declarations + applies them to the
new project's `phase`, deliverables seed, criteria seed, etc.

**Bundled vs overlay** mirrors steward-templates resolution:

```
hub/templates/projects/                            # bundled
├── research.v1.yaml
├── ablation-sweep.v1.yaml                         # legacy seed; phase-less
└── workspace.v1.yaml                              # standing-kind, no phases

<DataRoot>/teams/<team>/templates/projects/        # overlay (per-team)
└── research.v1.yaml                               # overrides bundled if same id
```

Resolution rule: overlay wins; bundled is the fallback. **Exception:**
if a hub-side feature flag pins a template (e.g.,
`research.frozen=true` in hub config), overlay is ignored — useful
for the demo deploy.

---

## 3. File layout + naming

```
hub/templates/projects/<id>.<format-version>.yaml
```

- `<id>` is dot-separated lowercase (`research`, `ablation-sweep`,
  `workspace`, `feature-development`, …). Stable across format-version
  bumps.
- `<format-version>` is `v1`, `v2`, … This is the *YAML schema*
  version, not the template-content version. Bump only when the schema
  is incompatible (rare).
- One template per file. Section schemas inline (§7).
- Sidecar prompt files at `hub/templates/projects/prompts/<id>.<phase>.md`
  for per-phase steward prompt variants (§13).

Template ids are global — overlay and bundled with the same id resolve
to a single registered template.

---

## 4. Top-level shape

```yaml
template: research                       # required; matches filename id
format_version: 1                        # required; matches filename .v1.yaml
template_version: 3                      # required; bump on content change
display_name: "Research project"         # required
description: |                            # required, 1–3 short sentences
  AI-for-science research lifecycle: idea → literature review →
  method → experiment → paper. Suitable for ablation studies,
  agent-driven exploration, and any project producing a paper.

kind: goal                               # required; goal | standing
                                         # 'standing' templates have NO phases

# Chassis-orthogonal seeds (existing today, preserved here for completeness)
parameters_schema: { ... }               # optional; JSON Schema for parameters_json
on_create_steward_template: steward.research.v1
                                         # optional; agent template id
default_overview_widget: portfolio_header
                                         # optional; chassis fallback if no
                                         # phase-specific widget declared

# NEW: lifecycle declarations
phases:                                  # required iff kind=goal; absent if kind=standing
  - id: idea
    display_name: "Idea"
    abbrev: "Idea"                       # short label for phase ribbon
    overview_widget: idea_conversation   # registry slug
    tiles: [Discussion]                  # phase-filtered tile set
    deliverables: []                     # 0..N per phase
    criteria:
      - id: scope-ratified
        kind: text
        body:
          text: "Director ratifies overall scope and direction."
        required: true
    steward_spawn: eager                 # eager | lazy | phase-triggered

  - id: initiation
    display_name: "Initiation"
    abbrev: "Init"
    overview_widget: deliverable_focus
    tiles: [References, Risks]
    deliverables:
      - id: proposal
        kind: proposal
        display_name: "Proposal"
        ratification_authority: director
        components:
          - kind: document
            ref: proposal-doc            # doc id within this template
            required: true
        section_schema:                   # only meaningful for kind=document components
          ref: proposal-doc-sections     # references a section_schemas[ref] below
    criteria:
      - id: proposal-ratified
        kind: gate
        body:
          gate: deliverable.ratified
          params: { deliverable_id: proposal }
        required: true
    transitions_in: { from: idea, mode: explicit }

# more phases: lit-review, method, experiment, paper ...

# Document section schemas (D7) — inline; can be reused across phases by ref id
section_schemas:
  proposal-doc-sections:
    schema_id: research-proposal-v1
    sections:
      - slug: motivation
        title: "Motivation"
        required: true
      - slug: sota
        title: "State of the art / Related work"
        required: true
      - slug: method
        title: "Method"
        required: true
      - slug: risks
        title: "Risks"
        required: false
      - slug: budget
        title: "Budget"
        required: false
      - slug: acceptance
        title: "Acceptance criteria"
        required: true

# Transition rules (D4, D6) — declared at template level for cross-phase rules
transitions:
  - from: idea
    to: initiation
    mode: explicit                       # explicit (default) | auto
  - from: initiation
    to: lit-review
    mode: explicit
  # auto example:
  - from: experiment
    to: paper
    mode: auto
    auto_when:
      all_required_criteria: true
      and_metric_holds_for_minutes: 5    # debounce automation

# Steward overlay (optional)
steward_prompt_overlays:                  # appended to base steward prompt per phase
  initiation: prompts/research.initiation.md
  experiment: prompts/research.experiment.md

# Documentation pointers (optional, surfaced in mobile)
docs:
  proposal_template_md: docs/templates/research-proposal.md
```

Every block is detailed below.

---

## 5. Phase declarations (`phases:`)

Required when `kind: goal`. **Absent or empty** when `kind: standing`
(workspaces have no phase model).

Per-phase fields:

| Field | Required | Notes |
|---|---|---|
| `id` | yes | Stable identifier; matches `projects.phase` value (D1). Lowercase dash-separated. |
| `display_name` | yes | UI-facing full name. |
| `abbrev` | yes | Short label for phase ribbon (≤8 chars recommended). |
| `overview_widget` | no | Slug into the chassis overview-widget registry. Falls back to `default_overview_widget` if absent. |
| `tiles` | no | Phase-filtered shortcut tile set. Empty list means "no shortcut tiles for this phase". |
| `deliverables` | no | 0..N deliverables (D8). |
| `criteria` | no | 0..N phase-level criteria (D9). |
| `steward_spawn` | no | `eager` (default) \| `lazy` \| `phase-triggered`. Per §6.5 of discussion. |
| `transitions_in` | no | Convenience: declares the inbound transition's `mode`. Equivalent to a top-level `transitions:` entry. |

**Phase order** is the YAML list order. Templates declare phases as
*ordered but skippable* (D4) — order matters for the phase ribbon
visual and for the auto-advance rules; transitions can skip steps when
declared.

---

## 6. Deliverable declarations (`phases[i].deliverables`)

Per D8: 0..N per phase, template-declared cardinality + component
requirements + ratification authority.

```yaml
deliverables:
  - id: proposal                         # required; unique within phase
    kind: proposal                       # required; freeform string
    display_name: "Proposal"             # required
    description: |                       # optional
      End-of-Initiation deliverable. Section-targeted authoring
      with the project steward; ratified as a whole when all required
      sections are ratified.
    ratification_authority: director     # required; director | council | auto
    required: true                       # default true
    ord: 0                               # optional; default by list order
    components:
      - kind: document                   # closed enum: document | artifact | run | commit
        ref: proposal-doc                # for document: refs a section_schemas key
        required: true
        ord: 0
      - kind: artifact                   # for non-document, ref is freeform; the
        ref: budget-spreadsheet          #   instantiated component populates ref_id
        required: false                  #   from runtime artifacts
    section_schema:                      # convenience: shorthand if there's exactly one
      ref: proposal-doc-sections         #   document component
```

**Component `kind` enum (closed for MVP):**

- `document` — `ref` is a key in `section_schemas:`. The instantiated
  component creates a Document row with that section schema.
- `artifact` — `ref` is a freeform name; populated when a real artifact
  is bound at runtime (e.g., uploaded by steward).
- `run` — `ref` is a freeform name; populated when a real run is bound
  (e.g., the steward dispatches a worker run and pins its id).
- `commit` — `ref` is a freeform name; populated by host-runner
  attestation.

**Ratification authority enum:**

- `director` — explicit director ratification required (default).
- `council` — N-of-M council approval (post-MVP; rejected at runtime in
  MVP if specified, with a warning).
- `auto` — chassis ratifies when all required components are
  themselves ratified and all phase criteria are met. Subject to D6 +
  the §B.5 ratify-prompt resolution: even `auto` deliverables post a
  ratify-prompt attention item rather than silently advancing the
  phase, *unless* `transitions[]` for the outbound transition is also
  `mode: auto` *and* the director has opted in via project settings.

---

## 7. Document section schemas (`section_schemas:`)

Per D7: declared inline within the template for MVP. Sections are the
authoring grain for typed structured documents; section state is
3-state (`empty | draft | ratified`) per the 2026-05-05 closure.

```yaml
section_schemas:
  proposal-doc-sections:                 # key; referenced by deliverables[].components[].ref
    schema_id: research-proposal-v1      # stable across template_version bumps;
                                         #   stored on documents.schema_id
    sections:
      - slug: motivation                 # stable identifier; used for section-targeted
        title: "Motivation"              #   sessions and audit events
        required: true                   # required for deliverable to be ratifiable
        guidance: |                      # optional; surfaced in editor as helper
          Why does this project matter? What outcome does success enable?
      - slug: method
        title: "Method"
        required: true
        guidance: |
          High-level approach. Specifics live in the Method deliverable.
      # ... more sections
```

**Slug stability:** `slug` is the durable identifier. Renaming `title`
is safe; renaming `slug` requires a migration step on existing
documents.

**Adding sections to an existing schema:** template_version bump +
runtime migration (TBD post-MVP). For MVP, schemas are append-only —
new sections appended at the end of `sections:`; deletion or reorder
is a breaking change.

**Section reuse across templates** (post-MVP): section schemas could
be promoted to top-level files (`hub/templates/section_schemas/*.yaml`)
when reuse emerges. MVP keeps them inline.

---

## 8. Acceptance criteria declarations (`phases[i].criteria`)

Per D5/D9: 3-kind enum (`text | metric | gate`). Phase-keyed; can
optionally reference a deliverable. Per the 2026-05-05 §B.5 closure:
metric criteria post a ratify-prompt rather than auto-advancing.

```yaml
criteria:
  - id: scope-ratified                   # required; unique within phase
    kind: text                           # text | metric | gate
    body:
      text: "Director ratifies overall scope and direction."
    deliverable_ref: null                # optional; ref to a deliverable id in this phase
    required: true                       # default true
    ord: 0

  - id: proposal-ratified
    kind: gate
    body:
      gate: deliverable.ratified         # well-known chassis gates (§8.3)
      params: { deliverable_id: proposal }
    deliverable_ref: proposal
    required: true

  - id: eval-accuracy-threshold
    kind: metric
    body:
      metric: experiment.eval_accuracy   # template-declared metric path
      operator: ">="
      threshold: 0.85
      evaluation: auto                   # auto | manual
      source_run_filter:
        tag: ablation-final
    deliverable_ref: experiment-results
    required: true
```

### 8.1 `kind: text` — free-text criterion

`body.text` is the human-readable statement. Marked met by an actor
(director or steward) explicitly. No automation.

### 8.2 `kind: metric` — automated threshold

Hub watches the metric (sourced from a run, an artifact, or an
explicit measurement). When `operator(value, threshold)` holds, hub
posts an attention item to ratify the criterion (D6 + §B.5). Director
confirms → `state` transitions `pending → met`.

| Field | Required | Notes |
|---|---|---|
| `metric` | yes | Dotted path; resolution is template-specific |
| `operator` | yes | `>=` \| `<=` \| `>` \| `<` \| `==` |
| `threshold` | yes | Number |
| `evaluation` | yes | `auto` (hub watches) \| `manual` (director enters value) |
| `source_run_filter` | no | If absent, latest matching run wins |

### 8.3 `kind: gate` — well-known chassis gate

Templates reference chassis-defined gates (no template logic). MVP
gate library:

| Gate handle | Meaning | Required `params` |
|---|---|---|
| `deliverable.ratified` | Specified deliverable is ratified | `deliverable_id` |
| `all-sections-ratified` | All required sections of a doc are ratified | `document_id` (resolved at runtime) |
| `phase.has-no-open-attention` | No open attention items reference this phase | none |
| `runs.completed-without-error` | All runs bound to a deliverable component completed | `deliverable_id` |

The gate library is closed for MVP (per the ethos of D8 component
enum). Templates needing a custom gate file an issue rather than
extending the YAML.

---

## 9. Transition rules (`transitions:`)

Top-level cross-phase transition declarations (D4, D6).

```yaml
transitions:
  - from: idea
    to: initiation
    mode: explicit                        # explicit | auto
  - from: initiation
    to: method                            # skip lit-review (D4 — phases skippable)
    mode: explicit
  - from: experiment
    to: paper
    mode: auto
    auto_when:
      all_required_criteria: true         # default condition
      and_metric_holds_for_minutes: 5     # optional debounce
      and_director_opted_in: true         # required for auto-advance per §B.5
```

**Mode semantics:**

- `explicit` (default) — phase advances only on explicit director
  ratification action.
- `auto` — chassis can advance phase when `auto_when` holds, **but**
  per the 2026-05-05 §B.5 closure, auto-advance posts a ratify-prompt
  attention item by default. Project settings can opt into truly silent
  auto-advance per-transition.

**Skippability:** transitions whose `from` is not the current phase but
a *predecessor* phase let the chassis re-enter a phase (rare;
admin-only). Phase ribbons render skipped phases as muted/strike-
through.

---

## 10. Overview widget bindings

The chassis maintains a registry of overview widget slugs:
`children_status`, `sweep_compare`, `recent_artifacts`,
`task_milestone_list`, `workspace_overview`, plus new lifecycle slugs
(`idea_conversation`, `deliverable_focus`, `paper_acceptance`, …).

Templates bind a slug per phase:

```yaml
phases:
  - id: idea
    overview_widget: idea_conversation
  - id: initiation
    overview_widget: deliverable_focus
```

Plus a template-level fallback:

```yaml
default_overview_widget: portfolio_header
```

Resolution at runtime: phase-specific binding wins; otherwise template
default; otherwise chassis default (`portfolio_header` for goal-kind,
`workspace_overview` for standing-kind). Unknown slug → log warning,
fall back.

---

## 11. Tile bindings (`phases[i].tiles`)

Per-phase shortcut tile set. Closed enum of well-known tile slugs:

| Slug | Meaning |
|---|---|
| `Outputs` | Artifacts viewer |
| `Documents` | Document list (free-floating + deliverable-bound) |
| `Schedules` | Schedules screen |
| `Plans` | Plans screen |
| `Assets` | Media browser |
| `Experiments` | Runs screen (research-bias label) |
| `References` | Citation / SOTA library (Initiation-relevant) |
| `Risks` | Risk register (Initiation-relevant) |
| `Discussion` | Project channel sheet (D10 — replaces former tab) |

Templates pick a subset per phase. Empty list = no shortcut tiles for
that phase. The chassis-default tile set (`Outputs`, `Documents`)
applies if `tiles:` is absent on a phase.

Adding new tile slugs requires a chassis change (registry); templates
cannot invent slugs.

---

## 12. Steward spawn policy (`phases[i].steward_spawn`)

Per §6.5 of the discussion. Three modes:

- **`eager`** (default for goal-kind templates): project steward
  spawned at project creation, before Idea phase even completes.
- **`lazy`**: spawned on first Initiation interaction. Cheap; first
  interaction slower.
- **`phase-triggered`**: spawned at the entry of the declared phase.
  E.g., research projects could phase-trigger spawn at Initiation
  rather than Idea, to save tokens during scope-only conversation.

Declared per-phase as a *threshold* — the spawn happens at the
*earliest* phase whose value isn't `lazy`. So:

```yaml
phases:
  - id: idea
    steward_spawn: lazy        # do not spawn yet
  - id: initiation
    steward_spawn: eager       # spawn here
```

is equivalent to `phase-triggered: initiation`. The two-level
declaration (`per-phase` rather than top-level) reads more naturally
for templates with several "spawn checkpoints" (e.g., spawn a
specialized worker per phase).

---

## 13. Steward prompt overlays (`steward_prompt_overlays`)

Per W7: the project steward's prompt can vary by phase. Overlays append
to the base steward prompt (loaded from
`hub/templates/agents/steward.<domain>.v1.md` per
[`steward-templates.md`](steward-templates.md)).

```yaml
steward_prompt_overlays:
  initiation: prompts/research.initiation.md
  experiment: prompts/research.experiment.md
```

The `prompts/...` paths are relative to the project template's
directory (`hub/templates/projects/`). They are markdown files (no
YAML) and are concatenated to the steward prompt in
`base + overlay[current_phase]` order.

If no overlay matches the current phase, the base prompt alone is
used.

---

## 14. Loading, caching, overlay

**Load timing:**

- At hub startup, walk `hub/templates/projects/*.yaml` (bundled via
  embed.FS) and the team's overlay path
  `<DataRoot>/teams/<team>/templates/projects/*.yaml`.
- Parse + validate (§15). On bundled-file errors → fail-closed (hub
  refuses to start; this is a build-time test). On overlay-file errors
  → fail-soft (warn in logs, skip the file, fall back to bundled).
- Build an in-memory `projectTemplateSpec[id]` map.

**Hot-reload (overlay only):**

- Watch overlay paths via fsnotify; reload on change. Validate before
  swap. On error: keep the previous in-memory spec; warn.
- Bundled files do not hot-reload; they require a hub restart (they're
  embedded).

**Caching:** templates are loaded once into memory; queries are
in-memory map lookups. No DB persistence of YAML content.

**Mobile-side:** the hub exposes a `GET /v1/project-templates`
endpoint (TBD in `hub-api-deliverables.md`) returning the parsed
specs. Mobile caches in `HubSnapshotCache` (sqflite) for offline use.

---

## 15. Validation rules (hub-enforced at load)

1. **`template` matches filename id.** Reject mismatch.
2. **`format_version` ≤ hub's max supported version.** Forward-compat
   skip-with-warning if newer.
3. **Phase ids unique.** Within `phases[]`.
4. **Deliverable ids unique within phase.** Across phases need not be
   unique; runtime composite key is `(project_id, phase, deliverable_id)`.
5. **Criterion ids unique within phase.**
6. **Section slugs unique within a section_schema.**
7. **All `deliverable_ref` and `components[].ref` resolve.** Reject
   dangling references.
8. **All `overview_widget` slugs known.** Warn (not reject) on unknown;
   fall back to default.
9. **All `tiles[]` slugs known.** Same warn-not-reject.
10. **Transition graph is well-formed.** No cycles; every `from` and
    `to` matches a declared phase id; only one transition per ordered
    pair.
11. **`ratification_authority: council`** is rejected at runtime in MVP
    with a warning if used (§6 — no councils until post-MVP).
12. **`section_schemas[*].sections` is append-only across versions.**
    Detected via stored history of schema_id evolutions; deletion or
    reorder is a breaking change. (TBD: persistence of this history.)

---

## 16. Versioning + format-bump policy

- **`format_version`** (this schema's version): bump only on
  schema-incompatible changes. Hub maintains parsers for all
  `format_version`s currently in service. Plan to keep N-1 supported.
- **`template_version`** (this template's content version): bump
  whenever any block changes. Surfaced in mobile so directors see
  "new template version available — re-instantiate?" affordance.
- **Section schema evolution:** append-only for MVP; full migration
  semantics deferred.

A template-spec change does NOT retroactively migrate already-
instantiated projects. Existing projects continue with their
instantiated phase set. Re-instantiation explicitly opts into the new
template_version.

---

## 17. Examples

### 17.1 Minimal — workspace template (no phases)

```yaml
template: workspace
format_version: 1
template_version: 1
display_name: "Workspace"
description: "Standing project for ongoing work without a defined endpoint."
kind: standing
default_overview_widget: workspace_overview
on_create_steward_template: steward.research.v1
```

No `phases:` block (kind=standing). All today's workspace mechanics
unchanged.

### 17.2 Minimal — flat goal template (no phases, today's behavior)

```yaml
template: ablation-sweep
format_version: 1
template_version: 1
display_name: "Ablation sweep"
description: "Run a benchmark grid; emit a digest."
kind: goal
default_overview_widget: sweep_compare
on_create_steward_template: steward.research.v1
parameters_schema:
  type: object
  required: [host_id, sweep_grid]
  properties:
    host_id: { type: string }
    sweep_grid: { type: object }
```

No `phases:` either — represents legacy/single-phase goal projects
and demonstrates that lifecycle is **opt-in**.

### 17.3 Full — research template (5 phases)

The complete research template specification is `research-template-spec.md`
(TBD); this file declares the *schema*, not the content.

A skeleton:

```yaml
template: research
format_version: 1
template_version: 1
display_name: "Research project"
description: "AI-for-science research lifecycle: idea → lit-review → method → experiment → paper."
kind: goal

default_overview_widget: portfolio_header
on_create_steward_template: steward.research.v1

phases:
  - id: idea
    display_name: "Idea"
    abbrev: "Idea"
    overview_widget: idea_conversation
    tiles: [Discussion]
    deliverables: []
    criteria:
      - id: scope-ratified
        kind: text
        body: { text: "Director ratifies scope direction." }
        required: true
    steward_spawn: eager

  - id: lit-review
    display_name: "Literature review"
    abbrev: "Lit-rev"
    overview_widget: deliverable_focus
    tiles: [References, Documents]
    deliverables:
      - id: lit-review-doc
        kind: lit-review
        display_name: "Literature review"
        ratification_authority: director
        components:
          - kind: document
            ref: lit-review-sections
            required: true
    criteria:
      - id: lit-review-ratified
        kind: gate
        body:
          gate: deliverable.ratified
          params: { deliverable_id: lit-review-doc }
        required: true

  # ... method, experiment, paper

section_schemas:
  proposal-doc-sections: { ... }
  method-doc-sections: { ... }
  experiment-report-sections: { ... }
  paper-draft-sections: { ... }
  lit-review-sections: { ... }

transitions:
  - { from: idea, to: lit-review, mode: explicit }
  - { from: lit-review, to: method, mode: explicit }
  - { from: method, to: experiment, mode: explicit }
  - { from: experiment, to: paper, mode: explicit }

steward_prompt_overlays:
  initiation: prompts/research.initiation.md
  experiment: prompts/research.experiment.md
```

Full body in `research-template-spec.md` (A6).

---

## 18. Open follow-ups

1. **Section schema evolution.** Append-only for MVP; persistent
   migration history (audit table, tooling) deferred.
2. **Section schema reuse across templates.** When emerges; defer
   until then.
3. **Custom gate handles.** Closed library for MVP; revisit when a
   real custom-gate need surfaces.
4. **Template inheritance / composition.** Today templates are
   self-contained YAMLs. Inheritance ("research-with-codex extends
   research") deferred — copy-paste for now.
5. **Mobile-side template editor.** Reading templates is in scope;
   editing through the app is post-MVP. Today's authoring is
   filesystem-only.
6. **Ratification authority `auto` semantics.** Per §6: subject to
   §B.5 ratify-prompt closure. Edge cases (criterion fails after
   auto-ratify is queued) need plan-grain detail.

---

## 19. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — design discussion + D1–D10 decisions
- [`reference/project-phase-schema.md`](project-phase-schema.md) —
  hub schema for phases, deliverables, criteria
- [`reference/steward-templates.md`](steward-templates.md) — agent
  template authoring contract; sibling system
- [`reference/frame-profiles.md`](frame-profiles.md) — YAML authoring
  pattern + overlay precedent
- [`decisions/010-frame-profiles-as-data.md`](../decisions/010-frame-profiles-as-data.md)
  — YAML-as-config principle
- [`decisions/017-layered-stewards.md`](../decisions/017-layered-stewards.md)
  — general / project steward + spawn rules
- `reference/research-template-spec.md` (TBD) — full research template
  content
- `reference/structured-document-viewer.md` (TBD) — section-aware
  viewer that consumes section schemas declared here
- `reference/structured-deliverable-viewer.md` (TBD) — composes the
  document viewer with deliverable + criterion panels
- `reference/hub-api-deliverables.md` (TBD) — HTTP endpoints for
  template loading + project instantiation

# Project lifecycle schema reference

> **Type:** reference
> **Status:** Current (2026-05-05) — W1 shipped at v1.0.352 (migration 0034)
> **Audience:** contributors (hub backend, mobile)
> **Last verified vs code:** v1.0.352

**TL;DR.** Schema specification for the project-lifecycle work
discussed in
[`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md).
Adds three new tables (`deliverables`, `deliverable_components`,
`acceptance_criteria`), extends two existing tables (`projects`,
`documents`), defines JSON shapes for structured document bodies and
typed criterion bodies, enumerates new audit event kinds, and locks
migration + backwards-compatibility rules. Schema is **chassis-only**
— no research-template-specific tables. All template-specific content
(phase set, section schemas, criterion specs, transitions) lives in
template YAML and is loaded at runtime, not stored as schema. Consumed
by the schema-wedge of the lifecycle plan (W1, W5b, W6); informs the
template-YAML-schema reference (sister doc, TBD).

---

## 1. Why this reference exists / scope

The lifecycle discussion locked ten design decisions (D1–D10) but did
not pin them at engineering grade. This reference does that: exact
DDL, FK rules, indexes, JSON shapes, audit kinds, migration plan,
backwards-compat rules. An engineer reading only this doc should be
able to write a hub-side migration without consulting the discussion.

**In scope:**
- New + modified table DDL
- JSON shapes for non-relational bodies
- Index strategy for the expected query patterns
- FK rules + ON DELETE behavior
- Audit event kinds added to `audit_events` (see
  [`audit-events.md`](audit-events.md))
- Migration order + backfill defaults
- Backwards-compatibility guarantees for existing projects + documents
- Validation rules enforced at the hub

**Out of scope:**
- Hub HTTP API surface — covered in `reference/hub-api-deliverables.md`
  (TBD).
- Template YAML schema — covered in `reference/template-yaml-schema.md`
  (TBD).
- Mobile rendering — covered in
  `reference/structured-document-viewer.md` and
  `reference/structured-deliverable-viewer.md` (TBD).
- Research template content — covered in
  `reference/research-template-spec.md` (TBD).

---

## 2. New tables

### 2.1 `deliverables`

Phase-bound bundle of components + criteria; the unit of acceptance
that gates phase advancement (D8).

```sql
CREATE TABLE deliverables (
  id                  TEXT PRIMARY KEY,
  project_id          TEXT NOT NULL,
  phase               TEXT NOT NULL,
  kind                TEXT NOT NULL,        -- template-declared, freeform
  ratification_state  TEXT NOT NULL DEFAULT 'draft'
                          CHECK (ratification_state IN ('draft','in-review','ratified')),
  ratified_at         TEXT,                 -- ISO-8601 UTC
  ratified_by_actor   TEXT,                 -- actor_kind:actor_id format
  required            INTEGER NOT NULL DEFAULT 1,
  ord                 INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at          TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
```

Notes:
- `kind` is a freeform string declared by template (e.g. `proposal`,
  `method`, `experiment-results`, `paper`). Hub does not validate
  against an enum; templates carry the schema.
- `ratification_state` is a chassis enum: `draft → in-review →
  ratified`. The intermediate `in-review` state is kept here even
  though document *sections* drop it (D7) — at deliverable level, the
  three-state flow models the director-mediated review (steward
  proposes → director reviews → director ratifies).
- `required` is the *deliverable's* requiredness for phase advance,
  not its components'. A non-required deliverable does not block phase
  advancement; the template declares.
- `ord` orders multiple deliverables within a phase (template-declared
  order; not user-mutable).
- `updated_at` is bumped by triggers or by the hub on every mutation.

### 2.2 `deliverable_components`

Typed reference to the actual content of a deliverable. Closed enum
component kinds for MVP (D8).

```sql
CREATE TABLE deliverable_components (
  id              TEXT PRIMARY KEY,
  deliverable_id  TEXT NOT NULL,
  kind            TEXT NOT NULL
                      CHECK (kind IN ('document','artifact','run','commit')),
  ref_id          TEXT NOT NULL,
  required        INTEGER NOT NULL DEFAULT 1,
  ord             INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (deliverable_id) REFERENCES deliverables(id) ON DELETE CASCADE
);
```

Notes:
- `ref_id` is a foreign-key-by-convention into the table implied by
  `kind`:
  - `kind='document'` → `documents.id`
  - `kind='artifact'` → `artifacts.id` (or blob ref; depends on
    existing artifacts schema)
  - `kind='run'` → `runs.id`
  - `kind='commit'` → an opaque commit identifier (host_id + sha or
    the existing commit-tracking shape)
- No DB-level FK across the typed reference (the relation is
  polymorphic). Hub validates referential integrity at write time.
- `required` lets the template mark some components mandatory ("the
  Proposal must have a document component") and others optional ("the
  Experiment-Results may have artifact components").
- Removing a component does not delete the referent. Conversely,
  deleting the referent (e.g., a run) leaves the component row
  pointing at a tombstoned target — hub renders this as
  "component unavailable".

### 2.3 `acceptance_criteria`

Phase-keyed criteria that gate phase advancement (D5/D9). Free-text
criteria are the `kind=text` case; structured criteria layer on top.

```sql
CREATE TABLE acceptance_criteria (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL,
  phase           TEXT NOT NULL,
  deliverable_id  TEXT,                     -- nullable; criteria may reference
                                            --  a deliverable's state, or be free-standing
  kind            TEXT NOT NULL
                      CHECK (kind IN ('text','metric','gate')),
  body            TEXT NOT NULL,            -- JSON; shape depends on kind (§3.2)
  state           TEXT NOT NULL DEFAULT 'pending'
                      CHECK (state IN ('pending','met','failed','waived')),
  met_at          TEXT,
  met_by_actor    TEXT,
  evidence_ref    TEXT,                     -- opaque URI: document section,
                                            --  run id, commit, etc.
  required        INTEGER NOT NULL DEFAULT 1,
  ord             INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  FOREIGN KEY (deliverable_id) REFERENCES deliverables(id) ON DELETE SET NULL
);
```

Notes:
- `deliverable_id` is nullable: a criterion may reference a deliverable
  ("Proposal is ratified"), or be free-standing ("Director ratifies
  scope"). Per D9.
- `state` flow: `pending → met` (success path), `pending → failed`
  (criterion failed; needs intervention), `pending → waived` (director
  explicitly waived; rare).
- Metric criteria (kind=`metric`) become `met` automatically when the
  hub observes the metric meeting the threshold; the hub then posts a
  ratify-prompt attention item per D6 + the §B.5 resolution
  (no auto-advance).
- `evidence_ref` is an opaque URI string. Conventions:
  - `document://<doc_id>#<section_slug>` — points at a section
  - `run://<run_id>` — points at a run
  - `commit://<host_id>/<sha>` — points at a commit
  - `manual://<actor_id>` — director or steward marked it manually

---

## 3. Modified tables

### 3.1 `projects`

Add the phase column + optional history column (D1).

```sql
ALTER TABLE projects ADD COLUMN phase           TEXT;          -- nullable
ALTER TABLE projects ADD COLUMN phase_history   TEXT;          -- nullable JSON
```

Notes:
- `phase` is nullable. NULL means "lifecycle disabled" — the project's
  template did not declare phases (workspaces, legacy goal projects).
  The mobile UI falls back to today's execution-style Overview. New
  projects created from a phase-declaring template get `phase` set to
  the template's first phase on creation.
- `phase_history` (optional) is a JSON array of transitions; see §3.4
  for shape. Transitions are also recorded in `audit_events`, so the
  history column is denormalized for fast read; can be skipped if
  query patterns don't justify it.
- No CHECK constraint on `phase` values; templates own the enum.

### 3.2 `documents`

Add a typed-document marker (D7). Existing `body TEXT` continues to
hold plain markdown for non-typed docs; for typed docs, `body` holds
JSON matching the section shape in §3.4.

```sql
-- documents.kind already exists (migration 0007) as freeform NOT NULL —
-- carries 'memo' / 'draft' / 'report' / 'review' today and gains the new
-- typed-doc senses ('proposal', 'method', 'paper', …) without DDL change.
ALTER TABLE documents ADD COLUMN schema_id   TEXT;       -- nullable, references
                                                         --  template's section schema slug
```

Notes:
- `kind` is the existing freeform NOT NULL column. The MVP does not
  add a second column; the same field carries both the original
  values (memo / draft / report / review, set on doc creation) and the
  new typed-doc senses (proposal / method / paper / experiment-report …,
  set when a template hydrates a typed doc).
- `schema_id` is the typed-document indicator: presence of a non-NULL
  schema_id means "structured document; render via the section-aware
  viewer." NULL means "plain markdown" — preserves the legacy path.
- `schema_id` references a template-declared section schema (not a
  hub-side row). Hub does not validate against template content; the
  rendering layer does.
- `body` column type is unchanged (`TEXT`). Plain docs keep TEXT
  markdown. Typed docs put JSON in TEXT — clients parse based on
  `schema_id`. This avoids a column-type migration.
- A typed doc may exist standalone (free-floating) OR be referenced as
  a `deliverable_components.ref_id` with `kind='document'`. The same
  document can be a deliverable component for at most one
  deliverable in MVP.

---

## 4. JSON shapes

### 4.1 `documents.body` — typed structured document

Used when `documents.kind` is non-NULL and the template declares a
section schema. Sections are first-class with per-section state (D7).

```json
{
  "schema_version": 1,
  "schema_id": "research-proposal-v1",
  "sections": [
    {
      "slug": "motivation",
      "title": "Motivation",
      "body": "<markdown content>",
      "status": "ratified",          // 'empty' | 'draft' | 'ratified'
      "last_authored_at": "2026-05-04T10:15:00Z",
      "last_authored_by_session_id": "sess-abc123",
      "ratified_at": "2026-05-04T11:00:00Z",
      "ratified_by_actor": "user:director-id"
    },
    {
      "slug": "method",
      "title": "Method",
      "body": "",
      "status": "empty",
      "last_authored_at": null,
      "last_authored_by_session_id": null,
      "ratified_at": null,
      "ratified_by_actor": null
    }
  ]
}
```

Notes:
- Section slugs are stable identifiers declared by the template's
  section schema. Renaming a section title does not change the slug.
- `status` enum is **3 states only** per the 2026-05-05 §B.2
  resolution: `empty | draft | ratified`. Drop `in-review` to reduce
  UI complexity and audit-event types.
- Section state lives inline in the JSON body; no separate
  `document_sections` table for MVP. Migration to a dedicated table
  is preserved if scale or concurrency demands it.
- Sections appear in template-declared order. Adding/removing sections
  to an existing typed doc requires the template version to bump and
  a per-doc migration step (TBD post-MVP).

### 4.2 `acceptance_criteria.body` — kind-dependent shape

Three shapes, keyed by `acceptance_criteria.kind`:

**`kind='text'`** (free-text criterion):

```json
{
  "text": "Director ratifies overall scope and direction."
}
```

**`kind='metric'`** (automatable threshold check):

```json
{
  "metric": "experiment.eval_accuracy",
  "operator": ">=",
  "threshold": 0.85,
  "evaluation": "auto",                     // 'auto' | 'manual'
  "source_run_filter": {                    // optional: which runs feed the metric
    "tag": "ablation-final"
  }
}
```

**`kind='gate'`** (named gate handle, hub-evaluated):

```json
{
  "gate": "all-method-sections-ratified",   // template-declared gate
  "params": { "deliverable_id": "<...>" }   // gate-specific params
}
```

Operators for `kind='metric'`: `>=`, `<=`, `>`, `<`, `==`. The hub
evaluates and updates `state` automatically; per the 2026-05-05 §B.5
resolution, hitting threshold posts a ratify-prompt attention item
rather than auto-advancing the phase.

### 4.3 `projects.phase_history` — optional transition log

```json
{
  "transitions": [
    {
      "from": "idea",
      "to": "initiation",
      "at": "2026-05-04T10:00:00Z",
      "by_actor": "user:director-id",
      "audit_event_id": "ae-..."
    }
  ]
}
```

Denormalized; canonical truth is in `audit_events` (`project.phase_*`
kinds). Skip the column if query patterns don't justify it.

---

## 5. Indexes + FK + cascade rules

```sql
-- Lookups by project + phase
CREATE INDEX idx_deliverables_project_phase
    ON deliverables(project_id, phase, ord);

-- Lookups by component → parent deliverable
CREATE INDEX idx_deliv_comp_deliv
    ON deliverable_components(deliverable_id, ord);

-- Reverse: find deliverables referencing a specific entity (e.g., a run)
CREATE INDEX idx_deliv_comp_ref
    ON deliverable_components(kind, ref_id);

-- Criteria by project + phase (hot path: "what's left for this phase?")
CREATE INDEX idx_criteria_project_phase
    ON acceptance_criteria(project_id, phase, ord);

-- Criteria filtered by deliverable (optional partial index if SQLite supports it)
CREATE INDEX idx_criteria_deliv
    ON acceptance_criteria(deliverable_id);

-- Document kind lookup (for "all proposals across team" cross-project queries)
CREATE INDEX idx_documents_kind
    ON documents(kind)
    WHERE kind IS NOT NULL;
```

**FK + cascade summary:**

| Table | FK target | ON DELETE |
|---|---|---|
| `deliverables.project_id` | `projects.id` | CASCADE — project archive cascades |
| `deliverable_components.deliverable_id` | `deliverables.id` | CASCADE |
| `deliverable_components.ref_id` | (polymorphic, by `kind`) | NO FK; hub validates |
| `acceptance_criteria.project_id` | `projects.id` | CASCADE |
| `acceptance_criteria.deliverable_id` | `deliverables.id` | SET NULL — criterion outlives deliverable for audit |

**Important:** `acceptance_criteria.deliverable_id` is SET NULL on
delete (not CASCADE) so that a deleted/replaced deliverable doesn't
silently drop the criterion's history. The criterion remains queryable
by `project_id + phase`.

---

## 6. New audit event kinds

Added to `audit_events.action` per
[`audit-events.md`](audit-events.md). All emit through the existing
`recordAudit(...)` helper.

| Kind | `target_kind` | `target_id` | `meta_json` |
|---|---|---|---|
| `project.phase_set` | `project` | project_id | `{ phase, by_template }` |
| `project.phase_advanced` | `project` | project_id | `{ from, to, criteria_met: [...] }` |
| `project.phase_reverted` | `project` | project_id | `{ from, to, reason }` *(rare; admin-only)* |
| `deliverable.created` | `deliverable` | deliverable_id | `{ project_id, phase, kind, required }` |
| `deliverable.updated` | `deliverable` | deliverable_id | `{ changed_fields: [...] }` |
| `deliverable.ratified` | `deliverable` | deliverable_id | `{ project_id, phase, kind }` |
| `deliverable.unratified` | `deliverable` | deliverable_id | `{ reason }` *(rare; admin-only)* |
| `deliverable_component.added` | `deliverable_component` | component_id | `{ deliverable_id, kind, ref_id, required }` |
| `deliverable_component.removed` | `deliverable_component` | component_id | `{ deliverable_id, kind, ref_id }` |
| `document.section_authored` | `document` | document_id | `{ section_slug, by_session_id, prior_status, new_status }` |
| `document.section_ratified` | `document` | document_id | `{ section_slug, ratified_by_actor }` |
| `criterion.created` | `criterion` | criterion_id | `{ project_id, phase, kind, deliverable_id }` |
| `criterion.met` | `criterion` | criterion_id | `{ evidence_ref, by_actor }` |
| `criterion.failed` | `criterion` | criterion_id | `{ reason }` |
| `criterion.waived` | `criterion` | criterion_id | `{ waived_by_actor, reason }` |

Activity feed renders all of these chronologically per
[ADR-019](../decisions/019-channels-as-event-log.md). The mobile
Activity tab gains an actor/kind filter for `deliverable.*` and
`criterion.*` so directors can scope to phase progress.

---

## 7. Migration plan

### 7.1 Order

Apply DDL in this order (single migration is fine; order is just for
clarity if it splits):

1. `ALTER TABLE projects ADD COLUMN phase` + `phase_history`
2. `ALTER TABLE documents ADD COLUMN kind` + `schema_id`
3. `CREATE TABLE deliverables`
4. `CREATE TABLE deliverable_components`
5. `CREATE TABLE acceptance_criteria`
6. Indexes from §5
7. New audit event kinds: register in the hub's audit-action allowlist
   (no DDL needed; it's runtime config)

### 7.2 Backfill defaults

| Existing data | Backfill |
|---|---|
| All existing `projects` rows | `phase = NULL`, `phase_history = NULL`. Lifecycle disabled; UI falls back to current Overview. |
| All existing `documents` rows | `kind = NULL`, `schema_id = NULL`. Body stays plain markdown. |
| New projects from phase-declaring templates | `phase` set to the template's first phase on creation; emit `project.phase_set` audit. |

No rows are touched in existing tables beyond the `ALTER TABLE`. New
tables start empty.

### 7.3 Backwards compatibility

**Guarantees:**
- Existing projects (no phase) continue to render and operate exactly
  as before.
- Existing documents (no kind) continue to render as plain markdown.
- No mobile build is broken by the migration; old clients reading new
  responses see fields they don't recognize and ignore them.
- `projects.steward_agent_id` and other lifecycle-orthogonal columns
  are unchanged.

**Forward-only constraints:**
- Once a project's `phase` is set non-NULL, it cannot revert to NULL
  without an admin action. Templates can change phase value; templates
  cannot turn off lifecycle for a project that has lifecycle data.
- Once a typed document has a non-NULL `kind`, it cannot revert to
  plain markdown without losing section structure.
- Deleting a deliverable cascades its components but only SET NULLs
  any criteria that referenced it (preserves audit).

---

## 8. Validation rules (hub-enforced)

Beyond the SQL CHECK constraints, the hub enforces:

1. **Phase value comes from template.** When `projects.phase` is set,
   the hub validates the value is in the project's template-declared
   phase set.
2. **Deliverable phase matches project phase or a past phase.** A
   deliverable's `phase` must equal `projects.phase` or a phase that
   precedes it in the template's order (no future-phase deliverables).
3. **Component ref integrity.** When a `deliverable_components` row
   is written, the hub verifies the referenced row exists in its typed
   table.
4. **Criterion phase coherence.** A criterion's `phase` must be the
   project's current or a past phase; future-phase criteria can be
   declared by templates but instantiated only when the phase is
   reached (template hydration timing TBD in template-YAML reference).
5. **Ratification authority.** When a deliverable is ratified, the
   `ratified_by_actor` must satisfy the template-declared ratification
   authority (director-only, council, or auto-when-criteria-met).
6. **Phase advance preconditions.** `project.phase_advanced` is
   accepted only when all `required=1` criteria for the source phase
   are in `state='met'` or `'waived'`. Ratify-prompt UX is the hub's
   way to surface this — D6 / §B.5.

---

## 9. Open follow-ups

1. **Document section evolution.** When a template version bumps and
   adds a section to a schema, existing typed documents need a
   migration. MVP punts; defer to a post-MVP discussion.
2. **Deliverable revocation.** `deliverable.unratified` is reserved
   but not user-facing in MVP. Whether revocation is allowed and how
   it cascades to phase state is an open design question.
3. **Multi-deliverable per phase ordering.** When a phase has 0..N
   deliverables (D8), can phase advance proceed if some are still in
   draft but the required ones are ratified? Yes — `required` flag
   handles this. Documented here for emphasis.
4. **Cross-project criteria.** `acceptance_criteria.kind='gate'` could
   reference a sibling project ("paper-X published"). Out of scope for
   MVP; gate handle namespace stays project-local.
5. **Mobile cache schema.** `HubSnapshotCache` (sqflite) needs to mint
   tables for the new endpoints' payloads. Mirrors the hub schema for
   read-side; no auth state. Sketched in mobile-side migration when
   wedge W5b lands.

---

## 10. Cross-references

- [`discussions/project-detail-lifecycle-architecture.md`](../discussions/project-detail-lifecycle-architecture.md)
  — design discussion + D1–D10 decisions
- [`reference/audit-events.md`](audit-events.md) — existing audit
  taxonomy + meta_json shape contract
- [`decisions/019-channels-as-event-log.md`](../decisions/019-channels-as-event-log.md)
  — Activity feed contract
- [`decisions/009-agent-state-and-identity.md`](../decisions/009-agent-state-and-identity.md)
  D7 — steward session scope routing
- [`decisions/017-layered-stewards.md`](../decisions/017-layered-stewards.md)
  — general / project steward architecture
- `reference/template-yaml-schema.md` (TBD) — phase + deliverable +
  criterion declarations in template YAML
- `reference/hub-api-deliverables.md` (TBD) — HTTP endpoints for
  phase advance, deliverable CRUD, criterion mark-met,
  section-targeted distillation

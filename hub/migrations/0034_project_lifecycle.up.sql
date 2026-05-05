-- Project lifecycle chassis (W1).
-- Adds the phase column + optional history JSON on projects (D1), the
-- typed-document section schema marker on documents (D7), and the three
-- new tables — deliverables, deliverable_components, acceptance_criteria
-- — that carry per-phase content (D8/D9). All defaults are NULL on
-- existing rows, so legacy projects continue to render lifecycle-disabled.
-- See docs/reference/project-phase-schema.md for the full schema spec
-- and docs/plans/project-lifecycle-mvp.md §5 for the migration plan.
--
-- Note on documents: A1 §3.2 originally proposed adding both `kind` and
-- `schema_id` here. `documents.kind` already exists (migration 0007) as a
-- freeform NOT NULL field carrying values like 'memo' / 'draft' / 'report' /
-- 'review' — and freeform is enough room to add the typed-doc senses
-- ('proposal', 'method', 'paper', …) without a column rename. So this
-- migration only adds the new `schema_id` field; presence of a non-NULL
-- schema_id is the typed-document indicator.

ALTER TABLE projects  ADD COLUMN phase           TEXT;
ALTER TABLE projects  ADD COLUMN phase_history   TEXT;

ALTER TABLE documents ADD COLUMN schema_id       TEXT;

CREATE TABLE deliverables (
  id                  TEXT PRIMARY KEY,
  project_id          TEXT NOT NULL,
  phase               TEXT NOT NULL,
  kind                TEXT NOT NULL,
  ratification_state  TEXT NOT NULL DEFAULT 'draft'
                          CHECK (ratification_state IN ('draft','in-review','ratified')),
  ratified_at         TEXT,
  ratified_by_actor   TEXT,
  required            INTEGER NOT NULL DEFAULT 1,
  ord                 INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at          TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

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

CREATE TABLE acceptance_criteria (
  id              TEXT PRIMARY KEY,
  project_id      TEXT NOT NULL,
  phase           TEXT NOT NULL,
  deliverable_id  TEXT,
  kind            TEXT NOT NULL
                      CHECK (kind IN ('text','metric','gate')),
  body            TEXT NOT NULL,
  state           TEXT NOT NULL DEFAULT 'pending'
                      CHECK (state IN ('pending','met','failed','waived')),
  met_at          TEXT,
  met_by_actor    TEXT,
  evidence_ref    TEXT,
  required        INTEGER NOT NULL DEFAULT 1,
  ord             INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  FOREIGN KEY (deliverable_id) REFERENCES deliverables(id) ON DELETE SET NULL
);

CREATE INDEX idx_deliverables_project_phase
    ON deliverables(project_id, phase, ord);

CREATE INDEX idx_deliv_comp_deliv
    ON deliverable_components(deliverable_id, ord);

CREATE INDEX idx_deliv_comp_ref
    ON deliverable_components(kind, ref_id);

CREATE INDEX idx_criteria_project_phase
    ON acceptance_criteria(project_id, phase, ord);

CREATE INDEX idx_criteria_deliv
    ON acceptance_criteria(deliverable_id);

CREATE INDEX idx_documents_schema
    ON documents(schema_id)
    WHERE schema_id IS NOT NULL;

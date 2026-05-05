-- Reverse 0034: drop the lifecycle tables + the new project/document
-- columns. Forward-only is the documented stance (see
-- reference/project-phase-schema.md §7.3) — this down-migration exists
-- for local-dev rollback during W1's iteration only.

DROP INDEX IF EXISTS idx_documents_schema;
DROP INDEX IF EXISTS idx_criteria_deliv;
DROP INDEX IF EXISTS idx_criteria_project_phase;
DROP INDEX IF EXISTS idx_deliv_comp_ref;
DROP INDEX IF EXISTS idx_deliv_comp_deliv;
DROP INDEX IF EXISTS idx_deliverables_project_phase;

DROP TABLE IF EXISTS acceptance_criteria;
DROP TABLE IF EXISTS deliverable_components;
DROP TABLE IF EXISTS deliverables;

ALTER TABLE documents DROP COLUMN schema_id;

ALTER TABLE projects  DROP COLUMN phase_history;
ALTER TABLE projects  DROP COLUMN phase;

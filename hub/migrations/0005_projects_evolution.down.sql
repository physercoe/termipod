-- Reverse P0.1. Drop indexes first so DROP COLUMN on indexed columns succeeds
-- (SQLite ≥3.35 / modernc.org/sqlite supports ALTER TABLE DROP COLUMN).

DROP INDEX IF EXISTS idx_projects_is_template;
DROP INDEX IF EXISTS idx_projects_template;
DROP INDEX IF EXISTS idx_projects_parent;

ALTER TABLE projects DROP COLUMN on_create_template_id;
ALTER TABLE projects DROP COLUMN steward_agent_id;
ALTER TABLE projects DROP COLUMN policy_overrides_json;
ALTER TABLE projects DROP COLUMN budget_cents;
ALTER TABLE projects DROP COLUMN is_template;
ALTER TABLE projects DROP COLUMN parameters_json;
ALTER TABLE projects DROP COLUMN template_id;
ALTER TABLE projects DROP COLUMN parent_project_id;
ALTER TABLE projects DROP COLUMN kind;
ALTER TABLE projects DROP COLUMN goal;

-- P0.1: Evolve `projects` to subsume the "directives" concept (blueprint §6.1).
-- Adds goal/kind/template/parameters/budget/policy/steward fields.
--
-- Note: SQLite ALTER TABLE ADD COLUMN cannot declare FOREIGN KEY REFERENCES
-- after the fact, and CHECK constraints can't be added via ALTER either.
-- `kind` is constrained to {'goal','standing'} at the application layer
-- (see handleCreateProject). All ID-ish columns are plain TEXT.

ALTER TABLE projects ADD COLUMN goal                  TEXT;
ALTER TABLE projects ADD COLUMN kind                  TEXT NOT NULL DEFAULT 'goal';
ALTER TABLE projects ADD COLUMN parent_project_id     TEXT;
ALTER TABLE projects ADD COLUMN template_id           TEXT;
ALTER TABLE projects ADD COLUMN parameters_json       TEXT;
ALTER TABLE projects ADD COLUMN is_template           INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN budget_cents          INTEGER;
ALTER TABLE projects ADD COLUMN policy_overrides_json TEXT;
ALTER TABLE projects ADD COLUMN steward_agent_id      TEXT;
ALTER TABLE projects ADD COLUMN on_create_template_id TEXT;

CREATE INDEX idx_projects_parent      ON projects(parent_project_id);
CREATE INDEX idx_projects_template    ON projects(template_id) WHERE template_id IS NOT NULL;
CREATE INDEX idx_projects_is_template ON projects(is_template);

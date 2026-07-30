-- Indexes go with the table; naming them explicitly documents what existed.
DROP INDEX IF EXISTS idx_environments_twin;
DROP INDEX IF EXISTS idx_environments_project;
DROP INDEX IF EXISTS idx_environments_team;
DROP INDEX IF EXISTS idx_environments_handle;
DROP TABLE IF EXISTS environments;

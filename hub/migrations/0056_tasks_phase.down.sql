DROP INDEX IF EXISTS idx_tasks_project_phase;
ALTER TABLE tasks DROP COLUMN phase;

DROP INDEX IF EXISTS idx_agent_spawns_task;
ALTER TABLE agent_spawns DROP COLUMN task_id;

ALTER TABLE tasks DROP COLUMN started_at;
ALTER TABLE tasks DROP COLUMN completed_at;
ALTER TABLE tasks DROP COLUMN result_summary;

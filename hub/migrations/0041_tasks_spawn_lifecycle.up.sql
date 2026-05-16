-- ADR-029 W1: link agent_spawns to a task + add task lifecycle columns.
--
-- Today the `tasks` table has assignee_id / created_by_id but no edge
-- to the agent_spawns row that actually executes the work, and no
-- timestamps for when the work started or finished. As a result, when
-- the project steward spawns a worker for a task:
--   - the Tasks tab can't show "this task is running on @worker"
--   - status flips from 'todo' to 'in_progress' to 'done' have to be
--     issued explicitly, which the steward almost never does
--   - the audit trail can't reconstruct who did what when
--
-- This migration closes those gaps:
--   - agent_spawns.task_id is the edge. ON DELETE SET NULL preserves
--     the spawn / agent_events / audit trail even if the task is
--     deleted (per ADR-029 D-2 + D-3).
--   - Partial index — only the spawned-for-a-task subset is ever
--     queried (most spawns stay ad-hoc and leave task_id NULL).
--   - tasks.started_at / completed_at carry the auto-derived
--     timestamps from the spawn lifecycle (D-3 flip-on-spawn /
--     terminated→done).
--   - tasks.result_summary is the steward-supplied or worker-supplied
--     one-line outcome, surfaced on the mobile task tile (Phase 2).
--
-- No backfill — pre-migration spawns leave task_id NULL, same as
-- ad-hoc spawns going forward. The Tasks tab tolerates NULL on every
-- field added here.

ALTER TABLE agent_spawns ADD COLUMN task_id TEXT
  REFERENCES tasks(id) ON DELETE SET NULL;

CREATE INDEX idx_agent_spawns_task ON agent_spawns(task_id)
  WHERE task_id IS NOT NULL;

ALTER TABLE tasks ADD COLUMN started_at     TEXT;
ALTER TABLE tasks ADD COLUMN completed_at   TEXT;
ALTER TABLE tasks ADD COLUMN result_summary TEXT;

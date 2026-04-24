-- W2: Task as atom (blueprint §6.1–6.2).
--
-- Tasks become the universal review-able work atom; plan steps that need
-- human visibility (human_decision gates, agent_spawn launches) materialize
-- a task row linked back via plan_step_id. Ad-hoc tasks keep plan_step_id
-- NULL — the `source` axis in handlers is derived from this column.

ALTER TABLE tasks ADD COLUMN plan_step_id TEXT;
CREATE INDEX idx_tasks_plan_step ON tasks(plan_step_id);

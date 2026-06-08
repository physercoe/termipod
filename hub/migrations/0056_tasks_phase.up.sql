-- WS1 (ADR-046 / ADR-044 amendment): tasks carry a phase.
--
-- The inline project spec declares per-phase `tasks:`; early-bind
-- materializes them at create as first-class task rows (ADR-029), one per
-- spec entry, stamped with the phase they belong to. The column is
-- nullable: ad-hoc tasks (steward- or director-created) carry no phase, and
-- every pre-existing task keeps NULL with no backfill. Mobile groups tasks
-- by phase the same way it groups deliverables / criteria.
ALTER TABLE tasks ADD COLUMN phase TEXT;
CREATE INDEX idx_tasks_project_phase ON tasks(project_id, phase);

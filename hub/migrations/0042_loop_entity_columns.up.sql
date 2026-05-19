-- ADR-034 (orchestration loop-closure runtime) B1 — the loop-entity
-- data model.
--
-- The loop-entity is a *role* over two existing tables, not a new table
-- (ADR-034 D-8): a directive / task is a `tasks` row, a question is an
-- `attention_items` row. This migration adds — additively, with no
-- backfill — the per-hop deadline columns and the terminal-reason
-- column the loop-closure runtime (the sweep, escalation, the directive
-- trace) needs.
--
-- The human-facing `tasks.status` set is UNCHANGED (the 2026-05-19
-- "option X" decision): `status` is the task-management lifecycle the
-- mobile UI renders; `terminal_reason` is the close-classification the
-- loop-closure runtime needs. They are additive, not redundant — `done`
-- + `completed` is a workflow state plus a close reason (ADR-034 D-6).
--
-- Every column is nullable or defaulted, so pre-migration rows — and
-- seed-demo's task fixtures — stay valid with no rewrite.

-- Per-hop deadlines + escalation state (ADR-034 D-2 / D-3 / D-4).
ALTER TABLE tasks ADD COLUMN inactivity_deadline TEXT;
ALTER TABLE tasks ADD COLUMN last_progress_at    TEXT;
ALTER TABLE tasks ADD COLUMN opened_at           TEXT;
ALTER TABLE tasks ADD COLUMN absolute_cap        TEXT;
ALTER TABLE tasks ADD COLUMN escalation_state    TEXT NOT NULL DEFAULT 'none'
  CHECK (escalation_state IN ('none', 'escalated_steward', 'escalated_principal'));
-- The close classification (ADR-034 D-6) — set when the entity closes,
-- alongside the unchanged human-facing `status`.
ALTER TABLE tasks ADD COLUMN terminal_reason TEXT
  CHECK (terminal_reason IN ('completed', 'failed', 'killed', 'timed_out', 'superseded'));

ALTER TABLE attention_items ADD COLUMN inactivity_deadline TEXT;
ALTER TABLE attention_items ADD COLUMN last_progress_at    TEXT;
ALTER TABLE attention_items ADD COLUMN opened_at           TEXT;
ALTER TABLE attention_items ADD COLUMN absolute_cap        TEXT;
ALTER TABLE attention_items ADD COLUMN escalation_state    TEXT NOT NULL DEFAULT 'none'
  CHECK (escalation_state IN ('none', 'escalated_steward', 'escalated_principal'));
ALTER TABLE attention_items ADD COLUMN terminal_reason TEXT
  CHECK (terminal_reason IN ('completed', 'failed', 'killed', 'timed_out', 'superseded'));
-- The lineage pointer to the enclosing directive/task (ADR-034 D-8) —
-- the parent the directive trace walks. Distinct in principle from the
-- existing ref_task_id (a free task reference), though for a question
-- raised under a task the two usually coincide.
ALTER TABLE attention_items ADD COLUMN cause TEXT REFERENCES tasks(id) ON DELETE SET NULL;

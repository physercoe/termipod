-- ADR-034 amendment (2026-05-19) — per-project loop-closure deadline
-- override.
--
-- ADR-034 D-2 said the per-hop deadline budgets "come from the agent
-- family / template; a directive may override." This realises that
-- override as two nullable per-project columns the director sets from
-- the mobile project-edit sheet. NULL = use the hub default budget
-- (loop_sweep.go's loopInactivityBudget / loopAbsoluteCapBudget); a
-- positive integer overrides it for every loop-entity in the project.
--
-- Additive, no backfill — pre-migration projects keep NULL and so keep
-- the hub defaults.
ALTER TABLE projects ADD COLUMN loop_inactivity_minutes   INTEGER;
ALTER TABLE projects ADD COLUMN loop_absolute_cap_minutes INTEGER;

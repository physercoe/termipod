DROP INDEX IF EXISTS idx_runs_dataset;
-- SQLite gained DROP COLUMN in 3.35; modernc.org/sqlite is well past it, and
-- the column carries no constraint that would block the drop.
ALTER TABLE runs DROP COLUMN dataset_id;

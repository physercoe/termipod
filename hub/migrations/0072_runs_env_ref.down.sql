-- SQLite gained DROP COLUMN in 3.35; modernc.org/sqlite is well past it, and
-- the column carries no constraint that would block the drop (0069 drops
-- runs.dataset_id the same way).
ALTER TABLE runs DROP COLUMN env_ref;

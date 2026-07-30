DROP INDEX IF EXISTS idx_host_commands_inflight;
-- SQLite gained DROP COLUMN in 3.35; modernc.org/sqlite is well past it, and
-- neither column carries a constraint or index that would block the drop (the
-- partial index above is dropped first).
ALTER TABLE host_commands DROP COLUMN progress_at;
ALTER TABLE host_commands DROP COLUMN progress_json;

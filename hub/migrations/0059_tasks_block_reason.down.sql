-- SQLite (modernc) supports DROP COLUMN. Reverses 0059.
ALTER TABLE tasks DROP COLUMN block_reason;

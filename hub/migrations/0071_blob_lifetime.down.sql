DROP INDEX IF EXISTS idx_blobs_expiring;
-- SQLite gained DROP COLUMN in 3.35; the partial index above is dropped first,
-- so neither column is still referenced by one.
--
-- Note this is a one-way door in practice: rolling back discards the knowledge
-- that some blobs were cache entries, so every surviving `derived` blob becomes
-- indistinguishable from a permanent one and lingers forever. That is the safe
-- direction (nothing is deleted), and it is why the up-migration's default is
-- `owned`.
ALTER TABLE blobs DROP COLUMN expires_at;
ALTER TABLE blobs DROP COLUMN class;

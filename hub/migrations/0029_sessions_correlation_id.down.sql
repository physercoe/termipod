DROP INDEX IF EXISTS idx_sessions_correlation;
ALTER TABLE sessions DROP COLUMN correlation_id;

-- agents.fanout (mcp_orchestrate.go) tags every spawned worker's
-- session with a correlation_id so agents.gather can find them as a
-- group. Nullable so non-fanout sessions stay unchanged. Indexed for
-- the per-correlation lookup gather makes on every poll tick.
ALTER TABLE sessions ADD COLUMN correlation_id TEXT;
CREATE INDEX idx_sessions_correlation
  ON sessions(team_id, correlation_id)
  WHERE correlation_id IS NOT NULL;

DROP INDEX IF EXISTS idx_agent_events_session;
ALTER TABLE agent_events DROP COLUMN session_id;
ALTER TABLE audit_events DROP COLUMN session_id;
ALTER TABLE attention_items DROP COLUMN session_id;
DROP INDEX IF EXISTS idx_sessions_active_worktree;
DROP INDEX IF EXISTS idx_sessions_current_agent;
DROP INDEX IF EXISTS idx_sessions_team_status_active;
DROP TABLE IF EXISTS sessions;

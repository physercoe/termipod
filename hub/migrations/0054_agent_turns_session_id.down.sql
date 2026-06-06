DROP INDEX IF EXISTS idx_agent_turns_session;
ALTER TABLE agent_turns DROP COLUMN session_id;

DROP INDEX IF EXISTS ux_agent_events_session_ordinal;
ALTER TABLE agent_events DROP COLUMN session_ordinal;

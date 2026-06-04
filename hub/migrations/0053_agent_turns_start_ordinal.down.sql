DROP INDEX IF EXISTS idx_agent_turns_agent_ordinal;
ALTER TABLE agent_turns DROP COLUMN start_ordinal;

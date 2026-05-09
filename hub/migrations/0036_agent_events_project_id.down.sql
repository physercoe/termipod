DROP TRIGGER IF EXISTS agent_events_stamp_project;

-- Restore the un-narrowed FTS update trigger from migration 0031.
DROP TRIGGER IF EXISTS agent_events_fts_update;
CREATE TRIGGER agent_events_fts_update AFTER UPDATE ON agent_events BEGIN
    DELETE FROM agent_events_fts WHERE event_id = old.id;
    INSERT INTO agent_events_fts(event_id, text)
    VALUES (new.id, new.payload_json);
END;

DROP INDEX IF EXISTS idx_agent_events_project_ts;
ALTER TABLE agent_events DROP COLUMN project_id;

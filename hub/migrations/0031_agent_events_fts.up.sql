-- Phase 1.5c (MVP parity gap — session search): full-text index over
-- agent event payloads so the user can find a past conversation by
-- content. Mirrors the events_fts pattern from migration 0001 (which
-- searches channel events) — different table because agent_events
-- and events are different concepts.

CREATE VIRTUAL TABLE agent_events_fts USING fts5(
    event_id UNINDEXED,
    text,
    tokenize = 'porter unicode61'
);

CREATE TRIGGER agent_events_fts_insert AFTER INSERT ON agent_events BEGIN
    INSERT INTO agent_events_fts(event_id, text)
    VALUES (new.id, new.payload_json);
END;

CREATE TRIGGER agent_events_fts_delete AFTER DELETE ON agent_events BEGIN
    DELETE FROM agent_events_fts WHERE event_id = old.id;
END;

CREATE TRIGGER agent_events_fts_update AFTER UPDATE ON agent_events BEGIN
    DELETE FROM agent_events_fts WHERE event_id = old.id;
    INSERT INTO agent_events_fts(event_id, text)
    VALUES (new.id, new.payload_json);
END;

-- Back-fill existing rows so a fresh deploy on an old DB gets
-- searchable history immediately. Safe to run unconditionally —
-- the table was just created so it's empty.
INSERT INTO agent_events_fts(event_id, text)
SELECT id, payload_json FROM agent_events;

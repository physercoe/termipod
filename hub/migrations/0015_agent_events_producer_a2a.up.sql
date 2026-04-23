-- P3.2b follow-up: widen agent_events.producer to include 'a2a'.
--
-- The A2A dispatcher on host-runner posts peer-originated input through
-- the same POST /input endpoint as phone/web clients, but stamps
-- producer='a2a' so the audit trail can tell them apart. The original
-- CHECK was ('agent','user','system') only; we rebuild the table to
-- widen the constraint.
--
-- SQLite can't alter a CHECK in place, so this is a rename+copy+replace
-- dance. The rest of the schema (indexes, FK from joins) is reasserted
-- after the swap.

PRAGMA foreign_keys=OFF;

ALTER TABLE agent_events RENAME TO agent_events_old;

CREATE TABLE agent_events (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,
    ts           TEXT NOT NULL,
    kind         TEXT NOT NULL,
    producer     TEXT NOT NULL CHECK (producer IN ('agent','user','system','a2a')),
    payload_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(agent_id, seq)
);

INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
SELECT id, agent_id, seq, ts, kind, producer, payload_json FROM agent_events_old;

DROP TABLE agent_events_old;

CREATE INDEX idx_agent_events_agent_seq ON agent_events(agent_id, seq);
CREATE INDEX idx_agent_events_agent_ts  ON agent_events(agent_id, ts);

PRAGMA foreign_keys=ON;

-- Reverse 0015: narrow agent_events.producer back to the original set
-- and drop any 'a2a' rows. Same rename+copy+replace dance as the up
-- migration since SQLite can't narrow a CHECK in place.

PRAGMA foreign_keys=OFF;

ALTER TABLE agent_events RENAME TO agent_events_new;

CREATE TABLE agent_events (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,
    ts           TEXT NOT NULL,
    kind         TEXT NOT NULL,
    producer     TEXT NOT NULL CHECK (producer IN ('agent','user','system')),
    payload_json TEXT NOT NULL DEFAULT '{}',
    UNIQUE(agent_id, seq)
);

INSERT INTO agent_events (id, agent_id, seq, ts, kind, producer, payload_json)
SELECT id, agent_id, seq, ts, kind, producer, payload_json
  FROM agent_events_new
 WHERE producer IN ('agent','user','system');

DROP TABLE agent_events_new;

CREATE INDEX idx_agent_events_agent_seq ON agent_events(agent_id, seq);
CREATE INDEX idx_agent_events_agent_ts  ON agent_events(agent_id, ts);

PRAGMA foreign_keys=ON;

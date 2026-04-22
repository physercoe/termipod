-- P1.7: agent_events store (blueprint §5.5 / §9 P1.7).
--
-- Unified per-agent event queue regardless of driving mode (M1 ACP, M2
-- structured stdio, M4 manual/pane). Producers: the agent (via host-runner
-- driver), the user (approvals, input), and the system (status changes).
-- The hub's AG-UI broker reads this table and fans out via SSE to clients.
--
-- seq is monotonic per agent; use (agent_id, seq) as the stable cursor
-- for replay. ts is server-assigned at insert time.

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
CREATE INDEX idx_agent_events_agent_seq ON agent_events(agent_id, seq);
CREATE INDEX idx_agent_events_agent_ts  ON agent_events(agent_id, ts);

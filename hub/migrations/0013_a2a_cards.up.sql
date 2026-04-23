-- P3.3a: A2A agent-card directory on the hub.
--
-- Host-runners push their live agent-card set here on startup and on agent
-- churn. The steward (and other discoverers) query this table to find agents
-- by handle across hosts — e.g. "where is worker.ml@gpu-host-1".
--
-- The card_json column stores the full agent-card document (protocol v0.3)
-- as served by the host-runner's local /a2a/<id>/.well-known/agent.json.
-- It includes the card's `url`, which the hub will rewrite to its own
-- /a2a/relay/... endpoint once the reverse tunnel lands (P3.3b). For now,
-- consumers should prefer host_id + agent_id to construct routes.
CREATE TABLE a2a_cards (
    id            TEXT PRIMARY KEY,
    team_id       TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    host_id       TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL,
    handle        TEXT NOT NULL,
    card_json     TEXT NOT NULL,
    registered_at TEXT NOT NULL,
    UNIQUE(team_id, host_id, agent_id)
);
CREATE INDEX idx_a2a_cards_team_handle ON a2a_cards(team_id, handle);
CREATE INDEX idx_a2a_cards_host        ON a2a_cards(host_id);

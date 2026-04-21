-- Slice 2: host-directed command queue + pane capture cache.
--
-- host_commands is the hub→host-runner work queue. Hub inserts rows and
-- host-runner polls (GET /hosts/:id/commands?status=pending), applies them
-- locally, and PATCHes back result_json + status. The design is pull-only
-- so a host-runner behind NAT never needs the hub to reach it.

CREATE TABLE host_commands (
    id           TEXT PRIMARY KEY,
    host_id      TEXT NOT NULL REFERENCES hosts(id)  ON DELETE CASCADE,
    agent_id     TEXT REFERENCES agents(id)          ON DELETE SET NULL,
    kind         TEXT NOT NULL,                     -- 'pause'|'resume'|'capture'|'terminate'
    args_json    TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending',   -- 'pending'|'delivered'|'done'|'failed'
    result_json  TEXT,
    error        TEXT,
    created_at   TEXT NOT NULL,
    delivered_at TEXT,
    completed_at TEXT
);
CREATE INDEX idx_host_commands_host_status ON host_commands(host_id, status);
CREATE INDEX idx_host_commands_agent ON host_commands(agent_id);

-- Cache the most recent pane capture so /pane reads are O(1) without
-- another host round-trip. host-runner writes this via result_json on a
-- capture command; hub copies it to agents.last_capture.
ALTER TABLE agents ADD COLUMN last_capture TEXT;
ALTER TABLE agents ADD COLUMN last_capture_at TEXT;

-- pending_payload_json stashes the gated action (e.g. the full spawnIn)
-- on an approval_request attention item, so the decide handler can
-- execute the action transactionally on approve without a second lookup.
ALTER TABLE attention_items ADD COLUMN pending_payload_json TEXT;
ALTER TABLE attention_items ADD COLUMN tier TEXT;

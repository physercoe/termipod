-- Replace the table-level UNIQUE(team_id, handle) on agents with a partial
-- unique index that only applies to live rows. Background: archiving an
-- agent leaves the row in place (soft delete via archived_at) so audit and
-- spawn history stay resolvable; with the table-level UNIQUE constraint,
-- the archived row's handle was still reserved and respawning a fresh
-- agent under the same handle (e.g. "steward") failed with
-- SQLITE_CONSTRAINT_UNIQUE (2067) → HTTP 409.
--
-- SQLite has no ALTER TABLE DROP CONSTRAINT, so we recreate the table.
-- defer_foreign_keys lets the self-FK on parent_agent_id and the inbound
-- FKs from other tables hold across the rename within this transaction.

PRAGMA defer_foreign_keys = ON;

CREATE TABLE agents_new (
    id                TEXT PRIMARY KEY,
    team_id           TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    handle            TEXT NOT NULL,
    kind              TEXT NOT NULL,
    backend_json      TEXT NOT NULL DEFAULT '{}',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    parent_agent_id   TEXT REFERENCES agents(id) ON DELETE SET NULL,
    status            TEXT NOT NULL DEFAULT 'pending',
    host_id           TEXT REFERENCES hosts(id) ON DELETE SET NULL,
    pane_id           TEXT,
    worktree_path     TEXT,
    journal_path      TEXT,
    budget_cents      INTEGER,
    spent_cents       INTEGER NOT NULL DEFAULT 0,
    idle_since        TEXT,
    pause_state       TEXT NOT NULL DEFAULT 'running',
    last_prompt_tail  TEXT,
    created_at        TEXT NOT NULL,
    terminated_at     TEXT,
    archived_at       TEXT,
    driving_mode      TEXT
);

INSERT INTO agents_new (
    id, team_id, handle, kind, backend_json, capabilities_json,
    parent_agent_id, status, host_id, pane_id, worktree_path, journal_path,
    budget_cents, spent_cents, idle_since, pause_state, last_prompt_tail,
    created_at, terminated_at, archived_at, driving_mode
)
SELECT
    id, team_id, handle, kind, backend_json, capabilities_json,
    parent_agent_id, status, host_id, pane_id, worktree_path, journal_path,
    budget_cents, spent_cents, idle_since, pause_state, last_prompt_tail,
    created_at, terminated_at, archived_at, driving_mode
FROM agents;

DROP TABLE agents;
ALTER TABLE agents_new RENAME TO agents;

CREATE INDEX idx_agents_team_status ON agents(team_id, status);
CREATE INDEX idx_agents_host ON agents(host_id);
CREATE UNIQUE INDEX agents_team_handle_active
    ON agents(team_id, handle) WHERE archived_at IS NULL;

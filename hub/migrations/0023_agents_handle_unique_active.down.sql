-- Restore the table-level UNIQUE(team_id, handle) constraint. If archived
-- rows exist that share a (team_id, handle) with a live row, this down
-- migration will fail — that's intentional. Operators must manually
-- decide whether to drop or rename archived rows before reverting.

PRAGMA defer_foreign_keys = ON;

DROP INDEX IF EXISTS agents_team_handle_active;

CREATE TABLE agents_old (
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
    driving_mode      TEXT,
    UNIQUE(team_id, handle)
);

INSERT INTO agents_old (
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
ALTER TABLE agents_old RENAME TO agents;

CREATE INDEX idx_agents_team_status ON agents(team_id, status);
CREATE INDEX idx_agents_host ON agents(host_id);

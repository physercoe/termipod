-- Termipod Hub — initial schema (plan §6).

PRAGMA foreign_keys = ON;

CREATE TABLE teams (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE auth_tokens (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,          -- 'owner' | 'host' | 'agent'
    token_hash   TEXT NOT NULL UNIQUE,
    scope_json   TEXT NOT NULL,
    expires_at   TEXT,
    revoked_at   TEXT,
    rotated_from TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_auth_tokens_hash ON auth_tokens(token_hash);

CREATE TABLE hosts (
    id                TEXT PRIMARY KEY,
    team_id           TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'disconnected',
    last_seen_at      TEXT,
    host_token_hash   TEXT,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    created_at        TEXT NOT NULL,
    UNIQUE(team_id, name)
);

CREATE TABLE agents (
    id                TEXT PRIMARY KEY,
    team_id           TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    handle            TEXT NOT NULL,
    kind              TEXT NOT NULL,               -- 'claude-code' | 'codex' | ...
    backend_json      TEXT NOT NULL DEFAULT '{}',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    parent_agent_id   TEXT REFERENCES agents(id) ON DELETE SET NULL,
    status            TEXT NOT NULL DEFAULT 'pending', -- pending|running|stale|paused|terminated
    host_id           TEXT REFERENCES hosts(id)  ON DELETE SET NULL,
    pane_id           TEXT,
    worktree_path     TEXT,
    journal_path      TEXT,
    budget_cents      INTEGER,                     -- NULL = unlimited
    spent_cents       INTEGER NOT NULL DEFAULT 0,
    idle_since        TEXT,
    pause_state       TEXT NOT NULL DEFAULT 'running', -- 'running' | 'paused'
    last_prompt_tail  TEXT,
    created_at        TEXT NOT NULL,
    terminated_at     TEXT,
    UNIQUE(team_id, handle)
);
CREATE INDEX idx_agents_team_status ON agents(team_id, status);
CREATE INDEX idx_agents_host ON agents(host_id);

CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    owner_id    TEXT REFERENCES agents(id),
    status      TEXT NOT NULL DEFAULT 'active',
    archived_at TEXT,
    config_yaml TEXT NOT NULL DEFAULT '',
    docs_root   TEXT,                              -- shared docs for context engineering (§10A)
    created_at  TEXT NOT NULL,
    UNIQUE(team_id, name)
);

CREATE TABLE channels (
    id          TEXT PRIMARY KEY,
    project_id  TEXT REFERENCES projects(id) ON DELETE CASCADE,  -- NULL = team scope
    scope_kind  TEXT NOT NULL,                     -- 'team' | 'project'
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE(scope_kind, project_id, name)
);

CREATE TABLE channel_members (
    channel_id  TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    agent_id    TEXT NOT NULL REFERENCES agents(id)   ON DELETE CASCADE,
    follow_mode TEXT NOT NULL DEFAULT 'full',       -- 'full' | 'mention'
    muted       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (channel_id, agent_id)
);

CREATE TABLE events (
    id                TEXT PRIMARY KEY,            -- ULID
    schema_version    INTEGER NOT NULL DEFAULT 1,
    ts                TEXT NOT NULL,               -- sender's clock
    received_ts       TEXT NOT NULL,               -- hub's clock; canonical order
    channel_id        TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    type              TEXT NOT NULL,
    from_id           TEXT REFERENCES agents(id),
    to_ids_json       TEXT NOT NULL DEFAULT '[]',
    parts_json        TEXT NOT NULL DEFAULT '[]',
    task_id           TEXT,
    correlation_id    TEXT,
    pane_ref_json     TEXT,
    usage_tokens_json TEXT,                        -- {input, output, cache_read, cost_cents}
    metadata_json     TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_events_channel_received ON events(channel_id, received_ts);
CREATE INDEX idx_events_received ON events(received_ts);
CREATE INDEX idx_events_task ON events(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_events_correlation ON events(correlation_id) WHERE correlation_id IS NOT NULL;

-- FTS5 virtual table indexing event text parts + message-ish fields.
-- Populated by triggers below; queried via /v1/search and MCP search().
CREATE VIRTUAL TABLE events_fts USING fts5(
    event_id UNINDEXED,
    text,
    tokenize = 'porter unicode61'
);

CREATE TRIGGER events_fts_insert AFTER INSERT ON events BEGIN
    INSERT INTO events_fts(event_id, text) VALUES (new.id, new.parts_json);
END;
CREATE TRIGGER events_fts_delete AFTER DELETE ON events BEGIN
    DELETE FROM events_fts WHERE event_id = old.id;
END;
CREATE TRIGGER events_fts_update AFTER UPDATE ON events BEGIN
    DELETE FROM events_fts WHERE event_id = old.id;
    INSERT INTO events_fts(event_id, text) VALUES (new.id, new.parts_json);
END;

CREATE TABLE milestones (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    due_at     TEXT,
    status     TEXT NOT NULL DEFAULT 'open',
    created_at TEXT NOT NULL
);

CREATE TABLE tasks (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    parent_task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    title          TEXT NOT NULL,
    body_md        TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'todo',
    assignee_id    TEXT REFERENCES agents(id) ON DELETE SET NULL,
    created_by_id  TEXT REFERENCES agents(id) ON DELETE SET NULL,
    milestone_id   TEXT REFERENCES milestones(id) ON DELETE SET NULL,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_tasks_project_status ON tasks(project_id, status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee_id);

CREATE TABLE attention_items (
    id                     TEXT PRIMARY KEY,
    project_id             TEXT REFERENCES projects(id) ON DELETE CASCADE,
    scope_kind             TEXT NOT NULL,                        -- 'team' | 'project' | 'channel'
    scope_id               TEXT,
    kind                   TEXT NOT NULL,                        -- 'decision' | 'approval' | ...
    ref_event_id           TEXT REFERENCES events(id) ON DELETE SET NULL,
    ref_task_id            TEXT REFERENCES tasks(id)  ON DELETE SET NULL,
    summary                TEXT NOT NULL,
    severity               TEXT NOT NULL DEFAULT 'minor',
    current_assignees_json TEXT NOT NULL DEFAULT '[]',
    decisions_json         TEXT NOT NULL DEFAULT '[]',
    escalation_history_json TEXT NOT NULL DEFAULT '[]',
    status                 TEXT NOT NULL DEFAULT 'open',
    created_at             TEXT NOT NULL,
    resolved_at            TEXT,
    resolved_by            TEXT REFERENCES agents(id) ON DELETE SET NULL
);
CREATE INDEX idx_attention_scope_status ON attention_items(scope_kind, scope_id, status);

CREATE TABLE agent_spawns (
    id                 TEXT PRIMARY KEY,
    parent_agent_id    TEXT REFERENCES agents(id) ON DELETE SET NULL,
    child_agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    spawn_spec_yaml    TEXT NOT NULL,
    spawn_authority_json TEXT NOT NULL DEFAULT '{}',
    task_json          TEXT,                                     -- {title, body, context_refs[], handoff_from}
    spawned_at         TEXT NOT NULL,
    terminated_at      TEXT,
    terminated_reason  TEXT,
    worktree_path      TEXT
);
CREATE INDEX idx_agent_spawns_child ON agent_spawns(child_agent_id);
CREATE INDEX idx_agent_spawns_parent ON agent_spawns(parent_agent_id);

CREATE TABLE agent_schedules (
    id              TEXT PRIMARY KEY,
    team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    cron_expr       TEXT NOT NULL,
    spawn_spec_yaml TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    last_run_at     TEXT,
    last_run_status TEXT,
    next_run_at     TEXT,
    created_by      TEXT REFERENCES agents(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL,
    UNIQUE(team_id, name)
);

CREATE TABLE blobs (
    sha256     TEXT PRIMARY KEY,
    scope_path TEXT NOT NULL,
    size       INTEGER NOT NULL,
    mime       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

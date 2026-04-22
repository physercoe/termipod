DROP TABLE IF EXISTS schedules;

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

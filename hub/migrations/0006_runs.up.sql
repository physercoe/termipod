-- Termipod Hub — Runs (§6.5).
--
-- A run is the unit of a single execution with a reproducibility contract.
-- Config is frozen at start. Metrics time-series are stored on the host via
-- trackio and only *referenced* on the hub (trackio_host_id, trackio_run_uri).
-- The hub never stores bulk metric data (data-ownership law, §4).

CREATE TABLE runs (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL,
    agent_id        TEXT,
    config_json     TEXT,
    seed            INTEGER,
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending|running|completed|failed|cancelled
    started_at      TEXT,
    finished_at     TEXT,
    trackio_host_id TEXT,
    trackio_run_uri TEXT,
    parent_run_id   TEXT,
    created_at      TEXT NOT NULL
);

CREATE INDEX idx_runs_project ON runs(project_id);
CREATE INDEX idx_runs_agent   ON runs(agent_id) WHERE agent_id IS NOT NULL;
CREATE INDEX idx_runs_parent  ON runs(parent_run_id) WHERE parent_run_id IS NOT NULL;
CREATE INDEX idx_runs_status  ON runs(status);

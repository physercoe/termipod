-- Run "extras" digests — the trackio sibling tables beside the scalar
-- metric curves (configs / system_metrics / alerts). Same data-ownership law
-- as run_metrics (§4): the hub stores only the compact digest the mobile run
-- item renders; bulk series stay on the host. The host-runner reads these from
-- the local trackio store and PUTs them here.

-- run_config — the run's hyperparameters, one JSON object per run.
CREATE TABLE run_config (
    run_id      TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    config_json TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- run_system_metrics — GPU/CPU utilization series. Same shape as run_metrics;
-- because trackio's system metrics are time-keyed (no training step), points
-- use a 0-based sample ordinal as the x-axis.
CREATE TABLE run_system_metrics (
    id           TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    metric_name  TEXT NOT NULL,
    points_json  TEXT NOT NULL,
    sample_count INTEGER NOT NULL DEFAULT 0,
    last_step    INTEGER,
    last_value   REAL,
    updated_at   TEXT NOT NULL,
    UNIQUE(run_id, metric_name)
);
CREATE INDEX idx_run_system_metrics_run ON run_system_metrics(run_id);

-- run_alerts — per-run warnings/notes. One row per alert; the PUT replaces the
-- whole set for a run atomically (delete + insert), like run_metrics.
CREATE TABLE run_alerts (
    id         TEXT PRIMARY KEY,
    run_id     TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    body       TEXT,
    level      TEXT NOT NULL DEFAULT 'warn',
    step       INTEGER,
    ts         TEXT,
    alert_id   TEXT,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_run_alerts_run ON run_alerts(run_id);

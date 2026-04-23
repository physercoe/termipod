-- P3.1a: Metric-digest storage.
--
-- The hub never stores bulk metric time-series (data-ownership law, §4) —
-- those live on the host that runs trackio. But the mobile app needs
-- *something* to render a sparkline with, so the host-runner polls
-- trackio locally, downsamples each curve to at most ~100 points, and
-- PUTs a compact digest here.
--
-- points_json is a JSON array of [step, value] pairs, canonically sorted
-- by step. Keep bodies under ~64 KiB per row — if a run has many metric
-- families the poller should split them across rows, one per metric.
CREATE TABLE run_metrics (
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
CREATE INDEX idx_run_metrics_run ON run_metrics(run_id);

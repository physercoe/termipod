-- Run histograms — wandb/tensorboard "Distributions" archetype.
--
-- Some training signals are distributions at a single step, not scalar
-- time-series: gradient magnitude per layer, weight distributions per
-- parameter block, logit entropy at an eval checkpoint. run_metrics
-- (0014) can't carry these because points_json is [[step,scalar],...].
-- Adding buckets would break every scalar-expecting consumer; separate
-- table is cleaner.
--
-- Data ownership (blueprint §4): the hub never stores the full tensor —
-- host-runners aggregate locally and PUT a binned digest here (edges +
-- counts). Same pattern as run_metrics: digest on the hub, full data
-- on the host.
--
-- buckets_json shape:
--   {"edges":[-0.1, -0.05, 0.0, 0.05, 0.1],  -- N+1 edges
--    "counts":[12, 145, 230, 98, 7]}         -- N counts
-- Server does NOT validate internal shape — just stores the string;
-- consumers parse. Kept bodies under ~8 KiB per row (≈128 bins max) so
-- a thousand rows fit comfortably in mobile memory.

CREATE TABLE run_histograms (
    id           TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    metric_name  TEXT NOT NULL,
    step         INTEGER NOT NULL,
    buckets_json TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE(run_id, metric_name, step)
);

CREATE INDEX idx_run_histograms_run_metric
    ON run_histograms(run_id, metric_name, step);

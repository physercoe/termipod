-- Run image series — checkpoint samples, attention maps, generated figures.
--
-- Wandb/tensorboard expose an "Images" panel: each run can emit one or
-- more *series* of images (e.g. `samples/generations`, `samples/attention`)
-- with one image per step. The mobile Run Detail screen needs the same
-- shape so a reviewer can scrub through checkpoints on the phone.
--
-- Data ownership (blueprint §4): the hub never stores bulk training
-- artefacts. Image *bytes* land in the existing content-addressed blobs
-- table (POST /v1/blobs → sha256). This table is a thin index: one row
-- per (run, metric, step) pointing at the blob.
--
-- metric_name lets a run carry multiple independent image streams —
-- the mobile UI groups by metric_name and renders one scrubber per.
-- Same convention as run_metrics metric_name so the UI can sort both
-- kinds of tiles into the same layout.

CREATE TABLE run_images (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    metric_name TEXT NOT NULL,
    step        INTEGER NOT NULL,
    blob_sha    TEXT NOT NULL REFERENCES blobs(sha256),
    caption     TEXT,
    created_at  TEXT NOT NULL,
    UNIQUE(run_id, metric_name, step)
);

CREATE INDEX idx_run_images_run_metric
    ON run_images(run_id, metric_name, step);

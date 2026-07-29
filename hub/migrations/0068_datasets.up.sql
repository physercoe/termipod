-- Datasets as a hub-owned entity (plan docs/plans/replay-datasets-episodes.md,
-- wedge W1). A dataset is a root of episodes — synchronized multimodal streams
-- (multi-cam video, proprioception, actions, language) — that a policy is
-- trained on or evaluated against. Runs were already first-class; the data they
-- consume and produce was not, so nothing in the product could say a dataset
-- exists, let alone open one.
--
-- Data-ownership law (blueprint §4): the hub holds the NAME, the location and a
-- folded digest. The episodes themselves — parquet shards, mp4s, frames — stay
-- on the host and are never copied here. The episodes *table* is not stored at
-- all: it is served from the host on demand, windowed and capped, because a
-- 50k-episode listing is bulk data wearing a metadata costume.
--
-- Project-scoped like runs (and, like runs, reached through the project's team
-- rather than carrying a team_id of its own) because a dataset belongs to a
-- line of work, and the run<->dataset provenance edges added in W5 join on the
-- same scope.
--
-- Registration is an explicit act — "Open in Replay", or a REST POST — never a
-- crawler. The no-surprise-scans posture: nothing walks a host looking for
-- datasets, and nothing re-reads one without being asked (see digest_ts and
-- fingerprint_json below).

CREATE TABLE datasets (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    host_id       TEXT REFERENCES hosts(id) ON DELETE SET NULL,
    name          TEXT NOT NULL DEFAULT '',
    root_path     TEXT NOT NULL,
    source        TEXT NOT NULL DEFAULT 'local',  -- local|sftp|hf
    -- Filled from the digest once the host has read the root; empty until then,
    -- and left empty for a root whose codebase_version we refuse to parse, so
    -- "unknown format" is distinguishable from "not read yet" via digest_ts.
    format        TEXT NOT NULL DEFAULT '',

    -- Opaque provenance handle, "family:env_id@version" (plan §3). Reserved
    -- from day one and deliberately unvalidated: the Environment registry is a
    -- sibling plan, and recording provenance before the registry exists means
    -- it later RESOLVES into rows instead of being backfilled by guesswork.
    env_ref       TEXT NOT NULL DEFAULT '',

    -- The folded digest as computed host-side (ADR-038 shape). Schema version
    -- is stored beside it so a bump refolds rather than reading old bytes under
    -- a new contract.
    digest_json            TEXT NOT NULL DEFAULT '',
    digest_schema_version  INTEGER NOT NULL DEFAULT 0,
    digest_ts              TEXT,
    -- Stat-only summary of the meta/ tree at fold time (files, bytes, newest
    -- mtime). Re-statting is one cheap syscall walk, so the UI can say "digest
    -- may be stale — Refresh" without re-reading anything. Refresh stays
    -- manual: a v3.0 refold is not free, and a background crawl is exactly the
    -- surprise this design refuses.
    fingerprint_json       TEXT NOT NULL DEFAULT '',

    registered_at TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE INDEX idx_datasets_project ON datasets(project_id);
CREATE INDEX idx_datasets_host ON datasets(host_id) WHERE host_id IS NOT NULL;

-- Registration is idempotent on (project, host, root): "Open in Replay" on a
-- tree row must be safe to hit twice, and the second hit should select the
-- dataset rather than create a duplicate that then drifts.
CREATE UNIQUE INDEX idx_datasets_identity
    ON datasets(project_id, COALESCE(host_id, ''), root_path);

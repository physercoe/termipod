-- P0.6: Artifacts primitive (blueprint §6.6).
--
-- Artifacts are content-addressed outputs produced by runs (checkpoints,
-- eval curves, logs, datasets...) or attached by users. They are the
-- canonical "output" surface, distinct from:
--   - Files (docs_root, path-keyed inputs agents read)
--   - Documents (versioned human-authored writeups)
--   - Blobs (team-global byte store; artifacts reference blobs by uri)
--
-- The `uri` column typically points at a blob ("blob:sha256/<hex>") but
-- may be any fetchable URI (e.g. "s3://...", "https://..."). `sha256`
-- and `size` are recorded when known for integrity checks and UI.
--
-- `lineage_json` is a free-form JSON object describing provenance —
-- parent artifact ids, source run id, input document ids, etc. Kept
-- schemaless for now; queries filter by project_id / run_id / kind.

CREATE TABLE artifacts (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  run_id TEXT,                        -- NULL if not produced by a run
  kind TEXT NOT NULL,                 -- 'checkpoint' | 'eval_curve' | 'log' | 'dataset' | 'report' | ...
  name TEXT NOT NULL,                 -- human-friendly label
  uri TEXT NOT NULL,                  -- 'blob:sha256/<hex>' or 's3://...' etc.
  sha256 TEXT,                        -- content hash (if known)
  size INTEGER,                       -- bytes (if known)
  mime TEXT,
  producer_agent_id TEXT,
  lineage_json TEXT,                  -- free-form JSON: {parents, inputs, ...}
  created_at TEXT NOT NULL
);

CREATE INDEX idx_artifacts_project ON artifacts(project_id);
CREATE INDEX idx_artifacts_run     ON artifacts(run_id) WHERE run_id IS NOT NULL;
CREATE INDEX idx_artifacts_kind    ON artifacts(kind);
CREATE INDEX idx_artifacts_sha     ON artifacts(sha256) WHERE sha256 IS NOT NULL;

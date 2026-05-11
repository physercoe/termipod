-- Closed artifact-kind registry (wave 2 W1, artifact-type-registry plan).
--
-- Validation lives in Go (`hub/internal/server/artifact_kinds.go`), not
-- as a CHECK constraint — open-question Q3 resolved 2026-05-11 in
-- favour of the whitelist so new kinds don't require a forward
-- migration each time. This file is therefore documentation +
-- backfill: it rewrites the legacy free-form kinds emitted by the
-- pre-W1 seed/MCP surface into the closed MVP set so unknown-kind
-- 400s don't fire on existing demo data.
--
-- Mapping (must mirror backfillLegacyArtifactKind in artifact_kinds.go):
--
--   checkpoint  → external-blob   (URI-only; weights live on object storage)
--   dataset     → external-blob   (URI-only blob — large file outside the renderer)
--   other       → external-blob   (intent-neutral landing for unknown opaque)
--   eval_curve  → metric-chart    (scalar curve; schema inferred at render time)
--   log         → prose-document  (text/plain in mime; markdown renderer falls through)
--   report      → prose-document  (text/markdown in mime)
--   figure      → image           (single static plot/image)
--   sample      → image           (legacy emitted only image samples in practice)
--
-- Mapping is intentionally lossy — `dataset` and `checkpoint` collapse
-- into `external-blob` because mobile renders them the same way (URI
-- chip). The audit log preserves the original name for forensic
-- queries; the lineage column is untouched.

UPDATE artifacts SET kind = 'external-blob'
 WHERE kind IN ('checkpoint', 'dataset', 'other');

UPDATE artifacts SET kind = 'metric-chart'
 WHERE kind = 'eval_curve';

UPDATE artifacts SET kind = 'prose-document'
 WHERE kind IN ('log', 'report');

UPDATE artifacts SET kind = 'image'
 WHERE kind IN ('figure', 'sample');

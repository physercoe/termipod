-- ADR-030 (governed actions and propose verb) W1 — schema migration.
--
-- Adds the five `attention_items` columns the generic `propose` MCP
-- verb needs to round-trip a load-bearing state change through the
-- attention queue. Every column is nullable; pre-migration rows
-- (including all rows already written by approval_request/
-- template_proposal/permission_prompt code paths) stay valid with
-- no rewrite. The propose-handler (W4) populates the new columns at
-- INSERT time; the decide path (W8 alias dispatchers + W5/W6/W7
-- apply functions) reads them at /decide time and writes the
-- applied-spec back to `executed_json`.
--
-- Disjoint from migration 0042 (loop-entity columns from ADR-034) —
-- no literal collision, but two conceptual overlaps the propose
-- handler must respect (see ADR-030 plan §2.2 W1):
--   1) `cause` (lineage, ADR-034 D-8) vs `target_ref_json` (mutation
--      target, ADR-030 D-1). For task.set_status the two often hold
--      the same task_id; the propose handler MUST populate both.
--   2) `assigned_tier` (decision authority, ADR-030 D-3, IMMUTABLE)
--      vs `escalation_state` (signal state, ADR-034 D-4, WALKS). Per
--      the Option 2′ rewrite of D-7, `assigned_tier` does not move
--      across ticks; the sweep walks `escalation_state` instead.
--
-- Indexes are partial on the new columns so the migration is cheap
-- on existing rows (no entries get added until propose is exercised)
-- and matches the precedent of 0019_artifacts (idx_artifacts_run,
-- idx_artifacts_sha).

ALTER TABLE attention_items ADD COLUMN change_kind      TEXT;
ALTER TABLE attention_items ADD COLUMN assigned_tier    TEXT
  CHECK (assigned_tier IS NULL OR assigned_tier IN
    ('worker', 'project-steward', 'general-steward', 'principal'));
ALTER TABLE attention_items ADD COLUMN change_spec_json TEXT;
ALTER TABLE attention_items ADD COLUMN target_ref_json  TEXT;
ALTER TABLE attention_items ADD COLUMN executed_json    TEXT;

CREATE INDEX idx_attention_change_kind
  ON attention_items(change_kind)
  WHERE change_kind IS NOT NULL;
CREATE INDEX idx_attention_assigned_tier
  ON attention_items(assigned_tier, status)
  WHERE assigned_tier IS NOT NULL;

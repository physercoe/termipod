-- Migration 0023 made the unique-handle constraint exclude *archived* rows,
-- but the "Recreate steward" UI flow only terminates the old steward — it
-- doesn't archive. Terminated-but-not-archived rows kept the handle
-- reserved, so the next spawn still failed with SQLITE_CONSTRAINT_UNIQUE
-- (2067) → HTTP 409.
--
-- Widen the partial-index predicate to exclude any non-live status. Once a
-- row is terminated / failed / crashed, its handle is freed for reuse
-- regardless of whether the operator has run the explicit archive step.

DROP INDEX IF EXISTS agents_team_handle_active;

CREATE UNIQUE INDEX agents_team_handle_active
    ON agents(team_id, handle)
    WHERE archived_at IS NULL
      AND status NOT IN ('terminated', 'failed', 'crashed');

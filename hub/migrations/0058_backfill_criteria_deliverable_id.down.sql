-- Not safely reversible: once backfilled, a deliverable_id set by this
-- migration is indistinguishable from one set legitimately at create
-- time, so nulling them back out would clobber correct data. Down is a
-- no-op (cf. 0057). The forward direction is idempotent — re-running it
-- only fills rows still NULL.
SELECT 1;

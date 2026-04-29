-- One-shot data heal — no reverse. Down is a no-op so the migration
-- is reversible (golang-migrate requires a down file) without
-- pretending we can reconstruct the prior orphan-active state.
SELECT 1;

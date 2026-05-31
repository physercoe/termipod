-- 0047_owner_tokens_to_operator.down.sql
--
-- Rolling back the operator/principal split: the down direction is
-- inherently lossy. Pre-0047 there were no operators, so up-migration
-- converted *every* owner to operator; but after the split, an install
-- may have minted genuine per-team `owner` tokens alongside the
-- operator. Mechanically converting every operator back to owner would
-- be correct for the historical root but would also demote any
-- legitimately-minted operators that post-date the split.
--
-- We document the no-op rather than mechanically reverse. A full
-- rollback of W2 should restore the pre-0047 snapshot; the operator
-- token kind simply stops being honoured once the W2 code is reverted
-- (auth.Middleware's allowlist drops it), so a stranded operator row is
-- inert rather than dangerous.

SELECT 'migration 0047 down: no-op (operator/principal split is one-way; restore a snapshot to fully revert)';

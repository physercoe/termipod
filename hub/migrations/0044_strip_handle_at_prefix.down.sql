-- 0044_strip_handle_at_prefix.down.sql
--
-- The up-migration is data-only and inherently lossy in the down
-- direction: stripping an `@` from `@coder` → `coder` is a fact, not
-- a transformation we can perfectly reverse without ambiguity (we
-- can't tell which originally-bare rows we should NOT re-prefix). A
-- mechanical re-prefix of every row would be wrong for principal-
-- minted agents whose handles were always bare.
--
-- We document the no-op explicitly rather than mechanically reverse:
-- if you must roll back, the operator should restore the pre-0044
-- snapshot. A SELECT-trace can be inserted here if you want a
-- best-effort report of "which handles changed" — see the audit
-- log for the v1.0.637 sweep window.

SELECT 'migration 0044 down: no-op (data normalization is one-way)';

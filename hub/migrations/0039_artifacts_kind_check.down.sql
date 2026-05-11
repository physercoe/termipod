-- Reverse-direction is information-lossy: multiple legacy kinds collapsed
-- into one MVP kind, so there's no way to recover the exact original
-- string from the new value. Down-migration is a no-op — the closed-set
-- validation lives in Go, so reverting to the open vocabulary is a code
-- rollback, not a schema change.
--
-- If you need legacy strings back for forensic work, the audit_events
-- table preserved the original `kind` at create time
-- (artifact.create entry, meta.kind field).

SELECT 1; -- no-op

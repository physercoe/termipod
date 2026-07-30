-- Slice: ADR-061 blob lifetime — a declared class per blob, and expiry.
--
-- The blob store has never deleted anything. Nothing calls DELETE on `blobs`,
-- there is no DELETE endpoint, and the only referrer with a foreign key is
-- run_images. That was survivable while every writer was storing something a
-- row owned; it stops being survivable once a browsed feature writes cache
-- bytes through the same door.
--
-- Two classes, and only two (D-1):
--
--   owned    Some other row is the referrer. Lifetime is EXACTLY today's:
--            permanent, deleted only if a future ADR builds a reference-graph
--            GC. `DEFAULT 'owned'` is what makes this migration semantically a
--            no-op for every blob and every writer that already exists.
--   derived  Reproducible from inputs the hub still has. Carries a non-NULL
--            expires_at; losing it is a cache miss.
--
-- The class is declared by the WRITER and never inferred (D-2). Any rule of the
-- form "large, or this mime, or from this endpoint means cacheable" eventually
-- classifies an irreplaceable event payload as disposable, and that failure is
-- silent and delayed.
--
-- The partial index is what keeps the sweeper's scan proportional to the number
-- of expiring blobs rather than to the whole store — which, for a store whose
-- permanent population only grows, is the difference that matters.
--
-- Note for anyone looking for a size drop after a sweep: there will not be one.
-- `blobs` lives in hub.db, where auto_vacuum=NONE makes incremental_vacuum a
-- documented no-op (store_maintenance.go), so deleted rows return their pages to
-- the freelist rather than to the OS. The reclamation that matters here is the
-- FILE unlink; a blob row is tiny next to the bytes it names.
ALTER TABLE blobs ADD COLUMN class      TEXT NOT NULL DEFAULT 'owned';
ALTER TABLE blobs ADD COLUMN expires_at TEXT;  -- NULL = never

CREATE INDEX idx_blobs_expiring ON blobs(expires_at) WHERE expires_at IS NOT NULL;

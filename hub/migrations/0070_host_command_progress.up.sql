-- Slice: ADR-058 host job surface — the progress heartbeat on a command row.
--
-- Job kinds (dataset_export_rrd today; dataset_extract_episode later) execute
-- DETACHED on the host-runner, so they cannot report through the synchronous
-- request/response path the 60s dataset verbs use. A running job PATCHes coarse
-- progress -- {phase, done, total} -- into progress_json at least every 30s.
--
-- progress_at is the hub's OWN receipt time for that patch, and it is what the
-- stale-job sweep compares against. It is deliberately not read back out of
-- progress_json: a liveness verdict must never depend on a remote host's clock,
-- or skew on one box decides whether the hub declares its jobs dead. NULL until
-- the first heartbeat arrives, so the sweep reads
-- COALESCE(progress_at, delivered_at).
--
-- No status value is added. `delivered` already means "running" for a job kind
-- and `done`/`failed` stay terminal, so every existing consumer of the command
-- lifecycle keeps working unchanged (ADR-058 §3).
ALTER TABLE host_commands ADD COLUMN progress_json TEXT;
ALTER TABLE host_commands ADD COLUMN progress_at TEXT;

-- A partial index over just the in-flight rows. The sweep scans
-- status='delivered' across every host, which the existing (host_id, status)
-- index cannot serve, and host_commands has no retention policy — so keeping
-- the sweep off a full-table scan matters more as the table grows.
CREATE INDEX idx_host_commands_inflight
    ON host_commands(kind, progress_at, delivered_at)
    WHERE status = 'delivered';

-- Track the host-runner binary's git revision and build time so operators
-- can tell at a glance whether a host is up-to-date. Populated from
-- runtime/debug.ReadBuildInfo on the host-runner side, sent in the
-- heartbeat body so binary swaps surface within ~10s without needing a
-- re-register. NULLs are fine — older clients (or binaries built outside
-- a git tree) just don't surface.
ALTER TABLE hosts ADD COLUMN runner_commit TEXT;
ALTER TABLE hosts ADD COLUMN runner_build_time TEXT;
ALTER TABLE hosts ADD COLUMN runner_modified INTEGER;

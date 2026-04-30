-- Resume cursor for engine-side session continuity (ADR-014).
-- Captured from claude-code's session.init `session_id` (and the
-- equivalent field on other engines). On `POST /sessions/{id}/resume`,
-- the handler splices `--resume <id>` into the rendered spawn cmd so
-- the freshly-spawned engine reattaches to its prior conversation
-- history instead of starting cold. Nullable: pre-init agents and
-- engines that don't surface a session_id leave it empty, and resume
-- falls back to its pre-ADR-014 cold-start behaviour.
ALTER TABLE sessions ADD COLUMN engine_session_id TEXT;

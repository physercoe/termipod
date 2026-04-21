-- audit_events — append-only trail of sensitive administrative actions.
--
-- Populated by server-side write-hooks in handlers that mutate agents,
-- attention items, schedules, and (future) tokens / hosts / policies.
-- The events table is for channel messages (replayable via event_log.go);
-- this is distinct and holds "who did what, when" for compliance.

CREATE TABLE audit_events (
    id            TEXT PRIMARY KEY,
    team_id       TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    ts            TEXT NOT NULL,
    actor_token_id TEXT,                                -- auth_tokens.id; NULL for system events
    actor_kind    TEXT NOT NULL DEFAULT 'system',       -- owner|user|agent|host|system
    actor_handle  TEXT,                                 -- resolved from scope.handle / role
    action        TEXT NOT NULL,                        -- e.g. agent.spawn, attention.decide
    target_kind   TEXT,                                 -- agent|attention|schedule|host|token
    target_id     TEXT,
    summary       TEXT NOT NULL,
    meta_json     TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_team_ts ON audit_events(team_id, ts DESC);
CREATE INDEX idx_audit_team_action_ts ON audit_events(team_id, action, ts DESC);

-- Add channels.team_id so team-scope channels (#hub-meta, the principal ↔
-- steward room) are isolated per team. Before this, team-scope channels had
-- project_id NULL and NO team binding, and ensureTeamChannel /
-- handleListTeamChannels / mcpListChannels filtered without a team — so every
-- team SHARED one #hub-meta and a second team's general steward landed in the
-- first team's room (ADR-037 G6 / W6, the highest-value sweep leak surfaced
-- in W3). Project-scope channels were already isolated transitively via
-- projects.team_id; we backfill them too so a single uniform
-- `channels.team_id` powers the cross-team event-handler guard.
--
-- SQLite has no ALTER TABLE DROP CONSTRAINT and we want team_id NOT NULL, so
-- we recreate the table (mirrors 0023). The migration runs with
-- foreign_keys=OFF (set on the migrations connection in db.go), so DROP TABLE
-- channels does NOT fire ON DELETE CASCADE against events / channel_members
-- and silently wipe dependent rows; the FKs reference channels(id) by name
-- and are preserved across the RENAME.

CREATE TABLE channels_new (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    project_id  TEXT REFERENCES projects(id) ON DELETE CASCADE,  -- NULL = team scope
    scope_kind  TEXT NOT NULL,                     -- 'team' | 'project'
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE(scope_kind, project_id, name)
);

-- Backfill: project-scope rows inherit their project's team; team-scope rows
-- (project_id NULL) belong to `default` — the only team that existed before
-- multi-team isolation, so every pre-existing #hub-meta is default's.
INSERT INTO channels_new (id, team_id, project_id, scope_kind, name, created_at)
SELECT c.id,
       COALESCE(p.team_id, 'default'),
       c.project_id, c.scope_kind, c.name, c.created_at
FROM channels c
LEFT JOIN projects p ON p.id = c.project_id;

DROP TABLE channels;
ALTER TABLE channels_new RENAME TO channels;

-- One team-scope channel of a given name per team (the table-level
-- UNIQUE above can't enforce this because project_id is NULL and SQLite
-- treats NULLs as distinct). This is what lets each team have its own
-- #hub-meta while blocking a duplicate within a team.
CREATE UNIQUE INDEX channels_team_scope_name_unique
    ON channels(team_id, scope_kind, name) WHERE project_id IS NULL;
CREATE INDEX idx_channels_team ON channels(team_id);

DROP INDEX IF EXISTS agents_team_handle_active;

CREATE UNIQUE INDEX agents_team_handle_active
    ON agents(team_id, handle) WHERE archived_at IS NULL;

-- Environment profiles (plan docs/plans/env-profiles-and-session-teleport.md,
-- wedge E1). A team-scoped, reusable bundle of {setup script + plain env vars +
-- secret references + network policy} that a spawn can attach so agents start in
-- a prepared environment — the gap the cloud-agent comparison surfaced (Claude
-- Code on the web / Codex cloud treat this as a first-class entity; termipod had
-- no env/setup fields on spawns at all).
--
-- Data-ownership law (blueprint §4): the hub holds NAMES + non-secret fields
-- only. `env_vars_json` and `setup_script` are hub-visible by design (secrets do
-- not belong in either). `secret_refs_json` holds *references* into the team's
-- zero-knowledge vault — never values; the hub never sees a secret value
-- (forbidden-pattern #15). In E1 secret_refs are stored + round-tripped but NOT
-- consumed at spawn (host-key envelopes land in E3).
--
-- Hard delete, no deleted_at — matches the rest of the schema (references,
-- tasks, …). Snapshot semantics make this safe: a spawn copies the resolved
-- env_vars + setup_script into its spawn_spec at spawn time, so deleting a
-- profile never mutates a running or historical agent's environment.

CREATE TABLE env_profiles (
    id                   TEXT PRIMARY KEY,
    team_id              TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    setup_script         TEXT NOT NULL DEFAULT '',      -- bash run in the workdir before the agent cmd
    setup_failure_policy TEXT NOT NULL DEFAULT 'fail',  -- fail|continue (default fail-closed)
    env_vars_json        TEXT NOT NULL DEFAULT '{}',    -- JSON object {KEY: value} (plain, hub-visible)
    secret_refs_json     TEXT NOT NULL DEFAULT '[]',    -- JSON array [{key, vault_item}] — refs, never values
    network_policy_json  TEXT NOT NULL DEFAULT '{"mode":"open"}',  -- {mode: open|allowlist|offline, allowlist}
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE INDEX idx_env_profiles_team ON env_profiles(team_id);
-- Names are the human handle used to pick a profile in the spawn sheet (E2); one
-- name per team keeps attach-by-name unambiguous. UNIQUE → the store returns 409
-- on a duplicate (mapDBError: SQLITE_CONSTRAINT → StatusConflict).
CREATE UNIQUE INDEX idx_env_profiles_team_name ON env_profiles(team_id, name);

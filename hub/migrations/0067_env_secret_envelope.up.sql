-- Env-profile secret delivery (ADR-056 D-3): the opaque, host-sealed envelope
-- carrying a profile's resolved secret_refs for this spawn. Set by a resolving
-- client (the only party holding the vault key); the hub stores and forwards it
-- but can never decrypt it. NULL when the spawn's profile carries no secrets.
-- Snapshot semantics like spawn_spec_yaml: fixed at spawn, reused on restart.
ALTER TABLE agent_spawns ADD COLUMN env_secret_envelope TEXT;

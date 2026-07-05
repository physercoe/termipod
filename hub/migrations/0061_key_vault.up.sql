-- Zero-knowledge SSH key vault sync (ADR-052 D-4). The hub is a BLIND blob
-- store: it holds only client-side-encrypted ciphertext it can never decrypt,
-- keyed to the calling principal, and syncs it across that principal's devices.
-- This is the carve-out that amends forbidden-pattern #15 — the hub never holds
-- the vault key or any plaintext, so it can never authenticate as the user.
--
-- Two tables:
--   key_vaults        - the one sealed vault blob per principal (versioned).
--   key_vault_devices - per-device enrollment: the device's public key plus the
--                       vault key wrapped to it (both opaque to the hub).

CREATE TABLE key_vaults (
    team_id             TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    handle              TEXT NOT NULL,               -- principal owner (server-derived from token scope)
    ciphertext          TEXT NOT NULL,               -- opaque AEAD-sealed vault (connections + keys), client-encrypted
    version             INTEGER NOT NULL DEFAULT 1,  -- optimistic-concurrency counter
    -- Recovery escrow (ADR-052 D-4): the vault key wrapped under a director-held
    -- recovery key. Opaque to the hub (it never holds the recovery key). Lets a
    -- principal who has lost every enrolled device recover the vault.
    recovery_envelope   TEXT,                        -- wrapped vault key; NULL until set
    recovery_hint       TEXT,                        -- non-secret label, e.g. "recovery code created 2026-07-05"
    recovery_updated_at TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    PRIMARY KEY (team_id, handle)
);

CREATE TABLE key_vault_devices (
    id          TEXT PRIMARY KEY,
    team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    handle      TEXT NOT NULL,               -- principal owner (server-derived)
    device_id   TEXT NOT NULL,               -- client-chosen stable device id
    device_name TEXT,                        -- human label ("desktop", "phone")
    public_key  TEXT NOT NULL,               -- device public key (opaque, for wrapping)
    wrapped_key TEXT,                         -- vault key wrapped to this device (opaque); NULL until an enrolled device wraps it
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (team_id, handle, device_id)
);

CREATE INDEX idx_key_vault_devices_owner ON key_vault_devices(team_id, handle);

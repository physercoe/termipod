-- Record which machine last synced the vault (desktop vault-status detail).
-- The vault blob itself is opaque ciphertext, but the pushing device's human
-- label is already non-secret metadata (it lives plaintext in
-- key_vault_devices.device_name), so recording the last writer here leaks
-- nothing new and lets any device show "last synced <when> from <machine>"
-- authoritatively — the hub's updated_at is the true last-push time, and this
-- column names the device that did it. NULL until a device that sends its name
-- pushes (older/mobile clients that omit it leave the prior value intact).
ALTER TABLE key_vaults ADD COLUMN last_device_name TEXT;

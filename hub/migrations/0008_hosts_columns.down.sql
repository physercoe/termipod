-- Reverse P0.6.  modernc.org/sqlite supports DROP COLUMN natively.
-- capabilities_json is NOT dropped here — it was introduced in 0001_initial.
ALTER TABLE hosts DROP COLUMN capabilities_probed_at;
ALTER TABLE hosts DROP COLUMN ssh_hint_json;

-- P0.6: host SSH hints + capability probe timestamp (blueprint §5.3.2, §5.3.3).
--
-- ssh_hint_json stores *non-secret* hints only — hostname, port, username,
-- optional jump_hint — so the mobile client can bind a live SSH session to a
-- host-row without the user re-typing connection details.  Forbidden-pattern
-- #15 (§7) and the data-ownership law (§4) forbid storing passwords, private
-- keys, passphrases, or any other secret material on the hub; the handler
-- rejects such keys with HTTP 400.
--
-- capabilities_probed_at records when the host-runner last refreshed
-- capabilities_json (agent-binary presence + supported modes).  The column
-- capabilities_json itself already exists from 0001_initial; we only add
-- the probe timestamp alongside the new hint column.

ALTER TABLE hosts ADD COLUMN ssh_hint_json TEXT;
ALTER TABLE hosts ADD COLUMN capabilities_probed_at TEXT;

-- The W2-S1 migration shim labelled every back-stamped session
-- 'Legacy steward (pre-sessions)' so operators could identify them
-- in the DB. That string then leaked into the mobile UI as the
-- session's display title, which is confusing for end users
-- ("legacy what? did something go wrong?").
--
-- Drop the label so the mobile fallback ("Steward session") takes
-- over. The information is still recoverable from the audit log if
-- anyone ever needs to know which sessions were created by the
-- shim — they have the earliest opened_at on a session whose
-- current_agent_id matches a steward's first agent_spawns row.
UPDATE sessions
   SET title = NULL
 WHERE title = 'Legacy steward (pre-sessions)';

-- Archive flag for agents. Operators tap "Delete" on terminated agents
-- to hide them from the live list; the row stays in the DB so audit
-- events and spawn history continue to resolve.
ALTER TABLE agents ADD COLUMN archived_at TEXT;

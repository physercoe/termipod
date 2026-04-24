-- W3: Task priority (blueprint §6.1).
--
-- A fixed four-value priority enum on the task row. Governance-free
-- (no user-created vocabulary to curate) and deliberately minimal: we
-- explicitly deferred a generic labels primitive for post-MVP. Priority
-- is the single axis labels usually carry that pays off immediately in
-- the hub taskboard / Me-tab triage surfaces.
--
-- Values: 'low' | 'med' | 'high' | 'urgent'. Default 'med' so every
-- existing task lands in the neutral middle without any backfill.

ALTER TABLE tasks ADD COLUMN priority TEXT NOT NULL DEFAULT 'med'
    CHECK (priority IN ('low','med','high','urgent'));
CREATE INDEX idx_tasks_priority ON tasks(priority);

-- Project-scoped insights aggregator (W2 / ADR-022 D4).
-- The /v1/insights endpoint sums `agent_events` by project. Joining
-- back through sessions(scope_kind='project') on every read scaled
-- linearly with transcript volume; a denormalized column + composite
-- (project_id, ts) index keeps the aggregator O(matching rows)
-- regardless of session count.
--
-- audit_events.project_id is intentionally NOT added (per
-- insights-phase-1.md §4): json_extract over audit_events.payload_json
-- is fine at MVP scale and a later phase can promote it.

ALTER TABLE agent_events ADD COLUMN project_id TEXT;

CREATE INDEX idx_agent_events_project_ts
  ON agent_events(project_id, ts)
  WHERE project_id IS NOT NULL;

-- One-shot backfill from sessions. golang-migrate runs each .up.sql
-- in a single transaction, so we can't loop-and-chunk inside SQL.
-- At MVP scale (target <100k rows per insights-phase-1.md §3) this
-- runs in well under 5s with WAL+synchronous=NORMAL; deployments
-- with significantly more events should run a manual one-off
-- backfill before promoting the migration.
UPDATE agent_events
   SET project_id = (
     SELECT s.scope_id FROM sessions s
      WHERE s.id = agent_events.session_id
        AND s.scope_kind = 'project'
   )
 WHERE session_id IS NOT NULL
   AND project_id IS NULL;

-- Narrow the FTS update trigger to fire only when payload_json
-- actually changes. Migration 0031 wrote it as `AFTER UPDATE ON
-- agent_events`, which means our project_id stamp below would re-run
-- the rebuild path. The FTS content is derived solely from
-- payload_json, so scoping the trigger to that column is both correct
-- and the only change that lets us safely add a column-update trigger.
DROP TRIGGER IF EXISTS agent_events_fts_update;
CREATE TRIGGER agent_events_fts_update
  AFTER UPDATE OF payload_json ON agent_events
BEGIN
  DELETE FROM agent_events_fts WHERE event_id = old.id;
  INSERT INTO agent_events_fts(event_id, text)
  VALUES (new.id, new.payload_json);
END;

-- Trigger keeps project_id in sync without touching the seven existing
-- INSERT INTO agent_events sites scattered across the server package.
-- Fires only when the inserter left project_id NULL but provided a
-- session_id; if the session is project-scoped we copy the scope_id
-- in. Because triggers run in the same transaction as the insert, the
-- joined sessions row sees the same MVCC snapshot as the caller.
CREATE TRIGGER agent_events_stamp_project
  AFTER INSERT ON agent_events
  WHEN NEW.session_id IS NOT NULL AND NEW.project_id IS NULL
BEGIN
  UPDATE agent_events
     SET project_id = (
       SELECT s.scope_id FROM sessions s
        WHERE s.id = NEW.session_id
          AND s.scope_kind = 'project'
     )
   WHERE id = NEW.id;
END;

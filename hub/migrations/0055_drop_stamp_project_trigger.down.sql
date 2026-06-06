-- Recreate the trigger dropped by the up migration (verbatim from 0036).
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

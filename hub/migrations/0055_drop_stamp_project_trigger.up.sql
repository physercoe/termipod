-- ADR-045 P1 step 4: drop the agent_events_stamp_project trigger.
--
-- The trigger (migration 0036) stamped agent_events.project_id after insert by
-- reading the sessions table. That is a cross-store read: agent_events moves to
-- events.db while sessions stays in hub.db (control), so the trigger can no
-- longer see sessions once the files split. The same resolution now happens
-- handler-side in insertAgentEvent (it reads the session's project from the
-- control store before the insert and writes project_id directly). The FTS
-- triggers are NOT dropped — they touch only agent_events + agent_events_fts,
-- both of which move together to events.db.

DROP TRIGGER IF EXISTS agent_events_stamp_project;

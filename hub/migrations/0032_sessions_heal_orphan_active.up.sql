-- Heal orphan-active sessions: rows whose status='active' but whose
-- current_agent_id points to an agent that is no longer alive (or
-- doesn't exist anymore). These accumulate when an agent dies via a
-- code path that didn't go through PATCH /agents/{id} status=terminated
-- (the path that auto-pauses sessions, added in v1.0.326). The mobile
-- UI buckets them under "Detached sessions" but used to render them
-- with a green "active" pill, which is misleading — there is no
-- engine to talk to.
--
-- This is a one-shot heal for current bad data. Future terminations
-- through the API auto-pause as part of the patch path.

UPDATE sessions
   SET status = 'paused',
       last_active_at = COALESCE(last_active_at, opened_at)
 WHERE status = 'active'
   AND (
        current_agent_id IS NULL
        OR current_agent_id NOT IN (
             SELECT id FROM agents
              WHERE status NOT IN ('terminated','failed','crashed')
                AND archived_at IS NULL
           )
   );

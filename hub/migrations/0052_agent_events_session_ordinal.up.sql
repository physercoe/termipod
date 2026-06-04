-- ADR-042 P1: session_ordinal — a dense, gap-free, monotonic-per-session
-- event coordinate.
--
-- `seq` is per-agent (UNIQUE(agent_id, seq)). A resumed session spans multiple
-- agents (resume mints a new agent_id, keeps the session_id), so the agents'
-- seq ranges OVERLAP — each restarts at 1. The session-scoped Insight surface
-- (transcript, digest, turn index, error samples) therefore needs a coordinate
-- that is unique and monotonic across the WHOLE session. `session_ordinal` is
-- that coordinate, assigned at insert (insertAgentEvent) as
-- COALESCE(MAX(session_ordinal),0)+1 WHERE session_id = ?. It is NULL for
-- events with no session (they never appear in a session view).

ALTER TABLE agent_events ADD COLUMN session_ordinal INTEGER;

-- Backfill existing rows so dev/test DBs stay self-consistent. The canonical
-- session order is (ts, agent_id, seq) — the same order the session-scoped
-- list endpoint uses. No production data exists today, so on a fresh DB this
-- updates zero rows (the migration runs before any events).
UPDATE agent_events
   SET session_ordinal = sub.rn
  FROM (
    SELECT id, ROW_NUMBER() OVER (
             PARTITION BY session_id ORDER BY ts, agent_id, seq
           ) AS rn
      FROM agent_events
     WHERE session_id IS NOT NULL
  ) AS sub
 WHERE agent_events.id = sub.id;

-- Uniqueness + the keyset index for session-ordinal paging (ADR-042 P3).
-- Partial so session-less events (NULL session_id / NULL ordinal) are
-- unconstrained.
CREATE UNIQUE INDEX ux_agent_events_session_ordinal
    ON agent_events(session_id, session_ordinal)
    WHERE session_id IS NOT NULL;

-- ADR-045 P1 step 4: denormalize the session id onto the turn index.
--
-- The OTLP export's session watermark (sessionsWithClosedTurnsSince) grouped
-- closed turns by session via a JOIN to agent_events — but agent_turns and
-- agent_events move to separate store files in this step, so a cross-file join
-- is impossible. Carrying session_id on the turn row keeps the watermark a
-- single-table read in the digest store.
--
-- The fold (openTurn) stamps session_id on every new turn from the turn's
-- start event; this backfills existing rows from the start event's session.
-- A session-less agent's turns read back as '' (the column default).

ALTER TABLE agent_turns ADD COLUMN session_id TEXT NOT NULL DEFAULT '';

UPDATE agent_turns
   SET session_id = COALESCE((
       SELECT e.session_id
         FROM agent_events e
        WHERE e.agent_id = agent_turns.agent_id
          AND e.seq = agent_turns.start_seq
   ), '');

-- The export watermark groups closed turns by session.
CREATE INDEX idx_agent_turns_session ON agent_turns(session_id);

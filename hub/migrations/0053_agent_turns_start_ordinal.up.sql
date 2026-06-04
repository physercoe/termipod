-- ADR-042 P2: the turn index gains start_ordinal — the session_ordinal of the
-- turn's start event, the session-unique anchor the Insight Navigator lands on
-- (start_seq is per-agent and collides across a resumed session's agents).
--
-- Populated by digest_fold (openTurn) and persisted by saveTurnRow. The digest
-- schema-version bump (4 → 5) refolds sealed digests so existing turns gain it;
-- this column just gives the refold somewhere to land. NULL on pre-refold rows
-- and for session-less agents (read back as 0).

ALTER TABLE agent_turns ADD COLUMN start_ordinal INTEGER;

-- "jump to the turn enclosing session-ordinal N" — the session-ordinal analogue
-- of idx_agent_turns_agent_seq.
CREATE INDEX idx_agent_turns_agent_ordinal ON agent_turns(agent_id, start_ordinal);

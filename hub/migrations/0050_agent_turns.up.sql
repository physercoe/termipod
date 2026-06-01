-- ADR-038 §3: the turn index — a first-class, queryable row per turn.
--
-- A turn is bracketed by turn.start {turn_id, ts} and turn.result {turn_id,
-- ts, status, ...}. The driver emits turn.start at the prompt-dispatch
-- boundary; until a driver adopts it the hub synthesizes a turn (boundary =
-- the first event after the prior turn.result; turn_id synthetic). One row is
-- opened on the start and closed on its turn.result, maintained incrementally
-- alongside agent_event_digests.
--
-- This one structure serves both consumers: navigation ("jump to turn k" →
-- start_seq; the session timeline = the union of agents' turns ordered by
-- start_ts) and the OTLP projection (each row is a span — ADR-038 §4). A child
-- table (not a JSON blob on the digest) keeps it queryable and paginated for
-- long runs.

CREATE TABLE agent_turns (
    agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    turn_id      TEXT NOT NULL,
    team_id      TEXT NOT NULL,
    idx          INTEGER NOT NULL,            -- 0-based per agent
    start_seq    INTEGER NOT NULL,
    start_ts     TEXT NOT NULL,
    end_seq      INTEGER NOT NULL DEFAULT 0,  -- 0 while the turn is open
    end_ts       TEXT NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,  -- end_ts − start_ts (wall clock; universal)
    status       TEXT NOT NULL DEFAULT '',
    cost_usd     REAL NOT NULL DEFAULT 0,
    in_tokens    INTEGER NOT NULL DEFAULT 0,
    out_tokens   INTEGER NOT NULL DEFAULT 0,
    tool_count   INTEGER NOT NULL DEFAULT 0,
    tool_failed  INTEGER NOT NULL DEFAULT 0,
    error_count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (agent_id, turn_id)
);

-- (agent_id, idx) for "jump to turn k"; (agent_id, start_seq) for the
-- range-scan that maps a seq anchor to its enclosing turn.
CREATE INDEX idx_agent_turns_agent_idx  ON agent_turns(agent_id, idx);
CREATE INDEX idx_agent_turns_agent_seq  ON agent_turns(agent_id, start_seq);

-- ADR-038 §1: per-agent event digest — a materialized read model over the
-- hub-owned event log (agent_events, migration 0011). Distinct from
-- run_metrics (0014), which digests host-owned numeric curves.
--
-- One row per agent, maintained INCREMENTALLY in the same transaction as the
-- agent_events insert (handlers_agent_events.go POST). watermark_seq is the
-- max seq folded so far (the consistent cut); every scalar below is a fold of
-- the events at or below it. The error number is the canonical union
-- (digest_fold.go), so /v1/insights and the transcript Errors lens reconcile.
--
-- A pre-existing agent with no row yet is backfilled lazily on first read
-- (the single O(n) pass; no periodic recompute).

CREATE TABLE agent_event_digests (
    agent_id          TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    team_id           TEXT NOT NULL,
    schema_version    INTEGER NOT NULL DEFAULT 1,
    updated_at        TEXT NOT NULL,

    watermark_seq     INTEGER NOT NULL DEFAULT 0,   -- max seq folded (the cut)
    event_count       INTEGER NOT NULL DEFAULT 0,
    turn_count        INTEGER NOT NULL DEFAULT 0,
    first_ts          TEXT NOT NULL DEFAULT '',
    last_ts           TEXT NOT NULL DEFAULT '',
    duration_ms       INTEGER NOT NULL DEFAULT 0,   -- last_ts − first_ts, wall clock

    cost_usd          REAL NOT NULL DEFAULT 0,       -- summed turn.result.cost_usd
    by_model_json     TEXT NOT NULL DEFAULT '{}',    -- model → {in,out,cache_read,cache_create}

    error_count       INTEGER NOT NULL DEFAULT 0,    -- canonical union (digest_fold.go)
    errors_json       TEXT NOT NULL DEFAULT '{}',    -- class → {count, sample_seqs[]}

    tool_total        INTEGER NOT NULL DEFAULT 0,
    tool_failed       INTEGER NOT NULL DEFAULT 0,
    tools_json        TEXT NOT NULL DEFAULT '{}',    -- name → {calls, failed, sample_seqs[]}

    latency_hist_json TEXT NOT NULL DEFAULT '{}',    -- fixed log-scale turn-latency buckets (mergeable)

    outcome           TEXT NOT NULL DEFAULT ''       -- assigned task state, else terminal/last-turn status
);

-- The insights sum-refactor (ADR-038 §5) merges the in-scope per-agent rows;
-- team_id is its primary filter axis.
CREATE INDEX idx_agent_event_digests_team ON agent_event_digests(team_id);

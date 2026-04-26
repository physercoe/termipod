-- Sessions are the durable conversational frame around an agent.
-- One identity (the steward agent row), many sessions over time;
-- transcript belongs to the session via session_id stamped on
-- agent_events, so a session survives a host-runner restart that
-- replaces the underlying agent process.
--
-- Scope: lifecycle (open / interrupted / closed) + the file
-- handle the resume path needs (worktree_path, spawn_spec_yaml).
-- Artifact loading at open and distillation at close are
-- explicitly deferred per the active workband — this table holds
-- the columns that future wedges will use without forcing them
-- now.

CREATE TABLE sessions (
  id                  TEXT PRIMARY KEY,
  team_id             TEXT NOT NULL,
  title               TEXT,
  scope_kind          TEXT,
  scope_id            TEXT,
  current_agent_id    TEXT,
  status              TEXT NOT NULL,         -- open | interrupted | closed
  opened_at           TEXT NOT NULL,
  last_active_at      TEXT NOT NULL,
  closed_at           TEXT,
  worktree_path       TEXT,
  spawn_spec_yaml     TEXT,
  FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE,
  FOREIGN KEY (current_agent_id) REFERENCES agents(id) ON DELETE SET NULL
);

CREATE INDEX idx_sessions_team_status_active
  ON sessions(team_id, status, last_active_at DESC);

CREATE INDEX idx_sessions_current_agent
  ON sessions(current_agent_id) WHERE current_agent_id IS NOT NULL;

-- Two open/interrupted sessions targeting the same worktree would
-- step on each other's edits. Closed sessions don't conflict —
-- their worktrees are historical artifacts that future GC can
-- reclaim.
CREATE UNIQUE INDEX idx_sessions_active_worktree
  ON sessions(team_id, worktree_path)
  WHERE status IN ('open','interrupted') AND worktree_path IS NOT NULL;

-- session_id is nullable: pre-sessions events stay where they are
-- (NULL means "before sessions existed or unscoped"); the
-- migration shim below back-fills the steward's currently-running
-- agent, so its in-flight transcript folds into a "legacy"
-- session when this lands.
ALTER TABLE agent_events ADD COLUMN session_id TEXT;
CREATE INDEX idx_agent_events_session ON agent_events(session_id, ts)
  WHERE session_id IS NOT NULL;

ALTER TABLE audit_events ADD COLUMN session_id TEXT;
ALTER TABLE attention_items ADD COLUMN session_id TEXT;

-- Migration shim: every existing live steward agent gets a
-- synthetic "open" session covering its lifetime to date. The
-- session inherits the latest spawn's worktree_path + spawn_spec_yaml
-- so resume works against pre-sessions agents too.
-- Title is left NULL so the mobile fallback ("Steward session")
-- takes over. Earlier drafts of this shim used a "Legacy steward
-- (pre-sessions)" string here; that was meant as a marker for
-- operators reading the DB, but it leaked into the UI as the
-- session's display title and confused users. See migration 0027
-- for the same cleanup applied retroactively to deployments that
-- ran the older form of this insert.
INSERT INTO sessions (
  id, team_id, title, scope_kind, current_agent_id, status,
  opened_at, last_active_at, worktree_path, spawn_spec_yaml
)
SELECT
  lower(hex(randomblob(16))),
  a.team_id,
  NULL,
  'team',
  a.id,
  'open',
  a.created_at,
  a.created_at,
  (SELECT s.worktree_path
     FROM agent_spawns s
    WHERE s.child_agent_id = a.id
    ORDER BY s.spawned_at DESC LIMIT 1),
  (SELECT s.spawn_spec_yaml
     FROM agent_spawns s
    WHERE s.child_agent_id = a.id
    ORDER BY s.spawned_at DESC LIMIT 1)
FROM agents a
WHERE a.handle = 'steward' AND a.status IN ('running','pending');

-- Stamp the legacy session on the in-flight agent's existing
-- agent_events too, so the transcript already shows up under the
-- session view on first read.
UPDATE agent_events
   SET session_id = (SELECT s.id FROM sessions s
                      WHERE s.current_agent_id = agent_events.agent_id
                        AND s.status = 'open' LIMIT 1)
 WHERE session_id IS NULL
   AND agent_id IN (SELECT current_agent_id FROM sessions WHERE status = 'open');

UPDATE sessions SET status = CASE status
  WHEN 'active'   THEN 'open'
  WHEN 'paused'   THEN 'interrupted'
  WHEN 'archived' THEN 'closed'
  ELSE status
END;

DROP INDEX IF EXISTS idx_sessions_active_worktree;
CREATE UNIQUE INDEX idx_sessions_active_worktree
  ON sessions(team_id, worktree_path)
  WHERE status IN ('open','interrupted') AND worktree_path IS NOT NULL;

-- Per ADR-009, session status uses program-shaped vocabulary:
--   open        -> active     (engine attached, conversation live)
--   interrupted -> paused     (engine detached; auto-resumes on host reattach)
--   closed      -> archived   (distillation filed; resumable via fork)
--   deleted     -> deleted    (unchanged)

UPDATE sessions SET status = CASE status
  WHEN 'open'        THEN 'active'
  WHEN 'interrupted' THEN 'paused'
  WHEN 'closed'      THEN 'archived'
  ELSE status
END;

-- The active-worktree partial index is condition-baked at create
-- time. Recreate it against the new status names so two live
-- sessions still can't share a worktree.
DROP INDEX IF EXISTS idx_sessions_active_worktree;
CREATE UNIQUE INDEX idx_sessions_active_worktree
  ON sessions(team_id, worktree_path)
  WHERE status IN ('active','paused') AND worktree_path IS NOT NULL;

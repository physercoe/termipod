-- ADR-025 W1: bind agents to a project at spawn time.
--
-- A nullable `project_id` column on `agents` lets the Agents tab on a
-- project's detail screen list its workers and steward without joining
-- through sessions. Pre-ADR rows stay NULL (no backfill — see ADR-025
-- non-goals).
--
-- ON DELETE SET NULL keeps the agent row alive when a project is
-- deleted; the audit trail (agent_spawns, agent_events) still resolves.
-- Without this, deleting a project would cascade and silently wipe the
-- workers that ran under it, which is the opposite of what a steward
-- accountability trail wants.
--
-- Partial index — most legacy rows have project_id IS NULL and we
-- only ever filter on the non-NULL subset (project-detail Agents tab,
-- agents.list?project_id=…).

ALTER TABLE agents ADD COLUMN project_id TEXT
  REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX idx_agents_project ON agents(project_id)
  WHERE project_id IS NOT NULL;

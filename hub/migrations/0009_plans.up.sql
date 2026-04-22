-- Plans and plan steps (blueprint §6.2).
--
-- A plan is an ordered, phased execution spec owned by a project. Steps are
-- the concrete units (agent spawns, LLM calls, shell, MCP, human gates) and
-- carry input/output refs so a later phase can consume an earlier phase's
-- artefacts. Template_id is a soft reference to the on-disk template name
-- that seeded the plan — no FK because templates live on the filesystem
-- under <dataRoot>/team/templates/ rather than in the DB.

CREATE TABLE plans (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    template_id  TEXT,                                -- soft ref to on-disk template
    version      INTEGER NOT NULL DEFAULT 1,
    spec_json    TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'draft',       -- draft|ready|running|completed|failed|cancelled
    created_at   TEXT NOT NULL,
    started_at   TEXT,
    completed_at TEXT
);
CREATE INDEX idx_plans_project ON plans(project_id);
CREATE INDEX idx_plans_status  ON plans(status);

CREATE TABLE plan_steps (
    id               TEXT PRIMARY KEY,
    plan_id          TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    phase_idx        INTEGER NOT NULL,
    step_idx         INTEGER NOT NULL,
    kind             TEXT NOT NULL,                   -- agent_spawn|llm_call|shell|mcp_call|human_decision
    spec_json        TEXT NOT NULL DEFAULT '{}',
    status           TEXT NOT NULL DEFAULT 'pending',
    started_at       TEXT,
    completed_at     TEXT,
    input_refs_json  TEXT NOT NULL DEFAULT '[]',
    output_refs_json TEXT NOT NULL DEFAULT '[]',
    agent_id         TEXT REFERENCES agents(id) ON DELETE SET NULL
);
CREATE INDEX idx_plan_steps_plan_order ON plan_steps(plan_id, phase_idx, step_idx);
CREATE INDEX idx_plan_steps_status     ON plan_steps(status);
CREATE INDEX idx_plan_steps_agent      ON plan_steps(agent_id);

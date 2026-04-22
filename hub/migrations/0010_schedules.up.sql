-- P0.3: schedules refactor (blueprint §6.3).
--
-- Replaces agent_schedules, which spawned agents directly — a now-forbidden
-- shortcut per §7. The new schedules table triggers a *plan* from a template;
-- plan instantiation is handled by the scheduler, execution by host-runner's
-- plan executor (Phase 1). No agent_schedules data is preserved (alpha).

DROP TABLE IF EXISTS agent_schedules;

CREATE TABLE schedules (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    template_id     TEXT NOT NULL, -- soft ref: "<category>/<name>" on disk
    trigger_kind    TEXT NOT NULL CHECK (trigger_kind IN ('cron','manual','on_create')),
    cron_expr       TEXT, -- required iff trigger_kind='cron'
    parameters_json TEXT NOT NULL DEFAULT '{}',
    enabled         INTEGER NOT NULL DEFAULT 1,
    next_run_at     TEXT,
    last_run_at     TEXT,
    last_plan_id    TEXT REFERENCES plans(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_schedules_project ON schedules(project_id);
CREATE INDEX idx_schedules_trigger ON schedules(trigger_kind, enabled);

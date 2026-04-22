-- P1.4 integration: record the concrete driving mode a spawn resolved to
-- (blueprint §5.3.2). Populated by hub's mode resolver at spawn time;
-- consumed by host-runner to pick the driver. Nullable so existing agents
-- pre-migration remain valid — they default to M4 when host-runner can't
-- find a mode declaration.

ALTER TABLE agents ADD COLUMN driving_mode TEXT;

DROP INDEX IF EXISTS idx_host_commands_agent;
DROP INDEX IF EXISTS idx_host_commands_host_status;
DROP TABLE IF EXISTS host_commands;
ALTER TABLE attention_items DROP COLUMN tier;
ALTER TABLE attention_items DROP COLUMN pending_payload_json;
ALTER TABLE agents DROP COLUMN last_capture_at;
ALTER TABLE agents DROP COLUMN last_capture;

ALTER TABLE attention_items DROP COLUMN cause;
ALTER TABLE attention_items DROP COLUMN terminal_reason;
ALTER TABLE attention_items DROP COLUMN escalation_state;
ALTER TABLE attention_items DROP COLUMN absolute_cap;
ALTER TABLE attention_items DROP COLUMN opened_at;
ALTER TABLE attention_items DROP COLUMN last_progress_at;
ALTER TABLE attention_items DROP COLUMN inactivity_deadline;

ALTER TABLE tasks DROP COLUMN terminal_reason;
ALTER TABLE tasks DROP COLUMN escalation_state;
ALTER TABLE tasks DROP COLUMN absolute_cap;
ALTER TABLE tasks DROP COLUMN opened_at;
ALTER TABLE tasks DROP COLUMN last_progress_at;
ALTER TABLE tasks DROP COLUMN inactivity_deadline;

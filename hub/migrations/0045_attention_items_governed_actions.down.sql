DROP INDEX IF EXISTS idx_attention_assigned_tier;
DROP INDEX IF EXISTS idx_attention_change_kind;

ALTER TABLE attention_items DROP COLUMN executed_json;
ALTER TABLE attention_items DROP COLUMN target_ref_json;
ALTER TABLE attention_items DROP COLUMN change_spec_json;
ALTER TABLE attention_items DROP COLUMN assigned_tier;
ALTER TABLE attention_items DROP COLUMN change_kind;

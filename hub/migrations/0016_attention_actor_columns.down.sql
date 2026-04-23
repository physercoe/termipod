-- Drop actor_{kind,handle} from attention_items. SQLite supports
-- DROP COLUMN since 3.35 (2021); all our test/prod binaries target
-- newer versions.

ALTER TABLE attention_items DROP COLUMN actor_handle;
ALTER TABLE attention_items DROP COLUMN actor_kind;

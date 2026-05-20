-- 0044_strip_handle_at_prefix.up.sql — handle normalization (post-v1.0.636).
--
-- Background: agent handles drifted from the bare-name convention
-- (`coder`, `steward.01KRB586` — what the glossary documents and what
-- principal handles always were) to an `@`-prefixed form because the
-- bundled steward templates passed `child_handle="@coder"` literally
-- and DoSpawn stored it verbatim. Worker persona files then rendered
-- `@{{parent.handle}}` against the already-prefixed value, producing
-- `@@steward.01KRB586` — a handle no a2a_card matches, surfacing as
-- "no A2A agent found for handle".
--
-- Fix is two-sided:
--   1) Code: DoSpawn strips a single leading `@` before INSERT; the
--      bundled templates also drop the literal `@` from their
--      child_handle arguments. (See hub/internal/server/handlers_agents.go
--      + hub/templates/.)
--   2) Data: this migration cleans up rows already written under the
--      old convention.
--
-- The strip is conservative — only handles starting with exactly one
-- `@` are normalized; rows already bare are untouched. a2a_cards
-- mirror agents.handle and so get the same treatment.
--
-- Uniqueness: agents has a partial UNIQUE on (team_id, handle) for
-- live rows (migration 0023). If both `@worker` and `worker` exist
-- live in the same team this UPDATE will fail loud — that's the right
-- failure mode (operator must resolve the collision before retry).

UPDATE agents
   SET handle = SUBSTR(handle, 2)
 WHERE handle LIKE '@%';

UPDATE a2a_cards
   SET handle = SUBSTR(handle, 2)
 WHERE handle LIKE '@%';

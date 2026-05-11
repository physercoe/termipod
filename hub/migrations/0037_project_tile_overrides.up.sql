-- Per-project tile composition overrides (lifecycle-walkthrough-followups W5).
-- Lets the steward + the user shape the per-phase shortcut tile set
-- without an APK rebuild. Resolution chain on the mobile side:
--   1. projects.phase_tile_overrides_json[<phase>]  (this column)
--   2. template YAML phase_specs[<phase>].tiles    (per research-template-spec)
--   3. Dart chassis default [outputs, documents]
--
-- Shape: {"<phase>": ["documents", "outputs", ...]}  — slugs only, no
-- labels/icons (those live in the chassis `tileSpecFor`). Unknown slugs
-- are dropped at parse time; the closed TileSlug enum is the
-- vocabulary, the composition is the data.

ALTER TABLE projects ADD COLUMN phase_tile_overrides_json TEXT;

-- Per-project overview-widget (hero) overrides (chassis-followup wave 1,
-- ADR-024 D10). Closes the ADR-023 inconsistency where steward + user
-- could swap tiles per phase but not heroes.
--
-- Resolution chain mirrors phase_tile_overrides:
--   1. projects.overview_widget_overrides_json[<phase>]    (this column)
--   2. template YAML phase_specs[<phase>].overview_widget  (template-side)
--   3. template YAML overview_widget                       (template-level default)
--   4. overviewWidgetDefault                               (chassis fallback)
--
-- Shape: {"<phase>": "<hero_slug>"} — slugs only, from the closed
-- kKnownOverviewWidgets set. Unknown slugs are dropped at parse time
-- mobile-side (visible-failure placeholder) and accepted-as-stored
-- server-side; the closed Dart enum is the vocabulary, the composition
-- is the data.

ALTER TABLE projects ADD COLUMN overview_widget_overrides_json TEXT;

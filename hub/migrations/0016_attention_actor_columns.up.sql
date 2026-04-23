-- IA wedge follow-up: record who raised each attention_item.
--
-- Before this migration the row stored no actor; the mobile Inbox had to
-- heuristically match on `created_by` / `source` / `origin` fields that
-- different callers spelled differently. Adding explicit actor_kind +
-- actor_handle columns (same shape as audit_events) lets every caller
-- stamp the raiser authoritatively, and lets the StewardBadge matcher
-- read one canonical field instead of guessing.
--
-- Both columns are nullable so legacy rows stay valid; existing
-- attention_items from before this migration simply carry NULL and
-- render without a badge (correct — we don't know who raised them).

ALTER TABLE attention_items ADD COLUMN actor_kind   TEXT;
ALTER TABLE attention_items ADD COLUMN actor_handle TEXT;

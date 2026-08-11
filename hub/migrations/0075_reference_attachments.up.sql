-- Portable attachment coordinates for cross-device Read-library sync.
-- Bytes remain in the configured attachment backend; the hub stores only the
-- metadata required to associate `<key>/<file>` with a reference. Absolute
-- device paths are deliberately absent from this schema.
--
-- `zotero_storage_json` remains for compatibility with older clients that know
-- only one Zotero attachment. New clients read both and write both when a
-- Zotero attachment is present.

ALTER TABLE reference_items
    ADD COLUMN attachments_json TEXT NOT NULL DEFAULT '[]';

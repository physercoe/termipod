-- ADR-020 W1: document_annotations primitive.
-- Anchored director feedback on a typed-document section. One annotation
-- per row; the optional char_start/char_end carry an in-section range when
-- the renderer can recover one (long-press on a paragraph). Annotations
-- never delete — resolve is the soft-close, per ADR-020 D3.
--
-- The kind enum mirrors ADR-020 D2: comment / redline / suggestion /
-- question. The renderer branches on kind to pick the glyph and any extra
-- affordances (suggestion shows replacement-preview when body is set).
--
-- parent_annotation_id is reserved for one-level reply per ADR-020 D1; the
-- MVP renderer ignores it (the column is added now to avoid a follow-on
-- migration when the reply UI lands).

CREATE TABLE document_annotations (
  id                    TEXT PRIMARY KEY,
  document_id           TEXT NOT NULL,
  section_slug          TEXT NOT NULL,
  char_start            INTEGER,
  char_end              INTEGER,
  kind                  TEXT NOT NULL DEFAULT 'comment'
                            CHECK (kind IN ('comment','redline','suggestion','question')),
  body                  TEXT NOT NULL,
  status                TEXT NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open','resolved')),
  author_kind           TEXT NOT NULL,
  author_handle         TEXT,
  parent_annotation_id  TEXT,
  created_at            TEXT NOT NULL DEFAULT (datetime('now')),
  resolved_at           TEXT,
  resolved_by_actor     TEXT,
  FOREIGN KEY (document_id)          REFERENCES documents(id)            ON DELETE CASCADE,
  FOREIGN KEY (parent_annotation_id) REFERENCES document_annotations(id) ON DELETE SET NULL
);

-- Per-section overlay query: render every open annotation for a section.
CREATE INDEX idx_doc_annot_doc_section
    ON document_annotations(document_id, section_slug, status);

-- "My open notes" view across docs.
CREATE INDEX idx_doc_annot_author_status
    ON document_annotations(author_handle, status)
    WHERE author_handle IS NOT NULL;

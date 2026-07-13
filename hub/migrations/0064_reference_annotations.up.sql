-- PDF annotations as child records of a reference (ADR-053 companion). The
-- data-ownership law (blueprint §4) says metadata lives on the hub, bytes stay
-- on the device — so an annotation is metadata: it never mutates the PDF file,
-- it points INTO it. This mirrors Zotero, which likewise stores annotations in
-- its database (not embedded in the PDF) so they sync as small deltas and can be
-- tagged / filtered / listed as first-class items:
--   https://www.zotero.org/support/kb/annotations_in_database
--
-- One row per annotation (NOT a JSON blob on reference_items): a highlight-heavy
-- PDF has hundreds, and a single blob would rewrite wholesale on every edit and
-- conflict across the director's devices. Per-row lets a PATCH touch one without
-- its siblings, and lets us filter by page / color / tag.
--
-- The geometry lives in position_json, kept OPAQUE to the hub and stored verbatim
-- (like enrichment_json on the parent). Its shape follows Zotero's convention so
-- imported Zotero highlights map 1:1 and we can export the same way:
--   rect-based (highlight|underline|note|text|image):
--     {"pageIndex":6,"rects":[[x1,y1,x2,y2], ...]}
--   ink (freehand draw):
--     {"pageIndex":2,"paths":[[x1,y1,x2,y2, ...], ...],"width":2}
-- Coordinates are PDF user-space points, page-relative, origin bottom-left,
-- UNSCALED — so a renderer multiplies by the current zoom and the overlay lands
-- correctly at any scale.

CREATE TABLE reference_annotations (
    id            TEXT PRIMARY KEY,
    reference_id  TEXT NOT NULL REFERENCES reference_items(id) ON DELETE CASCADE,
    team_id       TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    type          TEXT NOT NULL DEFAULT 'highlight',  -- highlight|underline|note|text|image|ink
    color         TEXT,                                -- hex, e.g. #ffd400
    page_index    INTEGER NOT NULL DEFAULT 0,          -- 0-based; mirrors position.pageIndex for filtering
    sort_index    TEXT,                                -- Zotero-style reading-order key (page+y+x)
    comment       TEXT,                                -- the annotation's note/comment
    text          TEXT,                                -- selected text (highlight/underline)
    author        TEXT,                                -- who made it: director handle or agent kind
    position_json TEXT NOT NULL DEFAULT '{}',          -- opaque geometry, Zotero-shaped (see above)
    tags_json     TEXT NOT NULL DEFAULT '[]',          -- JSON array of tag strings
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE INDEX idx_reference_annotations_ref ON reference_annotations(reference_id, page_index, sort_index);
CREATE INDEX idx_reference_annotations_team ON reference_annotations(team_id);

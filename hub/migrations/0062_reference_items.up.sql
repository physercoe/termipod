-- The reference library as a hub-owned entity (ADR-053). Metadata only — the
-- data-ownership law (blueprint §4): the hub holds names + fields; PDF bytes
-- stay on the device / go through the blob store, never here. This graduates the
-- desktop's device-local library (desktop/src/state/library.ts) to a hub entity
-- so AGENTS can read/create/update/delete references via REST + MCP, and it
-- syncs across the director's devices.
--
-- Table is `reference_items`, not `references` — REFERENCES is a SQL keyword.
-- The REST path (/references), the entity name (Reference), and the MCP tools
-- (reference_*) all use the "reference" term; only the physical table is suffixed.

CREATE TABLE reference_items (
    id               TEXT PRIMARY KEY,
    team_id          TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    type             TEXT NOT NULL DEFAULT 'article',  -- article|preprint|book|report|webpage|note
    title            TEXT NOT NULL DEFAULT '',
    authors_json     TEXT NOT NULL DEFAULT '[]',       -- JSON array of author name strings
    year             INTEGER,
    venue            TEXT,                              -- journal / conference / publisher
    doi              TEXT,
    arxiv_id         TEXT,
    url              TEXT,
    pdf_url          TEXT,
    abstract         TEXT,
    tldr             TEXT,
    citation_count   INTEGER,
    source           TEXT,                              -- zotero|semantic-scholar|manual|…
    external_id      TEXT,                              -- e.g. zotero:<key> / S2 paperId — dedupe key
    tags_json        TEXT NOT NULL DEFAULT '[]',        -- JSON array of tag strings
    collections_json TEXT NOT NULL DEFAULT '[]',        -- JSON array of collection-name strings
    notes            TEXT NOT NULL DEFAULT '',
    body_markdown    TEXT,
    details_json     TEXT,                              -- JSON object: long-tail source fields
    zotero_storage_json TEXT,                           -- JSON {key,file,content_type} attachment coords
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX idx_reference_items_team ON reference_items(team_id);
CREATE INDEX idx_reference_items_external ON reference_items(team_id, external_id);

-- Decision records (docs/plans/desktop-compare-wall-and-decisions.md §4.1,
-- lane B). The J6 Record surface has been an ADR-shaped form appending to a
-- device-local JSON draft; its own header states the target this table
-- implements: "records eventually link to the runs that justify them".
--
-- The join no competitor has (landscape §6): a decision or finding with LIVE
-- links to the runs, episodes, datasets, references and documents that
-- motivated it. Evidence links are TYPED IDS, not URLs — the same shape as a
-- UIRef entity field — so a link renders as a jump-chip in the app and an
-- agent dereferences it with the tools it already has.
--
-- Metadata only, as everywhere (blueprint §4): `body_md` is a short rationale,
-- not a document. Authoring lives in J2; this is a log with edges. The
-- non-goals in the plan are the fence against it becoming a wiki.
--
-- STATUS IS HISTORY. A record is proposed → accepted, and an accepted record
-- is never edited away: it is SUPERSEDED by a successor that points back at
-- it. That is why `supersedes_id` is an edge rather than a version column, and
-- why deletion is refused for anything past 'proposed' (the handler enforces
-- it; a mistaken proposal can still be dismissed).
--
-- Provenance is derived from the AUTHENTICATED CALLER, never from the body
-- (the F-08 lesson: attribution comes from the token). An agent writes
-- `created_by_kind = 'agent'` because its token says so, not because it said
-- so.

CREATE TABLE records (
    id                TEXT PRIMARY KEY,
    project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL DEFAULT 'decision',  -- decision|finding
    title             TEXT NOT NULL DEFAULT '',
    body_md           TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'proposed',  -- proposed|accepted|superseded
    -- The record this one replaces. ON DELETE SET NULL rather than CASCADE: a
    -- successor outlives its predecessor's row, and losing the edge is better
    -- than losing the decision.
    supersedes_id     TEXT REFERENCES records(id) ON DELETE SET NULL,
    created_by_kind   TEXT NOT NULL DEFAULT 'user',      -- user|agent
    created_by_id     TEXT,                              -- agent id when the caller was an agent
    origin_session_id TEXT,                              -- the agent's session, for the audit trail
    links_json        TEXT NOT NULL DEFAULT '[]',        -- [{kind,id,note}] — typed ids, never URLs
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

-- The list query is always "this project, newest first".
CREATE INDEX idx_records_project ON records(project_id, created_at DESC);
-- Walking a supersede chain forward ("what replaced this?").
CREATE INDEX idx_records_supersedes ON records(supersedes_id);

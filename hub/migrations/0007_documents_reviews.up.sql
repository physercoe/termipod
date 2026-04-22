-- P0.5: Documents + Reviews primitives (blueprint §6.7, §6.8).
--
-- Documents are versioned writeups (memo|draft|report|review) per project.
-- Small text goes in `content_inline` (~256KB upper bound, enforced at the
-- application layer); larger bodies are stored as artifacts and referenced
-- by `artifact_id` (a URI string). The `artifacts` table is a future PR, so
-- `artifact_id` is a loose TEXT column with no FK enforcement for now.
--
-- Reviews form a human-review queue attached to a document or artifact,
-- with states pending → approved | request_changes | rejected.

CREATE TABLE documents (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  kind TEXT NOT NULL,                 -- 'memo' | 'draft' | 'report' | 'review'
  title TEXT NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  prev_version_id TEXT,               -- chain of versions
  content_inline TEXT,                -- <~256KB; NULL if stored as artifact
  artifact_id TEXT,                   -- URI to large content; NULL if inline
  author_agent_id TEXT,
  created_at TEXT NOT NULL,
  CHECK (content_inline IS NOT NULL OR artifact_id IS NOT NULL)
);

CREATE INDEX idx_documents_project ON documents(project_id);
CREATE INDEX idx_documents_kind    ON documents(kind);
CREATE INDEX idx_documents_prev    ON documents(prev_version_id) WHERE prev_version_id IS NOT NULL;

CREATE TABLE reviews (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,          -- 'document' | 'artifact'
  target_id TEXT NOT NULL,
  requester_agent_id TEXT,
  state TEXT NOT NULL DEFAULT 'pending',  -- 'pending' | 'approved' | 'request_changes' | 'rejected'
  decided_by_user_id TEXT,
  decided_at TEXT,
  comment TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX idx_reviews_project ON reviews(project_id);
CREATE INDEX idx_reviews_state   ON reviews(state);
CREATE INDEX idx_reviews_target  ON reviews(target_kind, target_id);

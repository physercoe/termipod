package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// maxInlineDocBytes is the application-layer guard for content_inline.
// Blueprint §4 data-ownership law: small text may be inline in the hub;
// larger content must go through an artifact URI.
const maxInlineDocBytes = 256 * 1024

type documentIn struct {
	ProjectID     string `json:"project_id"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	PrevVersionID string `json:"prev_version_id,omitempty"`
	ContentInline string `json:"content_inline,omitempty"`
	ArtifactID    string `json:"artifact_id,omitempty"`
	AuthorAgentID string `json:"author_agent_id,omitempty"`
}

type documentOut struct {
	ID            string  `json:"id"`
	ProjectID     string  `json:"project_id"`
	Kind          string  `json:"kind"`
	Title         string  `json:"title"`
	Version       int     `json:"version"`
	PrevVersionID string  `json:"prev_version_id,omitempty"`
	ContentInline *string `json:"content_inline,omitempty"`
	ArtifactID    string  `json:"artifact_id,omitempty"`
	AuthorAgentID string  `json:"author_agent_id,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

func isValidDocKind(k string) bool {
	switch k {
	case "memo", "draft", "report", "review":
		return true
	}
	return false
}

func (s *Server) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in documentIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ProjectID == "" || in.Kind == "" || in.Title == "" {
		writeErr(w, http.StatusBadRequest, "project_id, kind, title required")
		return
	}
	if !isValidDocKind(in.Kind) {
		writeErr(w, http.StatusBadRequest, "kind must be one of: memo, draft, report, review")
		return
	}
	// Exactly one of content_inline or artifact_id.
	hasInline := in.ContentInline != ""
	hasArtifact := in.ArtifactID != ""
	if hasInline == hasArtifact {
		writeErr(w, http.StatusBadRequest, "exactly one of content_inline or artifact_id required")
		return
	}
	if hasInline && len(in.ContentInline) > maxInlineDocBytes {
		writeErr(w, http.StatusBadRequest, "content_inline exceeds 256KB; use artifact_id instead")
		return
	}

	version := 1
	var prevID sql.NullString
	if in.PrevVersionID != "" {
		var prevVersion int
		var prevProject string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT version, project_id FROM documents WHERE id = ?`,
			in.PrevVersionID).Scan(&prevVersion, &prevProject)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "prev_version_id not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if prevProject != in.ProjectID {
			writeErr(w, http.StatusBadRequest, "prev_version_id belongs to a different project")
			return
		}
		version = prevVersion + 1
		prevID = sql.NullString{String: in.PrevVersionID, Valid: true}
	}

	id := NewID()
	now := NowUTC()
	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO documents (
			id, project_id, kind, title, version, prev_version_id,
			content_inline, artifact_id, author_agent_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?)`,
		id, in.ProjectID, in.Kind, in.Title, version, prevID,
		in.ContentInline, in.ArtifactID, in.AuthorAgentID, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.recordAudit(r.Context(), team, "document.create", "document", id,
		in.Kind+" "+in.Title,
		map[string]any{"project_id": in.ProjectID, "kind": in.Kind, "version": version})

	out := documentOut{
		ID: id, ProjectID: in.ProjectID, Kind: in.Kind, Title: in.Title,
		Version: version, PrevVersionID: in.PrevVersionID,
		ArtifactID: in.ArtifactID, AuthorAgentID: in.AuthorAgentID,
		CreatedAt: now,
	}
	if hasInline {
		inline := in.ContentInline
		out.ContentInline = &inline
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	// Lists documents. Filters by project_id (?project=) optional; kind
	// (?kind=) optional. Returns metadata only (no content_inline) to keep
	// list responses small — clients call GET one for the body.
	project := r.URL.Query().Get("project")
	kind := r.URL.Query().Get("kind")

	q := `SELECT id, project_id, kind, title, version, prev_version_id,
	             artifact_id, author_agent_id, created_at
	      FROM documents WHERE 1=1`
	args := []any{}
	if project != "" {
		q += ` AND project_id = ?`
		args = append(args, project)
	}
	if kind != "" {
		if !isValidDocKind(kind) {
			writeErr(w, http.StatusBadRequest, "invalid kind filter")
			return
		}
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []documentOut{}
	for rows.Next() {
		var d documentOut
		var prev, artifact, author sql.NullString
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Kind, &d.Title, &d.Version,
			&prev, &artifact, &author, &d.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if prev.Valid {
			d.PrevVersionID = prev.String
		}
		if artifact.Valid {
			d.ArtifactID = artifact.String
		}
		if author.Valid {
			d.AuthorAgentID = author.String
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	doc := chi.URLParam(r, "doc")
	var d documentOut
	var prev, artifact, author, inline sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, project_id, kind, title, version, prev_version_id,
		       content_inline, artifact_id, author_agent_id, created_at
		FROM documents WHERE id = ?`, doc).Scan(
		&d.ID, &d.ProjectID, &d.Kind, &d.Title, &d.Version,
		&prev, &inline, &artifact, &author, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prev.Valid {
		d.PrevVersionID = prev.String
	}
	if artifact.Valid {
		d.ArtifactID = artifact.String
	}
	if author.Valid {
		d.AuthorAgentID = author.String
	}
	if inline.Valid {
		s := inline.String
		d.ContentInline = &s
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleListDocumentVersions(w http.ResponseWriter, r *http.Request) {
	// Walk the prev_version_id chain starting from {doc} back to the root.
	// Returned newest-first (current → v1). Metadata only.
	doc := chi.URLParam(r, "doc")
	out := []documentOut{}
	cursor := doc
	// Bounded walk to defend against accidental cycles. Documents versions
	// aren't expected to exceed a few hundred per chain.
	for i := 0; i < 1000 && cursor != ""; i++ {
		var d documentOut
		var prev, artifact, author sql.NullString
		err := s.db.QueryRowContext(r.Context(), `
			SELECT id, project_id, kind, title, version, prev_version_id,
			       artifact_id, author_agent_id, created_at
			FROM documents WHERE id = ?`, cursor).Scan(
			&d.ID, &d.ProjectID, &d.Kind, &d.Title, &d.Version,
			&prev, &artifact, &author, &d.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			if i == 0 {
				writeErr(w, http.StatusNotFound, "document not found")
				return
			}
			break
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if prev.Valid {
			d.PrevVersionID = prev.String
		}
		if artifact.Valid {
			d.ArtifactID = artifact.String
		}
		if author.Valid {
			d.AuthorAgentID = author.String
		}
		out = append(out, d)
		if !prev.Valid {
			break
		}
		cursor = prev.String
	}
	writeJSON(w, http.StatusOK, out)
}

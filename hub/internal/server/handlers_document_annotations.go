package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ADR-020 W1 — director-action-surface: anchored annotations on a typed
// document section. Five endpoints; DELETE is intentionally rejected with
// 405 because annotations are append-only-on-content (resolve is the
// soft-close, per ADR-020 D3).

const (
	annotationKindComment    = "comment"
	annotationKindRedline    = "redline"
	annotationKindSuggestion = "suggestion"
	annotationKindQuestion   = "question"

	annotationStatusOpen     = "open"
	annotationStatusResolved = "resolved"

	maxAnnotationBodyBytes = 16 * 1024 // 16 KB; redline/suggestion bodies are typed by hand
)

func isValidAnnotationKind(k string) bool {
	switch k {
	case annotationKindComment, annotationKindRedline,
		annotationKindSuggestion, annotationKindQuestion:
		return true
	}
	return false
}

// Annotation is the over-the-wire shape.
type Annotation struct {
	ID                 string `json:"id"`
	DocumentID         string `json:"document_id"`
	SectionSlug        string `json:"section_slug"`
	CharStart          *int   `json:"char_start,omitempty"`
	CharEnd            *int   `json:"char_end,omitempty"`
	Kind               string `json:"kind"`
	Body               string `json:"body"`
	Status             string `json:"status"`
	AuthorKind         string `json:"author_kind"`
	AuthorHandle       string `json:"author_handle,omitempty"`
	ParentAnnotationID string `json:"parent_annotation_id,omitempty"`
	CreatedAt          string `json:"created_at"`
	ResolvedAt         string `json:"resolved_at,omitempty"`
	ResolvedByActor    string `json:"resolved_by_actor,omitempty"`
}

// loadAnnotation reads a single annotation row by id. Returns 404 if
// missing. The document_id is used by callers to gate cross-document
// edits and to look up the project for audit context.
func (s *Server) loadAnnotation(
	r *http.Request, id string,
) (Annotation, int, error) {
	var a Annotation
	var charStart, charEnd sql.NullInt64
	var authorHandle, parentID, resolvedAt, resolvedByActor sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, document_id, section_slug, char_start, char_end,
		       kind, body, status,
		       author_kind, author_handle, parent_annotation_id,
		       created_at, resolved_at, resolved_by_actor
		  FROM document_annotations
		 WHERE id = ?`, id).Scan(
		&a.ID, &a.DocumentID, &a.SectionSlug, &charStart, &charEnd,
		&a.Kind, &a.Body, &a.Status,
		&a.AuthorKind, &authorHandle, &parentID,
		&a.CreatedAt, &resolvedAt, &resolvedByActor,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Annotation{}, http.StatusNotFound, errors.New("annotation not found")
		}
		return Annotation{}, http.StatusInternalServerError, err
	}
	if charStart.Valid {
		v := int(charStart.Int64)
		a.CharStart = &v
	}
	if charEnd.Valid {
		v := int(charEnd.Int64)
		a.CharEnd = &v
	}
	if authorHandle.Valid {
		a.AuthorHandle = authorHandle.String
	}
	if parentID.Valid {
		a.ParentAnnotationID = parentID.String
	}
	if resolvedAt.Valid {
		a.ResolvedAt = resolvedAt.String
	}
	if resolvedByActor.Valid {
		a.ResolvedByActor = resolvedByActor.String
	}
	return a, http.StatusOK, nil
}

// projectIDForDocument fetches the project_id for a document; used to
// scope audit rows and to confirm the annotation lives under a real doc.
func (s *Server) projectIDForDocument(
	r *http.Request, docID string,
) (string, int, error) {
	var projectID string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT project_id FROM documents WHERE id = ?`, docID).Scan(&projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusNotFound, errors.New("document not found")
		}
		return "", http.StatusInternalServerError, err
	}
	return projectID, http.StatusOK, nil
}

// createAnnotationIn — body for POST /documents/{doc}/annotations.
type createAnnotationIn struct {
	SectionSlug        string `json:"section_slug"`
	CharStart          *int   `json:"char_start,omitempty"`
	CharEnd            *int   `json:"char_end,omitempty"`
	Kind               string `json:"kind,omitempty"`
	Body               string `json:"body"`
	ParentAnnotationID string `json:"parent_annotation_id,omitempty"`
}

// handleCreateAnnotation — POST /v1/teams/{team}/documents/{doc}/annotations.
func (s *Server) handleCreateAnnotation(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	docID := chi.URLParam(r, "doc")
	var in createAnnotationIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(in.SectionSlug) == "" {
		writeErr(w, http.StatusBadRequest, "section_slug required")
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		writeErr(w, http.StatusBadRequest, "body required")
		return
	}
	if len(in.Body) > maxAnnotationBodyBytes {
		writeErr(w, http.StatusBadRequest,
			fmt.Sprintf("body exceeds %d bytes", maxAnnotationBodyBytes))
		return
	}
	kind := in.Kind
	if kind == "" {
		kind = annotationKindComment
	}
	if !isValidAnnotationKind(kind) {
		writeErr(w, http.StatusBadRequest,
			"kind must be one of: comment, redline, suggestion, question")
		return
	}
	if (in.CharStart == nil) != (in.CharEnd == nil) {
		writeErr(w, http.StatusBadRequest,
			"char_start and char_end must be provided together or both omitted")
		return
	}
	if in.CharStart != nil && *in.CharStart < 0 {
		writeErr(w, http.StatusBadRequest, "char_start must be >= 0")
		return
	}
	if in.CharStart != nil && in.CharEnd != nil && *in.CharEnd < *in.CharStart {
		writeErr(w, http.StatusBadRequest, "char_end must be >= char_start")
		return
	}
	projectID, status, err := s.projectIDForDocument(r, docID)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	// Confirm the section exists in the typed body (document must be a
	// typed/structured doc; plain markdown isn't supported per ADR-020 D6's
	// "annotations target sections" constraint).
	_, body, _, _, st, derr := s.loadStructuredDocument(r.Context(), docID)
	if derr != nil {
		writeErr(w, st, derr.Error())
		return
	}
	if findSection(body, in.SectionSlug) < 0 {
		writeErr(w, http.StatusUnprocessableEntity,
			"section not found in document body")
		return
	}

	_, actorKind, actorHandle := actorFromContext(r.Context())
	id := NewID()
	now := NowUTC()
	var charStartArg, charEndArg, parentArg, handleArg any
	if in.CharStart != nil {
		charStartArg = *in.CharStart
	}
	if in.CharEnd != nil {
		charEndArg = *in.CharEnd
	}
	if in.ParentAnnotationID != "" {
		parentArg = in.ParentAnnotationID
	}
	if actorHandle != "" {
		handleArg = actorHandle
	}
	_, ierr := s.db.ExecContext(r.Context(), `
		INSERT INTO document_annotations (
			id, document_id, section_slug, char_start, char_end,
			kind, body, status, author_kind, author_handle,
			parent_annotation_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?, ?)`,
		id, docID, in.SectionSlug, charStartArg, charEndArg,
		kind, in.Body, actorKind, handleArg, parentArg, now,
	)
	if ierr != nil {
		writeErr(w, http.StatusInternalServerError, ierr.Error())
		return
	}
	s.recordAudit(r.Context(), team, "annotation.created",
		"document", docID,
		fmt.Sprintf("%s on %s · %s", kind, in.SectionSlug, truncateForAudit(in.Body, 80)),
		map[string]any{
			"project_id":    projectID,
			"document_id":   docID,
			"annotation_id": id,
			"section":       in.SectionSlug,
			"kind":          kind,
		})
	a, _, err := s.loadAnnotation(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// handleListAnnotations — GET /v1/teams/{team}/documents/{doc}/annotations.
// Query params: section (optional), status (open|resolved|all; default open).
func (s *Server) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	docID := chi.URLParam(r, "doc")
	if _, status, err := s.projectIDForDocument(r, docID); err != nil {
		writeErr(w, status, err.Error())
		return
	}
	q := r.URL.Query()
	sectionSlug := strings.TrimSpace(q.Get("section"))
	statusFilter := strings.TrimSpace(q.Get("status"))
	if statusFilter == "" {
		statusFilter = annotationStatusOpen
	}
	if statusFilter != annotationStatusOpen &&
		statusFilter != annotationStatusResolved &&
		statusFilter != "all" {
		writeErr(w, http.StatusBadRequest,
			"status must be one of: open, resolved, all")
		return
	}

	args := []any{docID}
	q2 := `SELECT id, document_id, section_slug, char_start, char_end,
	              kind, body, status,
	              author_kind, author_handle, parent_annotation_id,
	              created_at, resolved_at, resolved_by_actor
	         FROM document_annotations
	        WHERE document_id = ?`
	if sectionSlug != "" {
		q2 += ` AND section_slug = ?`
		args = append(args, sectionSlug)
	}
	if statusFilter != "all" {
		q2 += ` AND status = ?`
		args = append(args, statusFilter)
	}
	q2 += ` ORDER BY created_at ASC, id ASC`

	rows, err := s.db.QueryContext(r.Context(), q2, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		var a Annotation
		var charStart, charEnd sql.NullInt64
		var authorHandle, parentID, resolvedAt, resolvedByActor sql.NullString
		if err := rows.Scan(
			&a.ID, &a.DocumentID, &a.SectionSlug, &charStart, &charEnd,
			&a.Kind, &a.Body, &a.Status,
			&a.AuthorKind, &authorHandle, &parentID,
			&a.CreatedAt, &resolvedAt, &resolvedByActor,
		); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if charStart.Valid {
			v := int(charStart.Int64)
			a.CharStart = &v
		}
		if charEnd.Valid {
			v := int(charEnd.Int64)
			a.CharEnd = &v
		}
		if authorHandle.Valid {
			a.AuthorHandle = authorHandle.String
		}
		if parentID.Valid {
			a.ParentAnnotationID = parentID.String
		}
		if resolvedAt.Valid {
			a.ResolvedAt = resolvedAt.String
		}
		if resolvedByActor.Valid {
			a.ResolvedByActor = resolvedByActor.String
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"annotations": out})
}

// patchAnnotationIn — body for PATCH /annotations/{id}. Only body and
// kind are mutable; the anchor and author are fixed.
type patchAnnotationIn struct {
	Body string `json:"body,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// handlePatchAnnotation — PATCH /v1/teams/{team}/annotations/{id}.
// Only the original author may edit body or kind.
func (s *Server) handlePatchAnnotation(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "annotation")
	var in patchAnnotationIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cur, status, err := s.loadAnnotation(r, id)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	_, actorKind, actorHandle := actorFromContext(r.Context())
	if cur.AuthorKind != actorKind || cur.AuthorHandle != actorHandle {
		writeErr(w, http.StatusForbidden, "only the author may edit this annotation")
		return
	}

	body := cur.Body
	kind := cur.Kind
	if in.Body != "" {
		if len(in.Body) > maxAnnotationBodyBytes {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("body exceeds %d bytes", maxAnnotationBodyBytes))
			return
		}
		body = in.Body
	}
	if in.Kind != "" {
		if !isValidAnnotationKind(in.Kind) {
			writeErr(w, http.StatusBadRequest,
				"kind must be one of: comment, redline, suggestion, question")
			return
		}
		kind = in.Kind
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE document_annotations SET body = ?, kind = ? WHERE id = ?`,
		body, kind, id,
	); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	projectID, _, _ := s.projectIDForDocument(r, cur.DocumentID)
	s.recordAudit(r.Context(), team, "annotation.edited",
		"document", cur.DocumentID,
		fmt.Sprintf("%s on %s", kind, cur.SectionSlug),
		map[string]any{
			"project_id":    projectID,
			"document_id":   cur.DocumentID,
			"annotation_id": id,
			"section":       cur.SectionSlug,
			"kind":          kind,
		})
	updated, _, err := s.loadAnnotation(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleResolveAnnotation — POST /v1/teams/{team}/annotations/{id}/resolve.
// Anyone with access to the team's annotations can resolve.
func (s *Server) handleResolveAnnotation(w http.ResponseWriter, r *http.Request) {
	s.transitionAnnotation(w, r, annotationStatusResolved, "annotation.resolved")
}

// handleReopenAnnotation — POST /v1/teams/{team}/annotations/{id}/reopen.
func (s *Server) handleReopenAnnotation(w http.ResponseWriter, r *http.Request) {
	s.transitionAnnotation(w, r, annotationStatusOpen, "annotation.reopened")
}

func (s *Server) transitionAnnotation(
	w http.ResponseWriter, r *http.Request, target, auditAction string,
) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "annotation")
	cur, status, err := s.loadAnnotation(r, id)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	if cur.Status == target {
		writeJSON(w, http.StatusOK, cur)
		return
	}
	_, actorKind, actorHandle := actorFromContext(r.Context())
	actor := actorKind
	if actorHandle != "" {
		actor = actorKind + ":" + actorHandle
	}
	var resolvedAt, resolvedBy any
	if target == annotationStatusResolved {
		resolvedAt = NowUTC()
		resolvedBy = actor
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE document_annotations
		    SET status = ?, resolved_at = ?, resolved_by_actor = ?
		  WHERE id = ?`,
		target, resolvedAt, resolvedBy, id,
	); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	projectID, _, _ := s.projectIDForDocument(r, cur.DocumentID)
	s.recordAudit(r.Context(), team, auditAction,
		"document", cur.DocumentID,
		fmt.Sprintf("%s on %s", cur.Kind, cur.SectionSlug),
		map[string]any{
			"project_id":    projectID,
			"document_id":   cur.DocumentID,
			"annotation_id": id,
			"section":       cur.SectionSlug,
		})
	updated, _, err := s.loadAnnotation(r, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteAnnotationDisallowed — DELETE rejected per ADR-020 D3.
// Annotations are part of the audit trail; resolve is the soft-close.
func (s *Server) handleDeleteAnnotationDisallowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, PATCH, POST")
	writeErr(w, http.StatusMethodNotAllowed,
		"annotations are append-only; use POST /resolve instead of DELETE (ADR-020 D3)")
}

func truncateForAudit(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}


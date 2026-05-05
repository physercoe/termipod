package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// W5a — Structured Document Viewer (A4). Typed documents (schema_id !=
// null) carry their body as JSON in `documents.content_inline`:
//
//   { "schema_version": 1,
//     "schema_id": "research-proposal-v1",
//     "sections": [
//       {"slug":"motivation","title":"…","body":"…","status":"ratified",
//        "last_authored_at": "...", "last_authored_by_session_id": "...",
//        "ratified_at": "...", "ratified_by_actor": "..."},
//       …
//     ]
//   }
//
// Per project-phase-schema.md §4.1 the section state enum is 3 values:
// empty | draft | ratified. Editing a ratified section silently moves
// it back to draft (server-side; mobile already prompts the director).

const (
	sectionStateEmpty    = "empty"
	sectionStateDraft    = "draft"
	sectionStateRatified = "ratified"
)

func isValidSectionState(s string) bool {
	switch s {
	case sectionStateEmpty, sectionStateDraft, sectionStateRatified:
		return true
	}
	return false
}

// structuredBody is the over-the-wire shape mirroring
// docs/reference/project-phase-schema.md §4.1.
type structuredBody struct {
	SchemaVersion int                `json:"schema_version"`
	SchemaID      string             `json:"schema_id"`
	Sections      []structuredSection `json:"sections"`
}

type structuredSection struct {
	Slug                    string `json:"slug"`
	Title                   string `json:"title,omitempty"`
	Body                    string `json:"body"`
	Status                  string `json:"status"`
	LastAuthoredAt          string `json:"last_authored_at,omitempty"`
	LastAuthoredBySessionID string `json:"last_authored_by_session_id,omitempty"`
	RatifiedAt              string `json:"ratified_at,omitempty"`
	RatifiedByActor         string `json:"ratified_by_actor,omitempty"`
}

// loadStructuredDocument reads a document row, validates it carries a
// schema_id, and parses content_inline as the structuredBody shape.
// Returns 404 / 409 / 500 errors via httpStatus alongside an explanatory
// message; callers wrap into HTTP responses.
func (s *Server) loadStructuredDocument(
	ctx context.Context, docID string,
) (projectID string, body structuredBody, content string, schemaID string, status int, err error) {
	var schemaNS, inline sql.NullString
	row := s.db.QueryRowContext(ctx, `
		SELECT project_id, schema_id, content_inline
		  FROM documents
		 WHERE id = ?`, docID)
	if err = row.Scan(&projectID, &schemaNS, &inline); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", structuredBody{}, "", "", http.StatusNotFound,
				errors.New("document not found")
		}
		return "", structuredBody{}, "", "", http.StatusInternalServerError, err
	}
	if !schemaNS.Valid || schemaNS.String == "" {
		return projectID, structuredBody{}, "", "", http.StatusConflict,
			errors.New("document is plain markdown; section endpoints require a typed (schema_id) document")
	}
	schemaID = schemaNS.String
	if !inline.Valid || inline.String == "" {
		return projectID, structuredBody{
			SchemaVersion: 1, SchemaID: schemaID, Sections: []structuredSection{},
		}, "", schemaID, http.StatusOK, nil
	}
	content = inline.String
	if uerr := json.Unmarshal([]byte(content), &body); uerr != nil {
		return projectID, structuredBody{}, content, schemaID,
			http.StatusUnprocessableEntity,
			fmt.Errorf("document body is not valid structured JSON: %v", uerr)
	}
	if body.SchemaID == "" {
		body.SchemaID = schemaID
	}
	return projectID, body, content, schemaID, http.StatusOK, nil
}

// findSection returns the index in body.Sections matching slug, or -1.
func findSection(body structuredBody, slug string) int {
	for i := range body.Sections {
		if body.Sections[i].Slug == slug {
			return i
		}
	}
	return -1
}

// writeStructuredDocument persists the modified body back into
// content_inline. Cheap path for MVP — typed docs are bounded in size by
// the same 256KB inline ceiling as plain ones.
func (s *Server) writeStructuredDocument(
	ctx context.Context, docID string, body structuredBody,
) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if len(encoded) > maxInlineDocBytes {
		return fmt.Errorf("structured body exceeds %d bytes", maxInlineDocBytes)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE documents SET content_inline = ? WHERE id = ?`,
		string(encoded), docID)
	return err
}

// patchSectionIn carries the manual-edit payload for PATCH section.
type patchSectionIn struct {
	Body                     string `json:"body"`
	ExpectedLastAuthoredAt   string `json:"expected_last_authored_at,omitempty"`
	LastAuthoredBySessionID  string `json:"last_authored_by_session_id,omitempty"`
}

// handlePatchDocumentSection — PATCH /documents/{doc}/sections/{slug}.
// Optimistic concurrency via expected_last_authored_at: if the cached
// value disagrees with the row, returns 412. Editing a ratified section
// silently downgrades to draft (UI surfaces a confirmation; the server
// trusts the request).
func (s *Server) handlePatchDocumentSection(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	docID := chi.URLParam(r, "doc")
	slug := chi.URLParam(r, "slug")
	var in patchSectionIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	projectID, body, _, schemaID, status, err := s.loadStructuredDocument(r.Context(), docID)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	idx := findSection(body, slug)
	if idx < 0 {
		writeErr(w, http.StatusNotFound, "section not found in document body")
		return
	}
	sec := body.Sections[idx]
	if in.ExpectedLastAuthoredAt != "" &&
		in.ExpectedLastAuthoredAt != sec.LastAuthoredAt {
		// Per A4 §5.6 — return 412 with hints so the mobile conflict UI can
		// show "edited elsewhere" + offer overwrite/discard/merge.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "section was modified elsewhere",
			"server_section":    sec,
			"expected":          in.ExpectedLastAuthoredAt,
			"actual":            sec.LastAuthoredAt,
		})
		return
	}
	now := NowUTC()
	sec.Body = in.Body
	if sec.Status == sectionStateRatified {
		sec.Status = sectionStateDraft
		sec.RatifiedAt = ""
		sec.RatifiedByActor = ""
	} else if sec.Status == sectionStateEmpty {
		sec.Status = sectionStateDraft
	}
	sec.LastAuthoredAt = now
	sec.LastAuthoredBySessionID = in.LastAuthoredBySessionID
	body.Sections[idx] = sec
	if body.SchemaID == "" {
		body.SchemaID = schemaID
	}
	if body.SchemaVersion == 0 {
		body.SchemaVersion = 1
	}
	if err := s.writeStructuredDocument(r.Context(), docID, body); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "document.section_authored",
		"document", docID,
		fmt.Sprintf("authored %s · %s", sec.Title, slug),
		map[string]any{
			"project_id": projectID,
			"document_id": docID,
			"section":    slug,
			"status":     sec.Status,
		})
	writeJSON(w, http.StatusOK, sec)
}

// statusIn carries the section-state transition payload.
type sectionStatusIn struct {
	Status string `json:"status"`
}

// handleSetDocumentSectionStatus — POST /documents/{doc}/sections/{slug}/status.
// Director-only ratification gesture in mobile; the hub enforces only
// the enum and stamps actor / timestamp. UI gating by role lives in the
// mobile layer (§5.4 of the viewer spec).
func (s *Server) handleSetDocumentSectionStatus(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	docID := chi.URLParam(r, "doc")
	slug := chi.URLParam(r, "slug")
	var in sectionStatusIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !isValidSectionState(in.Status) {
		writeErr(w, http.StatusBadRequest,
			"status must be one of: empty, draft, ratified")
		return
	}
	projectID, body, _, _, status, err := s.loadStructuredDocument(r.Context(), docID)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	idx := findSection(body, slug)
	if idx < 0 {
		writeErr(w, http.StatusNotFound, "section not found in document body")
		return
	}
	sec := body.Sections[idx]
	if in.Status == sectionStateRatified && sec.Status == sectionStateEmpty {
		writeErr(w, http.StatusConflict,
			"cannot ratify an empty section; author content first")
		return
	}
	now := NowUTC()
	sec.Status = in.Status
	if in.Status == sectionStateRatified {
		sec.RatifiedAt = now
		_, actorKind, actorHandle := actorFromContext(r.Context())
		actor := actorKind
		if actorHandle != "" {
			actor = actorKind + ":" + actorHandle
		}
		sec.RatifiedByActor = actor
	} else {
		sec.RatifiedAt = ""
		sec.RatifiedByActor = ""
	}
	body.Sections[idx] = sec
	if err := s.writeStructuredDocument(r.Context(), docID, body); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	auditAction := "document.section_authored"
	if in.Status == sectionStateRatified {
		auditAction = "document.section_ratified"
	}
	s.recordAudit(r.Context(), team, auditAction, "document", docID,
		fmt.Sprintf("%s · %s · %s", in.Status, sec.Title, slug),
		map[string]any{
			"project_id": projectID,
			"document_id": docID,
			"section":    slug,
			"status":     in.Status,
		})
	writeJSON(w, http.StatusOK, sec)
}

// nowUTCRFC3339 lets tests stub timing if they want; production calls
// NowUTC() through the Server (already RFC3339 nanos).
var _ = time.Now

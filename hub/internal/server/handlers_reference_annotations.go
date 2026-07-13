package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handlers_reference_annotations.go — PDF annotations as child records of a
// reference (ADR-053 companion, migration 0064). Metadata only: an annotation
// points INTO a PDF, it never mutates the bytes (blueprint §4). Same store-methods
// -shared-by-REST-and-MCP shape as handlers_references.go; MCP surface is in
// mcp_reference_annotations.go. Geometry (position) is opaque, Zotero-shaped JSON.

// annotationTypes is the closed set of annotation kinds, mirroring Zotero's
// reader (highlight, underline, sticky note, added text, screenshot/area, ink).
var annotationTypes = map[string]bool{
	"highlight": true, "underline": true, "note": true,
	"text": true, "image": true, "ink": true,
}

func normalizeAnnotationType(t string) string {
	if annotationTypes[t] {
		return t
	}
	return "highlight"
}

// annotationBody is the mutable projection a create/update sets.
type annotationBody struct {
	Type      string `json:"type"`
	Color     string `json:"color,omitempty"`
	PageIndex int    `json:"page_index"`
	SortIndex string `json:"sort_index,omitempty"`
	Comment   string `json:"comment,omitempty"`
	Text      string `json:"text,omitempty"`
	Author    string `json:"author,omitempty"`
	// Position is the geometry, kept verbatim and opaque to the hub. Zotero-shaped:
	// {"pageIndex":N,"rects":[[x1,y1,x2,y2],…]} or {"pageIndex":N,"paths":[[…]],"width":W}
	// in unscaled PDF points, origin bottom-left. See migration 0064.
	Position json.RawMessage `json:"position,omitempty"`
	Tags     []string        `json:"tags"`
}

type annotationOut struct {
	ID          string `json:"id"`
	ReferenceID string `json:"reference_id"`
	TeamID      string `json:"team_id"`
	annotationBody
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const annotationCols = `id, reference_id, team_id, type, color, page_index, sort_index,
	comment, text, author, position_json, tags_json, created_at, updated_at`

func scanAnnotation(row interface{ Scan(...any) error }) (annotationOut, error) {
	var a annotationOut
	var color, sortIdx, comment, text, author sql.NullString
	var position, tags string
	err := row.Scan(&a.ID, &a.ReferenceID, &a.TeamID, &a.Type, &color, &a.PageIndex, &sortIdx,
		&comment, &text, &author, &position, &tags, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return a, err
	}
	a.Color, a.SortIndex, a.Comment, a.Text, a.Author = color.String, sortIdx.String, comment.String, text.String, author.String
	if position != "" {
		a.Position = json.RawMessage(position)
	}
	a.Tags = parseStrArray(tags)
	return a, nil
}

func positionJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// referenceExists reports whether a reference with this id belongs to the team —
// the guard that scopes annotations by team (the FK alone doesn't check team).
func (s *Server) referenceExists(ctx context.Context, team, refID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM reference_items WHERE team_id = ? AND id = ?`, team, refID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) createAnnotation(ctx context.Context, team, refID string, b annotationBody) (annotationOut, error) {
	ok, err := s.referenceExists(ctx, team, refID)
	if err != nil {
		return annotationOut{}, err
	}
	if !ok {
		return annotationOut{}, sql.ErrNoRows
	}
	id := NewID()
	now := NowUTC()
	_, err = s.writeDB.ExecContext(ctx, `
		INSERT INTO reference_annotations (`+annotationCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, refID, team, normalizeAnnotationType(b.Type), refNullStr(b.Color), b.PageIndex,
		refNullStr(b.SortIndex), refNullStr(b.Comment), refNullStr(b.Text), refNullStr(b.Author),
		positionJSON(b.Position), jsonStrArray(b.Tags), now, now)
	if err != nil {
		return annotationOut{}, err
	}
	return s.getAnnotationByID(ctx, team, refID, id)
}

func (s *Server) getAnnotationByID(ctx context.Context, team, refID, id string) (annotationOut, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+annotationCols+` FROM reference_annotations
		 WHERE team_id = ? AND reference_id = ? AND id = ?`, team, refID, id)
	return scanAnnotation(row)
}

func (s *Server) listAnnotations(ctx context.Context, team, refID string) ([]annotationOut, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+annotationCols+` FROM reference_annotations
		 WHERE team_id = ? AND reference_id = ?
		 ORDER BY page_index ASC, sort_index ASC, created_at ASC`, team, refID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []annotationOut{}
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// patchAnnotation applies a partial JSON patch onto the existing row: keys
// present override, absent keys keep their stored value (struct-decode onto the
// loaded body). page_index is kept in step with position.pageIndex is the
// caller's job — the hub stores what it is given.
func (s *Server) patchAnnotation(ctx context.Context, team, refID, id string, patch json.RawMessage) (annotationOut, error) {
	cur, err := s.getAnnotationByID(ctx, team, refID, id)
	if err != nil {
		return annotationOut{}, err
	}
	if err := json.Unmarshal(patch, &cur.annotationBody); err != nil {
		return annotationOut{}, err
	}
	b := cur.annotationBody
	_, err = s.writeDB.ExecContext(ctx, `
		UPDATE reference_annotations SET
			type = ?, color = ?, page_index = ?, sort_index = ?, comment = ?, text = ?,
			author = ?, position_json = ?, tags_json = ?, updated_at = ?
		WHERE team_id = ? AND reference_id = ? AND id = ?`,
		normalizeAnnotationType(b.Type), refNullStr(b.Color), b.PageIndex, refNullStr(b.SortIndex),
		refNullStr(b.Comment), refNullStr(b.Text), refNullStr(b.Author), positionJSON(b.Position),
		jsonStrArray(b.Tags), NowUTC(), team, refID, id)
	if err != nil {
		return annotationOut{}, err
	}
	return s.getAnnotationByID(ctx, team, refID, id)
}

func (s *Server) deleteAnnotation(ctx context.Context, team, refID, id string) (bool, error) {
	res, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM reference_annotations WHERE team_id = ? AND reference_id = ? AND id = ?`, team, refID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---- REST handlers ---------------------------------------------------------

func (s *Server) handleListReferenceAnnotations(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	refID := chi.URLParam(r, "ref")
	ok, err := s.referenceExists(r.Context(), team, refID)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "reference not found")
		return
	}
	out, err := s.listAnnotations(r.Context(), team, refID)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateReferenceAnnotation(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	refID := chi.URLParam(r, "ref")
	var b annotationBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	out, err := s.createAnnotation(r.Context(), team, refID, b)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "reference not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetReferenceAnnotation(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	refID := chi.URLParam(r, "ref")
	id := chi.URLParam(r, "ann")
	out, err := s.getAnnotationByID(r.Context(), team, refID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "annotation not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateReferenceAnnotation(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	refID := chi.URLParam(r, "ref")
	id := chi.URLParam(r, "ann")
	patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInlineDocBytes))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, err := s.patchAnnotation(r.Context(), team, refID, id, patch)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "annotation not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteReferenceAnnotation(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	refID := chi.URLParam(r, "ref")
	id := chi.URLParam(r, "ann")
	ok, err := s.deleteAnnotation(r.Context(), team, refID, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "annotation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

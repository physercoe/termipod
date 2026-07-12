package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handlers_references.go — the hub-owned reference library (ADR-053). Metadata
// only (blueprint §4 data-ownership law): the hub holds reference fields; PDF
// bytes stay on the device / go through the blob store. Exposed over REST here
// and over MCP in mcp_references.go, both on the shared store methods below, so
// agents and the desktop see one library. Table: reference_items.

// refTypes is the closed set of coarse reference types the library models,
// mirroring the desktop RefType union (state/library.ts).
var refTypes = map[string]bool{
	"article": true, "preprint": true, "book": true,
	"report": true, "webpage": true, "note": true,
}

func normalizeRefType(t string) string {
	if refTypes[t] {
		return t
	}
	return "article"
}

type zoteroStorageRef struct {
	Key         string `json:"key"`
	File        string `json:"file"`
	ContentType string `json:"content_type,omitempty"`
}

// referenceBody is the mutable projection — the fields a create/update sets.
// Embedded into referenceOut so the wire shape is flat.
type referenceBody struct {
	Type          string            `json:"type"`
	Title         string            `json:"title"`
	Authors       []string          `json:"authors"`
	Year          *int              `json:"year,omitempty"`
	Venue         string            `json:"venue,omitempty"`
	DOI           string            `json:"doi,omitempty"`
	ArxivID       string            `json:"arxiv_id,omitempty"`
	URL           string            `json:"url,omitempty"`
	PDFURL        string            `json:"pdf_url,omitempty"`
	Abstract      string            `json:"abstract,omitempty"`
	TLDR          string            `json:"tldr,omitempty"`
	CitationCount *int              `json:"citation_count,omitempty"`
	Source        string            `json:"source,omitempty"`
	ExternalID    string            `json:"external_id,omitempty"`
	Tags          []string          `json:"tags"`
	Collections   []string          `json:"collections"`
	Notes         string            `json:"notes"`
	BodyMarkdown  string            `json:"body_markdown,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
	ZoteroStorage *zoteroStorageRef `json:"zotero_storage,omitempty"`
	// Enrichment is derived metadata the desktop scraper attaches (citation graph,
	// journal metrics, code/data links, topics, OA status). Opaque to the hub — it
	// is stored and returned verbatim so it round-trips to agents, but the hub
	// never interprets its shape. Stored in the enrichment_json column.
	Enrichment json.RawMessage `json:"enrichment,omitempty"`
}

type referenceOut struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	referenceBody
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type referenceFilter struct {
	Collection string
	Tag        string
	Source     string
	Q          string
	Limit      int
}

// ---- JSON column helpers ---------------------------------------------------

func jsonStrArray(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func parseStrArray(s string) []string {
	out := []string{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func refNullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

// ---- shared store methods (used by REST + MCP) -----------------------------

const referenceCols = `id, team_id, type, title, authors_json, year, venue, doi, arxiv_id,
	url, pdf_url, abstract, tldr, citation_count, source, external_id, tags_json,
	collections_json, notes, body_markdown, details_json, zotero_storage_json,
	enrichment_json, created_at, updated_at`

func scanReference(row interface{ Scan(...any) error }) (referenceOut, error) {
	var r referenceOut
	var authors, tags, collections string
	var year, citation sql.NullInt64
	var venue, doi, arxiv, url, pdfURL, abstract, tldr, source, extID, bodyMD, details, zotero, enrichment sql.NullString
	err := row.Scan(&r.ID, &r.TeamID, &r.Type, &r.Title, &authors, &year, &venue, &doi, &arxiv,
		&url, &pdfURL, &abstract, &tldr, &citation, &source, &extID, &tags,
		&collections, &r.Notes, &bodyMD, &details, &zotero, &enrichment, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	if enrichment.Valid && enrichment.String != "" {
		r.Enrichment = json.RawMessage(enrichment.String)
	}
	r.Authors = parseStrArray(authors)
	r.Tags = parseStrArray(tags)
	r.Collections = parseStrArray(collections)
	if year.Valid {
		v := int(year.Int64)
		r.Year = &v
	}
	if citation.Valid {
		v := int(citation.Int64)
		r.CitationCount = &v
	}
	r.Venue, r.DOI, r.ArxivID, r.URL = venue.String, doi.String, arxiv.String, url.String
	r.PDFURL, r.Abstract, r.TLDR, r.Source, r.ExternalID = pdfURL.String, abstract.String, tldr.String, source.String, extID.String
	r.BodyMarkdown = bodyMD.String
	if details.Valid && details.String != "" {
		_ = json.Unmarshal([]byte(details.String), &r.Details)
	}
	if zotero.Valid && zotero.String != "" {
		var z zoteroStorageRef
		if json.Unmarshal([]byte(zotero.String), &z) == nil {
			r.ZoteroStorage = &z
		}
	}
	return r, nil
}

func detailsJSON(m map[string]string) sql.NullString {
	if len(m) == 0 {
		return sql.NullString{}
	}
	b, _ := json.Marshal(m)
	return sql.NullString{String: string(b), Valid: true}
}

func zoteroJSON(z *zoteroStorageRef) sql.NullString {
	if z == nil {
		return sql.NullString{}
	}
	b, _ := json.Marshal(z)
	return sql.NullString{String: string(b), Valid: true}
}

func enrichmentJSON(raw json.RawMessage) sql.NullString {
	if len(raw) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(raw), Valid: true}
}

func (s *Server) createReference(ctx context.Context, team string, b referenceBody) (referenceOut, error) {
	id := NewID()
	now := NowUTC()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO reference_items (`+referenceCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, team, normalizeRefType(b.Type), b.Title, jsonStrArray(b.Authors), nullInt(b.Year),
		refNullStr(b.Venue), refNullStr(b.DOI), refNullStr(b.ArxivID), refNullStr(b.URL), refNullStr(b.PDFURL),
		refNullStr(b.Abstract), refNullStr(b.TLDR), nullInt(b.CitationCount), refNullStr(b.Source),
		refNullStr(b.ExternalID), jsonStrArray(b.Tags), jsonStrArray(b.Collections), b.Notes,
		refNullStr(b.BodyMarkdown), detailsJSON(b.Details), zoteroJSON(b.ZoteroStorage),
		enrichmentJSON(b.Enrichment), now, now)
	if err != nil {
		return referenceOut{}, err
	}
	return s.getReferenceByID(ctx, team, id)
}

func (s *Server) getReferenceByID(ctx context.Context, team, id string) (referenceOut, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+referenceCols+` FROM reference_items WHERE team_id = ? AND id = ?`, team, id)
	return scanReference(row)
}

func (s *Server) listReferences(ctx context.Context, team string, f referenceFilter) ([]referenceOut, error) {
	q := `SELECT ` + referenceCols + ` FROM reference_items WHERE team_id = ?`
	args := []any{team}
	if f.Source != "" {
		q += ` AND source = ?`
		args = append(args, f.Source)
	}
	if f.Q != "" {
		q += ` AND (LOWER(title) LIKE ? OR LOWER(authors_json) LIKE ? OR LOWER(abstract) LIKE ?)`
		like := "%" + strings.ToLower(f.Q) + "%"
		args = append(args, like, like, like)
	}
	q += ` ORDER BY created_at DESC`
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q += ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []referenceOut{}
	for rows.Next() {
		r, err := scanReference(rows)
		if err != nil {
			return nil, err
		}
		// Collection / tag membership are JSON arrays — filter in Go so the
		// SQL stays portable (no JSON1 dependency assumption).
		if f.Collection != "" && !containsStr(r.Collections, f.Collection) {
			continue
		}
		if f.Tag != "" && !containsStr(r.Tags, f.Tag) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// patchReference applies a partial JSON patch onto the existing row: keys
// present in `patch` override, absent keys keep their stored value (plain
// struct-decode semantics onto the loaded body).
func (s *Server) patchReference(ctx context.Context, team, id string, patch json.RawMessage) (referenceOut, error) {
	cur, err := s.getReferenceByID(ctx, team, id)
	if err != nil {
		return referenceOut{}, err
	}
	if err := json.Unmarshal(patch, &cur.referenceBody); err != nil {
		return referenceOut{}, err
	}
	b := cur.referenceBody
	_, err = s.writeDB.ExecContext(ctx, `
		UPDATE reference_items SET
			type = ?, title = ?, authors_json = ?, year = ?, venue = ?, doi = ?, arxiv_id = ?,
			url = ?, pdf_url = ?, abstract = ?, tldr = ?, citation_count = ?, source = ?,
			external_id = ?, tags_json = ?, collections_json = ?, notes = ?, body_markdown = ?,
			details_json = ?, zotero_storage_json = ?, enrichment_json = ?, updated_at = ?
		WHERE team_id = ? AND id = ?`,
		normalizeRefType(b.Type), b.Title, jsonStrArray(b.Authors), nullInt(b.Year), refNullStr(b.Venue),
		refNullStr(b.DOI), refNullStr(b.ArxivID), refNullStr(b.URL), refNullStr(b.PDFURL), refNullStr(b.Abstract),
		refNullStr(b.TLDR), nullInt(b.CitationCount), refNullStr(b.Source), refNullStr(b.ExternalID),
		jsonStrArray(b.Tags), jsonStrArray(b.Collections), b.Notes, refNullStr(b.BodyMarkdown),
		detailsJSON(b.Details), zoteroJSON(b.ZoteroStorage), enrichmentJSON(b.Enrichment), NowUTC(), team, id)
	if err != nil {
		return referenceOut{}, err
	}
	return s.getReferenceByID(ctx, team, id)
}

func (s *Server) deleteReference(ctx context.Context, team, id string) (bool, error) {
	res, err := s.writeDB.ExecContext(ctx, `DELETE FROM reference_items WHERE team_id = ? AND id = ?`, team, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---- REST handlers ---------------------------------------------------------

func (s *Server) handleListReferences(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	f := referenceFilter{
		Collection: r.URL.Query().Get("collection"),
		Tag:        r.URL.Query().Get("tag"),
		Source:     r.URL.Query().Get("source"),
		Q:          r.URL.Query().Get("q"),
	}
	out, err := s.listReferences(r.Context(), team, f)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateReference(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var b referenceBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(b.Title) == "" && b.ExternalID == "" {
		writeErr(w, http.StatusBadRequest, "title or external_id required")
		return
	}
	out, err := s.createReference(r.Context(), team, b)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetReference(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "ref")
	out, err := s.getReferenceByID(r.Context(), team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "reference not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateReference(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "ref")
	patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInlineDocBytes))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, err := s.patchReference(r.Context(), team, id, patch)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "reference not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteReference(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "ref")
	ok, err := s.deleteReference(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "reference not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

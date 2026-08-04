package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handlers_records.go — decision records (compare-wall/decisions plan §4.1,
// lane B wedge B1). Team-scoped through their project, REST here on the shared
// store methods below; the record_* MCP verbs (B3) will bind to the same
// methods so agents and the desktop see one log.
//
// Three rules carry the entity, and each of them is a refusal:
//
//   - PROVENANCE COMES FROM THE TOKEN. `created_by_kind` / `created_by_id` /
//     `origin_session_id` are derived from the authenticated caller and are
//     ignored if a body sends them. An agent is recorded as an agent because
//     its token says so.
//   - STATUS IS HISTORY. proposed → accepted is the only patchable
//     transition. 'superseded' is not something you set; it is what happens
//     to a record when its successor is ACCEPTED — a successor still under
//     review must not retire the decision it hopes to replace.
//   - EVIDENCE IS TYPED IDS. A link is {kind,id,note} with kind from a closed
//     set, never a URL. That is what makes a link a join key: the app renders
//     it as a jump-chip and an agent dereferences it with the tools it has.

// recordKinds / recordStatuses are the closed vocabularies. Both are validated
// at the boundary rather than defaulted silently — a record whose kind was
// quietly rewritten is a record nobody can find again.
var recordKinds = map[string]bool{"decision": true, "finding": true}

const (
	recordProposed   = "proposed"
	recordAccepted   = "accepted"
	recordSuperseded = "superseded"
)

// recordLinkKinds is the evidence vocabulary. Every member is an entity the
// hub can already resolve, which is the test for adding one: a link kind with
// no dereference is a dead chip.
var recordLinkKinds = map[string]bool{
	"run": true, "episode": true, "dataset": true, "reference": true, "doc": true,
}

// maxRecordBodyBytes keeps `body_md` a rationale rather than a document.
// Authoring lives in J2; the plan's non-goals are the fence.
const maxRecordBodyBytes = 64 * 1024

// maxRecordLinks bounds the evidence strip. A record citing 200 runs is a
// query, not a decision.
const maxRecordLinks = 64

// recordLink is one piece of evidence: a typed id, not a URL.
type recordLink struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
}

// recordBody is the mutable projection — what a create or patch may set.
// Provenance fields are deliberately absent: see the header.
type recordBody struct {
	Kind   string       `json:"kind"`
	Title  string       `json:"title"`
	BodyMD string       `json:"body_md"`
	Status string       `json:"status"`
	Links  []recordLink `json:"links"`
}

type recordOut struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	recordBody
	SupersedesID    string `json:"supersedes_id,omitempty"`
	CreatedByKind   string `json:"created_by_kind"`
	CreatedByID     string `json:"created_by_id,omitempty"`
	OriginSessionID string `json:"origin_session_id,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

const recordCols = `id, project_id, kind, title, body_md, status,
	COALESCE(supersedes_id, ''), created_by_kind, COALESCE(created_by_id, ''),
	COALESCE(origin_session_id, ''), links_json, created_at, updated_at`

func scanRecord(sc rowScanner) (recordOut, error) {
	var (
		out       recordOut
		linksJSON string
	)
	if err := sc.Scan(&out.ID, &out.ProjectID, &out.Kind, &out.Title, &out.BodyMD, &out.Status,
		&out.SupersedesID, &out.CreatedByKind, &out.CreatedByID, &out.OriginSessionID,
		&linksJSON, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return recordOut{}, err
	}
	out.Links = []recordLink{}
	if linksJSON != "" {
		// A row we cannot parse still lists — with no evidence rather than a
		// 500. The write path validates, so this is defence, not tolerance.
		_ = json.Unmarshal([]byte(linksJSON), &out.Links)
		if out.Links == nil {
			out.Links = []recordLink{}
		}
	}
	return out, nil
}

// validateRecordBody normalises and refuses. Returns the sanitised links so a
// caller never has to trust what it decoded.
func validateRecordBody(b *recordBody, creating bool) error {
	b.Kind = strings.TrimSpace(b.Kind)
	if b.Kind == "" && creating {
		b.Kind = "decision"
	}
	if !recordKinds[b.Kind] {
		return fmt.Errorf("kind must be one of: decision, finding")
	}
	if strings.TrimSpace(b.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len(b.BodyMD) > maxRecordBodyBytes {
		return fmt.Errorf("body_md is %d bytes; the limit is %d — a record is a rationale with links, not a document (author it in a document and link it)",
			len(b.BodyMD), maxRecordBodyBytes)
	}
	if len(b.Links) > maxRecordLinks {
		return fmt.Errorf("links has %d entries; the limit is %d", len(b.Links), maxRecordLinks)
	}
	for i := range b.Links {
		b.Links[i].Kind = strings.TrimSpace(b.Links[i].Kind)
		b.Links[i].ID = strings.TrimSpace(b.Links[i].ID)
		if !recordLinkKinds[b.Links[i].Kind] {
			return fmt.Errorf("links[%d].kind %q is not one of: run, episode, dataset, reference, doc", i, b.Links[i].Kind)
		}
		if b.Links[i].ID == "" {
			return fmt.Errorf("links[%d].id is required — evidence is a typed id, not a note", i)
		}
	}
	if b.Links == nil {
		b.Links = []recordLink{}
	}
	return nil
}

func recordLinksJSON(links []recordLink) string {
	if links == nil {
		links = []recordLink{}
	}
	buf, err := json.Marshal(links)
	if err != nil {
		return "[]"
	}
	return string(buf)
}

// recordProvenance derives who is writing from the authenticated caller.
// Never from the body (the F-08 lesson: attribution comes from the token).
func (s *Server) recordProvenance(ctx context.Context, team string) (kind, id, session string) {
	_, actorKind, handle := actorFromContext(ctx)
	if actorKind != "agent" {
		return "user", "", ""
	}
	// We know it was an agent even if the handle no longer resolves (a
	// terminated agent's row can be archived), so the KIND is recorded either
	// way — dropping to "user" would misattribute the write to the director.
	if handle == "" {
		return "agent", "", ""
	}
	var agentID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM agents WHERE team_id = ? AND handle = ?`, team, handle).Scan(&agentID)
	if err != nil || agentID == "" {
		return "agent", "", ""
	}
	return "agent", agentID, s.lookupAgentSession(ctx, agentID)
}

// ---- store -----------------------------------------------------------------

func (s *Server) getRecordByID(ctx context.Context, team, id string) (recordOut, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+recordCols+` FROM records r
		WHERE r.id = ? AND r.project_id IN (SELECT id FROM projects WHERE team_id = ?)`, id, team)
	return scanRecord(row)
}

type recordFilter struct {
	ProjectID string
	Kind      string
	Status    string
	Limit     int
}

func (s *Server) listRecords(ctx context.Context, team string, f recordFilter) ([]recordOut, error) {
	q := `SELECT ` + recordCols + ` FROM records
		WHERE project_id IN (SELECT id FROM projects WHERE team_id = ?)`
	args := []any{team}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, f.Status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []recordOut{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// createRecord writes a new record. `supersedes` is set by the supersede
// route; a plain create leaves it empty.
func (s *Server) createRecord(ctx context.Context, team, projectID string, b recordBody, supersedes string) (recordOut, error) {
	kind, byID, session := s.recordProvenance(ctx, team)
	// Only the director may mint an already-accepted record; an agent's
	// proposal is a proposal (the propose-verb posture, plan §4.3).
	status := recordProposed
	if b.Status == recordAccepted && kind == "user" {
		status = recordAccepted
	}
	id := NewID()
	now := NowUTC()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO records (id, project_id, kind, title, body_md, status, supersedes_id,
		                     created_by_kind, created_by_id, origin_session_id, links_json,
		                     created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, projectID, b.Kind, b.Title, b.BodyMD, status, nullIfEmpty(supersedes),
		kind, nullIfEmpty(byID), nullIfEmpty(session), recordLinksJSON(b.Links), now, now)
	if err != nil {
		return recordOut{}, err
	}
	return s.getRecordByID(ctx, team, id)
}

// errRecordTransition is returned for a status change the log does not allow.
// It is a 409, not a 400: the request is well-formed, the LOG says no.
type errRecordTransition struct{ msg string }

func (e errRecordTransition) Error() string { return e.msg }

// errRecordForbidden is returned when the CALLER may not perform an otherwise
// well-formed mutation. A 403, not a 409: the log would allow it, the caller's
// token does not.
type errRecordForbidden struct{ msg string }

func (e errRecordForbidden) Error() string { return e.msg }

// recordAgentGate enforces the propose-verb posture (plan §4.3: "agents never
// accept") on the mutating paths. Provenance-on-create alone does not carry
// it: the REST routes sit behind teamGate, which checks the token's TEAM and
// not its kind, so an agent bearer reaches PATCH and DELETE too. Without this
// gate an agent could create a proposal and immediately self-accept it — and,
// via /supersede plus a self-accept, retire the director's accepted decision.
//
// The rule for an agent caller: it may touch only records that are (a) still
// proposals and (b) its own, and it may never change `status`. A user (or
// operator) caller passes untouched. An agent whose handle no longer resolves
// has no id to own anything with, and fails closed.
func (s *Server) recordAgentGate(ctx context.Context, team string, cur recordOut, statusChanging bool) error {
	kind, byID, _ := s.recordProvenance(ctx, team)
	if kind != "agent" {
		return nil
	}
	if statusChanging {
		return errRecordForbidden{"agents never accept — proposing is the agent verb; the desktop user moves a record to accepted (plan §4.3)"}
	}
	if cur.Status != recordProposed {
		return errRecordForbidden{"a " + cur.Status + " record is history — an agent may propose a successor (POST /records/" + cur.ID + "/supersede), not edit it"}
	}
	if byID == "" || cur.CreatedByKind != "agent" || cur.CreatedByID != byID {
		return errRecordForbidden{"an agent may edit or withdraw only its own proposals"}
	}
	return nil
}

// patchRecord applies a partial patch. Status moves only proposed → accepted;
// accepting a record that supersedes another retires the predecessor in the
// same transaction, which is the only way a row reaches 'superseded'.
func (s *Server) patchRecord(ctx context.Context, team, id string, patch json.RawMessage) (recordOut, error) {
	cur, err := s.getRecordByID(ctx, team, id)
	if err != nil {
		return recordOut{}, err
	}
	if cur.Status == recordSuperseded {
		return recordOut{}, errRecordTransition{"this record was superseded; edit its successor instead"}
	}
	next := cur.recordBody
	if err := json.Unmarshal(patch, &next); err != nil {
		return recordOut{}, err
	}
	if err := validateRecordBody(&next, false); err != nil {
		return recordOut{}, err
	}
	if next.Status != cur.Status {
		if !(cur.Status == recordProposed && next.Status == recordAccepted) {
			return recordOut{}, errRecordTransition{fmt.Sprintf(
				"cannot move a record from %q to %q — the only patchable transition is proposed to accepted; "+
					"to retire an accepted record, POST a successor to /records/%s/supersede",
				cur.Status, next.Status, id)}
		}
	}
	if err := s.recordAgentGate(ctx, team, cur, next.Status != cur.Status); err != nil {
		return recordOut{}, err
	}
	accepting := cur.Status == recordProposed && next.Status == recordAccepted

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return recordOut{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE records SET kind = ?, title = ?, body_md = ?, status = ?, links_json = ?, updated_at = ?
		WHERE id = ?`,
		next.Kind, next.Title, next.BodyMD, next.Status, recordLinksJSON(next.Links), NowUTC(), id); err != nil {
		return recordOut{}, err
	}
	// The predecessor retires only now — a successor still under review must
	// not retire the decision it hopes to replace.
	if accepting && cur.SupersedesID != "" {
		if _, err := tx.ExecContext(ctx, `
			UPDATE records SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
			recordSuperseded, NowUTC(), cur.SupersedesID, recordAccepted); err != nil {
			return recordOut{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return recordOut{}, err
	}
	return s.getRecordByID(ctx, team, id)
}

// deleteRecord removes a record that is still a proposal. Anything past that
// is history: it supersedes, it does not disappear. An agent may withdraw
// only its OWN proposal (recordAgentGate) — deleting a competing one is not a
// verb agents have.
func (s *Server) deleteRecord(ctx context.Context, team, id string) error {
	cur, err := s.getRecordByID(ctx, team, id)
	if err != nil {
		return err
	}
	if cur.Status != recordProposed {
		return errRecordTransition{fmt.Sprintf(
			"a %s record is history and cannot be deleted — POST a successor to /records/%s/supersede", cur.Status, id)}
	}
	if err := s.recordAgentGate(ctx, team, cur, false); err != nil {
		return err
	}
	_, err = s.writeDB.ExecContext(ctx, `DELETE FROM records WHERE id = ?`, id)
	return err
}

// ---- REST handlers ---------------------------------------------------------

func (s *Server) handleListRecords(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	out, err := s.listRecords(r.Context(), team, recordFilter{
		ProjectID: r.URL.Query().Get("project"),
		Kind:      r.URL.Query().Get("kind"),
		Status:    r.URL.Query().Get("status"),
	})
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type recordCreateIn struct {
	ProjectID string `json:"project_id"`
	recordBody
}

func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in recordCreateIn
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRecordBodyBytes*2)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.ProjectID == "" {
		writeErr(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if err := validateRecordBody(&in.recordBody, true); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Records are team-scoped THROUGH their project, so the edge is resolved
	// before a row is written (projectInTeamCtx is the shared check).
	if err := s.projectInTeamCtx(r.Context(), team, in.ProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found in this team")
			return
		}
		s.writeDBErr(w, err)
		return
	}
	out, err := s.createRecord(r.Context(), team, in.ProjectID, in.recordBody, "")
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetRecord(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	out, err := s.getRecordByID(r.Context(), team, chi.URLParam(r, "record"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "record not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "record")
	patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecordBodyBytes*2))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, err := s.patchRecord(r.Context(), team, id, patch)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "record not found")
		return
	case err != nil:
		var fe errRecordForbidden
		if errors.As(err, &fe) {
			writeErr(w, http.StatusForbidden, fe.msg)
			return
		}
		var te errRecordTransition
		if errors.As(err, &te) {
			writeErr(w, http.StatusConflict, te.msg)
			return
		}
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.Contains(err.Error(), "is required") || strings.Contains(err.Error(), "must be one of") ||
			strings.Contains(err.Error(), "the limit is") || strings.Contains(err.Error(), "not one of") {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSupersedeRecord creates the successor and the edge in one call. The
// predecessor keeps its status until the successor is accepted.
func (s *Server) handleSupersedeRecord(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "record")
	prev, err := s.getRecordByID(r.Context(), team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "record not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if prev.Status == recordSuperseded {
		writeErr(w, http.StatusConflict, "this record was already superseded — supersede its successor instead")
		return
	}
	var b recordBody
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRecordBodyBytes*2)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// The successor inherits the kind it replaces unless it says otherwise; a
	// finding is not superseded by a decision without saying so.
	if strings.TrimSpace(b.Kind) == "" {
		b.Kind = prev.Kind
	}
	if strings.TrimSpace(b.Title) == "" {
		b.Title = prev.Title
	}
	if err := validateRecordBody(&b, true); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.createRecord(r.Context(), team, prev.ProjectID, b, prev.ID)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "record")
	err := s.deleteRecord(r.Context(), team, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "record not found")
		return
	case err != nil:
		var fe errRecordForbidden
		if errors.As(err, &fe) {
			writeErr(w, http.StatusForbidden, fe.msg)
			return
		}
		var te errRecordTransition
		if errors.As(err, &te) {
			writeErr(w, http.StatusConflict, te.msg)
			return
		}
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

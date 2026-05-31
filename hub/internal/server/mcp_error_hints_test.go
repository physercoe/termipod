package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// W3 (ADR-031) — 4xx errors on the discovery-confusable paths carry a
// structured recovery hint. These tests assert the hint names the
// sibling tool the caller most likely wanted.

// hintBody is the writeErrHint response shape.
type hintBody struct {
	Error string `json:"error"`
	Hint  struct {
		HintText string `json:"hint_text"`
		SeeTool  string `json:"see_tool"`
		SeeDoc   string `json:"see_doc"`
	} `json:"hint"`
}

// reqWithParams builds a request carrying chi URL params so a handler
// can be exercised directly without standing up the full router.
func reqWithParams(params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// Path 2 — documents_get on a missing id points at get_project_doc,
// the confusable filesystem-path sibling.
func TestErrorHint_GetDocument_NotFound(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleGetDocument(rec, reqWithParams(map[string]string{"doc": "01MISSINGDOCULID0000000000"}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
	var b hintBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body not JSON: %v — %s", err, rec.Body.String())
	}
	if b.Error == "" {
		t.Error("error field missing — writeErrHint must keep `error` for clients that ignore the hint")
	}
	if b.Hint.SeeTool != "get_project_doc" {
		t.Errorf("hint.see_tool = %q; want get_project_doc", b.Hint.SeeTool)
	}
	if b.Hint.HintText == "" {
		t.Error("hint.hint_text is required")
	}
}

// Path 1 — get_project_doc on a missing file points at documents_get
// (the read-by-ULID sibling — the 2026-05-18 steward incident).
func TestErrorHint_GetProjectDoc_NotFound(t *testing.T) {
	s, dir := newTestServer(t)
	docsRoot := filepath.Join(dir, "docsroot")
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind, docs_root)
		VALUES (?, ?, ?, ?, 'goal', ?)`,
		"proj-hint", defaultTeamID, "Hint Test", NowUTC(), docsRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec := httptest.NewRecorder()
	s.handleGetProjectDoc(rec, reqWithParams(map[string]string{
		"team": defaultTeamID, "project": "proj-hint", "*": "nope.md",
	}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 — body %s", rec.Code, rec.Body.String())
	}
	var b hintBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body not JSON: %v — %s", err, rec.Body.String())
	}
	if b.Hint.SeeTool != "documents_get" {
		t.Errorf("hint.see_tool = %q; want documents_get", b.Hint.SeeTool)
	}
	if b.Hint.HintText == "" {
		t.Error("hint.hint_text is required")
	}
}

// Path 4 — the role-gate denial names the tool and the escalation path
// in the message itself, because a JSON-RPC error reaches the agent
// only via `message` (the `data` field is not reliably surfaced).
func TestErrorHint_RoleDenied_NamesEscalation(t *testing.T) {
	e := roleDeniedErr("worker", "agents_spawn")
	if e == nil || e.Message == "" {
		t.Fatal("roleDeniedErr returned no usable error")
	}
	for _, want := range []string{"agents_spawn", "worker", "request_help", "not permitted for role"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("role-denied message missing %q: %s", want, e.Message)
		}
	}
}

// MCP-path parity with the REST hints: mcpGetProjectDoc is the tool agents
// actually call. It used to collapse every failure into a bare error.Error()
// — so a project with no docs_root (the default) returned "file does not
// exist", reading like a missing file and sending stewards in circles
// (tester feedback, 2026-05-31). Both failure modes must now name the real
// cause and steer to documents.get.
func TestMCPGetProjectDoc_NoDocsRoot_PointsAtDocumentsGet(t *testing.T) {
	s, _ := newTestServer(t)
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, ?, ?, 'goal')`,
		"proj-nodocs", defaultTeamID, "No Docs", NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	raw := json.RawMessage(`{"project_id":"proj-nodocs","path":"plans/x.md"}`)
	_, jerr := s.mcpGetProjectDoc(context.Background(), defaultTeamID, raw)
	if jerr == nil {
		t.Fatal("expected an error for a project with no docs_root")
	}
	for _, want := range []string{"docs_root", "documents.get"} {
		if !strings.Contains(jerr.Message, want) {
			t.Errorf("no-docs_root message missing %q: %s", want, jerr.Message)
		}
	}
	if strings.Contains(jerr.Message, "file does not exist") {
		t.Errorf("message still reads as a missing file: %s", jerr.Message)
	}
}

func TestMCPGetProjectDoc_MissingFile_PointsAtDocumentsGet(t *testing.T) {
	s, dir := newTestServer(t)
	docsRoot := filepath.Join(dir, "docsroot")
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind, docs_root)
		VALUES (?, ?, ?, ?, 'goal', ?)`,
		"proj-docs", defaultTeamID, "Has Docs", NowUTC(), docsRoot); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	raw := json.RawMessage(`{"project_id":"proj-docs","path":"nope.md"}`)
	_, jerr := s.mcpGetProjectDoc(context.Background(), defaultTeamID, raw)
	if jerr == nil {
		t.Fatal("expected an error for a missing file under docs_root")
	}
	if !strings.Contains(jerr.Message, "documents.get") {
		t.Errorf("missing-file message should steer to documents.get: %s", jerr.Message)
	}
}

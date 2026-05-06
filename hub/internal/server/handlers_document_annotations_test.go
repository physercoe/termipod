package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-020 W1 tests for the document_annotations primitive.

// createAnnotationDoc seeds a typed proposal document with three sections
// so the tests can attach annotations.
func createAnnotationDoc(t *testing.T, s *Server, token, projID string) string {
	t.Helper()
	return createTypedDocument(t, s, token, projID, "proposal", "proposal-v1",
		[]map[string]any{
			{"slug": "motivation", "title": "Motivation", "body": "why", "status": "draft"},
			{"slug": "approach", "title": "Approach", "body": "how", "status": "draft"},
			{"slug": "risks", "title": "Risks", "body": "if", "status": "empty"},
		})
}

func createAnnotation(
	t *testing.T, s *Server, token, docID string, payload map[string]any,
) (int, Annotation) {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/annotations",
		payload)
	var a Annotation
	if status == http.StatusCreated {
		if err := json.Unmarshal(body, &a); err != nil {
			t.Fatalf("decode annotation: %v body=%s", err, body)
		}
	}
	return status, a
}

func TestAnnotation_CreateAndListRoundTrip(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)

	cs := 4
	ce := 9
	status, a := createAnnotation(t, s, token, docID, map[string]any{
		"section_slug": "motivation",
		"char_start":   cs,
		"char_end":     ce,
		"kind":         "redline",
		"body":         "delete this clause",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: status=%d", status)
	}
	if a.Kind != "redline" || a.Status != "open" || a.Body != "delete this clause" {
		t.Errorf("roundtrip mismatch: %+v", a)
	}
	if a.CharStart == nil || *a.CharStart != cs || a.CharEnd == nil || *a.CharEnd != ce {
		t.Errorf("char range lost: %+v", a)
	}

	listStatus, listBody := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/annotations?section=motivation",
		nil)
	if listStatus != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", listStatus, listBody)
	}
	var lst struct {
		Annotations []Annotation `json:"annotations"`
	}
	_ = json.Unmarshal(listBody, &lst)
	if len(lst.Annotations) != 1 || lst.Annotations[0].ID != a.ID {
		t.Errorf("list: got %+v, want one annotation", lst.Annotations)
	}
}

func TestAnnotation_NullableCharRange(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)

	status, a := createAnnotation(t, s, token, docID, map[string]any{
		"section_slug": "approach",
		"body":         "good framing",
	})
	if status != http.StatusCreated {
		t.Fatalf("status=%d", status)
	}
	if a.Kind != "comment" {
		t.Errorf("default kind: got %q, want comment", a.Kind)
	}
	if a.CharStart != nil || a.CharEnd != nil {
		t.Errorf("expected null char range, got start=%v end=%v",
			a.CharStart, a.CharEnd)
	}
}

func TestAnnotation_RejectsUnknownKind(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)

	status, _ := createAnnotation(t, s, token, docID, map[string]any{
		"section_slug": "motivation",
		"kind":         "applause",
		"body":         "wow",
	})
	if status != http.StatusBadRequest {
		t.Errorf("expected 400 for bad kind, got %d", status)
	}
}

func TestAnnotation_RejectsMissingSection(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)

	status, body := createAnnotation(t, s, token, docID, map[string]any{
		"section_slug": "ghost",
		"body":         "...",
	})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d body=%+v", status, body)
	}
}

func TestAnnotation_StatusFilter(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)

	_, a1 := createAnnotation(t, s, token, docID,
		map[string]any{"section_slug": "motivation", "body": "open one"})
	_, a2 := createAnnotation(t, s, token, docID,
		map[string]any{"section_slug": "motivation", "body": "to be resolved"})

	// Resolve a2.
	resolveStatus, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/annotations/"+a2.ID+"/resolve", nil)
	if resolveStatus != http.StatusOK {
		t.Fatalf("resolve: %d", resolveStatus)
	}

	// Default filter (open) should only show a1.
	st, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/annotations", nil)
	if st != http.StatusOK {
		t.Fatalf("list: %d", st)
	}
	var lst struct {
		Annotations []Annotation `json:"annotations"`
	}
	_ = json.Unmarshal(body, &lst)
	if len(lst.Annotations) != 1 || lst.Annotations[0].ID != a1.ID {
		t.Errorf("default filter should hide resolved: got %+v", lst.Annotations)
	}

	// status=resolved → only a2.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/annotations?status=resolved", nil)
	_ = json.Unmarshal(body, &lst)
	if len(lst.Annotations) != 1 || lst.Annotations[0].ID != a2.ID {
		t.Errorf("resolved filter: got %+v", lst.Annotations)
	}

	// status=all → both.
	_, body = doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/annotations?status=all", nil)
	_ = json.Unmarshal(body, &lst)
	if len(lst.Annotations) != 2 {
		t.Errorf("status=all: got %d, want 2", len(lst.Annotations))
	}
}

func TestAnnotation_PatchByNonAuthorRejected(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)

	// Seed an annotation directly with a different author so the bearer
	// token's principal isn't the recorded author. Bypasses the create
	// handler intentionally — the test target is patch-by-non-author.
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO document_annotations (
			id, document_id, section_slug, kind, body, status,
			author_kind, author_handle, created_at
		) VALUES (?, ?, 'motivation', 'comment', 'first', 'open',
		          'agent', 'someone-else', ?)`,
		id, docID, NowUTC()); err != nil {
		t.Fatalf("seed annotation: %v", err)
	}

	st, body := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/annotations/"+id,
		map[string]any{"body": "tampered"})
	if st != http.StatusForbidden {
		t.Errorf("expected 403, got %d body=%s", st, body)
	}
}

func TestAnnotation_DeleteRejectedWith405(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)
	_, a := createAnnotation(t, s, token, docID,
		map[string]any{"section_slug": "motivation", "body": "no delete"})

	st, body := doReq(t, s, token, http.MethodDelete,
		"/v1/teams/"+defaultTeamID+"/annotations/"+a.ID, nil)
	if st != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d body=%s", st, body)
	}
	if !strings.Contains(string(body), "ADR-020") {
		t.Errorf("expected 405 message to cite ADR-020, got %s", body)
	}
}

func TestAnnotation_ResolveReopenAndAudits(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createAnnotationDoc(t, s, token, pid)
	_, a := createAnnotation(t, s, token, docID,
		map[string]any{"section_slug": "motivation", "body": "lifecycle"})

	st, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/annotations/"+a.ID+"/resolve", nil)
	if st != http.StatusOK {
		t.Fatalf("resolve: %d", st)
	}
	st, _ = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/annotations/"+a.ID+"/reopen", nil)
	if st != http.StatusOK {
		t.Fatalf("reopen: %d", st)
	}
	counts := countAuditActions(t, s)
	for _, k := range []string{"annotation.created", "annotation.resolved", "annotation.reopened"} {
		if counts[k] == 0 {
			t.Errorf("expected audit %q, got 0", k)
		}
	}
}

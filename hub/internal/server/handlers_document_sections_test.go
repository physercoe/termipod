package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// W5a — Structured Document Viewer (A4) tests for hub-side
// section endpoints. Covers happy-path PATCH + status, 412 mismatch,
// the empty→draft and ratified→draft state transitions, and audit
// kinds emitted.

func createTypedDocument(
	t *testing.T, s *Server, token, projID string,
	kind, schemaID string, sections []map[string]any,
) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"schema_id":      schemaID,
		"sections":       sections,
	})
	status, resp := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":     projID,
			"kind":           kind,
			"schema_id":      schemaID,
			"title":          "t",
			"content_inline": string(body),
		})
	if status != http.StatusCreated {
		t.Fatalf("create typed doc: status=%d body=%s", status, resp)
	}
	var doc struct {
		ID       string `json:"id"`
		SchemaID string `json:"schema_id"`
	}
	if err := json.Unmarshal(resp, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.SchemaID != schemaID {
		t.Errorf("schema_id roundtrip: got %q, want %q", doc.SchemaID, schemaID)
	}
	return doc.ID
}

func createProjectForDocs(t *testing.T, s *Server, token string) string {
	t.Helper()
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/projects",
		map[string]any{"name": "p", "kind": "goal"})
	if status != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", status, body)
	}
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &p)
	return p.ID
}

func TestDocumentSection_AllowsTypedKindsWhenSchemaIDSet(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)

	// Without schema_id, "proposal" is rejected by the legacy allowlist.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":     pid,
			"kind":           "proposal",
			"title":          "t",
			"content_inline": "hi",
		})
	if status != http.StatusBadRequest {
		t.Errorf("plain proposal should 400, got %d body=%s", status, body)
	}

	// With schema_id, the typed kind is accepted.
	docID := createTypedDocument(t, s, token, pid, "proposal", "proposal-v1",
		[]map[string]any{{"slug": "motivation", "title": "Motivation",
			"body": "", "status": "empty"}})
	if docID == "" {
		t.Fatal("expected docID")
	}
}

func TestDocumentSection_PatchEmptyToDraftAndAudits(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createTypedDocument(t, s, token, pid, "proposal", "proposal-v1",
		[]map[string]any{{
			"slug": "motivation", "title": "Motivation",
			"body": "", "status": "empty",
		}})

	status, body := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/sections/motivation",
		map[string]any{"body": "Why this matters."})
	if status != http.StatusOK {
		t.Fatalf("patch section: status=%d body=%s", status, body)
	}
	var sec structuredSection
	if err := json.Unmarshal(body, &sec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sec.Status != sectionStateDraft {
		t.Errorf("status: got %q, want draft", sec.Status)
	}
	if sec.Body != "Why this matters." {
		t.Errorf("body roundtrip: got %q", sec.Body)
	}
	if sec.LastAuthoredAt == "" {
		t.Errorf("last_authored_at not stamped")
	}
	counts := countAuditActions(t, s)
	if counts["document.section_authored"] == 0 {
		t.Errorf("expected document.section_authored audit; got %+v", counts)
	}
}

func TestDocumentSection_RatifiedEditDowngradesToDraft(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createTypedDocument(t, s, token, pid, "proposal", "proposal-v1",
		[]map[string]any{{
			"slug":             "motivation",
			"title":            "Motivation",
			"body":             "Original.",
			"status":           "ratified",
			"last_authored_at": "2026-05-01T00:00:00Z",
			"ratified_at":      "2026-05-02T00:00:00Z",
			"ratified_by_actor": "user:director",
		}})

	status, body := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/sections/motivation",
		map[string]any{
			"body":                       "Edited.",
			"expected_last_authored_at":  "2026-05-01T00:00:00Z",
		})
	if status != http.StatusOK {
		t.Fatalf("patch ratified section: status=%d body=%s", status, body)
	}
	var sec structuredSection
	_ = json.Unmarshal(body, &sec)
	if sec.Status != sectionStateDraft {
		t.Errorf("ratified→edit must downgrade to draft; got %q", sec.Status)
	}
	if sec.RatifiedAt != "" || sec.RatifiedByActor != "" {
		t.Errorf("ratified stamps not cleared: %+v", sec)
	}
}

func TestDocumentSection_PatchReturns412OnStaleExpected(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createTypedDocument(t, s, token, pid, "proposal", "proposal-v1",
		[]map[string]any{{
			"slug":             "motivation",
			"title":            "Motivation",
			"body":             "draft body",
			"status":           "draft",
			"last_authored_at": "2026-05-01T00:00:00Z",
		}})

	status, body := doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/sections/motivation",
		map[string]any{
			"body":                      "concurrent edit",
			"expected_last_authored_at": "2020-01-01T00:00:00Z",
		})
	if status != http.StatusPreconditionFailed {
		t.Errorf("expected 412, got %d body=%s", status, body)
	}
	if !strings.Contains(string(body), "modified elsewhere") {
		t.Errorf("expected conflict message in body; got %s", body)
	}
}

func TestDocumentSection_StatusRatifyAndUnratify(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)
	docID := createTypedDocument(t, s, token, pid, "proposal", "proposal-v1",
		[]map[string]any{
			{"slug": "motivation", "title": "Motivation",
				"body": "Why this matters.", "status": "draft",
				"last_authored_at": "2026-05-01T00:00:00Z"},
			{"slug": "scope", "title": "Scope",
				"body": "", "status": "empty"},
		})

	// Cannot ratify an empty section.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/sections/scope/status",
		map[string]any{"status": "ratified"})
	if status != http.StatusConflict {
		t.Errorf("ratify empty: expected 409, got %d body=%s", status, body)
	}

	// Ratify draft.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/sections/motivation/status",
		map[string]any{"status": "ratified"})
	if status != http.StatusOK {
		t.Fatalf("ratify: status=%d body=%s", status, body)
	}
	var sec structuredSection
	_ = json.Unmarshal(body, &sec)
	if sec.Status != sectionStateRatified {
		t.Errorf("status: got %q, want ratified", sec.Status)
	}
	if sec.RatifiedAt == "" {
		t.Errorf("ratified_at not stamped")
	}

	counts := countAuditActions(t, s)
	if counts["document.section_ratified"] == 0 {
		t.Errorf("expected document.section_ratified audit; got %+v", counts)
	}

	// Unratify. Use a fresh struct — with `omitempty` on the response,
	// reusing the prior `sec` would leave stale RatifiedAt populated.
	status, body = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents/"+docID+"/sections/motivation/status",
		map[string]any{"status": "draft"})
	if status != http.StatusOK {
		t.Fatalf("unratify: status=%d body=%s", status, body)
	}
	var sec2 structuredSection
	_ = json.Unmarshal(body, &sec2)
	if sec2.Status != sectionStateDraft {
		t.Errorf("unratify: got %q, want draft", sec2.Status)
	}
	if sec2.RatifiedAt != "" {
		t.Errorf("ratified_at not cleared on unratify: %+v", sec2)
	}
}

func TestDocumentSection_PlainMarkdownRejectsSectionEndpoints(t *testing.T) {
	s, token := newA2ATestServer(t)
	pid := createProjectForDocs(t, s, token)

	// Plain markdown doc — no schema_id.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":     pid,
			"kind":           "memo",
			"title":          "plain",
			"content_inline": "just markdown",
		})
	if status != http.StatusCreated {
		t.Fatalf("create plain doc: status=%d body=%s", status, body)
	}
	var doc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &doc)

	// PATCH section on plain doc → 409.
	status, body = doReq(t, s, token, http.MethodPatch,
		"/v1/teams/"+defaultTeamID+"/documents/"+doc.ID+"/sections/foo",
		map[string]any{"body": "x"})
	if status != http.StatusConflict {
		t.Errorf("expected 409, got %d body=%s", status, body)
	}
}

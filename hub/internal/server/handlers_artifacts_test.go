package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// seedTestProject inserts a project owned by the given team and returns
// its ID. Shared by the artifact tests.
func seedTestProject(t *testing.T, s *Server, team string) string {
	t.Helper()
	projID := NewID()
	if _, err := s.db.ExecContext(context.Background(), `
		INSERT INTO projects (id, team_id, name, status, created_at)
		VALUES (?, ?, ?, 'active', ?)`,
		projID, team, "p-"+projID, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

func TestCreateArtifact_ProjectScope(t *testing.T) {
	s, token := newA2ATestServer(t)
	projID := seedTestProject(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/artifacts"

	size := int64(1024)
	status, body := doReq(t, s, token, http.MethodPost, base, map[string]any{
		"project_id": projID,
		"kind":       "checkpoint",
		"name":       "step_1000.pt",
		"uri":        "blob:sha256/deadbeef",
		"sha256":     "deadbeef",
		"size":       size,
		"mime":       "application/octet-stream",
	})
	if status != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", status, body)
	}
	var out artifactOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// `checkpoint` is a legacy kind name; W1 remaps it to `external-blob`.
	if out.ID == "" || out.Kind != "external-blob" || out.URI == "" {
		t.Errorf("unexpected out=%+v", out)
	}
	if out.Size == nil || *out.Size != 1024 {
		t.Errorf("size=%v, want 1024", out.Size)
	}

	// List by project.
	status, body = doReq(t, s, token, http.MethodGet, base+"?project="+projID, nil)
	if status != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", status, body)
	}
	var list []artifactOut
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != out.ID {
		t.Errorf("list=%+v, want single artifact id=%s", list, out.ID)
	}

	// Get by id.
	status, body = doReq(t, s, token, http.MethodGet, base+"/"+out.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", status, body)
	}
	var got artifactOut
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.ID != out.ID {
		t.Errorf("got id=%s, want %s", got.ID, out.ID)
	}
}

func TestCreateArtifact_WithRun(t *testing.T) {
	s, token := newA2ATestServer(t)
	runID := seedTestRun(t, s, defaultTeamID)
	// Look up the project the run belongs to.
	var projID string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT project_id FROM runs WHERE id = ?`, runID).Scan(&projID)
	if err != nil {
		t.Fatalf("lookup project: %v", err)
	}

	base := "/v1/teams/" + defaultTeamID + "/artifacts"
	status, body := doReq(t, s, token, http.MethodPost, base, map[string]any{
		"project_id":   projID,
		"run_id":       runID,
		"kind":         "eval_curve",
		"name":         "eval.json",
		"uri":          "blob:sha256/cafef00d",
		"lineage_json": map[string]any{"parents": []string{"r1"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", status, body)
	}

	// Filter by run.
	status, body = doReq(t, s, token, http.MethodGet,
		base+"?run="+runID, nil)
	if status != http.StatusOK {
		t.Fatalf("list by run: status=%d body=%s", status, body)
	}
	var list []artifactOut
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].RunID != runID {
		t.Errorf("list=%+v, want single artifact for run=%s", list, runID)
	}
	if list[0].LineageJSON == "" {
		t.Errorf("lineage_json missing: %+v", list[0])
	}
}

func TestCreateArtifact_Validates(t *testing.T) {
	s, token := newA2ATestServer(t)
	projID := seedTestProject(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/artifacts"

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing project", map[string]any{"kind": "log", "name": "a", "uri": "blob:sha256/x"}},
		{"missing kind", map[string]any{"project_id": projID, "name": "a", "uri": "blob:sha256/x"}},
		{"missing name", map[string]any{"project_id": projID, "kind": "log", "uri": "blob:sha256/x"}},
		{"missing uri", map[string]any{"project_id": projID, "kind": "log", "name": "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _ := doReq(t, s, token, http.MethodPost, base, c.body)
			if status != http.StatusBadRequest {
				t.Errorf("status=%d want 400", status)
			}
		})
	}
}

func TestCreateArtifact_RejectsCrossTeamProject(t *testing.T) {
	s, token := newA2ATestServer(t)
	// Project belongs to a different team.
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"other-team", "other", NowUTC())
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	otherProj := seedTestProject(t, s, "other-team")

	base := "/v1/teams/" + defaultTeamID + "/artifacts"
	status, _ := doReq(t, s, token, http.MethodPost, base, map[string]any{
		"project_id": otherProj,
		"kind":       "log",
		"name":       "a",
		"uri":        "blob:sha256/x",
	})
	if status != http.StatusNotFound {
		t.Errorf("cross-team create: status=%d want 404", status)
	}
}

func TestCreateArtifact_RejectsMismatchedRun(t *testing.T) {
	s, token := newA2ATestServer(t)
	projA := seedTestProject(t, s, defaultTeamID)
	runB := seedTestRun(t, s, defaultTeamID) // different project

	base := "/v1/teams/" + defaultTeamID + "/artifacts"
	status, _ := doReq(t, s, token, http.MethodPost, base, map[string]any{
		"project_id": projA,
		"run_id":     runB,
		"kind":       "log",
		"name":       "a",
		"uri":        "blob:sha256/x",
	})
	if status != http.StatusBadRequest {
		t.Errorf("mismatched run: status=%d want 400", status)
	}
}

func TestCreateDocument_ResolvesArtifactID(t *testing.T) {
	// With the artifacts table landed, documents.artifact_id must resolve
	// to a real artifact in the same project.
	s, token := newA2ATestServer(t)
	projID := seedTestProject(t, s, defaultTeamID)

	// Unknown artifact_id → 400.
	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":  projID,
			"kind":        "report",
			"title":       "r",
			"artifact_id": "does-not-exist",
		})
	if status != http.StatusBadRequest {
		t.Errorf("unknown artifact_id: status=%d want 400", status)
	}

	// Create an artifact in a different project, then try to link it.
	otherProj := seedTestProject(t, s, defaultTeamID)
	artBase := "/v1/teams/" + defaultTeamID + "/artifacts"
	_, body := doReq(t, s, token, http.MethodPost, artBase, map[string]any{
		"project_id": otherProj,
		"kind":       "report",
		"name":       "r.pdf",
		"uri":        "blob:sha256/feedface",
	})
	var a artifactOut
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	status, _ = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":  projID, // wrong project
			"kind":        "report",
			"title":       "r",
			"artifact_id": a.ID,
		})
	if status != http.StatusBadRequest {
		t.Errorf("cross-project artifact_id: status=%d want 400", status)
	}

	// Correct project resolves happily.
	status, _ = doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/documents",
		map[string]any{
			"project_id":  otherProj,
			"kind":        "report",
			"title":       "r",
			"artifact_id": a.ID,
		})
	if status != http.StatusCreated {
		t.Errorf("valid artifact_id: status=%d want 201", status)
	}
}

func TestGetArtifact_NotFound(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/artifacts/does-not-exist", nil)
	if status != http.StatusNotFound {
		t.Errorf("status=%d want 404", status)
	}
}

// W1 — closed-set kind validation. Accepts every entry in
// validArtifactKinds, rewrites legacy names via
// backfillLegacyArtifactKind, and rejects anything else with 400.
func TestCreateArtifact_ClosedKindSet(t *testing.T) {
	s, token := newA2ATestServer(t)
	projID := seedTestProject(t, s, defaultTeamID)
	base := "/v1/teams/" + defaultTeamID + "/artifacts"

	// Every MVP kind round-trips.
	for kind := range validArtifactKinds {
		status, body := doReq(t, s, token, http.MethodPost, base, map[string]any{
			"project_id": projID,
			"kind":       kind,
			"name":       "a-" + kind,
			"uri":        "blob:sha256/cafef00d",
		})
		if status != http.StatusCreated {
			t.Errorf("create kind=%s: status=%d body=%s", kind, status, body)
			continue
		}
		var out artifactOut
		if err := json.Unmarshal(body, &out); err != nil {
			t.Errorf("decode kind=%s: %v", kind, err)
			continue
		}
		if out.Kind != kind {
			t.Errorf("create kind=%s: out.Kind=%s", kind, out.Kind)
		}
	}

	// Bogus kind rejected.
	status, _ := doReq(t, s, token, http.MethodPost, base, map[string]any{
		"project_id": projID,
		"kind":       "freeform-stuff",
		"name":       "x",
		"uri":        "blob:sha256/cafef00d",
	})
	if status != http.StatusBadRequest {
		t.Errorf("unknown kind: status=%d want 400", status)
	}

	// Legacy kinds remap. Each must produce 201 with the mapped kind.
	legacy := map[string]string{
		"checkpoint": "external-blob",
		"dataset":    "external-blob",
		"other":      "external-blob",
		"eval_curve": "metric-chart",
		"log":        "prose-document",
		"report":     "prose-document",
		"figure":     "image",
		"sample":     "image",
	}
	for legacyKind, want := range legacy {
		status, body := doReq(t, s, token, http.MethodPost, base, map[string]any{
			"project_id": projID,
			"kind":       legacyKind,
			"name":       "legacy-" + legacyKind,
			"uri":        "blob:sha256/cafef00d",
		})
		if status != http.StatusCreated {
			t.Errorf("legacy kind=%s: status=%d body=%s", legacyKind, status, body)
			continue
		}
		var out artifactOut
		if err := json.Unmarshal(body, &out); err != nil {
			t.Errorf("decode legacy=%s: %v", legacyKind, err)
			continue
		}
		if out.Kind != want {
			t.Errorf("legacy %s remap: got %s want %s", legacyKind, out.Kind, want)
		}
	}
}

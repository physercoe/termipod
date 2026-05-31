package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// putYAML performs a raw-body YAML PUT through the router as the given
// bearer and returns the status code.
func putYAML(t *testing.T, s *Server, tok, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w.Code
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// listHasTemplate reports whether the team's template listing includes
// category/name.
func listHasTemplate(t *testing.T, s *Server, tok, team, cat, name string) bool {
	t.Helper()
	st, body := doReq(t, s, tok, http.MethodGet, "/v1/teams/"+team+"/templates", nil)
	if st != http.StatusOK {
		t.Fatalf("list templates for %s: status=%d body=%s", team, st, body)
	}
	var entries []templateOut
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, e := range entries {
		if e.Category == cat && e.Name == name {
			return true
		}
	}
	return false
}

// TestTemplateOverride_IsolatesAcrossTeams is the W4 acceptance test
// (ADR-037 D5): a template PUT by one team is invisible to another, and
// each team's spawn resolves its own override.
func TestTemplateOverride_IsolatesAcrossTeams(t *testing.T) {
	s, operatorTok := newA2ATestServer(t)
	a := provisionTeam(t, s, operatorTok, "team-a", "A")
	b := provisionTeam(t, s, operatorTok, "team-b", "B")

	// team-a authors a worker override.
	bodyA := "template: agents.shared-name\nversion: 1\nteam: a\n"
	if st := putYAML(t, s, a.OwnerToken,
		"/v1/teams/team-a/templates/agents/shared-name.v1.yaml", bodyA); st != http.StatusCreated {
		t.Fatalf("team-a PUT: status=%d", st)
	}

	// The file lands in team-a's override dir — not team-b's, not global.
	aPath := filepath.Join(s.cfg.DataRoot, "teams", "team-a", "templates", "agents", "shared-name.v1.yaml")
	if !fileExists(aPath) {
		t.Errorf("expected override at %s", aPath)
	}
	for _, absent := range []string{
		filepath.Join(s.cfg.DataRoot, "teams", "team-b", "templates", "agents", "shared-name.v1.yaml"),
		filepath.Join(s.cfg.DataRoot, "team", "templates", "agents", "shared-name.v1.yaml"),
	} {
		if fileExists(absent) {
			t.Errorf("override leaked to %s", absent)
		}
	}

	// team-a GET sees the override; team-b GET 404s.
	if st, body := doReq(t, s, a.OwnerToken, http.MethodGet,
		"/v1/teams/team-a/templates/agents/shared-name.v1.yaml", nil); st != http.StatusOK {
		t.Errorf("team-a GET own override: status=%d body=%s", st, body)
	}
	if st, _ := doReq(t, s, b.OwnerToken, http.MethodGet,
		"/v1/teams/team-b/templates/agents/shared-name.v1.yaml", nil); st != http.StatusNotFound {
		t.Errorf("team-b GET team-a's override: status=%d, want 404", st)
	}

	// team-b LIST does not show team-a's override; team-a LIST does.
	if listHasTemplate(t, s, b.OwnerToken, "team-b", "agents", "shared-name.v1.yaml") {
		t.Errorf("team-b list shows team-a's override")
	}
	if !listHasTemplate(t, s, a.OwnerToken, "team-a", "agents", "shared-name.v1.yaml") {
		t.Errorf("team-a list missing its own override")
	}

	// The resolver picks team-a's override for a team-a spawn; team-b's
	// spawn cannot resolve it.
	got, err := s.readAgentTemplate("team-a", "agents.shared-name")
	if err != nil {
		t.Fatalf("readAgentTemplate team-a: %v", err)
	}
	if got != bodyA {
		t.Errorf("team-a resolve: got %q, want override", got)
	}
	if _, err := s.readAgentTemplate("team-b", "agents.shared-name"); err == nil {
		t.Errorf("team-b resolved team-a's override — leak")
	}
}

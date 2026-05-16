package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// The list-templates endpoint must surface `applicable_to.template_ids`
// so the mobile project pickers can filter without re-fetching every
// template's body. Templates omitting the field stay team-shared (the
// safe back-compat default → empty list = visible everywhere).
func TestListTemplates_SurfacesApplicableTemplateIDs(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Seed two agent templates: one scoped to a project template
	// (`research-project.v1`), one team-shared.
	agentsDir := filepath.Join(s.cfg.DataRoot, "team", "templates", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	scoped := []byte(
		"template: agents.bio-coder\nversion: 1\n" +
			"applicable_to:\n  template_ids: [bio-sim.v1, chem-sim.v1]\n")
	shared := []byte("template: agents.team-helper\nversion: 1\n")
	if err := os.WriteFile(
		filepath.Join(agentsDir, "bio-coder.v1.yaml"), scoped, 0o644); err != nil {
		t.Fatalf("write scoped: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(agentsDir, "team-helper.v1.yaml"), shared, 0o644); err != nil {
		t.Fatalf("write shared: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/templates?category=agents", nil)
	if status != http.StatusOK {
		t.Fatalf("list templates: status=%d body=%s", status, body)
	}
	var rows []templateOut
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	gotScoped := false
	gotShared := false
	for _, r := range rows {
		switch r.Name {
		case "bio-coder.v1.yaml":
			gotScoped = true
			if len(r.ApplicableTemplateIDs) != 2 ||
				r.ApplicableTemplateIDs[0] != "bio-sim.v1" ||
				r.ApplicableTemplateIDs[1] != "chem-sim.v1" {
				t.Errorf("bio-coder applicable_template_ids = %v; want [bio-sim.v1, chem-sim.v1]",
					r.ApplicableTemplateIDs)
			}
		case "team-helper.v1.yaml":
			gotShared = true
			if len(r.ApplicableTemplateIDs) != 0 {
				t.Errorf("team-helper applicable_template_ids should be empty (team-shared); got %v",
					r.ApplicableTemplateIDs)
			}
		}
	}
	if !gotScoped {
		t.Errorf("scoped template missing from list")
	}
	if !gotShared {
		t.Errorf("shared template missing from list")
	}
}

// Malformed YAML in `applicable_to` must NOT poison the listing — the
// row still appears with an empty applicable_template_ids so the
// picker treats it as team-shared (safe default).
func TestListTemplates_MalformedApplicableToFallsThrough(t *testing.T) {
	s, token := newA2ATestServer(t)
	agentsDir := filepath.Join(s.cfg.DataRoot, "team", "templates", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Tab character (not allowed in YAML indentation) intentionally
	// breaks the parse without breaking the basename / mod_time scan.
	bad := []byte("template: agents.broken\napplicable_to:\n\ttemplate_ids: [x]\n")
	if err := os.WriteFile(
		filepath.Join(agentsDir, "broken.v1.yaml"), bad, 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/templates?category=agents", nil)
	if status != http.StatusOK {
		t.Fatalf("list templates: status=%d body=%s", status, body)
	}
	var rows []templateOut
	_ = json.Unmarshal(body, &rows)
	found := false
	for _, r := range rows {
		if r.Name == "broken.v1.yaml" {
			found = true
			if len(r.ApplicableTemplateIDs) != 0 {
				t.Errorf("malformed YAML should fall through to empty applicable_template_ids; got %v",
					r.ApplicableTemplateIDs)
			}
		}
	}
	if !found {
		t.Errorf("broken.v1.yaml should still appear in the listing (parse failure must not drop the row)")
	}
}

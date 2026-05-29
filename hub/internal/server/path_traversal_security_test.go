package server

import (
	"net/http"
	"strings"
	"testing"
)

// F-03 — installProposedTemplate validates every component that flows
// into a filesystem path (category, name, blob_sha256). A crafted value
// must be refused before any os.ReadFile / os.WriteFile, closing the
// arbitrary read+write the propose→template.install path otherwise
// exposed.
func TestInstallProposedTemplate_RejectsTraversal(t *testing.T) {
	s, _ := newA2ATestServer(t)
	validSHA := strings.Repeat("a", 64)

	cases := []struct {
		name, payload, wantErr string
	}{
		{
			"category traversal",
			`{"category":"../../../etc","name":"x.v1","blob_sha256":"` + validSHA + `"}`,
			"unsafe template category",
		},
		{
			"name traversal",
			`{"category":"agents","name":"../../../tmp/evil","blob_sha256":"` + validSHA + `"}`,
			"unsafe template name",
		},
		{
			"sha path traversal",
			`{"category":"agents","name":"x.v1","blob_sha256":"../../../../etc/passwd"}`,
			"invalid blob_sha256",
		},
		{
			"sha non-hex",
			`{"category":"agents","name":"x.v1","blob_sha256":"zzzz"}`,
			"invalid blob_sha256",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.installProposedTemplate(c.payload)
			if err == nil {
				t.Fatalf("expected rejection, got nil error")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %q; want it to contain %q", err.Error(), c.wantErr)
			}
		})
	}
}

// F-07 — project create refuses a docs_root that escapes the hub data
// root, so get_project_doc / list_project_docs can't become an
// arbitrary-file oracle under the hub UID.
func TestCreateProject_RejectsDocsRootEscape(t *testing.T) {
	s, token := newA2ATestServer(t)

	for _, bad := range []string{"/etc", "/etc/ssh", "../../../etc", "~/.ssh"} {
		status, body := doReq(t, s, token, http.MethodPost,
			"/v1/teams/"+defaultTeamID+"/projects",
			map[string]any{"name": "esc", "kind": "goal", "docs_root": bad})
		if status != http.StatusBadRequest {
			t.Errorf("docs_root=%q create = %d; want 400 (body=%s)", bad, status, string(body))
		}
	}

	// A relative docs_root resolves under the data root and is accepted.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/projects",
		map[string]any{"name": "ok", "kind": "goal", "docs_root": "docs/proj-x"})
	if status != http.StatusCreated {
		t.Errorf("relative docs_root create = %d; want 201 (body=%s)", status, string(body))
	}
}

// F-07 backstop — a legacy row whose docs_root escapes the data root
// (inserted directly, bypassing the create-handler guard) is refused at
// read time, returning "no docs" rather than serving the file.
func TestResolveDocsRoot_RefusesLegacyEscape(t *testing.T) {
	s, token := newA2ATestServer(t)
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind, docs_root)
		VALUES ('proj-esc', ?, 'Escaped', ?, 'goal', '/etc')`,
		defaultTeamID, NowUTC()); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/projects/proj-esc/docs/passwd", nil)
	if status == http.StatusOK {
		t.Fatalf("escaped docs_root served a file (status 200): %s", string(body))
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d; want 404 (docs_root refused)", status)
	}
}

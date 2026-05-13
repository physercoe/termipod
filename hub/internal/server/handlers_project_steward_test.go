// ADR-025 W3 — ensureProjectSteward endpoint coverage.
//
// Mirrors handlers_general_steward_test.go but exercises the
// per-project variant: idempotency, archive-respawn, no-host
// failure, cross-team isolation, and the projects.steward_agent_id
// rebind. Engine launch isn't exercised — the test stops at the
// agent row + session row commit.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func seedProjectInTeamID(t *testing.T, s *Server, team, name string) string {
	t.Helper()
	id := NewID()
	if _, err := s.db.Exec(`
		INSERT INTO projects (id, team_id, name, created_at, kind)
		VALUES (?, ?, ?, ?, 'goal')`,
		id, team, name, NowUTC()); err != nil {
		t.Fatalf("seed project %q in %q: %v", name, team, err)
	}
	return id
}

func callEnsureProjectSteward(
	t *testing.T, srvURL, token, team, project, body string,
) (status int, raw []byte) {
	t.Helper()
	if body == "" {
		body = "{}"
	}
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		srvURL+"/v1/teams/"+team+"/projects/"+project+"/steward/ensure",
		bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ensure http: %v", err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, raw
}

func newProjectStewardTestServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := dir + "/hub.db"
	token, err := Init(dir, dbPath)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	s, err := New(Config{Listen: "127.0.0.1:0", DBPath: dbPath, DataRoot: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	srv := httptest.NewServer(s.router)
	t.Cleanup(srv.Close)
	return s, srv, token
}

func TestEnsureProjectSteward_FirstCallSpawns(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")

	status, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj, "")
	if status != http.StatusCreated {
		t.Fatalf("first call: want 201, got %d (%s)", status, raw)
	}
	var out ensureProjectStewardOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if out.AgentID == "" {
		t.Fatalf("missing agent_id: %s", raw)
	}
	if out.ProjectID != proj {
		t.Errorf("project_id=%q want %q", out.ProjectID, proj)
	}
	if out.AlreadyRan {
		t.Error("first call: already_running=true unexpected")
	}

	// Persisted: agent's project_id binding + kind + the
	// projects.steward_agent_id rebind.
	var (
		gotProject, gotKind, gotSteward string
	)
	row := s.db.QueryRow(`
		SELECT a.project_id, a.kind, COALESCE(p.steward_agent_id, '')
		  FROM agents a
		  JOIN projects p ON p.id = a.project_id
		 WHERE a.id = ?`, out.AgentID)
	if err := row.Scan(&gotProject, &gotKind, &gotSteward); err != nil {
		t.Fatalf("lookup spawned: %v", err)
	}
	if gotProject != proj {
		t.Errorf("agents.project_id=%q want %q", gotProject, proj)
	}
	if gotKind != projectStewardKindDefault {
		t.Errorf("agents.kind=%q want %q", gotKind, projectStewardKindDefault)
	}
	if gotSteward != out.AgentID {
		t.Errorf("projects.steward_agent_id=%q want %q", gotSteward, out.AgentID)
	}
}

func TestEnsureProjectSteward_IdempotentRepeat(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")

	_, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj, "")
	var first ensureProjectStewardOut
	_ = json.Unmarshal(raw, &first)

	status, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj, "")
	if status != http.StatusOK {
		t.Fatalf("second call: want 200, got %d (%s)", status, raw)
	}
	var second ensureProjectStewardOut
	_ = json.Unmarshal(raw, &second)
	if second.AgentID != first.AgentID {
		t.Errorf("second call returned different agent: first=%s second=%s",
			first.AgentID, second.AgentID)
	}
	if !second.AlreadyRan {
		t.Error("second call: already_running=false; want true")
	}
}

func TestEnsureProjectSteward_RespawnAfterArchive(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")

	_, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj, "")
	var first ensureProjectStewardOut
	_ = json.Unmarshal(raw, &first)

	if _, err := s.db.Exec(`
		UPDATE agents SET archived_at = ? WHERE id = ?`,
		NowUTC(), first.AgentID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	status, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj, "")
	if status != http.StatusCreated {
		t.Fatalf("respawn: want 201, got %d (%s)", status, raw)
	}
	var second ensureProjectStewardOut
	_ = json.Unmarshal(raw, &second)
	if second.AgentID == first.AgentID {
		t.Errorf("respawn returned same archived agent_id %s", second.AgentID)
	}
	if second.AlreadyRan {
		t.Error("respawn: already_running=true unexpected after archive")
	}
}

func TestEnsureProjectSteward_NoHost(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")
	// Deliberately no host registered.

	status, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj, "")
	if status != http.StatusFailedDependency {
		t.Fatalf("no-host: want 424, got %d (%s)", status, raw)
	}
	if !bytes.Contains(raw, []byte("no host")) {
		t.Errorf("no-host: expected helpful error, got: %s", raw)
	}
}

func TestEnsureProjectSteward_UnknownProject(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")

	status, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID,
		"proj-does-not-exist", "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown project: want 404, got %d (%s)", status, raw)
	}
	_ = s
}

func TestEnsureProjectSteward_CrossTeamIsolation(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")

	// Seed a foreign team + a project under it. The default-team
	// caller must NOT be able to spawn into a project that doesn't
	// belong to its team.
	if _, err := s.db.Exec(
		`INSERT INTO teams (id, name, created_at) VALUES ('other-team', 'other', ?)`,
		NowUTC()); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	foreignProj := seedProjectInTeamID(t, s, "other-team", "foreign")

	status, _ := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, foreignProj, "")
	if status != http.StatusNotFound {
		t.Errorf("cross-team: want 404, got %d", status)
	}
}

func TestEnsureProjectSteward_PinnedHost(t *testing.T) {
	s, srv, token := newProjectStewardTestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-1", "older-host")
	seedTestHost(t, s, defaultTeamID, "host-2", "newer-host")
	proj := seedProjectInTeamID(t, s, defaultTeamID, "demo")

	status, raw := callEnsureProjectSteward(t, srv.URL, token, defaultTeamID, proj,
		`{"host_id":"host-1"}`)
	if status != http.StatusCreated {
		t.Fatalf("pinned host: want 201, got %d (%s)", status, raw)
	}
	var out ensureProjectStewardOut
	_ = json.Unmarshal(raw, &out)

	var hostID string
	if err := s.db.QueryRow(
		`SELECT host_id FROM agents WHERE id = ?`, out.AgentID).Scan(&hostID); err != nil {
		t.Fatalf("lookup host_id: %v", err)
	}
	if hostID != "host-1" {
		t.Errorf("spawned on host=%q; want host-1 (pinned)", hostID)
	}
}

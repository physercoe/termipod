// handlers_general_steward_test.go — coverage for the singleton
// ensure-spawn endpoint that backs the home-tab Steward card (W4).
//
// What's verified:
//   - First call spawns a fresh general steward (201 Created).
//   - Second call returns the same instance (200 OK + already_running).
//   - Manual archive followed by another call respawns a fresh
//     instance (X.1 in run-lifecycle-demo.md).
//   - Missing host surfaces as 424 Failed Dependency (clear UX
//     instead of silent host-less spawn).
//
// Engine launch is not exercised — we don't have a host-runner here.
// The DoSpawn path runs to its DB commit and the agents row appears;
// host-side launch is host-runner's concern.

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

// callEnsureGeneralSteward POSTs to the ensure endpoint with the
// principal's bootstrap token (matches what the mobile UI does). The
// endpoint sits inside the team route, so the principal token's
// team scope authenticates.
func callEnsureGeneralSteward(t *testing.T, srvURL, token string) (status int, body []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "POST",
		srvURL+"/v1/teams/"+defaultTeamID+"/steward.general/ensure",
		bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ensure http: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, body
}

func TestEnsureGeneralSteward_FirstCallSpawns(t *testing.T) {
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

	// Need a host registered for the ensure handler to pick. seed
	// helper at handlers_a2a_test.go inserts a vanilla row.
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")

	status, raw := callEnsureGeneralSteward(t, srv.URL, token)
	if status != http.StatusCreated {
		t.Fatalf("first call: want 201, got %d (%s)", status, raw)
	}
	var out ensureGeneralStewardOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if out.AgentID == "" {
		t.Fatalf("missing agent_id in response: %s", raw)
	}
	if out.AlreadyRan {
		t.Errorf("first call: already_running=true unexpected")
	}

	// Verify the agents row was created with the right kind.
	var kind string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT kind FROM agents WHERE id = ?`, out.AgentID).Scan(&kind); err != nil {
		t.Fatalf("lookup spawned agent: %v", err)
	}
	if kind != generalStewardKind {
		t.Errorf("spawned kind=%q; want %q", kind, generalStewardKind)
	}
}

func TestEnsureGeneralSteward_IdempotentRepeat(t *testing.T) {
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
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")

	// First call → spawn.
	status, raw := callEnsureGeneralSteward(t, srv.URL, token)
	if status != http.StatusCreated {
		t.Fatalf("first call: want 201, got %d (%s)", status, raw)
	}
	var first ensureGeneralStewardOut
	_ = json.Unmarshal(raw, &first)

	// Second call → same instance.
	status, raw = callEnsureGeneralSteward(t, srv.URL, token)
	if status != http.StatusOK {
		t.Fatalf("second call: want 200, got %d (%s)", status, raw)
	}
	var second ensureGeneralStewardOut
	_ = json.Unmarshal(raw, &second)
	if second.AgentID != first.AgentID {
		t.Errorf("second call returned different agent: first=%s second=%s",
			first.AgentID, second.AgentID)
	}
	if !second.AlreadyRan {
		t.Errorf("second call: already_running=false; want true")
	}
}

func TestEnsureGeneralSteward_RespawnAfterArchive(t *testing.T) {
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
	seedTestHost(t, s, defaultTeamID, "host-1", "test-host")

	// Spawn the first instance.
	_, raw := callEnsureGeneralSteward(t, srv.URL, token)
	var first ensureGeneralStewardOut
	_ = json.Unmarshal(raw, &first)

	// Simulate the director archiving the steward. The director-
	// path uses /agents/{id} DELETE, but we side-step that here by
	// flipping the row to terminated directly — the test focuses on
	// the ensure-spawn lookup behaviour, not the archive handler.
	if _, err := s.db.ExecContext(context.Background(), `
		UPDATE agents SET status = 'terminated', terminated_at = ?
		WHERE id = ?`, NowUTC(), first.AgentID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Next ensure call should respawn a fresh instance — the
	// previous one is terminated.
	status, raw := callEnsureGeneralSteward(t, srv.URL, token)
	if status != http.StatusCreated {
		t.Fatalf("respawn: want 201, got %d (%s)", status, raw)
	}
	var second ensureGeneralStewardOut
	_ = json.Unmarshal(raw, &second)
	if second.AgentID == first.AgentID {
		t.Errorf("respawn returned same archived agent_id %s", second.AgentID)
	}
	if second.AlreadyRan {
		t.Errorf("respawn: already_running=true unexpected after archive")
	}
}

func TestEnsureGeneralSteward_NoHost(t *testing.T) {
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
	// Deliberately no host registered.

	status, raw := callEnsureGeneralSteward(t, srv.URL, token)
	if status != http.StatusFailedDependency {
		t.Fatalf("no-host: want 424, got %d (%s)", status, raw)
	}
	if !bytes.Contains(raw, []byte("no host")) {
		t.Errorf("no-host: expected helpful error, got: %s", raw)
	}
}

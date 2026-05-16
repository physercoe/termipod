package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/termipod/hub/internal/auth"
)

// mintNonOwnerToken issues an agent-kind bearer for the given team and
// returns the plaintext. Used to assert the owner-scope gate on admin
// endpoints — any kind != "owner" must 403.
func mintNonOwnerToken(t *testing.T, s *Server, team string) string {
	t.Helper()
	plain := auth.NewToken()
	scope := `{"team":"` + team + `","role":"agent","agent_id":"a-test"}`
	if err := auth.InsertToken(context.Background(), s.db, "agent", scope,
		plain, NewID(), NowUTC()); err != nil {
		t.Fatalf("mint non-owner token: %v", err)
	}
	return plain
}

// TestAdminFleetShutdown_StopsSessionsAndFiresVerb wires the full
// shutdown-all orchestration end-to-end: seed two live hosts, give one
// of them an active session, spin up a fake host-runner goroutine that
// long-polls the tunnel and acks `host.shutdown`, then call the admin
// endpoint and assert the per-host outcome + audit rows + sessions
// flipped to paused.
func TestAdminFleetShutdown_StopsSessionsAndFiresVerb(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-live", "live-1")
	seedTestHost(t, s, defaultTeamID, "host-stale", "stale-1")
	// Mark host-live as recently heartbeated; host-stale is left with
	// last_seen_at NULL so the live-host filter excludes it.
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE hosts SET last_seen_at = datetime('now') WHERE id = ?`,
		"host-live")
	if err != nil {
		t.Fatalf("set last_seen: %v", err)
	}

	// Seed an active session + its agent on host-live.
	ctx := context.Background()
	now := NowUTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO agents (id, team_id, handle, kind, status, host_id, pane_id, created_at)
		 VALUES (?, ?, ?, 'worker.v1', 'running', ?, ?, ?)`,
		"agent-1", defaultTeamID, "@worker", "host-live", "%pane-1", now)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, team_id, current_agent_id, status, opened_at, last_active_at)
		 VALUES (?, ?, ?, 'active', ?, ?)`,
		"sess-1", defaultTeamID, "agent-1", now, now)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// Fake host-runner: long-poll /tunnel/next, ack any host.shutdown
	// envelope. Sleeps if 204 to mimic the real loop's reconnect.
	ts := httptest.NewServer(s.router)
	defer ts.Close()
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if pollCtx.Err() != nil {
				return
			}
			pollReq, _ := http.NewRequestWithContext(pollCtx, http.MethodGet,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-live/a2a/tunnel/next?wait_ms=3000",
				nil)
			pollReq.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(pollReq)
			if err != nil {
				return
			}
			if resp.StatusCode == http.StatusNoContent {
				resp.Body.Close()
				continue
			}
			var env tunnelRequest
			_ = json.NewDecoder(resp.Body).Decode(&env)
			resp.Body.Close()
			if env.Kind != "host.shutdown" {
				continue
			}
			ack, _ := json.Marshal(map[string]any{"acked": true})
			reply := tunnelResponse{
				ReqID:   env.ReqID,
				Status:  http.StatusOK,
				Headers: map[string]string{"Content-Type": "application/json"},
				BodyB64: base64.StdEncoding.EncodeToString(ack),
			}
			body, _ := json.Marshal(reply)
			pr, _ := http.NewRequestWithContext(pollCtx, http.MethodPost,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-live/a2a/tunnel/responses",
				bytes.NewReader(body))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			pResp, _ := http.DefaultClient.Do(pr)
			if pResp != nil {
				pResp.Body.Close()
			}
			return
		}
	}()
	// Give the poller a chance to block on /next before we fire the
	// admin request.
	time.Sleep(50 * time.Millisecond)

	// Call admin endpoint with owner token.
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/fleet/shutdown",
		map[string]any{"reason": "test-shutdown"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out AdminFleetShutdownResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hosts) != 1 {
		t.Fatalf("expected 1 live host, got %d (%v)", len(out.Hosts), out.Hosts)
	}
	got := out.Hosts[0]
	if got.HostID != "host-live" {
		t.Errorf("host_id = %q, want host-live", got.HostID)
	}
	if got.SessionsStopped != 1 {
		t.Errorf("sessions_stopped = %d, want 1", got.SessionsStopped)
	}
	if !got.Acked {
		t.Errorf("acked = false (err=%q)", got.Error)
	}

	// Session flipped to paused.
	var sessionStatus string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM sessions WHERE id = ?`, "sess-1").Scan(&sessionStatus); err != nil {
		t.Fatalf("query session: %v", err)
	}
	if sessionStatus != "paused" {
		t.Errorf("session status = %q, want paused", sessionStatus)
	}
	// Agent flipped to terminated.
	var agentStatus string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM agents WHERE id = ?`, "agent-1").Scan(&agentStatus); err != nil {
		t.Fatalf("query agent: %v", err)
	}
	if agentStatus != "terminated" {
		t.Errorf("agent status = %q, want terminated", agentStatus)
	}
	// Audit rows: host.shutdown + session.stop + agent.terminate.
	rows, err := s.db.QueryContext(ctx,
		`SELECT action FROM audit_events WHERE team_id = ?`, defaultTeamID)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	wantContains := []string{"host.shutdown", "session.stop", "agent.terminate"}
	for _, want := range wantContains {
		found := false
		for _, a := range actions {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing audit action %q; got %v", want, actions)
		}
	}

	pollCancel()
	// Give the poller a brief chance to drain before the test
	// returns; don't wait forever.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

// TestAdminFleetShutdown_NoLiveHosts_NoCrash exercises the empty-fleet
// path: no hosts have heartbeated, the handler returns an empty list
// and 200 OK rather than 500ing.
func TestAdminFleetShutdown_NoLiveHosts_NoCrash(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-stale", "stale")
	// last_seen_at stays NULL so the host is not "live".

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/fleet/shutdown", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out AdminFleetShutdownResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hosts) != 0 {
		t.Errorf("hosts = %d, want 0", len(out.Hosts))
	}
}

// TestAdminFleetShutdown_NonOwnerGets403 locks the owner-scope gate.
// Any caller without an owner-kind bearer must be rejected before the
// orchestration touches the DB.
func TestAdminFleetShutdown_NonOwnerGets403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	// Issue an agent-kind token directly (bypassing the owner flow).
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, body := doReq(t, s, memberToken, http.MethodPost,
		"/v1/admin/fleet/shutdown", map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, body)
	}
	if !strings.Contains(string(body), "owner") {
		t.Errorf("body=%s, expected mention of owner gate", body)
	}
}

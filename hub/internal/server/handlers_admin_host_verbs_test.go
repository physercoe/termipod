package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeHostAcker runs a goroutine that long-polls one host's tunnel and
// acks the first control verb it sees with HTTP 200. It returns a stop
// function the test defers. Shared by the per-host control-verb tests
// so they exercise the real ack path without a host-runner.
func fakeHostAcker(t *testing.T, ts *httptest.Server, token, team, hostID string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			pollReq, _ := http.NewRequestWithContext(ctx, http.MethodGet,
				ts.URL+"/v1/teams/"+team+"/hosts/"+hostID+"/a2a/tunnel/next?wait_ms=2000",
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
			ack, _ := json.Marshal(map[string]any{"acked": true, "ok": true})
			reply := tunnelResponse{
				ReqID:   env.ReqID,
				Status:  http.StatusOK,
				Headers: map[string]string{"Content-Type": "application/json"},
				BodyB64: base64.StdEncoding.EncodeToString(ack),
			}
			b, _ := json.Marshal(reply)
			pr, _ := http.NewRequestWithContext(ctx, http.MethodPost,
				ts.URL+"/v1/teams/"+team+"/hosts/"+hostID+"/a2a/tunnel/responses",
				bytes.NewReader(b))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			if presp, _ := http.DefaultClient.Do(pr); presp != nil {
				presp.Body.Close()
			}
		}
	}()
	return func() {
		cancel()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}
}

// TestAdminHostShutdown_NonOwner403 locks the owner gate.
func TestAdminHostShutdown_NonOwner403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, _ := doReq(t, s, memberToken, http.MethodPost,
		"/v1/admin/hosts/host-x/shutdown", map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

// TestAdminHostShutdown_UnknownHost404 confirms a typo'd host id is a
// prompt 404 rather than a verb that blocks until timeout.
func TestAdminHostShutdown_UnknownHost404(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/hosts/nope/shutdown", map[string]any{})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
}

// TestAdminHostRestart_AcksViaTunnel wires a fake host-runner and
// confirms the per-host restart verb round-trips and writes an audit
// row matching the fleet path's shape.
func TestAdminHostRestart_AcksViaTunnel(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-r", "runner-1")

	ts := httptest.NewServer(s.router)
	defer ts.Close()
	stop := fakeHostAcker(t, ts, token, defaultTeamID, "host-r")
	defer stop()
	time.Sleep(50 * time.Millisecond)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/hosts/host-r/restart", map[string]any{"reason": "test"})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out AdminFleetHostResult
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Acked {
		t.Errorf("host should have acked the restart verb; result=%+v", out)
	}
	var n int
	_ = s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = 'host.restart' AND target_id = 'host-r'`).
		Scan(&n)
	if n != 1 {
		t.Errorf("host.restart audit rows = %d, want 1", n)
	}
}

// TestAdminHostUpdate_UnknownHost404 confirms the update route also
// resolves the host before firing the verb.
func TestAdminHostUpdate_UnknownHost404(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/admin/hosts/ghost/update", map[string]any{})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

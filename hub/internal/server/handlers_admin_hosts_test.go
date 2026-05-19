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

// TestAdminListHosts_ReturnsFleet seeds a live and a stale host and
// asserts GET /v1/admin/hosts reflects the heartbeat-derived liveness
// and the runner build info.
func TestAdminListHosts_ReturnsFleet(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-live", "live-1")
	seedTestHost(t, s, defaultTeamID, "host-stale", "stale-1")
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE hosts SET last_seen_at = datetime('now'), runner_commit = ?
		   WHERE id = ?`, "abc1234deadbeef", "host-live"); err != nil {
		t.Fatalf("set live: %v", err)
	}

	status, body := doReq(t, s, token, http.MethodGet, "/v1/admin/hosts", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out struct {
		Hosts []AdminHostRow `json:"hosts"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Hosts) != 2 {
		t.Fatalf("hosts = %d, want 2", len(out.Hosts))
	}
	byID := map[string]AdminHostRow{}
	for _, h := range out.Hosts {
		byID[h.HostID] = h
	}
	if !byID["host-live"].Live {
		t.Error("host-live should be live")
	}
	if byID["host-stale"].Live {
		t.Error("host-stale should not be live")
	}
	if byID["host-live"].RunnerCommit != "abc1234deadbeef" {
		t.Errorf("runner_commit = %q", byID["host-live"].RunnerCommit)
	}
}

// TestAdminListHosts_NonOwnerGets403 locks the owner-scope gate.
func TestAdminListHosts_NonOwnerGets403(t *testing.T) {
	s, _ := newA2ATestServer(t)
	memberToken := mintNonOwnerToken(t, s, defaultTeamID)
	status, body := doReq(t, s, memberToken, http.MethodGet, "/v1/admin/hosts", nil)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", status, body)
	}
}

// TestAdminHostPing_UnknownHost404 confirms a typo'd host id returns
// 404 promptly instead of blocking on a verb no host will dequeue.
func TestAdminHostPing_UnknownHost404(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/hosts/no-such-host/ping", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
}

// TestAdminHostPing_RoundTrips wires host.ping end-to-end: a live host,
// a fake host-runner that long-polls the tunnel and acks the verb with
// a build identity, then asserts the admin endpoint reports it.
func TestAdminHostPing_RoundTrips(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-live", "live-1")
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE hosts SET last_seen_at = datetime('now') WHERE id = ?`,
		"host-live"); err != nil {
		t.Fatalf("set last_seen: %v", err)
	}

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
			if env.Kind != "host.ping" {
				continue
			}
			ack, _ := json.Marshal(map[string]any{
				"ok": true, "version": "v9.9.9-test", "commit": "deadbeef",
			})
			reply := tunnelResponse{
				ReqID:   env.ReqID,
				Status:  http.StatusOK,
				Headers: map[string]string{"Content-Type": "application/json"},
				BodyB64: base64.StdEncoding.EncodeToString(ack),
			}
			b, _ := json.Marshal(reply)
			pr, _ := http.NewRequestWithContext(pollCtx, http.MethodPost,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-live/a2a/tunnel/responses",
				bytes.NewReader(b))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			pResp, _ := http.DefaultClient.Do(pr)
			if pResp != nil {
				pResp.Body.Close()
			}
			return
		}
	}()
	time.Sleep(50 * time.Millisecond)

	status, body := doReq(t, s, token, http.MethodPost,
		"/v1/admin/hosts/host-live/ping", nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var out struct {
		HostID string         `json:"host_id"`
		Ping   hostPingResult `json:"ping"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Ping.OK {
		t.Fatalf("ping ok=false: %s", out.Ping.Error)
	}
	if out.Ping.Version != "v9.9.9-test" {
		t.Errorf("version = %q, want v9.9.9-test", out.Ping.Version)
	}

	pollCancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

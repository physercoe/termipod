package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTunnel_RelayRoundTrip(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-1")

	// Spin up a "host-runner" goroutine: long-poll /next, echo any
	// request back as a 200 with a known body.
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-gpu/a2a/tunnel/next?wait_ms=5000", nil)
			if err != nil {
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
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

			reply := tunnelResponse{
				ReqID:   env.ReqID,
				Status:  http.StatusOK,
				Headers: map[string]string{"X-Echo-Path": env.Path},
				BodyB64: base64.StdEncoding.EncodeToString([]byte("hello from host-runner: " + env.Path)),
			}
			body, _ := json.Marshal(reply)
			pr, _ := http.NewRequestWithContext(ctx, http.MethodPost,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/host-gpu/a2a/tunnel/responses",
				strings.NewReader(string(body)))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			pResp, err := http.DefaultClient.Do(pr)
			if err == nil {
				pResp.Body.Close()
			}
		}
	}()

	// Public relay call — token-less — to /a2a/relay/<host>/<agent>/...
	time.Sleep(50 * time.Millisecond) // give the goroutine a chance to block on /next
	relayReq, _ := http.NewRequest(http.MethodGet,
		ts.URL+"/a2a/relay/host-gpu/agent-xyz/.well-known/agent.json?k=v", nil)
	relayResp, err := http.DefaultClient.Do(relayReq)
	if err != nil {
		t.Fatalf("relay call: %v", err)
	}
	defer relayResp.Body.Close()

	if relayResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(relayResp.Body)
		t.Fatalf("relay status=%d body=%s", relayResp.StatusCode, string(body))
	}
	if got := relayResp.Header.Get("X-Echo-Path"); got != "/a2a/agent-xyz/.well-known/agent.json" {
		t.Errorf("X-Echo-Path = %q, want /a2a/agent-xyz/.well-known/agent.json", got)
	}
	body, _ := io.ReadAll(relayResp.Body)
	if !strings.Contains(string(body), "hello from host-runner") {
		t.Errorf("body = %q, want echo", string(body))
	}

	cancel()
	wg.Wait()
}

func TestTunnel_RelayNoHostRunner_Times504(t *testing.T) {
	s, _ := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-1")

	// No tunnel goroutine: we expect 504 after the hub's internal timeout.
	// Override the relay timeout for the test by calling the manager
	// directly with a short context.
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	req := &tunnelRequest{
		ReqID:  "test-req",
		Method: http.MethodGet,
		Path:   "/a2a/agent-x/hello",
	}
	_, err := s.tunnel.enqueueAndWait(ctx, "host-gpu", req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTunnel_Next_CrossTeam_404(t *testing.T) {
	s, token := newA2ATestServer(t)
	// Seed host in a different team.
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"other-team", "other", NowUTC())
	if err != nil {
		t.Fatalf("seed team: %v", err)
	}
	seedTestHost(t, s, "other-team", "host-x", "other-host")

	req := httptest.NewRequest(http.MethodGet,
		"/v1/teams/"+defaultTeamID+"/hosts/host-x/a2a/tunnel/next?wait_ms=100", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404 body=%s", rr.Code, rr.Body.String())
	}
}

func TestTunnel_Response_UnknownReqID_Gone(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-1")

	status, _ := doReq(t, s, token, http.MethodPost,
		"/v1/teams/"+defaultTeamID+"/hosts/host-gpu/a2a/tunnel/responses",
		tunnelResponse{
			ReqID:  "bogus-id",
			Status: 200,
		})
	if status != http.StatusGone {
		t.Errorf("status=%d want 410", status)
	}
}

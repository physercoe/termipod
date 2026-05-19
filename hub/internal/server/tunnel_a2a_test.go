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
	"unicode/utf8"
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

// TestTunnel_RelayTwoHosts_RoutesPerPath is the P3.4 cross-host A2A
// smoke: two host-runners are long-polling /tunnel/next on the same hub
// concurrently, each for a distinct host id. A relay call to
// /a2a/relay/{host}/{agent}/... must reach the correct host based on
// the path segment — a call for host-A must not fan out to host-B's
// tunnel queue. This verifies the core multi-host invariant the demo
// depends on (steward on host-A invokes worker on host-B via the hub).
func TestTunnel_RelayTwoHosts_RoutesPerPath(t *testing.T) {
	s, token := newA2ATestServer(t)
	seedTestHost(t, s, defaultTeamID, "host-cpu", "cpu-vps")
	seedTestHost(t, s, defaultTeamID, "host-gpu", "gpu-worker")

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Counters so the test can assert each tunnel got its expected
	// number of requests and zero cross-talk.
	var cpuCount, gpuCount int
	var countMu sync.Mutex

	runTunnel := func(host, label string, counter *int) {
		for {
			if ctx.Err() != nil {
				return
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/"+host+"/a2a/tunnel/next?wait_ms=2000", nil)
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

			countMu.Lock()
			*counter++
			countMu.Unlock()

			reply := tunnelResponse{
				ReqID:   env.ReqID,
				Status:  http.StatusOK,
				Headers: map[string]string{"X-Served-By": label},
				BodyB64: base64.StdEncoding.EncodeToString([]byte("served-by:" + label + " path:" + env.Path)),
			}
			body, _ := json.Marshal(reply)
			pr, _ := http.NewRequestWithContext(ctx, http.MethodPost,
				ts.URL+"/v1/teams/"+defaultTeamID+"/hosts/"+host+"/a2a/tunnel/responses",
				strings.NewReader(string(body)))
			pr.Header.Set("Authorization", "Bearer "+token)
			pr.Header.Set("Content-Type", "application/json")
			if pResp, err := http.DefaultClient.Do(pr); err == nil {
				pResp.Body.Close()
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); runTunnel("host-cpu", "cpu-vps", &cpuCount) }()
	go func() { defer wg.Done(); runTunnel("host-gpu", "gpu-worker", &gpuCount) }()

	// Give both goroutines time to be blocked on /next.
	time.Sleep(100 * time.Millisecond)

	// Call relay for host-cpu — must land on the cpu goroutine.
	r1, err := http.Get(ts.URL + "/a2a/relay/host-cpu/agent-steward/hello")
	if err != nil {
		t.Fatalf("relay host-cpu: %v", err)
	}
	defer r1.Body.Close()
	if r1.Header.Get("X-Served-By") != "cpu-vps" {
		t.Errorf("host-cpu served by %q, want cpu-vps", r1.Header.Get("X-Served-By"))
	}
	b1, _ := io.ReadAll(r1.Body)
	if !strings.Contains(string(b1), "served-by:cpu-vps path:/a2a/agent-steward/hello") {
		t.Errorf("host-cpu body = %q, want cpu-vps echo", string(b1))
	}

	// Call relay for host-gpu — must land on the gpu goroutine.
	r2, err := http.Get(ts.URL + "/a2a/relay/host-gpu/agent-worker/train")
	if err != nil {
		t.Fatalf("relay host-gpu: %v", err)
	}
	defer r2.Body.Close()
	if r2.Header.Get("X-Served-By") != "gpu-worker" {
		t.Errorf("host-gpu served by %q, want gpu-worker", r2.Header.Get("X-Served-By"))
	}
	b2, _ := io.ReadAll(r2.Body)
	if !strings.Contains(string(b2), "served-by:gpu-worker path:/a2a/agent-worker/train") {
		t.Errorf("host-gpu body = %q, want gpu-worker echo", string(b2))
	}

	// A second call to host-cpu must still land on cpu only. This is the
	// anti-fanout check: host-gpu's counter must not advance.
	r3, err := http.Get(ts.URL + "/a2a/relay/host-cpu/agent-steward/ping")
	if err != nil {
		t.Fatalf("relay host-cpu 2: %v", err)
	}
	r3.Body.Close()

	countMu.Lock()
	gotCPU, gotGPU := cpuCount, gpuCount
	countMu.Unlock()
	if gotCPU != 2 {
		t.Errorf("cpu tunnel served %d requests, want 2", gotCPU)
	}
	if gotGPU != 1 {
		t.Errorf("gpu tunnel served %d requests, want 1 (no cross-talk)", gotGPU)
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

func TestPreviewA2ABody(t *testing.T) {
	// Happy path: JSON-RPC message/send envelope with a text part.
	env := []byte(`{"jsonrpc":"2.0","id":"x","method":"message/send","params":{"message":{"messageId":"m1","role":"user","parts":[{"kind":"text","text":"hello world"}]}}}`)
	if got := previewA2ABody(env); got != "hello world" {
		t.Errorf("preview: got %q, want %q", got, "hello world")
	}
	// Truncation at 200 chars.
	long := strings.Repeat("a", 250)
	env2 := []byte(`{"params":{"message":{"parts":[{"kind":"text","text":"` + long + `"}]}}}`)
	got := previewA2ABody(env2)
	// 200 truncated runes + the ellipsis rune — count runes, not
	// bytes ("…" is 3 UTF-8 bytes).
	if utf8.RuneCountInString(got) != 201 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncation: runes=%d suffix=%q", utf8.RuneCountInString(got), got[max(0, len(got)-5):])
	}
	// Empty / malformed inputs return "".
	if previewA2ABody(nil) != "" {
		t.Error("nil should return empty")
	}
	if previewA2ABody([]byte(`{not json`)) != "" {
		t.Error("malformed JSON should return empty")
	}
	// No text part returns "".
	envNoText := []byte(`{"params":{"message":{"parts":[{"kind":"image","text":"unused"}]}}}`)
	if previewA2ABody(envNoText) != "" {
		t.Error("non-text parts should return empty")
	}
}

// ADR-032: the A2A relay stamps the orchestration-envelope provenance
// into the body's message.metadata.termipod bag before forwarding, so
// the recipient host-runner composes the message envelope. This replaces
// the v1.0.630 `[A2A from @sender]` text-prefix decoration.

// termipodMetaBag parses the stamped termipod bag out of a relayed
// message/send body for assertion.
func termipodMetaBag(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env struct {
		Params struct {
			Message struct {
				Metadata struct {
					Termipod map[string]any `json:"termipod"`
				} `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("parse stamped body: %v", err)
	}
	return env.Params.Message.Metadata.Termipod
}

func TestStampA2AEnvelopeMeta(t *testing.T) {
	// Body already carries the sender-declared kind (from a2a.invoke);
	// the relay must add the resolved sender and preserve the kind.
	body := []byte(`{"jsonrpc":"2.0","id":"x","method":"message/send","params":{"message":{"messageId":"m1","role":"user","parts":[{"kind":"text","text":"please review the memo"}],"metadata":{"termipod":{"kind":"directive"}}}}}`)
	out := stampA2AEnvelopeMeta(body, "research-steward", RolePeerSteward)
	tp := termipodMetaBag(t, out)
	if tp["from_handle"] != "research-steward" {
		t.Errorf("from_handle = %v, want research-steward", tp["from_handle"])
	}
	if tp["from_role"] != RolePeerSteward {
		t.Errorf("from_role = %v, want %s", tp["from_role"], RolePeerSteward)
	}
	if tp["kind"] != "directive" {
		t.Errorf("kind = %v, want directive (sender-declared, preserved)", tp["kind"])
	}
	if !strings.Contains(string(out), "please review the memo") {
		t.Errorf("stamped body lost original text: %s", out)
	}
}

// Non-message/send methods (tasks/get, …) pass through unchanged.
func TestStampA2AEnvelopeMeta_OtherMethods(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"x","method":"tasks/get","params":{"id":"t1"}}`)
	if out := stampA2AEnvelopeMeta(body, "steward", RolePeerSteward); string(out) != string(body) {
		t.Errorf("tasks/get body must pass through unchanged; got %s", out)
	}
}

// Empty sender handle = no stamp (legacy unauthed peer path).
func TestStampA2AEnvelopeMeta_EmptyHandle(t *testing.T) {
	body := []byte(`{"method":"message/send","params":{"message":{"parts":[{"kind":"text","text":"hi"}]}}}`)
	if out := stampA2AEnvelopeMeta(body, "", RolePeerWorker); string(out) != string(body) {
		t.Errorf("empty handle must pass through; got %s", out)
	}
}

// Malformed envelope: pass through unchanged (best-effort).
func TestStampA2AEnvelopeMeta_Malformed(t *testing.T) {
	body := []byte(`not valid json`)
	if out := stampA2AEnvelopeMeta(body, "steward", RolePeerSteward); string(out) != string(body) {
		t.Errorf("malformed body must pass through; got %s", out)
	}
}

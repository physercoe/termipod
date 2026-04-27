package hostrunner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// HTTP round-trip: GET /v1/_info goes through the proxy, headers and
// body land on the upstream unchanged. The masking guarantee is the
// load-bearing one — if request headers don't survive, agents lose
// auth and the steward dies on the first MCP call.
func TestEgressProxy_PassesHTTP(t *testing.T) {
	var sawAuth string
	var sawPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	ep, err := startEgressProxy(context.Background(), "127.0.0.1:0",
		upstream.URL, slog.Default())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = ep.shutdown(ctx)
	}()

	req, _ := http.NewRequest(http.MethodGet, ep.LocalURL+"/v1/_info", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d; want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q; want {\"ok\":true}", body)
	}
	if sawAuth != "Bearer test-token" {
		t.Errorf("upstream saw Authorization = %q; want bearer", sawAuth)
	}
	if sawPath != "/v1/_info" {
		t.Errorf("upstream saw path = %q; want /v1/_info", sawPath)
	}
}

// SSE pass-through: frames must flush as they're written, not buffer
// until response close. The test server writes one frame, sleeps, then
// writes another; the proxy must hand the first frame to the reader
// before the second arrives. Without FlushInterval=-1 this would hang.
func TestEgressProxy_StreamsSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("test server: response writer doesn't support Flusher")
			return
		}
		_, _ = fmt.Fprintf(w, "data: first\n\n")
		flusher.Flush()
		time.Sleep(150 * time.Millisecond)
		_, _ = fmt.Fprintf(w, "data: second\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	ep, err := startEgressProxy(context.Background(), "127.0.0.1:0",
		upstream.URL, slog.Default())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = ep.shutdown(ctx)
	}()

	req, _ := http.NewRequest(http.MethodGet, ep.LocalURL+"/v1/teams/t/agents/a/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	// Read with a deadline-bounded scanner so a buffering bug shows
	// up as a clean test failure rather than hanging forever.
	type frameOrErr struct {
		frame string
		err   error
	}
	ch := make(chan frameOrErr, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				ch <- frameOrErr{frame: strings.TrimPrefix(line, "data: ")}
				return
			}
		}
		ch <- frameOrErr{err: io.EOF}
	}()

	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read first frame: %v", got.err)
		}
		if got.frame != "first" {
			t.Errorf("first frame = %q; want first", got.frame)
		}
	case <-time.After(120 * time.Millisecond):
		t.Fatal("first SSE frame not delivered within 120ms — proxy is buffering (FlushInterval=-1 not set?)")
	}
}

// Disabled when EgressProxyAddr is empty. startEgressProxy returns
// (nil, nil); callers should treat nil as "fall back to the real
// hub URL", and the runner does so via its egressProxy nil-check.
func TestEgressProxy_DisabledWhenAddrEmpty(t *testing.T) {
	ep, err := startEgressProxy(context.Background(), "",
		"http://example.invalid", slog.Default())
	if err != nil {
		t.Fatalf("disabled-mode start: %v", err)
	}
	if ep != nil {
		t.Fatalf("expected nil egressProxy when addr is empty; got %+v", ep)
	}
}

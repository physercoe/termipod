package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeTunnelClient struct {
	mu       sync.Mutex
	queue    []*TunnelEnvelope
	delivered []*TunnelResponseEnvelope
	noMore   atomic.Bool
}

func (f *fakeTunnelClient) NextTunnelRequest(ctx context.Context, hostID string, waitMs int) (*TunnelEnvelope, error) {
	for {
		f.mu.Lock()
		if len(f.queue) > 0 {
			next := f.queue[0]
			f.queue = f.queue[1:]
			f.mu.Unlock()
			return next, nil
		}
		f.mu.Unlock()
		if f.noMore.Load() {
			return nil, errors.New("stream closed")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (f *fakeTunnelClient) PostTunnelResponse(ctx context.Context, hostID string, env *TunnelResponseEnvelope) error {
	f.mu.Lock()
	f.delivered = append(f.delivered, env)
	f.mu.Unlock()
	return nil
}

func TestRunTunnel_DispatchesToLocalHandler(t *testing.T) {
	// Local handler echoes the path into the response body.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen", r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("saw " + r.URL.Path + "?" + r.URL.RawQuery))
	})

	cli := &fakeTunnelClient{
		queue: []*TunnelEnvelope{
			{
				ReqID:    "r1",
				Method:   http.MethodGet,
				Path:     "/a2a/agent-1/.well-known/agent.json",
				RawQuery: "k=v",
				BodyB64:  base64.StdEncoding.EncodeToString(nil),
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunTunnel(ctx, cli, "host-x", handler, nil, "", nil)
		close(done)
	}()

	// Poll for delivery.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		cli.mu.Lock()
		n := len(cli.delivered)
		cli.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cli.mu.Lock()
	got := cli.delivered
	cli.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("delivered = %d, want 1", len(got))
	}
	if got[0].Status != http.StatusTeapot {
		t.Errorf("status = %d, want 418", got[0].Status)
	}
	if got[0].Headers["X-Seen"] != "/a2a/agent-1/.well-known/agent.json" {
		t.Errorf("X-Seen = %q", got[0].Headers["X-Seen"])
	}
	body, _ := base64.StdEncoding.DecodeString(got[0].BodyB64)
	if string(body) != "saw /a2a/agent-1/.well-known/agent.json?k=v" {
		t.Errorf("body = %q", string(body))
	}

	cancel()
	<-done
}

// runTunnelOnce drains a single envelope through RunTunnel and returns
// the delivered response. Used by the kind-routing tests below.
func runTunnelOnce(t *testing.T, env *TunnelEnvelope, handler http.Handler, verbs HostVerbHandler, hostVersion string) *TunnelResponseEnvelope {
	t.Helper()
	cli := &fakeTunnelClient{queue: []*TunnelEnvelope{env}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunTunnel(ctx, cli, "host-x", handler, verbs, hostVersion, nil)
		close(done)
	}()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		cli.mu.Lock()
		n := len(cli.delivered)
		cli.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done
	cli.mu.Lock()
	defer cli.mu.Unlock()
	if len(cli.delivered) == 0 {
		t.Fatalf("no response delivered")
	}
	return cli.delivered[0]
}

func TestRunTunnel_KindA2A_RoutesToHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("relay-ok"))
	})
	verbs := HostVerbHandler(func(ctx context.Context, env *TunnelEnvelope) *TunnelResponseEnvelope {
		t.Errorf("verb handler should not run for kind=a2a")
		return nil
	})
	got := runTunnelOnce(t,
		&TunnelEnvelope{
			ReqID:   "r-a2a",
			Kind:    "a2a",
			Method:  http.MethodGet,
			Path:    "/a2a/agent-1/.well-known/agent.json",
			BodyB64: base64.StdEncoding.EncodeToString(nil),
		},
		handler, verbs, "v0.0.0")
	if got.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.Status)
	}
	body, _ := base64.StdEncoding.DecodeString(got.BodyB64)
	if string(body) != "relay-ok" {
		t.Fatalf("body = %q, want relay-ok", string(body))
	}
}

func TestRunTunnel_KindEmpty_RoutesToHandler(t *testing.T) {
	// Backcompat: absent Kind still reads as a2a.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	got := runTunnelOnce(t,
		&TunnelEnvelope{
			ReqID:   "r-empty",
			Method:  http.MethodGet,
			Path:    "/a2a/agent-1/foo",
			BodyB64: base64.StdEncoding.EncodeToString(nil),
		},
		handler, nil, "v0.0.0")
	if got.Status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", got.Status)
	}
}

func TestRunTunnel_KindHostVerb_RoutesToVerbHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a2a handler should not run for kind=host.*")
	})
	called := false
	verbs := HostVerbHandler(func(ctx context.Context, env *TunnelEnvelope) *TunnelResponseEnvelope {
		called = true
		if env.Kind != "host.example" {
			t.Errorf("env.Kind = %q, want host.example", env.Kind)
		}
		return &TunnelResponseEnvelope{
			ReqID:   env.ReqID,
			Status:  http.StatusOK,
			BodyB64: base64.StdEncoding.EncodeToString([]byte("verb-ok")),
		}
	})
	got := runTunnelOnce(t,
		&TunnelEnvelope{
			ReqID: "r-verb",
			Kind:  "host.example",
		},
		handler, verbs, "v9.9.9")
	if !called {
		t.Fatalf("verb handler not invoked")
	}
	if got.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.Status)
	}
	body, _ := base64.StdEncoding.DecodeString(got.BodyB64)
	if string(body) != "verb-ok" {
		t.Fatalf("body = %q, want verb-ok", string(body))
	}
}

func TestRunTunnel_UnknownVerb_TypedError(t *testing.T) {
	// Both nil HostVerbHandler and handler-returns-nil paths should
	// produce the canonical unknown_verb response with host_version.
	cases := []struct {
		name  string
		verbs HostVerbHandler
	}{
		{"nil_handler", nil},
		{"handler_returns_nil", func(ctx context.Context, env *TunnelEnvelope) *TunnelResponseEnvelope {
			return nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTunnelOnce(t,
				&TunnelEnvelope{ReqID: "r-unk", Kind: "host.does_not_exist"},
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
				tc.verbs, "1.0.610-alpha")
			if got.Status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", got.Status)
			}
			body, _ := base64.StdEncoding.DecodeString(got.BodyB64)
			var parsed struct {
				Error       string `json:"error"`
				Verb        string `json:"verb"`
				HostVersion string `json:"host_version"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("parse body %q: %v", string(body), err)
			}
			if parsed.Error != "unknown_verb" {
				t.Errorf("error = %q, want unknown_verb", parsed.Error)
			}
			if parsed.Verb != "does_not_exist" {
				t.Errorf("verb = %q, want does_not_exist", parsed.Verb)
			}
			if parsed.HostVersion != "1.0.610-alpha" {
				t.Errorf("host_version = %q, want 1.0.610-alpha", parsed.HostVersion)
			}
			if ct := got.Headers["Content-Type"]; ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

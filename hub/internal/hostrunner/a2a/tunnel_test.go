package a2a

import (
	"context"
	"encoding/base64"
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
		RunTunnel(ctx, cli, "host-x", handler, nil)
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

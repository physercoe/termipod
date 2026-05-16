package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termipod/hub/internal/hostrunner/a2a"
)

// TestHandleHostVerb_UnknownVerb_ReturnsNil pins the contract that
// unrecognised host.* verbs short-circuit with nil so the tunnel loop
// emits the canonical unknown_verb envelope. The hub-side error shape
// is asserted in a2a/tunnel_test.go.
func TestHandleHostVerb_UnknownVerb_ReturnsNil(t *testing.T) {
	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	got := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r1",
		Kind:  "host.does_not_exist",
	})
	if got != nil {
		t.Fatalf("unknown verb should return nil; got %+v", got)
	}
}

// TestHandleHostShutdown_AcksAndExits drives the full host.shutdown
// path with stubbed exit so the test doesn't terminate the process.
// Asserts: response is 200 with acked body, exit fires with code 0,
// and the exit happens AFTER the response is constructed (the
// goroutine sleep is squashed to ~0 here).
func TestHandleHostShutdown_AcksAndExits(t *testing.T) {
	prevExit, prevDelay := shutdownExit, shutdownExitDelay
	t.Cleanup(func() {
		shutdownExit = prevExit
		shutdownExitDelay = prevDelay
	})
	var exited atomic.Int32
	var gotCode atomic.Int32
	gotCode.Store(-1)
	exitCh := make(chan struct{}, 1)
	shutdownExit = func(code int) {
		gotCode.Store(int32(code))
		exited.Add(1)
		exitCh <- struct{}{}
	}
	shutdownExitDelay = 1 * time.Millisecond

	r := &Runner{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		drivers: map[string]Driver{},
	}
	payload, _ := json.Marshal(map[string]any{
		"reason":     "test-update",
		"force_kill": false,
	})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID:   "r-shutdown",
		Kind:    "host.shutdown",
		Payload: payload,
	})
	if resp == nil {
		t.Fatal("expected response envelope, got nil")
	}
	if resp.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		Acked             bool   `json:"acked"`
		StragglersStopped int    `json:"stragglers_stopped"`
		Reason            string `json:"reason"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse body %q: %v", string(body), err)
	}
	if !parsed.Acked {
		t.Errorf("acked = false")
	}
	if parsed.Reason != "test-update" {
		t.Errorf("reason = %q, want test-update", parsed.Reason)
	}
	// The handler scheduled exit on a goroutine; wait for it.
	select {
	case <-exitCh:
	case <-time.After(time.Second):
		t.Fatal("shutdownExit was not called")
	}
	if gotCode.Load() != 0 {
		t.Errorf("exit code = %d, want 0", gotCode.Load())
	}
}

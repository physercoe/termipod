package hostrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/termipod/hub/internal/hostrunner/a2a"
	"github.com/termipod/hub/internal/selfupdate"
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
	prevExit, prevDelay := verbExit, verbExitDelay
	t.Cleanup(func() {
		verbExit = prevExit
		verbExitDelay = prevDelay
	})
	var exited atomic.Int32
	var gotCode atomic.Int32
	gotCode.Store(-1)
	exitCh := make(chan struct{}, 1)
	verbExit = func(code int) {
		gotCode.Store(int32(code))
		exited.Add(1)
		exitCh <- struct{}{}
	}
	verbExitDelay = 1 * time.Millisecond

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
		t.Fatal("verbExit was not called")
	}
	if gotCode.Load() != 0 {
		t.Errorf("exit code = %d, want 0", gotCode.Load())
	}
}

// TestHandleHostUpdate_SuccessExits75 stubs the self-update routine to
// succeed and asserts the verb acks ok and schedules exit 75 so the
// supervisor respawns with the new binary.
func TestHandleHostUpdate_SuccessExits75(t *testing.T) {
	prevExit, prevDelay, prevSU := verbExit, verbExitDelay, runSelfUpdate
	t.Cleanup(func() {
		verbExit, verbExitDelay, runSelfUpdate = prevExit, prevDelay, prevSU
	})
	exitCh := make(chan int, 1)
	verbExit = func(code int) { exitCh <- code }
	verbExitDelay = 1 * time.Millisecond
	runSelfUpdate = func(_ context.Context, opt selfupdate.Options) (*selfupdate.Result, error) {
		if opt.Binary != "host-runner" {
			t.Errorf("Binary = %q, want host-runner", opt.Binary)
		}
		return &selfupdate.Result{
			Binary: "host-runner", FromVersion: "v1.0.0", ToVersion: "v1.0.1",
			Asset: "termipod-host-runner-v1.0.1-linux-amd64.tar.gz",
		}, nil
	}

	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	payload, _ := json.Marshal(map[string]any{"version": "v1.0.1", "reason": "update-all"})
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-update", Kind: "host.update", Payload: payload,
	})
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v, want 200", resp)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		OK        bool   `json:"ok"`
		ToVersion string `json:"to_version"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse body %q: %v", string(body), err)
	}
	if !parsed.OK || parsed.ToVersion != "v1.0.1" {
		t.Errorf("body = %+v, want ok + to_version v1.0.1", parsed)
	}
	select {
	case code := <-exitCh:
		if code != 75 {
			t.Errorf("exit code = %d, want 75", code)
		}
	case <-time.After(time.Second):
		t.Fatal("verbExit was not called")
	}
}

// TestHandleHostUpdate_FailureStaysUp stubs a self-update failure and
// asserts the verb returns 500 and does NOT exit — the host keeps
// running on the old binary.
func TestHandleHostUpdate_FailureStaysUp(t *testing.T) {
	prevExit, prevSU := verbExit, runSelfUpdate
	t.Cleanup(func() { verbExit, runSelfUpdate = prevExit, prevSU })
	verbExit = func(code int) { t.Fatalf("verbExit(%d) called on the failure path", code) }
	runSelfUpdate = func(_ context.Context, _ selfupdate.Options) (*selfupdate.Result, error) {
		return nil, errors.New("sha256 mismatch")
	}

	r := &Runner{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resp := r.handleHostVerb(context.Background(), &a2a.TunnelEnvelope{
		ReqID: "r-update-fail", Kind: "host.update",
	})
	if resp == nil || resp.Status != http.StatusInternalServerError {
		t.Fatalf("resp = %+v, want 500", resp)
	}
	body, _ := base64.StdEncoding.DecodeString(resp.BodyB64)
	var parsed struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	if parsed.OK || parsed.Error == "" {
		t.Errorf("body = %+v, want ok=false with an error", parsed)
	}
}

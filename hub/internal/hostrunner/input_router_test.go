package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeInputLister returns a canned slice the first call, then empty on
// subsequent polls. calls tracks how often the router hit the API so a
// test can assert polling actually happens.
type fakeInputLister struct {
	mu    sync.Mutex
	first []AgentEvent
	rest  []AgentEvent
	calls atomic.Int64
}

func (f *fakeInputLister) ListAgentEvents(_ context.Context, _ string, since int64, _ int) ([]AgentEvent, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	var src []AgentEvent
	if since == 0 {
		src = f.first
	} else {
		src = f.rest
	}
	out := make([]AgentEvent, 0, len(src))
	for _, ev := range src {
		if ev.Seq > since {
			out = append(out, ev)
		}
	}
	return out, nil
}

// capturingInputter records every Input call so tests can assert kind/
// payload routing. Thread-safe because the router polls from its own
// goroutine.
type capturingInputter struct {
	mu    sync.Mutex
	calls []inputCall
	err   error
}

type inputCall struct {
	kind    string
	payload map[string]any
}

func (c *capturingInputter) Input(_ context.Context, kind string, payload map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, inputCall{kind, payload})
	return c.err
}

func (c *capturingInputter) snapshot() []inputCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]inputCall, len(c.calls))
	copy(out, c.calls)
	return out
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func ev(seq int64, producer, kind, payloadJSON string) AgentEvent {
	return AgentEvent{
		ID:       "e-" + kind,
		AgentID:  "agent-1",
		Seq:      seq,
		Kind:     kind,
		Producer: producer,
		Payload:  json.RawMessage(payloadJSON),
	}
}

// TestInputRouter_DispatchesUserEvents: the router strips the "input."
// prefix, decodes the payload, and calls Input on the driver. Non-user
// events and non-input kinds are ignored.
func TestInputRouter_DispatchesUserEvents(t *testing.T) {
	lister := &fakeInputLister{
		first: []AgentEvent{
			ev(1, "agent", "text", `{"text":"hello from agent"}`),
			ev(2, "user", "input.text", `{"body":"run tests"}`),
			ev(3, "user", "input.cancel", `{"reason":"too slow"}`),
			ev(4, "system", "lifecycle", `{"phase":"started"}`),
			ev(5, "user", "input.approval", `{"request_id":"t1","decision":"allow"}`),
		},
	}
	drv := &capturingInputter{}
	r := NewInputRouter(lister, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Attach(ctx, "agent-1", drv, 0)

	// Wait until the three expected calls land or we time out.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(drv.snapshot()) >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.Detach("agent-1")

	calls := drv.snapshot()
	if len(calls) != 3 {
		t.Fatalf("want 3 Input calls; got %d (%+v)", len(calls), calls)
	}
	if calls[0].kind != "text" || calls[0].payload["body"] != "run tests" {
		t.Fatalf("calls[0] wrong: %+v", calls[0])
	}
	if calls[1].kind != "cancel" || calls[1].payload["reason"] != "too slow" {
		t.Fatalf("calls[1] wrong: %+v", calls[1])
	}
	if calls[2].kind != "approval" || calls[2].payload["request_id"] != "t1" {
		t.Fatalf("calls[2] wrong: %+v", calls[2])
	}
}

// TestInputRouter_AdvancesSeqOnError: if Input returns an error, the
// router must still advance its seq cursor so it doesn't retry the same
// failing event forever. (Logged as a warn so the operator can see it.)
func TestInputRouter_AdvancesSeqOnError(t *testing.T) {
	lister := &fakeInputLister{
		first: []AgentEvent{
			ev(1, "user", "input.text", `{"body":"boom"}`),
		},
		rest: []AgentEvent{
			ev(2, "user", "input.text", `{"body":"after error"}`),
		},
	}
	drv := &capturingInputter{err: &fakeErr{}}
	r := NewInputRouter(lister, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Attach(ctx, "agent-err", drv, 0)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(drv.snapshot()) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.Detach("agent-err")

	calls := drv.snapshot()
	if len(calls) < 2 {
		t.Fatalf("want at least 2 calls (seq advanced past error); got %d", len(calls))
	}
	if calls[0].payload["body"] != "boom" || calls[1].payload["body"] != "after error" {
		t.Fatalf("events out of order: %+v", calls)
	}
}

// TestInputRouter_Detach stops dispatch and waits for the goroutine.
func TestInputRouter_Detach(t *testing.T) {
	lister := &fakeInputLister{first: []AgentEvent{}}
	drv := &capturingInputter{}
	r := NewInputRouter(lister, silentLogger())
	r.Attach(context.Background(), "a", drv, 0)
	r.Detach("a")
	// Detaching an unknown agent must not panic or block.
	r.Detach("never-attached")
}

// TestInputRouter_ReattachCarriesSeq: re-attaching the same agent must
// keep the seq cursor so events the prior loop dispatched aren't
// redelivered.
func TestInputRouter_ReattachCarriesSeq(t *testing.T) {
	lister := &fakeInputLister{
		first: []AgentEvent{ev(1, "user", "input.text", `{"body":"first"}`)},
		rest:  []AgentEvent{ev(2, "user", "input.text", `{"body":"second"}`)},
	}
	drv1 := &capturingInputter{}
	r := NewInputRouter(lister, silentLogger())
	r.Attach(context.Background(), "agent-re", drv1, 0)

	// Wait for the first delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(drv1.snapshot()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Replace the driver. The router must not redeliver seq=1.
	drv2 := &capturingInputter{}
	r.Attach(context.Background(), "agent-re", drv2, 0)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(drv2.snapshot()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.Detach("agent-re")

	calls := drv2.snapshot()
	if len(calls) < 1 {
		t.Fatalf("want at least 1 call on drv2; got %d", len(calls))
	}
	for _, c := range calls {
		if c.payload["body"] == "first" {
			t.Fatalf("drv2 received seq=1 event that drv1 already saw: %+v", c)
		}
	}
}

// TestInputRouter_StopAll torches every attached loop.
func TestInputRouter_StopAll(t *testing.T) {
	lister := &fakeInputLister{}
	r := NewInputRouter(lister, silentLogger())
	r.Attach(context.Background(), "a1", &capturingInputter{}, 0)
	r.Attach(context.Background(), "a2", &capturingInputter{}, 0)
	r.StopAll()
}

// TestInputRouter_BadPayloadSkipped: a malformed JSON payload shouldn't
// crash the loop; the event is logged and skipped but the cursor still
// advances so we don't re-read it.
func TestInputRouter_BadPayloadSkipped(t *testing.T) {
	lister := &fakeInputLister{
		first: []AgentEvent{
			{Seq: 1, Producer: "user", Kind: "input.text", Payload: json.RawMessage(`not json`)},
			ev(2, "user", "input.text", `{"body":"still works"}`),
		},
	}
	drv := &capturingInputter{}
	r := NewInputRouter(lister, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Attach(ctx, "agent-bad", drv, 0)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(drv.snapshot()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.Detach("agent-bad")

	calls := drv.snapshot()
	if len(calls) != 1 || calls[0].payload["body"] != "still works" {
		t.Fatalf("want single call body=still works; got %+v", calls)
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "fake dispatch error" }

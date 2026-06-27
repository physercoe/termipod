package hostrunner

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// wedgedInputter parks every Input call until release is closed and, crucially,
// IGNORES ctx — mirroring StdioDriver.Input, whose stdin Write isn't preempted
// by a context cancel. That's the case that would deadlock Detach if the drain
// weren't bounded (#77.2).
type wedgedInputter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	done    chan struct{}
}

func (b *wedgedInputter) Input(_ context.Context, _ string, _ map[string]any) error {
	b.once.Do(func() { close(b.started) })
	<-b.release
	close(b.done)
	return nil
}

// TestInputRouter_DetachBoundedByDispatchDrain pins #77.2: Detach now waits for
// the per-event dispatch goroutines to drain (so they don't outlive teardown),
// but the wait is BOUNDED — a dispatch wedged in a ctx-ignoring driver.Input
// must not hang the stop path (which would deadlock, since stopDriver only
// closes the transport that unblocks it AFTER Detach returns).
func TestInputRouter_DetachBoundedByDispatchDrain(t *testing.T) {
	defer swapDrainTimeout(&inputDispatchDrainTimeout, 150*time.Millisecond)()

	lister := &fakeInputLister{first: []AgentEvent{{
		Seq: 1, Producer: "user", Kind: "input.answer",
		Payload: json.RawMessage(`{"body":"hi"}`),
	}}}
	r := NewInputRouter(lister, silentLogger())
	b := &wedgedInputter{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	r.Attach(context.Background(), "a1", b, 0)

	select {
	case <-b.started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch never started")
	}

	// Detach must return within ~the drain bound even though Input is wedged.
	detached := make(chan struct{})
	go func() { r.Detach("a1"); close(detached) }()
	select {
	case <-detached:
	case <-time.After(inputDispatchDrainTimeout + 2*time.Second):
		t.Fatal("Detach hung past the dispatch drain bound (#77.2 deadlock)")
	}

	// Release the straggler; it must complete (no leak) once unblocked, the way
	// the real driver's Stop() unblocks it right after Detach.
	close(b.release)
	select {
	case <-b.done:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch goroutine leaked after release")
	}
}

// TestInputRouter_DetachWaitsForFastDispatch pins the normal-path guarantee:
// Detach does not return until an in-flight dispatch finishes (#77.2). The
// dispatch signals it has started, then does measurable work; Detach is called
// while it's mid-flight, and must have waited for completion when it returns.
// Without the drain, Detach would return during the work and `completed` would
// still be false.
func TestInputRouter_DetachWaitsForFastDispatch(t *testing.T) {
	started := make(chan struct{})
	var completed atomic.Bool
	lister := &fakeInputLister{first: []AgentEvent{{
		Seq: 1, Producer: "user", Kind: "input.answer",
		Payload: json.RawMessage(`{"body":"hi"}`),
	}}}
	r := NewInputRouter(lister, silentLogger())
	r.Attach(context.Background(), "a1", inputterFunc(func() error {
		close(started)
		time.Sleep(100 * time.Millisecond) // in-flight work, ignores ctx
		completed.Store(true)
		return nil
	}), 0)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch never started")
	}
	r.Detach("a1")
	if !completed.Load() {
		t.Fatal("Detach returned before the in-flight dispatch completed (#77.2)")
	}
}

type inputterFunc func() error

func (f inputterFunc) Input(_ context.Context, _ string, _ map[string]any) error { return f() }

// blockingReader blocks on Read until unblock is closed, then reports EOF — a
// stand-in for a child stdout pipe that never closes, so a driver's readLoop
// scanner is stuck and only Stop()'s bound can end the wait (#77.3).
type blockingReader struct{ unblock chan struct{} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.unblock
	return 0, io.EOF
}

// TestStdioDriverStopBoundedWithoutCloser pins #77.3: with a nil Closer and a
// readLoop wedged on a never-closing pipe, Stop() must still return (bounded)
// instead of hanging the host-runner stop path on wg.Wait() forever.
func TestStdioDriverStopBoundedWithoutCloser(t *testing.T) {
	defer swapDrainTimeout(&driverStopDrainTimeout, 150*time.Millisecond)()

	rd := &blockingReader{unblock: make(chan struct{})}
	d := &StdioDriver{
		AgentID: "a1",
		Poster:  &capturingPoster{},
		Stdout:  rd,
		Closer:  nil, // the bug condition: nothing unblocks readLoop
		Log:     silentLogger(),
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stopped := make(chan struct{})
	go func() { d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(driverStopDrainTimeout + 2*time.Second):
		t.Fatal("Stop hung past the readLoop drain bound (#77.3)")
	}

	// Cleanup: let the abandoned readLoop unwind.
	close(rd.unblock)
}

// swapDrainTimeout sets *p to v and returns a restore func for defer.
func swapDrainTimeout(p *time.Duration, v time.Duration) func() {
	old := *p
	*p = v
	return func() { *p = old }
}

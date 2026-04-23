// Input router — blueprint §5.3.1 / P1.8.
//
// The hub is the source of truth for user→agent input: clients POST to
// /v1/teams/{team}/agents/{agent}/input, which writes an agent_events row
// with producer='user' and kind='input.<kind>'. This router is the piece
// that reads those rows on the host side and dispatches them to the
// running driver via its Inputter implementation.
//
// Polling over streaming here: SSE to the hub would cut latency, but each
// agent would then need its own long-lived HTTP connection + reconnect
// handling, which buys us ~500ms on a channel where humans are the event
// source anyway. A short poll per Inputter-capable agent is simpler and
// fails loud: a wedged connection is obvious from the poll error rate.
//
// Each agent gets its own goroutine. When the driver is torn down, the
// router's Stop(agentID) cancels that goroutine; the next event POST for
// a missing agent will be picked up on restart since the hub retains the
// events row indefinitely.
package hostrunner

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// inputPollInterval is the per-agent cadence. Tighter than the main host
// poll because user input is human-driven: 500ms keeps the round-trip
// from feeling laggy without thrashing the hub.
const inputPollInterval = 500 * time.Millisecond

// inputPollLimit caps how many events we pull per tick. Users can't type
// faster than this; the ceiling just bounds a catch-up burst after the
// router was paused (e.g. hub restart).
const inputPollLimit = 64

// InputLister is the narrow dependency the router needs from Client. The
// full Client satisfies this; tests inject a fake.
type InputLister interface {
	ListAgentEvents(ctx context.Context, agentID string, sinceSeq int64, limit int) ([]AgentEvent, error)
}

// InputRouter dispatches producer='user' agent_events to the per-agent
// driver's Input method. One goroutine per Attached agent; cheap enough
// at the expected cardinality (host-runner manages ~10–50 agents).
type InputRouter struct {
	Client InputLister
	Log    *slog.Logger

	mu      sync.Mutex
	agents  map[string]*inputAgentLoop
}

type inputAgentLoop struct {
	cancel  context.CancelFunc
	done    chan struct{}
	lastSeq int64
}

// NewInputRouter returns a router wired against the host-runner client.
// Log defaults to slog.Default() if nil.
func NewInputRouter(client InputLister, log *slog.Logger) *InputRouter {
	if log == nil {
		log = slog.Default()
	}
	return &InputRouter{
		Client: client,
		Log:    log,
		agents: map[string]*inputAgentLoop{},
	}
}

// Attach binds an Inputter driver to an agent id and starts the poll loop.
// startSeq is the highest already-dispatched seq (use 0 on fresh spawn —
// the hub may emit input events between spawn and driver ready, and we
// don't want to drop those). Attach is idempotent: a second Attach for
// the same agent replaces the prior dispatch target and keeps the seq
// cursor so events aren't re-delivered.
func (r *InputRouter) Attach(parent context.Context, agentID string, driver Inputter, startSeq int64) {
	if driver == nil {
		return
	}
	r.mu.Lock()
	// Replace an existing loop: cancel the old goroutine but carry the
	// seq forward so we don't redeliver input the old driver already saw.
	if prev, ok := r.agents[agentID]; ok {
		if prev.lastSeq > startSeq {
			startSeq = prev.lastSeq
		}
		prev.cancel()
		<-prev.done
	}
	ctx, cancel := context.WithCancel(parent)
	loop := &inputAgentLoop{
		cancel:  cancel,
		done:    make(chan struct{}),
		lastSeq: startSeq,
	}
	r.agents[agentID] = loop
	r.mu.Unlock()

	go r.run(ctx, agentID, driver, loop)
}

// Detach stops the poll loop for an agent. Safe to call for an unknown
// agent id; returns once the goroutine has exited so callers can rely on
// the dispatch having drained.
func (r *InputRouter) Detach(agentID string) {
	r.mu.Lock()
	loop, ok := r.agents[agentID]
	if ok {
		delete(r.agents, agentID)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	loop.cancel()
	<-loop.done
}

// StopAll tears down every attached loop. Called on host-runner shutdown
// so the main loop can exit cleanly.
func (r *InputRouter) StopAll() {
	r.mu.Lock()
	loops := make(map[string]*inputAgentLoop, len(r.agents))
	for k, v := range r.agents {
		loops[k] = v
	}
	r.agents = map[string]*inputAgentLoop{}
	r.mu.Unlock()
	for _, l := range loops {
		l.cancel()
		<-l.done
	}
}

func (r *InputRouter) run(ctx context.Context, agentID string, driver Inputter, loop *inputAgentLoop) {
	defer close(loop.done)
	t := time.NewTicker(inputPollInterval)
	defer t.Stop()
	for {
		// Poll once up front so a fresh attach doesn't wait a full tick
		// before picking up already-queued user input.
		r.tick(ctx, agentID, driver, loop)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *InputRouter) tick(ctx context.Context, agentID string, driver Inputter, loop *inputAgentLoop) {
	evs, err := r.Client.ListAgentEvents(ctx, agentID, loop.lastSeq, inputPollLimit)
	if err != nil {
		if ctx.Err() == nil {
			r.Log.Debug("input router list failed", "agent", agentID, "err", err)
		}
		return
	}
	for _, ev := range evs {
		// Advance the cursor regardless of producer so we don't re-scan
		// this event next tick. Missing a user event because of a driver
		// error is still preferable to an infinite retry loop.
		if ev.Seq > loop.lastSeq {
			loop.lastSeq = ev.Seq
		}
		// Dispatch both user-originated and a2a-originated input to the
		// driver — the hub stamps the producer column so the audit trail
		// preserves the origin, but at the driver layer both paths are
		// equivalent "something external wants the agent to act."
		if ev.Producer != "user" && ev.Producer != "a2a" {
			continue
		}
		if !strings.HasPrefix(ev.Kind, "input.") {
			continue
		}
		kind := strings.TrimPrefix(ev.Kind, "input.")
		var payload map[string]any
		if len(ev.Payload) > 0 {
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				r.Log.Warn("input payload decode failed",
					"agent", agentID, "seq", ev.Seq, "err", err)
				continue
			}
		}
		if err := driver.Input(ctx, kind, payload); err != nil {
			r.Log.Warn("input dispatch failed",
				"agent", agentID, "seq", ev.Seq, "kind", kind, "err", err)
		}
	}
}

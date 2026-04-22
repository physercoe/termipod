// Agent driver (blueprint §5.3.1, §9 P1.1).
//
// A driver is the host-runner component that turns a spawned agent into a
// stream of `agent_events` on the hub. Each driving mode (M1 ACP, M2
// structured stdio, M4 manual/pane) is a distinct Driver implementation;
// governance (MCP gateway, audit, budget) is identical across modes and
// lives elsewhere.
//
// This file defines the common interface plus the tiny poster abstraction
// used by all modes to publish events. The modes themselves live in
// driver_*.go.
package hostrunner

import "context"

// Driver is the lifecycle contract every mode satisfies. Start returns once
// the driver is wired and ready (initial events may or may not have been
// emitted yet); Stop must be idempotent — reconcile and ctx cancel can both
// fire it for the same agent during shutdown.
type Driver interface {
	Start(ctx context.Context) error
	Stop()
}

// Inputter is the optional capability for drivers that accept user input
// (blueprint §5.3.1 / P1.8). The host-runner's InputRouter subscribes to
// the hub's agent_events SSE, filters for producer='user' rows, and calls
// Input on the matching driver. Drivers that cannot accept input in their
// mode (e.g. plain M4 capture today) simply do not implement this
// interface — the router skips them with a debug log.
//
// `kind` is the hub-side input kind without the "input." prefix: text,
// approval, cancel, attach. `payload` is the decoded JSON map as stored
// by handlePostAgentInput.
type Inputter interface {
	Input(ctx context.Context, kind string, payload map[string]any) error
}

// AgentEventPoster is the narrow dependency the drivers need from Client.
// Declared as an interface so tests can fake it without wiring a full HTTP
// round-trip.
type AgentEventPoster interface {
	PostAgentEvent(ctx context.Context, agentID, kind, producer string, payload any) error
}

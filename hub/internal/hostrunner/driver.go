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

// AgentEventPoster is the narrow dependency the drivers need from Client.
// Declared as an interface so tests can fake it without wiring a full HTTP
// round-trip.
type AgentEventPoster interface {
	PostAgentEvent(ctx context.Context, agentID, kind, producer string, payload any) error
}

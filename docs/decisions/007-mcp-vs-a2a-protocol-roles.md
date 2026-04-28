# 007. MCP for agent↔hub authority; A2A for agent↔agent peers

> **Type:** decision
> **Status:** Accepted (2026-04-27)
> **Audience:** contributors
> **Last verified vs code:** v1.0.316

**TL;DR.** MCP is the wire for "agent asks the hub to do something
authoritative." A2A is the wire for "agent talks to another agent."
Channels are for ambient broadcast. Today's `agents.fanout` posts
worker tasks via the hub event queue (MCP-flavored); A2A-flavored
task delivery stays a Later item.

## Context

Three protocols coexist:
- **MCP** — agent → tool. Tool calls into hub authority surface.
  Served at `/mcp/<token>` (consolidated v1.0.298 per
  `decisions/002-mcp-consolidation.md`).
- **A2A** — agent ↔ agent. Peer messages and task lifecycle.
  Each host-runner serves `/a2a/<agent>/.well-known/agent.json`;
  hub relays cross-host (per `decisions/003-a2a-relay-required.md`).
- **Channels** — agent → broadcast. Team/project pub/sub. Audit-bearing.

The contentious case was "the steward gives a worker a task." That
walks two rules:
- MCP — the steward is asking the hub to do this
- A2A — the receiving entity *is* another agent

`discussions/agent-protocol-roles.md` (the discussion that fed this
ADR) traced the drift: the v1.0.296 orchestrator-worker slice
(`agents.fanout`/`gather`/`reports.post`) used MCP throughout. That's
correct for the spawn step (hub authority) but conflates the task-post
step (which is conceptually agent-to-agent).

Two clean designs:
- **A: Hub-mediated everything** (what shipped) — MCP fanout posts
  tasks as `input.text` events into agent_events; worker's
  InputRouter delivers; worker calls `reports.post` back via MCP.
- **C: A2A as preferred peer wire** — fanout returns each worker's
  `a2a_url`; steward calls `a2a.invoke(handle, text)` per worker;
  worker responds with A2A `Task.result`; gather polls both event
  bus and A2A task store.

Design A ships and works. Design C is correct but ~½ wedge of work
and not blocking the demo.

## Decision

Adopt the protocol-role mapping:
- **MCP** if the action mutates hub-managed state (spawn an agent,
  open a session, rename, write a doc, post a worker_report).
- **A2A** if the action is "ask another agent something" or "here's
  my response to your request" — discovery, point-to-point messaging,
  task lifecycle.
- **Channels** for ambient broadcast where the audience is "anyone
  subscribed."

Today's flow accepts the `agents.fanout` drift (Design A). Move
toward Design C when the demo exercises a multi-host A2A path that
demonstrates the asymmetry pain.

## Consequences

- The steward template's "Available tools" lists every authority
  capability — `decisions/002-mcp-consolidation.md` made all of them
  reachable through one MCP entry. `a2a.invoke` is one of them.
- `reports.post` stays as the typed within-host worker → hub feedback
  channel. `worker_report.v1` becomes a content-shape convention
  regardless of transport (MCP or A2A `Task.result`).
- `delegate` (the existing MCP tool that posts a `delegate`-typed
  channel event) lives on as a third "tell another agent something"
  path — confusing but used by some recipes. Retiring it is a
  Later item; rename to `channels.post_delegate` if/when retiring.
- Cross-hub federation is out of MVP scope per
  `../spine/blueprint.md` §9 P3.4.

## References

- Discussion: `../discussions/agent-protocol-roles.md` (full audit
  table + Design A/B/C analysis)
- Implementation: `decisions/002-mcp-consolidation.md`,
  `decisions/003-a2a-relay-required.md`
- Memory: shipped v1.0.296 orchestrator-worker slice

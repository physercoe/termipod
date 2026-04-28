# 002. Consolidate to a single MCP service in spawn `.mcp.json`

> **Type:** decision
> **Status:** Accepted (2026-04-27, shipped v1.0.298)
> **Audience:** contributors
> **Last verified vs code:** v1.0.310

**TL;DR.** Spawned agents register one MCP server (`hub-mcp-bridge`)
in their `.mcp.json`. The hub serves the union of the narrow surface
(gates, attention, post_excerpt, journal, orchestrator-worker
primitives) and the rich-authority surface (projects, plans, runs,
agents.spawn, schedules, channels, a2a.invoke, …) through one
endpoint. One symlink installs everything.

## Context

The codebase had grown two MCP servers:
- `hub-mcp-bridge` (stdio↔HTTP shim) — proxies the hub's in-process
  MCP at `/mcp/<token>`; exposes the narrow per-token catalog.
- `hub-mcp-server` (standalone daemon) — local stdio terminator
  with a hard-coded rich-authority catalog that calls hub REST.

Steward templates referenced rich-authority tools (`agents.spawn`,
`a2a.invoke`, `runs.create`, `plans.steps.*`, `schedules.*`,
`channels.create`, `projects.update`, `hosts.update_ssh_hint`) but
spawned agents only had the bridge in their `.mcp.json` — so those
tool calls failed. `discussions/agent-protocol-roles.md` flagged
this as the "phantom tool" problem.

Two paths considered:
1. Wire `hub-mcp-server` into spawns alongside the bridge (multicall
   pattern via host-runner basename detection). Two symlinks per
   install, two `.mcp.json` entries. Ship-fast.
2. Move the rich-authority catalog into the hub's in-process MCP.
   One symlink, one entry. Some refactoring.

Path 1 shipped briefly (v1.0.297) before the user pushed for Path 2:
*"ship into one service now, user does not like to install another
service (just one cmd is the best)."*

## Decision

Path 2. Add `mcp_authority.go` in the server package: a chi-router
HTTP transport that reuses the existing `internal/hubmcpserver` tool
closures in-process. Append the catalog to `mcpToolDefs()`; fall
through to `dispatchAuthorityTool` for unknown names.

Standalone `hub-mcp-server` binary still builds for ops/debug, just
not wired into spawns. `hub.MCPHubServerName` constant retired.

## Consequences

- Install drops to one symlink: `hub-mcp-bridge → host-runner`. Track
  A and Track B in `../how-to/install-host-runner.md` updated.
- Spawned agents reach every MCP tool through one `.mcp.json` entry.
  No engine-side awareness of the dual-server split.
- All auth/audit/broadcast middleware runs the same way for the
  rich-authority surface as for the narrow one — chi-router dispatch
  is in-process but wire-shape-identical.
- Egress proxy (v1.0.286) still masks the hub URL behind
  `127.0.0.1:41825` because the bridge's HUB_URL points there; one
  fewer URL to forward.
- Closes the "phantom tools" finding from
  `discussions/agent-protocol-roles.md`.

## References

- Code: `hub/internal/server/mcp_authority.go`,
  `hub/internal/hubmcpserver/api.go`,
  `hub/internal/server/mcp.go` `dispatchAuthorityToolRaw` fall-through
- Commits: v1.0.297 (multicall — superseded same day) → v1.0.298
  (in-process consolidation)
- Discussion: `../discussions/agent-protocol-roles.md`
- Related: `decisions/007-mcp-vs-a2a-protocol-roles.md`

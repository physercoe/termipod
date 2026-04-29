# 005. User is owner/director; steward operates the system

> **Type:** decision
> **Status:** Accepted (2026-04-23)
> **Audience:** contributors
> **Last verified vs code:** v1.0.347

**TL;DR.** The mobile app is a **principal/director's surface**, not
an operator's surface. The user directs agents; agents operate the
system. The steward is a CEO-class operator who reaches every system
surface — missing MCP tools are infra gaps, not intentional
boundaries.

## Context

Two competing interaction models surfaced during early UX iteration:
1. **Operator model** — the user pushes buttons that mutate state
   directly (create project, edit schedule, post message, run cron).
   The mobile app is a control panel.
2. **Director model** — the user expresses intent ("let's run a
   sweep on Shakespeare"); the steward decomposes, spawns workers,
   posts artifacts, surfaces decisions for review. Mobile is a
   conversational and ratification surface.

The director model maps to how the user actually behaves: they
direct, they don't operate. Forcing them through 27 mobile screens
to push individual buttons is the pre-rebrand MuxPod experience and
explicitly *not* what termipod is for.

This implies the **steward** must be able to do everything an
operator could do — otherwise the user falls back to operator-mode
buttons. So every authority operation needs a corresponding MCP tool
the steward can call.

## Decision

The steward is a CEO-class operator. Every system surface is
reachable through MCP from the steward's session:
- Projects: create / update / list / get
- Plans: create / steps.* / advance
- Runs: create / attach_artifact / list / get
- Schedules: create / update / delete / run
- Channels: create (project + team scope) / post_event
- Documents + reviews: create / list
- Hosts: update_ssh_hint
- Agents: spawn / fanout / list / pause / shutdown
- A2A: invoke

The user retains owner-class authority for *ratification* — the
steward asks (`request_approval`, `request_select`, `request_help`,
`permission_prompt`) before taking strategic-tier actions
(`tiers.go` `TierStrategic`). The first three are turn-based since
v1.0.338 ([ADR-011](011-turn-based-attention-delivery.md)) — they
return immediately and the principal's reply lands as a new user
turn rather than a tool-result. `permission_prompt` is sync on
Claude (the `canUseTool` hook contract has no deferred branch —
vendor constraint, not a design choice) but turn-based on Codex
since v1.0.345 ([ADR-012](012-codex-app-server-integration.md)
D3) — codex's `app-server` JSON-RPC protocol permits arbitrary
response latency on the long-lived stdio pipe, and slice 4 of the
codex wedge bridges those server-initiated approval requests to
attention_items just like the three async kinds.
See [`reference/attention-kinds.md`](../reference/attention-kinds.md)
for the per-kind decision tree and resolution semantics.

Mobile UX renders the steward chat as the primary entry point;
direct-mutation screens (Projects tab create button, etc.) remain
for diagnostic / fallback use but aren't the intended path.

## Consequences

- Missing MCP tools are infra gaps, not features. The steward MCP
  parity audit (`discussions/ux-steward-audit.md`) tracked these as
  bugs. v1.0.156 closed P4.4 by shipping the missing tools.
- Tier policy gates strategic actions through `request_select` etc.
  The user is the only legitimate ratifier of strategic-tier calls;
  the steward never auto-approves them.
- Permission model: agents auto-allow trivial / routine calls;
  significant tier asks; strategic always asks. See
  `feedback_permission_scope` memory.
- This decision is what makes the chat-first UI viable. Without it,
  the mobile app would have to grow a button per operation and we'd
  end up at MuxPod's surface.

## References

- Memory: `feedback_steward_executive_role`,
  `feedback_ux_principal_director`
- Audit: `../discussions/ux-steward-audit.md`
- Tier model: `hub/internal/server/tiers.go`
- Related: `decisions/004-single-steward-mvp.md` (one steward),
  `decisions/002-mcp-consolidation.md` (single MCP service that
  serves the full surface)

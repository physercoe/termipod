# 003. A2A relay is required for the demo (GPU hosts are NAT'd)

> **Type:** decision
> **Status:** Accepted (2026-04-23)
> **Audience:** contributors
> **Last verified vs code:** v1.0.310

**TL;DR.** Cross-host agent ↔ agent traffic always goes through the
hub's reverse-tunnel relay, never direct. The demo's GPU host is
behind NAT (rented compute, residential, etc.) and can't accept
inbound connections.

## Context

P3.2 of the blueprint commits to A2A peers serving agent-cards on
each host-runner. Naïvely, agent A on host X resolves agent B's card
URL and POSTs `message/send` directly. That works on a LAN or two
publicly-routable boxes.

The demo's locked configuration (`decisions/001-locked-candidate-a.md`)
puts the steward on a public VPS but the ml-worker on a GPU box that
is typically:
- Rented by the hour (Lambda, Modal, …) with no inbound port
- Behind NAT in a residential setting
- Or behind a corp firewall

Direct peer-to-peer fails for any of these. Two paths considered:
1. Require the GPU host to expose a port (DDNS, port-forward,
   cloudflared tunnel, …) — pushes ops complexity to the user.
2. Always route A2A traffic through the hub. The hub already has a
   public URL (it's how agents reach `/mcp/<token>`); add a relay
   path that forwards `message/send` to whichever host-runner is
   currently connected.

## Decision

Adopt path 2. The hub serves `/a2a/relay/<host>/<agent>/...`. Each
host-runner maintains a server-sent stream from the hub for relay
delivery. Agent-cards are rewritten by the hub's directory so the
URL field points to the relay path instead of the raw host.

Direct peer-to-peer still works on a LAN if both hosts are reachable
— but the standard path, and the only path the demo can rely on, is
relay-routed.

## Consequences

- A2A always traverses the hub even on the same LAN. Slight latency
  cost; outweighed by deployment uniformity.
- Hub becomes a cross-host privacy/audit chokepoint — every
  agent ↔ agent message can be logged.
- Agents see only the relay URL in their cards; they never learn the
  underlying host's IP. Pairs with `egress proxy` (v1.0.286) which
  also masks hub URL from agents — `agent_compose.dart` agents only
  ever see `127.0.0.1:41825/...` and the hub's relay path, never the
  real network.
- P3.2b (A2A task endpoints) was sequenced *after* P3.3 (relay) — see
  `../plans/research-demo-gaps.md` for the dependency.
- Cross-hub federation (multiple hubs exchanging A2A) is explicitly
  out of MVP scope per `../spine/blueprint.md` §9 P3.4.

## References

- Code: hub `/a2a/relay/...` endpoint; `hub/internal/hostrunner/a2a/`
- Plan: `../plans/research-demo-gaps.md` P3.3
- Memory: `project_a2a_relay_required`
- Related: `decisions/007-mcp-vs-a2a-protocol-roles.md`

# 018. Hub ↔ host-runner connectivity assumes a tailnet, not reverse-SSH

> **Type:** decision
> **Status:** Accepted (2026-04-19) — back-dated from when the choice was made
> **Audience:** contributors · operators
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** Termipod assumes the hub and its host-runners are mutually reachable on the same private network — typically a Tailscale / headscale tailnet, but any private overlay (WireGuard, ZeroTier, etc.) works. We considered shipping a reverse-SSH tunnel subsystem (~500 lines of code) so the hub could reach NAT'd hosts and rejected it. The user's environment was already on a tailnet, and *all* termipod's installation guidance assumes private connectivity. This is *separate from* [ADR-003](003-a2a-relay-required.md) which gates *agent ↔ agent* traffic through a hub-side relay regardless of network topology — that's about cross-trust path uniformity, not reachability. ADR-003 still holds; this ADR is about the *transport between hub and host-runner*.

---

## Context

Three connectivity questions surfaced as the hub design solidified in mid-April 2026:

1. **Hub → host-runner.** Hub is a Go daemon on a VPS (usually). Host-runner runs on each host where agents live (the same VPS for the steward; a GPU box for workers; sometimes a laptop). Hub needs to send commands to host-runner (spawn, terminate, reload config) and receive event streams.
2. **Host-runner → hub.** Host-runner POSTs events, attention items, run digests to the hub's REST API. Trivially works through any NAT — outbound HTTP is universally allowed.
3. **Agent ↔ agent across hosts (A2A).** Steward on the VPS calls `a2a.invoke` against ml-worker on the GPU box. This is [ADR-003](003-a2a-relay-required.md)'s domain — relay through the hub.

The problem is question 1. If the GPU box is NAT'd, the hub can't initiate a TCP connection to it.

Two paths considered.

### Path A — reverse-SSH tunnel subsystem (rejected)

The hub would manage SSH connections initiated *from* each host-runner *to* the hub, expose forward-tunneled local ports the hub could write to, and multiplex commands over them. Deployment-friendly (works through any NAT) but heavy:

- ~500 lines of Go to manage tunnel lifecycles, reconnects, authentication.
- An additional service identity (the SSH key) per host-runner.
- A debugging surface (tunnel up / down) the operator has to learn.
- Multiplexing semantics that subtly diverge from the hub's existing HTTP/JSON model.

### Path B — assume private connectivity (adopted)

If the hub and all host-runners are on the same private overlay network (Tailscale tailnet, WireGuard, etc.), then the hub can directly reach each host's `host-runner` HTTP endpoint over the overlay. No tunnel code needed; the OS networking stack is the tunnel.

The user's environment was already on a Tailscale tailnet for unrelated reasons. Adding mux-pod / termipod to that tailnet was a one-command change; building reverse-SSH would have been a week.

---

## Decision

**D1. Hub ↔ host-runner uses direct private-network HTTP.** Hub treats every host-runner as a reachable endpoint on the network. The default install assumes a tailnet; documentation lists alternatives (WireGuard, ZeroTier, plain LAN) but the user is responsible for setting up the overlay.

**D2. No reverse-SSH or tunnel-management code in the hub.** The hub does not initiate or manage tunnels. If a deployment can't satisfy D1 (rare, e.g. fully air-gapped GPU boxes that can only outbound HTTPS), the operator is expected to use a generic tunnel tool (cloudflared, ngrok, frp, …) outside termipod.

**D3. Hub URL bound at install.** Hub serves on a chosen address (typically the tailnet IP + a high port). Each host-runner is configured with the hub's URL at install time. Hub URL changes (network reshape) require host-runner reconfiguration — there's no auto-discovery in MVP.

**D4. Direction independence.** Both hub → host-runner and host-runner → hub use the same overlay. There's no sense of "the hub is the public side" — both sides are equally private. This matters for the next decision: ADR-003's relay is a *cross-trust* primitive (agents on host X shouldn't see host Y's IP), not a reachability primitive.

**D5. Host-runner identity by registered URL.** A host-runner's URL is its identity for routing purposes. Two host-runners on the same host with different ports are distinct identities. The hub's `hosts` table stores the URL; mobile shows it in host details.

---

## Consequences

**Becomes possible:**
- Bring up a hub + N host-runners with `tailscale up` on each box and a single config edit. No tunnel debugging.
- Direct SSE streams hub → host-runner without long-poll workarounds. The hub's reconciliation is faster because it can push spawn commands without waiting for the host-runner's next poll.
- The install how-tos ([how-to/install-host-runner.md](../how-to/install-host-runner.md), [how-to/install-hub-server.md](../how-to/install-hub-server.md)) stay short.

**Becomes harder:**
- Operators without a private overlay must set one up before installing termipod. Tailscale's free tier covers personal use; this is rarely a blocker, but it's a real precondition.
- Air-gapped GPU boxes (rare; some HPC clusters) can't be host-runners without an outbound tunnel-out arrangement, which is operator's responsibility.
- Multi-hub federation across networks (post-MVP) requires either a shared tailnet across the federation or per-hub tunnels — neither is in scope today.

**Becomes forbidden:**
- Adding a reverse-SSH or tunnel subsystem to the hub for "convenience." If a deployment context emerges that genuinely can't satisfy D1, document the alternative externally (a how-to for cloudflared, frp, etc.) rather than putting tunnel-management code in the hub.
- Assuming the hub is on a public IP. Some deployments will have the hub itself on a tailnet IP with no public address; that's fine. Public-IP assumptions in code (e.g. a feature that emits the hub's URL to public services) need to be revisited.

---

## Migration

This ADR is back-dated documentation of a choice the project's been operating under since the hub daemon shipped in `406fdf7` (2026-04-19). No migration. For new contributors:

1. The deployment story is "set up Tailscale (or equivalent), put hub on one node, put host-runner on each compute node, configure with each other's tailnet URLs." Match the install how-tos exactly.
2. If you find code assuming the hub has a public IP or that tunnel-management is termipod's responsibility, that's a bug — file an issue and link this ADR.

---

## References

- Code: `hub/internal/server/server.go`, `hub/internal/hostrunner/main.go` (HTTP-based config; no tunnel code).
- [ADR-003](003-a2a-relay-required.md) — the *agent ↔ agent* relay (different concern; both ADRs co-exist).
- [How-to: install-hub-server](../how-to/install-hub-server.md) — operator deployment narrative.
- [How-to: install-host-runner](../how-to/install-host-runner.md) — same.
- [Discussion: positioning §1.5](../discussions/positioning.md) — the personal-tool framing that makes "user has a tailnet" a reasonable assumption.

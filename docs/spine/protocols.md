# Protocol layering

> **Type:** axiom
> **Status:** Current (2026-05-05)
> **Audience:** contributors
> **Last verified vs code:** v1.0.351

**TL;DR.** Every inter-component edge in termipod is characterised by
its relationship type, and the protocol is forced by the type, not
chosen by fashion. Five relationship types, seven edges, three driving
modes for the host-runner ↔ agent control channel, plus the relay /
A2A / AG-UI conventions. Extracted from the original `blueprint.md`
§5 (P1.6 doc-uplift refactor).

---

## 1. Relationship types

| Code | Name | Meaning |
|---|---|---|
| **C** | control | Neither side is agent-shaped; imperative commands + status |
| **S** | supervision | One side owns the other's process lifetime; structured observation and steering of a subprocess |
| **R** | RPC-with-capability | Callee exposes typed named capabilities; caller invokes them. Callee is a tool, not a reasoner |
| **P** | peer | Both sides are autonomous reasoners coordinating via tasks and artifacts |
| **O** | observation | One side streams semantic events, the other renders |

---

## 2. Edge matrix

| Edge | Type | Protocol | Transport |
|---|---|---|---|
| Human ↔ Hub | C | termipod REST + SSE | HTTPS |
| Human ↔ Agent (live view) | O | **AG-UI** (hub-brokered) | SSE over HTTPS |
| Hub ↔ Host-runner | C | termipod REST + SSE | HTTPS, host-initiated |
| Host-runner ↔ Agent (local) | S | **ACP (Zed's Agent Client Protocol)** | JSON-RPC over stdio |
| Agent ↔ Hub (authority caps) | R | **MCP**, relayed via host-runner | UDS / localhost to host-runner, then relayed |
| Agent ↔ Host-runner (local caps) | R | **MCP** | UDS / localhost |
| Agent ↔ Agent (any host) | P | **A2A** | HTTPS direct, or hub reverse-tunnel relay |

---

## 3. The relay principle for agent → hub

Agents never open direct network connections to the hub. The host-runner
runs a local MCP gateway exposing `hub://` capabilities; agent calls
traverse:

```
agent (MCP client) → host-runner MCP gateway → hub REST
```

Host-runner stamps the agent's identity, enforces local budget and rate
limits, reuses its single persistent hub token.

Consequences:

- One hub token per host, not per agent.
- Agent credentials never exist; nothing to leak or rotate per spawn.
- Air-gapped compute nodes (only host-runner allowed egress) work by
  construction.
- Short hub outages degrade gracefully (host-runner buffers writes,
  serves cached reads).
- Audit stays per-agent — host-runner preserves identity in the
  forwarded call.

---

## 4. ACP scope

ACP operates only on the host-runner ↔ agent edge on the same machine.
It does not cross the network. It runs in parallel with tmux: ACP is
the *control* channel (structured events: lifecycle, text, tool-call,
diff, progress, pause-for-approval, cancel); tmux is the *display*
channel (human-readable TTY output).

For agents that don't speak ACP, host-runner falls back to pane
scraping and synthesizes minimal events (lifecycle markers + periodic
text-message events from pane captures). AG-UI fidelity is lower but
the system still works.

**Billing caveat for Claude Code via ACP.** Zed's ACP adapter for
Claude Code (`@agentclientprotocol/claude-agent-acp`) wraps the Claude
*Agent SDK*, not the Claude Code CLI. The Agent SDK officially
supports only `ANTHROPIC_API_KEY` billing; Pro/Max subscription
billing is an open upstream issue
(anthropics/claude-agent-sdk-python#559). A community workaround —
`claude setup-token` then `CLAUDE_CODE_OAUTH_TOKEN=…` — has worked
intermittently and is not officially supported. Host-runner therefore
offers three driving modes plus a one-shot plan-step path (§5,
[`blueprint.md §6.2`](blueprint.md)); ACP is not the only structured
way to drive Claude Code.

---

## 5. Agent driving modes

A **driving mode** describes how host-runner wires the stdio of a
*persistent* agent — i.e. one that has its own identity, lifecycle,
and authority. Governance (MCP gateway, audit, budget) is identical
across modes; only the control channel differs.

There are three modes:

| # | Mode | Control channel | AG-UI fidelity | Claude Code subscription-compatible | Notes |
|---|---|---|---|---|---|
| M1 | **ACP** | JSON-RPC over stdio via ACP adapter | High (native) | No (API key only via Agent SDK) | Preferred for agents with native ACP (Gemini CLI, Codex, OpenCode, Cline, …). |
| M2 | **Structured stdio** | Agent-native JSON-line protocol (e.g. `claude --input-format stream-json --output-format stream-json`) | High (agent-equivalent to ACP) | Yes (drives the CLI binary directly) | Per-agent shim. Recommended default for Claude Code under Pro/Max. |
| M4 | **Per-engine local-stream tap** | Per engine — claude-code: JSONL tail + `tmux send-keys`; others: tmux pane PTY scrape | High for engines with a JSONL adapter (claude-code today); Low otherwise | Yes | Per [ADR-027](../decisions/027-local-log-tail-driver.md): adapter ships claude-code first; gemini / codex / kimi retain the legacy pane-PTY binding until their adapters land. Emits the same `agent_event` shapes M1/M2 produce when a JSONL adapter is bound. |

(M3 "headless one-shot" is not a mode; it's a `llm_call` step inside a
deterministic plan phase — see [`blueprint.md §6.2`](blueprint.md). A
one-shot invocation lacks a persistent session, so it doesn't meet
the agent-shape threshold.)

**Mode M4 is a first-class mode, not just a fallback.** Derivation
from axioms:

- A1 (attention scarcity) is not violated — the user only enters the
  pane when the structured UI has failed them or when they want
  direct control. For the rest of the fleet, structured modes still
  hold.
- A2 (work bound to compute) is satisfied — the pane is the compute's
  native interface; typing into it is the most direct form of
  "spatial" work.
- A3 (authority) still holds — the agent's outbound calls still go
  through host-runner's MCP gateway, so policy and audit are
  unaffected. Host-runner captures pane output to the audit log
  whether the human or a structured adapter is driving input.

Use cases for M4: debugging an agent that has gone sideways in M1/M2,
real-time pairing, agents that lack any structured output mode,
operator preference.

**Hooks as a side channel (orthogonal to all modes).** Claude Code
and several other agents expose hooks (`pre_tool_use`,
`post_tool_use`, `session_start`, etc.) that shell out to user
scripts. Host-runner installs hooks that POST structured events to
its local MCP gateway. This yields high-fidelity tool-call and
approval events even in M4, and augments M1/M2 with events the
structured protocol doesn't carry. Hooks are an additive event
source, not a mode.

---

## 6. Mode resolution and host capability discovery

**Mode declaration.** Every project template declares one
`driving_mode` (single value, M1|M2|M4) and an optional ordered
`fallback_modes` list (e.g. `[M4]` to degrade to pane-only on
structured-protocol failure). A spawn request may override both.

**Billing declaration.** The user declares the billing context per
agent family per host (e.g. "Claude Code uses subscription on host X,
API key on host Y"). Host-runner does **not** infer or probe billing.
If a declared mode is known to conflict with the declared billing
(e.g. Claude Code M1 under subscription, blocked by Agent SDK), spawn
fails fast with a clear error.

**Host capability discovery.** Host-runner probes *binary presence
and version only* and reports on heartbeat:

```json
{
  "agents": {
    "claude-code": { "installed": true, "version": "…",
                     "supports": ["M1","M2","M4"] },
    "gemini-cli":  { "installed": true, "supports": ["M1","M4"] },
    "codex":       { "installed": false }
  },
  "probed_at": "…"
}
```

Hub caches this per-host as `hosts.capabilities_json` with a
staleness TTL (default 5 minutes). Mode resolution at spawn time is:

```
resolve(template.mode, spawn_override, host.capabilities, user.billing_decl)
  → concrete_mode | fail_fast(reason)
```

---

## 7. Enter-pane and SSH binding

Every agent detail sheet exposes an **"Enter pane"** action that
drops the user into a full-screen tmux view of that agent's pane. In
M1/M2 the pane is read-mostly (display channel); in M4 it is the
control channel. A mode badge on the agent card identifies which.

The plumbing bridges three facts:

1. Hub knows `agent.host_id` + pane coords (session, window, pane).
2. Hub's `hosts` table carries a non-secret `ssh_hint_json`
   (hostname, port, username, optional jump-host hint) — set during
   host registration. Secrets never live in the hub (data-ownership
   law).
3. The phone keeps a local
   `hub_host_bindings(hub_host_id → connection_id)` mapping to its
   own SSH Connection entries (which hold the actual credentials in
   flutter_secure_storage).

Enter-pane flow:

- Binding present → phone opens SSH to the bound Connection and
  issues `tmux attach -t <s> \; select-window -t <w> \; select-pane
  -t <p>`.
- Binding missing but `ssh_hint_json` present → phone opens the
  Connection form pre-filled from the hint; user supplies
  credentials; binding saved.
- Neither present → action disabled with an explanation.

Constraint: **host-runner runs as the SSH user.** This guarantees
the tmux socket host-runner wrote to is the same socket the human's
SSH session attaches to. Split-user deployments (host-runner as a
service account, humans as different users) are out of MVP scope;
they require a documented shared-socket path with group perms and
more involved UX.

Multi-user teams (each member bringing their own SSH credentials)
are deferred; MVP assumes solo / same user.

---

## 8. A2A topology

The host-runner is the A2A terminus. Each host-runner exposes one
A2A endpoint per live agent, with an agent-card at
`/a2a/<agent-id>/.well-known/agent.json`. Host-runner publishes
agent-cards to the hub's A2A directory.

Transport rules:

- Direct host-runner ↔ host-runner when mutual reachability allows.
- Hub reverse-tunnel relay when NAT blocks direct (hub already
  holds a persistent connection from each host-runner).

A2A payloads are small (task JSON, artifact URIs). Bulk artifact
bytes are fetched by URI from host or cloud, never through A2A task
bodies. The data-ownership law holds.

A2A supports bilateral multi-turn discussion scoped to a task — the
writer and critic agents can exchange 4 clarifying turns inside a
single "review" task, with provenance preserved. Multilateral group
chat is not A2A's concern; use hub channels via MCP post.

**A2A observability.** A2A is agent-to-agent wire, but humans still
need to watch it. When an agent invokes or responds over A2A, the
host-runner emits the call and result as events into the **calling
agent's AG-UI stream** — event kinds `a2a.invoke` (outbound task
with target agent-card, capability, task summary) and `a2a.response`
(inbound result / error / turn). The events join the same SSE feed
the phone already renders for that agent, so A2A activity appears
inline in the agent's channel card alongside thoughts and tool
calls. No parallel observability channel, no extra subscription.
Bilateral multi-turn A2A discussions surface as a sequence of
`a2a.invoke` / `a2a.response` pairs on both agents' streams, keyed
by the shared task id so a future UI can collapse them into a
threaded view.

---

## 9. AG-UI is the broker's output wire

The hub is the sole translator from internal protocols (ACP, A2A
task status, hub events) to AG-UI. The app only knows AG-UI. This
keeps client complexity bounded and lets us evolve internal wire
formats without breaking the phone.

AG-UI pause-for-approval is the wire format of termipod's existing
attention/approval system. The attention UI and AG-UI approval event
are the same thing — one internal model, one external standard.

---

## 10. Cross-references

- [`blueprint.md`](blueprint.md) — axioms, ontology, data-ownership
  law (the surrounding spine doc this was extracted from)
- [`forbidden-patterns.md`](forbidden-patterns.md) — corollaries that
  follow from this protocol layering
- [`../reference/data-model.md`](../reference/data-model.md) —
  primitives consumed by the protocol layer
- [`../reference/architecture-overview.md`](../reference/architecture-overview.md)
  — C4 view referencing this matrix
- [`system-flows.md`](system-flows.md) — sequence diagrams showing
  these protocols in action
- [`../decisions/002-mcp-consolidation.md`](../decisions/002-mcp-consolidation.md)
  — single MCP service decision
- [`../decisions/003-a2a-relay-required.md`](../decisions/003-a2a-relay-required.md)
  — A2A relay decision
- [`../decisions/007-mcp-vs-a2a-protocol-roles.md`](../decisions/007-mcp-vs-a2a-protocol-roles.md)
  — protocol-role split
- [`../decisions/010-frame-profiles-as-data.md`](../decisions/010-frame-profiles-as-data.md)
  — driving-mode frame profiles

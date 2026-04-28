# Agent communication protocols — when MCP, when A2A, when channels

Status: **discussion note, 2026-04-27**. Triggered by a sharp reading
of v1.0.296: the new orchestrator-worker slice (fanout/gather/reports)
uses MCP, but A2A is the spec'd peer protocol — when does each apply,
and have we drifted?

**v1.0.298 update**: the rich-authority MCP catalog (projects, plans,
runs, agents.spawn, schedules, channels, a2a.invoke, …) that lived
only in the standalone `hub-mcp-server` daemon was consolidated into
the hub's own `/mcp/<token>` endpoint via `internal/server/mcp_authority.go`
(a chi-router HTTP transport reuses the existing tool closures
in-process). The "phantom `a2a.invoke`" finding in §2 below is
therefore resolved as of v1.0.298 — see the inline note in the table.

This doc resolves the question by stating what each protocol is for,
where today's code falls, and what the cleanest split looks like
going forward.

---

## 1. The three protocols (and what each is for)

We have three protocols in play, sometimes confused for each other:

| Protocol | Direction | Purpose | Who exposes it |
|---|---|---|---|
| **MCP** | agent → tool | Authority operations the agent can take. Tool calls. | The hub (via `/mcp/<token>` and the in-process `mcp.go` + `mcp_more.go`). |
| **A2A** | agent ↔ agent | Peer-to-peer messages and tasks between agents. Discovery via agent-cards. | Each host-runner serves cards at `/a2a/<agent-id>/.well-known/agent.json`; hub relays. |
| **Channels** | agent → broadcast | Team/project-scoped pub/sub. Audit-bearing. | The hub (`channels` table + SSE bus). |

Two simple rules tell which to use:

- **MCP if the action mutates hub-managed state** (spawn an agent,
  open a session, rename a thing, write a doc, post a worker_report).
- **A2A if the action is "I want to ask another agent something" or
  "here is my response to your request"**. Discovery, point-to-point
  messaging, task lifecycle.
- **Channels** for ambient broadcast where the audience is "anyone
  subscribed" (steward → `#hub-meta`, project chatter, decisions).

The contentious case is "the steward gives a worker a task." That
walks two rules:
- **MCP** because the steward is asking the hub to deliver this.
- **A2A** because the receiving agent IS another agent.

The right answer: **A2A is the peer wire. MCP is the
agent-asks-the-hub wire. The steward dispatching a task to a worker
is A2A.** What I shipped in fanout collapses both because it's
convenient — that's the drift.

---

## 2. What's actually wired today (audit)

| Path | Protocol used | Right or drift? |
|---|---|---|
| Steward calls `agents.fanout` (spawn N + post task) | MCP | **Drift on the second half.** The spawn is correctly MCP (hub-authority); the task post should be A2A. |
| Steward calls `agents.gather` (poll completion) | MCP | Right — querying hub state. |
| Worker calls `reports.post` (write completion event) | MCP | Right — writing to hub state. |
| Steward calls `delegate` (mcp_more.go) | MCP, posts a channel event | **Half-right.** Today it just posts a `delegate` event to a channel; receiving agents subscribe. Effectively a typed broadcast. The semantics overlap with A2A's `message/send` but the implementation goes through channels. |
| Cross-host A2A relay (`/a2a/relay/{host}/{agent}/...`) | A2A | Right. |
| Steward calls `a2a.invoke(handle, text)` | **Reachable as of v1.0.298** via `mcp_authority.go` (chi-router transport reuses the hubmcpserver tool catalog in-process) | Right — the hub serves it under `/mcp/<token>`; the steward calls it through the bridge like any other authority tool. |
| Worker reports back to steward post-task | A2A response **OR** channel post **OR** `reports.post` (just shipped) | Three competing paths, no canonical choice. |

**One real problem remains from this audit:**

1. ~~**`a2a.invoke` isn't reachable**~~ — **resolved v1.0.298.** The
   chi-router transport in `mcp_authority.go` exposes the full
   hubmcpserver catalog through `/mcp/<token>`, including `a2a.invoke`,
   `agents.spawn`, `runs.create`, `plans.steps.*`, `schedules.*`,
   `channels.create`, `projects.update`, `hosts.update_ssh_hint`. The
   steward calls them through the same bridge it uses for everything
   else. Question 3 in §5 is also closed by this.

2. **Reports have three possible paths.** A2A response (the protocol's
   native completion signal), channel post (broadcast convention),
   and `reports.post` (the new typed event). We need to pick one.

---

## 3. Two clean designs (pick one)

### Design A — Hub-mediated everything (what I shipped, made explicit)

- **Steward → workers**: via MCP `agents.fanout`. Task delivery is a
  hub event in `agent_events` (`producer='user'`, `kind='input.text'`).
  Worker's `InputRouter` picks it up.
- **Worker → steward**: via MCP `reports.post`. Stored as
  `kind='worker_report'` event. Steward sees via `agents.gather`.
- **A2A** stays for discovery (agent-cards) and for cross-team /
  cross-trust-boundary peer talk. Within a team, A2A is rarely used.

Pros:
- Works on any host (no `--a2a-addr` requirement).
- Single audit story (everything goes through `agent_events`).
- Server-side aggregation primitive (`agents.gather`) is easy.
- One protocol's edge cases (NAT, TLS, peer auth) instead of two.

Cons:
- A2A becomes a thin layer used mostly for discovery.
- Lock-in to hub-mediated routing — can't peer outside the hub.
- Conceptual asymmetry: the steward "talks to" workers via MCP,
  which feels wrong because workers are agents not tools.

### Design B — A2A for peer talk, MCP for hub-authority

- **Steward → workers**: spawn via MCP `agents.fanout` (just creates
  agents; doesn't post the task). Then `a2a.invoke(agent_id, task)`
  per worker — A2A peer protocol with task semantics.
- **Worker → steward**: A2A response. Workers fulfill the task and
  the A2A `Task` object's terminal `result` is the report. The
  shape (`status`, `summary_md`, `output_artifacts`, etc.) lives in
  the A2A response payload.
- **`agents.gather`** polls A2A task states (already in `TaskStore`
  per host-runner) instead of `worker_report` events. Or remains as
  hub-state polling, just consuming task lifecycle instead of event
  rows.

Pros:
- Each protocol does what it was designed for.
- A2A's task model (submitted → working → completed/failed/cancelled)
  is what the steward actually wants.
- Workers don't need to know about MCP `reports.post`; they just
  finish their A2A task.
- Symmetry: agents talk to agents over A2A.

Cons:
- Requires every host to expose A2A (--a2a-addr). Today optional.
- Reports' typed shape (status, summary_md, ...) needs to be a
  convention in the A2A `Task.result` payload. Less server-side
  validation than `reports.post`.
- More moving parts (relay tunnels, agent-card publishing) for
  every spawn.

### Design C — Hybrid (recommended)

The honest middle: **A2A is canonical when both ends can speak it;
MCP fallback for hub-mediated when not.**

- **`agents.fanout`** spawns N workers. Returns `agent_id` + (if
  available) `a2a_url` per worker.
- **Steward dispatches tasks**:
  - If worker has an A2A url → steward calls `a2a.invoke(url, task)`.
  - Else → steward calls `agents.send_input(agent_id, task)` (the MCP
    fallback that does what fanout does today).
- **Worker reports**:
  - If task came via A2A → worker's A2A `Task.result` is the report,
    typed by `worker_report.v1` shape convention.
  - If task came via MCP input → worker calls `reports.post` (today's
    flow).
- **`agents.gather`** unifies both paths server-side: queries
  `worker_report` events AND A2A task states for the correlation.

This is more code but correctly says "A2A is the agent wire when
viable, MCP is the hub wire."

---

## 4. Recommendation

**Today (v1.0.296)**: Design A is what's shipped. It works.

**Direction**: Move toward **Design C** when the user actually has a
multi-host A2A setup that demonstrates the asymmetry pain. Steps:

1. ~~**First, port `a2a.invoke` to the in-process MCP**~~ — **done
   v1.0.298** via `mcp_authority.go`. Steward dispatches via A2A from
   the bridge surface today.
2. **Add `a2a_url` to the fanout return** for workers whose host
   exposes A2A. Steward picks per-worker.
3. **Define `worker_report.v1` as a content-shape convention**
   regardless of transport. Workers use the same fields; reports.post
   is the MCP flavor, A2A `Task.result` is the A2A flavor.
4. **Update `agents.gather`** to consume both event-bus reports AND
   A2A task states for the same correlation_id.

**Don't** rip out `reports.post`. It works for the within-host case
and as a fallback. A2A is added as the preferred path, not as a
replacement.

---

## 5. The single decision

Three real questions for you:

1. **Accept Design A for now (drift), or fix to C immediately?** A is
   shipped and working; C is correct but ~2 wedges of work. My read:
   accept A for the device walkthrough; queue C as the next slice if
   the multi-host demo path actually exercises the A2A side.

2. **Should `delegate` (the existing MCP tool that posts a delegate
   event to a channel) be retired in favor of A2A?** It's a third way
   to "tell another agent something" and adds confusion. The clean
   answer is yes, retire — but it's used by some recipes today.

3. ~~**The steward template's "Available tools" section lists tools
   that don't actually exist in the in-process MCP**~~ — **resolved
   v1.0.298.** All of those tools (`agents.spawn`, `a2a.invoke`,
   `runs.create`, `plans.steps.*`, `schedules.*`, `channels.create`,
   `projects.update`, `hosts.update_ssh_hint`) are now reachable via
   `/mcp/<token>` — `mcp_authority.go` mounts the hubmcpserver catalog
   in-process through a chi-router transport. One symlink, one
   `.mcp.json` entry; steward sees the union of the narrow and rich
   surfaces.

---

## 6. Glossary clarifications (to avoid future confusion)

- **MCP server**: the hub's `/mcp/<token>` endpoint. The bridge
  (`hub-mcp-bridge`) is a stdio↔HTTP shim that connects spawned
  agents to it. As of v1.0.298 the hub serves the union of the narrow
  catalog (gates, attention, post_excerpt, journal, orchestrator-worker
  primitives) AND the rich-authority catalog (projects, plans, runs,
  agents.spawn, schedules, channels, a2a.invoke, …) through that one
  endpoint — `mcp_authority.go` reuses the `internal/hubmcpserver`
  tool closures via a chi-router HTTP transport. The standalone
  `hub-mcp-server` binary still builds but is no longer wired into
  spawn `.mcp.json`.
- **MCP tool**: an authority capability the hub exposes
  (`agents.fanout`, `reports.post`, `delegate`, `request_approval`,
  …). Not the same as claude-code's built-in tools (`Bash`, `Edit`,
  `Read`, …) which are the *agent's* tools, not the hub's.
- **A2A peer**: another agent. Discovered via the hub's directory of
  agent-cards. Talked to via the relay (NAT'd hosts) or directly.
- **A2A task**: one logical unit of work the steward sends a worker.
  Has a state machine: submitted → working → completed | failed |
  cancelled. The terminal state's `result` carries the worker's
  output.
- **delegate** (the MCP tool): posts a `delegate`-typed event to a
  channel with `to_ids = [target_handle]`. NOT A2A. NOT a task. Just
  a structured channel message. Worth retiring or renaming to
  `channels.post_delegate` for clarity.
- **input event**: a row in `agent_events` with `producer='user'` (or
  `producer='a2a'`). The InputRouter on host-runner picks it up and
  delivers to the agent's stdin/stream-json. This is the actual
  delivery mechanism — A2A and MCP both land here.

---

## 7. What actually changes if we pick C

Tiny, with the v1.0.298 consolidation already done:

1. ~~Port `a2a.invoke`~~ — **done v1.0.298** via `mcp_authority.go`
   (chi-router transport reuses `internal/hubmcpserver` tools
   in-process; nothing duplicated).
2. Add `a2a_url` to fanout's per-worker result. ~10 LoC.
3. Update `agents.gather` to also poll A2A task store. ~50 LoC.
4. Update steward prompt's recipe to prefer `a2a.invoke` when the
   worker advertised an `a2a_url`. ~prompt edit.

Total: roughly a third of a wedge. Not blocking the demo.

---

## 8. References

- [A2A v0.3 spec](https://github.com/a2aproject/a2a-spec) — the peer
  protocol Termipod implements
- [MCP spec](https://modelcontextprotocol.io/) — what `mcp_more.go`
  serves
- `../spine/blueprint.md` §5 — protocol layering, originally drawn the
  same way as this doc but glossed over the orchestration wedge
- `multi-agent-sota-gap.md` — the production-framework
  reference for what orchestration actually needs

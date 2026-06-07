# 016. Subagent operation-scope manifest

> **Type:** decision
> **Status:** Accepted (2026-04-30) · **Amended (2026-06-07)** — see [§Amendment](#amendment-2026-06-07)
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** Termipod-managed agents are gated at the hub MCP boundary by
a **role-based operation-scope manifest** — two roles (steward, worker),
each with a fixed allowed set of `hub://*` tools. `agents.spawn`,
template authoring, schedule authoring, project mutation, and
agent-termination are **steward-only**. Workers get the bounded
worker-surface (documents/runs/reviews/channels/attention/A2A-to-parent
and read-side tools). Engine-internal subagents (claude-code `Task`,
codex app-server children) are **out of scope** — they share the
parent's MCP client and inherit its scope by construction. Enforcement
lives in `hub/internal/server/mcp_authority.go`; the manifest is a
single grep-able `roles.yaml` file. This is termipod's *only*
governance line in MVP — `budget_cents` is deferred, per-tool approval
gates are deferred, secret-bearing tools are deferred. Scope-not-budget.

---

## Context

[Blueprint §3.3](../spine/blueprint.md) commits to two agent roles —
steward (manager) and worker (IC) — but does not enumerate which hub
authority capabilities each may invoke. Today the answer is implicit
in per-template `tool_allowlist` fields. As the template surface grows
(multi-tier stewards per
[research-demo-lifecycle](../discussions/research-demo-lifecycle.md),
plus first-class authoring of templates by stewards), an implicit
allowlist becomes a lever a misbehaving steward could pull to grant
its workers anything.

Three forces named the requirement:

1. **MVP demo design** locks no `budget_cents` enforcement and full
   default-engine tool permissions. Without budget, the only governance
   surface is *which hub authority tools each role can call*.
2. **Steward-authored templates** mean the worker `tool_allowlist` is
   itself agent-authored. A steward could write a worker template that
   declares `hub://agents.spawn` in its allowlist; without a server-side
   manifest the spawn would succeed.
3. **Engine-internal subagents** (claude-code's `Task`, codex
   app-server children, similar in others) emerged as a clarification
   point — termipod must *not* monitor or restrict them, and the
   manifest must be unambiguous about that.

Without an explicit manifest, structural safety degrades to template-
discipline — a moving target the operator can't easily audit.

---

## Decision

**D1. Two roles, fixed at the hub boundary.**

Every termipod-managed agent has exactly one role: `steward` or
`worker`. Stored on the `agents` row (already present as
`agent_kind` → role-derivation table; no migration). No third role in
MVP. Role is set at spawn time and immutable for the agent's lifetime.

**D2. Steward-only tools (workers BLOCKED).**

| Tool | Why steward-only |
|---|---|
| `hub://agents.spawn` | Workers cannot multiply themselves. Structural safety even without budget. |
| `hub://agents.archive` | Only the steward terminates peers. |
| `hub://agents.terminate` | Same. |
| `hub://templates.agent.{create,update,delete}` | Only the steward authors templates. Workers consume them. |
| `hub://templates.prompt.{create,update,delete}` | Same. |
| `hub://templates.plan.{create,update,delete}` | Same. |
| `hub://schedules.{create,update,delete,run}` | Schedules are project-orchestration, not IC. |
| `hub://projects.update` (mutable fields: goal, parameters_json, budget_cents, policy_overrides_json, steward_agent_id, on_create_template_id) | Project metadata is the steward's authority. |
| `hub://hosts.update_ssh_hint` | Host metadata is operator-tier; only steward proxies it. |
| `hub://policy.*` (when added) | Reserved. |

**D3. Worker surface (workers ALLOWED, stewards also ALLOWED).**

| Tool | Use |
|---|---|
| `hub://documents.{create,update,read,list}` | Phase artifacts, code drafts, results, papers. |
| `hub://reviews.{create,list}` | Request human review on a document or artifact. |
| `hub://runs.{register,complete,attach_metric_uri,attach_artifact}` | Experiment registration, metric URIs, artifact attachment. |
| `hub://run.metrics.read` | Read digests. |
| `hub://channels.post_event` | Project-scope and team-scope channel posts. |
| `hub://attention.{create,reply}` for `request_select` / `request_help` / `request_approval` | Ask the director when blocked. |
| `hub://a2a.invoke` (parent steward only — see D4) | Report results up the chain. |
| `hub://tasks.{create,update,list}` | Self-managed subtask tracking. |
| All hub read-side tools (`*.list`, `*.get`, `*.read`) | Observation. |

Steward role *also* gets every tool in this set (it's a superset).

**D4. A2A target restriction for workers.**

Workers may invoke `hub://a2a.invoke` only against their **parent
steward** (per `agent_spawns.parent_agent_id`). Worker-to-worker A2A is
permitted only via a steward's explicit relay; cross-project A2A from
a worker is denied.

This prevents two failure modes: (a) a worker recruiting peers
sideways to do work the steward didn't authorise, and (b) information
flow across project boundaries without steward involvement.

**D5. Engine-internal subagents are out of scope.**

The manifest governs **termipod-managed agents** — rows in the hub's
`agents` table, each backed by one host-runner-supervised process and
one tmux pane. **Engine-internal subagents** are *not* termipod agents:

- claude-code's `Task` tool fan-out
- codex app-server child sessions
- gemini-cli subagent invocations
- any analogous engine-internal mechanism

They share their parent agent's MCP client, hence inherit the parent's
operation scope by construction. Termipod **does not enumerate,
restrict, or monitor** them beyond what frame profiles surface in the
transcript.

This is the right boundary: structural safety from the manifest holds
(an engine-internal subagent cannot escape its parent's scope), and
termipod doesn't fight engine-internal patterns that make engines
productive (parallel `Task` fan-out in claude-code is what enables
fast multi-perspective analysis).

**D6. Enforcement: middleware in `mcp_authority.go`, manifest in
`roles.yaml`.**

The hub's MCP authority server (currently
`hub/internal/server/mcp_authority.go` and
`hub/internal/hubmcpserver/tools.go`) gains a single role-gating
middleware:

```go
func (s *Server) authorizeMCPCall(ctx context.Context, agentID, tool string) error {
    role := s.lookupAgentRole(ctx, agentID)
    if !s.roles.Allows(role, tool) {
        return &MCPError{Code: -32601, Message: "tool not permitted for role: "+role}
    }
    return nil
}
```

`roles.yaml` carries the manifest:

```yaml
roles:
  steward:
    allow_all: true            # superset
  worker:
    allow:
      - documents.*
      - reviews.create
      - reviews.list
      - runs.register
      - runs.complete
      - runs.attach_metric_uri
      - runs.attach_artifact
      - run.metrics.read
      - channels.post_event
      - attention.create
      - attention.reply
      - a2a.invoke              # restricted target — see D4 enforcement
      - tasks.create
      - tasks.update
      - tasks.list
      - "*.list"
      - "*.get"
      - "*.read"
    a2a_invoke_target: parent_steward_only
```

The file lives at `hub/config/roles.yaml`, embedded via `embed.FS` and
overrideable at `<DataRoot>/roles.yaml`. Hot-reload via existing
`Invalidate()`; changes take effect for next MCP call.

**D7. Self-modification guard for stewards.**

A steward cannot edit its own kind's template via `templates.*`. The
template-authoring MCP rejects edits where `target_kind ==
caller.agent_kind`. Specifically: `steward.general.v1` is
*read-only* to any general-steward instance; a domain steward can edit
worker templates but not its own domain-steward template. Avoids
confused-deputy escalation where a steward grants itself extra tools by
self-template-mutation.

This guard is **independent of D6** — D6 is a hub-MCP-tool-call gate;
D7 is a content-level check inside the templates.* tool implementations.

---

## Consequences

**Becomes possible:**
- A misbehaving steward cannot grant its workers `agents.spawn` by
  authoring their template, because the worker's MCP call still goes
  through D6's middleware which checks role, not template.
- Operators audit governance by reading one file (`roles.yaml`) instead
  of grepping per-template `tool_allowlist` blocks.
- New roles (e.g. a future `auditor` role with read-only access) drop
  in as a `roles.yaml` entry, not as a per-tool flag in MCP tool
  implementations.

**Becomes harder:**
- Adding a new steward-only tool requires a `roles.yaml` edit and a
  release. (The manifest is now part of the contract; an MCP tool that
  forgets to be listed silently denies for both roles.)
- Template-time `tool_allowlist` becomes advisory — an authoring hint
  for the engine's tool-discovery flow, not the security boundary. We
  should explicitly document this in template prose so contributors
  don't assume otherwise.

**Becomes forbidden:**
- Per-template `tool_allowlist` declaring tools forbidden by
  `roles.yaml`. The hub silently denies; we should add a CI check that
  surfaces the inconsistency at template-load time as a warning.
- Any tool implementation skipping the role middleware. Pattern:
  every MCP tool handler starts with
  `if err := s.authorizeMCPCall(...); err != nil { return err }`.
  Lint-able.

---

## Migration

No schema change. New `roles.yaml` is added. Existing per-template
`tool_allowlist` entries remain valid (the manifest is *additional*
gating, not replacement). Worker templates that today list
steward-only tools start failing those calls after this lands —
expected.

Three steps:

1. Land middleware + `roles.yaml` (one PR, no template changes).
2. Audit existing worker templates; remove steward-only tools from
   their `tool_allowlist` for clarity (no behaviour change since the
   middleware already denies).
3. Document in `reference/agent-templates.md` (or its successor) that
   `tool_allowlist` is advisory and `roles.yaml` is the authority.

---

## References

- [Discussion: research-demo-lifecycle](../discussions/research-demo-lifecycle.md) §4 (D4–D6) — the design pressure that forced this ADR
- [Blueprint §3.3](../spine/blueprint.md) — steward / worker role split
- [ADR-005](005-owner-authority-model.md) — principal/director authority model the manifest enforces
- [Plan: research-demo-lifecycle wedges](../plans/research-demo-lifecycle-wedges.md) — W1 implements the middleware
- Existing infra: `hub/internal/server/mcp_authority.go`, `hub/internal/hubmcpserver/tools.go`

---

## Amendment (2026-06-07)

Triggered by tester feedback: a `claude-code` steward spawned **dozens
of hub workers** for small tasks instead of doing the cheap work itself
or fanning out *inside its own engine*. D5 settled that engine-internal
subagents are out of *governance* scope; it did not say *when the
steward should prefer one primitive over the other*. That silence let
the steward route all parallelism to the heavyweight inter-engine
primitive. The full reasoning — cost hierarchy, well-tested practice,
the governance argument — is in
[`discussions/intra-vs-inter-engine-delegation.md`](../discussions/intra-vs-inter-engine-delegation.md).

**D-amend-1. Prefer the cheapest delegation tier; the inter-engine
boundary is the unit of director attention and governance, not of
compute.**

D5 stays as written (engine-internal subagents are not enumerated,
restricted, or monitored). This amendment adds the *preference* the
steward prompt must encode. Three tiers, cheapest first:

| Tier | Primitive | Marginal cost |
|---|---|---|
| 1 | **Inline** — the steward's own turn | ~0 |
| 2 | **Intra-engine subagent** — claude-code `Task`, codex app-server child | tokens only (a separate context window) |
| 3 | **Inter-engine hub worker** — `agents.spawn` / `agents.fanout` | a process + [hub session](../reference/glossary.md#hub-session) + cold-start context + RAM + a slot of director attention |

The steward **defaults to the lowest tier** and promotes a unit to a
tier-3 hub worker only when at least one **promotion trigger** holds:
it crosses a host; it needs a different engine; it is a durable,
director-visible deliverable (a tracked [task](../reference/glossary.md#task));
it must outlive the steward's turn; it needs its own budget/policy/
permission envelope; it needs a hard failure-isolation boundary; or it
is large enough that the spawn overhead amortizes. If none hold —
same engine, same host, small, ephemeral, no separate deliverable —
the work stays inline or intra-engine.

The governance line is unchanged and load-bearing: an engine-internal
subagent runs its `hub://*` calls through the parent's MCP client
(D5), so every *consequential action* still crosses the hub boundary
under the steward's identity and is audited there. Reifying a hub
worker per micro-step buys **no** extra governance — it only floods
the director's attention (IA axiom A1) and the host's memory. Govern
**actions and deliverables**, not compute decomposition.

**Scope of this amendment.** Prompt-level only — no schema, middleware,
or `roles.yaml` change. D1–D7 enforcement is untouched. Implemented
across all five steward prompts in the same arc — and the tier-2
mechanism genuinely differs per engine, so each got an engine-correct
pass, not a copy:

- `steward.v1` (claude-code) — full "Delegation ladder" section; `Task`
  affirmed as the tier-2 mechanism (inherits parent MCP scope, D5).
- `steward.codex` — codex's native **parallel subagents** (invoked in
  plain language; ephemeral, codex-orchestrated; `/agent` to inspect)
  named as tier-2.
- `steward.kimi` — kimi-code's **`Agent` tool** (`explore`/`plan`/`coder`
  built-ins; isolated context, ephemeral) named as tier-2.
- `steward.gemini` — deprecated engine (retires 2026-06-18), not
  re-verified; universal promotion-trigger guard only, no tier-2 tool
  named.
- `steward.antigravity` — keeps its existing ban on `agy`'s native
  `invoke_subagent` (private bus, ungovernable); the guard routes
  parallel exploration inline instead.

The codex/kimi subagent facts are from the engines' own docs (verified
2026-06), not the codebase — the per-engine prompts had simply omitted
them. Their docs don't confirm MCP-connection sharing, so the prompts
stay conservative: native subagents for *ephemeral* compute only, never
as a substitute for a governed worker.

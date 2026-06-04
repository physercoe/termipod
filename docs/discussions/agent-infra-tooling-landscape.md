---
name: Agent infra-tooling landscape — gateways, observability, caching, sandboxes
description: A 2026 deep-research capture of trending open-source tooling that sits on the hub or host-runner — the plumbing layer beneath the engines — to help TermiPod understand and use agents better and save cost. Distinct from agent-side-tooling-landscape.md (what a Claude Code instance ships with) and integrating-open-source-agents.md (which engines to spawn): this is about the LLM gateway, observability backend, semantic-cache/router, sandbox, and MCP-gateway layers. Argues the two highest-leverage moves both ride infrastructure TermiPod already has — host-runner controls the agent's spawn environment (cmd.Env) and the hub already ships an OTLP exporter (ADR-038) — so (1) injecting a base-URL to route engine traffic through an LLM gateway (Bifrost, Go + embeddable + virtual-key budgets that map to the team/owner model; LiteLLM as the batteries-included alternative) and (2) turning on Claude Code native OpenTelemetry into the existing OTLP pipe are both config changes, not engine changes. Secondary layers: observability backends (Phoenix/Langfuse/SigNoz/OpenObserve), semantic caching/routing (GPTCache/RouteLLM/vLLM Semantic Router), host-runner sandboxing (E2B microVM/Daytona/microsandbox/container-use — ties to deferred WS4), and external MCP federation (Docker MCP Gateway/agentgateway). Ends with a prioritized adoption shortlist and the open forks.
---

# Agent infra-tooling landscape — gateways, observability, caching, sandboxes

> **Type:** discussion
> **Status:** Open (2026-06-04) — landscape capture + recommendation; no
> decisions taken. Raised after the v1.0.801-alpha team-management work, when
> the director asked for a deep scan of trending open-source agent tooling
> usable on host-runner or hub to understand/use agents better and save cost.
> **Audience:** contributors
> **Last verified vs code:** v1.0.801-alpha

**TL;DR.** The trending 2026 open-source agent stack splits into five
infra layers that sit *beneath* the engines, on the hub or host-runner:
**LLM gateway**, **observability backend**, **semantic cache / router**,
**sandbox**, and **MCP gateway**. The two highest-leverage moves both ride
infrastructure TermiPod already has, so they are *config changes, not engine
changes*: (1) **route engine traffic through an LLM gateway by injecting a
base-URL at spawn** — host-runner already sets each agent's `cmd.Env`
(`plan_executor.go:210`, `launch_m2.go:337`, `driver_exec_resume.go:624`), so
pointing `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` at a gateway unlocks
centralized spend, **per-team budgets**, semantic caching, and fallback across
every engine at once; and (2) **turn on Claude Code's native OpenTelemetry into
the OTLP exporter we already built** ([ADR-038](../decisions/038-per-run-event-digest.md),
`hub/internal/otlptrace/`) for real-time per-agent token/cost/cache metrics.
Top gateway pick for a Go hub is **Bifrost** (Go, Apache-2.0, embeddable as a
Go module, virtual-key/team/customer budgets that mirror our operator→team→owner
model, built-in semantic cache + MCP gateway + OTel); **LiteLLM** is the
batteries-included Python alternative. Everything else is secondary.

This doc is the **infra/plumbing** lens. It is distinct from its siblings:
[agent-side-tooling-landscape.md](agent-side-tooling-landscape.md) is about what
a single Claude Code / Codex instance ships with (skills/hooks megapacks);
[integrating-open-source-agents.md](integrating-open-source-agents.md) is about
*which engines* to spawn; [external-agent-services.md](external-agent-services.md)
is about consuming agent SaaS; [observability-gap.md](observability-gap.md)
(resolved → [ADR-022](../decisions/022-observability-surfaces.md)) is about the
*in-app* insights surface. This doc is about the third-party services that run
on our own hub/host-runner.

---

## 1. Where this plugs into what we already have

The recommendations are cheap precisely because they land on existing seams:

| TermiPod today | Cite | What the infra layer adds |
| --- | --- | --- |
| Host-runner sets each agent's environment at spawn | `plan_executor.go:210`, `launch_m2.go:337`, `driver_exec_resume.go:624` | Inject a gateway base-URL + per-team key, or telemetry env, **without touching the drivers** |
| Hub exports OTLP traces (operator opt-in) | [ADR-038](../decisions/038-per-run-event-digest.md), `hub/internal/otlptrace/trace.go`, `Config.OTLPEndpoint`, how-to [export-traces-to-otlp.md](../how-to/export-traces-to-otlp.md) | A backend to *receive* the traces, plus engine-native metric streams |
| Hub computes `cost_usd` per session from the transcript digest | [ADR-038](../decisions/038-per-run-event-digest.md), plan [agent-run-analysis-mode.md](../plans/agent-run-analysis-mode.md) | Cost measured **at the call site** (incl. cache-read tokens, ~90% cheaper, currently invisible) and *acted on* (budgets, routing) |
| Per-team budgets + owner tokens (operator→team→owner) | team management, v1.0.801-alpha | Gateway **virtual keys** map 1:1 to teams — enforce spend caps centrally |
| Host-runner spawns agents in **tmux panes, no isolation** | host-runner | A real sandbox boundary — directly relevant to the deferred WS4 ([internal-techdebt-cleanup.md](../plans/internal-techdebt-cleanup.md)) |
| Hub MCP catalog + dispatcher; host-runner MCP gateway hooks | `hubmcpserver`, `mcp_gateway_hooks_test.go` | Federation of *external* MCP servers under one governed endpoint |

---

## 2. LLM gateways — cost saving + unified spend (the big one)

A gateway sits between the engine and the provider. Because host-runner owns the
spawn env, we can route an engine through it by setting a base-URL — no driver
change. This is the single biggest unlock for cost and legibility.

| Tool | Lang | License | Fit |
| --- | --- | --- | --- |
| **Bifrost** (maximhq) | **Go** | Apache-2.0 | OpenAI-compatible **+ native Anthropic/Gemini passthrough**; **virtual keys → team/customer budgets** (mirror our model); semantic cache, fallback, load-balance; **built-in OTel + MCP gateway**; **embeddable** via `go get github.com/maximhq/bifrost/core`; ~11µs overhead at 5k RPS |
| **LiteLLM** (BerriAI) | Python | MIT | The de-facto standard; 100+ providers, per-key/team/tag budgets, spend dashboard, guardrails. Larger ecosystem; Python proxy hop (~4ms), heavier at high RPS |
| **Envoy AI Gateway** | Go/Envoy | Apache-2.0 | Only if already on Istio/Envoy; early, limited providers, no semantic cache |

**Why Bifrost for us specifically.** It is the only mainstream gateway that is
(a) **Go** — runs inside or beside the hub with no new runtime, even as a library;
(b) has a **virtual-key/team/budget hierarchy** that mirrors the operator→team→owner
structure we just shipped; and (c) bundles semantic cache + MCP gateway + OTel,
collapsing three of the layers below into one dependency.

**Integration sketch (host-runner, ~1 spawn-env change).** Per engine, set
`ANTHROPIC_BASE_URL=http://gateway/anthropic` (and a per-team virtual key as the
engine's API key). Cost, caching, routing, and budgets then apply uniformly
across Claude Code / Codex / Gemini / Kimi. The existing digest stays as a
cross-check, not the source of truth.

**Cost levers unlocked.** Semantic cache (GPTCache-style; vendors report 60–68%
call reduction on repeat work), fallback to cheaper models on overflow, and
**prompt-cache visibility** (cache-read tokens are ~90% cheaper and currently
unaccounted in our digest).

---

## 3. Observability backends — understanding agents

We already *emit* OTLP; we mostly need a **receiver** and to light up
engine-native telemetry. This complements, not replaces, the in-app insights
surface ([ADR-022](../decisions/022-observability-surfaces.md)).

- **Claude Code native OTel** *(adopt first)* — `CLAUDE_CODE_ENABLE_TELEMETRY=1`
  emits `claude_code.cost.usage` (USD/request) and token metrics with
  `model` / `agent.name` / `skill.name` / `query_source` attributes. Host-runner
  sets the env at spawn, pointed at the hub's existing endpoint or a collector.
- **Langfuse** (MIT, self-hostable) — prompt/eval/trace management; OTel-native;
  integrates with LiteLLM.
- **Arize Phoenix** (source-available) — OTel-from-the-ground-up; agent/RAG
  traces; hallucination eval; clean pure-trace viewer.
- **OpenLLMetry** (Traceloop) — vendor-neutral OTel instrumentation to stay
  backend-agnostic.
- **SigNoz / OpenObserve** — full OTel backends (logs+metrics+traces) that
  already publish Claude Code dashboards; OpenObserve ships a single-binary
  deploy that suits a self-hosted fleet.

---

## 4. Semantic caching & model routing — pure cost reduction

- **GPTCache** (Zilliz) — embedding-similarity semantic cache; claims up to 10×
  cost / 2–100× latency on hits.
- **RouteLLM** (LMSYS) — learned router; large cost cuts at ~95% GPT-4 quality,
  but **binary** (two-model) routing.
- **vLLM Semantic Router** (Red Hat) — newer; intelligent routing for
  self-hosted / open-weight models.

If we adopt Bifrost or LiteLLM, semantic caching is built in — reach for these
standalone only for a specialized router. The common pattern is RouteLLM
(decide model) + a gateway (execute, track, budget).

---

## 5. Sandboxing & isolation — host-runner hardening (ties to WS4)

Host-runner runs agents in tmux panes with no isolation. The 2026 sandbox layer
is directly relevant to the deferred WS4 structured-command work
([internal-techdebt-cleanup.md](../plans/internal-techdebt-cleanup.md)).

| Tool | Isolation | Cold start | Note |
| --- | --- | --- | --- |
| **E2B** | Firecracker **microVM** (kernel-level) | ~150ms p50 | Purpose-built for AI agents; strongest isolation |
| **Daytona** | container | ~27ms p50 | Stateful workspaces; fastest; weaker isolation |
| **microsandbox / Firecracker direct** | microVM | — | Self-hosted, no SaaS dependency |
| **container-use** (Dagger) | container + git worktrees | — | Per-agent isolated env + worktree — conceptually closest to our model |

For a NAT'd GPU box running untrusted agent output, microVM isolation
(E2B / Firecracker) is the security-grade option; container-use is the closest
philosophical match (worktree-per-agent, which we already do). A "later" item,
worth folding into WS4's scope on revisit.

---

## 6. MCP gateways — tool governance (we are partly here)

We have a hub MCP catalog + host-runner gateway hooks. External MCP gateways
matter when we want to **federate third-party MCP servers** under one governed,
audited endpoint:

- **Docker MCP Gateway** — proxy + catalog; stdio/SSE/HTTP transport
  translation; runs MCP servers in containers.
- **agentgateway** — built on **MCP *and* A2A** (we use both); drop-in
  security/observability/governance for agent↔tool and agent↔agent. Most
  architecturally aligned.
- **ContextForge / Microsoft mcp-gateway** — registry + reverse proxy; K8s
  session-aware routing.

---

## 7. Prioritized adoption shortlist

| Priority | Move | Where | Effort | Payoff |
| --- | --- | --- | --- | --- |
| **P0** | Claude Code native OTel → existing OTLP pipe | host-runner spawn env | XS | Real-time per-agent token/cost/cache metrics |
| **P0** | Stand up an OTel backend to view what we already emit (Phoenix or SigNoz) | hub-side ops | S | Immediately "understand agents" |
| **P1** | LLM gateway via base-URL injection (**Bifrost**) | host-runner spawn env + hub-side service | M | Central spend, **per-team budgets**, caching, fallback |
| **P2** | Semantic cache on (built into the gateway) | gateway config | XS | Direct cost reduction on repeat work |
| **P3** | Sandbox isolation (E2B / microsandbox / container-use) — fold into WS4 | host-runner | L | Safe untrusted-agent execution |
| **P3** | External MCP federation (agentgateway) | hub | M | Governed third-party tools, A2A-aligned |

---

## 8. Caveats

- **A gateway adds a hop and a failure mode.** Mitigate with native-passthrough
  mode + fallback; Bifrost's embeddable Go core avoids a separate process if we
  want.
- **Self-hosted, single-engine framing.** Our agents are CLI coding agents
  calling their own backends, so the gateway only helps for engines whose
  base-URL is configurable (Claude Code, Codex/OpenAI; Gemini varies). Confirm
  per-engine env support before committing.
- **Competitive watch, not adoption.** The trending *orchestrators* — OpenClaw
  (~210k stars), Agno, Bernstein, MS Conductor — overlap with what TermiPod *is*
  (a control plane). Track them as positioning input (see
  [positioning.md](positioning.md)), not as dependencies.

---

## 9. Open forks (for a follow-up decision)

1. **Gateway shape:** Bifrost **embedded as a Go module** in the hub, vs Bifrost
   / LiteLLM as a **sidecar** the host-runner points at. Embedded keeps the tree
   lean (one runtime, our preference per the OTLP-exporter precedent) but couples
   gateway upgrades to hub releases.
2. **Cost source of truth:** keep the transcript-derived digest as authoritative
   and treat the gateway as a cross-check, or migrate to gateway-measured cost
   (more accurate, incl. cache reads) and demote the digest.
3. **P0 first:** resolve to a small plan for Claude Code OTel injection against
   the spawn-env code, since it is XS-effort and unlocks the observability
   backend choice.

## Sources

External references gathered 2026-06-04:
[Bifrost](https://github.com/maximhq/bifrost) ·
[LiteLLM](https://github.com/BerriAI/litellm) ·
[Claude Code OTel monitoring (SigNoz)](https://signoz.io/blog/claude-code-monitoring-with-opentelemetry/) ·
[Claude Code monitoring docs](https://code.claude.com/docs/en/monitoring-usage) ·
[Langfuse](https://github.com/langfuse/langfuse) ·
[Arize Phoenix / observability roundup](https://openobserve.ai/blog/llm-observability-tools/) ·
[RouteLLM (LMSYS)](https://www.lmsys.org/blog/2024-07-01-routellm/) ·
[vLLM Semantic Router (Red Hat)](https://www.redhat.com/en/blog/bringing-intelligent-efficient-routing-open-source-ai-vllm-semantic-router) ·
[Daytona vs E2B sandboxes](https://northflank.com/blog/daytona-vs-e2b-ai-code-execution-sandboxes) ·
[Docker MCP Gateway](https://www.docker.com/blog/docker-mcp-gateway-secure-infrastructure-for-agentic-ai/) ·
[agentgateway](https://github.com/agentgateway/agentgateway)

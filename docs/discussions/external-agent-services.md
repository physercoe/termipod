# External agent services — three shapes, vendor snapshot, termipod fit

> **Type:** discussion
> **Status:** **Post-MVP** (decision logged 2026-05-12). Drafted
> 2026-05-12. Three integration shapes scoped; vendor snapshot for
> shape 2 (long-running external workers) included as §6. No code
> commitment.
> **Audience:** principal + future contributors evaluating how
> termipod consumes external agent services that solo / small-team
> users cannot self-host.
> **Last verified vs code:** v1.0.508
> **Sibling discussions:**
> [`aip-acps-china-standard.md`](aip-acps-china-standard.md) (the
> *be-consumed* direction — how termipod gets called by external
> hubs, deferred post-MVP), and
> [`voice-input-cloud-vs-offline.md`](voice-input-cloud-vs-offline.md)
> (overlap with the Cloudflare Voice Pipeline primitive).

**TL;DR.** As agent SaaS proliferates, termipod's solo/small-team
users will want to consume capabilities they can't self-host — cloud
Claude sessions, Cursor cloud coding agents, GitHub Copilot Cloud
Agent, plus a Cambrian explosion of vertical SaaS MCP servers
(Linear, GitHub, Notion, Slack, …). This decomposes into three
shapes, each with a different cost/benefit profile. **Shape 1 (tool
surface, MCP-shaped)** is essentially free today — engines already
support it natively; termipod's only job is documentation. **Shape 2
(long-running external workers)** is the genuine post-MVP wedge:
write one `external-agent` driver and pick three vendors that solve
distinct user problems. **Shape 3 (peer-hub federation)** is deferred
along with AIP/ACPs — only matters once termipod itself is consumed
by others. **Recommended Shape-2 vendor sequence:** Anthropic Managed
Agents first (highest user-value-per-LOC), Cursor Cloud Agents
second (coding-task niche), Cloudflare consumed as **Shape-1
primitives** rather than as a Shape-2 host (avoid TypeScript-only
lock-in). All three Shape-2 wedges are post-MVP.

---

## 1. Why this matters now

Termipod's current model assumes the team owns its host-runners and
runs its own steward. That's correct for the MVP — the system should
work for a solo dev with one Linux box and an Android phone — but it
caps the ceiling. Three forces push toward external consumption:

1. **Capability gap.** A solo dev cannot host a frontier-model coding
   sandbox, a browser-automation fleet, a 24/7 cron agent, or a
   research literature crawler. These exist as SaaS today.
2. **Cost gap.** Running a 7B–70B local model is rarely cheaper than
   API tokens once you account for hardware + power + idle.
3. **Specialization gap.** Vertical agents (Linear AI, Cursor cloud,
   Copilot agent, vendor MCP servers) ship faster than any team can
   replicate.

The MVP is right not to solve these. The question is how to keep the
door open without painting ourselves into a hub-only corner.

## 2. Three shapes, one decision tree

| Shape | What it is | Wire | Termipod role | Fit today |
|---|---|---|---|---|
| **1. External tool** | A function the steward invokes; returns one result | **MCP server** (de-facto), REST, gRPC | Hub configures + optionally proxies; engine often talks direct | **Already works** — engines support `.mcp.json` natively |
| **2. External worker** | A remote process that runs long, streams progress, holds state | A2A agent card, MCP with session, OpenAI Assistants, vendor REST | Hub treats it as a new `host_kind`; agent_events streams transparently | **Post-MVP wedge** — see §5 |
| **3. Peer hub federation** | Two complete agent ecosystems meet under cert-rooted identity | A2A federation, ACPs/AIP | Hub publishes cards + accepts inbound; cross-hub relay | **Deferred** — see [AIP discussion](aip-acps-china-standard.md) |

**Decision tree:** if the steward needs a single answer to a single
question, it's a tool (Shape 1). If it needs a collaborator that runs
for minutes-to-hours and streams partial progress, it's a worker
(Shape 2). If both ecosystems need to register fleets and discover
each other's capabilities under shared trust, it's federation (Shape
3).

## 3. Shape 1 — external tool surface (already works)

MCP-server consumption is settled in May 2026. Engines (claude-code,
codex, gemini) all read `.mcp.json` / `settings.json` / equivalent
and route tool calls accordingly. Three deployment modes for the
config + traffic, **none of which require new hub architecture**:

| Mode | `.mcp.json` source | Call path | Hub visibility | When to use |
|---|---|---|---|---|
| **1A. User-managed** | User edits host-runner filesystem | engine → external MCP, direct | Hub blind | Solo dev exploring |
| **1B. Hub-pushed config** | Hub writes `.mcp.json` at spawn time (already does this for hub-MCP entry) | engine → external MCP, direct (hub knew about it) | Knows the config; doesn't see per-call traffic | Team sharing service configs, not credentials |
| **1C. Hub-proxied** | Engine's `.mcp.json` points at hub URL; hub re-publishes external tools in its MCP namespace | engine → hub → external MCP | Every call audited, budgeted, credentialed | Org with shared creds + compliance |

**Cost to ship in termipod:**

- 1A: 0 LOC; documentation only (`docs/how-to/configure-external-mcps.md`,
  with worked examples for Linear / GitHub / Notion / Slack).
- 1B: ~100 LOC — extend host-runner spawn to inject team-configured
  MCP entries; new `external_mcp_services` table.
- 1C: ~300 LOC — hub-side proxy + credential vault + namespace
  re-publishing + audit + budget gate. Earns its weight only when
  compliance / shared-credential rotation matters.

**Most users will live in 1A or 1B forever.** Mode 1C is the
"termipod as enterprise trust boundary" play — keep it on the
roadmap, don't build it until a user pays for it.

### The Shape-1 vendor universe (already saturated)

As of May 2026, the hosted MCP-server market includes Linear, GitHub,
Notion, Slack, Stripe, Atlassian, Sentry, HubSpot, Neon, Vercel,
Supabase, Figma — all with OAuth and remote HTTPS endpoints,
one-click installable in claude-code / Cursor / ChatGPT. The list
grew from 16 servers in January 2026 to 25+ by April. Documentation
that points users at this universe is the single highest-leverage
move termipod can make in 2026.

## 4. Shape 2 — long-running external workers (the post-MVP wedge)

This is what the user's question was really about. The pattern: a
remote service runs for minutes-to-hours on the steward's behalf,
streams progress, may need approval mid-task, returns artifacts at
the end. Examples: Anthropic Managed Agents running a refactor;
Cursor Cloud Agent fixing a bug across the repo; GitHub Copilot
Cloud Agent landing a PR; Cloudflare Project Think running a
scheduled research crawl.

### Architectural shape

Termipod's existing driver / frame-profile pattern (ADR-010)
generalizes cleanly. Today drivers are YAML-described local
processes; add an **`external-*` driver family** where the spawn
binds to a remote endpoint instead of a child process:

```yaml
# example frame-profile fragment
kind: external-mcp-session
endpoint: https://api.anthropic.com/v1/managed-agents
auth:
  method: bearer
  secret_ref: managed_claude
session:
  create:  POST /sessions
  resume:  POST /sessions/{id}/resume
  cancel:  POST /sessions/{id}/cancel
events:
  stream:  GET  /sessions/{id}/events  # SSE
```

The hub's existing pieces apply unchanged:

- **agent_events** is the same — only the driver fan-out differs.
- **A2A relay** stays inbound-only for now; outbound is a Shape-3
  concern.
- **budget_cents** gates calls before they spawn.
- **audit_events** records cost + tokens + latency per call.
- **Credential vault** (the same primitive that holds SSH keys) holds
  per-service API keys; hub injects at proxy time, never exposes to
  the steward.

### What's actually missing

| Primitive | Status | Effort |
|---|---|---|
| `external_services` table (id, kind, base_url, auth, secret_ref, capability_manifest_url) | net-new | ~150 LOC |
| Credential vault entries for external services | reuses keys table primitive | ~50 LOC |
| One driver per vendor (Anthropic / Cursor / etc.) | net-new per vendor; YAML profile + Go shim | ~200 LOC each |
| Webhook callback endpoint (for async vendors) | net-new | ~150 LOC |
| Cost meter on audit_events (cost_cents, tokens_in/out columns) | half-built (budget_cents exists) | ~80 LOC |
| Per-service rate limit (token bucket per external service) | net-new | ~100 LOC |

**Total first-vendor wedge: ~700–800 LOC.** Subsequent vendors:
~100–200 LOC each (mostly YAML + a few API shims).

## 5. Shape 3 — peer hub federation (deferred)

Covered in [`aip-acps-china-standard.md`](aip-acps-china-standard.md).
Don't reopen until shapes 1 + 2 have shipped and termipod is valuable
enough that someone wants to *consume* our agents. The technical
shape (cards + cert chain + ADP-style discovery) is the same in both
directions; the AIP discussion's three wedges (identity, ACS publish,
ADP search) close this from termipod's side. RabbitMQ explicitly
non-fit; hub relay covers the multi-party-coordination role.

## 6. Vendor snapshot — May 2026

What's available as of writing, ordered by termipod attractiveness:

### Anthropic Managed Agents (Apr 8 2026 launch)

- **Wire**: REST API. Same tools as Claude Code (code execution,
  files, bash, web, MCP).
- **Pricing**: token rates + **$0.08 / session-hour active runtime**.
  Predictable, single line on the bill.
- **State**: persistent filesystem + conversation history across
  sessions.
- **Lock-in**: Anthropic-only infrastructure; no on-prem.
- **Termipod fit**: **highest**. Same model family our local
  Claude Code workers already use. Solo dev's "I want a cloud Claude
  task" answer is one driver away.
- **First driver to write.**

### Cursor Cloud Agents (Feb 24 2026 launch)

- **Wire**: REST. Basic-auth via API key. **Supports MCP** for tool
  integration with the agent.
- **State**: cloud VMs; persistent across launches.
- **Specialization**: repo-aware, parallel via git worktrees.
- **Termipod fit**: **high** for coding-specific cloud workers.
  The "kick off a task from your phone, come back later" UX matches
  termipod exactly.
- **Second driver to write.**

### Cloudflare — Project Think + Agents Week 2026 primitives

Cloudflare made the deepest push of any vendor at **Agents Week
2026** (mid-April). Key surfaces:

- **Project Think** (preview): durable execution with crash recovery
  (fibers), sub-agents with isolated SQLite + typed RPC, persistent
  sessions with FTS5 over message history, sandboxed code execution
  via Dynamic Workers. **Wire**: WebSocket (typed RPC via
  `@callable()`) + HTTP + SSE. **MCP-publishable** out of the box.
  **TypeScript-only SDK.**
- **Cloudflare Email Service** (public beta) — agents send / receive
  / process email natively. No SMTP to host.
- **Browser Run** — browser automation with Live View, CDP,
  Human-in-the-Loop, 4× concurrency. Cheaper than Browserbase.
- **Agent Memory** (managed) — persistent memory across sessions.
- **Sandboxes GA** — isolated shell + filesystem + background-process
  environments.
- **Voice Pipeline** (experimental) — STT/TTS over WebSocket. See
  [voice-input discussion](voice-input-cloud-vs-offline.md) for
  context.
- **AI Gateway** — unified inference proxy for 14+ model providers.
  Centralized cost / audit even when the steward picks different
  LLMs.
- **Managed OAuth for Access** (RFC 9728) — standard agent auth.

**Cloudflare's pitch**: edge-priced primitives a solo dev can compose.
**Termipod fit**: **high as Shape-1 primitives**, **medium as Shape-2
host**. Use CF Email / Browser / Voice / AI Gateway as tools that
the steward calls. Don't write business logic in Project Think —
that's TypeScript-only and locks termipod to CF.

### OpenAI on AWS Bedrock — Managed Agents (Apr 2026, limited preview)

- **Wire**: AWS Bedrock API (REST + SigV4 auth).
- **Pricing**: standard Bedrock token rates.
- **Strength**: OpenAI's agent harness + frontier models; cross-cloud
  via AWS infra.
- **Termipod fit**: **medium**. Requires AWS account + IAM. Better
  for AWS-native teams; awkward for solo devs on a single VPS.
- **Defer until a user asks.**

### OpenAI Codex Background Computer Use (Apr 16 2026)

- **Wire**: OpenAI Assistants API.
- **Specialization**: full macOS desktop control.
- **Termipod fit**: **low** — desktop-OS-specific; termipod is
  terminal-focused.
- **Skip.**

### Google Cloud — Gemini Enterprise Agent Platform (Cloud Next 2026)

- **Rebrand** of Vertex AI / Agentspace.
- **Wire**: A2A protocol (production-grade), managed MCP servers
  across GCP services.
- **Strength**: native A2A means future-proof if A2A wins the
  cross-vendor war.
- **Pricing**: GCP enterprise — annual commits typical.
- **Termipod fit**: **lower for MVP** (enterprise-pitched); **watch**
  because if A2A becomes the lingua franca, this is termipod's
  natural Shape-3 peer.

### GitHub Copilot Cloud Agent

- **Wire**: GitHub Actions environment; PR / issue-driven (no direct
  REST endpoint to spawn an agent today).
- **Pricing**: bundled with Copilot subscription — effectively free
  for users who already have one.
- **State**: isolated dev environment per task; results land as PRs.
- **Termipod fit**: **high for repo-bound work**. Plumbing path:
  termipod hub's GitHub MCP server creates an issue with
  `@github-copilot ...`; Copilot picks it up; status streams back
  via GitHub webhooks. **Zero infra cost** for users who already pay
  for Copilot.
- **A clever Shape-1.5 — not quite tool, not quite worker — but
  reachable via the existing GitHub MCP server today.**

### Awareness-only

| Vendor | Note |
|---|---|
| **Amazon Bedrock** (non-OpenAI) | Many models, no agent harness lock-in; commodity inference layer |
| **Anthropic Claude Skills** (CLI bundled) | Already integrated via local Claude Code driver |
| **Lutra / Lindy / Sema4** | Vertical agent SaaS; consume via webhooks or MCP if available |
| **Linear AI / Notion AI / Slack AI** | First-party MCP servers — Shape 1, already covered |

## 7. Cross-cutting primitives

Build once, reuse across all Shape-2 vendors:

- **`external_services` registry table** — id, kind, name, base_url,
  auth_method (none / bearer / oauth2 / mtls), secret_ref,
  capability_manifest_url. REST CRUD + mobile screen for the user
  to add vendors.
- **Credential vault wiring** — reuses the keys-table primitive.
  Hub-side encryption; never exposed to steward prompt.
- **Cost meter** — audit_events grows `cost_cents`, `tokens_in`,
  `tokens_out`. `budget_cents` per project becomes a hard gate.
- **Egress audit** — every outbound call to an external service is
  an audit_event with service name, project, run_id, cost, latency.
  The receipt that lets users trust hub with credentials.
- **Per-service rate limits** — hub-side token bucket prevents burst
  overages; many vendors charge by RPS.
- **Webhook callback endpoint** — `POST /v1/external/{service}/callback/{run_id}`
  for async vendors that call back with results.

## 8. Recommendation — sequenced post-MVP wedges

**Decision (2026-05-12):** all wedges below are **post-MVP**. No
code commitment. Sequence reflects user-value-per-LOC.

### Free move (Mode 1A documentation)

- Write `docs/how-to/configure-external-mcps.md` with worked
  examples for Linear / GitHub / Notion / Slack / Stripe / Atlassian.
- **Cost**: 0 LOC, ~100 lines of doc. Users get external agents
  immediately.

### Wedge 1 — Shape-1 hub-pushed config (Mode 1B)

- `external_mcp_services` table + host-runner spawn injects entries
  into engine `.mcp.json` / `settings.json`.
- **Cost**: ~100 LOC. Hub knows configs; doesn't proxy traffic.
- **Value**: team-shared service catalog; one place to add Linear
  credentials, propagates to every host-runner.

### Wedge 2 — Shape-2 first driver (Anthropic Managed Agents)

- Cross-cutting primitives (§7): `external_services` table,
  credential vault, cost meter, audit, webhook.
- One `external-mcp-session` driver mapping Anthropic Managed
  Agents REST onto agent_events.
- **Cost**: ~700–800 LOC including primitives (one-time).
- **Value**: termipod can spawn a cloud Claude session as a worker.
  Solo dev's biggest "I can't host this myself" answer.

### Wedge 3 — Shape-2 second driver (Cursor Cloud Agents)

- New driver only; primitives reused.
- **Cost**: ~150–200 LOC.
- **Value**: long-running coding tasks complementary to local
  Claude Code workers.

### Wedge 4 — Shape-1 hub-proxied (Mode 1C)

- Only build when compliance / shared-credential rotation lands as
  a real requirement.
- **Cost**: ~300 LOC.

### Watch list (no commitment)

- **A2A protocol stabilization.** If A2A wins, the Shape-2 driver
  template becomes a single A2A-client implementation; vendor-specific
  drivers retire.
- **Cloudflare Project Think GA.** If CF ships a Go SDK or a stable
  REST surface, the email / browser / voice primitives become much
  more attractive.
- **Cross-vendor MCP session resumption.** Right now session state
  is vendor-specific. A standard for "resume this conversation on a
  different vendor" would change termipod's architecture
  significantly.

## 9. Open questions

These are the unresolved design decisions if/when Wedge 2 lands:

1. **Credential scope.** Per-project? Per-team? Per-user? Probably
   per-team (single Linear/GitHub creds per team), but per-user
   makes sense for personal OpenAI / Anthropic accounts.
2. **Steward visibility.** Does the steward see "this came from
   Anthropic Managed" or just see results? Best for transparency,
   worst for vendor portability.
3. **Cost attribution UI.** Mobile screen showing per-vendor spend
   per project? Probably yes — users want to know which external
   agent is burning their budget.
4. **Failure semantics.** External vendor outage during a long task:
   resume on a different vendor, retry on same, or surface to user?
   Per-driver policy or hub default?
5. **Streaming back-pressure.** A noisy external worker can flood
   agent_events. Same SSE-bandwidth concern that
   [`post_mvp_bandwidth_privacy`](../../) addresses — rate-limit at
   the hub.

These are documented now so a future implementation has them ready
to answer; none block the MVP.

---

## Sources

- [Cloudflare Project Think — blog announcement](https://blog.cloudflare.com/project-think/) (Apr 2026)
- [Cloudflare Agents Week 2026 — full update list](https://www.cloudflare.com/agents-week/updates/)
- [Cloudflare Agents docs](https://developers.cloudflare.com/agents/)
- [Claude Managed Agents — Anthropic platform docs](https://platform.claude.com/docs/en/managed-agents/overview)
- [Claude Agent SDK overview](https://code.claude.com/docs/en/agent-sdk/overview)
- [Amazon Bedrock OpenAI Managed Agents](https://aws.amazon.com/about-aws/whats-new/2026/04/bedrock-openai-models-codex-managed-agents/)
- [Google Cloud Next 2026 — Gemini Enterprise Agent Platform](https://thenextweb.com/news/google-cloud-next-ai-agents-agentic-era)
- [Cursor Cloud Agents API docs](https://cursor.com/docs/cloud-agent/api/endpoints)
- [GitHub Copilot Cloud Agent — GitHub blog](https://github.blog/news-insights/product-news/github-copilot-meet-the-new-coding-agent/)
- [Awesome MCP Servers — registry](https://github.com/appcypher/awesome-mcp-servers)
- [25+ remote MCP servers list, 2026](https://blog.premai.io/25-best-mcp-servers-for-ai-agents-complete-setup-guide-2026/)

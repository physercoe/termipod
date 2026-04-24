# TermiPod — Positioning & Business Analysis

**Status:** Draft for review, 2026-04-24.
**Sources:** `docs/blueprint.md` (canonical thesis), `docs/ia-redesign.md` (mobile IA), `docs/ux-steward-audit.md`, `docs/research-demo-candidates.md`, competitive research April 2026.

This document answers seven questions:

1. What is TermiPod really?
2. Who is the buyer and what is their pain?
3. What is the killer/unique differentiator?
4. When would a user pick TermiPod *instead of* Claude Code Remote Control / Happy / Tactic Remote / OpenHands / CrewAI?
5. One-sentence, one-minute, and elevator-demo pitches.
6. What explicitly isn't TermiPod (non-goals).
7. Distribution, SEO, and go-to-market.

---

## 1. What is TermiPod?

**Not** a mobile SSH client. Not a mobile Claude Code client. Not an agent framework.

> **TermiPod is a mobile-first control plane for a fleet of AI agents distributed across multiple machines, where a single human acts as director and a steward agent coordinates the work on their behalf.**
> — paraphrase of blueprint §1.

Three layers, each resisting a specific failure mode (blueprint §3.4):

- **Hub** (Go daemon) — authority layer. Stores names, policies, events, references. *Not bytes.*
- **Host-runner** — deterministic local deputy on each host. Spawns agents, owns panes, enforces budget/policy, relays agent↔hub calls through an MCP gateway.
- **Agent** — the stochastic executor (Claude Code, Codex, Aider, any LLM-driven CLI).

Plus an **A2A protocol** so a steward on a VPS can delegate a train run to a worker on a NAT'd GPU box via a reverse-tunnel relay.

The mobile app is the **director's cockpit** over this stack. SSH/tmux exists but is a "maintenance hatch reachable from inside" (IA axiom A6) — the breakglass, not the product.

---

## 2. Who buys it, and what's their pain?

### Primary ICP — the Director

Blueprint §1: *"researcher or small team acting as principal to a fleet of agent ICs."*

Concrete archetypes, ordered by immediacy of pain:

| ICP | Day-to-day | Why they hurt today |
|---|---|---|
| **Solo ML researcher** running nightly sweeps | writes goals, reviews briefings, ratifies gpu spend | has a VPS + a home GPU box; no tool spans both, no tool gives a phone-glance of overnight runs |
| **Indie AI hacker** running multiple coding agents | juggles Claude Code, Codex, Aider across projects | each tool has its own session; no unified attention queue; mobile apps cover 1 session at a time |
| **Small autonomy-focused startup (1–5 engineers)** | CTO wants to delegate to agents with governance | needs budget caps, audit log, approvals — no existing tool has them |
| **Open-source maintainer** | runs triage/review agents on issues overnight | wants to approve agent PRs from bed; current tools need laptop awake |
| **Homelab enthusiast with a GPU box** | ML experiments + self-hosted infra | wants to pull work out of the house and onto their phone without exposing raw SSH to the internet |

### The pains, ranked

1. **Review bottleneck.** Blueprint axiom A1: *"Human attention ≪ agent output."* One human cannot skim 6 training run logs, 4 code review diffs, and a weekly summary in the course of a workday. Every competitor dumps terminal bytes; TermiPod filters through the steward into a ratify/reject queue.
2. **Multi-host reality.** Serious agent work spans a VPS (always-on steward), a GPU box (training), sometimes a Mac (iOS builds), sometimes a CI runner. Every other mobile tool assumes "one local machine, keep it awake."
3. **Vendor lock-in.** Remote Control ties to Anthropic plans. Happy wraps Claude/Codex CLIs. Tactic Remote is iOS-only and closed. TermiPod runs *anything* that speaks to a pty — including your homegrown shell script.
4. **Phone is the only ubiquitous device.** Laptops die. Phones don't. IA axiom A1: *"The phone is opened in glances, not sessions. Hundreds of sub-minute interactions per day, not eight hours at a desk."* Every competitor still models the mobile client as a session bridge, not a glance surface.
5. **No governance layer.** No existing mobile agent tool has: budget caps, per-agent usage rollups, policy overrides, immutable audit log, multi-member teams, per-member stewards. TermiPod ships all of these.
6. **Flaky networks.** Subway, airplane, hotel wifi. Remote Control polls the Anthropic API; drops = black screen. TermiPod has an SQLite snapshot cache (shipped v1.0.204–208) — list/detail screens show last-known-good with "Offline · last updated HH:MM" banner.

---

## 3. The killer/unique differentiator

One sentence:

> **Everyone else gives you one agent in your pocket. TermiPod gives you a team of agents across your own infrastructure, coordinated by a steward on your behalf — and a phone-shaped cockpit to direct and ratify without operating.**

Decomposed:

| Dimension | Remote Control / Happy / Tactic | TermiPod |
|---|---|---|
| Topology | 1 phone ↔ 1 session ↔ 1 host | 1 director ↔ N agents ↔ M hosts |
| Agent count | 1 active at a time | Fleet; steward spawns more |
| Host span | Single local machine | VPS + GPU + Mac + CI, coordinated via A2A |
| Agent vendor | Claude Code (+ Codex for Happy) | Agent-agnostic — any CLI that takes a pty |
| Authoring model | User types messages | User writes a goal; steward decomposes into a plan |
| User posture | Operator | Director — ratifies, doesn't operate |
| Governance | None | Policies, budgets, audit, team roles |
| Data ownership | Cloud relay or laptop-only | Hub holds names/events; hosts hold bytes; blueprint §4 law |
| Offline | Requires relay | SQLite snapshot cache; last-known-good on every list |
| Open source | Happy: yes; others: no | Apache 2.0, self-hosted Go hub |

**The thing none of them can copy cheaply:** TermiPod's three-layer split (hub = names, host-runner = deterministic deputy, agent = stochastic executor) plus A2A routing. Remote Control *can't* add multi-host or multi-vendor without becoming a different product. Happy can't add governance without a hub. OpenHands can't become a mobile director-cockpit without rebuilding its UX top-to-bottom. TermiPod is what you get when you start from "the phone is the cockpit and the agents are distributed" as first principles.

---

### Second competitive axis: messenger-bridge agents

A different category of "AI agent on your phone" ships as a bot inside messengers you already have:

| Product | What it is | Status |
|---|---|---|
| **OpenClaw** | MIT, self-hosted, bridges a personal agent across 15+ messengers (WhatsApp / Telegram / Discord / Slack / Signal / iMessage / WeChat / LINE…). Pluggable model backend. 247k GH stars. | Viral 2026. Core maintainer Steinberger joined OpenAI Feb 2026; non-profit foundation taking stewardship. |
| **Hermes Agent** (Nous Research) | Open-source self-improving agent with persistent memory, cross-platform memory sharing, built-in cron scheduler, skills-from-experience. Telegram / Discord / Slack / WhatsApp / Signal / CLI. | Released Feb 25 2026, 95k stars in 7 weeks. |
| **Claude Code Channels** (Anthropic) | Anthropic's answer to OpenClaw: bridge a local Claude Code session to Telegram + Discord. Plugin architecture; Slack / WhatsApp / iMessage on roadmap. | Research preview, March 2026. Requires Claude Pro/Max plan. |
| **3P Telegram/WhatsApp bots** | `claude-code-telegram`, Emergent Wingman, nanobot, dozens of others | Cambrian. |

These are **not** the same category as TermiPod, but they answer the same user question — *"how do I use AI agents from my phone?"* — with a different thesis. Worth reading carefully.

**The messenger-bridge thesis:** don't ship an app. Use the chat apps the user already has open 20 hours a day. Let the LLM session be the UI. One agent, cross-platform memory, no new icon on the home screen.

**Where messenger bridges win over TermiPod:**

- **Zero new UI to learn.** The user already knows Telegram.
- **Personal-assistant use case.** "Remind me to ping dentist Tuesday", "summarize HN overnight" — text in, text out. TermiPod is over-built for this.
- **Always-available.** The messenger is already on every device. No TermiPod install step.
- **Cross-platform continuity.** OpenClaw/Hermes remember you identically on WhatsApp, Telegram, Slack. TermiPod is phone-first.
- **Passive notifications feel native.** Cron jobs that deliver a Telegram message are frictionless.

**Where TermiPod wins over messenger bridges:**

- **Fleet coordination can't live in a chat bubble.** Six parallel training runs, each with live loss curves; a kanban of tasks filterable by status; a steward plan with structured steps and cost estimates — all of these need rich UI that Telegram can't render. Messenger bridges degrade to `"run 1: loss=2.1", "run 2: loss=2.0"` text that you re-read to track.
- **Plan ratification is structured, not conversational.** "Approve plan?" with a 6-step scaffold + cost estimate is a different decision shape than "shall I proceed?" in chat.
- **Multi-host routing.** OpenClaw runs on one host; it skills up inside itself. TermiPod has a steward (VPS) delegating to workers (GPU) via A2A — a distributed topology, not a single-agent chat.
- **Governance surfaces.** Budget caps, policy overrides, audit log, team roles, usage rollups — all need dedicated screens. Messengers can render the audit log but can't *be* the governance cockpit.
- **Offline glance.** Messenger bridges need a live relay. TermiPod's SQLite snapshot cache shows last-known-good lists on a subway.
- **Purpose-built attention queue.** "Attention" (ratify/review) ≠ "unread chat messages". Separating them is a feature, not a constraint.
- **Provenance and audit.** Blueprint axiom A3 (governance) requires every autonomous action be traceable. A Telegram thread is not an audit log — it's a chat log that mixes human and agent turns with no schema.

**Rule of thumb for the README:**

> Use a messenger bridge (OpenClaw, Hermes, Claude Code Channels) when you want a personal agent that answers on the apps you already use. Use TermiPod when you're directing a fleet of agents across your own infrastructure and need a purpose-built cockpit with governance.

The two categories can coexist on the same phone. Many TermiPod directors will also run OpenClaw for personal-assistant tasks. TermiPod does not aim to be the chat surface for casual asks.

---

## 4. When should a user pick TermiPod — and when shouldn't they?

### Pick TermiPod when

- You run **two or more agent CLIs** and want one inbox.
- You have work that spans a **VPS + a GPU box + your laptop** and want it coordinated.
- You're a **researcher running nightly sweeps** and want to kick them off from your phone and review in the morning.
- You're a **small team** sharing a budget, needing policy enforcement or audit trail.
- You want **open source, self-hosted, no vendor account** gating mobile access.
- You work in **flaky-network environments** and need offline glance-review.
- You want **AI agent governance** — approval queues, budget caps, tier gating — and no product has it yet.
- You want to use **any LLM CLI** including homegrown shell tools without adopting a new agent framework.

### Use Remote Control, Happy, or Tactic Remote instead when

- You run **exactly one** Claude Code (or Codex) session on **one** machine.
- Your laptop is always on and always connected.
- You're paying for Claude Max anyway and just want the official path.
- You want **voice control** (Happy has it; TermiPod doesn't).
- You're not running multiple agents or multiple hosts and don't need governance.

### Use OpenClaw / Hermes / Claude Code Channels instead when

- You want a **personal assistant** that answers on Telegram / WhatsApp / Discord / Slack — not a fleet cockpit.
- Your primary interaction is **chat** (text in, text out), not plan-ratify-review.
- You don't want to install another app — the chat apps you already open 20 hours/day are enough.
- You want **one agent with cross-platform memory** (same "self" on WhatsApp and Telegram), not a director/steward/worker topology.
- Your work is **unbounded and conversational** (remind me, summarize, rewrite) — not research-ops with provenance.

Being honest about all three saves us from trying to be everything and being nothing.

### Use OpenHands / CrewAI / LangGraph instead when

- You want to **write** an agent framework, not **direct** a fleet.
- You need **web UI**, not mobile.
- You're building a product on top; TermiPod is a director's harness, not a framework to embed.

---

## 5. The pitches

### One sentence

> **TermiPod is a mobile control plane for fleets of AI agents across your own hardware — write a goal on your phone, a steward turns it into a plan, and a team of agents executes while you ratify.**

### One minute

Claude Code, Codex, and Aider write code ten times faster than you can review. The moment you run more than one — or more than one machine — the mobile tools break down: Anthropic's Remote Control is a 1:1 bridge to a single local session, Happy wraps one CLI at a time, Tactic Remote is iOS only. None of them span hosts, none coordinate multiple agents, none have governance.

TermiPod is built on a different axiom: the user is the director, not the operator. You write a natural-language goal on your phone — "ablation sweep on nanoGPT, tell me which optimizer scales better." A steward agent running on your VPS decomposes it into a 6-step plan. You tap Approve. The steward delegates training runs via A2A to your GPU box, even if it's behind NAT. Three hours later, your Inbox has one attention item: a briefing with loss curves and a recommendation. You ratify and go back to dinner.

Everything stays on your infrastructure — the hub is a Go daemon you host, agents run where compute lives, the mobile app is open-source Flutter. Every action is audit-logged, every budget is policy-enforced, and when you're offline the SQLite snapshot cache still shows you the last-known-good dashboard. Agent-agnostic, multi-host, multi-tenant, offline-first, Apache 2.0.

### 90-second elevator demo

Physical setup: a $5/mo Hetzner VPS, a home GPU box behind NAT, a phone. Hub daemon running on the VPS. Host-runner on the GPU box. TermiPod app on the phone.

1. **+ New Project**, pick **ablation-sweep** template, type: *"Compare AdamW vs Lion on nanoGPT at 3 model sizes — tell me which scales better."*
2. Steward (on the VPS) responds with a 6-step plan: `fetch_repo`, `make_worktree`, `generate_configs`, `a2a.delegate(worker.train x6)`, `collect_metrics`, `brief`. Each step has a cost estimate. Tap **Approve**.
3. First A2A invoke fires on the activity feed: `{target: worker@gpu.train, config: adamw-128}`. Switch to **Runs** tab — six training runs appear, trackio sparklines stream loss curves live.
4. Close the app. Go to dinner.
5. Three hours later, phone pings: **"1 pending attention."** Open Inbox. A briefing document is waiting: *"Lion scales better above 8M params. AdamW wins below 2M. Here's the plot."* Markdown with inline loss-vs-steps figures.
6. Tap **Approve**. The briefing archives. Spend: $2.41 of a $10 budget.

What happened: a steward on a $5 VPS coordinated 6 GPU training runs at home while you were eating. You didn't write a DAG, didn't SSH into anything, didn't babysit a session. You directed, ratified, reviewed.

---

## 6. Non-goals

Explicit from blueprint §10 and `research-demo-gaps`:

- **Not competing with Claude Code or Codex on single-agent UX.** *They are the agents; we are the control plane.*
- **Not competing with W&B on plotting breadth.** Trackio / W&B / TensorBoard sparklines are embedded, not replaced.
- **Not a general LLM chat app.** TermiPod is for bounded agent work with provenance, not open-ended conversation.
- **Not IDE-integrated.** The IDE is the agent's concern, not the director's.
- **Not a solo-developer-on-one-laptop tool.** The governance/audit/multi-host features have cost; users who don't need them should use Claude Code Remote Control instead.

These non-goals are a feature, not an omission. They keep TermiPod's scope honest.

---

## 7. Distribution & SEO

### Current state (diagnosis)

- README leads with "Mobile SSH terminal — built for tmux and AI agents." This was true a year ago; today it understates the product by one layer. A reader searching for "mobile agent control plane" bounces; a reader searching for "mobile ssh tmux" arrives but doesn't see the hub story until line 121.
- Comparison table benchmarks against Termux / JuiceSSH / Termius / ConnectBot — all SSH clients. None of these are the real competitors in 2026. Claude Code Remote Control / Happy / Tactic Remote should be the table.
- No positioning on the landing page for "multi-agent," "director," "steward," "fleet," "governance." These are the terms the ICP searches for.

### SEO terrain (April 2026)

Owned by incumbents:

- *"mobile Claude Code"*, *"Claude Code on phone"*, *"Claude Code remote"* — Anthropic (Remote Control) and Happy dominate.

Open territory (low competition, high ICP relevance):

- *"open-source Claude Code mobile self-hosted"* — partially Happy; room for TermiPod.
- *"multi-agent mobile control plane"* — open.
- *"AI agent governance mobile"* — open.
- *"director phone fleet of agents"* — open.
- *"steward agent coordination phone"* — open.
- *"self-hosted AI coding agent dashboard"* — lightly contested (OpenHands web UI, no mobile).
- *"mobile SSH tmux AI agents"* — TermiPod already ranks.

### Recommended README changes

1. **Replace the one-line pitch.** Stop leading with "Mobile SSH terminal." Lead with: *"Mobile control plane for fleets of AI agents — direct from your phone, a steward coordinates the work, you ratify and review."*
2. **Add a "What makes it different" section above the screenshots.** 3 bullets: multi-agent fleet, multi-host via A2A, self-hosted Apache 2.0. Link to the elevator demo.
3. **Add the real 2026 comparison table.** Keep the SSH table as a secondary section for users arriving from SSH queries.
4. **Put the Termipod Hub section second, not inside "Features".** It's the headline, not a sub-feature.
5. **Add a "30-second demo" block near the top** — the phone-in-restaurant story from §5, abbreviated.
6. **Add meta keywords / repo topics**: `claude-code-mobile`, `codex-mobile`, `multi-agent-mobile`, `ai-agent-governance`, `self-hosted-ai-agents`, `director-agent-fleet`.

### Distribution channels ranked by ICP reach

| Channel | Why | Effort |
|---|---|---|
| **GitHub README + Topics** | ICP searches here first; free | Low (this doc) |
| **Anthropic Claude Code community** (Discord, subreddit) | Bingo ICP — already on Claude Code | Medium |
| **r/LocalLLaMA, r/MachineLearning** | Solo researcher ICP | Medium (post the demo video) |
| **HN Show HN** | Hits when the pitch is sharp enough | One shot — do after README refresh |
| **Anthropic partner page / MCP registry** | If we expose MCP tools, we list there | Low once plumbing exists |
| **Product Hunt** | Awareness more than activation | Low leverage alone, useful paired with HN |
| **Tech-Twitter researcher circles** | Direct line to solo-researcher ICP | Medium — pick 5 voices who care about agent ops |
| **arxiv paper + demo repo** | Long-horizon credibility for research ICP | High; consider once demo is bullet-proof |

Deferred: App Store / Play Store featuring. Not high leverage until the demo is one-tap and the Hub has a one-line install.

### Metrics to measure

- `+ New Project` → Approve → first A2A invoke: end-to-end under 90 seconds from cold phone.
- Weekly active directors (= sessions with ≥1 Approve tap).
- Hub-host deployments (proxy for self-hosted adoption).
- Stars-to-install ratio — low means strong pitch converts.

---

## 8. Risks & open questions

- **Setup complexity.** Competitors: install app, paste token, done. TermiPod: install hub on a VPS, install host-runner on each host, paste URL+token into phone. This is the steepest friction. Mitigation: one-liner install script (`curl | sh`) for the hub + a "Quick Start on DigitalOcean" tutorial.
- **Demo dependency on GPU.** The research demo needs a GPU box. We've shipped `mock-trainer` (project memory) to cover the demo without hardware, but the "wow" moment is weaker. Real ICP has the hardware; content for the broader audience needs the mock path.
- **Anthropic Remote Control gap narrows.** If Anthropic adds multi-session and team features, our moat compresses. Our durable moat is the three-layer architecture + multi-vendor + self-hosted + open-source. Invest there, not in matching their features.
- **"Steward" is unfamiliar vocabulary.** Users understand "agent" and "assistant." "Steward" lands after 30 seconds of explanation. README should use both terms and let readers map them.
- **iOS distribution.** Android APK ships; iOS is source-build only. TestFlight is the unlock; App Store is later. The Claude Code mobile story is primarily iOS-led, which pressures this.

---

## Appendix A — Elevator-demo checklist (for video production)

- Hub daemon running on VPS (screen share of `hub serve` log in tiny corner to show it exists)
- Host-runner on GPU box (or mock-trainer if GPU unavailable)
- Phone portrait-recorded over-the-shoulder; never show laptop as operator
- Start on TermiPod Home; do NOT pre-seed the project
- Total runtime target: 75 seconds phone-side + 15 seconds of "what just happened" voiceover

## Appendix B — Copy variants for social / PH / HN

- **HN title:** *"Show HN: TermiPod — mobile control plane for a fleet of AI agents"*
- **PH tagline:** *"Direct agents from your phone. Your VPS coordinates, your hardware runs the work, you ratify."*
- **Twitter bio:** *"Your agents on your hosts, directed from your phone. Open source. Apache 2.0."*
- **Repo description:** *"Mobile control plane for AI agents across your own hardware. Director + steward + fleet. Open source (Apache 2.0)."*
- **One-liner disambiguation (for readers arriving from OpenClaw / Hermes searches):** *"OpenClaw and Hermes put an agent in your messengers. TermiPod puts a fleet cockpit in your phone. Different shapes of 'AI agents on mobile'."*

# Research-demo lifecycle: idea → paper, agent-authored, director-curated

> **Type:** discussion
> **Status:** Resolved (2026-04-30) → [`decisions/001-locked-candidate-a.md`](../decisions/001-locked-candidate-a.md) (amended) + [`decisions/016-subagent-scope-manifest.md`](../decisions/016-subagent-scope-manifest.md)
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** The MVP demo's locked candidate (ablation sweep + briefing,
[ADR-001](../decisions/001-locked-candidate-a.md)) was *architecture-
sharp* but *research-dull* — it terminated one phase short of looking
like research. The 2026 landscape (IKP/01.me, Sakana AI Scientist v2,
Google PaperOrchestra) sets a clearer bar: **idea → lit-review → method
→ experiment → paper, all delegated to agents, gated on a phone.** The
amended demo covers the full lifecycle. Substeps are simplified for
MVP; the end-to-end shape is preserved. Architecturally this requires
**no schema change**: it lands as templates, prompts, one MCP tool
group, one mobile screen, one role-gating middleware, and a documented
non-goal around budget + secret-bearing tools. This doc captures the
design and the decisions it locked.

---

## 1. Why this discussion exists

Three things forced a re-look at the locked candidate:

1. The 2026 case studies (single-author, multi-agent, days-not-months)
   showed that *agent-driven research lifecycle* is the implicit demo
   bar set by competitors. Sakana's AI Scientist v2 ships
   workshop-level papers; Google's PaperOrchestra (April 2026) takes
   raw experimental logs + an idea and writes the paper, beating AI
   Scientist v2 by 39–86% on overall paper quality. The IKP paper
   (`01.me/research/ikp/`, Bojie Li, 2026) is a representative solo
   case: 1 director, multi-agent, ~4 days, real published artifact.
2. Termipod's existing demo (Candidate A) hit only *phase 4* of that
   lifecycle (the experiment). A reviewer watching it sees agents run
   a benchmark grid + emit a digest — accurate but not what the
   landscape calls *research*.
3. The architecture *already supports* the full lifecycle (plans with
   phases, `human_gated` boundaries, `agents.spawn`, A2A reporting,
   documents + reviews). The gap is content (templates + prompts), not
   primitives.

The user's framing — *director gives an idea; steward proposes a plan;
director approves; phase by phase, steward spawns subagents, results
flow up, director gates each boundary; loops happen inside phases* —
maps onto blueprint §6.2 (plans are shallow; iteration lives inside
`agent_driven` phases) without any new primitive.

---

## 2. The 2026 landscape (snapshot, why this matters)

| System | Shape | What it produces | Fits termipod? |
|---|---|---|---|
| **Sakana AI Scientist v1/v2** | Idea → experiments → paper → review | Full paper, ~$15 | Single-host, single-engine; orthogonal to our differentiation. |
| **Google PaperOrchestra (2026-04)** | Idea + experimental logs → paper, with AgentReview iterative critic | Paper; +39–86% quality vs Scientist v2 | Closest scoped analogue: doesn't run experiments, just writes. We have logs from phase 4 → adopt this shape for phase 5. |
| **Sakana Fugu (2026)** | Multi-agent orchestration over frontier models | Strong results across coding/math/reasoning | Validates orchestration-as-product positioning. |
| **Claude Code Agent Teams (preview)** | Orchestrator + subagents, parallel methodologies | In-session multi-agent | Engine-internal subagents — *not* termipod-managed (see §7). |
| **IKP (Bojie Li, 2026)** | Solo human, multi-agent, ~4 days, arxiv:2604.24827 | Real published paper | The representative *director-on-phone* case the demo should match in shape. |

The bar is not "match Sakana on autonomous discovery." It is **"produce
paper-shaped output from delegated work, directable from a phone, with
multi-host + multi-engine + governance — the axes single-engine clients
don't have."**

---

## 3. The 5-phase lifecycle (the demo)

Plan template `research-project.v1`. Linear, shallow per blueprint §6.2.
Iteration lives inside each `agent_driven` phase, hidden from the
plan-level director view by the steward.

| # | Phase | Kind | Steward's job | Subagents | Phase artifact | Gate |
|---|---|---|---|---|---|---|
| **0** | **Bootstrap** | `agent_driven` (general steward only; no subagents) | Read director's idea, draft a 5-phase plan + the domain-steward template + worker templates customized to the idea | none | Plan proposal + draft templates | `human_gated`: approve plan + templates? |
| **1** | **Lit Review** | `agent_driven` (domain steward) | Spawn 1–3 `lit-reviewer.v1` workers; aggregate findings into a synthesis doc | `lit-reviewer.v1` × N | Lit-review document — what's known, what's open | `human_gated`: review acceptable? |
| **2** | **Method & Code** | `agent_driven` | Spawn `coder.v1` to write training/eval code; iterate code-review until tests pass; freeze experiment matrix | `coder.v1`, optional `critic.v1` | Frozen experiment spec + code worktree commit | `human_gated`: approve method? |
| **3** | **Experiment** | `agent_driven` | Spawn `ml-worker.v1` × N (existing template, the original Candidate A); collect run digests | `ml-worker.v1` (existing) × N | Run digests + result-summary doc | `human_gated`: results convincing? iterate or proceed? |
| **4** | **Paper** | `agent_driven` | Spawn `paper-writer.v1` to consume digests + lit-review + method docs → 6-section paper. Optional `critic.v1` revise-loop | `paper-writer.v1`, optional `critic.v1` | Paper document attached to project | `human_gated`: approve paper? (project closes) |

The original Candidate A's ablation sweep is now **phase 3** — same
compute, same A2A path, embedded in the lifecycle.

Subagent reports flow: subagent → A2A `message/send` → steward →
`documents.create` → `attention.create` (pause-for-approval) → director
on phone → `input.attention_reply` resumes the plan. All shipped.

---

## 4. Design decisions locked

**D1. Lifecycle replaces single-phase.** ADR-001's locked candidate
becomes the 5-phase lifecycle. Original ablation sweep is preserved as
phase 3.

**D2. Layered stewards.** Two steward kinds, two lifetimes.

- **General steward** (`steward.general.v1`) — frozen template bundled
  in the hub binary. **Persistent, one per team, always-on.**
  Bootstraps new projects (authors domain-steward + worker templates +
  plan as its first assignment), then remains available as the
  director's concierge for cross-project work, debugging, discussion,
  and template/schedule editing. Archived only by manual director
  action.
- **Domain steward** (`steward.research.v1`, `steward.infra.v1`,
  `steward.briefing.v1`, …) — overlay-authored by the general steward,
  editable by the director. Project-scoped lifetime; archived at
  project completion.

The pattern fits [agent-lifecycle](../spine/agent-lifecycle.md)'s
single-agent-bootstrap-window framing — the general steward
operationalises the bootstrap window — but extends it: the general
steward does not exit at window's close; it delegates project
orchestration to the domain steward and stays available for everything
else. Manager/IC invariant ([blueprint §3.3](../spine/blueprint.md))
holds: general steward authors *infrastructure* (templates, plans,
schedules) and *advises* (reads, summarises, explains); IC work is
delegated to workers spawned by domain stewards. See §6 for the
*general* vs *general-purpose* distinction (the latter is blueprint
§3.4's anti-pattern).

**D3. Templates as overlay artifacts, authored by the steward.**

Per-team template overlay at:

```
<DataRoot>/teams/<team>/templates/
  agents/<name>.yaml
  prompts/<name>.md
  plans/<name>.yaml
```

Hub binary ships **seed templates** (`embed.FS`) that the general
steward copies to overlay on first project create. Hub never overwrites
team overlay after that. New seed versions are imported via an explicit
director action, never automatically. This mirrors the existing
`agent_families` overlay pattern.

The general steward authors *worker* templates and *domain-steward*
templates. It does **not** edit its own kind (`steward.general.v1`) —
that's frozen, bundled, and only updated via hub release. Avoids
confused-deputy escalation.

The director can author or edit any overlay template at any time via
the mobile template editor (raw YAML/Markdown text editor for MVP).

**D4. Scope-not-budget governance for MVP.** The only governance line
is the **operation-scope manifest** (ADR-016): which `hub://*` MCP tools
each agent role may call. `budget_cents` stays in the schema but is
ignored at runtime. Per-tool approval gates are not enforced.
Engine-native tools are fully open (Bash, Edit, Read, Write, WebSearch,
WebFetch, engine-internal `Task`/subagent — see §7). Approval is a
phase-boundary concern, not a per-action concern.

**D5. Open default engine tools + safe-by-design self-extension.**
Agents have full access to their engine's native tools. If a tool is
missing, the agent **websearches for it, downloads it, runs it** — but
only from authoritative sources. The demo is constrained to operations
that don't need API keys or trigger malware risk; encoded as
prompt-level guardrails (§5), not as new infrastructure.
`attention.request_secret` and other key-bearing flows are **deferred
to post-MVP**.

**D6. Engine-internal subagents are out of scope of the operation-scope
manifest.** Termipod governs *termipod-managed* agents (rows in
`agents`, one host-runner-supervised process each). Engine-internal
subagents (claude-code's `Task` tool, codex app-server child sessions,
similar in other engines) share their parent agent's MCP client and
inherit its scope by construction. Termipod does not enumerate,
restrict, or monitor them beyond what frame profiles surface in the
transcript. See ADR-016 for the formal statement.

**D7. The general steward stays alive across the project.** This was
the user's modification to the bootstrap-only framing. The general
steward bootstraps in phase 0, then remains available as a persistent
concierge for everything else — multi-project debugging, free
discussion, template/schedule edits, cross-project sweeps, future
project bootstraps. Director archives it manually if at all. See §4
on what it does outside bootstrap.

---

## 5. Safety guardrails (prompt-encoded, not infrastructure)

For MVP the demo is constrained to operations that don't need secrets
or trigger malware risk. The constraints are encoded in worker
prompts:

| Activity | Allowed | Forbidden |
|---|---|---|
| Lit-review search | arxiv.org, papers-with-code, openreview, github (read-only), well-known conference proceedings | Random blogs, scraped paywalled content, screenshot-OCR of papers |
| Tool installation | `pip` from PyPI (signed, well-known maintainer), `apt` from official repos, official binary releases from project's GitHub releases page | `curl <random-url> \| bash`, untrusted package mirrors, single-maintainer one-star packages, typosquats |
| Datasets | Hugging Face datasets (curated splits), GitHub-hosted data with clear licence, arxiv supplementary material | Web scraping, terms-of-service-restricted data |
| Code dependencies | PyTorch, NumPy, transformers, datasets, scipy, matplotlib, pandas (well-known + load-bearing libraries) | Tiny obscure packages, packages with no recent commits |
| External API calls | None requiring keys | All API-key-protected endpoints (the demo deliberately doesn't need them) |

Worker prompts include the explicit instruction: *"prefer authoritative
sources, prefer signed packages, prefer libraries with broad adoption;
if a tool you'd reach for is obscure or single-maintainer, prefer a
well-known alternative or skip the operation and surface a
`request_help` to the director."*

This means **`attention.request_secret` is out of MVP scope.** It
returns when real-key cases emerge.

---

## 6. *general* vs *general-purpose* — keeping the right invariant

[Blueprint §3.4](../spine/blueprint.md) names *general-purpose steward*
as an **anti-pattern**: a single agent that answers questions, edits
files, runs tests, AND arbitrates approvals — collapsing manager and
IC. Single-engine clients (Happy, CCUI) do this; we don't.

The *general steward* introduced here is **not** that anti-pattern. It
is general in the sense of *team-scoped and project-agnostic*, not
general in the sense of *does both manager and IC*. Concretely:

| | General steward (this design) | General-purpose steward (anti-pattern) |
|---|---|---|
| Scope | Team-level, persistent | Per-project |
| Manager work | Yes — authors templates, plans, schedules | Yes |
| IC work | **No** — delegates to workers | Yes — answers questions, edits files, runs tests |
| Token budget | Bounded — manager-only context | Unbounded — code + tools + decisions |
| Approval surface | Clean — approvals are governance | Muddied — approvals mixed with tool noise |

The glossary makes this explicit (`general steward` entry + Distinguish
from line). Prompt-engineered into `steward.general.v1`: *"You manage
and advise. If asked to do IC (write code, run experiments, draft
papers), delegate to a worker or politely decline."*

---

## 7. Engine-internal subagents — explicit non-restriction

When a termipod-managed agent invokes its engine's internal subagent
mechanism (claude-code `Task`, codex app-server child sessions, similar
in others), those subagents:

- Are **not** rows in `agents`. They share the parent's process, MCP
  client, tmux pane, and host-runner supervision.
- **Inherit the parent's operation scope by construction.** A worker's
  internal subagents share worker scope; a steward's internal subagents
  share steward scope.
- Are **not enumerated or monitored** by termipod beyond what the
  parent's frame profile surfaces in the transcript.
- Are **fully unrestricted** by termipod governance. The engine decides
  what they can do; we observe whatever stream-json the parent emits.

This is the right boundary: structural safety from the operation-scope
manifest holds (an internal subagent can't escape its parent's scope),
and termipod doesn't fight engine-internal patterns (parallel `Task`
fan-out is what makes engines like claude-code productive).

This needs codifying in [blueprint §3.3](../spine/blueprint.md) as a
clarifying paragraph, and in
[ADR-016](../decisions/016-subagent-scope-manifest.md) as a formal
exemption.

---

## 8. What's actually new (deliverables)

**No schema migrations. No new primitives. Templates + prompts + small
infra + mobile screens.**

| Layer | Deliverable | Effort |
|---|---|---|
| Hub MCP | `hub://templates.{agent,prompt,plan}.{create,update,delete,list,get}` | 2 days |
| Hub overlay | Team-scoped overlay loader at `<DataRoot>/teams/<team>/templates/` | 0.5 day |
| Hub middleware | Operation-scope role-gating in `mcp_authority.go` (driven by `roles.yaml`) | 1 day |
| Hub seed | `embed.FS` bundled seeds for steward.general.v1, steward.research.v1, lit-reviewer.v1, coder.v1, paper-writer.v1, critic.v1, ml-worker.v1 (existing), research-project.v1 plan | 3 days (mostly prompt-engineering) |
| Mobile | Template editor screen (raw text edit), phase-0 review surface bundling plan + templates, persistent-steward entry on home tab | 3 days |
| Harness | `seed-demo --shape lifecycle` with all phase states represented | 1 day |
| Docs | This doc + ADR-016 + ADR-001 amendment + blueprint §3.3 amendment + plan | already in flight |

Total ~10–13 days of work. Architecturally zero risk; demo risk is
prompt quality + the mobile editor UX.

---

## 9. Open questions (post-MVP)

**OQ-1.** When the steward edits a worker template mid-project, do
existing-running workers re-spawn with new version, or finish on old?
*Working assumption:* existing-spawned agents keep their version;
future spawns use latest. This matches existing overlay loader
behaviour.

**OQ-2.** Director and steward editing the same template concurrently
— last-write-wins or optimistic-lock? *Working assumption:* last-write-
wins for MVP (both are humans-in-the-loop slow; conflict is rare).

**OQ-3.** When the demo encounters a real key-bearing tool need, what
shape does `attention.request_secret` take? *Deferred from MVP.*

**OQ-4.** Cross-project memory for the general steward — does it
remember last week's work on project A when discussing project B
today? Engine-resume cursor (ADR-014) provides intra-conversation
continuity; cross-project synthesis is a deeper question. *Deferred
from MVP.*

**OQ-5.** "Soft" vs "hard" enforcement of the manager/IC invariant on
the general steward — prompt-engineered politeness ("I'll delegate
that to a worker") vs structural enforcement (refuse certain tool
calls). *MVP is soft (prompt). Hard enforcement would require a
classifier on incoming requests, which is not worth its weight at
this stage.*

---

## 10. Recommendation + ordering

Land in this order (also see
[`plans/research-demo-lifecycle-wedges.md`](../plans/research-demo-lifecycle-wedges.md)):

1. **W1** — Operation-scope middleware + `roles.yaml` (foundational; smallest)
2. **W2** — Template-authoring MCP tools + team overlay loader
3. **W3** — Mobile template editor + phase-0 review + persistent-steward entry point
4. **W4** — `steward.general.v1` template + bootstrap-and-concierge prompt
5. **W5** — Domain steward seed (`steward.research.v1`) + worker seeds + safety guardrails
6. **W6** — `research-project.v1` plan template + `seed-demo --shape lifecycle`

Each wedge ships independently; the demo is fully end-to-end after W6.

---

## 11. References

- [ADR-001 (amended)](../decisions/001-locked-candidate-a.md) — locked candidate now lifecycle, not single-phase
- [ADR-016](../decisions/016-subagent-scope-manifest.md) — subagent operation-scope manifest
- [Blueprint §3.3 (amended)](../spine/blueprint.md) — general/domain steward layering + engine-internal subagent exemption
- [Plan: research-demo-lifecycle wedges](../plans/research-demo-lifecycle-wedges.md)
- [Discussion: integrating open-source agents](integrating-open-source-agents.md) — this work uses claude-code/codex/gemini for the worker engines; OpenClaude is a candidate seventh engine in Group A there
- [Agent lifecycle §6.2 — bootstrap window](../spine/agent-lifecycle.md)
- External (snapshot 2026-04-30):
  - IKP / 01.me — github.com/01-me/ikp; arxiv:2604.24827
  - Sakana AI Scientist v2 — github.com/SakanaAI/AI-Scientist-v2
  - Google PaperOrchestra (2026-04) — marktechpost paper-orchestra coverage
  - Claude Code Agent Teams — code.claude.com/docs/en/sub-agents

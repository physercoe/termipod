# Competitive landscape — April 2026

> **Type:** reference
> **Status:** Current (2026-05-01) — refresh quarterly; the landscape moves faster than that and this file decays.
> **Audience:** principal · partners · contributors making product decisions
> **Last verified vs code:** v1.0.350-alpha
> **Sources:** vendor docs, repo READMEs, public blog posts, and direct hands-on through April 2026.

**TL;DR.** Termipod sits at the intersection of two adjacent product categories: **mobile control-plane apps for AI coding agents** (Anthropic Remote Control, Happy, Tactic Remote, Codex web) and **autonomous-research agent systems** (Sakana AI Scientist v2, Google PaperOrchestra, IKP/01.me, AIDE, STORM). Neither category is a direct competitor — every adjacent product makes one or two of termipod's bets and skips the others. This file is the synthesized landscape map that shaped the architecture (multi-host A2A, frame profiles, layered stewards, lifecycle demo) and the positioning (personal tool with multi-host × multi-engine × multi-session × governance, not a SaaS or a single-engine wrapper). For the why-it-shaped-the-architecture chain, see [discussions/positioning §1.5](../discussions/positioning.md) and [discussions/lifecycle-amendment-2026-04.md](../discussions/lifecycle-amendment-2026-04.md).

---

## Method & scope

This synthesis is from a deliberate research phase on **2026-04-19** ("before exec the termipod-hub-mvp plan, please conduct an objectively, comprehensively research about this plan"), refreshed 2026-04-30 around the lifecycle amendment. Sources are public: official docs, GitHub READMEs, vendor blog posts, talk transcripts, and hands-on use where practical. No NDAs, no insider info; everything cited here a reader can verify.

Two categories are tracked because both shape termipod's identity:

- **Category A — Mobile control-plane apps.** Direct UX competitors. A user choosing termipod is choosing it *over* one of these.
- **Category B — Autonomous-research agent systems.** Demo-bar setters. A reviewer evaluating termipod's research-lifecycle demo compares it to these. Not direct UX competitors (they are servers and frameworks, not phone apps).

A third category — **messenger-bridge agents** (OpenClaw, Hermes, Claude Code Channels) — is covered in [discussions/positioning §1.5](../discussions/positioning.md). They answer the same user question ("AI agents on your phone") with a different thesis, but they're orthogonal to termipod's fleet-cockpit framing, not directly competing.

---

## Category A — Mobile control-plane apps

What each one is, what it does well, where its design leaves room for termipod.

### Anthropic Claude Code Remote Control

**What it is.** Anthropic's official Claude Code mobile remote. Pairs your phone to a single Claude Code session running on your laptop or desktop. Sees the same conversation; can send messages; surfaces tool-permission prompts.

**Status April 2026.** Production, App Store, requires a Claude account.

**What it does well.** Official path; same Anthropic-grade transcript on both ends; biometric auth; clean UX; backed by Anthropic's resources.

**What termipod does that it doesn't.**

- Multi-engine. Remote Control is Claude only; termipod runs claude / codex / gemini-cli (and any future engine that lands a frame profile).
- Multi-host. Remote Control assumes "one local machine, keep it awake." Termipod coordinates a fleet across VPS / GPU box / laptop via the hub + A2A relay.
- Multi-session. One active session at a time in Remote Control; many in termipod with a unified attention queue.
- Self-hosted. Remote Control routes through Anthropic infrastructure; termipod data lives on the user's hub.
- Open source. Apache 2.0 vs. closed.

**What it does that termipod has had to backfill (the "no short-board" axes).** Single-engine session UX: resume across restarts, fork a session to explore, scope-grouped session list, vocabulary that doesn't surprise existing Remote Control users. Termipod ships these as MVP-parity features (ADR-009, [plan: mvp-parity-gaps](../plans/mvp-parity-gaps.md)) — a user switching from Remote Control should find their muscle memory works.

### Happy (open-source)

**What it is.** Open-source mobile companion for Claude Code (and now Codex). Pairs an `npm`-installed `happy-coder` companion on the user's machine to a phone via E2E-encrypted relay. Voice control. ~95k stars at peak.

**Status April 2026.** Active; primary maintainer based in HK.

**What it does well.** End-to-end encryption. Voice. Polished single-engine UX. Open source.

**What termipod does that it doesn't.**

- Hub model. Happy's relay is a single-host bridge (one machine ↔ one phone). Termipod's hub is a coordination plane (N machines ↔ M agents ↔ one principal).
- Governance. Happy has no audit log, no policy gates, no per-agent budget framing. Termipod's governance lives in the hub schema.
- Multi-host A2A. A steward on a VPS coordinating workers on a GPU box is termipod's primary path; not Happy's.
- Plan-and-ratify framing. Happy is a chat surface; termipod's UX puts plans + approvals as first-class.

**What termipod borrows from Happy.** UX ergonomics for the single-engine path. The "no short-board" commitment ([positioning §3](../discussions/positioning.md)) explicitly names Happy + claudecode-remote as the muscle-memory benchmark. Happy's E2E encryption is post-MVP for termipod.

### Tactic Remote

**What it is.** Closed-source iOS app for remote SSH + agent control.

**What termipod does that it doesn't.**

- Open source.
- Cross-platform (Android first; iOS source-build today, TestFlight when distribution is ready).
- Hub-coordinated multi-host.

Tactic Remote is the iOS-side reminder that iOS distribution matters. The Claude Code mobile story is iOS-first, which pressures termipod's iOS pipeline. App Store distribution is post-MVP.

### Codex web / Codex iOS

**What it is.** OpenAI's mobile and web codex front-ends.

**Status April 2026.** Production; tied to OpenAI accounts.

**What termipod does that it doesn't.**

- Self-hosted. Codex web routes through OpenAI; termipod's hub runs on the user's VPS.
- Multi-engine. (Codex web is codex-only.)
- Multi-host. Same.

Codex web is the "official path for codex users," analogous to Anthropic Remote Control for Claude. Same set of differences applies.

---

## Category B — Autonomous-research agent systems

These are not mobile apps and not direct UX competitors. They set the **demo bar** for "what does an agent-driven research workflow look like." Termipod's lifecycle demo (idea → lit-review → method → experiment → paper) is calibrated against these.

### Sakana AI Scientist v2 (2025) and v2.5

**What it is.** Sakana's autonomous research agent system. v2 (mid-2025) ships agent-authored workshop-level papers. v2.5 (early 2026) extends with better experiment design + a tree-search outer loop.

**What it teaches termipod.** End-to-end "idea to paper" is a credible target for autonomous agents in 2026. The phase boundaries (idea / lit / method / experiment / paper) we use are roughly consistent with Sakana's pipeline. Sakana operates as a server-side system; termipod's principal-direction framing is different — the director gates each phase, the agents don't autonomously close the loop.

### Google PaperOrchestra (April 2026)

**What it is.** Google research system that takes raw experimental logs + an idea seed and writes the paper. Beats AI Scientist v2 by 39–86% on overall paper quality (Google's published benchmark).

**What it teaches termipod.** The phase decomposition matters more than the per-phase model size. Termipod's `paper-writer.v1` worker borrows the "citation-only-from-lit-review" rule from PaperOrchestra's prompt patterns.

### IKP / 01.me case (Bojie Li, 2026)

**What it is.** Documented solo case where one director shepherded a multi-agent team through real published research in roughly 4 days. Public writeup at `01.me/research/ikp/`.

**What it teaches termipod.** A solo principal directing a multi-agent team is a real, achievable workflow today. Termipod's ICP is exactly this director archetype. The case validated that termipod's personal-tool framing + lifecycle demo aren't science fiction.

### AIDE (Apart Research, 2025)

**What it is.** ML-engineering agent that iteratively writes and improves code on Kaggle problems. Strong on iterative-refinement loops.

**What it teaches termipod.** Iteration-inside-phase is the right pattern for `agent_driven` phases (blueprint §6.2). The director sees one phase result; the steward's intra-phase iteration is invisible. AIDE validated that pattern.

### STORM (Stanford, 2024)

**What it is.** Multi-agent literature review / Wikipedia-article generation system. Multi-perspective gathering with role-prompted sub-agents.

**What it teaches termipod.** The lit-review phase benefits from a small fan-out of role-prompted workers (skeptic, expander, summarizer). Termipod's `lit-reviewer.v1` template borrows this pattern but ships with a single worker by default; multi-worker fan-out is a director-tunable parameter.

### MLE-Bench

**What it is.** OpenAI's benchmark of agent performance on Kaggle ML competitions.

**What it teaches termipod.** The benchmark exists; it's not a product. Termipod's demo isn't trying to win MLE-Bench — that's not the point of a personal-tool research lifecycle. But it's a useful sanity check: termipod's demo path uses the same primitives MLE-Bench-winning agents use (worktree-per-experiment, structured artifacts, a critic loop), just composed differently.

### OpenHands / CrewAI / LangGraph / autoGen

**What they are.** Agent frameworks for building agents. Web UIs (OpenHands), Python SDKs (CrewAI, LangGraph, autoGen).

**What termipod does that they don't.** Termipod is a director's harness, not a framework to embed. It runs vendor CLIs (claude-code, codex, gemini-cli) as agents; users don't write agents in Python in termipod. The line is intentional: those frameworks are tools for engineers building agents; termipod is for directors using agents.

### Devin 2.0 (Cognition)

**What it is.** Planner-worker agent system. 35-minute context window before degradation; explicit planner role.

**What it teaches termipod.** The planner / worker split is the right structural decomposition. Termipod's steward / worker architecture pre-dates Devin but converges on the same shape. The 35-minute context-degradation problem is real; termipod's approach is different — workers are spawned per task with bounded context, not extended through a single long-running session.

### Cursor 2.x

**What it is.** Cursor's IDE-agent v2: 8 parallel agents, auto-managed git worktrees per agent, visible task list.

**What it teaches termipod.** Worktree-per-agent is the right concurrency model for parallel agents (matches termipod's `agents.worktree_path`). The visible task list is a first-class UI surface. Termipod's parallel-agents story (multiple workers under a domain steward) lands in the same place but driven from the phone, not the IDE.

---

## Differentiation matrix

A condensed version of [positioning §3](../discussions/positioning.md) — re-stated for the audit-doc audience:

| Axis | Anthropic Remote | Happy | Tactic Remote | Codex web | Termipod |
|---|---|---|---|---|---|
| Topology | 1 phone ↔ 1 host | 1 phone ↔ 1 host | 1 phone ↔ 1 host | 1 phone ↔ cloud | **N hosts ↔ N agents ↔ 1 director** |
| Engines | Claude only | Claude + Codex | Claude (+) | Codex only | **Claude + Codex + Gemini + frame-profile-extensible** |
| Multi-session | Limited | Yes | Yes | Yes | **Yes, with attention-queue rollup** |
| Self-hosted | No | Optional | No | No | **Yes (Apache 2.0 hub)** |
| Multi-host A2A | No | No | No | No | **Yes (relay-based, NAT-tolerant)** |
| Governance | None | None | None | OpenAI policy | **Operation-scope manifest, audit log, principal authority** |
| Offline | No | No | No | No | **SQLite snapshot cache (last-known-good)** |
| Lifecycle demo | n/a | n/a | n/a | n/a | **5-phase research, agent-authored, director-gated** |

The columns of "no" for non-termipod products aren't gaps in *their* design; they're choices appropriate to single-engine remote-control product framing. Termipod's columns of "yes" are not features added to a remote-control app; they're consequences of starting from a different product category — multi-agent fleet cockpit instead of single-session bridge.

---

## What this landscape changed in termipod

Documenting the landscape is only useful if the design absorbed lessons from it. Here are the chains:

1. **Multi-engine via frame profiles ([ADR-010](../decisions/010-frame-profiles-as-data.md)).** Happy ships a manual integration per engine. Termipod ships a YAML data model so adding a new engine is content, not Go code. Cause: "multi-engine maintaining is a rabbit hole" (transcript, 2026-04-29).
2. **Lifecycle amendment (ADR-001 D-amend-1).** Sakana / PaperOrchestra / IKP raised the demo bar from "ablation sweep" to "idea → paper." Cause: [discussions/lifecycle-amendment-2026-04.md](../discussions/lifecycle-amendment-2026-04.md).
3. **Layered stewards ([ADR-017](../decisions/017-layered-stewards.md)).** Devin's planner-worker, Cursor's worktree-per-agent, AIDE's iteration-inside-phase together pointed at: have a manager that doesn't do IC work + ICs that don't escalate. Termipod's general/domain split + manager/IC invariant is the synthesis.
4. **Personal-tool frame ([positioning §1.5](../discussions/positioning.md)).** Looking at all of Category A and asking "what's missing for solo directors with their own hardware?" produced the multi-host × multi-engine × self-hosted × governance combination as the differentiation.

---

## Refresh cadence

This file decays. Anthropic and OpenAI ship monthly; the messenger-bridge category had a Cambrian explosion in February. Refresh expectation: every 90 days (one per quarter). Refresh trigger: any new Anthropic or OpenAI mobile-product announcement, any new framework reaching ≥10k stars in <2 months, any change in the personal-tool ICP's demo expectations. When refreshing, update the status block date and add a brief diff section noting what changed since the last refresh.

---

## References

- [Discussion: positioning](../discussions/positioning.md) — full strategic frame; this file is the data, that one is the decisions.
- [Discussion: integrating-open-source-agents](../discussions/integrating-open-source-agents.md) — what we'd absorb / not absorb.
- [Discussion: multi-agent-sota-gap](../discussions/multi-agent-sota-gap.md) — gap analysis vs. production frameworks.
- [Discussion: lifecycle-amendment-2026-04](../discussions/lifecycle-amendment-2026-04.md) — what the landscape changed.
- Public sources: Sakana AI Scientist papers, Google PaperOrchestra (April 2026 publication), `01.me/research/ikp/`, AIDE repo, MLE-Bench paper, STORM repo, Cursor + Devin product pages.

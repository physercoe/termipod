# Roadmap

> **Type:** vision
> **Status:** Current (2026-04-28)
> **Audience:** principal, contributors, reviewers
> **Last verified vs code:** v1.0.316

**TL;DR.** The MVP target is the research demo from `blueprint.md` §9
Phase 4: a user writes a directive on phone → steward decomposes →
fleet executes runs across hosts → briefing agent summarizes overnight
→ user reviews on phone. Everything else flows from that. Phases P0–P3
are shipped; P4 backend is feature-complete; current focus is reliability
and UX polish from device walkthroughs.

---

## Mission

Termipod is a mobile-first **director's surface** for orchestrating
backend agents (claude-code, codex, …) across multiple remote hosts.
The user is principal/director — they don't operate the system, they
direct agents that operate it.

What makes us different from single-engine clients (Happy,
claudecode-remote, …):
- **Multi-host** — A2A relay routes agent ↔ agent across NAT'd boxes
- **Multi-session** — sessions are first-class durable conversations,
  surviving agent respawn
- **Multi-engine** — claude-code, codex, etc. are interchangeable per
  template
- **Steward-orchestrated** — the steward agent is the entry point;
  user delegates project-shaped goals, not task-shaped commands

## North Star (next 90 days)

**Run the locked Candidate-A demo end-to-end on hardware.** Steward
decomposes a nanoGPT-Shakespeare optimizer × size sweep, dispatches
ml-worker spawns to a GPU host via A2A, briefing agent writes a
takeaway doc that surfaces in the mobile Me tab — all driven from
the phone with no laptop in the path.

Demo readiness is measured by `discussions/run-the-demo` (the
dress-rehearsal harness covers the path without GPU; the hardware
run is the actual milestone).

---

## Phases (big picture)

The blueprint defines five phases. Status as of v1.0.308:

| Phase | Title | Status |
|---|---|---|
| **P0** | Hub primitives (schema) | ✅ Shipped |
| **P1** | Structured wire (protocols) | ✅ Shipped |
| **P2** | App UI | ✅ Shipped |
| **P3** | Integrations (trackio, A2A) | ✅ Shipped |
| **P4** | Research demo | 🟡 Backend feature-complete; UX hardening + hardware run remaining |

### Phase summaries

**P0 — Hub primitives.** All schema landed: projects evolution,
plans + plan_steps, schedules, runs, documents, reviews,
hosts.ssh_hint_json + capabilities_json. See `spine/blueprint.md` §9.0.

**P1 — Structured wire.** Multi-mode host-runner driver (M1 ACP, M2
stdio, M4 pane); plan-step executor; capability probe; mode resolver;
hub MCP server (consolidated v1.0.298 — single service via
`mcp_authority.go`); host-runner MCP gateway; AG-UI broker; structured
input endpoint.

**P2 — App UI.** AgentFeed, AgentCompose, plan viewer, workflows tab,
triage (Me), project / task / plan / run / review screens, templates
browser, team settings (schedules, budgets, audit log).

**P3 — Integrations.** Trackio + wandb + tensorboard pollers in
host-runner; mobile sparkline; A2A server with agent-cards; hub
directory + reverse-tunnel relay; cross-host smoke test.

**P4 — Research demo.** Templates shipped (steward.research,
ml-worker, briefing); steward decomposition recipe; SOTA
orchestrator-worker slice (agents.fanout / agents.gather /
reports.post); dress-rehearsal harness (seed-demo + mock-trainer,
no-GPU). The remaining work is verifying the path on real hardware
and polishing the principal's UX.

---

## Now / Next / Later (current focus)

The phase view is for big picture. This section is the active
working list — what's actually moving this week or next.

### Now (in flight)

| Item | Why | Where |
|---|---|---|
| **Reliability hardening from device walkthroughs** | Hardware-demo gate is "two consecutive walkthroughs without principal-blocking bugs" | per-version commits as device tests surface issues |

### Next (committed, not started)

| Item | Why | Trigger |
|---|---|---|
| **Hardware run of Candidate-A demo** | The actual MVP milestone (`decisions/001-locked-candidate-a.md`) | Two consecutive walkthrough-clean device tests |
| **Briefing agent overnight schedule** | Demo path needs the steward to schedule the briefing autonomously | After hardware run smoke-tests the worker path |
| **Anti-drift Layer 3** | OpenAPI for hub REST + ADR backlinks from spine docs | Triggers when surface drift bites — currently tractable by hand |

### Later (intent, no commitment)

These have design memos in `discussions/` and may or may not be
prioritized after the demo lands.

- **Simple/Advanced mode for the mobile UI** — Activity / Hosts tabs
  are operator-shaped; less-technical principals would benefit from
  hiding or folding them (`discussions/simple-vs-advanced-mode.md`).
  Revisit post-demo.
- **CI-generated README screenshots** — automate via
  `integration_test` + `binding.takeScreenshot()` against a
  `seed-demo` hub; eliminates the screenshot-drift problem
  (`discussions/screenshot-automation.md`). Defer until post-demo
  IA stabilizes.
- **Pending dependency upgrades** — ~14 Dependabot PRs open as of
  v1.0.319, 5 majors deferred for individual review (riverpod 3.3,
  google_fonts 8, flutter_foreground_task 9, connectivity_plus 7,
  modernc.org/sqlite 1.50). Triage when post-demo bandwidth opens.
- **Domain packs / marketplace** — content-pack extensibility
  (`discussions/post-mvp-domain-packs.md`)
- **Multi-steward wedge 3** — deferred per memory
- **Per-member stewards (F-1)** — deferred until 2nd user
- **Code-as-artifact** — deferred (`discussions/code-as-artifact.md`)
- **Agent fleet / squads** — deferred (`discussions/agent-fleet.md`)
- **Monolith refactor** — terminal_screen.dart, hub_client.dart
  (`discussions/monolith-refactor.md`)
- **Sandbox-style worker isolation** — bwrap/Seatbelt + microVM
- **Cache eviction by bytes** — switch HubSnapshotCache from row-cap
  to byte-cap
- **iOS TestFlight distribution** — Android APK suffices for demo
- **Cloud sync of mobile data** — single-device works; cross-device
  is a transfer flow today

---

## Done this quarter (rolling)

Most recent first. Major work units only — bug-fix releases roll up.

| Version | What |
|---|---|
| v1.0.317 | GitHub-ecosystem hygiene + changelog seed (Dependabot, CodeQL, PR template, stale-doc warning) |
| v1.0.316 | Status-block linter wired to CI (anti-drift Layer 1) |
| v1.0.315 | Spine docs reconciled with shipped state (sessions Tentative→Resolved, blueprint phase status, IA wedges marked) |
| v1.0.314 | coding-conventions rewritten first-principles + memory body audit |
| v1.0.311 | 8 retroactive ADRs in decisions/ |
| v1.0.310 | docs/ reorganized into 7-primitive layout; sessions promoted out of DRAFT |
| v1.0.309 | Foundation: README + roadmap + doc-spec |
| v1.0.308 | Cancel button surfaces whenever agent is busy |
| v1.0.305 | Cache coverage extended to all hot-path detail fetches |
| v1.0.304 | Cache-first cold start (UI lights up before network) |
| v1.0.303 | Cold-start `refreshAll` (Projects/Me/Hosts/Agents populate on launch) |
| v1.0.300 | Steward composer matched to action-bar composer (parity) |
| v1.0.299 | Steward chat polish — syntax-highlighted code, color-coded diffs, per-tool icons |
| v1.0.298 | MCP consolidation — single service in spawn `.mcp.json` |
| v1.0.296 | SOTA orchestrator-worker slice (fanout/gather/reports) |
| v1.0.293 | Cache coverage for sessions list + channel events |
| v1.0.290 | Multi-steward wedges 1+2 (handle suffix + domain templates) |
| v1.0.286 | Egress proxy in host-runner (masks hub URL from spawned agents) |
| v1.0.285 | Tail-first paginated transcripts + hub backup/restore |
| v1.0.281 | Replace-steward keeps the session (engine swap continues conversation) |
| v1.0.280 | Soft-delete sessions + agent-identity binding doc |
| v1.0.182 | All 7 IA-redesign wedges shipped |

Earlier history: `git log --oneline` from the v1.0.180 boundary or
[`spine/blueprint.md`](spine/blueprint.md) §9 for the original
phase commitments.

---

## Open questions

Carried forward across the quarter; surfaced here so they're not
lost in conversation history.

- **When do we actually run the hardware demo?** Currently gated on
  reliability — defining the gate explicitly: two consecutive device
  walkthroughs with zero principal-blocking bugs.
- **Does `steward-sessions` graduate to first-party "done"?** Yes —
  promoted out of DRAFT in this commit (now `spine/sessions.md`).
- **A2A vs MCP for orchestration** — resolved per
  `decisions/007-mcp-vs-a2a-protocol-roles.md`. MCP for agent ↔ hub,
  A2A for agent ↔ agent. Today's `agents.fanout` posts via MCP; A2A
  fallback is a Later item.
- **Domain-pack commercialization** — see
  `discussions/post-mvp-domain-packs.md`. Phase 1 (first-party packs
  as embedded data) is the validation step before any marketplace
  engineering.

---

## How this doc evolves

- **Phases** change rarely — when a new top-level workstream emerges
  beyond P0–P4 of the blueprint.
- **Now / Next / Later** changes whenever the active work changes —
  expected churn is weekly during active development.
- **Done this quarter** appends as releases ship. Truncate annually
  (current entries roll into a quarterly archive section if this
  list exceeds ~40 rows).
- **Open questions** are loud reminders, not action items. Move into
  PLAN or DECISION when one becomes actionable.

---

## References

- Architecture: [`spine/blueprint.md`](spine/blueprint.md) §9
- Decisions made: [`decisions/`](decisions/)
- Active work units: [`plans/`](plans/)
- Demo path detail: `discussions/run-the-demo.md` (post-reorg) or
  current `run-the-demo.md`

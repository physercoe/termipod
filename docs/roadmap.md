# Roadmap

> **Type:** vision
> **Status:** Current (2026-08-24)
> **Audience:** principal, contributors, reviewers
> **Last verified vs code:** mobile `2026.824.751-alpha` · hub/host `2026.817.322-alpha` · desktop `2026.817.322`

**TL;DR.** The MVP target is unchanged: the research demo from
`blueprint.md` §9 Phase 4 — a user writes a directive → steward
decomposes → fleet executes runs across hosts → briefing agent
summarizes overnight → user reviews. Phases P0–P3 are shipped; P4
backend is feature-complete; the hardware run is still the milestone.

What changed since June: the project grew a second first-class client.
The **desktop workbench** (React + TypeScript on an Electron shell,
ADR-050/055) went from zero to a full director's cockpit in its own
release lane — transcripts, projects + task board, approvals, admin,
breakglass SSH + vault, then the research surfaces (Read / Author /
Inspect), embodied replay (J8), the agent↔UI interplay arc (browser
bridge, `ui_get_focus`, annotation pointing), env profiles + session
teleport, and a split-pane shell. Mobile + hub kept moving in parallel:
design-token enforcement (ADR-047), vocabulary presets + the full i18n
sweep (ADR-048), inline-spec project lifecycle (ADR-046), sealed env
secrets (ADR-056), session teleport (ADR-057), and the
environments/datasets/blob-lifetime model (ADR-058…061). Versioning
switched to CalVer (`YYYY.MMDD.HHMM`) with four per-component release
lanes (`mobile-v*` / `hub-v*` / `host-v*` / `electron-v*`).

---

## Mission

Termipod is a **director's surface** — on phone and desktop — for
orchestrating backend agents (claude-code, codex, gemini-cli,
kimi-code, …) across multiple remote hosts. The user is
principal/director — they don't operate the system, they direct agents
that operate it.

What makes us different from single-engine clients (Happy,
claudecode-remote, …):
- **Multi-host** — A2A relay routes agent ↔ agent across NAT'd boxes
- **Multi-session** — sessions are first-class durable conversations,
  surviving agent respawn (and, since ADR-057, surviving a move to a
  different host)
- **Multi-engine** — claude-code, codex, gemini-cli, kimi-code (ACP
  and the TS port, ADR-054) are interchangeable per template
- **Steward-orchestrated** — the steward agent is the entry point;
  user delegates project-shaped goals, not task-shaped commands
- **Two clients, one hub** — the Flutter mobile app and the Electron
  desktop workbench speak the same API against the same hub; the
  desktop adds researcher surfaces (Read / Author / Inspect / Replay)
  that have no phone equivalent

### MVP commitment: superset, not replacement (no short-board effect)

The above bullets are the *additional* axes we ship. They are
**net-add**, not substitutes for the table-stakes axes that
single-engine remote-control apps already do well. The MVP must be
**at competitive parity in every axis those apps support** —
single-session chat, resume, list past sessions, fork to
explore, sensible session vocabulary. A user switching from
claudecode-remote / Codex remote / Happy should find their muscle
memory works here, not be told "we don't do that yet."

A feature being weaker than what those apps ship is a bug, not a
future enhancement. This is why decisions like ADR-009 (fork
operator, scope-grouped session list, archive vocabulary) land in
MVP rather than post-MVP — they aren't polish; they're the
short-board fix.

The selling point is *both* directions: pick us up to keep the
single-engine workflow you already know, and grow into the fleet /
governance / multi-host capabilities when you need them.

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

The blueprint defines five phases. Status as of `2026.730.1231-alpha`:

| Phase | Title | Status |
|---|---|---|
| **P0** | Hub primitives (schema) | ✅ Shipped |
| **P1** | Structured wire (protocols) | ✅ Shipped |
| **P2** | App UI | ✅ Shipped |
| **P3** | Integrations (trackio, A2A) | ✅ Shipped |
| **P4** | Research demo | 🟡 Backend feature-complete; UX hardening + hardware run remaining |

Two workstreams grew beyond the original five phases in July:

| Lane | Status |
|---|---|
| **Desktop workbench** (ADR-050/055, `electron-v*` lane) | ✅ Core shipped — WS2–WS8 (shell, navigator, transcripts, approvals, projects + task board, admin, SSH terminal + vault, packaging) plus the Electron migration M0–M4 complete; Tauri lane retired at M3.4 (2026-07-22). Auto-update stays manual-install until signing certs land and a release is promoted onto the `electron-latest` feed. Record: [`changelog-desktop.md`](changelog-desktop.md) |
| **Embodied research workbench** (ADR-058…061) | 🟡 In flight — J8 replay (datasets rail, episode player, URDF/3D pose, Rerun export, host-job surface) and the Environment entity (E0 + E2a) shipped; E2b/E2c resolution surfaces and hardware device-verifies remain. Plans: [`plans/replay-datasets-episodes.md`](plans/replay-datasets-episodes.md), [`plans/environments-and-embodiments.md`](plans/environments-and-embodiments.md) |

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
| ⚠ **Redeploy the hub + host-runner binaries** | Three consecutive cuts — `2026.730.1231`, `2026.805.1022`, `2026.817.322` — carry 51 + 17 + 18 server-side commits that are **not live** until the binaries are redeployed. Every pane-state feature is host-runner code by construction; the streamed-output relay (E3), the bridge result passthrough (E4), the sub-agent provenance stamp (R5) and the `runtime_switch_fields` gate (R6) are hub code. This is the longest-outstanding item in the release record, and it compounds each cut | Director action. `changelog.md` §2026.817.322-alpha |
| **Desktop Companion vision-parity + pane-state detection** | The two lanes that dominated the 2026-08 cycle, both now past their bulk. Vision-parity **W1–W4 complete**: the Companion drives a claude or codex session locally with no hub, survives an app restart, and shows streamed output, inline media, sub-agent attribution and model/mode pills. Pane-state **P1–P4** give host-runner a declarative state authority for every engine without a structured M4 adapter, replacing a single regex and a 90-second stall | Remaining: vision-parity **L3c** (session catalog + loopback WebSocket, deferrable) and pane-state **P5** (hub-distributed manifest updates, deferrable) + **S1/S2** (settle semantics, not started). `plans/desktop-companion-vision-parity.md`, `plans/pane-state-manifests.md` |
| **Embodied research program — environments + replay (ADR-058…061)** | J8 replay shipped end-to-end on the desktop (dataset library rail, episode player with channel plots + multi-camera video, URDF forward kinematics + 3D pose, Rerun companion + export, remote roots over SFTP) and the host-job surface (ADR-058 executor + export kind) landed. The **Environment entity** shipped E0 (`env_ref` on runs + episodes) and E2a (hub CRUD + `/environments/resolve`). All four ADRs 058–061 Accepted 2026-07-30 with a laned build order. | In flight: E2b/E2c resolution surfaces; device-verify of remote video + Rerun export on hardware. `plans/replay-datasets-episodes.md`, `plans/environments-and-embodiments.md`, `plans/desktop-workbench-jobs.md` |
| **Agent ↔ UI interplay (ADR-059, ADR-062)** | The desktop is becoming agent-addressable: browser bridge W1–W3 shipped (MCP webtabs read → act behind per-spawn opt-in + audit → remote hub-relayed driving behind approval cards, ADR-059); ui-context D1–D2.1 shipped (`ui_get_focus` off-by-default, annotation overlay pointing, global annotate trigger). ADR-062 (UI as an agent-addressable entity) is Proposed. | In flight: ui-context wedges D3–D6. `plans/desktop-agent-browser-bridge.md`, `plans/desktop-ui-context-and-pointing.md` |
| **Desktop ↔ mobile parity backlog** | The desktop reached feature parity on SSH (jump hosts, SOCKS5, `ssh_config` import), spawn env-sealing, and — with PR #485 (merged 2026-07-30) — steward spawning from the Navigator rail (it previously spawned only workers). | Ongoing. `plans/desktop-mobile-parity.md` |
| **Device-verification backlog (the hardware gate)** | The standing pre-hardware-run verifications: multi-team isolation (ADR-037), orchestration loop closure (ADR-032/034), M4 log-tail scenarios (ADR-027 §11) — plus newer desktop device-verifies (bastion SSH, remote episode video, macOS terminal right-click, teleport on device). The hardware-demo gate is unchanged: two consecutive walkthroughs without principal-blocking bugs. | Pending: device walkthroughs |
| **Docs & process** | Doc surfaces drifted behind the July desktop arc (this audit reconciles them); the loop-discipline-and-dreaming plan and the compare-wall + citation-bridge plans await director review. | `plans/loop-discipline-and-dreaming.md`, `plans/desktop-compare-wall-and-decisions.md`, `plans/desktop-citation-bridge.md` |

### Next (committed, not started)

| Item | Why | Trigger |
|---|---|---|
| **Hardware run of Candidate-A demo** | The actual MVP milestone (`decisions/001-locked-candidate-a.md`) | Two consecutive walkthrough-clean device tests |
| **Environments E2b/E2c** | E2a shipped hub CRUD + `/environments/resolve`; the client resolution surfaces (env chips resolving to entities, register-from-run) are the next wedges of `plans/environments-and-embodiments.md` | Lane order per the plan; E2a merged 2026-07-30 |
| **Browser-bridge / ui-context wedges D3–D6** | Completes the agent↔UI interplay arc under ADR-059/062 (D3+: history, deeper target registry, cross-surface pointing) | Fleet capacity after D2.1 |
| **Compare wall Lane A + citation bridge C1** | Two Proposed plans extending the Compare and Read/Author surfaces (`plans/desktop-compare-wall-and-decisions.md`, `plans/desktop-citation-bridge.md`); citeproc licensing is an open director call | Director review of the plans |
| **Signing certs + `electron-latest` promotion** | Desktop auto-update is wired (electron-updater) but stays manual-install until builds are signed and a release is promoted onto the rolling feed | Certs acquired |
| **ADR-031 agent tool ergonomics — Phases 1+2 (MVP)** | Two-tier descriptions + `tools_get` meta-tool + structured hints + per-persona intent index (`decisions/031-agent-tool-ergonomics.md`, `plans/agent-tool-ergonomics-rollout.md`; `tools_get` itself shipped) | Whenever the agent-side UX wedge is prioritized |
| **Cross-vendor integration smoke (slice 7 × 2)** | `request_help` end-to-end against a live codex binary AND a live gemini binary on a real test host — validates the vendor-neutral attention surface for both ADR-012 and ADR-013. Tests today use fakes for both protocols; slice 7 closes the loop on real upstream binaries | Real codex + gemini binaries available in a test host |
| **Briefing agent overnight schedule** | Demo path needs the steward to schedule the briefing autonomously | After hardware run smoke-tests the worker path |
| **Anti-drift Layer 3** | OpenAPI for hub REST + ADR backlinks from spine docs | Triggers when surface drift bites — the 2026-07-30 docs audit shows it is starting to |

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
- **Pending dependency upgrades** — ~15 Dependabot PRs open as of
  2026-07-30, majors deferred for individual review (riverpod 3.3,
  google_fonts 8, flutter_foreground_task 9, connectivity_plus 7,
  modernc.org/sqlite 1.54). Triage when post-demo bandwidth opens.
- **Domain packs / marketplace** — content-pack extensibility
  (`discussions/post-mvp-domain-packs.md`)
- **Multi-steward wedge 3** — deferred per memory
- **Kimi steward routing follow-up** — the ADR-026 project-steward
  routing fixes (Bug A `projects.steward_agent_id` + Bug B
  `openStewardSession`) documented but held pending design review
- **Remote kimi-web wiring** — the desktop assistant dock's kimi web
  pane against a remote host (SSH-forward follow-up)
- **Per-member stewards (F-1)** — deferred until 2nd user
- **Code-as-artifact** — deferred (`discussions/code-as-artifact.md`)
- **Agent fleet / squads** — deferred (`discussions/agent-fleet.md`)
- **Monolith refactor** — terminal_screen.dart, hub_client.dart
  (`discussions/monolith-refactor.md`)
- **Sandbox-style worker isolation** — bwrap/Seatbelt + microVM
- **Cache eviction by bytes** — switch HubSnapshotCache from row-cap
  to byte-cap
- **Selectable Postgres storage backend (ADR-045 D3)** — the
  scale-out path beyond a single-box SQLite hub. Decided, not built;
  gated on a real multi-hub / high-team-count need. Sharding (D2,
  shipped v1.0.808) is the single-box win that makes this a
  *choice* rather than a forced migration.
  (`decisions/045-hub-storage-scaling.md`,
  `plans/hub-storage-scaling.md`)
- **iOS TestFlight distribution** — Android APK suffices for demo
- **Cloud sync of mobile data** — single-device works; cross-device
  is a transfer flow today

---

## Done this quarter (rolling)

Most recent first. Major work units only — bug-fix releases roll up.

**Mobile + hub + host lane** (sequential `v1.0.x` until 2026-07-22, then
CalVer with per-component tags):

| Version | What |
|---|---|
| 2026.824.751-alpha | **The breakglass terminal stops lying about why it failed** (mobile lane only). Connected state is raised only after tmux detection and the persistent shell are up, so a drop no longer surfaces as a secondary "SSH client not available"; every immediate reconnect trigger funnels through one single-flight dial; a reconnect can finish setup the first dial never started. The tmux action bar's prefix chords — which could never have worked, since TermiPod drives tmux through `exec` and is not an attached client — become native confirmation-aware operations. Tree refresh trusts exit status over stderr noise and follows stable `%N` pane IDs. Security: a connection's configured tmux path was spliced unquoted into a shell command |
| 2026.817.322-alpha | **Pane-state detection becomes a lane, and four engine vocabularies get corrected.** host-runner classifies what is on an agent's pane from vendored declarative manifests instead of one regex plus a 90-second stall — a pure evaluator over herdr's schema + 19 TOMLs (P1), wired to the poll tick (P2), raising and *retracting* attention on a blocked pane (P3, the first self-retracting host-runner row), and explaining its own verdict rule by rule via `pane_explain` (P4). Independents: native-resume recipes as shared YAML pinned across Go and TS (N1), generic pane-input hardening so multi-line input arrives as one block (Q1). Engine corrections, each measured against a real binary: claude's discarded sub-agent provenance (R5), codex's swallowed streaming command output (E3), the attachment shapes codex rejects (L4c), and the runtime mode/model switch resolving the engine from `agents.kind` rather than `backend_json.kind` — dead for every steward (#570) — plus the per-field `runtime_switch_fields` mask that makes three impossible switches say so instead of advising an impossible template (R6). Security: an engine-supplied `engine_session_id` could inject shell into the next respawn. Read gains `attachments_json` + `rating` columns. Mobile: `pane_state` in the verbose tier, replay dedupe folds streamed text |
| 2026.805.1022-alpha | **A server-side cut: environments, a datasets catalog, and MCP 2026-07-28 compatibility.** The Environment entity E2a (hub CRUD + `/environments/resolve`); the `datasets_*` MCP read/write tools that were Replay's catalog gap; ADR-063 MCP compatibility (`MCP-Protocol-Version` headers, `resultType`, cacheable lists, `server/discover`, `_meta` tolerance, dual-published annotations, `structuredContent`); decision records as a durable entity; kimi 0.31 wire events; corrected agent-event vocabulary for claude M2 and codex M2. Mobile unchanged apart from two strings — cut for lockstep |
| 2026.730.1231-alpha | **Env profiles + sealed secrets + session teleport reach the phone.** Spawn-sheet env-profile picker + management screen; secret refs sealed client-side to the target host's key (ADR-056 — the hub stores only ciphertext; Go/Rust/Dart KAT-locked); paused sessions teleport to another host with the handoff bundle re-sealed (ADR-057, claude-code + kimi-code-ts). Hub side also carries the environments E0+E2a entity work (`env_ref` on runs/episodes, migration 0073 CRUD + `/environments/resolve`), the datasets/episodes entity (ADR-060, migrations 0068–0069), blob lifetime (ADR-061, 0071), host-command progress (0070), and the transcript-digest issue classes + streaming-markdown fixes. All three lanes cut together; first triple-lane tag |
| 2026.727.206-alpha | **Fleet updates from mobile, with live progress.** `host.update` verbs fixed + admin update-progress rendering |
| 2026.724.335-alpha | **Release lanes split per component** — `mobile-v*` / `hub-v*` / `host-v*` tag namespaces (desktop already separate on `electron-v*`) |
| 2026.722.236-alpha | **CalVer.** Versions become `YYYY.MMDD.HHMM` (UTC build time), a valid monotonic semver so updaters survive the switch |
| v1.0.822 | Terminal reconnect UX fixes (stuck error overlay, breakglass terminal) |
| v1.0.821 | **Cross-device SSH key-vault sync (ADR-052 D-4)** — zero-knowledge sync of connections + keys through the hub |
| v1.0.820 | Hub robustness sweep (#74–#79) + automated storage maintenance (ADR-045 D4) |
| v1.0.816-819 | **ADR-048 vocabulary-preset runtime + the full #138 i18n sweep.** Role nouns swappable by preset (tech/business/political/research) orthogonal to locale; last hardcoded strings removed; `lint-hardcoded-strings.sh` ratchet guards the win. ADR-048 complete |
| v1.0.811-815 | **ADR-047 design-system enforcement** — named tokens (spacing/radius/type/color) become the single source of truth across the Flutter UI; semantic status-container tokens; lint-enforced |
| v1.0.809 | **ADR-046 projects-from-inline-spec (WS0–WS5)** — a project's full spec lives in its own `config_yaml`; create is a governed `project.create` proposal; presets are reference examples; phases early-bind with completion-gating |
| v1.0.808 | **ADR-045 P2 — hub storage scaling.** Event + digest stores shard per team (`<dataRoot>/teams/<team>/{events.db,digest.db}`) behind an LRU-bounded connection registry; `hub.db` stays the global control plane. Per-team parallel fold worker (fold lag bounded by core count, not team count); Tier-1 SQLite pragma tuning (`temp_store=MEMORY`, 256 MiB mmap, writer-only cache); `hub-server db split-teams` offline migration. Measured ~+33 % aggregate ingest when sharded to core count (~1100 ev/s flat-out on 2 vCPU); ~1000 concurrent agents on a 2 GB VPS with no SQLITE_BUSY cliff. Follow-on probe-driven fixes (`4407e12`): insights cache-key + bounded per-team writer cache. ADR-045 Accepted (D1+D2 shipped; D3 selectable Postgres backend decided, not built) |
| v1.0.805-806 | **Channel-free agent comms + Me-page action boundary.** Channels deferred to a future experiment (tools stay in tree, no prompt instructs their use); steward status routes 3 ways — chat reply / `post_notice` FYI (new answerless tool → Me-page Messages) / `request_*` decision → Requests. Me-page classifies Requests-vs-Messages by action (`pending_payload`); dismissable Messages via guarded `/attention/{id}/resolve` |
| v1.0.804 | **ADR-044 adaptive project lifecycle (P1–P3).** The project lifecycle is a roadmap, not a fixed template contract: agents materialize deliverables, criteria editable via `propose`, AC-driven system-approved phase advance (human gating = `gate` criterion). P1 added `criteria.list`/`phase.status` MCP reads; P2 the propose verbs; P3 AC auto-advance. ADR-044 Accepted |
| v1.0.803 | **ADR-043 engine launch contract on the family (P1–P3).** Mode-selecting argv moves off the persona onto the engine family — declarative per-mode `launch` block, launcher composes. Generalizes ADR-010 (frame-profiles-as-data) to the input side. ADR-043 Accepted |
| v1.0.800-802 | **Tech-debt cleanup + ADR-042 dense session ordinal + team management from mobile.** WS1–WS3 internal cleanup; `session_ordinal` as canonical session-scoped identity (fixes resume/navigator wrong-row); operator team CRUD from the mobile app |
| v1.0.784-799 | **ADR-038/039/040/041 — the Insight workbench arc.** Per-run event digest + `agent_turns` turn index (ADR-038) + OTLP trace export (P3); Insight transcript lens as a server keyset query (ADR-039); transcript surfaces decoupled by mode into `LiveFeed` + `InsightTranscript` (ADR-040); the card-filter / Navigator-outline / Sessions-rail workbench layout (ADR-041, R1–R4). Plus run-detail UI (trackio/wandb-style Overview/System/Config) and agent-surface consolidation onto one full-screen `SessionChatScreen` (retired the project-agent bottom sheet). Preceded by the v1.0.768-783 transcript-navigation arc (lens bar, minimap, turn stepper) that motivated the rebuild |
| v1.0.752-767 | **ADR-037 multi-team isolation W1–W6 + stop/terminate naming split.** Path-team authorization gate (W1); operator/principal split (W2); team provisioning endpoint (W3); per-team template overrides (W5); team-scoped agent workdir (W6); cross-cutting isolation sweep. Naming: `stop` (resumable, paused) split from `terminate` (permanent, archived); `agents.resume` is the inverse of `terminate`. ADR-037 D1–D7 |
| v1.0.704-728 | **ADR-036 Phase B polish + multi-engine telemetry parity + security hardening.** statusLine cost/rate-limit/200K chips polished on-device; codex M2 + antigravity statusLine parity with claude-code M4; configurable principal-directive envelope templates with mobile hot-reload. Security sweep: agent-kind tokens refused as REST bearers, principal-override identity bound to the authenticated token, filesystem path-traversal closed on template install + project docs, cross-host blob read path hardened |
| v1.0.696-703 | **ADR-036 claude-code statusLine as authoritative M4 telemetry channel.** Phase A (hub-side, v1.0.696-698): host-runner `status-fire` shim + UDS gateway `status_line` tool + adapter caches latest frame for in-process overrides + session_id rotation handler (fixes /clear blindness). Phase A.5 (v1.0.699): cold-open mobile bubble + busy-state regression fixed via testable kind-list sets. Phase B (v1.0.700-703): hub-side pricing infrastructure (operator-override YAML + //go:embed default, mtime hot-reload) + GET /sessions/{id}/cost endpoint + 5 mobile chips landing on the agent telemetry strip (process cost · session cost · 5h rate-limit · 7d rate-limit · 200K alarm) + AppBar `session_name` fallback. ~5,840 LOC + ~117 tests across 9 wedges. ADR-036 Accepted |
| v1.0.674-695 | **ADR-030 governed actions + `propose` verb — all phases complete.** Generalises apply-on-approve from two bespoke branches to one MCP verb across 5 propose kinds (deliverable.set_state, phase.advance, task.set_status, agent.spawn, template.install) with Validate/DryRun/Apply/Rollback. 22 wedges across 24 commits: Phase 1 (hub, 11 wedges, ~2,775 LOC + ~85 tests) → Phase 2 (steward prompts + 3 scenarios) → Phase 3 (mobile cards + propose inbox + override sheet + policy viewer + stalled digest). ADR-030/032/033/034 all landed (envelope on fan-back, unified tool registry, questionAttentionKinds extension). Loop-closure audit catches every governed-action mutation. ADR-030 Accepted |
| v1.0.624-630 | Seven-release follow-on series after the v1.0.620-623 spawn-robustness bundle: buildSpawnVars sibling-boundary fix (un-merged read); unbound `{{project_id}}` + `{{parent.handle}}` prompt refs + Layer-4 startup audit; steward wake never reached engine (unhandled `input.task_completed` kind across all drivers) → unified on `input.text`; worker/steward prompt blocked-protocol + validate-before-delegate; preserve blocked verdict on manual stop; mobile archive agent gets Feed tab; `documents.get` MCP tool + A2A sender attribution in relay body. Plus design landings: discussions + plans + ADRs 031/032 (Proposed) for the agent-tool-ergonomics and message-routing-envelope work the band-aids in this series motivated |
| v1.0.620-623 | ADR-030 governed actions + spawn-robustness bundle. coder.v1 spawn incident class closed structurally: typed validators for 7 HIGH-severity free-form fields; agent-template naming spec + filename↔internal-id audit; steward prompt rewrite; MCP description hygiene rule; sync-wait three-state return for `agents.spawn`. ADR-030 Proposed (governed actions + propose verb) |
| v1.0.612-619 | Eight follow-on fixes after the spawn-robustness bundle: `project_steward_request` resolution fans back to the general steward; A2A notification flipped to sender-side (`a2a.sent`); task close-out protocol footer in CLAUDE.md; per-engine context file (CLAUDE/AGENTS/GEMINI.md — codex/kimi/gemini stewards had been silently ignoring their prompt body); task-detail screen collapsed seven sections to three; `permission_mode` empty→skip default on MCP spawn + Library reset menu; host-runner terminate logging; abandoned-task auto-derive to `cancelled` + project-scoped agent history |
| v1.0.611-alpha | ADR-029 Phase 1.5 + Phase 2 (tasks-first-class complete) + ADR-028 Phase 1. Worker delivery (task body → CLAUDE.md `## Task` section + close-out footer) + auto-posted first input + `tasks.complete` verb + `result_summary`; `task.notify` / `run.notify` / `a2a.sent` system-event notifications into the receiver's session; mobile `_TaskTile` triad (assignee + assigner + relative time) + task detail screen (linked spawn feed + session + filtered audit timeline) + denormalized list joins + pull-to-refresh. ADR-028 P1: `hub-server shutdown-all`. ADR-029 fully shipped → Accepted |
| v1.0.610-alpha | ADR-029 Phase 1 — tasks first-class. `agents.spawn` accepts `task_id` or inline `task`; flip-on-spawn + most-recent-spawn auto-derive on agent terminal status; `tasks.delete` MCP wrapper; `cancelled` terminal status (sticky against auto-derive); audit at six task-mutation sites; `NoteKind.todo` → `NoteKind.reminder` rename with on-device migration; glossary entries for task/note/todo |
| v1.0.609-alpha | Cross-scope session guard — three-layer fix to the StewardStrip-tap-creates-phantom-project-session bug (mobile route guard + hub 400 + scope-preferred lookup). Plus offline host chip color on the Hosts screen |
| v1.0.600-608 | Library polish + project-steward UX + A2A audit + Sessions categories — Library tab clone-from-existing + project-templates + collapsible sections + search; Me-page `template_proposal` preview + inline project-steward action; `agents.spawn` MCP auto-injects `parent_agent_id`; pull-to-refresh on Agents tab; `a2a.message_sent` row in `audit_events` (sender attribution + 200-char body preview); Sessions screen grouped by general/project/domain stewards + detached; lifecycle test scenarios 17-24 |
| v1.0.592-599 | ADR-027 LocalLogTailDriver + template authoring surface — M4 swap to JSONL tail + `--permission-prompt-tool` approval channel + host-runner UDS gateway hook surface (9 MCP tools). Project-scoped stewards auto-derive workdir; mobile spawn-steward engine row reads YAML mode+model; template authoring scaffold tools + `applicable_to` scope filter |
| v1.0.580-583 | Settings IA refactor — six-category Settings home (Display / Input / Files & Media / Data / System / About) + search bar + TeamSwitcher polish. Defines the device-vs-team scope model carried by later screens |
| v1.0.575-588 | ADR-026 Kimi Code engine — fourth ACP engine. Engine family register + MCP config writer + `cmd` splice + ACP auth-required surfacing with engine-specific remediation + steward template + capability matrix doc. W6-W7c fixes for resume cursor splice, mode/model RPC synthesis, currentId capture |
| v1.0.564-574 | ADR-025 project steward accountability — workers project-scoped + sessioned; one steward per engaged project (lazy, consent-gated); general steward delegates via `request_project_steward`; spawn-FAB reroutes through project steward; `agents.spawn` project-binding gate (W9); steward-mediated agent reconfig CTA |
| v1.0.531-547 | Voice input Path C + deep-link router — Alibaba DashScope WebSocket ASR (PCM16 streaming via `record ^6.0.0`); Mode A puck long-press + Mode B panel mic; settings + session orchestrator. Deep-link router expansion routes hero / tiles / sub-routes (incl. `project/<pid>/documents/<docId>`) through the chassis |
| v1.0.489-530 | ADR-024 project detail chassis + PDF viewer feature recovery — Wave 2 + canvas-app artifact viewer + hero consolidation + multi-run views. PDF viewer rebuilt on pdfrx 2.2.24 pin (after the 2.3.x native-assets regression): tappable links + page badge + TOC drawer + find-in-PDF + go-to-page. Deliverables show real component name + artifact kind |
| v1.0.473-488 | ADR-023 agent-driven mobile UI — persistent overlay panel + URI intent channel; agent ↔ system wire format is URI strings (typed router stays internal). D1-D11 locked (cf. project memory) |
| v1.0.444-462 | ADR-022 observability — `/v1/hub/stats` + scope-parameterized `/v1/insights` (project/team/agent/engine/host) + A2A relay throughput + 6 mobile entry points (Hosts tab Hub group, Hub Detail, Activity AppBar, Me Stats card, Agent Detail tab, Host Detail button) + Tier-2 drilldowns (engine arbitrage, multi-host distribution, tool-call efficiency, lifecycle flow). W5e ($/X) / W5f (snippet) / W6 (rollup trigger) deferred post-MVP |
| v1.0.430-435 | ADR-021 Phase 4 — cross-engine image content block inputs (hub `images:[]` contract + claude/codex/ACP wire shapes + gemini-exec strip-and-warn + mobile attach UI w/ thumbnail strip) |
| v1.0.420-424 | ADR-021 Phase 2 — mode/model picker (cross-engine wire-path fan-out: M1 ACP rpc / claude+codex respawn / gemini-exec per-turn argv) + mobile chip strip |
| v1.0.410-413 | ADR-021 Phase 1 — `session/load` resume cursor + `authenticate` dispatch + mobile replay-event dedupe |
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

**Desktop lane** (`electron-v*`; `0.x` was the Tauri era — full record in
[`changelog-desktop.md`](changelog-desktop.md)):

| Version | What |
|---|---|
| 2026.817.322 | **The Companion becomes a place you can run an agent, and Read becomes a discovery workspace.** A claude (L3a) or codex (L4c) session runs on this machine with no hub in the path and survives quitting the app (L3b) — transcript restored from our own append-only log, memory from the vendor's native resume, because those two fail separately. The transcript around it gained streamed command output and inline media (R4), a sub-agent panel with real provenance (R5), and model/permission pills that appear only where the switch is real (R6). `author_guide` gives the agent the format rules before it writes (coworking C2+C3), and UI-context sharing reseeds claude and codex, not just kimi (F4). Read went from library to workspace: six literature sources plus Google Scholar via SerpApi, cadence monitors with history, social subscriptions, ratings, provider-attributed citation counts. Plus rich Markdown/CSV Inspect previews, a bundled Maple Mono NF CN terminal font with the macOS DOM renderer, a background SFTP transfer queue, project archiving and typed project documents, appearance controls, and a monochrome theme with integrated window chrome. **vision-parity W4 complete** |
| 2026.805.1022 | **The agent becomes a co-worker in the local tabs.** `author_read` / `author_apply` / `author_render` behind a per-(agent, target, session) consent lease rather than bearer scope (ADR-064); live-apply adapters so an external write reaches the mounted editor (diagram, canvas, excalidraw, tables, figures); ID-addressed diagram ops with all-or-nothing batches; `desktop_open` as the navigate class. Alongside it the Companion telemetry a session needs — context ring, engine-reported cost, turn footers, inline approval cards — plus the comparison wall with decision records, Inspect media previews, and the TypeScript frame-profile interpreter pinned to the hub's Go one |
| 2026.730.1242 | **The largest desktop cut so far — five plans reached their shipping wedges at once.** J8 Replay end-to-end (dataset rail + episodes table, episode player with channel plots + multi-camera video, URDF forward kinematics + 3D pose, Rerun companion + export incl. remote-over-SSH); architecture graph goes hybrid-aware with honest KV-cache math, zoom/annotate/SVG-PNG export, and config-vs-config diff; **agent browser bridge W1–W3** (read → opt-in actions + audit → hub-relayed remote driving with approval cards, ADR-059); **split pane S1–S3**; **ui-context D1–D2** (`ui_get_focus` off-by-default + annotation-overlay pointing) and D2.1 (global annotate trigger); env profiles + sealed secrets + teleport on desktop; SSH jump-host/SOCKS5 parity with `ssh_config` import; unified assistant dock (kimi web + Companion tabs) |
| 2026.727.206–938 | **The Inspect arc.** Project trees over local/SFTP/hub/forge roots; config-only model view with VRAM + FLOPS estimators; LeRobot policy/VLA configs; roots context menus; Windows kimi-web fix |
| 2026.724.305/405 | **Projects task board W1** — master-detail kanban with rich cards (W2 inline edits + W3 agent-aware drag-and-drop and filters followed direct-to-main) |
| 2026.723.247 | **Author workbench overhaul** — shell + outline + canvas (figures, draw.io, Excalidraw), Discover results that survive tab switches |
| 2026.722.1327 | **Read: real `<webview>` browser tab + open-access PDF download** with a downloads shelf |
| 2026.722.211–818 | **CalVer; Tauri lane retired (ADR-055 M3.4); M4 Chromium paydown** — Playwright-driven Electron e2e harness, native context menus, `<webview>` embeds |
| 0.1.0–0.3.87 (2026-07-05 → 07-22) | **The workbench build-out** (ADR-050/051/052/053): WS2–WS8 — three-region shell + navigator + palette, SSE transcripts with Live/Insight/History, approvals dock, projects + docs + runs, admin cockpit, breakglass SSH terminal + key vault + WASM crypto + folder sync, packaging; transcript visual redesign; reference library; then the Electron migration M0–M3.x that ended the Tauri era |

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

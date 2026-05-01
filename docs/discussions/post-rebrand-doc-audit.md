# Post-rebrand documentation audit

> **Type:** discussion
> **Status:** Open (2026-05-01) — resolves to a backfill plan once gaps are triaged.
> **Audience:** reviewers · contributors (humans + AI agents)
> **Last verified vs code:** v1.0.350-alpha
> **Scope:** post-rebrand only (commits and conversations from 2026-04-14 forward, plus the immediate pre-rebrand week that captures *why* we forked and pivoted).

**TL;DR.** Termipod rebranded from `mux-pod` on 2026-04-14 (commit `e0ecf94`) and pivoted from a remote-tmux client into a multi-agent platform. Across the next 17 days the project shipped 523 commits, 4 spine docs, 15 ADRs, 9 plans, 19 discussions, 6 how-tos, and 87 memory entries. A pass over the project's session transcript (1,343 substantive user/AI exchanges, 2026-04-07 → 2026-05-01) finds **~19 load-bearing rationale gaps** and **~25 nice-to-have clarifications** where decisions were made and lessons learned but never written into a doc-spec primitive. This report is the navigable index of those gaps; it is the input to pass 3 (backfill).

---

## 1. Method

The audit ran in two passes against three sources:

- **Source A — git log since `e0ecf94`** (523 commits). Anything before the rebrand is mux-pod legacy and out of scope.
- **Source B — auto-memory** at `/home/ubuntu/.claude/projects/-home-ubuntu-mux-pod/memory/` (87 entries indexed in `MEMORY.md`).
- **Source C — session transcript** (single jsonl, 345 MB, 80,665 entries). After filtering tool results, system reminders, and pure acks, 1,343 substantive user/AI exchanges remain — the design conversation captured in flight.

Pass 1 sized the corpus and confirmed the fork-detach point.

Pass 2 extracted the 1,343 exchanges, clustered by topic via keyword scoring (35 topics; see `/tmp/audit/dossiers/` for per-topic dossiers), then dispatched 8 parallel review agents. Each agent read its assigned cluster + the relevant existing docs, and reported gaps that satisfy two tests:

1. **Load-bearing**: a reviewer or future contributor cannot understand the project's current shape without it.
2. **Not derivable** from reading the doc-spec, the spine, or the code.

The agents reported 50 candidate gaps; this report dedupes and merges them into 19 load-bearing + 25 nice-to-have entries.

---

## 2. Gap matrix (topic × primitive coverage)

Cells: `✓` = adequate · `~` = partial · `—` = gap. "Memory only" means rationale exists in the auto-memory file system but no doc-spec primitive covers it.

| Topic cluster | Spine | ADR | Plan | Discussion | Reference | How-to | Memory | Verdict |
|---|---|---|---|---|---|---|---|---|
| Fork & rebrand pivot | — | — | — | — | — | — | ✓ (one entry) | **gap** |
| Personal-tool positioning | — | — | — | ~ | — | — | ✓ | **gap** |
| Hub topology / tailnet assumption | ~ | — | — | — | — | ~ | — | **gap** |
| MCP consolidation | — | ✓ (002) | — | ~ | — | — | ✓ | adequate |
| A2A relay | — | ✓ (003) | — | ~ | — | — | ✓ | adequate |
| Channels as event log | ~ | — | — | — | — | — | — | **gap** |
| Schedules instantiate plans (forbidden #11) | ~ | — | — | — | — | — | — | **gap** |
| Three-layer host-runner separation | ~ | — | — | — | — | ✓ | — | partial |
| Hub-tui language choice | — | — | — | — | — | — | — | nice-to-have |
| Stewards: single MVP | — | ✓ (004) | ✓ | — | — | — | ✓ | needs-amend (status) |
| Stewards: layered (general + domain) | — | ~ (001 D-amend-2) | ✓ | — | — | — | ✓ | **gap (own ADR + reference)** |
| `@steward` handle + ensure-spawn endpoint | — | — | — | — | — | — | ✓ | **gap** |
| Manager/IC invariant for general steward | — | ~ (016) | — | — | — | — | ✓ | **gap** |
| Engines — frame profiles | — | ✓ (010) | ✓ | ~ | ✓ | — | ✓ | adequate |
| Engines — codex | — | ✓ (012) | — | — | — | — | ✓ | adequate |
| Engines — gemini | — | ✓ (013) | — | — | — | — | ✓ | adequate |
| Engines — claude resume | — | ✓ (014) | — | — | — | — | ✓ | adequate |
| Permission model — auto-allow design | — | ~ (005, 011) | — | — | — | — | ✓ | **gap** |
| Permission model — vendor contract asymmetry | — | ~ (011) | — | — | — | — | — | **gap** |
| Governance roles ontology (principal/steward/worker) | — | ✓ (005) | — | — | — | — | ✓ | needs-axiom |
| Subagent scope manifest | — | ✓ (016) | — | — | — | — | ✓ | adequate |
| Sessions — close→archive + fork + scope | ~ | ✓ (009) | ✓ | ✓ | — | — | ✓ | adequate |
| Multi-writer fork invariant | — | ~ (014) | — | — | — | — | ✓ | **gap** |
| Attention → notification surface mapping | — | ~ (011) | — | ~ | ~ | — | ✓ | **gap** |
| Rate-limit / token-bucket model | — | — | — | — | — | — | ~ | **gap** |
| Demo — Candidate A locked | — | ✓ (001) | ✓ | ✓ | — | ✓ | ✓ | adequate |
| Demo — lifecycle amendment rationale (2026-04-30) | — | ~ (001 D-amend-1) | ✓ | ~ | — | — | ✓ | **gap** |
| 2026 competitive landscape research | — | — | — | ~ | — | — | ✓ | **gap** |
| Egress proxy (post-MVP) | — | — | — | — | — | — | ✓ | nice-to-have |
| Domain-pack Phase 1 (post-MVP) | — | — | — | ✓ | — | — | ✓ | nice-to-have |
| Artifacts primitive (4-axis outputs) | ✓ (§6.6) | — | — | — | — | — | ✓ | partial |
| Activity feed (audit_events schema) | ~ | — | — | — | — | — | ✓ | **gap** |
| Offline snapshot cache | — | ✓ (006) | — | — | — | — | ✓ | partial |
| Transcript source of truth | — | ~ (014) | — | ✓ | — | — | ✓ | adequate |
| Snippet / action-bar system | ~ (IA §6.4) | — | — | — | — | — | ✓ | **gap** |
| Persistent steward Me-tab card | ~ (IA §6.7) | — | — | — | — | — | ✓ | **gap** |
| Local notifications | — | — | — | — | — | — | ~ | nice-to-have |
| Data export / import format | — | — | — | — | — | — | ✓ | nice-to-have |
| Compose drafts | — | — | — | — | — | — | ✓ | adequate |
| IA wedges 1–7 | ✓ (§11) | — | — | — | — | — | ✓ | adequate |
| ADR-015 (numbering gap) | — | — | — | — | — | — | — | meta-gap |

---

## 3. Load-bearing gaps — must fill

For each, "Substance" is what the transcript / memory shows but docs don't. "Where" names the primitive + filename. "Why load-bearing" is the test you can run to fail without it.

### 3.1 The pivot itself — fork-detach + rebrand decision
**Substance.** On 2026-04-14 (`e0ecf94`) the project broke compatibility (`si.mox.mux_pod` → `com.remoteagent.termipod`), accepted that existing installs cannot upgrade in-place, and committed to a different product category. The transcript captures the framing ("personal tool, not enterprise multi-tenant"; "if you want a single-engine remote-control app, use claudecode-remote — we're aiming higher"). No ADR records this; only the memory file `project_todo_rename_to_termipod.md` (DONE). A new contributor arriving via git log will see a `feat!:` rename commit and no rationale.
**Where.** `decisions/015-fork-detach-and-rebrand.md` (this is also the natural backfill for the ADR-015 numbering gap — see §5.1).
**Why load-bearing.** Anyone asking "why aren't we just adding features to mux-pod?" or "why did we drop SOCKS5/SFTP polish to ship a hub?" needs a single linkable answer.

### 3.2 Personal-tool / hub-mvp positioning rationale (2026-04-19)
**Substance.** The user explicitly asked for objective competitive research before committing to the hub MVP. The agent ran a multi-source landscape survey (Sakana AI Scientist v2, Google PaperOrchestra, IKP/01.me, AIDE, MLE-Bench, STORM, claudecode-remote, Happy, Codex web). Conclusion: "the differentiator is multi-host × multi-session × multi-engine for a single director, framed as a personal lab assistant." `discussions/positioning.md` exists but is comparative-feature-only; the *strategic frame* ("personal tool, not multi-tenant SaaS") is in transcript only.
**Where.** Expand `discussions/positioning.md` with a "Strategic frame" section, or new `discussions/termipod-as-personal-tool.md`.
**Why load-bearing.** Investor and partner conversations cite this; it shapes every downscope decision (no enterprise RBAC, no billing, no per-user steward in MVP).

### 3.3 Tailnet assumption replaced reverse-SSH
**Substance.** Early hub design included ~500 lines of reverse-SSH tunnel code so the hub could reach hosts behind NAT. The user observation "all our hosts are already on the same headscale/tailscale network" eliminated that subsystem. Decision: assume tailnet membership; cross-host A2A tunnels through the hub relay (ADR-003) only as the *cross-trust* path, not for reachability. Memory has no entry; the choice is implicit in the install how-tos.
**Where.** `decisions/0NN-tailnet-deployment-assumption.md` or amend ADR-003 with a "Reachability assumption" section.
**Why load-bearing.** Anyone planning a non-tailnet deployment will hit a wall and need to reconstruct the rationale.

### 3.4 Channels as event log + correlation, not exchange table
**Substance.** Around 2026-04-19 a design choice locked: channels hold the event record (one append-only stream per channel); A2A replies, task results, and subagent traffic are correlated via optional `task_id` / `correlation_id` fields rather than a separate exchanges/threads table. This unifies storage and renderer. The forbidden-pattern rule (blueprint: "schedules don't spawn agents directly; they instantiate plans") is the same family of decision and shares the rationale.
**Where.** `decisions/0NN-channels-as-event-log.md` (small, focused) + a paragraph expansion in `spine/blueprint.md` §6 around forbidden pattern #11 (schedules→plans→agents).
**Why load-bearing.** Future "let's add an exchanges table" or "let's let cron spawn agents" PRs need the reasoned no.

### 3.5 Three-layer separation rationale (hub / host-runner / engine)
**Substance.** `spine/blueprint.md` §3.2–§3.3 names the three layers. Transcript captures the *teaching narrative* — host-runner is a deterministic deputy because (a) the agent is ephemeral, (b) compute is bound to a host, (c) the deterministic/stochastic boundary is auditable. New contributors keep asking "why not collapse host-runner into hub or into the agent?"
**Where.** Expand `spine/blueprint.md` §3.4 with a "Why three layers" sub-section, or a short `discussions/three-layer-separation.md` resolving to that amendment.
**Why load-bearing.** This question repeats. Document once.

### 3.6 Layered stewards — own ADR + reference contract
**Substance.** ADR-001 D-amend-2 (2026-04-30) introduces general (frozen, persistent, `@steward` handle) + domain (overlay-authored, project-scoped, `*-steward` suffix) stewards. ADR-004 still says "single steward MVP" with no supersede note. The handle convention, the singleton ensure-spawn endpoint (`POST /v1/teams/{team}/steward.general/ensure`, race-coalesces on unique-handle), the frozen-template invariant, and the manager/IC boundary are scattered across memory + W4–W5 wedge plan + concierge prose.
**Where.** Three actions:
1. New `decisions/0NN-layered-stewards.md` (ADR with the handle convention, ensure-spawn idempotency contract, frozen-template invariant, and manager/IC boundary).
2. `reference/steward-templates.md` (bundled steward.general.v1 contract; what overlay can / can't shadow; engine selection per template).
3. Status block on `decisions/004-single-steward-mvp.md` updated to "Superseded by ADR-NNN-layered-stewards" with a forward link.
**Why load-bearing.** Today a reviewer landing on ADR-004 will believe MVP is single-steward and misunderstand the live `@steward` concierge.

### 3.7 Permission model — auto-allow design principle + vendor asymmetry
**Substance.** The user explicitly directed (2026-04-25) that "claude can use common tool just as user uses claude on a pc" — routine tool calls auto-allow; only strategic decisions surface attention. The system supports three permission modes (native `canUseTool` hook, MCP-routed `permission_prompt`, `--dangerously-skip-permissions`), but only the MCP mode is documented. ADR-011 D6 names `permission_prompt` as a Claude-only sync exception without explaining the *vendor contract asymmetry* — Claude's hook is sync request/response with no deferred branch; Codex's JSON-RPC has one. This shapes how each engine's approval bridge works.
**Where.** `reference/permission-model.md` (three modes, when each kicks in, vendor contract shapes per engine, where to add a new engine's gate). Cross-link from ADR-011 D6 with a "Vendor contract" sidebar.
**Why load-bearing.** Adding a new engine requires picking the gate; without this doc, contributors guess and re-derive. Also: the design is a *positive opinion* (auto-allow tool calls) — without a doc, it reads as a missing feature.

### 3.8 Governance roles ontology (principal / director / steward / worker / operator)
**Substance.** ADR-005 establishes the principal/operator distinction ("user is principal/director, not operator"). The transcript fleshes out the full ontology: **principal** = owner with ultimate authority; **director** = principal-in-context-of-direction; **steward** = CEO-class operator (delegates IC work, surfaces strategic decisions); **worker** = IC subagent. Memory captures it (`feedback_steward_executive_role.md`, `feedback_ux_principal_director.md`) but the canonical ontology is nowhere in `docs/spine/` or `docs/reference/`.
**Where.** `spine/governance-roles.md` (axiom — slow-changing, foundational) or a new section in `spine/blueprint.md`.
**Why load-bearing.** Half the prompts and UI labels in the codebase use these terms; without a single canonical definition, drift is inevitable.

### 3.9 Manager/IC invariant for general steward
**Substance.** General steward "manages" — authors templates, schedules, reviews — and never does IC work (write code, run experiments). Soft-enforced by prompt; hard-enforced by ADR-016 D6 role middleware + D7 self-mod guard. Never explicitly stated as a principle.
**Where.** Either inline in 3.6's new layered-stewards ADR, or `decisions/0NN-manager-ic-invariant.md`. Add a glossary entry.
**Why load-bearing.** Without it, the next prompt iteration could let general steward "just write a small fix" and silently break the role boundary.

### 3.10 Multi-writer fork invariant (engine session stores)
**Substance.** ADR-014 D7 has fork = cold-start (drops `spawn_spec_yaml`). The *why* is that engine session stores assume a single live attacher: Claude's `~/.claude/projects/<cwd>/<sid>.jsonl`, Gemini's `<projdir>/.gemini/sessions/<uuid>`, Codex's CLI thread store all race on concurrent writes if two sessions resume the same id. Future fork-feature work (resume-from-turn-N, distillation) will collide with this constraint.
**Where.** Expand ADR-014 with an "Engine session-store assumptions" section (~3 sentences).
**Why load-bearing.** A future "make fork resumable" attempt will silently corrupt engine state without this guardrail documented.

### 3.11 Attention → notification surface mapping
**Substance.** ADR-011 specifies turn-based delivery for attention kinds. ADR-005 lists which actions trigger `request_approval / select / help`. But the *surface mapping* — which attention kinds become FCM/APNs notifications, which stay in-app, which trigger badge counts — is implicit in code (recent commits 4d0317a, 55bada2, 8caff8a). User UX questions ("when does the steward pop a decision?") have repeated.
**Where.** `reference/attention-delivery-surfaces.md` — table of `attention_kind` × surface (push, in-app, badge, silent) × timeout × example.
**Why load-bearing.** Adding a new attention kind requires this mapping. Today contributors copy from the nearest example without a contract.

### 3.12 Rate-limit / token-bucket model
**Substance.** Hub enforces rate limits per agent / team / session. Recent fix `857c151` ("handle µs/ns resetsAt") exposed that the model is undocumented — display bug surfaced as "resets in 1540333567h" because the parser assumed seconds while server emitted µs. No reference doc says what the buckets are, what the refill cadence is, what triggers overflow.
**Where.** `reference/rate-limiting.md` — one page: bucket shape, scope (per-agent / per-team / per-session), refill, overflow, headers, mobile rendering contract.
**Why load-bearing.** Limits are governance. Limits without documentation = mystery throttles.

### 3.13 Lifecycle amendment rationale (2026-04-30)
**Substance.** ADR-001 D-amend-1 expanded the demo from single-phase experiment to 5-phase research lifecycle. The amendment names the new shape but is thin on *why now* — what changed on 2026-04-30. Transcript: the 2026 multi-agent research-automation landscape (Sakana AI Scientist v2, Google PaperOrchestra, IKP/01.me) raised the bar from "ablation sweep" to "agent-authored end-to-end paper." The amendment's reasoning chain, plus the architectural note that *the existing primitives already support it (no new schema)*, is in transcript only.
**Where.** `discussions/lifecycle-amendment-2026-04.md` (resolved → ADR-001 D-amend-1) — or expand the ADR with a "Design pressure" subsection.
**Why load-bearing.** The next external presentation needs to explain *why we widened the demo* in one citable paragraph.

### 3.14 2026 competitive landscape synthesis
**Substance.** The user requested (2026-04-19) and reviewed a substantial competitive analysis covering Sakana AI Scientist v2, Google PaperOrchestra, IKP/01.me, AIDE, MLE-Bench, STORM, claudecode-remote, Happy, Codex web. Findings shaped feature picks (steward as orchestrator, A2A relay, frame profiles). Synthesis lives in transcript; only fragments in `discussions/multi-agent-sota-gap.md` and `discussions/integrating-open-source-agents.md`.
**Where.** `reference/competitive-landscape-2026.md` (lookup material — frozen at audit date, refreshed quarterly) — or `discussions/2026-autonomous-research-landscape.md`.
**Why load-bearing.** External-facing material (investor decks, partner outreach) cites this work as having been done. Without a citable artifact, it's hearsay.

### 3.15 Activity feed (audit_events) schema and contract
**Substance.** Activity feed reuses `audit_events` (one entry: `project_activity_feed_foundation.md`). Memory says "agent.spawn / run.create / document.create / template.edit / steward.spawn …" emit but the canonical action taxonomy + `meta_json` shape per action are unwritten. New code that should emit an audit event has to reverse-engineer the convention.
**Where.** `reference/audit-events.md` — action taxonomy, `meta_json` per action, the `recordAudit` API, mobile render contract.
**Why load-bearing.** Audit log is governance; under-documented audit logs decay fast.

### 3.16 Snippet / action-bar system
**Substance.** Two months of design across 54 transcript exchanges + many commits: preset profiles (Claude Code, Codex) with 30+ snippets each, variable substitution (text vs option-enum), per-pane profile state keyed by `${connectionId}|${paneId}`, send-immediately vs insert flag, the relationship between snippets / history / recent. IA §6.4 names the surface; nothing documents the data model.
**Where.** `reference/action-bar-system.md` — preset structure, snippet schema, variable types, per-pane state, render contract.
**Why load-bearing.** This is one of three core feature axes (along with sessions and attention). Bug fixes and new presets keep landing without a spec to validate against.

### 3.17 Persistent steward Me-tab card (concierge framing)
**Substance.** Shipped commit `8caff8a` (2026-04-30, W3 partial). The card surfaces `@steward` on Me as an always-available concierge. IA §6.7 enumerates four steward access points but doesn't address Me-card prominence or how it interacts with attention escalation. The "concierge" framing — what general steward will and won't do without a project context — is in memory only.
**Where.** Either an amendment to `spine/information-architecture.md` §6.1 with version tag, or a small `discussions/steward-me-tab-card.md` resolved into the IA.
**Why load-bearing.** This card is the user's primary entry to AI orchestration; its semantics need to be canonical.

### 3.18 Why schedules instantiate plans (forbidden #11)
**Substance.** Blueprint forbids schedules spawning agents directly; schedules instantiate plan templates. Transcript captures the rationale: audit trail of plan runs, reviewability, reproducibility. The rule is in blueprint; the *reasoning* is implicit.
**Where.** Expand `spine/blueprint.md` forbidden pattern #11 with a one-paragraph "Why" rider, or a short `discussions/schedules-instantiate-plans.md`.
**Why load-bearing.** Periodically someone proposes "let cron just spawn an agent." The reasoned no needs to be one click away.

### 3.19 ADR-004 status update (chained to 3.6)
**Substance.** Status fix only — `decisions/004-single-steward-mvp.md` should declare "Superseded by ADR-NNN-layered-stewards" or "Amended by ADR-001 D-amend-2." Currently it reads as canonical MVP guidance, contradicted by live behavior.
**Where.** Edit the status block. ~2 lines.
**Why load-bearing.** Five-minute fix; prevents reviewer confusion permanently.

---

## 4. Nice-to-have clarifications

Compactly listed. Each is a short doc, an inline expansion, or a status fix.

| # | Topic | Action |
|---|---|---|
| 4.1 | Hub-tui language choice (Go vs TS+Ink) | New ADR or `discussions/hub-tui-language-choice.md` capturing the tradeoff. |
| 4.2 | MCP consolidation depth (per-agent rejected) | Expand ADR-002 with the *against* case. |
| 4.3 | Tech stack choices (nginx not Caddy, FTS5, all-Go) | Short ADR or `reference/tech-stack-rationale.md`. |
| 4.4 | Policy hot-reload + edit-from-mobile | `plans/policy-editor-mobile-ui.md` or `how-to/edit-team-policy-from-mobile.md` (after ship). |
| 4.5 | Template versioning + change audit | `discussions/template-and-policy-versioning.md` (currently deferred). |
| 4.6 | Concierge scope explicit definition | Expand ADR-001 D-amend-2 with a "Concierge scope" subsection. |
| 4.7 | Single → layered evolution archaeology | Expand ADR-001 D-amend-2 with a "Design pressure" paragraph. |
| 4.8 | ADR-016 role coverage of general steward | Add a one-line example to ADR-016 D2. |
| 4.9 | Codex app-server investigation methodology | `discussions/exploring-engine-integration-shapes.md` (post-MVP). |
| 4.10 | Gemini PR #14504 watershed (pre/post resume) | One-paragraph rider in ADR-013 D6. |
| 4.11 | Codex feeder OQ-1 scope clarification | Plan note in ADR-014 OQ-1. |
| 4.12 | YAML-profile authoring ergonomics | Expand `reference/frame-profiles.md` §6 with hot-reload and validator workflow. |
| 4.13 | Context mutation marker motivation | One-paragraph preamble in ADR-014 OQ-4. |
| 4.14 | Close→archive rename rationale | 2–3 sentences in ADR-009 D2 (program-shaped vocabulary, not anthropomorphic). |
| 4.15 | Scope-not-budget governance rationale | 3–4 sentences in ADR-016 §Context. |
| 4.16 | Steward template authoring authority (conflict resolution) | Amendment to `discussions/research-demo-lifecycle.md` §4. |
| 4.17 | Egress proxy (post-MVP) | `decisions/0NN-agent-egress-proxy.md` or amend ADR-016. |
| 4.18 | Domain-pack Phase 1 first-party content | Promote from `discussions/post-mvp-domain-packs.md` Phase 1 into its own plan. |
| 4.19 | Engine-internal subagent scope | Amend `spine/blueprint.md` §3.3 (already promised in `discussions/integrating-open-source-agents.md` §7). |
| 4.20 | Multi-agent SOTA pattern attribution | Section in `discussions/research-demo-candidates.md` §5 or new reference. |
| 4.21 | Artifacts four-axis output model (Files / Artifacts / Documents / Assets) | `reference/artifacts-and-outputs.md`. |
| 4.22 | Offline cache storage-layering rule | `reference/hub-snapshot-cache.md` or expand ADR-006. |
| 4.23 | Transcript-architecture pointer | One-page `reference/transcript-architecture.md` linking the discussion. |
| 4.24 | Data export / import format | `plans/data-export-import.md` or `reference/data-export-format.md` (already shipped in v1.0.2). |
| 4.25 | Local notification triggers + payload | `how-to/test-local-notifications.md` + `reference/notification-rules.md`. |

---

## 5. Meta-gaps

### 5.1 ADR-015 — deliberate skip, document it
**Finding.** Two distinct ADR-015 proposals appear in the 2026-04-30 transcript: (a) "pin engine-context-mutation boundary" (folded into ADR-014 OQ-4 instead) and (b) "M5 graphical engines, Aider as DSL-extension" (deferred post-MVP). Neither was authored. The number is a deliberate skip.
**Action.** Add a one-line note in `decisions/README.md` explaining the gap, so future readers don't think they're missing a decision.

### 5.2 Tutorials directory empty
**Finding.** `docs/tutorials/` exists per doc-spec but holds no files. The doc-spec lists tutorial as an operational adjunct; if we don't intend to write any soon, an explanatory README in the directory ("we ship learning-oriented walkthroughs only when a feature stabilizes — see `how-to/` for task-oriented guides") prevents ambiguity. Otherwise: write `tutorials/your-first-agent-spawn.md` as the doc-spec's worked example.
**Action.** Write a short directory README, or land the example tutorial.

### 5.3 Doc-spec compliance pass
**Finding.** Doc-spec at `docs/doc-spec.md` was established recently (2026-04-28). Pre-spec docs may not have status blocks. A separate compliance-sweep audit (out of scope for this report) would walk every doc and add the block where missing.
**Action.** A separate plan: `plans/doc-spec-compliance-sweep.md`.

---

## 6. Backfill prioritization

Priorities are estimated effort × demo / reviewer impact. Unit ≈ one focused author-hour.

**Tier 1 — load-bearing, ship before next external review (~6h):**
- §3.1 fork-detach + rebrand ADR (1h) — also closes ADR-015 numbering gap
- §3.6 layered-stewards ADR + reference + ADR-004 status (2h)
- §3.13 lifecycle amendment rationale (1h)
- §3.16 snippet / action-bar reference (1h)
- §3.19 ADR-004 status update (5min — fold into §3.6)
- §5.1 ADR-015 skip note (5min)

**Tier 2 — load-bearing, before next contributor onboarding (~7h):**
- §3.2 personal-tool positioning (1h)
- §3.7 permission model reference + vendor asymmetry (1.5h)
- §3.8 governance roles axiom (1h)
- §3.11 attention → notification surface mapping (1h)
- §3.12 rate-limit reference (45min)
- §3.14 competitive landscape synthesis (1.5h — pull from transcript dossier)

**Tier 3 — load-bearing but localized (~4h):**
- §3.3 tailnet assumption ADR (45min)
- §3.4 channels-as-event-log ADR (1h)
- §3.5 three-layer rationale (45min)
- §3.9 manager/IC invariant (45min)
- §3.10 multi-writer fork invariant (30min)
- §3.15 activity-feed schema reference (1h)
- §3.17 persistent steward card IA amendment (30min)
- §3.18 schedules-instantiate-plans rationale (30min)

**Tier 4 — nice-to-have:** the 25 items in §4. Land opportunistically when adjacent code is touched.

Total Tier 1+2+3 ≈ **~17 author-hours** to close every load-bearing gap.

---

## 7. How to use this report

- **As a reviewer**: skim §2 to see where docs are thin; jump to §3 for any topic flagged `gap`. You should not need to read the transcript.
- **As a contributor (human or AI)**: when you're about to ship work in a topic flagged `gap`, write the doc *with* the work — don't accept "I'll document later."
- **As an author of pass 3 (backfill)**: §6 is your work queue; §3 entries are doc skeletons (substance + suggested filename + primitive).

This report itself is a discussion (Open). It resolves when (a) every Tier 1+2+3 gap has a doc and (b) the gap matrix in §2 has no `gap` cells. Update the matrix as gaps close.

---

## 8. Provenance

- Source jsonl: `/home/ubuntu/.claude/projects/-home-ubuntu-mux-pod/02b98ce1-a56d-4d28-8d2c-bf1dce9f75b7.jsonl` (345 MB, 2026-04-07 → 2026-05-01).
- Extraction: `/tmp/audit/extract.py` (1,343 substantive exchanges).
- Clusters: `/tmp/audit/cluster.py` (35 topics).
- Per-topic dossiers: `/tmp/audit/dossiers/*.md` (1.4 MB, ephemeral working set).
- Review: 8 parallel Explore agents, 2026-05-01.

Working files under `/tmp/audit/` are intentionally not checked in — they are reproducible from the source jsonl with the scripts above. If/when the audit is re-run on a later transcript, regenerate.

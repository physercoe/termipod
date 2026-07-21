# Decisions (ADRs)

> **Type:** axiom
> **Status:** Current (2026-06-07)
> **Audience:** contributors
> **Last verified vs code:** v1.0.808

**TL;DR.** The decision log. Each numbered file records one
architectural choice, why we made it, and what followed. Append-only:
once Accepted, an ADR is immutable except for status changes. New
decisions supersede old ones via the `Supersedes` link.

Read `../doc-spec.md` §6 for the lifecycle rules. New ADRs use the
next sequential number — don't reserve, don't skip.

---

## Index

| # | Title | Status | Supersedes |
|---|---|---|---|
| [001](001-locked-candidate-a.md) | Locked Candidate-A as MVP demo | Accepted 2026-04-23 | — |
| [002](002-mcp-consolidation.md) | Consolidate to a single MCP service in spawn `.mcp.json` | Accepted 2026-04-27 | — |
| [003](003-a2a-relay-required.md) | A2A relay is required (GPU hosts are NAT'd) | Accepted 2026-04-23 | — |
| [004](004-single-steward-mvp.md) | One steward per team for MVP; per-member deferred | Superseded 2026-04-30 by [017](017-layered-stewards.md) | — |
| [005](005-owner-authority-model.md) | User is owner/director; steward operates the system | Accepted 2026-04-23 | — |
| [006](006-cache-first-cold-start.md) | Mobile renders cached snapshots before network | Accepted 2026-04-27 | — |
| [007](007-mcp-vs-a2a-protocol-roles.md) | MCP for agent↔hub, A2A for agent↔agent | Accepted 2026-04-27 | — |
| [008](008-orchestrator-worker-slice.md) | Adopt the SOTA orchestrator-worker pattern (6-item slice) | Accepted 2026-04-27 | — |
| [009](009-agent-state-and-identity.md) | Agent identity and session lifecycle | Accepted 2026-04-28 | — |
| [010](010-frame-profiles-as-data.md) | Frame profiles as data — vendor schemas leave Go for YAML | Accepted 2026-04-29 | — |
| [011](011-turn-based-attention-delivery.md) | Turn-based delivery for async attention kinds | Accepted 2026-04-29 | — |
| [012](012-codex-app-server-integration.md) | Codex integration via `codex app-server` JSON-RPC, not `codex exec` | Accepted 2026-04-29 | — |
| [013](013-gemini-exec-per-turn.md) | Gemini integration is exec-per-turn-with-resume | Accepted 2026-04-29 | — |
| [014](014-claude-code-resume-cursor.md) | Claude-code resume threads `--resume <session_id>` | Accepted 2026-04-30 | — |
| [015](015-fork-detach-and-rebrand.md) | Fork detach + rebrand: mux-pod → termipod | Accepted 2026-04-14 | — |
| [016](016-subagent-scope-manifest.md) | Subagent operation-scope manifest (roles.yaml) | Accepted 2026-04-30 | — |
| [017](017-layered-stewards.md) | Layered stewards (general frozen + domain overlay) | Accepted 2026-04-30 | [004](004-single-steward-mvp.md) |
| [018](018-tailnet-deployment-assumption.md) | Hub ↔ host-runner connectivity assumes a tailnet | Accepted 2026-04-19 (back-dated) | — |
| [019](019-channels-as-event-log.md) | Channels = append-only event logs with task_id/correlation_id | Accepted 2026-04-19 (back-dated) | — |
| [020](020-director-action-surface.md) | Director-action surface on typed documents and deliverables | Accepted 2026-05-06 | — |
| [021](021-acp-capability-surface.md) | ACP capability surface — resume, auth, mode/model, image inputs | Accepted 2026-05-08 | — |
| [022](022-observability-surfaces.md) | Observability surfaces — insights aggregator + hub stats | Accepted 2026-05-09 | — |
| [023](023-agent-driven-mobile-ui.md) | Agent-driven mobile UI — overlay + URI intents + compact mode | Accepted 2026-05-10 | [005](005-owner-authority-model.md) |
| [024](024-project-detail-chassis.md) | Project detail chassis | Accepted 2026-05-11 | — |
| [025](025-project-steward-accountability.md) | Project steward accountability — workers, scope, lazy materialization, director consent | Accepted 2026-05-13 | — |
| [026](026-kimi-code-engine.md) | Kimi Code CLI is the fourth engine, M1-only | Accepted 2026-05-14 | — |
| [027](027-local-log-tail-driver.md) | LocalLogTailDriver replaces agent-mode M4 (claude-code first) | Accepted 2026-05-15 | — |
| [028](028-host-control-via-tunnel-and-cli.md) | Host control via the tunnel + a CLI ops surface | Proposed 2026-05-16 | — |
| [029](029-tasks-as-first-class-primitive.md) | Tasks as the first-class primitive for steward-dispatched work | Accepted 2026-05-18 (all phases shipped v1.0.610–611-alpha) | — |
| [030](030-governed-actions-and-propose-verb.md) | Governed actions + `propose` verb (apply-on-approve generalisation, 4-tier ladder) | Proposed 2026-05-17 | — |
| [031](031-agent-tool-ergonomics.md) | Agent tool ergonomics — two-tier descriptions, `tools.get`, structured hints, no polymorphism | Accepted 2026-05-18 | — |
| [032](032-message-routing-envelope.md) | Message routing — the orchestration message envelope `{from,to,kind,text,cause,thread}` + admission pipeline | Proposed 2026-05-18 (revised 2026-05-19) | — |
| [033](033-tool-catalog-naming-and-registration.md) | Tool catalog — one naming convention + single registration point | Accepted 2026-05-18 | — |
| [034](034-orchestration-loop-closure.md) | Orchestration loop-closure runtime — closure invariant, per-hop deadlines, stall escalation, lifecycle hooks | Proposed 2026-05-19 | — |
| [035](035-antigravity-engine-m4-locallogtail.md) | Antigravity CLI (`agy`) as the fifth engine, via M4 LocalLogTail; Gemini CLI sunset | Proposed 2026-05-22 | — |
| [036](036-claude-code-statusline-telemetry.md) | claude-code statusLine as authoritative M4 telemetry channel | Proposed 2026-05-24 | — |
| [037](037-multi-team-isolation-and-operator-principal-split.md) | Multi-team isolation + operator/principal split | Proposed 2026-05-31 | (single-team reading of [005](005-owner-authority-model.md)) |
| [038](038-per-run-event-digest.md) | Per-run event digest + turn index (`agent_turns`) + OTLP projection — canonical run summary, accurate navigation, operator traces | Proposed 2026-06-01 | — |
| [039](039-insight-lens-as-server-query.md) | Insight transcript lens as a server keyset query (`kind=` paging) | Proposed 2026-06-02 | — |
| [040](040-transcript-surfaces-decoupled-by-mode.md) | Decouple the transcript surfaces — one file per mode (`LiveFeed` / `InsightTranscript`), additive by file | Accepted 2026-06-02 | — |
| [041](041-insight-workbench-layout.md) | The Insight transcript workbench — card-filter lens, outline Navigator, Sessions rail (drop stepper + N/M pill) | Accepted 2026-06-03 | — |
| [042](042-dense-session-ordinal.md) | A dense per-session event ordinal (`session_ordinal`) — canonical session-scoped identity; fixes resume/navigator wrong-row | Accepted 2026-06-04 | — |
| [043](043-engine-launch-contract-on-the-family.md) | The engine launch contract (mode-selecting argv) lives on the family, not the persona — declarative per-mode `launch` block, launcher composes; generalizes [010](010-frame-profiles-as-data.md) to the input side | Accepted 2026-06-05 | — |
| [044](044-adaptive-project-lifecycle.md) | The project lifecycle is adaptive, not a fixed template contract — agents materialize deliverables, criteria editable via propose, AC-driven system-approved phase advance (human gating = `gate` criterion) | Accepted 2026-06-05 · Amended 2026-06-08 (early-bind + completion-gating) | — |
| [045](045-hub-storage-scaling.md) | Hub storage scaling — deferred bounded-staleness fold, event/digest store separation (per-class + per-team shards), selectable sqlite\|postgres backend (D3 decided, not built) | Accepted 2026-06-06 | (amends [038](038-per-run-event-digest.md) §2) |
| [046](046-projects-from-inline-spec.md) | A project's spec is its `config_yaml`; create is a governed `project.create` whose approval materializes the project — template/project collapse, presets as reference examples, no steward `template.install`, steward bound + spawned on Start | Proposed 2026-06-08 | (amends [044](044-adaptive-project-lifecycle.md)) |
| [047](047-design-system-enforcement.md) | Design-system enforcement — named tokens as the single source of truth (M3-aligned spacing/radius/type scales), WCAG 2.1 AA floor, one central `chipTheme`, single brand accent, no-new-violations CI ratchet | Accepted 2026-06-09 | — |
| [048](048-themed-vocabulary-overlay.md) | Themed vocabulary overlay — role terms swap by **vocabulary preset** (tech/business/political/research) × language via a `VocabPack`, orthogonal to gen-l10n; promotes the deferred wedge to MVP (tester-driven) | Accepted 2026-06-10 | — |
| [049](049-multi-agent-collaboration-via-github.md) | Multi-agent collaboration via GitHub — delegate any dev work to heterogeneous agent CLIs on different hosts; maintainer/builder roles, ticket lifecycle + tiers, two-axis identity (git-config handle vs shared account), `holds:<resource>` baton, verify-before-merge; vendor-agnostic, general (i18n was the pilot) | Accepted 2026-06-11 | — |
| [050](050-desktop-workbench-delivery-model.md) | Desktop research workbench delivery model — a local-first, hub-served **web-tech** app (second client on the client-agnostic API), not a wide Flutter layout; two-halves split (portable control plane vs component-heavy research workbench); operative rule build·embed·integrate·interop; BUILD the fleet-native surfaces (headline = multi-run comparison wall), EMBED/INTEGRATE the rest | Accepted 2026-07-04 · Amended 2026-07-05 | — |
| [051](051-desktop-client-stack.md) | Desktop client stack — **Tauri v2** shell (Win/Mac/Linux + plain browser) hosting **React + TypeScript** (chosen by the embed axis: the workbench's tldraw/BlockNote/Monaco/Rerun/Viser components are React-first); TanStack Query over REST+cache; the Rust core proxies SSE (auth header) + keychains the token; a shared **DTCG `tokens.json` → Style Dictionary → {Dart, CSS}** pipeline makes ADR-047 tokens load-bearing across both clients | Proposed 2026-07-05 | (implements [050](050-desktop-workbench-delivery-model.md)) |
| [052](052-breakglass-ssh-and-key-vault.md) | Breakglass SSH terminal — **xterm.js + Tauri `russh`** (not libghostty); **two paths** (managed host = hub-brokered keyless audited PTY; personal = direct SSH + client keys); non-secret host/connection metadata syncs; private keys share via a **zero-knowledge vault** (hub stores blind ciphertext it can't decrypt) → **amends forbidden-pattern #15**; authenticate the A2A relay + retire the cleartext backup | Accepted 2026-07-05 | (amends [forbidden-patterns.md](../spine/forbidden-patterns.md) #15; builds on [051](051-desktop-client-stack.md)) |
| [053](053-hub-reference-library-entity.md) | Hub-owned reference library entity (agent-accessible) | Accepted 2026-07-11 | — |
| [054](054-kimi-code-ts-engine.md) | kimi-code-ts: the TypeScript Kimi Code is a separate family, M1-only | In review | — |
| [055](055-desktop-electron-shell.md) | Desktop shell — **Electron** (one pinned Chromium) replacing the Tauri OS-webview matrix; IPC contract preserved behind a runtime-agnostic bridge; Rust core ported to the TS main process **except vault crypto (Rust→WASM, byte-compat with mobile)**; electron-builder/-updater distribution with a data-egress handoff release | Proposed 2026-07-21 | supersedes [051](051-desktop-client-stack.md) **D-1** (shell only; D-2–D-5 stand) |

---

## How to add an ADR

1. Pick the next number (`ls decisions/0*-*.md | sort | tail -1`).
2. Filename: `NNN-short-name.md`, lowercase-hyphenated.
3. Use the template below. All five status-block lines required.
4. Index it in this README.
5. If the ADR supersedes another, set the supersedee's status to
   `Superseded` and link forward.

### Template

```markdown
# NNN. Short title

> **Type:** decision
> **Status:** Accepted (YYYY-MM-DD)
> **Audience:** contributors
> **Last verified vs code:** vX.Y.Z
> **Supersedes:** decisions/NNN-prior.md  (optional)

**TL;DR.** One or two sentences — the decision in plain language.

## Context

What forced the question. Why now. What was tried or considered.

## Decision

What we chose. Be precise — the ADR is read for the *what*.

## Consequences

What flows from this. Things that became easier; things that became
harder; things now forbidden by the choice.

## References

- Code: paths, commits
- Related ADRs
- Discussions that fed this decision
```

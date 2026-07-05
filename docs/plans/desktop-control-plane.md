# Desktop control plane — the unified web-tech client (shell + first surface)

> **Type:** plan
> **Status:** Proposed (2026-07-05) — the **first build target** of the desktop
> workbench ([ADR-050](../decisions/050-desktop-workbench-delivery-model.md)) is
> the **control plane itself**, rebuilt in web-tech as the unified client shell
> that the research-workbench surfaces later mount into. This plan proposes the
> **tech stack** and **UI design** and sequences the build (WS0–WS8). Needs
> **[ADR-051](../decisions/051-desktop-client-stack.md)** (framework + shared-token
> pipeline), now **Proposed** (WS0 done). Grounded in a three-scan
> survey of the hub API, the mobile control-plane IA, and the ADR-047 token
> system; all `file:line` claims below were verified against HEAD.
> **Audience:** principal · contributors
> **Last verified vs code:** v1.0.821

**TL;DR.** Build the **portable control plane** half of ADR-050 first — a
**Tauri v2 + React + TypeScript** desktop app (Win/Mac/Linux + plain-browser
build) that is a *second client* on the hub's existing REST+SSE API. It is **not**
a wide replica of the phone's five mutually-exclusive tabs; it is a **multi-pane
mission control** that does what mobile structurally cannot: watch the whole fleet,
one live transcript, and the approvals queue **simultaneously**, driven by keyboard
and a command palette. The one load-bearing prerequisite is a **shared design-token
pipeline** (DTCG `tokens.json` → Style Dictionary → both Flutter Dart *and* web
CSS), because ADR-050 made cross-client visual parity load-bearing and today's
tokens are Dart-locked. Framework choice is decided by the *other* half: the
unified client must eventually host the workbench's React-first embeds (Monaco,
tldraw, BlockNote, Rerun-react, Viser) — so React is the substrate the components
we embed already speak.

---

## 1. Why the control plane is the first surface (not the comparison wall)

ADR-050's amendment (A-2) decided the control plane is **rebuilt** in web-tech into
**one unified client**, not embedded from Flutter and not deferred. That inverts the
build order: the workbench surfaces (comparison wall, reader/author, canvas,
robotics viewers) are *components that mount into a shell* — the shell is the
control plane. Building the wall first would mean building it twice (once
standalone, once re-homed). So: **the shell + control plane come first; the
workbench mounts into it.** This also delivers standalone value immediately — a
proper wide-screen fleet cockpit the phone can't be.

The spine stays **direct → observe → decide → record** (the PI-not-coder frame):
the director watches the fleet, directs agents, decides approvals, and the record
accretes. Desktop's leverage over mobile is **simultaneity, density, keyboard,
bulk operations, persistent context** — precisely the affordances the phone's
`IndexedStack` tab model forbids.

## 2. What exists to build against (grounded)

**Hub API** — one chi router (`hub/internal/server/server.go:351`), team-scoped
REST under `/v1/teams/{team}/…`, bearer auth (`auth/token.go:124`) + `teamGate`
(`team_gate.go:32`, 403 on scope≠path). The human client consumes REST + SSE and
drives governance through `GET /attention` + `POST /attention/{id}/decide`
(`handlers_attention.go:376`) — it does **not** use `/mcp/{token}` (that is the
agent door).

**Streaming is SSE only** — `GET …/agents/{id}/stream` (`server.go:490`) and
`GET …/channels/{ch}/stream` (`server.go:570`), `data:` JSON frames + 5 s
`: ping` keepalives, `?since=` backfill (agents by integer `seq`, channels by
`received_ts`). **Critical constraint:** these require an `Authorization` header,
which browser-native `EventSource` cannot set — the Flutter app already works
around this with a raw header-capable `HttpClient` (`lib/services/hub/events_api.dart:60-99`).
The desktop client resolves it in the Tauri layer (§3).

**Live vs polled** — on mobile only `LiveFeed` and channel streams are SSE; the
Activity feed, Attention inbox, Sessions list, and Insights are **REST + cache +
manual refresh** (`activity_feed.dart:64-102`). A multi-pane desktop showing
several at once must make them all feel live (§4, WS2).

**Design tokens are Dart-locked** — `lib/theme/tokens.dart` (spacing/radius/type)
+ `lib/theme/design_colors.dart` (~60 `const Color`), no JSON intermediate,
enforced by the `lint-design-tokens.sh` ratchet (baseline 100). Sharing with a web
client needs a neutral source (§3, WS1). Vocabulary (`VocabPack`, 4 presets ×
{en,zh}, 21 axes) is a **separate string-only subsystem** — a parallel i18n
concern, not part of the visual-token problem.

## 3. Tech stack (proposed, with reasoning)

| Layer | Choice | Why |
|---|---|---|
| **Shell** | **Tauri v2** (Rust core + OS webview) | ADR-050 A-1; small binaries; Win/Mac/Linux + same build runs in a plain browser; Rust side solves the SSE-auth-header + keychain problems (below) |
| **Framework** | **React + TypeScript** | The unified client must host the workbench's **React-first embeds** (Monaco, CodeMirror6, **tldraw** React-only, **BlockNote** React-only, `@rerun-io/web-viewer-react`, **Viser** React+r3f, Plotly, Embedding Atlas). Pick the substrate the components we embed already speak; Svelte/Solid would force per-embed wrappers/iframes. Largest ecosystem + talent pool. Cost (more boilerplate than Svelte) accepted. |
| **Server-state** | **TanStack Query** | Direct map of the mobile "REST + cache + `staleSince`" pattern (`HubSnapshotCache`) to a mature web equivalent — staleTime, background refetch, invalidation |
| **UI state** | **Zustand** | Ephemeral pane layout, selection, palette state — light, unopinionated |
| **Live bus** | one SSE-subscription manager | Mirrors `EventsApi`; fans agent/channel streams into Query cache + a live-feed store; subscribe-before-backfill (`?since=`) carried over |
| **Hub SDK** | typed TS facade-over-subclients | Mirror `hub_client.dart`'s shape (`system/events/attention/hosts/agents/sessions/projects/runs/…`); one `HubTransport` (bearer inject, `/v1/_info` probe, teamGate 403); structural TS interfaces (loose, like the Flutter `Map<String,dynamic>`, typed where stable) |
| **Styling** | token-driven CSS vars + headless primitives (Radix/Ark) | Consume the shared tokens (§ WS1); avoid an opinionated kit (MUI/Chakra) that fights the tokens |
| **Tables/virtualization** | TanStack Table + Virtual | Fleet lists, audit console, long transcripts |
| **Routing** | addressable pane-state (TanStack Router or React Router) | Multi-pane, not URL-per-screen: the URL encodes *which entity is selected in each pane* so a layout is shareable/restorable |

**The Tauri Rust core earns its place** by solving three things a plain browser
build can't do cleanly: (1) **SSE with an auth header** — the Rust side holds the
bearer and proxies REST+SSE to the webview, so the frontend never fights
`EventSource`'s header limitation; (2) **token at rest** in the OS keychain
(parallel to `flutter_secure_storage`); (3) native menus/notifications, and a
later breakglass PTY path if wanted. The **plain-browser build** substitutes a
fetch-based SSE reader and in-memory/session token — same frontend, swapped
transport.

**Shared design-token pipeline (the load-bearing prerequisite, WS1).** Introduce a
neutral **`tokens.json` in DTCG format** as the new single source of truth; a
**Style Dictionary** build emits **both** `lib/theme/tokens.dart` +
`design_colors.dart` (Flutter — now *generated*, byte-verified against today's
values so nothing changes visually and the ratchet stays green) **and**
`tokens.css` (CSS custom properties for the web client). Feed the linter's
duplicated value-sets (`lint-design-tokens.sh:52-54`) from the same JSON, killing
the third copy. This makes ADR-050's "shared tokens are load-bearing" mechanical
instead of aspirational. Because it *reverses* the hand-authored direction of
`tokens.dart`, it is an explicit decision → recorded in ADR-051 (WS0).

## 4. UI design — multi-pane mission control

The frame is three regions plus persistent chrome. Every region exploits an
affordance the phone's mutually-exclusive tabs (`home_screen.dart:53`) deny:

```
┌──────────────────────────────────────────────────────────────────────┐
│ Titlebar:  team ▾   ·   fleet health  ●3 ▶12 ⚠2   ·   $budget   ·  ⌘K │  global status
├────────────┬─────────────────────────────────────┬────────────────────┤
│ NAVIGATOR  │            FOCUS  (center)          │  ATTENTION DOCK +   │
│  (left)    │   the selected work surface —       │  INSPECTOR (right)  │
│            │   SPLITTABLE / TABBABLE             │                     │
│ Hosts ▸    │                                     │  Approvals queue    │
│  ▸ agents  │  ┌───────────────┬───────────────┐  │  (ALWAYS visible):  │
│  ▸ sessions│  │ live transcript│ its project    │  │  propose→approve    │
│            │  │ (LiveFeed/SSE) │ board          │  │  cards · stalled    │
│ Projects ▸ │  │                │                │  │  digest · help reqs │
│  ▸ tasks   │  └───────────────┴───────────────┘  │  ───────────────    │
│  ▸ runs    │   two agents side by side, OR a     │  Inspector for the  │
│            │   transcript beside the board that  │  current selection  │
│ [filter]   │   spawned it, OR the audit console  │  (config · digest)  │
├────────────┴─────────────────────────────────────┴────────────────────┤
│ Statusbar:  12 running · 2 blocked · 2 need you · hosts 4/4 · sync ✓   │  persistent
└──────────────────────────────────────────────────────────────────────┘
```

**Left — Navigator.** A persistent, filterable **fleet + projects tree**
(Hosts ▸ agents ▸ sessions; Projects ▸ tasks/runs) — the always-there context the
phone lacks (it flips tabs to change scope). Collapsible; doubles as global nav.

**Center — Focus, splittable.** The killer capability. The IA scan enumerated the
surfaces that are **one-at-a-time on mobile but naturally simultaneous on desktop**:
SessionChatScreen's 4-way `View ▾` (Feed/Pane/Journal/Insights,
`sessions_screen.dart:2754-2788`); N live sessions (mobile retargets *one*
SessionChatScreen via a rail, `:2793-2799`); an approval card ↔ the transcript turn
that raised it (`live_feed.dart:70-76`); a project's 5 pills
(`project_detail_screen.dart:51-58`). All of these become **split/tabbed panes**
here.

**Right — Attention Dock + Inspector.** The **approvals queue is always visible**
(mobile buries it in the Me tab, `me_screen.dart:40-62`) — governance is the moat,
so it is never more than a glance away. Reuse the mobile **per-kind card taxonomy**
(`ProposeCardRouter`: deliverable.set_state / phase.advance / task.set_status /
agent.spawn / template.install / project.create → generic fallback) as web
components. Below it, a contextual **inspector** for the current selection (agent
config, session digest, task detail).

**Cross-cutting affordances (all net-new vs mobile):**
- **Command palette (⌘K)** — the keyboard spine: jump to any host/agent/project/
  session, spawn/dispatch an agent, run a governed action, decide an approval,
  switch team. *Direct* made instant.
- **Multi-select + bulk actions** on the fleet (pause/stop/archive N agents) —
  generalizes the mobile sessions-list batch archive/delete
  (`sessions_screen.dart:59-116`).
- **Persistent status bar** — fleet health counters, aggregate cost/budget, host
  connectivity → an ambient monitor.
- **Live everywhere.** Because several polled-on-mobile surfaces now share a
  screen, the client runs a **live-event bus**: agent/channel SSE for transcripts;
  for the REST-only surfaces (attention, audit, fleet) a short TanStack-Query
  refetch cadence, **upgradable** to a hub-side team-firehose SSE later (see Open
  questions — the hub streams per-agent/per-channel today, not team-wide).

## 5. Surfaces in scope (control plane) vs deferred (workbench)

**In scope:** Fleet (hosts+agents lifecycle) · Session/Transcript reader
(LiveFeed + digest panes) · Attention/Approvals dock · Projects/Tasks/Runs board ·
Activity/Audit console · Command-palette dispatch · Team/Governance + operator
Admin cockpits · thin device Settings.

> **Shell parity with mobile (done 2026-07-05):** device **Settings** overlay,
> **light/dark/system themes** (a semantic CSS layer over the shared light+dark
> tokens — WS1 already carried both), and **en/zh i18n** (`desktop/src/i18n/`, the
> web analogue of gen-l10n; vocabulary presets can layer later). All UI strings
> route through it; both persist to `localStorage`.

**Deferred to the workbench (mount into this shell later, ADR-050):** the multi-run
comparison wall + robotics video-grid, the reader/author pair, the ideation canvas,
the robotics viewers (three.js/urdf-loader/Viser/Rerun). This plan builds the shell
they need.

## 6. Workstreams

**WS0 — ADR-051 + roadmap (docs).** Record: React+TS+Tauri v2 as the unified-client
stack (the embed-substrate argument); the DTCG shared-token pipeline and its
reversal of hand-authored `tokens.dart`; the pane-based IA that reinterprets (not
replicates) the mobile tabs. Update `decisions/README.md`, `roadmap.md` (a
"Desktop" Now/Next row), glossary ("unified client", "control-plane shell",
"attention dock").

**WS1 — Shared design-token pipeline (load-bearing prerequisite). ✅ DONE
(2026-07-05).** `design-tokens/tokens.json` (DTCG) is the neutral SoT;
`design-tokens/build.mjs` emits `build/tokens.css` (the web/desktop artifact) and
verifies the Flutter mirror against the JSON. Wired into CI ("Verify shared design
tokens (DTCG)" step). Gate met: **Flutter build byte-unchanged, zero visual diff,
one CSS artifact produced.** Two forks resolved during build:
- **Open Question 1 (SoT direction) → keep Dart hand-authored + verify.** Rather
  than regenerate the richly-documented `tokens.dart`/`design_colors.dart` from
  JSON (brittle to reproduce byte-identically, and unverifiable without the
  Flutter SDK locally), `build.mjs --check` asserts every Dart constant matches
  `tokens.json` **bidirectionally** — the JSON is authoritative-by-enforcement,
  the Dart keeps its ADR-047 guidance docs, and the gate needs no Flutter to run.
- **Emitter → zero-dependency Node, not Style Dictionary.** A ~60-value set
  doesn't justify a `node_modules`/lockfile/`npm ci` + supply-chain surface in
  CI; the input is standard DTCG so Style Dictionary remains a drop-in later. (A
  lighter deviation from ADR-051's letter; its intent — one shared DTCG pipeline
  — is preserved. Recorded here + in `design-tokens/README.md`.)

**WS2 — App shell + hub SDK + streaming pipe. 🚧 IN PROGRESS (foundation landed
2026-07-05).** Scaffolded under `desktop/`: Vite + React + TS; the typed hub SDK
(`src/hub/` — transport with bearer + `/v1/_info` probe + teamGate-403 mapping,
`client.ts` facade, `sse.ts` **fetch-based** SSE reader that sets the auth header
directly, so no `EventSource` limitation and the same code path serves browser +
Tauri); the three-region `AppShell` (Navigator | Focus | Attention dock) + status
chrome; the ⌘K command-palette shell; the shared-token CSS (WS1) driving the theme;
and **one read-only surface** — the audit console over REST + TanStack Query (5 s
refetch). The **Tauri v2 Rust core** (`src-tauri/`) is a minimal shell + a
`hub_request` REST proxy (token-out-of-webview path). **Verified:** frontend
typechecks + production-builds locally; the Rust core compiles via a new CI job
(`.github/workflows/desktop.yml`) since the dev host has no cargo. **Remaining WS2
exit:** point it at a real hub for the live end-to-end run (needs the director's
hub). Rust keychain token storage + SSE proxy deferred to WS8; browser build is the
default target meanwhile.

**WS3 — Fleet mission-control. 🚧 FIRST SLICE DONE (2026-07-05).** Navigator tree
(hosts ▸ agents, grouped, status dots), persistent status bar (running/paused/
need-you/hosts, polled), single-agent lifecycle via REST (pause/resume/stop/
terminate/archive — `agent_actions_menu.dart` semantics; DELETE = archive). Live via
polled fleet refetch (5 s). **Remaining:** sessions branch, multi-select + bulk ops,
respawn.

**WS4 — Session/Transcript reader. 🚧 FIRST SLICE DONE (2026-07-05).** LiveFeed over
`streamAgentEvents` (fetch-SSE, `tail` backfill + `seq` cursor + reconnect), a text
composer (`POST …/input`, flat `{kind:'text',body}`), and a sibling **digest** tab
(`GET …/digest`, ADR-038). **Remaining:** split-pane N transcripts, typed per-kind
event rendering (currently best-effort text extraction), `/turns` filter, Insights.

**WS5 — Attention/Approvals dock. 🚧 FIRST SLICE DONE (2026-07-05).** The
always-visible right dock now renders open attention items as **per-kind cards**
(permission_prompt → tool + input; propose → `change_kind` + spec/target preview +
principal **override**; help_request → reply composer; generic → approve/reject),
driving `POST /attention/{id}/decide` (approve | reject | override, ADR-030 W9;
help-reply via `body`) with a 6 s refetch shared with the status-bar counter. The
governance moat is surfaced across all three regions now. **Remaining:** stalled-card
variant (escalation_state), deliverable ratify/unratify + criteria mark-met/fail/
waive, option (`select`/`elicit`) buttons, originating-context jump.

**WS6 — Projects/Tasks/Runs board + Activity console. 🚧 FIRST SLICE DONE
(2026-07-05).** The Navigator gained a **Projects** section (`GET /projects`);
selecting a project opens a **tasks kanban** in the Focus region (ADR-029 statuses
todo/in_progress/blocked/done/cancelled via `GET /projects/{id}/tasks`, 8 s refetch).
Focus selection is now a discriminated union (agent | project | none). The activity
console already ships (WS2). **Remaining:** overview/runs/plans/deliverables panes,
task detail + status change, streaming the audit console.

**WS7 — Team/Governance + Admin cockpits. 🚧 FIRST SLICE DONE (2026-07-05).** An
**Admin & Governance** overlay (titlebar button + ⌘K) with tabs: **Team** (members
`GET /principals` + policy `GET /policy`), **Hosts** (`GET /v1/admin/hosts` +
ping/restart/update/shutdown, each a two-click `ConfirmButton`), **Agents**
(`GET /v1/admin/agents` + confirmed kill). Admin endpoints 403 gracefully for
non-operator tokens. **Remaining:** teams/upkeep tabs (token rotate, DB vacuum),
policy editing (`PUT /policy`), channels.

**WS8 — Packaging + continuum + breakglass terminal. 🚧 PACKAGING DONE (2026-07-05).**
Tauri installers for all three desktop OSes are produced by
`.github/workflows/desktop-release.yml` (matrix Linux/macOS-universal/Windows via
`tauri-action`; run on demand or by a `desktop-v*` tag → draft release; installers
also uploaded as run artifacts) — CI is the bundle factory since the dev host has no
Rust. **Remaining WS8:** keychain token storage; auto-update; phone↔desktop deep-link
handoff. **Breakglass SSH terminal** mirroring the mobile Connections/Keys/Terminal
surfaces — **xterm.js** + a Tauri Rust **`russh`** transport (not `libghostty`);
host rows gain an "open terminal" action. Backend + the shared-key / hub-safety model
(managed-host PTY relay vs personal direct-SSH; **zero-knowledge key vault**) are
decided in [ADR-052](../decisions/052-breakglass-ssh-and-key-vault.md) (amends
forbidden-pattern #15); the hub-side PTY-relay + relay-auth piece is a separate hub
workstream.

**Sequencing:** WS0 → WS1 → WS2 → (WS3 ‖ WS4) → WS5 → WS6 → WS7 → WS8. WS1 before
any UI (parity risk); WS2 is the spine everything hangs on; WS3/WS4 are the
observe core and can parallelize; WS5 is the decide moat.

## 7. Open questions (forks to confirm — not blockers)

1. ~~**Token-pipeline direction**~~ — **RESOLVED (WS1, 2026-07-05):** keep Dart
   hand-authored and **verify** it against `tokens.json` bidirectionally
   (`design-tokens/build.mjs --check`), emitting `tokens.css` for web. Chosen over
   generating Dart because byte-identical regeneration of the doc-carrying Dart is
   brittle and unverifiable without the Flutter SDK; verification gives the same
   "JSON is authoritative" guarantee with zero Flutter-build churn.
2. **Fleet liveness** — poll the REST-only surfaces now (recommended) vs. add a
   **hub-side team-firehose SSE** (a small Go addition — the hub streams per-agent/
   per-channel today, not team-wide). Poll now, add firehose if the polling cadence
   feels stale.
3. **SSE transport in the desktop build** — Rust proxy (recommended, clean header +
   keychain) vs. frontend fetch-SSE everywhere (simpler, one code path).
4. **Router + headless kit** — TanStack vs React Router; Radix vs Ark. Low-stakes,
   defer to WS2.
5. **Vocabulary** — share the `VocabPack` terms as JSON to the web i18n layer now,
   or defer until the presets ship on mobile. Lean defer.

## Closes / advances

Advances **ADR-050** (realizes the unified web-tech client);
**[ADR-051](../decisions/051-desktop-client-stack.md)** (WS0) records the stack. Establishes the shell the workbench first surface (the multi-run comparison
wall) mounts into. No hub Go changes except the *optional* WS-7-adjacent
team-firehose SSE (Open question 2), which would be its own small PR.

## Related

- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — delivery model
  (local-first web-tech, unified client, Tauri).
- [`desktop-research-surface.md`](../discussions/desktop-research-surface.md) — the
  two-halves split this builds the first half of.
- [`research-tooling-landscape.md`](../discussions/research-tooling-landscape.md) —
  the workbench components that later mount into this shell.
- [ADR-047](../decisions/047-design-system-enforcement.md) — the token system the
  shared pipeline (WS1) extends to web.

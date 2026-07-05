# 051. Desktop client stack — Tauri + React + shared design tokens

> **Type:** decision
> **Status:** Proposed (2026-07-05) — implements [ADR-050](050-desktop-workbench-delivery-model.md)
> A-1/A-2 (cross-platform Tauri shell + unified web-tech client). Fixes the two
> stack forks ADR-050 left "TBD": the **UI framework** and the **shared-token
> pipeline** that makes cross-client visual parity load-bearing. Planned in
> [`plans/desktop-control-plane.md`](../plans/desktop-control-plane.md).
> **Audience:** contributors · maintainers
> **Last verified vs code:** v1.0.820

**TL;DR.** The desktop client is **Tauri v2** (Rust core + OS webview → Win/Mac/Linux
installers + the same build in a plain browser) hosting a **React + TypeScript**
single-page app. React is chosen not for the control plane but for the *workbench*
half: the unified client must eventually mount the research components ADR-050
selected to EMBED (Monaco, CodeMirror 6, tldraw, BlockNote, `@rerun-io/web-viewer-react`,
Viser, Plotly), and those are React-first — the framework should be the one the
components we embed already speak. Server-state rides **TanStack Query** over the
hub's REST+cache pattern; the **Tauri Rust core proxies SSE** (the hub's event
streams need an `Authorization` header that browser `EventSource` cannot set) and
holds the bearer token in the OS keychain. The load-bearing piece is a **shared
design-token pipeline**: a neutral DTCG `tokens.json` → Style Dictionary → **both**
the Flutter Dart token classes **and** web CSS custom properties, so ADR-047's
"single source of truth" spans both clients instead of drifting.

## Context

ADR-050 decided the desktop surface is a local-first, hub-served **web-tech app**,
and its amendment (A-1/A-2) fixed the shell as **Tauri** and the client as **unified**
(control plane rebuilt in web-tech, not embedded from Flutter). Two stack decisions
were explicitly deferred as "framework TBD": *which* web framework, and *how* the
shared design system is expressed across two clients. This ADR settles both, because
[`plans/desktop-control-plane.md`](../plans/desktop-control-plane.md) — the first
build (the control-plane shell) — cannot start WS1/WS2 without them.

Three grounded facts from the codebase scans (cited in the plan) constrain the
choice:

1. **The unified client must host React-first embeds.** ADR-050's EMBED list and
   the robotics survey ([`embodied-ai-tooling-landscape.md`](../discussions/embodied-ai-tooling-landscape.md)
   §3.5) are dominated by components whose only first-class binding is React:
   **tldraw** and **BlockNote** are React-only; Monaco/CodeMirror/Plotly/Rerun/Viser
   ship React wrappers. Choosing Svelte/Solid would force a wrapper or an iframe
   boundary per embed.
2. **The hub pushes over SSE that needs an auth header.** Live updates are SSE only
   (`hub/internal/server/server.go:490`, `:570`), `data:` JSON frames + `?since=`
   backfill, and the stream GET carries `Authorization: Bearer` — which browser-native
   `EventSource` cannot set (the Flutter client already works around this with a raw
   header-capable `HttpClient`, `lib/services/hub/events_api.dart:60-99`).
3. **The design tokens are Dart-locked.** ADR-047's tokens live only as Dart `const`
   classes (`lib/theme/tokens.dart`, `lib/theme/design_colors.dart`) — no neutral
   JSON — enforced by the `lint-design-tokens.sh` ratchet. ADR-050 A-2 made
   cross-client parity **load-bearing**, so a web client cannot simply re-type the
   values by hand.

## Decision

- **D-1 — Shell: Tauri v2.** The client is packaged with **Tauri v2** (Rust core +
  the OS-native webview) into Windows / macOS / Linux installers; the identical web
  build also runs in a plain browser against a remote hub (the no-install path).
  The Rust core is not incidental — it earns its place by (a) **proxying REST + SSE**
  so the frontend never fights `EventSource`'s missing-header limitation, (b)
  holding the bearer token in the **OS keychain** (parallel to Flutter's
  `flutter_secure_storage`), and (c) native menus/notifications and a later
  breakglass PTY path. Electron is the fallback only on a concrete Tauri capability
  gap.

- **D-2 — Framework: React + TypeScript.** The SPA is **React + TypeScript**, chosen
  by the *embed* axis (Context §1): the unified client must host the workbench's
  React-first components, so React is the substrate they already speak. Secondary:
  the largest component ecosystem and talent pool. The accepted cost is more
  boilerplate than Svelte/Solid — judged worth it against per-embed wrapper churn.

- **D-3 — Data layer: TanStack Query + a single SSE manager.** Server state uses
  **TanStack Query** (staleTime / background refetch / invalidation) — a direct map
  of the mobile "REST + cache + `staleSince`" pattern (`HubSnapshotCache`). One
  **SSE-subscription manager** (mirroring `EventsApi`) fans agent/channel streams
  into the Query cache + a live-feed store, preserving the subscribe-before-backfill
  `?since=` discipline. Ephemeral UI state (pane layout, selection, palette) uses a
  light store (Zustand). The hub SDK is a **typed TS facade over per-domain
  sub-clients** mirroring `hub_client.dart`'s shape, with one `HubTransport` (bearer
  inject, `/v1/_info` probe, `teamGate` 403 handling); entities are structural TS
  interfaces (loose like the Flutter `Map<String,dynamic>`, typed where stable).

- **D-4 — Shared design tokens via a DTCG pipeline (the load-bearing piece).** A
  neutral **`tokens.json` in DTCG format** becomes the single source of truth; a
  **Style Dictionary** build emits **both** `lib/theme/tokens.dart` +
  `design_colors.dart` (Flutter — now *generated*, byte-verified against today's
  values so nothing changes visually and the `lint-design-tokens.sh` ratchet stays
  green) **and** `tokens.css` (CSS custom properties for the web client). The
  linter's duplicated value-sets (`lint-design-tokens.sh:52-54`) are fed from the
  same JSON, removing the third copy. This realizes ADR-047's "single source of
  truth" across two clients (ADR-050 A-2) mechanically rather than by convention.
  Vocabulary (`VocabPack`, ADR-048) is a separate string-only subsystem and is *not*
  part of this pipeline — the web client consumes the same presets through a parallel
  i18n layer.

- **D-5 — The human client uses REST + SSE, never `/mcp/{token}`.** The desktop app
  is a *human* client: it reads/writes over REST, subscribes over SSE, and drives
  governance through `GET /attention` + `POST /attention/{id}/decide`
  (`hub/internal/server/handlers_attention.go:376`). The `/mcp/{token}` endpoint is
  the *agent* door and is out of scope for this client. No hub changes are required
  beyond the *optional*, separately-decided team-firehose SSE (an open question in
  the plan, not decided here).

## Consequences

**Easier / unlocked:**
- The workbench components ADR-050 chose to EMBED drop in without wrapper or iframe
  friction, because the runtime speaks their native binding.
- Cross-client visual parity becomes a build artifact, not a discipline: one edit to
  `tokens.json` propagates to Flutter and web together, and CI can diff both outputs.
- SSE-with-auth and secret-at-rest are solved once in the Rust core, cleanly, for the
  installed builds; the browser build degrades to a fetch-based SSE reader.

**Harder / cost:**
- A **second frontend toolchain** (Node/Vite/Tauri) and CI lane beside Flutter.
- The token pipeline **reverses the authoring direction** of `tokens.dart` — today
  hand-authored, henceforth generated. A one-time migration must prove byte-parity
  and keep the ratchet green (plan WS1); until then the Dart files stay hand-authored.
- React's boilerplate/perf discipline (memoization, render control under
  high-frequency SSE) is on us, versus Svelte/Solid's finer-grained defaults.

**Unaffected:**
- The Flutter mobile client, its IA, and `hub-tui/` are untouched; the hub API is the
  meeting point.
- ADR-048 vocabulary presets are orthogonal (string layer, not visual tokens).

## Alternatives considered

- **Svelte / SolidJS.** Leaner runtime and finer reactivity (attractive for SSE-heavy
  panes), but the decisive embed axis (Context §1) makes them net-negative: tldraw and
  BlockNote are React-only, and every other target ships React-first — each embed would
  need a wrapper or iframe. Rejected on the workbench half, which is the reason for
  web-tech at all.
- **Flutter-web / one adaptive Flutter tree.** Already rejected by ADR-050 (inherits
  Flutter's editor/embedding weaknesses without the web-component reuse).
- **Electron shell.** Heavier footprint and memory than Tauri for the same web app;
  kept only as a fallback on a concrete Tauri gap (D-1).
- **Hand-maintained parallel tokens (no pipeline).** Two hand-authored token sources
  is exactly the drift ADR-047 was created to end and ADR-050 A-2 made load-bearing;
  rejected.
- **Parse Dart → emit CSS (keep Dart as source).** Viable (the Dart is literal-only)
  and lower-churn, but keeps Dart as the privileged source and leaves the linter's
  third copy unsynced; kept as the fallback if the DTCG migration proves costly (plan
  Open question 1).

## References

- Plan: [`plans/desktop-control-plane.md`](../plans/desktop-control-plane.md) — the
  WS0–WS8 build (this ADR is WS0); WS1 = the token pipeline, WS2 = shell + SDK.
- Implements: [ADR-050](050-desktop-workbench-delivery-model.md) A-1/A-2
  (Tauri shell + unified web-tech client).
- Extends: [ADR-047](047-design-system-enforcement.md) (named tokens as SoT — this
  pipeline carries them to a second client); orthogonal to
  [ADR-048](048-themed-vocabulary-overlay.md) (string presets).
- Component register: [`discussions/research-tooling-landscape.md`](../discussions/research-tooling-landscape.md)
  and [`discussions/embodied-ai-tooling-landscape.md`](../discussions/embodied-ai-tooling-landscape.md)
  §3.5 (the React-first embeds that drive D-2).

# Desktop Companion vision parity — kimi-web's bar on our data scheme

> **Type:** plan
> **Status:** In flight (2026-08-02) — W1 landing in wedge order §4;
> principal review done
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** 2026.730.1231-alpha (`cea267fa`) — every
> anchor below re-verified against that tip by the authoring audit
> **Freshness:** contract

**TL;DR.** Make the Companion dock the vision-first, kimi-web-grade
surface for **claude-code and codex** (and any future family), driven by
the typed `agent_events` **vocabulary** — produced either by a
desktop-local driver (no hub required) or by the hub (remote/fleet);
the hub is an option, never a prerequisite. The investigation record is
[companion-vision-and-kimi-web-bar.md](../discussions/companion-vision-and-kimi-web-bar.md)
(topology answer in its §9 addendum); this plan converts it into six
lanes: **F** (free the Companion from its kimi coupling), **L**
(local driving layer, hub-optional), **E** (close specific hub
event-vocabulary gaps for claude/codex), **R** (renderer + composer
parity against the kimi-web feature table), **D** (design-system
enforcement, the kimi-web mechanism), **T** (ACP M1 transport rows).
Lanes are independently shippable; F1 + L1 + E1 + R1 are the
highest-leverage wedges.

---

## 1. The bar, concretely

"kimi-web's bar" decomposes into five properties (discussion §5):

1. Paste/drag/annotate an image and it lands with the agent natively.
2. A transcript where tool traffic reads as quiet 1-line rows, groups
   fold, and **only actionable things (questions, approvals) get full
   cards** — in one fixed position.
3. Live telemetry: context-window fill, cost, turn durations,
   streaming output while a command runs.
4. A composer that carries session controls (model, permission mode,
   slash commands) instead of hiding them in an info tab.
5. A token-disciplined visual layer where off-system UI is
   *unmergeable*, and motion is tokenized and restrained.

Principle (discussion §8/§9): **the UI contract is the typed
`agent_events` vocabulary, not the hub process** — a superset of ACP
on the axes that matter (usage `by_model`, subagents, compaction,
attention items). The vocabulary has two producers: a desktop-local
driver (lane L; the Companion works with no hub at all, the way
kimi-web's SPA needs only the engine's own daemon) and the hub (the
option for remote hosts, fleet, durable cross-device state). ACP is a
transport rung (lane T), never the renderer's ceiling.

## 2. Decisions

- **D-1. One Companion, N engines, per-engine surface resolution.**
  The Companion is the umbrella surface; per engine it resolves to the
  richest available pairing of {UI} × {service}: the vendor's UI over
  the vendor's service where the vendor ships both (kimi: kap-server +
  SPA — **the kimi-web tab is this policy's first instance, not a
  sibling**), TermiPod's renderer over the vendor's service where only
  the service exists (codex app-server), TermiPod's renderer over the
  TermiPod-built local service where neither exists (claude).
  TermiPod's renderer binds any agent id
  (`desktop/src/ui/AgentCompanion.tsx`) regardless of which service
  feeds it.
- **D-2. Additive vocabulary only.** Every lane-E change is a new kind
  or new payload field; no renames. New/promoted kinds must be swept
  through **both clients'** busy walks and fold allowlists before
  landing — the `session.init` busy-parity incident is the governing
  precedent (a "state" kind reused as a data carrier inherits all its
  state semantics).
- **D-3. Full-card discipline.** In the Companion feed, exactly two
  surfaces render as full interactive cards: questions and approvals
  (R1). Everything else is rows/groups/footers.
- **D-4. Degrade honestly.** Features whose data an engine can't supply
  (e.g. live tool output on claude M2, cost on codex) degrade to
  absence — no synthesized placeholders. The imputed-cost path
  (`hub/internal/pricing/compute.go`) is the one sanctioned
  approximation, labeled as imputed wherever shown.
- **D-5. Scope guard.** Goal strips, cron cards, side chats,
  undo/fork/export verbs, git headers, server-side prompt queue/steer,
  and kimi swarm-phase parity are **out** (§5).
- **D-6. Prioritized driving mode = M2** for claude-code and codex
  (`fallback_modes: [M4]`) — i.e. the existing steward defaults stand;
  kimi = M1 (its only structured mode). Rationale: the Companion is
  local-first, and the engines' *native* structured wires are the
  richest feeds we have — codex app-server is the only stream with
  live text + `context_window` + real cancel, and claude stream-json
  is the only claude mode with native image input and authoritative
  `cost_usd`/`by_model`. M4 is structurally wrong as primary
  (`prompt_image` ✗ in every M4 flavour, input via send-keys, TUI owns
  the transcript) — it stays the fallback/attach-to-terminal rung. M1
  via the external adapters would interpose a lossy layer over wires
  we already drive natively (`codex-acp` wraps the same app-server);
  lane T adds M1 as an available rung for convergence and future
  engines, never as the Companion's priority. The Companion itself
  hardcodes no mode — it binds an agent and renders what the resolved
  rung yields; lane E closes M2's gaps precisely because M2 is the
  rung that matters.
- **D-7. Hub-optional.** The Companion binds an event *source* behind
  one interface — `local` (Electron-main driver speaking the engine's
  native M2 protocol; no hub process anywhere) or `hub` (today's SDK
  path). Same vocabulary, same renderer, same input kinds. Drift
  between the two producers is prevented structurally: frame-profile
  translation is declarative YAML (ADR-010), so the local driver
  interprets the *same* `agent_families.yaml` rules and is validated
  against the *same* corpus fixtures as the Go interpreter. Hub-only
  capabilities (fleet, teleport, remote hosts, cross-device
  persistence, a2a) simply don't appear on local sources — degrade
  honestly per D-4.
- **D-8. Service-first topology.** Sessions live in a **service**, not
  in a UI-owned pipe: prefer the daemon + WebSocket shape (discussion
  §9) so a session outlives any client, survives UI restarts, and
  supports reattach and multi-view (the vendor TUI and the Companion
  sharing one session). stdio spawn-per-session remains the fallback
  where no service exists or the daemon is unavailable — never the
  preference. Combined with D-1 this is the flexibility invariant: a
  vendor shipping a new service or UI changes one row in the
  resolution table, not the architecture.

## 3. Lanes and wedges

### Lane F — free the Companion (desktop; asymmetries #1/#2/#5/#6)

- **F1 — dock decoupling.** `AssistantDock.tsx` currently returns null
  unless the kimi web panel exists *and* its server started, holding
  the Companion hostage. Split: Companion tab always available when the
  hub is connected; kimi tab (and its start/stop lifecycle) only when
  the kimi family is installed. Keep both-mounted/CSS-hidden semantics.
- **F2 — annotation target registry.** Replace
  `annotationTargets.ts`'s `{kimi: boolean, companion}` with an ordered
  target list (kimi-web injection target = one row, each bound
  Companion mount = one row); `GLOBAL_ORIGIN` picks the first bound
  target. Preserves D2.2 ordering (kimi first when its panel is open).
- **F3 — capability-gated composer.** Desktop `Composer.tsx` offers
  image/pdf/audio/video attach unconditionally; mobile consults the
  family registry's `prompt_image`/`prompt_*` flags
  (`lib/widgets/image_attach/composer_image_attach.dart`). Port the
  gate (flags ride `session.init` / family lookup); disabled kinds
  show the materialize-fallback hint instead of silently uploading.
- **F4 — user-level MCP reseed for claude/codex.** Generalize
  `desktop/electron/src/kimimcp.ts` (kimi-only today) so the
  UI-sharing toggle also deep-merges/removes the additive
  `termipod-desktop` stdio-relay entry in `~/.claude.json`
  (`mcpServers`) and `~/.codex/config.toml` (`[mcp_servers]`), with the
  same discipline: additive-only, atomic tmp+rename, corrupt file left
  untouched, no env in the entry. Ad-hoc (non-hub-spawned) claude/codex
  sessions then get vision pull like kimi does.

### Lane L — local driving layer (desktop; hub optional, D-7)

- **L1 — event-source abstraction.** Extract the Companion's data
  dependency (`listAgentEvents` / `streamAgent` / `postAgentInput` /
  attention resolve) into an `AgentEventSource` interface with the hub
  SDK as the first implementation. Renderer, folds, and Composer are
  untouched — they already consume typed events. The dock's agent
  picker lists sources: local sessions + hub agents when a hub is
  configured.
- **L2 — TS frame-profile interpreter.** Interpret
  `agent_families.yaml` `frame_profile` rules in Electron main,
  validated against the **same corpus fixtures** the Go interpreter
  uses (`docs/reference/frame-profiles.md`) — parity by construction,
  not by discipline. Driver-side imperative supplements that the YAML
  can't express (delta throttle, approval parking, `session.init`
  composition) are enumerated per engine in L3/L4 and covered by
  ported unit tests.
- **L3 — the TermiPod local agent service + claude driver (D-8:
  "we build one" where the vendor ships none).** Electron main hosts a
  long-lived local agent service: it owns engine children, keeps a
  per-session append-only event log with snapshot + cursor semantics
  (the kap-server pattern, discussion §9), and serves clients over
  renderer IPC plus an optional loopback WebSocket with a bearer token
  — so sessions outlive the renderer, and other clients can attach
  later without an architecture change. Claude children inside the
  service speak M2 stream-json (D-6): image input lowered to Anthropic
  content blocks, workdir `.mcp.json` seeding reusing the F4
  machinery, cancel via process signal. Across full app restarts the
  service rebinds via engine-native resume + log replay. Emits the
  lane-E corrected shapes from day one (`thought`, `context_window` on
  `usage`, normalized `by_model`).
- **L4 — codex via the vendor's service (D-8: use theirs when it
  exists).** Prefer **WebSocket attach** to a `codex app-server`
  daemon — spawn it detached if absent, authenticate with its bearer
  scheme — so the session survives Companion and app restarts and can
  be shared with the vendor TUI; stdio spawn-per-session is the
  fallback rung only. Text-delta throttle port, parked
  approvals/elicitations surface directly as R1 cards (no attention
  table locally), `turn/interrupt` cancel, `.codex/config.toml`
  seeding.

### Lane E — event-vocabulary gaps (hub; verified per-driver)

Local drivers (L3/L4) are new code and must emit the corrected shapes
below natively; the wedges here fix the **hub** producers so both
sources stay byte-compatible.

Audit ground truth: claude M2 = `driver_stdio.go`, codex M2 =
`driver_appserver.go`, claude M4 = `drivers/local_log_tail/claude_code/`.

- **E1 — claude M2 thinking + usage completeness.**
  (a) The assistant content-block walk handles only `text` and
  `tool_use`; a `thinking` block falls to `kind=raw`
  (`driver_stdio.go:349-372`). Emit `thought` — marker-only if the
  block is signature-only (match the M4 mapper's
  `{marker_only:true, signature_present}` shape).
  (b) Stamp `context_window` onto `usage` events (source:
  `turn.result.by_model[model].context_window`, else the M4 adapter's
  model table) so the R2 ring works on claude M2.
  (c) Fix the profile translator's `by_model` camelCase passthrough —
  the v1 grammar's known map-iter parity gap
  (`agent_families.yaml:270-274`); either add the map-iter construct or
  post-normalize in the driver. Digest fold and insights both name
  `by_model` as authoritative, so the mangled path silently zeroes
  per-model stats whenever the profile path is active.
- **E2 — codex M2 plan + turn telemetry.**
  (a) Promote `turn/plan/updated` from `kind=system` raw dump
  (`agent_families.yaml:652-660`, which already records this as a
  future wedge) to the `plan` kind in the ACP driver's snapshot shape
  (`entries[]`, hub-stamped `message_id`, `partial:true`) so codex
  plans reach the plan card and Todos dock chip on both clients.
  (b) Stamp `duration_ms` onto codex `turn.result` — the driver
  already tracks `turnID` at start (`driver_appserver.go:1486-1511`);
  measure wall-clock driver-side. Leave `cost_usd` absent (D-4;
  imputed cost covers the digest).
- **E3 — streaming tool output (`tool_call_update` with content).**
  Today the kind exists only from the ACP driver, and codex's
  `item/*/outputDelta` is swallowed by the delta filter
  (`driver_appserver.go:1410-1425`) — a running bash shows nothing
  until it exits. Un-swallow for `commandExecution` output: reuse the
  existing per-item buffer + ~5 Hz throttle
  (`handleAgentMessageDelta`/`flushStream` pattern) to emit
  `tool_call_update {tool_use_id, output (cumulative), partial:true}`,
  finalized by the existing `tool_result`. claude M2 has no wire
  channel for this — degrade (D-4). Client folds join on the existing
  dual id-shape helper (`callToolIdOf`/`callToolId` — the ×3-recurrence
  bug class; both key shapes, both clients).
- **E4 — hub relay image passthrough** (asymmetry #3).
  `mcp_browser_bridge.go` flattens desktop-bridge tool results through
  `mcpResultJSON` → a remote agent gets a PNG as base64-in-text. When
  the bridge result already carries MCP `content[]` with an `image`
  block, pass it through as a real image content block. Small Go
  change; the local stdio relay already proves the shape end-to-end.

### Lane R — renderer + composer parity (desktop)

- **R1 — inline approval/question cards (correctness first).**
  `EventCard.tsx` has **no case** for `approval_request` /
  `attention_request` / parked-approval markers — they fall to the
  generic payload `<details>` dump, while `toolGroups.ts:57-62` hides
  the gate `tool_call` on the assumption an inline card exists. Build
  the two full cards (D-3): approval card with typed bodies where the
  payload carries them (diff/command/file; codex elicitation forms from
  `mcpServer/elicitation/request` are already parked as attention
  items) and question card (claude M4 `dialog_type:user_question`
  shape). Answer path: existing input kinds `approval` / `answer` /
  `attention_reply` (`handlers_agent_input.go:282-437`). The
  AttentionDock stays as the cross-agent aggregator; the Companion
  renders the same item inline, single-resolve semantics shared.
- **R2 — context ring + cost.** Zero `context_window` reads exist in
  `desktop/src` today; mobile's telemetry strip already has the field
  (parity-miss class, 5th instance). Add context-fill to
  `transcriptStats.ts` + stats strip + a Composer-adjacent ring:
  codex M2 = `usage.last_total_tokens / usage.context_window`;
  claude M4 = latest `usage.context_window` (statusLine-authoritative);
  claude M2 = post-E1. Cost: live strip shows `turn.result.cost_usd`
  running sum where the engine ships it, else digest/imputed only
  (labeled, D-4). A `/compact`-style affordance appears at high fill
  (verb exists: `context.compacted` mutation path).
- **R3 — turn footers + compaction dividers.** `turn.result` is in
  `ALWAYS_HIDDEN_KINDS`; render it instead as a quiet per-turn footer
  (duration · msgs · cost when present) under the closing assistant
  row. Add EventCard cases for `context.compacted` / `.cleared` /
  `.rewound` (today: generic dump) and promote M4's
  `system{subtype:compact_boundary}` to the same divider treatment —
  hairline + centered label, token before→after when the payload has
  it. Sweep `feedLens.ts` allowlists deliberately (D-2).
- **R4 — live output + agent-produced media.** Render E3's cumulative
  `tool_call_update` output inside the running tool row (expandable
  while running, scroll-capped like kimi-web's 50-line block), folding
  by the `streamingPartials.ts` chain mechanism. Render agent-produced
  images: `tool_result` MCP image blocks and `termipod-att://` refs
  paint inline (the `Markdown.tsx` resolver exists); `blob:` refs get
  the blob resolver instead of today's skip (asymmetry #4).
- **R5 — subagent panel.** The dock's flat name-match rows
  (`stateDock.ts`) gain a detail panel: click a subagent chip → side
  panel with that subagent's filtered event stream (events already
  carry `subagent` marking — the digest fold skips `subagent:true`
  usage). Phase bars are kimi-only data — out (D-5).
- **R6 — composer pills.** Model pill and permission-mode pill wired
  to the **existing but unused** input kinds `set_model` / `set_mode`
  (`handlers_agent_input.go:415-503`), sourced from `session.init`
  (`model`, `permission_mode`, `slash_commands`) and the family
  registry's `permission_modes`. Thinking-effort segment only where the
  engine reports it (`status_line` / `fast_mode_state`). Slash picker
  already exists — fold it into the same pill row.

### Lane D — design-system enforcement (desktop)

- **D1 — desktop UI reference doc.** `ui-guidelines.md` is
  Flutter-only; the desktop semantic layer
  (`01-base-shell.css:11-100`) is undocumented. Write the desktop
  counterpart: token layers (DTCG source → generated primitives →
  desktop semantic layer), the three-tier tool rendering rule, the
  full-card discipline (D-3), motion rules (tokens `--ease`, duration
  caps, reduced-motion), mono-vs-sans semantics.
- **D2 — harden the ratchet toward kimi-web's cop.**
  `lint-desktop-tokens.sh` ratchets hex/primitive-var counts and
  hard-fails phantom tokens. Add kimi-web `check-style.mjs`-class
  rules (MIT — port the patterns): off-scale radius, off-scale
  font-weight, gradient text, glassmorphism/backdrop-filter, colored
  glows; new categories enter as ratchets at current baseline, flip to
  hard-zero as R-lane wedges retire violations.
- **D3 — motion pass.** Tool row/group expands to the
  `grid-template-rows: 0fr↔1fr` pattern; scroll pinning on expand
  (kimi-web's rAF `pinScrollFor`); pulsing running dot; one reserved
  "waiting for first response" spinner state. All under the token caps
  from D1.

### Lane T — ACP M1 transport rows (hub registry; YAML-only by design)

- **T1 — claude-code M1** via `agentclientprotocol/claude-agent-acp`
  and **codex M1** via `codex-acp`: one registry row each + steward
  template `cmd:` change, per the `launch_m1.go` header contract
  ("no Go diff"). Version-pin the adapters. Review gates: each
  adapter's `promptCapabilities.image` actually true; permission-mode
  mapping fidelity vs our `permission_modes` templates; fallback chain
  `[M2, M4]` retained so a broken adapter degrades, not breaks.
  Payoff: capability negotiation feeds F3, and the ACP driver already
  emits `thought`/`tool_call_update`/`plan`/`diff` natively — engines
  running M1 get lane-E richness for free.

## 4. Sequencing

```
W1 (unblock + correctness):  F1, F2, L1, E1, R1
W2 (local + telemetry):      L2, E2, R2, R3, F3
W3 (local claude + live):    L3, E3, E4, R4
W4 (local codex + controls): L4, R6, R5, F4
parallel, any time:          D1 → D2 → D3;  T1 after F3
```

Each wedge is a separate PR with the standard review pass. Lane E
before its lane-R consumer in every pair (E1→R2, E3→R4); L1 before
any L3/L4, L2 before L3. Lane L can also accelerate independently —
the renderer consumes one shape regardless of source (D-7).

## 5. Non-goals (recorded so they don't creep)

Embedding vendor **cloud** apps (claude.ai, chatgpt.com — discussion
§2/§4; local vendor UIs are in-policy per D-1); goal strips / budget
enforcement UI; cron-fire cards; BTW-style side chats; undo/fork/export
session verbs; git status headers; server-side prompt queue + steer
verbs (the InputRouter's implicit queue stays as-is); kimi swarm
phase-bar parity; porting kimi-web code wholesale (patterns under MIT,
yes; Vue components, no); non-loopback exposure of the L3 local
service (LAN/mobile clients are a recorded follow-up, not this plan).

## 6. Review anchors + standing bug classes

- **Busy-parity:** every new/promoted kind (E1 `thought`, E2 `plan`,
  E3 `tool_call_update`-with-content, R3 un-hiding `turn.result`)
  must be walked through both clients' `_isAgentBusy`/busy logic and
  fold allowlists before merge (D-2 precedent).
- **Dual tool-id shape:** any new tool-lineage consumer checks both
  `id` and `tool_use_id` via the shared helpers (`callToolIdOf` /
  `callToolId`) — 3 prior recurrences.
- **Mobile↔desktop parity:** R2 closes a known parity miss
  (mobile telemetry strip has `contextWindow`; desktop doesn't) — 5th
  instance of the class; every lane-R wedge states its mobile
  counterpart status explicitly (ship, follow-up, or N/A with reason).
- **Digest invariants:** E1(c) touches `by_model` — digest fold
  (`digest_fold.go:463-495`) treats it as authoritative at turn close;
  corpus parity tests must cover the normalized shape, and
  `ensureAgentDigest` refolds sealed rows on schema bump (no
  migration).
- **CI blind spot:** desktop frontend state tests don't run in CI —
  run `node --test src/state/*.test.ts src/ssh/*.test.ts
  src/terminal/*.test.ts` manually per wedge. Screen-bound work (R1
  cards, R2 ring, R4 media, D3 motion) accrues to the owed
  Playwright/device pass.
- **Frame-profile discipline:** E1/E2 profile edits need corpus
  fixtures per `docs/reference/frame-profiles.md`; the fallback rule
  (unmatched → `raw`) means a mis-written rule fails silent — pin with
  fixtures, not eyeballs.

## 7. Acceptance

**Hub-less (the primary scenario, D-7):** with no hub configured and
the kimi binary absent, the Companion spawns a **local claude-code**
and a **local codex** session (L3/L4): dock opens (F1), annotate-crop
lands as a native image (F2/F3), approvals and questions render as the
only two full cards and resolve inline (R1), the context ring fills
and the `/compact` affordance appears (E1-shape/R2), a long-running
codex bash streams output into its row (E3-shape/R4), plans render as
cards for both engines (E2-shape), the composer shows model +
permission pills that actually switch (R6), sessions survive a
renderer restart and rebind after a full app restart (L3), a codex
session started by the vendor TUI can be attached and continued from
the Companion (L4/D-8), and `lint-desktop-tokens` passes with the D2
rules on. With the kimi binary present, the kimi tab appears as the
vendor-UI arm of the same policy (D-1) — gating nothing.

**Hub-attached (the option):** the same surface bound to hub-spawned
codex M2 + claude-code M2 agents passes the same script unchanged —
same cards, same ring, same pills — plus fleet-only affordances. Byte
compatibility of the two sources is pinned by the shared frame-profile
corpus (L2). kimi-web, when present, keeps its tab — and no longer
gates anything.

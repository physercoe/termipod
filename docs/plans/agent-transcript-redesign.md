# Agent transcript redesign — tool groups, state chips, slash picker, kimi M4 wire-tail

> **Type:** plan
> **Status:** Draft — for maintainer review
> **Audience:** contributors, maintainer
> **Last verified vs code:** main @ `57d96f6d` / kimi-code 0.28.1 on macOS arm64, 2026-07-23

**TL;DR.** The agent transcript on both clients renders every tool call as an
individually-collapsed card, gives session state (todos, usage, mode) no real
home, and — for kimi engines — silently drops usage/subagent/todo data that
the engine produces but ACP doesn't stream. This plan redesigns the transcript
around a shared model — **turn-grouped prose + batched tool-activity cards +
a state-chip row** — borrowing proven patterns from kimi-web (MIT), Cline,
and Zed, and adds a kimi `wire.jsonl` tail adapter as kimi's proper **M4**
mode. Four delivery phases (tool groups → state chips → slash picker →
wire-tail), all evidenced below from live probing of kimi-code 0.28.1 and a
code map of both termipod clients. **Deferred by director decision:** mobile
turn navigation (`TurnStepperPill`/`FeedMinimap` promotion) and mobile
mode/plan/goal composer toggles.

---

## 1. Background — the three felt problems

1. **Tool events drown the transcript.** Neither client batches: every
   `tool_call` is its own row, collapsed by default (mobile `FoldableToolCall`,
   `tool_renderers.dart:103`) or behind `<details>` (desktop
   `EventCard.tsx:270`). Scanning a work session means expanding card after
   card. kimi-cli/kimi-web instead *group* consecutive tool calls into one
   glanceable card.
2. **Session state has no home.** Todos render as transient per-update
   snapshot cards (a new card per `plan` update — no fold-in-place); usage
   lives in chips/sheets scattered across surfaces; cron/background has no
   representation. Users can't answer "what is the agent tracking right now?"
   without scrolling.
3. **Kimi M1 silently drops data the engine produces.** ACP streams no usage
   telemetry and no subagent inner activity for kimi-code 0.28.x (verified by
   live probe, §2.1) — but kimi records *all of it* to its local session
   store. Termipod renders neither.

Secondary: the slash-command picker is absent on desktop and has no kimi
profile on mobile, although kimi's ACP emits a full command catalog.

## 2. Evidence (all gathered live, 2026-07-23, kimi-code 0.28.1)

### 2.1. What crosses the ACP wire — and what doesn't

Probe: `kimi --yolo acp` driven over JSON-RPC (`initialize` → `session/new` →
`session/prompt` forcing `TodoList` + `Bash`). Observed `session/update`
types:

| Emitted | termipod handling today |
|---|---|
| `agent_message_chunk`, `agent_thought_chunk` | ✓ text/thought, fold-in-place via `message_id` |
| `tool_call`, `tool_call_update` | ✓ rows; desktop *hides* all updates (`feedLens.ts:50-57`) |
| `plan` (todo entries + status) | ✓ mapped (`driver_acp.go:1539`) but renders as a **new snapshot card per update** |
| `available_commands_update` | forwarded, tagged hidden system (`driver_acp.go:1546-1560`, comment reserves it "for a future slash-command picker") |
| `session/request_permission` (fired even under `--yolo`) | ✓ approval_request flow |
| **NOT emitted:** usage/quota, subagent inner activity, background-task state | nothing to render |

`session/new` returns `configOptions` (model/thinking/mode selects) — the
driver already translates these (`translateConfigOptions`,
`driver_acp.go:939`).

### 2.2. kimi's richer local surfaces (the M4 data source)

- **Session wire store** — `~/.kimi-code/sessions/<wd>/<session>/agents/*/wire.jsonl`
  (protocol v1.4, canonical schema in the OSS repo's `packages/protocol`):
  - `usage.record` — per-turn tokens incl. cache read/creation split + model
  - `tools.update_store` `key:"todo"` — authoritative todo list per change
  - `context.append_loop_event` → `tool.call` carries kimi's own `display`
    hints (e.g. `{kind:"todo_list"}`)
  - `state.json` maps the subagent tree (`parentAgentId`); each subagent has
    its own `wire.jsonl` → **subagent inner activity is recoverable**
  - `permission.record_approval_result`, `llm.request`, `config.update`
  - Workspace mapping needs no hash reverse-engineering: `~/.kimi-code/workspaces.json`
    maps cwd → `wd_*` id (plus `session_index.jsonl`).
- **`kimi web`** first-party server (REST + WebSocket, bearer auth):
  `/api/v1/sessions` exposes `busy`/`main_turn_active`/`pending_interaction`;
  `/sessions/<id>/messages` returns full history incl. thinking; `/providers`
  + `/models` catalog. Useful cross-check; **not** proposed as a dependency
  (internal, undocumented — the wire file is stabler).
- **kimi-code is fully open source (MIT)**, monorepo `MoonshotAI/kimi-code`:
  `apps/kimi-web` (Vue 3 — the reference transcript UI), `apps/vis` (session
  replay visualizer — proves wire-store reconstruction end-to-end),
  `apps/kimi-inspect` (protocol inspector), `apps/vscode` (baseline manager),
  `packages/transcript` (model library), `packages/protocol` (v2 schema),
  `packages/acp-adapter` (ground truth on what ACP strips).

### 2.3. kimi-web transcript anatomy (screenshotted live)

Tool calls batched into a group card (`● ☰ N tool calls · done` header;
rows = icon + verb + key arg + diffstat `+1 −1` + status ✓; per-row lazy
expansion); assistant text as prose between groups; **filter chips above the
composer** — `Bash (32) · Sub Agent (3) · Todos (3/5)` — where the Todos chip
*is* the plan state (done/total); composer Mode menu (Plan/Swarm/Goal
toggles) + permission-mode selector + model picker; `Latest messages` jump
pill; `ConversationToc` turn navigation; inline media cards.

![kimi-web tool groups](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/kimi-web/transcript-tool-groups.png)

![kimi-web media + state chips](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/kimi-web/transcript-media-chips.png)

![kimi-web composer mode toggles](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/kimi-web/composer-mode-toggles.png)

Source components (Vue, MIT): `apps/kimi-web/src/components/chat/`
(`ToolGroup.vue`, `ToolRow.vue`, `ToolCall.vue`, `TodoCard.vue`,
`ThinkingBlock.vue`, `DiffView.vue`, `ApprovalCard.vue`,
`ConversationToc.vue`, `SlashMenu.vue`, `GoalStrip.vue`, `CronNotice.vue`,
`TasksPane.vue`) + `chatTurnRendering.ts` (the turn-grouping model).

### 2.4. Current termipod substrate (code map)

Already exists and reusable:

- **Lineage folding** — mobile `FoldMaps.fromEvents` (`live_feed.dart:996`),
  desktop `useToolMaps` (`AgentTranscript.tsx:93-117`): result/update → parent
  join. The grouping substrate.
- **Stream folding** — mobile `collapseStreamingPartials`
  (`feed_reducer.dart:1236-1285`, `message_id`+`partial` chains). **Desktop
  has no port** — codex/gemini streams stack as duplicate rows (parity bug).
- **Turn anchors** (`feed_reducer.dart:1090` / desktop equivalent), busy
  inference (`agentIsBusy`, byte-parity both clients), lens kind-sets
  (`FeedLens {all,text,turns,tools,errors}`), session-state reducers
  (`modeModelStateFromEvents`, `latestStatusLinePayload`, rate-limit/cost).
- **Attention** — mobile pinned approval cards; desktop `AttentionDock`.
- Desktop always-hides `tool_call_update` and demotes `thought` to verbose
  (`feedLens.ts:50-66`) — mobile shows both; a second parity gap.

## 3. Reference designs — what to borrow from whom

| Source | License | Borrow |
|---|---|---|
| **kimi-web** (MoonshotAI/kimi-code) | MIT | Tool-group cards; filter chips with counts; Todos (n/m) chip; turn-grouped prose; `Latest messages` pill; `SlashMenu` UX |
| **Cline** | Apache-2.0 | Task-timeline polish; per-edit approve/reject; cumulative session **Changes rollup** (P5) |
| **Zed** agent panel | GPL — design only | ACP-native rendering: inline per-edit diffs, mode selector |
| **Goose** (Block) | Apache-2.0 | Electron desktop IA (same shell tech as our desktop) |
| **codex-cli / gemini-cli** | Apache-2.0 | Exec-cell + approval rendering for the terminal surface |
| **Open WebUI / LibreChat** | BSD-3-ish / MIT | Mobile-responsive chat idioms (sheets, long-press actions) |

**Visual-design stance (unchanged from prior review):** keep termipod's
dark-first Linear/Radix token system. Borrow interaction/information
patterns only — no palette, no light surfaces, no per-card shadows.

## 4. Goals / non-goals / deferrals

### Goals

- G1. Scan a busy transcript without expanding anything: tool activity is
  batched into glanceable group cards; errors still surface.
- G2. Session state visible at a glance: a chip row with counts incl. a live
  **Todos (done/total)** chip that opens the full checklist.
- G3. Plan updates fold in place (one card updates, not N snapshots).
- G4. Slash-command picker on both clients, fed engine-neutrally from the
  ACP catalog.
- G5. Kimi M4 renders structured events (usage, todos, tool calls, subagent
  activity) from the wire store instead of raw pane text.

### Non-goals

- Visual restyle / palette changes (covered by the stance above).
- Upstream ACP changes (usage/subagent frames) — file with Moonshot
  separately; the wire-tail (P4) is our no-wait path.
- `kimi web` WS bridge as an integration (internal API; wire file only).
- IDE features (baseline revert, editor decorations).

### Deferred (director decision, recorded here so they don't get re-litigated)

- **Mobile turn navigation** (promoting `TurnStepperPill`/`FeedMinimap` from
  Insight into the live feed).
- **Mobile mode/plan/goal composer toggles** (SessionDetailsSheet pickers
  already cover mode/model; composer-level toggles wait).

## 5. The redesign — one transcript model, two renderers

**Model:** turn groups (anchored by `input.text`) → assistant prose →
**activity-group cards** (consecutive tool calls of a turn, one card) →
rows (icon + verb + key arg + diffstat + status, lazy detail). A **state-chip
row** (kind filters with live counts + Todos done/total) sits above the
composer. Errors auto-expand at row level and surface in the group header
count.

**Renderers:** desktop gets the chip row + optional side/rail treatments;
mobile gets the same chips above its composer and **bottom sheets** for the
full todo checklist (no rails). Same reducers, same counts, same model.

## 6. Phases

### P1 — Tool-group cards + plan fold-in-place + desktop streaming parity

The daily-annoyance phase; pure client/hub presentation, no protocol change.

- **Hub** (`hub/internal/hostrunner/driver_acp.go:1539`): stamp `plan`
  updates with a stable per-turn `message_id` + `partial: true` so the
  existing collapse chain folds them into **one card that updates in place**
  (G3). ~20 lines + test (`driver_acp_test.go`).
- **Desktop parity** (`desktop/src/ui/feedLens.ts`, `AgentTranscript.tsx`):
  port `collapseStreamingPartials` (mobile's reducer is the byte-reference);
  stop always-hiding `tool_call_update` — fold into the parent card's status
  pill like mobile (`feedLens.ts:50-57`).
- **Group cards, both clients**: group consecutive `tool_call` events within
  a turn (threshold ≥2) into one card: header `● N tool calls · done/running`,
  rows with icon + localized verb + key argument + diffstat + status, per-row
  lazy expansion, error rows auto-expanded and counted in the header.
  - Mobile: `live_feed.dart` build pipeline (post-`FoldMaps`),
    `transcript/tool_renderers.dart` group widget; reuse `FoldableToolCall`
    for rows.
  - Desktop: `AgentTranscript.tsx` virtual-list grouping,
    `ui/EventCard.tsx` group component; styles in
    `styles/partials/05-transcript-boards.css` per the token stance.
- Verification: existing feed reducer tests extended (grouping, error
  surfacing); desktop typecheck + e2e smoke; manual: a kimi steward session
  with 10+ tool calls scans cleanly.

### P2 — State-chip row + Todos chip (the session-state home)

- **Chip row above the composer, both clients**: `Tools (n) · Sub-agents (n)
  · Todos (done/total) · Errors (n)` — counts from existing reducers +
  `FoldMaps`; chips act as lens filters (extends `FeedLens`, no new state
  system). Mobile replaces the hidden funnel menu (`feed_misc.dart:210`);
  desktop augments the lens `<select>` (`AgentTranscript.tsx:960`).
- **Todos chip** opens the live checklist: mobile modal bottom sheet, desktop
  popover — fed by the latest folded `plan` event (P1 makes this stable).
- Sub-agent chip counts `Agent`/`Task`-named tool calls (name match,
  engine-agnostic); deep subagent rendering waits for P4 (kimi) / upstream
  (others).
- Verification: reducer unit tests for counts; lens behavior unchanged when
  no chip active.

### P3 — Slash-command picker (engine-neutral, ACP catalog)

- **Hub** (`driver_acp.go:1546-1560`): lift `available_commands_update` into
  the synthesized session state (`session.init.slash_commands` shape, the
  claude-code frame-profile precedent `agent_families.yaml:133`), incl. on
  `session/load` replay. Engine-neutral: covers kimi-code, kimi-code-ts,
  gemini in one move.
- **Mobile**: the dynamic `/` suggestion strip already consumes
  `session.init.slash_commands` (`agent_compose.dart:201-227`,
  `live_feed.dart:945-964`) — zero new UI. Add a static `kimi` entry to
  `lib/models/snippet_presets.dart` (+ `action_bar_presets.dart` id) as the
  no-catalog fallback, mirroring claude/codex.
- **Desktop**: new `/` picker in `ui/Composer.tsx` (prefix match → insert as
  raw text send, mirroring mobile's `raw: true` path); `SlashMenu.vue` as the
  UX reference. The read-only chip cloud in `AgentInfo.tsx:261` stays.
- Verification: hub test that an `available_commands_update` synthesizes the
  state event; mobile widget test for the strip; desktop typecheck + e2e.

### P4 — Kimi M4 wire-tail adapter (structured kimi without ACP)

Kimi's M4 today falls through to the generic `PaneDriver` (noted as a
non-goal gap in `docs/plans/kimi-code-ts-engine.md:104`). Replace it with a
LocalLogTail-style adapter — the claude/antigravity precedent — that tails
the wire store:

- **Locate**: read `~/.kimi-code/workspaces.json` (respect `KIMI_CODE_HOME`)
  for cwd → `wd_*` dir; pick the session via `session_index.jsonl` /
  newest `session_*` dir created after spawn.
- **Tail & parse** `agents/*/wire.jsonl` (schema = OSS `packages/protocol`,
  v1.4): map `context.append_loop_event` (`tool.call`/`tool.result`, with
  `display` hints) → `tool_call`/`tool_result` events; `tools.update_store`
  `key:"todo"` → `plan` events; `usage.record` → `usage` events;
  `permission.record_approval_result` → approval lifecycle; subagent wire
  files (via `state.json` `parentAgentId`) → nested/subagent-marked events.
- **Deliver as kimi-code(-ts) M4** in `agent_families.yaml` frame-adapter
  slot, falling back to `PaneDriver` if the store isn't found (older builds).
- Optional later reuse: the same adapter can *enrich* M1 sessions with usage
  telemetry ACP withholds — declared out of scope for P4 proper to keep the
  wedge reviewable.
- Verification: hostrunner test with a fixture wire.jsonl (recorded from a
  real session, sanitized); manual: kimi pane spawn shows structured rows +
  usage chip.

### P5 — Future (recorded, not scheduled)

- **Cron/background-task state** — kimi's tool surface has
  `CronCreate/CronList` and background `TaskList`, but state isn't in the
  wire store in a stable shape; revisit with upstream.
- **Session Changes rollup** — cumulative per-session diff surface
  (Cline's task diff; kimi `apps/vscode` `baseline.manager.ts` pattern).
- **Inspect surface** — raw-wire record/replay/diff for driver debugging
  (kimi-inspect concept) on top of the existing `raw` verbose events.
- **kimi-insight/vis-style "visualize session" action** on sealed
  transcripts.

## 7. Open questions for the maintainer

1. Tool-group threshold and default expansion: group at ≥2 consecutive calls,
   header collapsed when the group is done? (kimi-web ships expanded-rows /
   collapsible-header; Cline ships collapsed-by-default.) Proposal: expanded
   rows while running, auto-collapse header on turn end.
2. Should the chip row *replace* the lens control or coexist (chips = quick
   lenses, funnel = advanced)? Proposal: coexist on desktop, replace on
   mobile.
3. P4 scope guard: OK to keep M1-enrichment out of P4 and review it as its
   own wedge?
4. Where does the desktop Todos popover live — anchored to the chip, or a
   pinned card in the status bar (`.transcript-status`)?
5. Mobile funnel removal: any operator attachment to the current funnel UI,
   or clean swap?

## Appendix A — ACP probe transcript (excerpt)

```
[tool_call] {"title":"TodoList","kind":"other","status":"pending"}
[PLAN] [{"content":"echo hi","status":"completed"},{"content":"echo bye","status":"in_progress"}]
[request_permission] options=["approve_once","approve_always","reject"]
[tool_call] {"title":"Bash","kind":"execute","status":"pending"}
[prompt result] {"stopReason":"end_turn"}
=== DISTINCT UPDATE TYPES SEEN ===
available_commands_update, agent_thought_chunk, tool_call, tool_call_update, plan, agent_message_chunk
```

## Appendix B — wire.jsonl event types (real session, protocol v1.4)

```
metadata ×1 · config.update ×2 · tools.set_active_tools ×1 · turn.prompt ×1
context.append_message ×1 · context.append_loop_event ×26 (step.begin/content.part/tool.call/tool.result/step.end)
llm.tools_snapshot ×1 · llm.request ×5 · tools.update_store ×3 (key="todo")
usage.record ×5 ({model, usage:{inputOther, output, inputCacheRead, inputCacheCreation}, usageScope:"turn"})
permission.record_approval_result ×2
```

## Appendix C — kimi-code OSS repo map (MIT)

```
apps/kimi-web (Vue 3 reference UI) · apps/vis (session replay) · apps/kimi-inspect (protocol inspector)
apps/vscode (baseline.manager.ts) · apps/kimi-code (CLI/TUI)
packages/protocol (v2 wire schema) · packages/transcript (model lib) · packages/acp-adapter
```

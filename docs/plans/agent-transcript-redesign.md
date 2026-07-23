# Agent transcript redesign — embed kimi-web, tool groups, state dock, slash picker, kimi M4 wire-tail

> **Type:** plan
> **Status:** Accepted 2026-07-23 — maintainer decisions recorded in §7
> **Audience:** contributors, maintainer
> **Last verified vs code:** main @ `57d96f6d` / kimi-code 0.28.1 on macOS arm64, 2026-07-23

**TL;DR.** The agent transcript on both clients renders every tool call as an
individually-collapsed card, gives session state (todos, usage, mode) no real
home, and — for kimi engines — silently drops usage/subagent/todo data that
the engine produces but ACP doesn't stream. This plan redesigns the transcript
around a shared model — **turn-grouped prose + batched tool-activity cards +
a kimi-web-style state dock** — borrowing proven patterns from kimi-web
(MIT, source-verified), Cline, and Zed; embeds **`kimi web` as a desktop web
panel** so kimi users get the first-class UI today; and adds a kimi
`wire.jsonl` tail adapter as kimi's proper **M4** mode. Five delivery phases
(**P0** embed panel → **P1** tool groups + plan fold-in-place + desktop
parity → **P2** state dock → **P3** slash picker → **P4** wire-tail), all
evidenced below from live probing of kimi-code 0.28.1, the kimi-web source,
and a code map of both termipod clients. **Deferred by director decision:**
mobile turn navigation (`TurnStepperPill`/`FeedMinimap` promotion) and mobile
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

### 2.3. kimi-web transcript anatomy (screenshotted live + source-verified)

**Turn rendering.** Consecutive tool calls in a turn form a `tool-stack`
(`chatTurnRendering.ts` — a run of ≥2; a lone call renders standalone via
position `single`). The group card (`ToolGroup.vue`): header `● ☰ N tool
calls · <state>`, aggregate state = **running > error > done**; rows = icon +
verb + key arg + diffstat `+1 −1` + status ✓, per-row lazy detail. Groups are
**expanded by default and never auto-collapse** — header click is the only
toggle (with scroll-pinning). Assistant text flows as prose between groups.

**State dock (not transcript filters).** The chips above the composer —
`Bash (32) · Sub Agent (3) · Todos (3/5)` — are a **bottom dock**
(`ChatDock.vue`): ambient state chips that toggle a **dock panel** above the
composer listing that kind of task. kimi-web has *no transcript filtering*;
chips are state + detail-panel toggles, orthogonal to any lens. Only
**background** bash/subagent tasks get chips — *foreground* subagents render
inline in the transcript (`ConversationPane.vue:225-229`).

**Todos.** The checklist (`TodoCard.vue`) renders **inside the dock panel**
("待办 · N/M" header owned by the dock); done rows = strikethrough + faint,
in-progress = medium weight, and todo rows share `StatusGlyph` with the
bash/subagent task rows so all state reads as one system. On **mobile**,
todos get a **dedicated `~/todo` tab** (TodoCard CSS), not a dock.

**Composer.** Mode menu with Plan/Swarm/Goal toggles + permission-mode
selector ("Manual") + model picker; `SlashMenu.vue` on `/`. Elsewhere:
`Latest messages` jump pill, `ConversationToc` turn navigation, inline media
cards, `GoalStrip`/`CronNotice`/`StatusPanel`/`TasksPane` state components.

![kimi-web tool groups](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/kimi-web/transcript-tool-groups.png)

![kimi-web media + state chips](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/kimi-web/transcript-media-chips.png)

![kimi-web composer mode toggles](https://raw.githubusercontent.com/agentfleets/termipod/issue-assets-llmforge/docs/issue-assets/kimi-web/composer-mode-toggles.png)

Source components (Vue, MIT): `apps/kimi-web/src/components/chat/`
(`ToolGroup.vue`, `ToolRow.vue`, `ToolCall.vue`, `TodoCard.vue`,
`ChatDock.vue`, `ThinkingBlock.vue`, `DiffView.vue`, `ApprovalCard.vue`,
`ConversationToc.vue`, `SlashMenu.vue`, `GoalStrip.vue`, `CronNotice.vue`,
`TasksPane.vue`) + `chatTurnRendering.ts` (the turn-grouping model).

### 2.3.1. kimi-web is embeddable — and its server is local

`kimi web` serves the full UI on `127.0.0.1:<port>` with a bearer token in
the URL hash (`#token=…`, printed at startup; persistent token rotatable via
`kimi web rotate-token`). The UI is an SPA with client-side session switching
— **no per-session deep link** (the hash carries the token, not routes).
This makes the UI embeddable in a `<webview>` guest as-is (see P0).

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
| **kimi-web** (MoonshotAI/kimi-code) | MIT | Tool-group cards (expanded-by-default, running>error>done); **state dock** (chips toggle a bottom task panel, not feed filters); Todos-in-dock with shared status glyphs; background-only task chips / foreground-subagents-inline; turn-grouped prose; `Latest messages` pill; `SlashMenu` UX |
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

- G0. Kimi users get kimi's first-class transcript UI *today*, embedded as a
  web panel in the desktop app — the escape hatch while G1–G5 build the
  native cross-engine rendering (and the wedge that makes the assistant panel
  type-extensible: terminal | files | **web**).
- G1. Scan a busy transcript without expanding anything: tool activity is
  batched into glanceable group cards; errors still surface.
- G2. Session state visible at a glance: a **state dock** — ambient chips
  with live counts (incl. **Todos done/total**) that toggle a detail panel,
  per the kimi-web ChatDock model. State, not feed filtering.
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
rows (icon + verb + key arg + diffstat + status, lazy detail; groups expanded
by default, user opt-in collapse; aggregate state running > error > done).
A **state dock** sits above the composer: ambient chips with live counts
(background tasks, background subagents, Todos done/total) that toggle a
**detail panel** for that kind — *not* feed filters; the existing lens system
stays untouched. Foreground subagents render inline in the transcript. Errors
auto-expand at row level and surface in the group header count.

**Renderers:** desktop gets the dock panel above the composer; mobile gets
the same chips, opening **dedicated tabs / bottom sheets** for the todo
checklist (kimi-web's `~/todo` tab pattern — no rails, no docks on small
screens). Same reducers, same counts, same model.

## 6. Phases

### P0 — Embed kimi web as a session web panel (desktop escape hatch)

Ships kimi's first-class transcript UI *inside* termipod today, and makes the
assistant panel type-extensible (terminal | files | **web**). Independent of
P1–P4, which build the native cross-engine rendering.

- **Substrate exists — with one required extension**: the Read surface's
  `<webview>` stack (`desktop/src/surfaces/BrowserView.tsx` +
  `desktop/electron/src/webtab.ts`) solves guest hardening — no preload,
  window-open/navigation policy, real history. But its `will-attach-webview`
  guard **rejects any partition other than `persist:webtab`**, and its
  navigation policy allows any http(s) origin — so the kimiweb panel cannot
  simply reuse it. P0's concrete main-process work is extending `webtab.ts`
  to a small partition **allowlist with per-partition navigation policy**,
  the kimiweb partition pinned loopback-only. Then add a third panel kind in
  the session area (`desktop/src/terminal/SessionView.tsx` terminal|files
  sub-switcher) hosting the guest.
- **Local kimi**: spawn/attach `kimi web --no-open --port <free>` (lifecycle
  mirrors `LocalAgentLauncher`'s local-process management), capture the
  printed `#token=…`, embed `http://127.0.0.1:<port>/#token=<tok>`. Dedicated
  **non-persistent** `kimiweb` partition — the bearer token rides the URL
  hash and a persistent partition would keep it in guest history; the token
  is re-captured at each spawn anyway. Navigation policy pinned to loopback.
- **Remote hosts**: one new wedge — SSH local port-forward (`forwardOut`) on
  the existing ssh2 connection (termipod has no port-forwarding today; only
  hub A2A tunnels), new `ssh_forward_start/stop` IPC; the same panel then
  works for agents on remote GPU boxes running `kimi web` on their loopback.
- **Honest caveats**: (a) kimi-web is an SPA with **no per-session deep
  link** — the panel opens its last-active session and the user switches in
  its own sidebar (upstream feature request candidate); (b) this is a
  *parallel UI*, not an integration — hub events / attention / team features
  do not see what happens inside the guest. It's "kimi's UI in our chrome",
  not a data path.
- Verification: desktop typecheck + e2e smoke (guest loads a loopback URL,
  policy blocks external navigation); manual: kimi session usable end-to-end
  inside the panel (todos dock, subagents, usage, mode toggles, slash menu).

### P1 — Tool-group cards + plan fold-in-place + desktop streaming parity

The daily-annoyance phase; pure client/hub presentation, no protocol change.

- **Hub** (`hub/internal/hostrunner/driver_acp.go:1539`): stamp `plan`
  updates with a stable per-turn `message_id` + `partial: true`. Note the
  `plan` arm currently posts with `tagIfReplay` only — it needs the same
  `stampTurnID` turn tracking the `tool_call` arm has. ~20 lines + test
  (`driver_acp_test.go`).
- **Clients must fold `plan` too** — the hub stamp alone does nothing:
  `collapseStreamingPartials` folds ONLY kinds `text` and `thought`
  (`feed_reducer.dart:1252` kind allowlist). Add `'plan'` to the mobile
  allowlist and include it in the desktop port below; that pair — not the
  hub change by itself — is what delivers **one card that updates in place**
  (G3).
- **Desktop parity** (`desktop/src/ui/feedLens.ts`,
  `desktop/src/surfaces/AgentTranscript.tsx`):
  port `collapseStreamingPartials` (mobile's reducer is the byte-reference);
  stop always-hiding `tool_call_update` — fold into the parent card's status
  pill like mobile (`feedLens.ts:50-57`).
- **Group cards, both clients**: group consecutive `tool_call` events within
  a turn (threshold ≥2 — kimi-web's `tool-stack` rule; a lone call stays
  standalone) into one card: header `● N tool calls · <state>` with aggregate
  state **running > error > done**; rows with icon + localized verb + key
  argument + diffstat + status, per-row lazy detail. **Groups default
  expanded and never auto-collapse** (kimi-web behavior — collapse is user
  opt-in per group); error rows auto-expand their detail and are counted in
  the header.
  - Mobile: `live_feed.dart` build pipeline (post-`FoldMaps`),
    `transcript/tool_renderers.dart` group widget; reuse `FoldableToolCall`
    for rows.
  - Desktop: `surfaces/AgentTranscript.tsx` virtual-list grouping (the
    measured list must re-measure when a group toggles), `ui/EventCard.tsx`
    group component; styles in `styles/partials/05-transcript-boards.css`
    per the token stance.
- Verification: existing feed reducer tests extended (grouping, error
  surfacing); desktop typecheck + e2e smoke; manual: a kimi steward session
  with 10+ tool calls scans cleanly.

### P2 — State dock (the session-state home, kimi-web ChatDock model)

Ambient state chips above the composer that **toggle a detail panel** — state
visibility, not feed filtering (the lens/funnel system stays untouched on
both clients).

- **Chips (both clients)**: `Tasks (n running) · Sub-agents (n) · Todos
  (done/total)` — counts from existing reducers + `FoldMaps`. **Background
  tasks only** earn chips; foreground subagents render inline in the
  transcript (kimi-web rule). Background detection: ACP/tool-call metadata
  where available (kimi `display` hints via P4, claude task frames);
  engine-agnostic name match (`Agent`/`Task`) as the baseline.
- **Desktop**: a collapsible **dock panel above the composer**
  (ChatDock analogue) listing the chip's kind — task rows and todo rows share
  one status-glyph style, done todos strikethrough + faint. Todos content
  comes from the folded `plan` card (P1 makes it stable).
- **Mobile**: chips open a **dedicated tab / modal bottom sheet** with the
  same lists (kimi-web's `~/todo` tab pattern) — no dock on small screens.
- Verification: reducer unit tests for counts; widget/component tests for the
  dock open/close + list rendering; lens behavior unchanged (no chip = no
  filter).

### P3 — Slash-command picker (engine-neutral, ACP catalog)

- **Hub** (`driver_acp.go:1546-1560`): lift `available_commands_update` into
  the synthesized session state (`session.init.slash_commands` shape, the
  claude-code frame-profile precedent `agent_families.yaml:133`), incl. on
  `session/load` replay. Engine-neutral: covers kimi-code, kimi-code-ts,
  gemini in one move. **Busy-parity anchor**: today's system/system tagging
  exists precisely so `_isAgentBusy` skips these frames — the synthesized
  state event must stay on that skip path (kind/producer `system`, or the
  skip list) on BOTH clients.
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
  real session, sanitized — the fixture pins protocol v1.4; the adapter must
  gate on the wire `metadata` protocol version and fall back to `PaneDriver`
  on mismatch, and tolerate partial trailing lines: append/flush cadence of
  wire.jsonl is unverified); manual: kimi pane spawn shows structured rows +
  usage chip.

### P5 — Future (recorded, not scheduled)

- **Cron/background-task state** — kimi's tool surface has
  `CronCreate/CronList` and background `TaskList`, but state isn't in the
  wire store in a stable shape; revisit with upstream.
- **Session Changes rollup** — cumulative per-session diff surface
  (Cline's task diff; kimi `apps/vscode` `baseline.manager.ts` pattern).
- **Transcript Insight mode** — raw-wire record/replay/diff for driver
  debugging (kimi-inspect concept) as a mode of the existing transcript
  Insight view (`insight_transcript.dart` lineage), on top of the existing
  `raw` verbose events. NOT a new surface — and not the J3 **Inspect** tab,
  whose name is taken by the code/diffs/logs/models inspector
  (`docs/plans/debug-code-logs-diffs-models.md` §0a).
- **kimi-insight/vis-style "visualize session" action** on sealed
  transcripts.

## 7. Decisions (maintainer, 2026-07-23)

1. **P0 — accepted, local-first.** The embedded kimi-web panel is wanted;
   ship the local spawn/attach path. The remote SSH-forward wedge
   (`ssh_forward_start/stop` over the existing ssh2 connection) is its own
   follow-up PR.
2. **Web-panel kind — kimi-scoped UI, registry-shaped internals.** Build the
   panel-kind plumbing so another agent web UI is one registry row later; no
   generic UI now.
3. **Tool-group expansion — kimi-web behavior as written**: ≥2 threshold,
   expanded by default, never auto-collapse, user opt-in per group. (See the
   desktop re-measure note in P1.)
4. **P4 scope — M1-enrichment stays out.** Dedupe/provenance between ACP
   frames and wire events for the same tool call needs its own design;
   review it as a separate wedge.
5. **Mobile todo surface — bottom sheet first** (less IA churn); promote to
   a dedicated tab only if the checklist becomes multi-section.

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

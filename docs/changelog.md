# Changelog

> **Type:** reference
> **Status:** Current (2026-04-30)
> **Audience:** contributors, operators
> **Last verified vs code:** v1.0.349

**TL;DR.** Append-only record of what shipped in each tagged release.
One section per version, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/) — Added / Changed /
Fixed / Deprecated / Removed / Security. Entries link to the commit
or PR for forensic detail.

This complements:
- `roadmap.md` — current focus and Now/Next/Later view
- `decisions/` — append-only ADRs for architectural choices
- Git tag annotations — short-form release notes per tag

History before v1.0.280 lives in git log only. The active-development
arc starts at v1.0.280 (steward sessions soft-delete + agent-identity
binding). Seed entries prior to that are in
[`#earlier-history`](#earlier-history) below.

---

## v1.0.411-alpha — 2026-05-08

### Added
- **ACP `session/load` on respawn (ADR-021 W1.2).** When the hub
  resumes a gemini-cli session that has a captured engine cursor,
  it now injects `resume_session_id: <id>` into the rendered
  `spawn_spec_yaml`. `SpawnSpec.ResumeSessionID` plumbs the value
  through `launch_m1.go` to `ACPDriver.ResumeSessionID`. On
  handshake, the driver caches `agentCapabilities.loadSession`
  from the `initialize` response; when both the cursor is set AND
  the agent advertises load support, it calls `session/load`
  instead of `session/new`. On load failure (stale cursor, agent
  doesn't actually implement the method), the driver logs a
  warning and falls back to `session/new` so the operator still
  gets a session — fresh, but usable.
- **Replay event tagging.** Session/update notifications streamed
  by the agent during `session/load` (the historical-turn replay)
  are tagged `replay: true` in their event payloads via the new
  `tagIfReplay` helper. Live notifications after Start completes
  are unaffected. Mobile-side dedupe (W1.3) consumes this flag.
- **`spliceACPResume` helper.** Sibling to `spliceClaudeResume` —
  yaml.v3-Node-based top-level field injection so the cursor
  flows through the same template-derived YAML pipeline as
  claude's `--resume` cmd splice. Defensive: empty cursor →
  no-op, idempotent, replaces a stale prior id.

### Tests
- 4 new ACPDriver tests cover load-when-capable, fallback when
  loadSession unsupported, fallback on rpc-error, and replay
  tagging round-trip.
- 4 new `spliceACPResume` shape tests + 1 end-to-end resume test
  (`TestSessions_ResumeThreadsACPCursor`) pin the gemini-cli
  resume path mirror of the claude resume pin.

---

## v1.0.410-alpha — 2026-05-08

### Added
- **ACP `session.init` event for engine-side cursor capture
  (ADR-021 W1.1).** `ACPDriver.Start()` now emits a dedicated
  `session.init` agent event with `producer=agent` after the ACP
  `session/new` handshake completes. The hub's engine-neutral
  `captureEngineSessionID` (gate: `kind=session.init &&
  producer=agent`) lifts the gemini sessionId into
  `sessions.engine_session_id` — same column claude already uses
  per ADR-014. No migration; column existed since 0033. This is
  the prerequisite for W1.2 (`session/load` on respawn): without
  the cursor in the database, there is nothing to splice on
  resume. Tests cover the driver-side emission and the hub-side
  capture for `kind=gemini-cli` agents.

---

## v1.0.349-alpha+1 — 2026-04-30 (docs/tooling, no app rebuild)

### Added
- **Glossary** ([`docs/reference/glossary.md`](reference/glossary.md))
  — canonical defs for every project-specific term that has more
  than one possible meaning. ~50 entries across 11 domains
  (Sessions, Agents, Engines, Hosts, Events, Attention, UI,
  Protocols, Storage, Process). Each entry has a one-line def, an
  optional *Distinguish from:* line, and a link to its canonical
  concept doc. §12 indexes the "easy to confuse with" pairs for
  fast disambiguation. Trigger: 200K LOC of accumulated drift +
  the 2026-04-30 claude-code resume bug, which surfaced because
  *session* meant two different things in two adjacent layers and
  nothing pinned the boundary.
- **doc-spec §7 — term-consistency contract.** Codifies the rules:
  first-use linking to glossary, no new term without an entry in
  the same commit, qualifier required when ambiguous. CI lint
  enforces #1 and #2; #3 is review discipline.
- **CI lint** (`scripts/lint-glossary.sh`). Four checks: glossary
  structure (no orphan headings), §12 index integrity, spelling-
  variant drift detection across all docs (with code-context
  filtering so `hub/internal/hostrunner` package paths don't
  false-flag), and a warning-level new-term gate. Wired into
  `.github/workflows/ci.yml` alongside the existing
  `lint-docs.sh`.
- **PR template** gains a "Term consistency" section pointing at
  the glossary contract and the local lint command.
- **Tester / end-user UI guide**
  ([`docs/how-to/report-an-issue.md`](how-to/report-an-issue.md))
  — bug-report template + annotated ASCII layouts of every major
  screen + UI vocabulary (AppBar, BottomNav, BottomSheet, Card,
  Chip, ListTile, FAB, TabBar, …) + verb glossary (tap vs
  long-press vs swipe) + common confusion points (Resume vs Fork,
  agent vs engine, status chip colours). Parallel artifact to the
  engineering glossary, audience: testers and normal users.

### Changed
- **doc-spec.md** restructured: §7 is the new term-consistency
  contract; §8 (was §7) is the contract for new docs; §9 (was §8)
  lists CI lints; §10/§11 (open questions / references)
  renumbered.
- **Two real prose drift fixes** caught by the new lint:
  `host runner` → `host-runner` in
  `discussions/transcript-ux-comparison.md` and
  `plans/agent-state-and-identity.md`.
- **`discussions/transcript-source-of-truth.md`** status block
  forwarded to ADR-014 (the operation-log framing this discussion
  rests on); broken auto-memory cross-link replaced with a memory
  reference (not a doc link).
- **`docs/README.md`** index gains pointers to glossary +
  report-an-issue.

---

## v1.0.349-alpha — 2026-04-30

### Fixed
- **Claude-code resume actually resumes** ([ADR-014](decisions/014-claude-code-resume-cursor.md)).
  Pre-v1.0.349, tapping Resume on a paused claude-code session
  spawned a fresh engine session every time — same hub transcript
  window, brand-new claude conversation cursor. The CLI flag exists
  (`claude --resume <session_id>`); the hub just never threaded it.
  Surfaced from device-test feedback on v1.0.348-alpha.

  Three pieces, one wedge:
  - **Migration `0033`** adds `sessions.engine_session_id TEXT`.
    Engine-neutral column — claude calls it `session_id`, gemini
    calls it `session_id`, codex calls it `threadId`; all three
    can land their cursors here as their capture paths get wired.
  - **Capture path** (`captureEngineSessionID` in
    `handlers_sessions.go`). The `POST /agents/{id}/events`
    handler watches for `kind=session.init && producer=agent`
    frames, lifts `payload.session_id` from claude's stream-json
    `system/init` (already extracted by `StdioDriver.legacyTranslate`
    at `driver_stdio.go:295`), and `UPDATE`s the live session row.
    Best-effort — capture failure can't fail the event insert; the
    worst case is a cold-start resume, the pre-ADR-014 baseline.
    `kind=text` events that happen to carry session_id are
    explicitly ignored, as are `producer=user` echoes.
  - **Splice path** (`spliceClaudeResume` in `resume_splice.go`).
    `handleResumeSession` reads `engine_session_id` alongside
    `spawn_spec_yaml`. When the dead agent's `kind=claude-code`
    and a cursor exists, the helper walks the spec's yaml.v3 node
    tree to `backend.cmd`, strips any prior `--resume <other>`
    pair, and splices `--resume <id>` directly after the `claude`
    binary token. The handler passes the rewritten spec to
    `DoSpawn` but never `UPDATE`s `sessions.spawn_spec_yaml`, so
    successive resumes always splice from a clean cmd.

  Codex (`AppServerDriver.ResumeThreadID`) and gemini
  (`ExecResumeDriver.SetResumeSessionID`) already have the
  driver-side resume plumbing; both are still waiting on hub-side
  capture paths to feed them. Tracked as ADR-014 OQ-1 / OQ-2.

  11 resume-cursor tests: 7 splice unit tests (basic shape,
  idempotence, prior-id replacement, non-claude passthrough, empty
  inputs, malformed yaml, missing key, absolute path bin) + 3
  capture + 2 end-to-end resume tests proving
  `agent_spawns.spawn_spec_yaml` carries `--resume <id>` after a
  warm resume and stays clean after a cold one + 1 fork guard
  (`TestSessions_ForkDoesNotInheritEngineSessionID`) pinning the
  fork-is-cold-start invariant so a future "helpfully" inheriting
  change fails loudly at CI rather than mid-conversation.

### Added (continued)
- **Hub transcript is the operation log** ([ADR-014](decisions/014-claude-code-resume-cursor.md) OQ-4 input-side).
  The three engines all ship interactive commands that mutate
  engine-side context without emitting any frame back: claude's
  `/compact` `/clear` `/rewind`, gemini's `/compress` `/clear`. The
  engine's view of the conversation silently diverges from the
  hub's `agent_events` log — same `engine_session_id`, smaller or
  differently-shaped context. Without observability the operator
  scrolls back through what *looks* like a continuous transcript
  and gets surprising agent answers grounded in a context that no
  longer matches what they're reading.

  v1.0.349 ships the input-side observable. The hub's input route
  watches `kind=text` bodies for a leading per-engine slash command
  and, on match, emits a follow-up typed `agent_event` row with
  `producer=system` and `kind ∈ {context.compacted, context.cleared,
  context.rewound}`. Mobile renders these as inline operation chips
  so the transcript reads "[user] /compact → [system] context
  compacted" — same hub session, same `engine_session_id`, but the
  marker pins where the engine view diverged.

  Per-engine vocabulary in
  `hub/internal/server/context_mutation.go`:
  - claude-code: `/compact`, `/clear`, `/rewind`
  - gemini-cli: `/compress`, `/clear`
  - codex: TBD — slash vocabulary not yet audited; emission is a
    no-op until ADR-014 OQ-4b lands

  Engine-*emitted* mutations (e.g. claude's auto-compact when the
  context window fills) still aren't observable — those need the
  engine's stream to surface the event, which is option α deferred
  in `discussions/fork-and-engine-context-mutations.md`.

  10 new tests: 5 detector unit tests (per-engine vocab, leading-
  slash discipline, case sensitivity, unknown-engine no-op) + 5
  end-to-end input-route tests proving the marker lands at
  `seq=N+1` after the input.text row, that plain text emits no
  marker, that non-text input kinds (answer, etc.) skip the
  detector even when their body looks slash-y, and that codex
  agents stay silent until their vocabulary is audited.

### Changed
- **ADR-014 expanded** with the fork-is-cold-start section, the
  hub-vs-engine session boundary (cursor inheritance forbidden),
  and four open questions for follow-up wedges:
  OQ-1 codex `threadId` capture, OQ-2 gemini cross-restart cursor
  feeder, OQ-3 reconcile-driven respawn, **OQ-4 engine-side
  context mutations** (claude `/compact` `/clear` `/rewind`,
  gemini `/compress` — the hub today doesn't observe these and
  the engine's view of the conversation drifts from the hub's
  `agent_events` log without any marker frame), and OQ-5 fork
  productisation. Cross-linked to a new
  [`discussions/fork-and-engine-context-mutations.md`](discussions/fork-and-engine-context-mutations.md)
  that maps the design space across both axes (fork carryover +
  mutation observability) for the next wedge to start from.
- **`docs/decisions/README.md`** index gains rows for ADR-013 and
  ADR-014 — the prior wedge's index update was missed in v1.0.348.

---

## v1.0.348-alpha — 2026-04-29

### Added
- **Gemini integration via exec-per-turn-with-resume** ([ADR-013](decisions/013-gemini-exec-per-turn.md)).
  Third engine alongside claude-code (M2 stream-json) and codex
  (M2 app-server JSON-RPC). gemini-cli has no `app-server`
  equivalent, but headless mode now emits a stable `session_id`
  (PR [#14504](https://github.com/google-gemini/gemini-cli/pull/14504),
  Dec 2025) and accepts `--resume <UUID>` for cross-process session
  continuity. Wedge shipped as slices 1-6, all in this release:
  - **Slice 1:** ADR-013 written; ADR-011 D6 + ADR-012 D6 cross-link
    the per-engine `permission_prompt` matrix.
  - **Slice 2:** gemini-cli frame profile in `agent_families.yaml`
    — top-level `type`-keyed dispatch (init/message/tool_use/
    tool_result/error/result) into the same typed agent_event
    vocabulary claude/codex emit. M2 added to supports. No
    evaluator extension needed (unlike codex's dotted-path
    matchesAll).
  - **Slice 3:** `driver_exec_resume.go` is the spawn-per-turn
    driver. Captures `session_id` from the first `init` event,
    threads `--resume <UUID>` through every subsequent argv;
    `SetResumeSessionID` seeds the cursor on host-runner restart.
    `launch_m2` short-circuits family=gemini-cli before the
    long-running spawn machinery — exec-per-turn doesn't anchor a
    pane (PaneID=""), the bin is resolved via `exec.LookPath`, and
    a `CommandBuilder` injection seam keeps tests off real exec.
  - **Slice 4:** `permission_prompt` is unsupported on gemini
    (ADR-013 D4 — gemini has no in-stream approval gate). Driver
    rejects `attention_reply` with `kind=permission_prompt` as a
    defense-in-depth check. Reference + discussion docs grew the
    per-engine matrix (Claude sync, Codex turn-based, Gemini
    unsupported). Stewards self-route through `request_approval`.
  - **Slice 5:** per-family MCP config materializer adds
    `<workdir>/.gemini/settings.json` (JSON, stdio command+env shape
    matching claude's `.mcp.json` — gemini-cli's `mcpServers`
    schema accepts it identically). 0o600 inside .gemini/ 0o700.
    No CODEX_HOME-style env trick needed; gemini reads project-
    scoped settings.json automatically.
  - **Slice 6:** `agents.steward.gemini.v1` template + prompt ship
    in the embedded fs. Spawn cmd is bin-only (`gemini`) — the
    driver appends `-p <text> --output-format stream-json
    --resume <UUID> --yolo` per turn, ADR-013 D7. Prompt grows a
    "Decisions that need approval" section since gemini has no
    engine-side gate.

  15 new tests cover every wire-format contract: 7 driver tests
  (first-turn argv, second-turn --resume threading, rehydration,
  Stop interrupting in-flight Wait, permission_prompt rejection,
  nil CommandBuilder), 4 MCP-config tests (wire shape, escapes,
  perms, dispatcher branch isolation), 3 frame-profile tests
  (corpus, payload fields, embedded), 1 embedded-template test.
  Slice 7 (cross-vendor `request_help` smoke against live codex +
  live gemini binaries) remains unfunded and gated on a test host
  with both binaries installed — same gate as ADR-012 slice 7.

### Changed
- **Roadmap "Now" gains the gemini wedge** as Done; verifying on
  device next. The "Next" entry "Gemini exec-per-turn driver"
  collapses into the cross-vendor smoke (slice 7 × 2) — codex and
  gemini share the integration-smoke gate.

---

## v1.0.347-alpha — 2026-04-29

### Added
- **Codex integration via app-server JSON-RPC** ([ADR-012](decisions/012-codex-app-server-integration.md)).
  Codex CLI joins claude-code as a first-class engine; the hub
  drives `codex app-server --listen stdio://` over a long-lived
  JSON-RPC pipe rather than `codex exec --json` per turn. Wedge
  shipped as slices 1-6:
  - **Slice 2 (v1.0.343):** frame profile in `agent_families.yaml`
    translates app-server's thread/turn/item lifecycle plus
    telemetry into the same typed agent_event vocabulary
    claude uses. `matchesAll` grew dotted-path support
    (`params.item.type: agentMessage`) for one-method-many-types
    dispatch.
  - **Slice 3 (v1.0.344):** `driver_appserver.go` is the JSON-RPC
    client + thread manager. Handshake is initialize → initialized
    notification → thread/start (or thread/resume <id>); Input(text)
    maps to turn/start; the Driver interface is the launch_m2
    return type so codex and claude both fit.
  - **Slice 4 (v1.0.345):** approval bridge. Codex's
    `item/commandExecution/requestApproval` and siblings POST an
    `attention_items` row (kind=permission_prompt) and park the
    JSON-RPC request id locally; `dispatchAttentionReply` fires for
    permission_prompt too, and the driver's `Input("attention_reply")`
    looks up the parked id and writes the per-method JSON-RPC
    response on /decide resolution. Vendor-neutral equivalent of
    Claude's permission_prompt without the canUseTool sync limit.
  - **Slice 5 (v1.0.346):** per-family MCP config materializer.
    Claude keeps `.mcp.json`; codex writes `.codex/config.toml`
    (TOML, hand-formatted, no library dep). Token at 0o600.
  - **Slice 6 (v1.0.347):** `agents.steward.codex.v1` template +
    prompt ship in the embedded fs. Spawn cmd
    `CODEX_HOME=.codex codex app-server --listen stdio://` bypasses
    codex's trusted-projects gate.
- **Decision history on Me page.** Clock icon opens recent resolved
  attentions; tap into one to see the per-decision audit trail
  (timestamp, decider, verdict, reason/body/option) on the detail
  screen.

### Changed
- **Permission_prompt is now per-engine, not per-architecture.**
  Sync on Claude (canUseTool contract); turn-based on Codex
  (app-server deferrable JSON-RPC). ADR-011 D6's
  bridge-mediated-stdio post-MVP wedge is now Claude-only by
  construction (ADR-012 D7).
- **Me filter chip "Approvals" → "Requests"** since the bucket
  spans approval_request, select, help_request, template_proposal —
  none of which are pure approve/deny.

### Fixed
- **Resume preserves transcript.** Stopping an active session and
  resuming it minted a new agent and the chat opened empty — the
  list/SSE endpoints AND'd `agent_id = ?` even when `session=<id>`
  was provided. Now session=<id> scopes by session_id (with team
  auth), orders by ts, and the mobile feed dedupes by event id +
  paginates with a new `before_ts` cursor since per-agent seq is
  unusable as a cross-agent total order.
- **Stream-dropped banner on idle close cycles.** SSE onDone with
  no error is an idle artifact (proxy keepalive, mobile carrier),
  not a real drop. Banner now fires only on onError.
- **Rate-limit countdown rendering "1540333567h"** when Anthropic
  shipped resetsAt as a microsecond-precision integer. Unit
  heuristic now handles seconds / ms / µs / ns plus a 7-day
  sanity bound so any future unit confusion drops the tile.

## v1.0.338-alpha — 2026-04-29

### Changed
- request_approval / request_select / request_help converted from
  long-poll to turn-based delivery. The MCP call now returns
  immediately with `{id, status: "awaiting_response"}`; the agent
  ends its turn per the updated tool description. The principal's
  reply lands as a fresh user turn (`input.attention_reply` agent
  event, `producer="user"`) when /decide resolves the attention.
  Removes the 10-minute timeout, the connection-pinned wait, and
  the failure mode where a reply 12 minutes after the question was
  silently dropped. Persistence moves from the open HTTP connection
  to the conversation history — a 3-day-later reply still wakes
  the agent. permission_prompt is unchanged: it stays sync because
  Claude's canUseTool protocol has no "deferred" branch (vendor
  contract limitation, not a design choice).
- handleDecideAttention fans out the resolution to the originating
  agent via a new `dispatchAttentionReply` helper. Target lookup is
  attention.session_id → sessions.current_agent_id; if the session
  was resumed since the request was raised, the new agent (which
  inherits the conversation context) receives the reply. Best-
  effort: a fan-out hiccup doesn't roll back the /decide.
- StdioDriver gains a new input kind `attention_reply` that produces
  a user-text turn (NOT a tool_result, since the original tool call
  has already returned). Format per attention kind:
    approval → "Approved" / "Rejected. Reason: <reason>"
    select   → "Selected: <option>"
    help     → "<body>" verbatim or "Dismissed without reply"
  Short correlation prefix `[reply to <kind> <id-prefix>]` so the
  agent can match replies to multiple in-flight requests.
- `agent_input` HTTP handler accepts the new `attention_reply` kind
  for completeness (so an operator can wake an agent from CLI in a
  pinch); server-side fan-out from /decide is the primary producer.

### Removed
- `requestSelectTimeout` and `requestHelpTimeout` constants (10
  minutes each). No replacement — turn-based delivery has no time
  bound.
- The long-poll branches and timeout-handling code in mcpRequestSelect
  and mcpRequestHelp.

### Tests
- TestRequestHelp_ReturnsAwaitingResponseImmediately: pins the
  synchronous return contract (1s upper bound, fail-fast on a long-
  poll regression).
- TestDecide_HelpRequestFansOutAttentionReply: end-to-end — agent
  asks → user decides → input.attention_reply event posted to the
  agent with the principal's body verbatim.
- TestMCP_RequestSelect_TurnBasedRoundTrip: replaces the prior
  `_StoresOptionsAndLongPolls` test; covers the new return shape +
  decide behavior.
- TestStdioDriver_InputFrames: 3 new subtests for attention_reply
  formatting (help_request approve, select approve, approval_request
  reject).

### Docs
- docs/reference/attention-kinds.md §5 rewritten as
  "Resolution semantics — turn-based delivery" with a worked round-
  trip diagram, per-kind /decide payloads, per-kind user-turn text
  format, and a "Why turn-based, not long-poll" rationale section.
  permission_prompt called out as the principled exception.

## v1.0.337-alpha — 2026-04-29

### Added
- "Open project" button on the approval-detail Origin section, next to
  "Open in chat". Visible when the attention has a project pointer
  (project_id column or scope_kind='project' + scope_id). Routes to
  ProjectDetailScreen using the cached project row from hub state.
- Scroll-to-event-id on session chat: SessionChatScreen + AgentFeed
  gain an `initialSeq` parameter. After the cold-open backfill, the
  feed scrolls to and briefly highlights (2px primary-tinted border,
  ~1.2s) the event whose seq matches. Used by approval-detail's
  "Open in chat" button so the principal lands at the agent's turn
  that raised the request, not at the generic tail.
  Implementation: GlobalKey on the matched AgentEventCard +
  Scrollable.ensureVisible — works with non-uniform row heights
  without a positioned-list dependency. Falls back to tail scroll
  when the seq isn't in the loaded page (older than 200 newest).
  Auto tail-follow disables on a successful jump so subsequent SSE
  events don't yank the user back to the bottom mid-read.
- Host info on host detail: OS, arch, kernel, CPU count, total
  memory, hostname now render as named rows on the host detail
  sheet (Hosts tab → tap host). Sourced from a new
  `capabilities.host` field on the host-runner capabilities sweep.
  Host-runner probes once at startup (ProbeHostInfo) and re-attaches
  the cached pointer to every push so a hub mobile session always
  sees the static facts even if the runner restarted in the middle.
  Linux reads /proc/meminfo MemTotal; Darwin reads `sysctl hw.memsize`;
  kernel via `uname -r` on both. Memory rendered in GiB
  (10 GiB → "10 GiB", 0.5 GiB → "512 MiB"). Replaces the previous
  raw-JSON dump that wasn't readable in practice.
- Capabilities row on host detail rewritten as "Engines" with
  installed family + version joined by `·` (e.g.
  "claude-code 1.0.27 · codex 0.5.1"). Missing engines hidden so
  the sheet doesn't list every supported engine just to say "no".
- Tests: TestProbeHostInfo_PopulatesStaticFields pins OS/arch/CPU
  population and asserts memory is non-zero on Linux/Darwin where
  the probe path is reachable.

### Changed
- HostInfo struct embedded in Capabilities is JSON-optional
  (`omitempty`) for back-compat — old runners (pre-v1.0.337) emit no
  host field and the renderer hides those rows rather than showing
  unknowns.

## v1.0.336-alpha — 2026-04-29

### Added
- Approval detail screen now renders origin context: agent + session
  pointers ("Open in chat" jumps directly to the originating session's
  transcript), the last 10 transcript turns leading up to the request
  (filtered by session_id, capped by attention.created_at), and
  inline action controls that mirror the Me-page card. Resolving from
  the detail screen pops back to the Me page since the row drops off
  the open list.
- Server: request_approval / request_select / request_help all stamp
  attention_items.session_id at insert time via new
  Server.lookupAgentSession helper. Empty for system-originated
  attentions (budget, spawn approval) and pre-v1.0.336 rows; the
  detail screen degrades gracefully to a metadata-only view.
- New endpoint: GET /v1/teams/{team}/attention/{id}/context returns
  {session_id, agent_id, agent_handle, events: [...]} with newest-
  first transcript turns. Two tests pin the contract — full round
  trip from request_help and the no-session-pointer fallback.
- attentionOut now carries session_id; the list endpoint exposes it
  to mobile so the Me-page card can pre-decide whether the detail
  screen will have anything to render.

### Changed
- Inline action widgets (InlineApprovalActions, InlineHelpRequestActions)
  extracted from me_screen.dart to lib/screens/me/inline_actions.dart
  so the approval detail screen can reuse them without a circular
  import. Both gain an optional onResolved callback so the detail
  screen can pop after a successful decide; the Me-page card leaves
  it null and lets the row drop out of the open list on its own.
- approval_detail_screen.dart rewritten as a ConsumerStatefulWidget
  that fetches context on mount; the apologetic "actions will land
  here in a follow-up" footer is gone — actions are inline.

## v1.0.335-alpha — 2026-04-29

### Added
- New `help_request` attention kind — the third interaction shape,
  complementing `approval_request` (binary) and `select` (n-ary).
  Used when the agent needs free-text input from the principal:
  clarification, direction, opinion, or hand-back ("I'm stuck, take
  over"). MCP tool `request_help` parallels `request_approval` and
  `request_select`; payload carries `question`, optional `context`
  (agent's framing), and `mode` (`clarify` | `handoff`). The decide
  endpoint now accepts a `body` field; an approve on a help_request
  without a body is rejected (400) since the principal's reply *is*
  the answer. Long-poll surfaces the body to the agent verbatim,
  same shape as `request_select`'s option_id flow.
- `docs/reference/attention-kinds.md` — canonical authoring guide
  for picking between the three kinds. Decision tree by
  answer-space cardinality, anti-pattern table with what to use
  instead, worked examples for clarify and handoff modes. The MCP
  tool docstring on `request_help` carries the short form;
  contributors and AI agent maintainers consult this doc for the
  long form. Linked from `hub-agents.md`.
- Mobile `_HelpRequestActions` widget on the Me page renders a
  free-text composer (Send / Skip) when a help_request attention
  appears in the approvals list. Mode chip ("clarify" / "hand-back")
  surfaces the agent's framing; agent's `context` shows above the
  composer. The approval-detail screen footer copy is now
  kind-aware so it doesn't mislead help_request users with
  "Approve / Deny" instructions.

### Changed
- `request_select` is now explicitly tracked in `tiers.go` as
  `TierRoutine` (was relying on the `request_decision` alias entry).

## v1.0.334-alpha — 2026-04-29

### Fixed
- Steward auth tokens now revoke when the agent terminates. Each
  spawn mints a `kind='agent'` row in `auth_tokens` (the bearer the
  agent uses for `/mcp/{token}`); previously no path revoked it, so
  every spawn → terminate cycle left a still-valid token row, and
  pause/resume compounded it (one resume = one fresh token + one
  orphaned-but-live token). New `auth.RevokeAgentTokens(ctx, exec,
  agentID, now)` helper accepts either `*sql.DB` or `*sql.Tx`; called
  from `handlePatchAgent` when status flips to terminated/failed/
  crashed (covers UI terminate, host-runner ack, and the
  `shutdown_self` MCP path which lands here via host-runner) and
  from `handleSpawn`'s session-swap branch in the same tx so a
  rolled-back swap also rolls back the revoke. Idempotent on the
  `revoked_at IS NULL` clause.
- Mobile Auth screen (`tokens_screen.dart`) hides agent-kind rows.
  They're machine-issued + machine-revoked; surfacing them invited
  the operator to revoke a live agent's bearer (which would just
  look like a crash). The "New token" dialog also drops the `agent`
  kind chip — there's no human-issuance flow for agent tokens.

## v1.0.333-alpha — 2026-04-29

### Added
- ADR-010 Phase 1.6: `frame_translator` flag wired end-to-end. New
  `Family.FrameTranslator` field in `agent_families.yaml` selects
  the per-engine translator: `""` / `"legacy"` (default; today's
  hardcoded `legacyTranslate`), `"profile"` (data-driven
  `ApplyProfile` authoritative, legacy not invoked), `"both"`
  (profile authoritative + legacy in shadow with divergence logged
  via slog). Schema sidecar carries the enum so editor LSPs catch
  typos.
- Driver dispatch refactor: `StdioDriver.translate()` is now a
  3-way switch on `FrameTranslator`; the existing translator body
  moved verbatim into `legacyTranslate` and is reachable from both
  the default path and the "both" shadow run. `launch_m2.go`
  populates `FrameTranslator` + `FrameProfile` from the family
  registry at driver construction.
- `profile_diff.go`: extracted `DiffEvents` + `ParityIgnoreFields`
  + `capturingPoster` from the parity test into shared production
  code so the runtime "both"-mode divergence logging and the test
  parity diff use the same machinery and respect the same known-gap
  list. Misconfig (FrameTranslator set, FrameProfile nil) falls
  through to legacy with a warning rather than silently dropping
  events.
- 5 mode-dispatch tests: legacy default, profile-only, both with
  parity-clean frame (no warning), both with synthetic mismatched
  profile (warning fires with diff details), profile-mode misconfig
  fallback.

### Status
- ADR-010 Phase 1 is complete. The data-driven translator is
  shipped, parity-tested, flag-controllable, and dark by default.
  Phase 2 (canary → flip default → delete legacy) starts when the
  operator flips claude-code's `frame_translator: both` in their
  hub deploy and runs for a release window without divergence
  warnings.

## v1.0.332-alpha — 2026-04-29

### Added
- ADR-010 Phase 1.5: parity-test harness + seed corpus.
  `profile_parity_test.go` runs every frame in
  `testdata/profiles/claude-code/corpus.jsonl` through both
  translators (the legacy hardcoded `translate()` and the new
  data-driven `ApplyProfile`) and diffs the resulting agent_events
  by `(kind, producer, payload)`. Diff output is rule-level and
  agent-readable: which frame, which event index, which payload
  field, and what the legacy/profile values were. 13-frame seed
  corpus exercises every translate() branch (system.init / 3
  rate_limit shapes / task subtypes / assistant text+tool / user
  tool_result / result / error / unknown raw fallback).
- Grammar extension: `payload_expr: <expr>` for whole-payload
  passthrough. Used when the legacy translator emits the raw frame
  as payload (system fallback, error, deprecated completion alias)
  — three rules in the claude-code profile now use it. Mutually
  exclusive with `payload`; documented in
  `docs/reference/frame-profiles.md` §4 and the JSON Schema sidecar.
- `HUB_STREAM_DEBUG_DIR` env var: when set, the StdioDriver tees
  every raw stream-json line to `<dir>/<agent_id>.jsonl`. Operators
  use this to grow the corpus from real claude-code traffic — run
  the agent, copy interesting frames into the testdata directory,
  re-run the parity test.

### Changed
- Two known-gap fields documented as deliberate parity skips
  rather than profile bugs:
    - `by_model` — legacy normalizeTurnResult renames inner
      camelCase keys (inputTokens → input, etc.); v1 grammar has
      no map-iter construct.
    - `overage_disabled` — legacy derives a bool from
      `reason != nil`; v1 grammar has no bool-from-nullable
      predicate. Mobile reads `reason` directly.
  Adding to `parityIgnoreFields` is a deliberate policy decision;
  reviewers should read the comment before extending.

### Status
- ADR-010 Phase 1 is feature-complete (1.1 schema, 1.2 evaluator,
  1.3 translator, 1.4 profile + agent-readability artifacts, 1.5
  parity harness). Phase 1.6 (frame_translator flag) and Phase 2
  (canary → flip default) remain. Profile-driven translation is
  still dark — the legacy translator owns production traffic until
  the flag wires up.

## v1.0.331-alpha — 2026-04-29

### Removed
- `aider` retired from supported engines. Project decision: only
  cover dominant-vendor products (Anthropic claude-code, OpenAI
  codex, Google gemini-cli). Aider is a small open-source project
  that doesn't justify the per-engine maintenance cost. Touched:
  `agent_families.yaml` (entry deleted), `modes/resolver.go`
  (AgentKind comment), `lib/screens/team/agent_families_screen.dart`
  (defaults list), `families_test.go` /
  `spawn_mode_test.go` / `resolver_test.go` (test inputs swapped to
  `codex` where the test exercised cross-engine resolver behavior),
  `driver_stdio.go` comment, plus docs (discussion, plan, reference,
  hub-agents.md, steward-ux-fixes.md). ADR-010 §Context kept its
  decision-time mention of aider per ADR-immutability convention.

## v1.0.330-alpha — 2026-04-29

### Added (still dark — profile authored but legacy translator owns traffic)
- `hub/internal/agentfamilies/agent_families.yaml`: canonical
  claude-code `frame_profile` block. ~10 rules covering session.init
  (with camelCase/snake_case coalesce), all three rate_limit_event
  shape variants (flat / system-subtype / nested rate_limit_info),
  the system fallback, assistant multi-emit (content blocks +
  when_present-gated usage), user.tool_result filter, result →
  turn.result + completion (deprecated alias), and error. Each rule
  carries an inline `# ` comment naming the SDK release it was
  authored for so AI maintainers extending later have the
  upstream-shape lineage.
- `docs/reference/frame-profiles.md`: the agent-facing authoring
  reference. Grammar in BNF, dispatch semantics, scope rules, three
  worked input→output examples (rate_limit shape collapse, assistant
  multi-emit, system subtype hierarchy), common pitfalls calling out
  divergences from JSONata-style expectations. ~250 lines.
- `hub/internal/agentfamilies/agent_families.schema.json`: JSON
  Schema sidecar so editor LSPs (and AI editors) get autocomplete +
  inline validation while authoring overlays. yaml-language-server
  comment in the YAML wires it up automatically.
- `FrameProfile.Description` field — agent-facing prose header that
  states dispatch semantics + scope conventions inline so a fresh
  maintainer reading rule 17 sees the model without grep'ing the
  implementation.
- 7 smoke tests against the embedded profile covering every rule
  surface; full corpus diff test arrives in Phase 1.5.

### Changed
- `docs/plans/frame-profiles-migration.md` Phase 1.4 expanded with
  the five agent-native deliverables (description / reference /
  schema / inline comments / validator). New project memory entry
  `feedback_agent_native_design.md` captures "agent-native is a
  design principle" as a durable lesson — applies beyond frame
  profiles to any future declarative surface (action bar profiles,
  templates, attention-item options).

### Known parity gap
- `result.modelUsage` inner-key renaming (camelCase → snake_case in
  the `by_model` payload). The v1 grammar has no map-iter construct;
  by_model passes through verbatim. Tracked for grammar extension in
  Phase 1.5 once the parity diff surfaces the real shape.

## v1.0.329-alpha — 2026-04-29

### Added (dark code — not yet wired into live driver)
- `hub/internal/agentfamilies`: extended `Family` struct with optional
  `FrameProfile` (ADR-010 schema). New types `FrameProfile`, `Rule`,
  `Emit`. YAML round-trip test locks the wire shape so a rename
  surfaces immediately. Embedded families ship without profiles in
  v1; `FrameProfile == nil` is the steady state until Phase 1.4
  authors the claude-code profile.
- `hub/internal/hostrunner/profile_eval`: new package implementing
  the hand-rolled expression subset (D2 of ADR-010). Grammar:
  `$.path`, `$.path[N]`, `$$.outer.path`, `"literal"`, and
  `a || b || "default"` coalesce. ~150 LoC, zero third-party deps,
  full test coverage of nil propagation / outer scope / array
  indexing / malformed input.
- `hub/internal/hostrunner/profile_translate.go`: `ApplyProfile`
  evaluates a profile against a frame and returns the emitted events.
  Most-specific-match-wins dispatch: an init frame fires only the
  `{type: system, subtype: init}` rule, not the generic `{type:
  system}` fallback. Rules tied for specificity all fire (assistant's
  per-block + usage rules co-fire). When-present gates on a
  non-nil expression; gated rules suppress emit but don't trigger
  the raw fallback. No-match → `kind=raw` verbatim (D5).

This wedge is the load-bearing infrastructure for plan
`docs/plans/frame-profiles-migration.md` Phase 1. Phases 1.4–1.6
(claude-code profile + parity corpus + flag wiring) remain.

## v1.0.328-alpha — 2026-04-29

### Added
- `lib/widgets/agent_feed.dart`: inline answer card for the
  `AskUserQuestion` tool. claude-code emits a tool_call whose input
  carries `questions[].options[]`; the card renders the question +
  options as buttons and ships the picked label back as a
  `tool_result` so the agent can continue. Previously the prompt
  silently timed out, leaving a stale "looks like the question
  prompt was canceled" reply in the transcript.
- `hub/internal/server/handlers_agent_input.go` + `driver_stdio.go`:
  new `answer` input kind. Carved off `approval` because the agent
  expects a clean reply string, not a "decision: note" tuple — the
  driver wraps `body` in a `tool_result` keyed by `request_id` and
  ships it on stdin.

### Fixed
- `hub/internal/hostrunner/driver_stdio.go`:
  `translateRateLimit` now peeks into `rate_limit_info` (and
  `rateLimitInfo`) before reading status/window/resets-at fields.
  Recent claude-code SDK builds nest the actual rate-limit values
  under that sub-object; with the flat lookup the mobile telemetry
  strip stayed empty (window/status/resets-at all nil) every time
  the agent shouted about quota. Three shapes are now handled in
  one path: top-level fields (legacy), `system.subtype=rate_limit_event`
  (mid-versions), and the nested `rate_limit_info` (current).
  Regression test: `TestStdioDriver_RateLimitEventNestedInfo`.
- `lib/widgets/agent_feed.dart`: SSE re-subscribe no longer pops
  "Stream dropped" the moment a *clean* close happens. A clean close
  (`onDone`) after the agent finished a turn is normal — proxy idle
  timeout, mobile-network keepalive cycle, app suspend — and the
  reconnect either gets immediate replay or sits idle waiting on the
  next event. Banner now fires only on real `onError`, or after
  three consecutive empty close cycles, so a finished transcript
  doesn't surface a phantom error.

## v1.0.327-alpha — 2026-04-29

### Fixed
- `hub/migrations/0032_sessions_heal_orphan_active.up.sql`: one-shot
  migration that flips orphan-active sessions to `paused`. Bad data
  accumulated when an agent died via a code path that didn't auto-
  pause its sessions (the auto-pause was added in v1.0.326 but only
  fires through PATCH /agents/{id} status=terminated). Without this
  heal, the device-walkthrough showed sessions in the Detached group
  with a green "active" pill even though the agent was long gone.
  Regression test: `TestSessions_HealOrphanActive`.
- `lib/screens/sessions/sessions_screen.dart`: the Detached sessions
  group now treats every member as Previous and renders any
  `status=active|open` row as `paused` for display. Same rationale as
  the migration — the engine these rows pointed to is gone, so a
  green pill misleads the user. The bucket also auto-expands now
  (instead of starting collapsed) since Previous is the only content
  there. The chat AppBar's Stop action drops out when the attached
  agent isn't live in `hubProvider.agents`, mirroring the list-row
  defensive override.
- `lib/providers/sessions_provider.dart`: `resume()` and `fork()` now
  also call `hubProvider.refreshAll()` so a freshly-spawned steward
  shows up in the cached agents list immediately. Without this, the
  resumed/forked session got bucketed into the Detached group on the
  next render — its `current_agent_id` pointed at an agent the cache
  hadn't seen yet — until the user pulled-to-refresh.

### Changed
- `lib/screens/sessions/sessions_screen.dart`: per-row session menu
  now exposes a status-appropriate terminal action — Stop (active),
  Archive (paused). Previously the only way to kill a session was
  via the chat AppBar's Stop, which forced the user to enter the
  conversation first; archiving a paused session had no surface at
  all. Existing rename / fork-from-archive / delete entries are
  unchanged.
- `lib/screens/sessions/sessions_screen.dart`: Detached group is now
  default-expanded; previously the user had to tap "previous (N)"
  to see what was inside, which was confusing because for that
  group the previous list IS the entire group.

## v1.0.326-alpha — 2026-04-28

### Fixed
- `hub/internal/hostrunner/egress_proxy.go`: rewrite `req.Host` to
  upstream's host in the reverse-proxy Director. Without this, the
  agent's local `127.0.0.1:41825` Host header was forwarded upstream;
  Cloudflare-fronted hubs returned 403 because that hostname isn't a
  known CF zone. Regression test added.
- `hub/internal/hostrunner/driver_stdio.go`: also dispatch
  `type=system,subtype=rate_limit_event` to the rate-limit
  translator. Recent claude-code SDK versions wrap the signal under a
  `system` envelope; without the subtype branch the event was
  passed through as kind=`system` and the mobile telemetry strip
  never saw a `rate_limit` kind. Both shapes now feed the same
  helper.
- `lib/screens/projects/projects_screen.dart`: drop the
  Project/Workspace bottom-sheet picker that fronted the create FAB.
  The kind toggle inside `ProjectCreateSheet` already covers the
  same choice via a SegmentedButton, so the pre-pick was a redundant
  extra tap.
- `lib/widgets/agent_feed.dart` `_systemBody`: render claude-code's
  `task_started` / `task_updated` / `task_notification` system
  subtypes as one-liners (e.g. `Task updated · is_backgrounded=true`)
  instead of dumping the full envelope JSON.
- `hub/internal/server/handlers_agents.go`: extend the auto-pause
  rule to `terminated`. Previously only `crashed` and `failed`
  flipped the matching active session to `paused`, so a user who
  tapped Stop session ended up with a dead agent but a session that
  still claimed to be active — the chat AppBar kept offering Stop
  and the sessions list kept the row in the active bucket. Per
  ADR-009 D6 / the documented Stop-session contract. Existing
  test renamed/extended to cover all three terminal statuses.

### Changed
- `lib/widgets/agent_feed.dart`: jump-to-tail pill is now always
  visible while the user is scrolled away from the bottom (not just
  when new events arrive) and surfaces the current scroll position
  as a percentage. Tool-call cards gained a fold chevron in the name
  row that collapses the body to just the name + status pill, so
  noisy multi-step calls don't dominate the transcript.
- `hub/internal/server/handlers_sessions.go` `handleForkSession`:
  fork no longer auto-attaches to the team's live steward. A
  running steward agent is bound to its own active session via a
  single stream-json connection; pointing a second active session
  at it would race events between the two and silently strand the
  older conversation mid-turn. Fork now always lands the new
  session as `paused` with `current_agent_id` NULL by default, and
  the app drives a spawn (or replace-into-session) into it. An
  explicit `agent_id` parameter is still honoured for callers
  that genuinely have a session-less steward, but the server
  rejects (409) if that agent already owns an active session.
  Tests reworked: `TestSessions_ForkAlwaysUnattachedByDefault`
  asserts the no-auto-attach contract, and
  `TestSessions_ForkRejectsBusyAgent` covers the explicit-but-busy
  guard.
- `lib/screens/sessions/sessions_screen.dart` `_forkSession`:
  always opens the spawn-steward sheet bound to the new session id
  on a successful fork response with empty agent_id (now the
  default path), then navigates into the chat once the spawn
  lands. Replaces the prior misleading "no live steward to attach
  the fork to" error and the silent dual-attach race.
- `lib/screens/sessions/sessions_screen.dart`: the synthetic
  "(no live steward)" group on the Sessions page is renamed to
  "Detached sessions" with a sub-line explaining why the bucket
  exists ("Original steward gone — open to read, fork to continue
  with a fresh one").
- `lib/services/hub/open_steward_session.dart`: when a scope is
  passed but no scope-matching session exists for the live
  steward, open one in that scope instead of silently falling back
  to the steward's general/team session. Fixes the "tap project
  steward chip → land in team/general" routing surprise.
- `lib/screens/team/spawn_steward_sheet.dart`: cap sheet height at
  85% of the screen and wrap the content in a SingleChildScrollView
  so the Cancel/Start row stays reachable on short phones.
- `lib/screens/me/me_screen.dart`: replace the "My work" project
  strip with an "Active sessions" strip — sessions are what the
  principal is actively in the middle of, while the Projects tab
  already covers full project navigation. Each tile shows session
  title + scope (General / Project: <name> / Approving) + steward
  name; tap pushes `SessionChatScreen`. Strip is hidden when no
  active sessions exist. New `meActiveSessionsSection` arb key
  (en + zh); legacy `meMyWorkSection` key removed since nothing
  else referenced it.
- `lib/screens/team/spawn_steward_sheet.dart` + rename dialog in
  `sessions_screen.dart`: relabel the field as **Name** and accept
  the bare domain (`research`, `infra-east`); the app appends the
  `-steward` suffix internally via `normalizeStewardHandle` before
  submitting. The user no longer has to know about the suffix
  convention. Helper text now spells out the uniqueness scope —
  unique among **live stewards on this team**; stopping a steward
  frees the name for reuse. Stale description text dropped its
  `#hub-meta` reference and the "one agent" framing now that
  multi-steward is shipped.

## v1.0.316-alpha — 2026-04-28

### Added
- `scripts/lint-docs.sh` — enforces doc-spec status block,
  resolved-discussion forward links, cross-reference resolution, and
  stale-doc warning (Layer 1 of the anti-drift design).
- `.github/workflows/codeql.yml` — security/quality scanning on push
  and weekly cron.
- `.github/dependabot.yml` — weekly dep-update PRs for Flutter pub +
  Go modules + GitHub Actions.
- `.github/pull_request_template.md` — PR checklist mirroring
  doc-spec §7.
- `docs/changelog.md` (this file) — Keep-a-Changelog format.

### Changed
- `doc-spec.md` §7: documents the three CI rules and DISCUSSION
  resolution accepting both ADR and plan links.

## v1.0.315-alpha — 2026-04-28

### Changed
- `spine/sessions.md`: 14 "Tentative:" markers walked individually,
  marked Resolved (with version where known) or Open. Reading note
  added.
- `spine/blueprint.md` §9: per-bullet status indicators (✅/🟡) +
  ADR cross-links.
- `spine/information-architecture.md` §11: 7 wedges marked ✅ shipped
  with version range; final paragraph rewritten as archaeology.

## v1.0.314-alpha — 2026-04-28

### Changed
- `reference/coding-conventions.md`: rewritten first-principles —
  links to upstream (Effective Dart, `analysis_options.yaml`) instead
  of duplicating; project-specific deltas only; each rule justified
  by the bug it prevents.

### Fixed
- Memory body drift: `user_physercoe.md` (fork name + retired dev
  machine), `project_research_demo_focus.md` (P4 status),
  `project_steward_workband.md` (sequence completed).

## v1.0.313-alpha — 2026-04-28

### Added
- Status blocks on every remaining doc (21 files). Every doc in
  `docs/` now declares Type / Status / Audience / Last-verified at
  the top.
- `reference/ui-guidelines.md` rewritten for Flutter (was
  pre-rebrand React Native).

### Changed
- H1s renamed to match filenames where they had drifted
  (`Wedge memo: Transcript / approvals / quick-actions UX` →
  `Transcript / approvals / quick-actions UX — competitive scan`,
  etc.).

## v1.0.312-alpha — 2026-04-28

### Added
- `reference/coding-conventions.md` rewritten for Flutter/Dart + Go
  (was pre-rebrand React Native).

### Changed
- 4 spine docs gain formal status blocks.
- 3 resolved discussions linked to their ADRs.

## v1.0.311-alpha — 2026-04-27

### Added
- 8 retroactive ADRs in `docs/decisions/` covering shipped decisions:
  Candidate-A lock, MCP consolidation, A2A relay, single-steward MVP,
  owner-authority model, cache-first cold start, MCP-vs-A2A protocol
  roles, orchestrator-worker slice.
- `decisions/README.md` indexes them.

## v1.0.310-alpha — 2026-04-27

### Changed
- 26 doc files reorganized into 7-primitive layout: spine/,
  reference/, how-to/, decisions/, plans/, discussions/, tutorials/,
  archive/.
- Renames per naming spec: `ia-redesign.md` →
  `information-architecture.md`, `agent-harness.md` →
  `agent-lifecycle.md`, `steward-sessions.md` → `sessions.md`,
  `vocab-audit.md` → `vocabulary.md`, `hub-host-setup.md` →
  `install-host-runner.md`, `hub-mobile-test.md` →
  `install-hub-server.md`, `release-test-plan.md` →
  `release-testing.md`, `mock-demo-walkthrough.md` →
  `run-the-demo.md`, `monolith-refactor-plan.md` →
  `monolith-refactor.md`, `wedges/` → `plans/`.
- `spine/sessions.md` promoted out of DRAFT.

## v1.0.309-alpha — 2026-04-27

### Added
- `docs/README.md` — navigation index.
- `docs/roadmap.md` — vision + phases + Now/Next/Later.
- `docs/doc-spec.md` — contract every doc honors (7 primitives,
  status block spec, naming spec, lifecycle rules).

## v1.0.308-alpha — 2026-04-27

### Changed
- Steward composer: cancel button surfaces whenever agent is busy
  (regardless of field content). Tooltip varies by content.

## v1.0.307-alpha — 2026-04-27

### Changed
- Steward composer: cancel only on text+busy (predictive-input flow).
  `isAgentBusy` plumbed from `AgentFeed` via event-stream scan.

## v1.0.306-alpha — 2026-04-27

### Changed
- Steward composer: collapsed cancel onto send slot via text-empty
  heuristic; bolt long-press = save-as-snippet (mirrors action-bar
  pattern).

## v1.0.305-alpha — 2026-04-27

### Added
- Read-through caches for `getAgent`, `getRun`, `getPlan` +
  `listPlanSteps`, `getReview`, `listAgentFamilies` — every detail
  screen serves last-known data from cache.

## v1.0.304-alpha — 2026-04-27

### Added
- Cache-first cold start: `_loadConfig` reads SQLite snapshots
  synchronously into `HubState`; UI lights up before network refresh
  resolves. Pairs with v1.0.303's `refreshAll` schedule. (ADR-006)

## v1.0.303-alpha — 2026-04-27

### Fixed
- Empty Projects/Me/Hosts/Agents on cold start: `HubNotifier.build()`
  now schedules `Future.microtask(refreshAll)` whenever
  `_loadConfig()` returns a configured state.

## v1.0.302-alpha — 2026-04-27

### Changed
- Documentation pass: agent-protocol-roles.md, hub-agents.md,
  research-demo-gaps.md, steward-ux-fixes.md updated to reflect
  v1.0.298 MCP consolidation + W-UI completion.

## v1.0.301-alpha — 2026-04-27

### Fixed
- Drop unused `_statusColor` (CI lint, was unreferenced after v1.0.299
  refactor).

## v1.0.300-alpha — 2026-04-27

### Changed
- Steward composer matched to action-bar composer: fontSize 14,
  maxHeight 120 (unbounded lines), inline clear button, save-as-snippet
  button.

## v1.0.299-alpha — 2026-04-27

### Added
- Steward chat polish: syntax-highlighted code blocks via
  `flutter_highlight`, color-coded diff view with line gutter,
  per-tool icons on `tool_call` cards.

## v1.0.298-alpha — 2026-04-27

### Changed
- Single MCP service: `mcp_authority.go` reuses the hubmcpserver
  catalog in-process via chi-router transport. One `hub-mcp-bridge`
  symlink, one `.mcp.json` entry. (ADR-002)

## v1.0.297-alpha — 2026-04-27

### Changed
- *(Superseded by v1.0.298.)* Wired `hub-mcp-server` into spawn
  `.mcp.json` via host-runner multicall pattern.

## v1.0.296-alpha — 2026-04-27

### Added
- SOTA orchestrator-worker slice: `agents.fanout`, `agents.gather`,
  `reports.post` MCP tools + steward template recipe + worker_report
  v1 schema. (ADR-008)
- Mobile: per-host agents view.

## v1.0.295-alpha — 2026-04-26

### Changed
- Renamed `request_decision` → `request_select` MCP tool with
  back-compat alias. Start-session path for orphaned stewards.

## v1.0.294-alpha — 2026-04-26

### Changed
- Hide MCP gate `tool_call` cards in transcript; remove standalone
  Close-session action (close = terminate).

## v1.0.293-alpha — 2026-04-26

### Added
- Cache sessions list + channel events for offline.

## v1.0.292-alpha — 2026-04-26

### Fixed
- Cache `recentAuditProvider` for offline activity feed.

## v1.0.291-alpha — 2026-04-26

### Added
- Multi-steward wedges 2+3: hosts sort + agent rename.

## v1.0.290-alpha — 2026-04-26

### Added
- Multi-steward wedge 1: handle-suffix convention (`*-steward`),
  auto-open-session on spawn, domain steward templates
  (`steward.research`, `steward.infra`).

## v1.0.286-alpha — 2026-04-26

### Added
- Egress proxy in host-runner: in-process reverse proxy masks the
  hub URL from spawned agents (`.mcp.json` carries
  `127.0.0.1:41825/`, not the public hub).

## v1.0.285-alpha — 2026-04-26

### Added
- Tail-first paginated transcripts.
- Hub backup/restore via `hub-server backup` / `hub-server restore`.

## v1.0.281-alpha — 2026-04-26

### Changed
- Replace-steward keeps the session: engine swap continues the
  conversation. Sessions are durable across respawn.

## v1.0.280-alpha — 2026-04-26

### Added
- Soft-delete sessions + UI; documented agent-identity binding.

---

## Earlier history

Major work units shipped before v1.0.280, summarized:

- **v1.0.200–203** — Artifacts primitive (§6.6 end-to-end). Outputs
  is the 4th axis (Files/Outputs/Documents/Assets).
- **v1.0.208** — Offline snapshot cache: HubSnapshotCache +
  read-through + mutation invalidation + Settings clear (5 wedges).
- **v1.0.175–182** — IA redesign: 7 wedges (nav skeleton, host
  unification, Me tab, Projects tab, Activity tab, Team switcher,
  Steward surface).
- **v1.0.166–167** — Activity feed foundation: audit_events as the
  activity log; mutations call recordAudit; MCP `get_audit` exposes it.
- **v1.0.157** — A2A relay + tunnel for NAT'd GPU hosts.
- **v1.0.151–156** — MCP tool surface expansion to close P4.4 audit:
  `schedules.*`, `tasks.*`, `channels.create`, `projects.update`,
  `hosts.update_ssh_hint`.
- **v1.0.141–148** — Trackio metric digest (storage + poller +
  mobile sparkline).
- **v1.0.49** — Audit log: `audit_events` table + REST + mobile screen.
- **v1.0.27** — Rebrand from MuxPod to termipod.
- **v1.0.18** — File manager (Settings > Browse Files).
- **v1.0.17** — Compose drafts (Save as Snippet → drafts category).
- **v1.0.2** — Data Export/Import via DataPortService.

For any version not listed above, `git log v1.0.X-alpha` and
`git show v1.0.X-alpha` (tag annotation) are authoritative.

---

## Conventions

- **One section per tagged release**, newest first.
- **Categories** (Keep a Changelog): Added · Changed · Fixed ·
  Deprecated · Removed · Security. Omit unused categories.
- **Cross-references**: link to ADRs (`ADR-NNN` or
  `decisions/NNN-name.md`) when a change implements a decision.
- **Patch-level entries**: bug-fix-cadence releases roll up; the
  changelog records substantive changes, not every tag.
- **Append at top**: new entries go above `## v1.0.316-alpha`.
- **Don't rewrite history**: changelog is append-only (modulo typo
  fixes). Past entries are the historical record.

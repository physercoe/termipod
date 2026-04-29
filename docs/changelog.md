# Changelog

> **Type:** reference
> **Status:** Current (2026-04-28)
> **Audience:** contributors, operators
> **Last verified vs code:** v1.0.316

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

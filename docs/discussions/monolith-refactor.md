# Monolith refactor — agent_feed, terminal_screen, hub_client, 1k-LOC screens

> **Type:** discussion
> **Status:** Open (not started — files have grown, not shrunk)
> **Audience:** contributors
> **Last verified vs code:** v1.0.728
> **Freshness:** rolling

**TL;DR.** Tech-debt sketch for the largest files in the Flutter app.
None of the wedges below have shipped, and in the ~400 versions since
this doc was first written (v1.0.312) the files have grown, not shrunk.
The headline: `agent_feed.dart` was dismissed here as "borderline-fine
at 1,295 LOC" — it is now **6,196 LOC, the single largest file in the
codebase** (see "Drift since v1.0.312" below). The goal is unchanged:
shrink the seams without changing user-facing behavior, one wedge per
PR, no new dependencies.

**Why the refactor keeps getting deferred (and why that's now a cost):**
these files block contributor velocity and hide bugs, but they never
blocked a release, so feature wedges always won the slot. That was the
right call pre-demo (a behavioral regression in `terminal_screen.dart`
would have been catastrophic). Post-demo — P4 backend is feature-complete
and the focus is reliability/UX polish (`docs/roadmap.md`) — the refactor
can now fail safely, and the cost of *not* doing it compounds: the
dispatch-classification logic in `agent_feed.dart` has been the site of
repeated fails-open regressions (v1.0.667/699/717/720, see
ADR-context in the changelog), partly because it lives buried in a
1,800-LOC widget state class.

---

## Drift since v1.0.312 (why this refresh exists)

This doc was last verified at v1.0.312. At v1.0.728 the picture has
materially changed. Measured drift in the files it tracked:

| File | v1.0.312 | v1.0.728 | Δ | Note |
|---|---:|---:|---:|---|
| `agent_feed.dart` | 1,295 | **6,196** | +4,901 | doc called it "borderline-fine"; now #1 |
| `terminal_screen.dart` | 4,532 | 4,903 | +371 | still #2 |
| `hub_client.dart` | 1,902 | 3,571 | +1,669 | nearly doubled; R1 never ran |
| `projects_screen.dart` | 1,644 | 2,944 | +1,300 | |
| `settings_screen.dart` | 1,801 | 2,629 | +828 | |
| `project_detail_screen.dart` | 1,749 | 1,998 | +249 | |
| `runs_screen.dart` | 1,928 | 1,928 | 0 | |
| `connection_form_screen.dart` | 1,562 | 1,562 | 0 | |
| `ansi_text_view.dart` | 1,522 | 1,522 | 0 | intentional (bucket C) |

Files >1k LOC went from **12 → 22**. New >1k violators not in the
original doc: `sessions_screen.dart` (2,731), `steward_overlay_chat.dart`
(1,635), `structured_deliverable_viewer.dart` (1,413),
`approval_detail_screen.dart` (1,405), `templates_screen.dart` (1,326),
`shortcut_tile_strip.dart` (1,207), `me_screen.dart` (1,128),
`research_phase_heroes.dart` (1,050).

**Lesson worth keeping:** the original doc's open-question #3 ("add a
CI rule: fail the build if any file in `lib/` exceeds 1500 LOC") was
deferred — and *that deferral is why the drift went unnoticed*. No such
guard exists today (`scripts/` has no LOC check). Without a tripwire, a
"borderline-fine" file quintuples in silence. This refresh promotes that
guard from open-question to **R0** below.

---

## Constraints & non-goals

**Constraints:**
- No behavior change. Every refactor wedge must be a pure rearrangement
  with green CI on `flutter analyze` + `flutter test` + Android/iOS
  release builds, plus a manual smoke test of the affected surface.
  (Flutter is CI-only here — there is no local Flutter SDK, so each
  wedge's verification leans on CI + on-device smoke.)
- No new dependencies. Riverpod + the existing widget patterns are
  sufficient.
- One refactor wedge per PR. Don't bundle.
- Each wedge ≤ 3 days of focused work; if it grows, split it.

**Non-goals:**
- Not rewriting or redesigning any screen. The IA redesign already
  shipped and the surfaces are correct.
- Not changing the public API of `HubClient` for callers (composition
  facade approach — see R1).
- Not chasing the long tail of 600–1000 LOC files; only the >1k violators.
- Not adding tests *as* the refactor — that's a separate tech-debt item
  (Dart test ratio is ~9% of `lib/` LOC: 11,127 test LOC vs 122,492
  `lib/` LOC). But existing tests must stay green.
- Not (here) refactoring the Go hub. Its own monoliths have grown
  (`seed_demo_lifecycle.go` 2,893, `driver_acp.go` 1,861,
  `driver_appserver.go` 1,834, `handlers_agents.go` 1,670,
  `handlers_attention.go` 1,214) and deserve a companion plan — flagged
  in R5 below, out of scope for the Flutter wedges.

---

## Sequencing

```
R0  CI LOC tripwire          (½ day, do FIRST — stops the bleeding)
        ↓
R1  hub_client split         (3–4 days, low risk, sets the pattern)
        ↓
R2A agent_feed split         (8–10 days, highest payoff — now the largest file)
R2T terminal_screen split    (8–10 days, high risk)
        ↓
R3  screen sweep             (parallelizable, medium risk)
        ↓
R4  remainder                (opportunistic, no fixed budget)
        ↓
R5  Go hub companion plan    (separate doc, separate budget)
```

R0 first because without a tripwire the whole exercise erodes (the drift
table above is the proof). R1 next because it's the lowest risk, has no
UI surface area, and establishes the composition-facade pattern that R3
reuses. **R2A (agent_feed) and R2T (terminal_screen) are the high-risk,
high-payoff tier; do R2A first** — it's now the largest file *and* the
one still actively churning (dispatch-classification fixes keep landing
there), so shrinking it pays the most and reduces ongoing regression
risk. R3 last because it's the most parallelizable.

---

## R0 — CI LOC tripwire (½ day, do first)

Add a `scripts/lint-file-size.sh` (wired into the existing lint runner
and CI) that fails when any `lib/**/*.dart` file exceeds a ceiling.
Suggested staged ceilings so it can land *before* the refactor without
turning CI red on day one:

- Phase 0 (now): ceiling **6,300** — fails only if `agent_feed.dart`
  grows further. Ratchets down as wedges land.
- After R2A/R2T: ceiling **1,800**.
- After R3: ceiling **1,500** (the original doc's target).

The ratchet is the point: every wedge that lands lowers the ceiling, so
regression is structurally impossible rather than merely discouraged.
`ansi_text_view.dart` (bucket C, intentionally large) gets an explicit
allowlist entry with a comment, not a blanket exemption.

**Acceptance:** CI fails on a synthetic 6,400-LOC file; passes on HEAD;
ceiling + allowlist documented in the script header.

---

## R1 — `hub_client.dart` split (3,571 LOC → ~300 LOC facade + entity clients)

**Scope:** `lib/services/hub/hub_client.dart` (3,571 LOC, single class).
No sub-clients exist yet (`lib/services/hub/` already has sibling
helpers — `hub_read_through.dart`, `hub_snapshot_cache.dart`,
`hub_profiles.dart`, etc. — but the client itself is monolithic).

**Cleavage by entity** (audit the method names at refactor time; the
buckets below are directional — the file has grown since the original
audit so re-derive exact LOC):

| Sub-client | Owns |
|---|---|
| `HubAuthClient` | `getInfo`, `verifyAuth`, token plumbing in `_open` |
| `HubProjectsClient` | projects CRUD, project-channel events |
| `HubAgentsClient` | agents CRUD, spawns, terminate/archive |
| `HubPlansClient` | plans, plan steps, tasks |
| `HubChannelsClient` | channels, team channels, post/list events |
| `HubHostsClient` | hosts, registration, runner deploy |
| `HubRunsClient` | runs, schedules, firings |
| `HubArtifactsClient` | outputs, blobs, documents, templates, deliverables |
| `HubAuditClient` | attention, decide, audit events |
| `HubPolicyClient` | policy, tokens |
| **Shared core** | `_open`, `_get/_post/_patch/_put/_delete`, `_readJson`, error mapping, snapshot-cache invalidation |

**Approach: composition facade.**

```dart
// lib/services/hub/hub_client.dart — slimmed to ~300 LOC
class HubClient {
  HubClient({required HubAuth auth, ...}) :
    _core = HubHttpCore(auth: auth, ...) {
    projects  = HubProjectsClient(_core);
    agents    = HubAgentsClient(_core);
    // … one per entity
  }
  late final HubProjectsClient projects;
  late final HubAgentsClient   agents;
  // …
}
```

**Migration path for callers:** keep the old method signatures as
`@Deprecated` delegating shims in `HubClient` for two releases, then
delete. This means **the refactor ships as one PR** without a coordinated
cutover across the call sites; migrating call sites becomes a background
task.

**Risks:**
- Snapshot-cache invalidation crosses entity boundaries. Mitigation:
  invalidation lives on `HubHttpCore.invalidate(prefix)`, called by
  sub-clients — already the shape today (see `hub_snapshot_cache.dart`).
- `test/hub_client_*` will need import updates.

**Acceptance:** `hub_client.dart` ≤ 350 LOC; no sub-client > 400 LOC;
all call sites compile unchanged (via shims); existing tests pass;
manual smoke: list projects, create project, post message, view attention.

**Estimated effort:** 3–4 days.

---

## R2A — `agent_feed.dart` split (6,196 LOC → ~1,000 LOC container + 6 sibling files)

**Scope:** `lib/widgets/agent_feed.dart` — **37 classes in one file.**
The two dominant pieces are `_AgentFeedState` (the feed container +
event reducer, ~1,800 LOC) and the per-event card rendering (~1,140 LOC).
This is the new R2-class monolith and the highest-value target.

> **The executable wedge sequence lives in
> `docs/plans/agent-feed-split.md`** (PLAN). A code-grounded read of the
> file revised the ordering below: the reducer is already top-level pure
> functions with 10 dedicated test files, so it is extracted **first**
> (lowest risk), and a shared render-primitives layer is extracted second
> to dissolve the existing `_ToolKvLine`/`_payloadOf` duplication and
> unblock clean cluster moves. The cluster table below remains the
> cleavage map; the plan doc is authoritative for sequence and mechanism
> (import-based layering, not `part`).

**Cleavage by class cluster** (verified against the class list at HEAD):

| New file | Classes (line refs at v1.0.728) | ~LOC |
|---|---|---:|
| `agent_feed.dart` (slim container) | `AgentFeed`, `_AgentFeedState` build + glue | ~1,000 |
| `agent_feed/feed_reducer.dart` | the event-ingestion + kind-classification logic lifted out of `_AgentFeedState` (`kAgentTurnActiveKinds` etc.) into a non-widget reducer | ~400 |
| `agent_feed/event_card.dart` | `AgentEventCard`, `_AgentEventCardState`, `_CardHeader` (3436–4571) | ~1,140 |
| `agent_feed/interaction_cards.dart` | `_PendingPermissionPrompts`, `_PermissionPromptCard(State)`, `_PlanApprovalBody`, `_CompactionBody`, `_PendingSelections`, `_SelectionCard(State)` (2740–3436) | ~700 |
| `agent_feed/approval_cards.dart` | `_ApprovalCard(State)`, `_ApprovalOption`, `_DecisionChip`, `_AskUserQuestionCard(State)`, `_AskOption` (4571–5042) | ~470 |
| `agent_feed/tool_renderers.dart` | `_CollapsibleMono`, `_FoldableToolCall(State)`, `_ToolKvLine`, `_DiffView`/`_DiffLine`/`_DiffKind`, `_ToolResultInline(State)` | ~600 |
| `agent_feed/telemetry_strip.dart` | `_TelemetryStrip`, `_TelemetryTile`, `_ModelTokens`, `_StatusPill` (5418–6092) | ~675 |
| `agent_feed/feed_misc.dart` | `_OfflineBanner`, `_VerboseToggleChip`, `_NewEventsPill` | ~150 |

**The high-value, high-risk piece is `feed_reducer.dart`.** The
event-kind classification (which kinds mark a turn active vs idle, which
chips a payload feeds) currently lives inside `_AgentFeedState` and has
been the site of repeated dispatch-fails-open regressions. Lifting it
into a pure, testable reducer — with the cross-cutting kind-classification
contract test already at
`test/widgets/agent_feed_kind_classification_test.dart` guarding it — is
the single biggest defect-prevention payoff in this whole plan. Do this
extraction deliberately and keep that test green at every step.

**The card/strip extractions are low risk** — they are pure presentation
(bucket A/D recipe): move the class to a sibling file, pass screen-level
state as constructor args, don't introduce inherited widgets.

**Phasing** — five sub-PRs, presentation first to de-risk the reducer:

1. **R2A.1** `feed_misc` + `telemetry_strip` (1d, lowest risk).
2. **R2A.2** `tool_renderers` (1.5d).
3. **R2A.3** `approval_cards` + `interaction_cards` (2d).
4. **R2A.4** `event_card` (2d) — the big presentation move.
5. **R2A.5** `feed_reducer` (3d, highest risk) — by now the file is
   ~half its size; lift the reducer out as a pure function/Notifier and
   prove it against the kind-classification contract test.

**Manual regression checklist** (each sub-PR runs it):

- [ ] Live agent turn: events stream in, busy spinner shows then clears
- [ ] Tool call card folds/unfolds; tool_result default-folded
- [ ] Diff view renders insert/delete/context
- [ ] Permission prompt card appears, allow/deny dispatches
- [ ] AskUserQuestion card: select option, submit
- [ ] Plan-approval + compaction bodies render
- [ ] Telemetry strip: cost chip, context-fill %, rate-limit chips
- [ ] New-events pill appears on scroll-up, tap jumps to bottom
- [ ] Offline banner on network loss
- [ ] Verbose toggle flips envelope-row visibility

**Acceptance:** `agent_feed.dart` ≤ 1,100 LOC; no sibling > 1,200 LOC;
all five sub-PRs ship; checklist clean each time;
`agent_feed_kind_classification_test.dart` green throughout;
`flutter analyze` zero new warnings.

**Estimated effort:** 8–10 days (5 sub-PRs).

---

## R2T — `terminal_screen.dart` split (4,903 LOC → ~700 LOC widget + 5 controllers)

**Scope:** `lib/screens/terminal/terminal_screen.dart`. Single
`_TerminalScreenState` owns ~50 methods across 7 concerns. (Grew modestly
since the original audit — 4,532 → 4,903 — so the cleavage below still
holds; re-confirm method assignments at refactor time.)

**Cleavage by concern:**

| Controller | ~LOC | Owns |
|---|---:|---|
| `TerminalSessionController` | ~700 | SSH connect/reconnect/disconnect, tmux session/window/pane lifecycle, pause/resume polling, stale watchdog, backend heartbeat/content, selectors + create/kill dialogs |
| `TerminalScrollController` | ~500 | scroll handlers, scrollback reset/extend, position indicator, bottom-anchor logic |
| `TerminalInputController` | ~600 | special-key dispatch, key input, gesture mode, insert menu, snippet picker, profile sheet |
| `TerminalLifecycleController` | ~250 | app-lifecycle, metrics, keep-screen-on, foreground task, connectivity |
| `TerminalTransferController` | ~400 | image + file transfer/download listeners |
| `TerminalScreen` (slim) | ~700 | `build()`, layout, AppBar, breadcrumb, error/raw-mode headers, hand-off to controllers |

**Approach:** state controllers as `AutoDisposeNotifierProviderFamily`
scoped to `(connectionId, sessionName)`. **The widget calls into
controllers, not the reverse** — controllers never hold `BuildContext`;
UI affordances (snackbars, dialogs) stay in the widget reacting to
controller state/streams.

**Risks:**
- Implicit ordering between concerns (resume-polling fires after metrics
  change after lifecycle resume). Make the ordering explicit before the
  split; each controller exposes a `Stream<Event>`, the widget orchestrates.
- The buffered-update path (`_applyBufferedUpdate`/`_scheduleUpdate`/
  `_applyUpdate`) crosses scroll + session boundaries. Keep it in
  `TerminalSessionController` (the writer); expose a read-only view to
  scroll.
- Most-touched file in the repo and **no automated tests** — relies on
  the manual checklist + on-device smoke (no local Flutter). Plan for a
  quiet week or coordinate a freeze.

**Phasing** — five sub-PRs, ascending risk: R2T.1 Lifecycle (1d) →
R2T.2 Transfer (1.5d) → R2T.3 Scroll (2d) → R2T.4 Input (2d) →
R2T.5 Session (3d). After all five, `terminal_screen.dart` is ~700 LOC
of `build()` + glue.

**Manual regression checklist:** connect/attach/see buffer · type/see
output · pinch-zoom/swipe-panes/two-finger · snippet picker · profile
sheet · background 30s then foreground (still attached) · raw-mode
toggle · image upload · file download · 10s network loss/reconnect ·
scroll-to-top/bottom + bottom-anchor follows · vi/vim exit sane ·
new/kill window, kill last pane → disconnect.

**Acceptance:** `terminal_screen.dart` ≤ 800 LOC; no controller > 800 LOC;
all five sub-PRs ship; `flutter analyze` zero new warnings.

**Estimated effort:** 8–10 days.

---

## R3 — 1k-LOC screen sweep (parallelizable)

**Scope:** the remaining >1k-LOC files after R1/R2A/R2T retire the top
three. Current list at v1.0.728 (excluding the R1/R2 targets and the
intentional `ansi_text_view.dart`):

| File | LOC | Bucket |
|---|---:|---|
| `projects_screen.dart` | 2,944 | A — list+filter |
| `sessions_screen.dart` | 2,731 | A — list+detail-sheet (NEW) |
| `settings_screen.dart` | 2,629 | B — sectioned settings |
| `project_detail_screen.dart` | 1,998 | A — list+filter+detail-sheet |
| `runs_screen.dart` | 1,928 | A — list+filter+detail-sheet |
| `steward_overlay_chat.dart` | 1,635 | A — message list (NEW) |
| `connection_form_screen.dart` | 1,562 | B — sectioned form |
| `structured_deliverable_viewer.dart` | 1,413 | C — render (NEW) |
| `approval_detail_screen.dart` | 1,405 | B — sectioned detail (NEW) |
| `connections_screen.dart` | 1,329 | A — list+filter |
| `templates_screen.dart` | 1,326 | A — list+CRUD (NEW) |
| `snippets_screen.dart` | 1,271 | A — list+CRUD |
| `shortcut_tile_strip.dart` | 1,207 | A — tile list (NEW) |
| `snippet_picker_sheet.dart` | 1,180 | D — sheet |
| `me_screen.dart` | 1,128 | B — sectioned (NEW) |
| `project_create_sheet.dart` | 1,123 | D — sheet |
| `research_phase_heroes.dart` | 1,050 | A — widget cluster (NEW) |
| `action_bar_settings_screen.dart` | 1,029 | B — sectioned settings |

**Buckets define the recipe:**

- **A (list+filter+rows):** extract row widgets to sibling files
  (`_RunRow` → `runs/widgets/run_row.dart`), extract filter chip bar +
  empty state. Target ≤ 600 LOC.
- **B (sectioned forms/settings):** extract each section to a sibling
  (`_AuthSection`, `_JumpHostSection` …); keep validation + the single
  `GlobalKey<FormState>` in the screen. Target ≤ 400 LOC of composition.
- **C (render):** `structured_deliverable_viewer.dart` and
  `ansi_text_view.dart` are render-shaped; don't split for size —
  profile, document hot paths, extract only pure-data helpers.
- **D (sheets):** same as A; convert inline `StatefulBuilder` state to a
  real `StatefulWidget` first if needed.

**Priority order** (stop after the first 5 unless time permits):

1. `projects_screen.dart` (2,944 → ~600) — heart of the IA, now #1 here.
2. `sessions_screen.dart` (2,731 → ~700) — new + large.
3. `settings_screen.dart` (2,629 → ~400 + sections) — least risky.
4. `project_detail_screen.dart` (1,998 → ~600) — most-edited project surface.
5. `runs_screen.dart` (1,928 → ~700).

**Acceptance per file:** ≤ target LOC; no new dependencies; existing
tests pass; manual smoke of the headline interaction.

**Estimated effort:** 1–1.5 days per priority file → 6–8 days for the
top 5; the remaining ~13 are opportunistic (and the R0 ratchet keeps
them from regressing).

---

## R4 — opportunistic remainder

Incremental cleanup as files are touched, not a dedicated wedge:
- `withOpacity` → `withValues(alpha:)` when the file is touched.
- Delete stale l10n keys (`vaultLegacy*`) next time `app_en.arb` is edited.
- Stale tab keys (`tabHub`, `tabServers`, `tabSnippets`, `tabKeys`).
- Role-bound strings per `docs/reference/vocabulary.md` (the canonical
  vocab doc — note: the original location `docs/vocabulary.md` no longer
  exists, the doc moved under `reference/`).

---

## R5 — Go hub companion plan (separate doc)

Out of scope for the Flutter wedges, but the hub has grown its own
monoliths since this doc was written and they should not be ignored:

| File | LOC |
|---|---:|
| `hub/internal/server/seed_demo_lifecycle.go` | 2,893 |
| `hub/internal/hostrunner/driver_acp.go` | 1,861 |
| `hub/internal/hostrunner/driver_appserver.go` | 1,834 |
| `hub/internal/server/handlers_agents.go` | 1,670 |
| `hub/internal/server/handlers_attention.go` | 1,214 |
| `hub/internal/server/handlers_insights.go` | 1,125 |

These want a Go-side companion plan (the cleavage shape differs — Go
splits along handler/driver responsibility, not widget tree). Flagged
here so the R0 tripwire's Go analogue (a `*.go` LOC ceiling) lands in
the same spirit. Spin out a dedicated discussion when the Flutter wedges
have momentum.

---

## Wedge ledger (for tracking)

| ID | Wedge | Target | Days | Status |
|---|---|---|---:|---|
| R0 | CI LOC tripwire (staged ceiling + ratchet) | ceiling 6,300→1,500 | 0.5 | not started |
| R1 | hub_client → sub-clients + facade | 3,571 → 350 + clients | 3–4 | not started |
| R2A.1 | agent_feed: feed_misc + telemetry_strip | -825 | 1 | not started |
| R2A.2 | agent_feed: tool_renderers | -600 | 1.5 | not started |
| R2A.3 | agent_feed: approval + interaction cards | -1,170 | 2 | not started |
| R2A.4 | agent_feed: event_card | -1,140 | 2 | not started |
| R2A.5 | agent_feed: feed_reducer (pure, tested) | -400 | 3 | not started |
| R2T.1 | TerminalLifecycleController | -250 | 1 | not started |
| R2T.2 | TerminalTransferController | -400 | 1.5 | not started |
| R2T.3 | TerminalScrollController | -500 | 2 | not started |
| R2T.4 | TerminalInputController | -600 | 2 | not started |
| R2T.5 | TerminalSessionController | -700 | 3 | not started |
| R3.1 | projects_screen split | 2,944 → 600 | 1.5 | not started |
| R3.2 | sessions_screen split | 2,731 → 700 | 1.5 | not started |
| R3.3 | settings_screen split | 2,629 → 400 + sections | 1 | not started |
| R3.4 | project_detail_screen split | 1,998 → 600 | 1.5 | not started |
| R3.5 | runs_screen split | 1,928 → 700 | 1.5 | not started |

**Total budget:** ~32 days of focused work. R2A and R3 parallelize
across contributors; R0 + R1 should land first and serially.

---

## Open questions

1. **Tests as part of refactor, or after?** The Dart test ratio (~9% of
   `lib/` LOC) is low. Adding tests *during* R1 (HubClient is pure logic,
   easy to test) and R2A.5 (the feed reducer — already has a contract
   test to extend) is the safest path and the highest leverage; skip
   test-writing for the pure-presentation moves to keep them fast.

2. **Coordinate with autonomous-loop work?** R-wedges are explicitly
   *non-feature*. Either pause the loop during R-phases, or scope it to
   surfaces not under refactor.

3. ~~**CI guards.**~~ **Resolved → promoted to R0.** The original
   deferral of this guard is the proximate cause of the drift documented
   above; it is now the first wedge, with a staged-ceiling ratchet so it
   can land before the refactor without turning CI red.

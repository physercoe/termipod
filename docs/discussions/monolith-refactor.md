# Monolith refactor plan — terminal_screen, hub_client, 1k-LOC screens

**Status:** Planning doc, post-demo. Tech-debt items #1, #2, #4 from
internal audit (2026-04-25). Goal: shrink the four largest seams in the
codebase without changing user-facing behavior.

**Why now is also why later:** these files block contributor velocity and
hide bugs, but they don't block the P4 research demo. Sequencing this
plan after the demo means the refactor can fail safely — pre-demo, a
behavioral regression in `terminal_screen.dart` would be catastrophic.

---

## Constraints & non-goals

**Constraints:**
- No behavior change. Every refactor wedge must be a pure rearrangement
  with green CI on `flutter analyze` + `flutter test` + Android/iOS
  release builds, plus a manual smoke test of the affected surface.
- No new dependencies. Riverpod + the existing widget patterns are
  sufficient.
- One refactor wedge per PR. Don't bundle.
- Each wedge ≤ 3 days of focused work; if it grows, split it.

**Non-goals:**
- Not rewriting or redesigning any screen. The IA redesign already
  shipped (wedges 1–7) and the surfaces are correct.
- Not changing the public API of `HubClient` for callers (composition
  facade approach — see R1).
- Not chasing the long tail of 600–1000 LOC files; only the >1k violators.
- Not adding tests *as* the refactor — that's tech debt #3, scheduled
  separately. But existing tests must stay green.

---

## Sequencing

```
R1  hub_client split        (3–4 days, low risk, sets pattern)
        ↓
R2  terminal_screen split   (8–10 days, high risk, biggest payoff)
        ↓
R3  screen sweep            (5–7 days, parallelizable, medium risk)
        ↓
R4  remainder               (opportunistic, no fixed budget)
```

R1 first because: lowest risk, no UI surface area, establishes the
composition-facade pattern that R3 will reuse for screens. R2 second
because it's the biggest payoff but needs the team to be in
refactor-mode. R3 last because it's the most parallelizable — once
R1+R2 land, multiple contributors can pick screens independently.

---

## R1 — `hub_client.dart` split (1,902 LOC → ~250 LOC facade + 9 entity clients)

**Scope:** `lib/services/hub/hub_client.dart` (1,902 LOC, 121 `Future`
methods, single class).

**Cleavage by entity (audit of method names):**

| Sub-client | Roughly | Methods |
|---|---:|---|
| `HubAuthClient` | ~80 LOC | `getInfo`, `verifyAuth`, plus token plumbing currently in `_open` |
| `HubProjectsClient` | ~280 LOC | `listProjects/Cached`, `createProject`, `updateProject`, `archiveProject`, project-channel events |
| `HubAgentsClient` | ~250 LOC | `listAgents/Cached`, `getAgent`, `spawnAgent`, `terminateAgent`, `archiveAgent`, `listSpawns/Cached` |
| `HubPlansClient` | ~220 LOC | plans + plan steps + tasks (`listTasks/Cached`, `patchTask`, `createTask`, plan CRUD) |
| `HubChannelsClient` | ~180 LOC | `listChannels/Cached`, `listTeamChannels/Cached`, `createTeamChannel`, `createChannel`, post/list events |
| `HubHostsClient` | ~120 LOC | `listHosts/Cached`, host registration, runner deploy |
| `HubRunsClient` | ~150 LOC | runs, schedules, firings |
| `HubArtifactsClient` | ~120 LOC | outputs, blobs, documents, templates |
| `HubAuditClient` | ~80 LOC | `listAttention/Cached`, `decide`, audit events |
| `HubPolicyClient` | ~60 LOC | `getPolicy`, `putPolicy`, tokens |
| **Shared core** | ~250 LOC | `_open`, `_get/_post/_patch/_put/_delete`, `_readJson`, error mapping, snapshot-cache invalidation |

**Approach: composition facade.**

```dart
// lib/services/hub/hub_client.dart — slimmed to ~250 LOC
class HubClient {
  HubClient({required HubAuth auth, ...}) :
    _core = HubHttpCore(auth: auth, ...) {
    projects  = HubProjectsClient(_core);
    agents    = HubAgentsClient(_core);
    plans     = HubPlansClient(_core);
    channels  = HubChannelsClient(_core);
    hosts     = HubHostsClient(_core);
    runs      = HubRunsClient(_core);
    artifacts = HubArtifactsClient(_core);
    audit     = HubAuditClient(_core);
    policy    = HubPolicyClient(_core);
  }

  late final HubProjectsClient projects;
  late final HubAgentsClient   agents;
  // …
}
```

**Migration path for callers:** keep the old method signatures as
delegating shims in `HubClient` for two releases, then delete:

```dart
// HubClient
@Deprecated('Use client.projects.list() instead')
Future<List<Map<String,dynamic>>> listProjects({bool? isTemplate}) =>
    projects.list(isTemplate: isTemplate);
```

This means **the refactor can ship as one PR** without a coordinated
cutover across 50+ call sites. Migration of call sites then becomes a
background task.

**Risks:**
- Snapshot-cache invalidation logic crosses entity boundaries (creating
  a project invalidates the projects list cache, which is fine, but
  posting a channel event invalidates *that project's* channels —
  cross-client coupling). Mitigation: invalidation lives on
  `HubHttpCore.invalidate(prefix)`, called by sub-clients. Already the
  shape today.
- Tests at `test/hub_client_*` will need import updates. ~5 files.

**Acceptance:**
- `hub_client.dart` ≤ 300 LOC.
- No sub-client > 350 LOC.
- All existing call sites compile unchanged (via delegating shims).
- Existing hub_client tests pass.
- Manual smoke: list projects, create project, post message, view
  attention.

**Estimated effort:** 3 days (1 day extraction, 1 day test fixup,
1 day manual verification + slack).

---

## R2 — `terminal_screen.dart` split (4,532 LOC → ~600 LOC widget + 5 controllers)

**Scope:** `lib/screens/terminal/terminal_screen.dart`. Single
`_TerminalScreenState` owns ~50 methods covering 7 distinct concerns.

**Cleavage by concern (verified against the method list):**

| Controller | LOC | Owns |
|---|---:|---|
| `TerminalSessionController` | ~700 | SSH connect/reconnect/disconnect, tmux session/window/pane lifecycle, `_pausePolling`, `_resumePolling`, `_startStaleWatchdog`, `_onBackendHeartbeat`, `_onBackendContentUpdate`, the session/window/pane selectors and create/kill dialogs |
| `TerminalScrollController` | ~500 | `_onTerminalScroll`, `_maybeResetScrollbackAtBottom`, `_maybeExtendScrollback`, `_buildScrollPositionIndicator`, `_buildScrollLineCounter`, bottom-anchor logic (the recent v1.0.14–17 work) |
| `TerminalInputController` | ~600 | `_dispatchSpecialKey`, `_dispatchKey`, `_handleKeyInput`, `_handleTwoFingerSwipe`, `_toggleGestureMode`, `_showInsertMenu`, `_showSnippetPicker`, `_showProfileSheet` |
| `TerminalLifecycleController` | ~250 | `didChangeAppLifecycleState`, `didChangeMetrics`, `_applyKeepScreenOn`, foreground task wiring, connectivity monitoring |
| `TerminalTransferController` | ~400 | `_ensureImageTransferListener`, `_handleImageTransfer`, `_ensureFileTransferListener`, `_handleFileTransfer`, `_handleFileDownload` |
| `TerminalScreen` (slim) | ~600 | `build()`, top-level layout, AppBar, breadcrumb header, error overlay, raw-mode header, hand-off to controllers |

**Approach: state controllers as Riverpod `Notifier`s scoped to the
terminal screen.**

```dart
// One family-keyed provider per controller, scoped to (connectionId, sessionName).
// Auto-disposes when the terminal screen pops.

final terminalSessionControllerProvider = AutoDisposeNotifierProviderFamily<
    TerminalSessionController, TerminalSessionState, TerminalKey>(
  TerminalSessionController.new,
);
```

**The widget tree calls into controllers, not the other way around.**
This is critical — we don't want controllers holding `BuildContext`
references. UI affordances (snackbars, dialogs) stay in the widget; the
controller exposes streams/state and the widget reacts.

**Risks:**
- The current state has implicit ordering between concerns (e.g.
  resume-polling fires after metrics change after lifecycle resume). A
  refactor that splits these could reorder them subtly. Mitigation: each
  controller exposes a Stream<Event>, the widget orchestrates the
  ordering it needs. Make the ordering explicit before the split.
- `_applyBufferedUpdate` / `_scheduleUpdate` / `_applyUpdate` cross
  scroll + session boundaries. They're the buffer that the SSH stream
  writes into and the scroll position reads from. Keep them in
  `TerminalSessionController` (the writer), expose a read-only view to
  `TerminalScrollController`.
- `terminal_screen.dart` is the most-touched file in the repo. Plan the
  refactor for a quiet week or coordinate a freeze.
- No automated tests. Need a manual regression checklist (see below).

**Phasing within R2** — don't try to extract all five controllers in one
PR. Five sub-PRs:

1. **R2.1 Lifecycle** (1d, lowest risk) — Pull out `TerminalLifecycleController`. Smallest blast radius; touches only resume/pause/wakelock/connectivity.
2. **R2.2 Transfer** (1.5d) — Pull out `TerminalTransferController`. Image and file transfer are self-contained.
3. **R2.3 Scroll** (2d) — Pull out `TerminalScrollController`. The scroll-anchor logic recently churned (v1.0.14–17), so it's well-understood. Carry the buffered-update read path.
4. **R2.4 Input** (2d) — Pull out `TerminalInputController`. Input routing + special keys + insert menu + snippet picker.
5. **R2.5 Session** (3d) — The remainder becomes `TerminalSessionController`. By this point the file is already half its size, so this is mostly renaming and removing dead local state.

After all five: `terminal_screen.dart` is ~600 LOC of `build()` + glue.

**Manual regression checklist** (each sub-PR runs it):

- [ ] Connect to a host, attach to tmux, see initial buffer
- [ ] Type a command, see output
- [ ] Pinch to zoom, swipe to switch panes, two-finger swipe
- [ ] Open snippet picker, send a snippet
- [ ] Open profile sheet, switch profile
- [ ] Background the app for 30s, foreground — terminal still attached
- [ ] Toggle raw mode, send keys, exit raw
- [ ] Image upload from gallery
- [ ] File download to Documents
- [ ] Lose network for 10s, watch reconnect
- [ ] Scroll to top, scroll to bottom, bottom-anchor follows new output
- [ ] Open vi/vim, exit — scroll position and bottom-anchor sane
- [ ] Create new window, kill window, kill last pane → disconnect

**Acceptance:**
- `terminal_screen.dart` ≤ 700 LOC.
- No controller > 800 LOC.
- All five sub-PRs ship, manual checklist clean each time.
- `flutter analyze` zero new warnings.

**Estimated effort:** 8–10 days end-to-end (5 sub-PRs × 1.5–2 days).

---

## R3 — 1k-LOC screen sweep (~5–7 days, parallelizable)

**Scope:** the 12 remaining 1k-LOC files (after R1 retires hub_client and
R2 retires terminal_screen):

| File | LOC | Bucket |
|---|---:|---|
| `runs_screen.dart` | 1928 | A — list+filter+detail-sheet |
| `settings_screen.dart` | 1801 | B — sectioned settings |
| `project_detail_screen.dart` | 1749 | A — list+filter+detail-sheet |
| `projects_screen.dart` | 1644 | A — list+filter |
| `connection_form_screen.dart` | 1562 | B — sectioned form |
| `ansi_text_view.dart` | 1522 | C — render perf |
| `connections_screen.dart` | 1443 | A — list+filter |
| `agent_feed.dart` | 1295 | A — list+filter+row variants |
| `snippets_screen.dart` | 1264 | A — list+CRUD |
| `snippet_picker_sheet.dart` | 1177 | D — sheet |
| `project_create_sheet.dart` | 1104 | D — sheet |
| `action_bar_settings_screen.dart` | 1029 | B — sectioned settings |

**Buckets define refactor recipe:**

- **A (list+filter+rows):** Extract row widgets to sibling files
  (`_RunRow` → `runs/widgets/run_row.dart`). Extract filter chip bar.
  Extract empty state. Target screen file: ≤ 600 LOC.
- **B (sectioned forms/settings):** Extract each section to a sibling
  file (`_AuthSection`, `_JumpHostSection`, `_ProxySection` …). Target
  main file: ≤ 400 LOC of section composition.
- **C (render perf):** `ansi_text_view.dart` is different — it's the
  actual ANSI parser + xterm bridge, performance-sensitive. Don't split
  for size; instead profile, document hot paths, and only extract
  pure-data helpers. Target: stay 1500 LOC, *with comments* explaining
  why each section is shaped the way it is.
- **D (sheets):** Same recipe as A — extract row widgets, filter,
  empty state. Sheets are smaller windows but follow the same shape.

**Priority order** (stop after the first 5 unless time permits):

1. **`project_detail_screen.dart`** (1749 → ~600) — most-edited file in
   the project surface; payoff is biggest.
2. **`runs_screen.dart`** (1928 → ~700) — second most-edited.
3. **`projects_screen.dart`** (1644 → ~600) — heart of the IA.
4. **`settings_screen.dart`** (1801 → ~400 + sections) — least risky;
   pure list of switches/dialogs.
5. **`connection_form_screen.dart`** (1562 → ~400 + sections) — high
   risk (auth correctness) but the section shape is obvious.

After 5: pause, evaluate whether the others still bother the team. They
may not — having shrunk the top of the list, the next worst file is
`agent_feed.dart` at 1295, which is borderline-fine.

**Risks per bucket:**
- **A**: row widgets often access screen-level state (filter, selected,
  callbacks). Pass them as constructor args; don't introduce inherited
  widgets unless 4+ consumers exist.
- **B**: sectioned forms share a single `GlobalKey<FormState>`. Keep
  validation in the screen; sections are presentation only.
- **C**: don't.
- **D**: sheets often inline their state (`StatefulBuilder`). Convert to
  a real `StatefulWidget` first if needed.

**Acceptance per file:**
- File ≤ target LOC.
- No new dependencies.
- Existing tests pass.
- Manual smoke: open the screen, exercise the headline interaction.

**Estimated effort:** 1–1.5 days per priority-1 file → 5–7 days total
for the top 5. The remaining 7 are opportunistic.

---

## R4 — opportunistic remainder

After R1+R2+R3, the codebase has no >1k-LOC violations except possibly
`ansi_text_view.dart` (intentional). The remainder of the audit
(seed_demo.go, force-unwraps, vocab debt, stale l10n) is best handled
incrementally as files are touched, not as a dedicated wedge.

The vocab audit (`docs/vocabulary.md`) already specifies this for
role-bound strings. Apply the same principle to:
- `withOpacity` → `withValues(alpha:)` when the file is touched.
- `vaultLegacy*` l10n keys — delete next time `app_en.arb` is edited.
- Stale tab keys (`tabHub`, `tabServers`, `tabSnippets`, `tabKeys`) —
  same.

---

## Wedge ledger (for tracking)

| ID | Wedge | LOC delta target | Days | Owner | Status |
|---|---|---|---:|---|---|
| R1 | hub_client → 9 sub-clients + facade | 1902 → 250 + 9×~200 | 3 | TBD | not started |
| R2.1 | TerminalLifecycleController extracted | -250 | 1 | TBD | not started |
| R2.2 | TerminalTransferController extracted | -400 | 1.5 | TBD | not started |
| R2.3 | TerminalScrollController extracted | -500 | 2 | TBD | not started |
| R2.4 | TerminalInputController extracted | -600 | 2 | TBD | not started |
| R2.5 | TerminalSessionController extracted | -700 | 3 | TBD | not started |
| R3.1 | project_detail_screen split | 1749 → 600 | 1.5 | TBD | not started |
| R3.2 | runs_screen split | 1928 → 700 | 1.5 | TBD | not started |
| R3.3 | projects_screen split | 1644 → 600 | 1.5 | TBD | not started |
| R3.4 | settings_screen split | 1801 → 400 + sections | 1 | TBD | not started |
| R3.5 | connection_form_screen split | 1562 → 400 + sections | 1.5 | TBD | not started |

**Total budget:** ~21 days. With a single contributor, a calendar
month. With two, three weeks. With the team, R3 parallelizes, so two
calendar weeks.

---

## Open questions

1. **Tests as part of refactor, or after?** Audit item #3 (test ratio
   6.3%) is real. Adding tests *during* refactor is the safest path
   for R2 (terminal). But it doubles the timeline. Recommendation: add
   service-layer tests during R1 (HubClient is pure logic, easy to
   test); skip tests for R2/R3 to keep them moving, schedule a separate
   "service test pass" wedge after.

2. **Coordinate with autonomous-loop work?** The autonomous loop ships
   feature wedges in `feedback_wedge_size.md`. R1–R3 are explicitly
   *non-feature*. Either pause the loop during R-phases, or scope the
   loop to surfaces not under refactor (the steward/agent layer is
   safe; project_detail and runs are not).

3. **CI guards.** Worth adding a CI rule: fail the build if any file in
   `lib/` exceeds 1500 LOC. Catches regression after R3 ships. Lightweight.

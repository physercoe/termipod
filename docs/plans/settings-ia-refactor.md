# Settings + TeamSwitcher IA refactor — implementation plan

> **Type:** plan
> **Status:** Draft (2026-05-14) — director-aligned, ready to implement
> **Audience:** contributors
> **Last verified vs code:** v1.0.579

**TL;DR.** Implementation tracker for the IA proposed in
[`docs/discussions/settings-and-team-scope-ia.md`](../discussions/settings-and-team-scope-ia.md).
Two wedges. **W1 (v1.0.580) = structural refactor**: the
1870-line flat ListView in `settings_screen.dart` becomes a
6-row home + 6 sub-screens, voice rows fold into Input, Image +
File transfer merge under "Files & Media", "Toolbar Profile"
renames to "Preset", and the "You" category is dropped (Language
→ Display, Feedback already in About, Local notifications →
Display). **W2 (v1.0.581) = discoverability layer**: search bar
on Settings home with 600ms tint-pulse deep-link, sub-label
"current value" on each home row, plus TeamSwitcher popup
section label + tooltip copy + first-run nudge so users learn
the two-scope mental model on first encounter. No ADR — this is
presentation polish, not load-bearing infrastructure. A third
wedge (~v1.0.582, tracked separately) handles the Templates
list chrome (tab + chip filters); out of scope here.

---

## 1. Goal

After this plan:

- Settings home presents **6 category cards** (≤ Miller's budget),
  each with a sub-label showing the current value where
  applicable. Two-tier max, except dense areas (NavPad, Voice,
  Action-bar) get their own sub-sub-page per OQ-1.
- Voice setup is reachable in two taps from Settings → Input →
  Voice, no longer a hidden standalone route.
- Image + File transfer share a parent (Files & Media) so the
  duplicate rows (path format / auto-enter / bracketed paste)
  surface once.
- The word "Profile" is no longer overloaded across hub vs
  toolbar — toolbar's becomes "Preset".
- Search on Settings home jumps directly to a destination
  sub-screen and flashes the matched row for 600ms.
- TeamSwitcher pill makes its scope explicit: a section label
  inside the popup names "team-shared" items; tooltip copy
  describes the device-vs-team split; a first-run nudge fires
  when a Settings search misses on a team-flavored query.

## 2. Non-goals

- **Templates list chrome.** Out of scope; tracked as a separate
  plan (~v1.0.582).
- **Pinned / Recent row on Settings home.** Deferred per
  discussion §6 OQ-4. Ship the 6-category taxonomy first; measure
  whether search alone is enough before adding state.
- **TeamSwitcher structural refactor.** The pill is already
  correctly scoped; only label/tooltip/nudge changes here.
- **NavPad drag-reorder.** Tracked separately
  (`project_todo_drag_reorder_buttons`); the IA wedge doesn't
  touch button customization beyond moving NavPad into Input.
- **Light-theme parity.** Existing dark-first colour pins stay
  per `project_light_theme_parity`. The new sub-screens use the
  same `DesignColors.*` constants as the current settings_screen.
- **Backend / data-model changes.** Pure presentation refactor.
  `SettingsState`, `settingsProvider`, secure_storage layout —
  all untouched.

## 3. Vocabulary

- **Settings home** — the new 6-row landing screen replacing the
  flat ListView at `lib/screens/settings/settings_screen.dart`.
- **Category** — one of the 6 top-level rows on Settings home
  (Display, Input, Files & Media, Data, Advanced, About).
- **Sub-screen** — the page reached by tapping a category. Lists
  ≤ 7 grouped rows, may include navigators into sub-sub-pages.
- **Sub-sub-page** — dense detail page reached from a sub-screen
  (NavPad, Voice, Action-bar preset, Image transfer, File
  transfer). Three tap-depths from home; reserved for areas
  with ≥ 4 controls.
- **Preset (toolbar)** — was "Profile" in the action-bar context.
  Rename only; semantics unchanged.
- **Current-value sub-label** — the right-aligned secondary text
  on each home row showing the current setting (`Theme · Dark`,
  `Voice · Off`).

## 4. Surfaces affected

| Surface | Change | Wedge |
|---|---|---|
| `lib/screens/settings/settings_screen.dart` | Rewrite from flat ListView to 6-row home; spawn 6 sub-screen widgets | W1 |
| `lib/screens/settings/display_screen.dart` (new) | Theme · Terminal cursor · Font family/size/min · Scrollback · Language · Local notifications | W1 |
| `lib/screens/settings/input_screen.dart` (new) | NavPad → · Custom keyboard · Action-bar preset → · Voice → · Haptic · Invert pane nav · Keep screen on | W1 |
| `lib/screens/settings/files_media_screen.dart` (new) | Image transfer → · File transfer → · Shared knobs (auto-enter, bracketed paste) | W1 |
| `lib/screens/settings/data_screen.dart` (new) | Export · Import · Clear cache · Browse files · Vault (legacy) | W1 |
| `lib/screens/settings/advanced_screen.dart` (new) | Experimental floating pad (size, center key) | W1 |
| `lib/screens/settings/about_screen.dart` (new) | Version · Check update · Source code · Feedback · Licenses · App icon | W1 |
| `lib/screens/settings/voice_settings_screen.dart` | Keep file; reachable via Input → Voice → (existing route preserved) | W1 (no diff) |
| `lib/screens/settings/action_bar_settings_screen.dart` | Rename internal references from "Profile" to "Preset"; reachable via Input → Action-bar preset → | W1 |
| `lib/screens/settings/sub_screens/navpad_screen.dart` (new) | NavPad's 6 controls extracted from current ListView | W1 |
| `lib/screens/settings/sub_screens/image_transfer_screen.dart` (new) | Image-transfer's 7 rows extracted | W1 |
| `lib/screens/settings/sub_screens/file_transfer_screen.dart` (new) | File-transfer's 5 rows extracted | W1 |
| `lib/l10n/app_en.arb` (+ siblings) | Rename `sectionTerminal`→`scopeDisplay`, `sectionToolbar`→`scopePreset`, etc.; new keys for sub-labels; new `settingsSearchHint`, `searchNudgeTeam` | W1 + W2 |
| `lib/widgets/settings_search_bar.dart` (new) | Persistent search bar at top of home; substring match against an indexed `(title, subtitle, route)` list | W2 |
| `lib/widgets/settings_row_highlight.dart` (new) | 600ms tint-pulse animation wrapper used on the deep-link target | W2 |
| `lib/widgets/team_switcher.dart` | Add "On this team" section label above Templates/Team settings; bump tooltip copy | W2 |
| `lib/widgets/team_switcher_nudge.dart` (new) | One-shot dismissible chip surfaced when Settings search misses on team-flavored queries | W2 |
| `lib/providers/settings_provider.dart` | Add `searchNudgeDismissed` flag (per-device, prefs-backed) | W2 |
| `docs/how-to/release-testing.md` | New scenario §X — verify the 6-category nav + search highlight + team-switcher nudge | W2 |
| `pubspec.yaml` | +1 per wedge (1.0.580, 1.0.581) | both |

## 5. Wedges

### W1 (v1.0.580). Structural settings refactor.

One commit. The carrier of all "data moved" changes. After W1
ships, the IA is correct on the surface; W2 layers discoverability
on top.

**5.1 Six-category home.**

Replace the flat ListView body of `settings_screen.dart` with a
single Column of 6 large category cards. Each card:

- Leading icon (Material rounded outline, theme-aware tint)
- Title (l10n key)
- Sub-label: the current value of the most representative setting
  in that category — `Theme · Dark` for Display, `Voice · Off`
  for Input, etc.
- Trailing chevron

Card order is fixed (no user reordering in v1):

1. **Display** — Theme · Terminal cursor · Font family · Font
   size · Min font size · Scrollback · Language · Local
   notifications
2. **Input** — NavPad → · Custom keyboard · Action-bar preset →
   · Voice → · Haptic feedback · Invert pane nav · Keep screen on
3. **Files & Media** — Image transfer → · File transfer → ·
   Auto-enter on paste · Bracketed paste
4. **Data** — Export backup · Import backup · Clear offline
   cache · Browse local files · Vault (legacy)
5. **Advanced** — Experimental floating pad → (size, center key)
6. **About** — Version · Check update · Source code · Feedback ·
   Licenses · App icon

**5.2 Drop "You", redistribute three rows.**

Per director directive (2026-05-14):

- **Language** → Display (the language code IS what the app
  *shows*).
- **Local notifications** → Display. Slight scope expansion:
  Display now covers anything the app produces toward the user's
  senses, not strictly visual. Defensible alternative is a 7th
  "System" category — single-row, awkward. Pin in Display for
  v1; revisit only if more system-flavored toggles arrive.
- **Feedback channel** — no move. Already in About in current
  code.

No "You" sub-screen widget is created. The category disappears.

**5.3 Voice moves into Input.**

`voice_settings_screen.dart` is preserved as-is — it stays at the
same route. The change is that Input's sub-screen has a `Voice →`
navigator row at the top of the input-modality cluster (NavPad,
Custom keyboard, Action-bar preset, Voice). Current-value
sub-label reads `Voice · On` or `Voice · Off`. Three-tier path:
Settings → Input → Voice.

Rationale per
[discussion §3 / Q2](../discussions/settings-and-team-scope-ia.md#q2--should-voice-be-in-input):
voice is an input modality; the toggle + DashScope credentials
belong together; iOS keeps Keyboard + Dictation together.

**5.4 Image + File transfer merge into Files & Media.**

Today they're two top-level sections with overlapping rows
(`remote path`, `auto-enter`, `bracketed paste`, `path format`).
Per OQ-3 recommendation:

- New parent sub-screen `Files & Media`.
- Two navigator rows inside: `Image transfer →` and
  `File transfer →` → each goes to its own sub-sub-page.
- Two shared toggles at the bottom of Files & Media: `Auto-enter
  on paste`, `Bracketed paste`. These previously appeared in
  BOTH sections (duplicate rows); now they live once and apply
  to both transfer types.

**5.5 Rename Toolbar "Profile" → "Preset".**

The word "Profile" is overloaded across:
- Hub profile (TeamSwitcher) — which hub + team you're connected
  to
- Member profile (Team Settings)
- Toolbar profile (Settings → Input → Action-bar preset) —
  which set of toolbar buttons shows

Rename the toolbar variant to "Preset" everywhere:
- l10n strings: `activeProfile`→`activeToolbarPreset`,
  `addNewProfile`→`addNewToolbarPreset`,
  `addNewProfileDesc`→`addNewToolbarPresetDesc`,
  `customizeGroups`→`customizeToolbarPreset` (kept similar).
- UI text in `action_bar_settings_screen.dart` and any tab-switcher
  / chip / dropdown that uses the word.

This is a one-direction find-and-replace; no migration needed
since the underlying storage keys are internal names that don't
surface to users.

**5.6 l10n key renames.**

Map every old `sectionFoo` key to its new scope-named equivalent:

| Old | New |
|---|---|
| `sectionTerminal` | `scopeDisplay` (via Display sub-screen) |
| `sectionNavPad` | `scopeInputNavPad` |
| `sectionExperimental` | `scopeAdvanced` |
| `sectionToolbar` | `scopeInputPreset` (was Profile) |
| `sectionBehavior` | (split — Haptic/KeepScreenOn/Invert → Input; Local notif → Display) |
| `sectionAppearance` | (merged into Display) |
| `sectionImageTransfer` | `scopeFilesImageTransfer` |
| `sectionFileTransfer` | `scopeFilesFileTransfer` |
| `sectionData` | `scopeData` |
| `sectionAbout` | `scopeAbout` |

New keys for the 6 category-card titles + ~6 sub-label
current-value formatters. Old keys deleted in the same wedge —
no shim needed since they're internal.

**5.7 Tests.**

- Widget test: Settings home renders 6 cards; tapping each
  navigates to the correct sub-screen.
- Widget test: Input → Voice → navigation lands on
  `voice_settings_screen` without crashing (preserves the
  existing route).
- Widget test: Action-bar preset sub-screen exists and renders
  without "Profile" in any visible string.
- Golden file: snapshot of Settings home in dark theme so future
  changes are visible in diffs.

### W2 (v1.0.581). Discoverability layer.

One commit. Layered on top of W1's structure.

**5.8 Search bar on Settings home.**

Persistent search bar at the top of the home screen (above the 6
cards). Implementation:

- Build a static index at app start: walk the 6 sub-screens, the
  3 sub-sub-pages, and extract every row's `(title, subtitle,
  route, anchor_id)` tuple. ~50 entries total.
- Substring match (case-insensitive) against title AND subtitle
  per OQ-2.
- Result list appears as a Material `SearchAnchor` overlay; tap
  a result → `Navigator.push(route)` to the destination
  sub-screen with the matched row's `anchor_id` carried in route
  args.
- Destination sub-screen reads `anchor_id` from `ModalRoute`'s
  arguments; if set, scrolls the matched row into view AND fires
  the highlight pulse (next item).

**5.9 600ms tint-pulse on deep-link target.**

New widget `_RowHighlight` wraps any `ListTile`/`SwitchListTile`
in a tween-animated background. When the destination sub-screen
receives an `anchor_id` arg, the matched row's background
animates `colorScheme.primaryContainer @ 0.35` → 0 over 600ms
(Material Motion convention). Single-fire; on second visit
without an anchor, the wrapper is a passthrough.

Animation timing: `Curves.easeOut`, 600ms total. Honors the
device's reduced-motion accessibility setting (fall back to
static 200ms tint that fades over 1500ms).

**5.10 Sub-label "current value" on home rows.**

Wire the sub-label slot on each category card to a derived
string from `settingsProvider`:

- Display → `Theme · Dark` (or Light)
- Input → `Voice · Off` (most user-relevant; could also be
  `Custom keyboard · On` — pick one)
- Files & Media → `Image format · JPEG` (highest-frequency
  setting in that scope)
- Data → (no sub-label — no single representative value)
- Advanced → `Floating pad · Off` (only when on, otherwise
  blank)
- About → `v1.0.581-alpha`

Convention: when no representative value exists, render no
sub-label rather than a generic count.

**5.11 TeamSwitcher polish.**

Three small changes in `lib/widgets/team_switcher.dart`:

- Add a section label above the bottom items in the popup (after
  the second `PopupMenuDivider`):
  ```dart
  PopupMenuItem(enabled: false, height: 28,
    child: Text('On this team', style: ...semibold-caps...))
  ```
- Update `teamSwitcherTooltip` l10n key copy to:
  *"Switch profile · team-shared settings live in here"*
- (no structural change to the popup items themselves)

**5.12 First-run nudge.**

When the user types in the Settings search bar and the query
matches a known team-flavored term (`members`, `policies`,
`template`, `templates`, `channel`, `channels`, `auth`,
`council`, `budget`) AND no rows match in the local index, show
a one-shot dismissible chip below the search results:

> *"Looking for team-shared settings? Tap the team pill at the
> top."*

Storage: `settingsProvider.searchNudgeDismissed` bool, persisted
to prefs. Once dismissed, never reappears. The chip has a small
× button on the right.

The keyword list is a `const Set<String>` in the search bar
widget — easy to extend, but conservative for v1.

**5.13 Tests + tester docs.**

- Widget test: search "font" → results include the 3 font rows
  from Display; tapping one navigates to Display with the matched
  row highlighted (anchor arg present).
- Widget test: search "members" → zero local results, nudge chip
  appears, dismissing it sets the prefs flag.
- `release-testing.md` gets a new scenario (~§7.8 or §6.X) for
  the IA refactor:
  - Verify 6 cards on Settings home, tap each → correct sub-screen.
  - Verify Voice reachable via Input → Voice; voice toggle works.
  - Verify search hits both title and subtitle text.
  - Verify deep-link highlight pulses on matched row.
  - Verify TeamSwitcher tooltip + section label render.
  - Verify "members" search triggers the nudge chip, dismissing
    persists across app restart.

## 6. Verification

- **Wedge-local tests** as noted (widget tests per surface).
- **On-device smoke** after W1: walk the 6 categories, verify
  every setting from the old flat list is reachable in ≤ 3 taps;
  verify Voice setup works end-to-end from the new Input → Voice
  path.
- **On-device smoke** after W2: type 5 representative queries
  ("font", "voice", "backup", "members", "policies") and verify
  results + highlights / nudge behavior.
- **No regression in stored prefs.** The wedge MUST NOT change
  any `shared_prefs` key the existing `settingsProvider` reads.
  All renames are UI-side only. Tester verifies by upgrading from
  a v1.0.579 build to a v1.0.580 build with existing settings and
  confirming nothing reverts to default.

## 7. Open questions

All seven OQs from
[the discussion doc §6](../discussions/settings-and-team-scope-ia.md#6-open-questions)
are resolved by director sign-off (2026-05-14):

- OQ-1 → 3-tier for dense areas (NavPad, Voice, Action-bar
  preset, Image transfer, File transfer).
- OQ-2 → search indexes title + subtitle.
- OQ-3 → Image + File transfer merge under Files & Media.
- OQ-4 → Pinned / Recent deferred (no W3 in this plan).
- OQ-5 → standalone screens kept; navigated from new sub-pages.
- OQ-6 → l10n rename in W1.
- OQ-7 → TeamSwitcher polish in W2 (same release window as the
  IA refactor user-visibly).

One **minor follow-up to flag**: Local notifications in Display
(§5.2) is a slight scope stretch. If on-device review feels off,
the polish move is to introduce a 7th "Behavior" or "System"
category to host this row alone — but this would be a follow-up
wedge, not part of W1.

## 8. Rollout

- **W1 ships first** as a structural refactor. Even without
  search, the 6-card home + sub-screen navigation is a
  significant ergonomic improvement over the current flat
  scroll. W1-alone is shippable.
- **W2 ships ~1 release later** once W1 has settled. Splitting
  is honest about the two-phase value delivery: W1 is the
  "data moved" change, W2 is the "find things fast" layer. If
  W1 surfaces any IA mistakes (a category that feels misfiled
  on-device), they get caught and fixed before W2's search
  index hardens around them.
- **No coordinated release with Templates list chrome.** That
  third wedge (~v1.0.582) lives in its own plan and ships on
  its own timeline.

## 9. Risks

- **Hidden setting becomes "lost"** for users who memorized the
  old scroll position. Mitigation: W2's search bar gives every
  setting a direct path; the release notes call out the
  redistributed rows (Language, Local notif, Voice path).
- **3-tier paths feel deep** on mobile. Mitigation: search +
  sub-label preview keep most interactions to 1 tap; only
  detail editing requires the full drill-down.
- **l10n breakage** from key renames. Mitigation: rename keys
  in the same wedge that renames their consumers; CI's
  `flutter analyze` catches any orphan references.
- **Display scope feels stretched** by Local notifications.
  Mitigation: §7 OQ-flag. Follow-up wedge if on-device review
  surfaces friction.
- **Search nudge keyword list** drifts from reality (someone
  adds a new team-flavored concept and the nudge stops firing
  on relevant queries). Mitigation: keep the keyword list as a
  short `const Set<String>` close to the team-switcher widget
  so a refactor naturally touches it.

## 10. References

- [Settings + TeamSwitcher IA discussion](../discussions/settings-and-team-scope-ia.md) —
  authoritative for the two-scope mental model and category
  rationale.
- Current code: `lib/screens/settings/settings_screen.dart`
  (1870 lines), `lib/widgets/team_switcher.dart`,
  `lib/screens/team/team_screen.dart`.
- Material Motion — 600ms tween convention for state-change
  highlights.
- iOS Human Interface Guidelines — Settings pattern
  (scope-based ordering).
- ADR-023 — agent-driven mobile UI (broader UI framing this
  fits into; doesn't gate this wedge).

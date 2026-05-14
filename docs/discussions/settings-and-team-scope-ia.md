# Settings + TeamSwitcher information architecture

> **Type:** discussion
> **Status:** Open — director-aligned (2026-05-14), ready for implementation wedge
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.579

**TL;DR.** Termipod has two correctly-scoped configuration trees that
were never explicitly named, plus one of them (Settings) is a
1870-line flat scroll. This doc pins the **two-scope mental model**
(Device vs Team), specifies the **seven-category Settings IA**, and
records the placement rules so future contributors don't re-decide
where a new toggle belongs. The TeamSwitcher pill is already
correctly scoped and needs no structural change — only better
naming and discoverability hints. The Settings refactor is one
wedge (~v1.0.580); a follow-up wedge handles the Templates list
chrome (tab + chip filters).

---

## 1. Problem

Two pain points compound:

- **Hidden split.** "Where do I change X?" depends on whether X is
  device-local (Settings tab) or team-shared (TeamSwitcher pill).
  Neither surface names its scope, so first-time users hunt in
  Settings, find nothing for team-shared concerns, and may not
  discover the pill at all.
- **Flat scroll.** Once inside Settings,
  `lib/screens/settings/settings_screen.dart` is 1870 lines of
  ListView with 10 inline `_SectionHeader` sections (Terminal,
  NavPad, Experimental, Toolbar, Behavior, Appearance,
  ImageTransfer, FileTransfer, Data, About). Plus standalone
  screens for Voice, ActionBar, FileBrowser, Licenses. Users
  scroll through 30+ rows to find one toggle.

Three laws determine "can I find this fast?":

- **Hick's Law** — decision time grows with the log of visible
  choices. Hiding choices behind one categorical tap is almost
  always a win when the count exceeds ~7.
- **Miller's chunking (4±1)** — any single screen should present
  groupings the user can hold in working memory at once.
- **Recognition over recall (Nielsen #6)** — users shouldn't have
  to remember "is *theme* under Appearance or Display?" The label
  and its scope must match the user's mental model of "whose
  behavior am I about to change?"

A fourth principle that matters for mobile but is often
underweighted: **information scent** (Pirolli/Card). Users follow
labels that smell like their goal. "Image transfer" smells like
nothing; "When I send a photo" smells like the goal — but we don't
need to rewrite copy that radically; a few well-placed sublabels
("Theme · Dark") do most of the work.

---

## 2. The two-scope model

```
┌──────────────────────────────────────────┬──────────────────────────────────────────┐
│  DEVICE SCOPE — "this phone"             │  TEAM SCOPE — "this team on this hub"    │
│  Reached via: Settings (cog tab)         │  Reached via: TeamSwitcher pill (AppBar) │
│  Storage: shared_prefs +                 │  Storage: hub DB (governance-controlled) │
│           flutter_secure_storage         │                                          │
│  Survives team switches                  │  Per hub_profile (URL + teamId + token)  │
├──────────────────────────────────────────┼──────────────────────────────────────────┤
│  Terminal cursor / fonts / scrollback    │  Profiles list (active marker)           │
│  Input (NavPad, custom kbd, voice,       │  Add profile · Manage profiles           │
│         action-bar / toolbar, haptic)    │  Templates & engines (overlay editor)    │
│  Theme + language                        │  Team settings →                         │
│  Local notif enable (OS gate)            │    Members · Policies · Channels         │
│  Image / file transfer formats           │    Governance (Budgets · Auth ·          │
│  Data: export / import / cache           │      Councils · Steward defaults)        │
│  About (version / update / source /      │                                          │
│         licenses)                        │                                          │
└──────────────────────────────────────────┴──────────────────────────────────────────┘
```

The split is correct and load-bearing. It mirrors how iOS, Linear,
Figma, and Slack all separate "device prefs" from "workspace
config." This doc does NOT propose collapsing them — it proposes
**naming them** so users know which tree to walk.

### Placement decision rule

> *"Would this setting change if I switched to a different team or
> hub?"*
>
> - **Yes** → it's team-scoped → TeamSwitcher
> - **No** → it's device-scoped → Settings

Apply this rule to every new setting before placement. If the
answer is ambiguous, you've probably found a real overlap (see
§5).

---

## 3. Settings — seven categories

Seven top-level rows. Each opens a sub-screen with ≤ 7 grouped
rows. Two-tier max except where the section is dense enough to
warrant its own sub-sub-page (currently: NavPad, Voice — see §6
open questions).

| # | Category | Rows | Why this category |
|---|---|---|---|
| 1 | **You** | Language · Local notifications · Feedback channel | Identity-flavored device prefs. Small (~3 rows) but high-clarity scope. |
| 2 | **Display** | Theme (dark/light) · Terminal cursor · Terminal font family · Font size · Min font size · Scrollback lines | "How things look on screen" — all visual chrome. |
| 3 | **Input** | NavPad → · Custom keyboard · Action-bar toolbar → · Voice → · Haptic feedback · Invert pane nav · Keep screen on | "How input reaches the app" — any tactile or input modality. **Voice lives here**: it IS an input modality (parallel to NavPad, custom kbd), and keeping the toggle + DashScope credentials together avoids a two-visit setup flow. |
| 4 | **Files & Media** | Image transfer → · File transfer → · Auto-enter on paste · Bracketed paste | Image + File transfer share path-format / auto-enter / bracketed-paste rows today; merge under one parent. |
| 5 | **Data** | Export backup · Import backup · Clear offline cache · Browse local files · Vault (legacy) | Local data lifecycle — backup, restore, wipe, inspect. |
| 6 | **Advanced** | Experimental floating pad · Floating pad size · Floating pad center key · Bracketed paste edge cases | Hidden by default; for power users + bug-hunt. |
| 7 | **About** | Version · Check update · Source code · Feedback · Licenses · App icon | Standard "about this build" — last by convention. |

Total top-level rows: **7** (Miller-budget compliant). Total leaf
toggles redistributed: ~30, same as today — the wedge moves them,
doesn't add or remove.

### Always-visible on the Settings home

- **Search bar at the top.** Power-user escape hatch. Mandatory
  once hierarchy exists; users who already know the label name
  shouldn't pay the taxonomy tax. Substring match across row
  titles AND subtitles; deep-link into the destination sub-screen
  with the row highlighted.
- **Sub-label "current value"** on each top-level row where
  possible (`Theme · Dark`, `Language · English`, `Voice · Off`).
  Converts click-to-verify into glance-to-verify. Cheap.
- **Recent / Pinned row** — *deferred from v1*. See §6 OQ-4.

---

## 4. TeamSwitcher — keep as-is, label better

`lib/widgets/team_switcher.dart` is structurally correct: a
PopupMenuButton in the AppBar that lists profiles (active marker)
+ Add/Manage profiles + Templates & engines + Team settings. The
content maps to TeamScreen (Members/Policies/Channels/Governance)
and its sub-screens (Budgets/Auth/Councils/Steward).

**Recommended changes — discoverability only, no IA changes:**

- **Section label inside the popup**: today the popup has a
  "Profiles" header but no equivalent for the bottom four items.
  Add a section label `On this team` (or similar) above
  "Templates & engines" + "Team settings" so the scope is named
  at the moment of choice.
- **Pill tooltip copy** (`teamSwitcherTooltip` l10n string): bump
  to something like *"Switch team / hub profile · team-shared
  settings live here"* — first-time users see this on long-press
  and learn the convention.
- **First-run hint**: when the user lands on a tab and Settings
  search yields zero results for a likely team-scoped query
  ("members", "policies", "template", "channel"), surface a one-
  line nudge: *"Looking for team-shared settings? Tap the team
  pill at the top."* Single dismissible chip; never reappears
  once dismissed.

The TeamSwitcher does not get a structural refactor in the
Settings wedge. It gets cheap text + one nudge.

---

## 5. The overlap zone

Two narrow overlaps exist between Device and Team scopes. Naming
the convention here so future agents don't re-decide:

### 5.1 Notifications

- **Settings → You → Local notifications**: the OS-level enable
  gate. "TermiPod may show notifications on this device." Local
  toggle, stored in `shared_prefs`. Disabling here disables
  everything regardless of team config.
- **TeamSwitcher → Team settings → Channels**: which events the
  team-side push registration subscribes to (per-channel
  routing). Stored on the hub.

Both rows must sub-label clearly so the user knows which layer
they're touching. Specifically: the Settings row reads "Local
notifications · OS gate"; the team-side surface stays in Channels.

### 5.2 Hub profile credentials

- The bytes (token, server URL) are device-local
  (`flutter_secure_storage`) — they MUST be on the device, not on
  the server.
- The relationships they define (which team I'm operating in) are
  team-scoped.

Convention: profile management (Add / Manage / Delete) lives in
the TeamSwitcher because that's where the user's entry intent
points ("I want to add/change a hub"). Settings does not
duplicate this. The credentials don't appear under Data →
backup/restore either — backup excludes secrets by design.

---

## 6. Open questions

OQ-1 through OQ-7 are decision-blockers for the implementation
wedge. Resolve before writing code.

### OQ-1. Two-tier vs three-tier for dense sub-categories

NavPad has 6 toggles today (mode, dpad style, customize buttons,
repeat rate, haptic, custom keyboard); Voice has 5 (toggle, API
key, region, model, auto-send); Action-bar toolbar has 3.

**Option A (two-tier strict)**: Input page lists all ~14 rows
flat, with NavPad / Voice / Action-bar each rendered as a single
"summary row" → tap to expand into a dedicated sub-sub-screen.
Maximum 2 tap-depths.

**Option B (three-tier for dense areas)**: Input page lists 7
group headers (NavPad, Voice, Action-bar, Haptic, Custom keyboard,
Invert pane nav, Keep screen on); first three are tappable into
sub-sub-screens with the dense knobs. Three tap-depths for NavPad
internals.

**Recommendation: B.** NavPad and Voice have enough internal
complexity that flattening them into a list-of-rows-with-modal-
chooser is worse UX than letting them be their own page. The
existing `voice_settings_screen.dart` is already this shape and
works well. Cost: one more tap for a NavPad mode change, which is
infrequent.

**Director decision required.** This doc proposes B; if A is
preferred, the wedge plan flattens accordingly.

### OQ-2. Search scope — title only or title + subtitle?

The cheap option indexes only row titles. The better option also
indexes subtitles ("Tap to choose a fallback font family if the
primary is unavailable") and l10n description keys. Estimated
delta: ~50 indexable strings instead of ~30.

**Recommendation: title + subtitle.** Subtitles carry the
"information scent" the user often searches for ("font", "haptic",
"backup"). Implementation cost is minor — the rows are
declaratively built, so we can extract a `(title, subtitle,
route)` triple alongside the existing render path.

**Director decision required.** Default = title+subtitle unless
told otherwise.

### OQ-3. Image + File transfer — merge or keep separate?

Current state: two sections with overlapping rows
(remote path / auto-enter / bracketed paste / path format). They
differ only in (a) image-only knobs (format / quality / resize)
and (b) file-only knobs (download path).

**Option A (merge)**: One "Files & Media" page with a tab strip
inside — `Images | Files | Shared`. Shared knobs live in the
Shared tab.

**Option B (keep separate)**: Two siblings — "Image transfer" and
"File transfer". Status quo, but indented one level deeper under
Files & Media.

**Recommendation: A**, but only if the tab strip doesn't add
noticeable chrome weight. Otherwise B.

**Director decision required.**

### OQ-4. Pinned / Recent on Settings home — ship or defer?

A "Recent / Pinned" row at the top would track the last 3 toggled
settings (or user-pinned via long-press), bypassing the taxonomy
for repeat changes. Industry precedent: VSCode "Frequently used
settings", iOS Spotlight surfacing recent Settings rows.

**Trade**: real ergonomic win for power users; adds state
(per-device persistence), one more concept to maintain.

**Recommendation: defer to a follow-up wedge.** Ship the 7-cat
taxonomy + search first; measure whether search alone is enough
before adding state. If users still complain about access time
for frequent toggles (theme, voice, scrollback), add Pinned in a
second wedge.

### OQ-5. Standalone screens — fold or link?

Today there are standalone screens at:
`voice_settings_screen.dart`, `action_bar_settings_screen.dart`,
`file_browser_screen.dart`, `licenses_screen.dart`.

**Recommendation: keep as standalone routes, navigate to them
from the new sub-pages.** No file deletions; the new IA shells
just provide structured entry points. This minimizes the wedge's
blast radius and lets the standalone pages keep their independent
deep-link contracts (e.g. `licenses_screen` is also reachable
from the legal footer if any).

### OQ-6. l10n migration

Renames: `l10n.sectionTerminal` → `l10n.scopeDisplay`, etc.
Existing string values stay; only the key names change.

**Recommendation**: rename keys in the same wedge. l10n keys are
not user-visible; consumers are just the Dart code. No
backwards-compat shim needed.

### OQ-7. TeamSwitcher tooltip + first-run nudge — same wedge or follow-up?

The two discoverability tweaks in §4 (popup section label,
tooltip copy update, first-run nudge for team-flavored search
misses) are small (~1 hour) and self-contained.

**Recommendation: same wedge as Settings refactor.** Ship the
naming + discoverability together so the "two-scope" mental model
lands as one user-visible release rather than dribbling out.

### Non-blockers (decide during implementation)

- Material 3 chevron / icon conventions on sub-page rows — follow
  existing termipod theme defaults.
- Whether the Settings AppBar shows a "back to top" / "expand
  all" affordance — almost certainly no; the categories ARE the
  navigation.
- Pinned slot count if/when OQ-4 lands — proposed 3, never more.

---

## 7. Implementation sketch

One wedge, ~v1.0.580, single commit:

1. Refactor `settings_screen.dart` from flat ListView into a 7-row
   home screen.
2. Create 7 sub-screen widgets (Display, Input, Files & Media,
   Data, Advanced, About, You). Each pulls its rows from the
   existing settings_screen body.
3. Wire the search bar with row-list indexing (title + subtitle
   per OQ-2).
4. Add sub-label "current value" rendering on each top-level home
   row.
5. Rename l10n keys (OQ-6).
6. Update TeamSwitcher copy + first-run nudge (OQ-7).
7. Update `docs/how-to/release-testing.md` §X with a settings IA
   verification scenario.

No backend work. No data model changes. Pure presentation
refactor with state-preserving wrapping (existing
`SettingsState` and providers untouched).

A follow-up wedge ~v1.0.581 adds Templates list chrome (tab +
chip filters + search) — out of scope for this discussion but
referenced for completeness.

---

## 8. References

- Nielsen Norman Group — "Information Architecture: Study Guide"
  (canonical IA principles, recognition over recall).
- iOS Human Interface Guidelines — "Settings" pattern (scope-
  based ordering).
- macOS Ventura System Settings (negative example — internal
  taxonomy vs user mental model).
- Linear, Figma, Slack — three production examples of explicit
  device vs workspace scope split.
- ADR-023 — agent-driven mobile UI (the broader UI framing this
  fits into).
- Code touched by the implementation wedge:
  `lib/screens/settings/settings_screen.dart`,
  `lib/widgets/team_switcher.dart`,
  `lib/screens/settings/voice_settings_screen.dart`,
  `lib/screens/settings/action_bar_settings_screen.dart`.

# Vocab audit — theme-pack groundwork

**Status:** Reference doc, not a build spec. The theme-pack feature itself
is deferred to post-demo (see `../plans/research-demo-gaps.md`); this file is the
artifact that survives until then so that:

1. New strings land in the correct axis from day one (no retroactive
   sweep).
2. Hardcoded role-bound text is logged, not lost.
3. The eventual swap is a rename, not a rewrite.

---

## 1. The vocab axes

Each axis is a concept that shifts together when a theme changes. Strings
inside an axis must always co-vary — if `steward` becomes `Chief of Staff`,
every reference to "the steward" in the same theme follows.

| Axis | Current canonical term | Swappable? | Why |
|---|---|---|---|
| `role.steward` | Steward | ✅ | The AI CEO-class operator. Most theme-sensitive term in the app. |
| `role.agent` | Agent | ✅ | The steward's workers. |
| `role.principal` | Principal / "you" | ✅ | The human board/owner directing the steward. |
| `role.council` | Council | ✅ | Multi-reviewer panels. |
| `entity.team` | Team | ✅ | The tenant. |
| `entity.project` | Project | ✅ | Goal-bound work. |
| `entity.workspace` | Workspace | ✅ | Standing/continuous surface. |
| `entity.task` | Task | ✅ | Atomic unit of work. |
| `entity.plan` | Plan | ✅ | Phased execution spec. |
| `entity.run` | Run | ✅ | Execution instance. |
| `entity.schedule` | Schedule | ✅ | Recurring trigger. |
| `entity.template` | Template | ✅ | Reusable plan scaffold. |
| `entity.channel` | Channel | ✅ | Comms surface. |
| `entity.host` | Host | ⚠️ | Mostly technical; *Bureau / Lab / Workshop* feels forced. Treat as neutral by default; per-theme override only when it adds value. |
| `entity.review` | Review | ✅ | Approval gate. |
| `entity.document` | Document | ✅ | Reference artifact. |
| `entity.output` | Output (artifact) | ✅ | Produced file. |
| `surface.attention` | Attention / Inbox | ✅ | What's pending for the principal. |
| `surface.approval` | Approval | ✅ | Decision gate. |
| `surface.directive` | Directive | ✅ | Principal's instruction. |
| `surface.brief` | Brief / Digest | ✅ | Daily summary. |
| `tech.host` (SSH host, ports, tmux) | Host / Port / Session / Pane | ❌ | Pure technical vocabulary. Never themed. |
| `tech.terminal` (UI verbs) | Cancel / Close / Save | ❌ | Generic UI. Never themed. |

**Always-neutral:** auth fields, SSH/tmux concepts, file transfer, font
sizes, theme picker, settings UI, dialog buttons, error messages, version
strings. ~80% of `app_en.arb` keys fall here and need no change.

---

## 2. Theme presets

Four themes are the working set. Per-axis values for each:

| Axis | tech (default) | business | political | research |
|---|---|---|---|---|
| `role.steward` | Steward | Chief of Staff | Chief of Staff | Lab Manager |
| `role.agent` | Agent | Specialist | Operative | Researcher |
| `role.principal` | Owner | CEO | Principal | PI |
| `role.council` | Review board | Committee | Council | Review panel |
| `entity.team` | Team | Org | Office | Group |
| `entity.project` | Project | Initiative | Operation | Study |
| `entity.workspace` | Service | Department | Bureau | Lab |
| `entity.task` | Ticket | Action item | Action | Step |
| `entity.plan` | Roadmap | Roadmap | Strategy | Protocol |
| `entity.run` | Build | Execution | Operation | Trial |
| `entity.schedule` | Schedule | Cadence | Calendar | Schedule |
| `entity.template` | Pipeline | Playbook | Playbook | Protocol |
| `entity.channel` | Channel | Channel | War room | Notebook |
| `entity.review` | Review | Approval | Sign-off | Peer review |
| `entity.document` | Doc | Brief | Memo | Paper |
| `entity.output` | Artifact | Deliverable | Output | Result |
| `surface.attention` | Inbox | Action items | Briefings | Inbox |
| `surface.approval` | Approval | Approval | Sign-off | Sign-off |
| `surface.directive` | Spec | Directive | Directive | Hypothesis |
| `surface.brief` | Digest | Daily brief | Briefing | Lab notes |

**ZH equivalents** are deferred; the pattern will be:
`{theme}_{lang}` packs (e.g. `business_zh`: 首席运营官 / 专员 / CEO / 委员会 …).
Translation work belongs to the implementation wedge, not this audit.

---

## 3. Swap-bound l10n keys (current state)

These keys in `lib/l10n/app_en.arb` and `app_zh.arb` are tied to one of
the axes above and must move together when the theme changes. Total: ~75
keys (out of 584).

### `role.steward` (24 keys)
- `stewardConfigTitle`, `stewardConfigComingSoon`,
  `stewardConfigRunning`, `stewardConfigNotRunning`,
  `stewardConfigAutonomyLabel`, `stewardConfigBudgetLabel`,
  `stewardConfigScopeLabel`, `stewardConfigScopeHint`,
  `stewardConfigModelLabel`, `stewardConfigSave`,
  `stewardConfigSaved`, `stewardConfigLocalOnlyNote`
- Hardcoded today: 31 string-literal occurrences across `lib/` (largest
  cluster: `me_screen.dart`, `team/audit_screen.dart`,
  `widgets/steward_badge.dart`).

### `role.agent` (5 keys)
- `projectNoAgents`, `workspaceNoAgents`, `categoryCliAgent`,
  plus indirect references in `agent_feed.dart`, `spawn_agent_sheet.dart`
- Hardcoded: 14 occurrences.

### `role.council` (2 keys)
- `councilsTitle`, `councilsComingSoon`

### `entity.project` + `entity.workspace` (24 keys, paired)
- `kindProject`, `kindWorkspace`, `kindProjectLower`, `kindWorkspaceLower`
- `kindProjectHelper`, `kindWorkspaceHelper`
- `newProject`, `newWorkspace`, `sectionProjects`, `sectionWorkspaces`
- `projectsEmpty`, `projectDetailEditTooltip`, `workspaceDetailEditTooltip`
- `projectEditTitle`, `workspaceEditTitle`
- `projectArchiveAction`, `workspaceArchiveAction`
- `projectArchiveTitle`, `workspaceArchiveTitle`
- `projectArchiveConfirm`, `workspaceArchiveConfirm`
- `projectCreateFabTooltip`, `projectCreateFabLabel`
- `workspaceOverviewTitle`

### `entity.workspace` cadence/firing (~10 keys)
- `workspaceCadence`, `workspaceNoSchedules`, `workspaceManualOnly`,
  `workspaceMultipleSchedules`, `workspaceNextRunPending`,
  `workspaceNextIn`, `workspaceLastFired`, `workspaceNoFirings`,
  `workspaceRecentFirings`, `workspaceViewAllFirings`,
  `workspaceFiringLoading`, `workspaceCadenceEvery`,
  `workspaceCadenceEveryAt`

### `surface.attention` (1 key)
- `meAttentionSection`

### `entity.host` — borderline (5 keys, treat as neutral)
- `tabHosts`, `hostsEmpty`, `hostsEmptyDesc`, `hostsAddBookmark`,
  `hostScopePersonal/Team/TeamPersonal`
- Recommendation: leave as-is; "Host" is technical enough that re-theming
  it adds confusion.

---

## 4. Hardcoded strings to migrate

Found by `grep` over `lib/**/*.dart` — these aren't in `.arb` yet and
will need migration before the theme-pack wedge. Counts as of 2026-04-25:

| Term | Hardcoded occurrences | Notes |
|---|---:|---|
| `Steward` / `steward` | 31 | Heaviest debt. Includes `me_screen.dart` "Direct" FAB tooltip text, `widgets/steward_badge.dart`, `team/audit_screen.dart` filter chips, `team_screen.dart` quick-action labels. |
| `Agent` / `agent` | 14 | `agent_feed.dart`, `spawn_agent_sheet.dart`, `archived_agents_screen.dart`. |
| `Council` | 2 | Settings entry only. |
| `Hub` / `hub-meta` | 5 | Mostly technical paths (`#hub-meta`); leave channel ID stable, only the user-facing label is themable. |
| `Approval` / `Approve` / `Reject` | 5 | `_ApprovalActions` row in `me_screen.dart`. |

Migration pattern when touching one of these files:

```dart
// before
const Text('Direct steward')

// after — even before the theme picker exists
Text(l10n.fabDirectSteward)
```

…and add `fabDirectSteward` to both `.arb` files. The string stays
hardcoded English/Chinese until the theme pack ships, but the key is
now in the right slot.

---

## 5. Implementation shape (when the wedge is scheduled)

Two viable shapes — both compatible with this audit:

**Shape A: One key per axis-term, helper resolves at call-site.**
```dart
final voc = ref.watch(vocabularyProvider);
Text(voc.steward); // resolves to "Steward" / "Chief of Staff" / "PI" …
```
- One generated `Vocabulary` class per theme, picked from a Riverpod
  `vocabularyProvider`.
- l10n keys still per-language but theme-neutral; theme overrides live in
  a small `vocab_<theme>.dart` map.
- Lower l10n surface area; preferred.

**Shape B: Per-theme `.arb` packs.**
- `app_en_business.arb`, `app_zh_political.arb` …
- Picked at runtime via `localizationsDelegates`.
- Cleaner separation but multiplies the l10n file count by `themes ×
  langs` (4 × 2 = 8 packs to keep in sync). Not recommended.

Either way, axis tags from §1 are the source of truth — they pin the set
of strings that must co-vary.

---

## 6. Standing rule for new strings

When adding a user-facing string:

1. Check §1 — does it sit on a vocab axis?
2. If yes, check §3 — is there an existing key for that axis-term? Reuse
   it. If not, add a key whose name encodes the axis (`stewardX`,
   `projectX`, `workspaceX`, `councilX`, `attentionX`, …).
3. If no (it's neutral UI/tech vocab), name it for the surface as usual.

This single rule is the entire cost of "audit-now, swap-later." Future
theme-pack work then becomes: write four 75-string Vocabulary maps, wire
the picker, done.

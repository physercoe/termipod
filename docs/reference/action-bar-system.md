# Action bar system

> **Type:** reference
> **Status:** Current (2026-05-01)
> **Audience:** contributors · UI/UX
> **Last verified vs code:** v1.0.350-alpha

**TL;DR.** The action bar is the row(s) above the terminal keyboard that surface fast inputs to the connected pane: special keys (Esc, Tab, Ctrl, Alt), navigation (arrows, page-up, jump-to-end), and **snippets** — preset and user-defined commands with optional `{{variable}}` substitution. Each connected pane carries its own profile (Claude Code, Codex, Generic Tmux, …); switching the active CLI agent in a pane switches the snippet set. This file is the data-model + behavior reference. The user-facing UI lives in `lib/widgets/action_bar/`.

---

## Mental model

A **profile** is a curated bundle of snippets + a layout configuration matched to a particular CLI agent or tmux usage style. A **snippet** is a one-tap shortcut that sends or inserts text into the pane, optionally after a fill-in dialog. The picker tabs split snippets into **presets** (built-in, version-controlled, code-defined) and **custom** (user-authored, persisted to SharedPreferences).

Per-pane state means a Claude Code pane and a Codex pane on the same device can have completely different snippet sets active at the same time without the user manually flipping a global toggle. The split between built-in code-defined presets and user-authored custom snippets means presets evolve with the app while customs are durable across upgrades.

---

## Data model

### `Snippet`

`lib/providers/snippet_provider.dart`

| Field | Type | Default | Notes |
|---|---|---|---|
| `id` | `String` | required | Stable identifier. Presets use `preset-cc-…` / `preset-codex-…`; user snippets get a UUID at create time. |
| `name` | `String` | required | Display label on the picker chip. Convention: the literal slash command (`/compact`) for CLI-agent snippets; free-form for tmux/general. |
| `content` | `String` | required | The text sent or inserted. Supports `{{var}}` placeholders resolved via `Snippet.resolve()`. |
| `category` | `String` | `'general'` | Free-form bucket: `general`, `tmux`, `claude-code`, `codex`, `cli-agent`, etc. Used for grouping in the picker. |
| `variables` | `List<SnippetVariable>` | `[]` | Variables prompted before send/insert. Order = dialog field order. |
| `sendImmediately` | `bool` | `false` | If `true`, tap → resolve → send to the pane's stdin directly. If `false`, tap → resolve → insert into the compose bar (user can edit before send). |

### `SnippetVariable`

| Field | Type | Default | Notes |
|---|---|---|---|
| `name` | `String` | required | Placeholder name in `content` (`{{name}}`). |
| `defaultValue` | `String` | `''` | Pre-fills the input. |
| `kind` | `SnippetVarKind` | `text` | `text` → free-form `TextField`. `option` → `DropdownButtonFormField` over `options`. |
| `options` | `List<String>` | `[]` | Required when `kind == option`; ignored otherwise. |
| `hint` | `String?` | null | Hint text inside the input. Text-kind only. |
| `optional` | `bool` | `false` | Text-kind only. When true, blank input → empty string + surrounding space collapsed. So `/compact {{focus}}` with empty focus resolves to `/compact`, not `/compact ` (trailing space). |

### `SnippetsState` (storage shape)

| Field | What it stores |
|---|---|
| `snippets` | The user's custom snippets (full Snippet objects). |
| `presetOverrides` | User edits to a preset, keyed by the *original* preset id. Picker swaps the override in when the same id renders. |
| `deletedPresets` | Set of preset ids the user has hidden. Original is still in code but not displayed. |

Storage key: `'snippets'` in SharedPreferences. Plus `'snippet_preset_overrides'` and `'snippet_deleted_presets'` for the override + hide sets.

### `ActionBarPresets` (profiles)

`lib/models/action_bar_presets.dart`

A profile carries: `id` (e.g. `'claude-code'`, `'codex'`, `'tmux'`), display name, key palette layout (which special-key buttons appear and where), and the set of snippet preset IDs that show by default. Presets are *code-defined* — editing them requires a release. Custom profiles (user-authored) are persisted alongside.

Auto-detect from a connection's launch command: if the cmd contains `claude` → `claude-code`; if it contains `codex` → `codex`; otherwise `tmux`.

### `ActionBarState` (runtime state)

`lib/providers/action_bar_provider.dart`

| Field | What it tracks |
|---|---|
| `activeProfileId` | Global default profile. New panes inherit this until they diverge. |
| `activeProfileByPanel` | Per-panel override. Key = `${connectionId}|${paneId}`. Wins over the global default for that panel. |
| `profiles` | Built-in + custom profile list. |
| `currentPage` | Action bar swipe position (multi-page layouts). |
| `ctrlArmed`, `altArmed`, `ctrlLocked`, `altLocked` | Modifier state. Tap = arm-once; double-tap = lock. Lock persists across keypresses until tapped off. |

Storage keys: `settings_action_bar_active_profile`, `settings_action_bar_profile_by_panel`, `settings_action_bar_profiles` (custom profiles), `settings_action_bar_command_history`, `settings_action_bar_compose_mode`.

---

## Variable resolution

`Snippet.resolve(Map<String,String> values)` does the substitution. Two rules:

1. **Standard substitution.** `{{name}}` → `values['name']` (or `defaultValue` if absent).
2. **Optional collapse.** If `variable.optional && value.isEmpty`, the placeholder *plus a single surrounding space* is removed: `/compact {{focus}}` → `/compact`. This is why the UX of an empty optional variable doesn't leave trailing whitespace artifacts in CLI agents that interpret them.

Variables are resolved before send. The compose-bar render has them already substituted; the user edits the resolved text, not the template.

---

## UX surfaces

| Surface | File | Role |
|---|---|---|
| Action bar (the row(s)) | `lib/widgets/action_bar/action_bar.dart` | Renders special keys + the snippet/insert chip; delegates to children. |
| Action bar button | `lib/widgets/action_bar/action_bar_button.dart` | Single key; handles arm/lock state. |
| Compose bar | `lib/widgets/action_bar/compose_bar.dart` | Multi-line text entry above the action bar. Bracketed paste via `tmux paste-buffer -p` for true multi-line atomic insert (avoids autocomplete fighting `\n`). |
| Snippet picker sheet | `lib/widgets/action_bar/snippet_picker_sheet.dart` | Modal bottom sheet with Presets / Custom tabs. |
| Insert menu | `lib/widgets/action_bar/insert_menu.dart` | Dropdown shown when long-pressing the insert chip. |
| Profile sheet | `lib/widgets/action_bar/profile_sheet.dart` | Profile-switch modal for the active pane. |
| Page (per profile) | `lib/widgets/action_bar/action_bar_page.dart` | The swipe-paged layout per profile. |

### Interaction patterns

- **Tap snippet** → if `variables.isEmpty`: directly send/insert based on `sendImmediately`. Else open variable dialog.
- **Insert button on dialog** → resolve variables → send (if `sendImmediately`) or insert into compose (otherwise). The dialog and the picker sheet pop together; the wrapper at `SnippetPickerSheet.show` does the sheet pop, the dialog pops itself. *Double-popping is a recurring bug class* — see [bug fix in v1.0.350-alpha](https://github.com/physercoe/termipod/commit/8d04851) where the dialog was popping three routes (dialog + manual sheet pop + wrapper sheet pop) and unwound the user out of the terminal screen back to Hosts.
- **Long-press snippet** → save-as-snippet flow (lift the current compose-bar text into a new custom snippet).
- **Cancel on dialog** → only the dialog pops; sheet stays.
- **Modifier keys** → tap arms (one-shot, consumed by next keypress). Double-tap locks (persists until tapped off). Pattern is consistent with desktop shells.

---

## Built-in presets (overview)

`lib/models/snippet_presets.dart` defines the bundled snippet sets. Naming convention: name = the slash command literal; content = the slash command with `{{variable}}` expansion when applicable; `sendImmediately: true` for non-destructive lifecycle commands; `category: 'claude-code' | 'codex' | 'tmux' | 'cli-agent'` for grouping.

Top-level summary at v1.0.350-alpha:

| Profile | Preset count | Sample commands |
|---|---|---|
| `claude-code` | ~30 | `/clear`, `/compact {{focus}}`, `/context`, `/rewind`, `/resume`, `/model {{model}}`, `/permissions {{mode}}` |
| `codex` | ~25 | `/init`, `/clear`, `/model`, `/approvals`, `/diff`, `/feedback` |
| `tmux` | ~15 | `Esc:`, `Esc:q`, `prefix-c` (new window), pane navigation |

Adding a new preset is a code edit (new entry in `SnippetPresets._presets`). Users can override or hide any preset; their override persists across upgrades. The original definition stays in code as the recoverable default.

---

## Lifecycle & migration

- **First launch (no stored state).** Built-in presets render directly; no `SharedPreferences` write yet.
- **First user edit.** A custom snippet, edited preset, or hidden preset triggers a write. From then on, the storage key has authoritative content.
- **App upgrade.** Built-in presets shipped in code update freely. User snippets and overrides survive (keyed by `id`). A removed preset (id no longer in code) silently drops from picker; the user's override of it is preserved as orphan storage and surfaces only if the id is re-added.
- **Data export/import.** `DataPortService` (v1.0.2+) round-trips `snippets`, `snippet_preset_overrides`, `snippet_deleted_presets`, plus `settings_action_bar_*` keys.

---

## Adding a new profile

When supporting a new CLI agent (e.g. a future first-class engine):

1. Add the profile id constant to `ActionBarPresets` (e.g. `static const String fooId = 'foo';`).
2. Add an entry to `_presets` mapping in `SnippetPresets._presets` with the agent's slash commands.
3. Wire auto-detect in `ActionBarPresets.detectFromCommand()` — typical: `if (cmd.contains('foo')) return fooId;`.
4. Optionally provide a default key palette (multi-page action bar layout) in `action_bar_presets.dart`.

No schema migration needed; the new profile id starts appearing in the picker on the next launch.

---

## Related

- [How-to: report-an-issue](../how-to/report-an-issue.md) — when filing a UI bug, attach the active profile id and a reproduction with snippet-tap timing.
- [Reference: ui-guidelines](ui-guidelines.md) — long-press, tap-feedback duration (220ms), modifier UX patterns.
- Memory: `feedback_bolt_icon_ambiguity.md` (bolt icon reserved for snippet ActionChips), `feedback_tap_feedback_duration.md` (220ms hold).
- Code: `lib/widgets/action_bar/`, `lib/providers/snippet_provider.dart`, `lib/providers/action_bar_provider.dart`, `lib/models/snippet_presets.dart`, `lib/models/action_bar_presets.dart`, `lib/models/action_bar_config.dart`.

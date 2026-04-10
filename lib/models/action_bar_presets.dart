import 'action_bar_config.dart';

/// Built-in action bar profiles for different CLI tools
class ActionBarPresets {
  ActionBarPresets._();

  static const String claudeCodeId = 'claude-code';
  static const String codexId = 'codex';
  static const String generalTerminalId = 'general-terminal';
  static const String tmuxId = 'tmux';

  /// Default profile ID
  static const String defaultProfileId = generalTerminalId;

  /// All built-in profiles
  static List<ActionBarProfile> get all => [
        claudeCode,
        codex,
        generalTerminal,
        tmux,
      ];

  /// Get a built-in profile by ID, returns null if not found
  static ActionBarProfile? getById(String id) {
    for (final profile in all) {
      if (profile.id == id) return profile;
    }
    return null;
  }

  /// Master catalog of all unique buttons from built-in profiles,
  /// de-duplicated by value. Users pick from this when adding buttons.
  ///
  /// Also merges in [extraCatalogButtons] so the picker offers every
  /// standard keyboard key (numbers, shift-number symbols, F1-F12,
  /// brackets, quotes, Delete/Insert, etc.) even though no preset
  /// profile ships them in its default groups.
  static List<ActionBarButton> get buttonCatalog {
    final seen = <String>{};
    final catalog = <ActionBarButton>[];
    for (final profile in all) {
      for (final group in profile.groups) {
        for (final button in group.buttons) {
          if (seen.add(button.value)) {
            catalog.add(button);
          }
        }
      }
    }
    for (final button in extraCatalogButtons) {
      if (seen.add(button.value)) {
        catalog.add(button);
      }
    }
    return catalog;
  }

  /// Button catalog grouped by type for the picker UI.
  static Map<ActionBarButtonType, List<ActionBarButton>> get buttonCatalogByType {
    final result = <ActionBarButtonType, List<ActionBarButton>>{};
    for (final button in buttonCatalog) {
      result.putIfAbsent(button.type, () => []).add(button);
    }
    return result;
  }

  /// Detect suggested profile from pane_current_command
  static String? detectProfileId(String? currentCommand) {
    if (currentCommand == null || currentCommand.isEmpty) return null;
    final cmd = currentCommand.toLowerCase();
    if (cmd.contains('claude')) return claudeCodeId;
    if (cmd.contains('codex')) return codexId;
    return null;
  }

  // ---------------------------------------------------------------------------
  // Claude Code
  // ---------------------------------------------------------------------------

  static const claudeCode = ActionBarProfile(
    id: claudeCodeId,
    name: 'Claude Code',
    isBuiltIn: false,
    groups: [
      ActionBarGroup(id: 'cc-quick', name: 'Quick', buttons: [
        ActionBarButton(id: 'cc-snippet', label: '⚡', type: ActionBarButtonType.action, value: 'snippet', iconName: 'bolt', description: 'Snippets'),
        ActionBarButton(id: 'cc-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape', longPressValue: 'Escape Escape'),
        ActionBarButton(id: 'cc-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab', longPressValue: 'BTab'),
        ActionBarButton(id: 'cc-cc', label: 'C-C', type: ActionBarButtonType.ctrlCombo, value: 'C-c'),
        ActionBarButton(id: 'cc-y', label: 'y', type: ActionBarButtonType.confirm, value: 'y'),
        ActionBarButton(id: 'cc-n', label: 'n', type: ActionBarButtonType.confirm, value: 'n'),
        ActionBarButton(id: 'cc-cd', label: 'C-D', type: ActionBarButtonType.ctrlCombo, value: 'C-d'),
      ]),
      ActionBarGroup(id: 'cc-nav', name: 'Navigate', buttons: [
        ActionBarButton(id: 'cc-left', label: '←', type: ActionBarButtonType.specialKey, value: 'Left', iconName: 'arrow_left'),
        ActionBarButton(id: 'cc-up', label: '↑', type: ActionBarButtonType.specialKey, value: 'Up', iconName: 'arrow_drop_up'),
        ActionBarButton(id: 'cc-down', label: '↓', type: ActionBarButtonType.specialKey, value: 'Down', iconName: 'arrow_drop_down'),
        ActionBarButton(id: 'cc-right', label: '→', type: ActionBarButtonType.specialKey, value: 'Right', iconName: 'arrow_right'),
        ActionBarButton(id: 'cc-home', label: 'Home', type: ActionBarButtonType.specialKey, value: 'Home'),
        ActionBarButton(id: 'cc-end', label: 'End', type: ActionBarButtonType.specialKey, value: 'End'),
      ]),
      ActionBarGroup(id: 'cc-ctrl', name: 'Ctrl', buttons: [
        ActionBarButton(id: 'cc-cl', label: 'C-L', type: ActionBarButtonType.ctrlCombo, value: 'C-l'),
        ActionBarButton(id: 'cc-cr', label: 'C-R', type: ActionBarButtonType.ctrlCombo, value: 'C-r'),
        ActionBarButton(id: 'cc-cz', label: 'C-Z', type: ActionBarButtonType.ctrlCombo, value: 'C-z'),
        ActionBarButton(id: 'cc-ck', label: 'C-K', type: ActionBarButtonType.ctrlCombo, value: 'C-k'),
        ActionBarButton(id: 'cc-cu', label: 'C-U', type: ActionBarButtonType.ctrlCombo, value: 'C-u'),
        ActionBarButton(id: 'cc-cy', label: 'C-Y', type: ActionBarButtonType.ctrlCombo, value: 'C-y'),
      ]),
      ActionBarGroup(id: 'cc-edit', name: 'Edit', buttons: [
        ActionBarButton(id: 'cc-ctrl-mod', label: 'CTRL', type: ActionBarButtonType.modifier, value: 'ctrl'),
        ActionBarButton(id: 'cc-alt-mod', label: 'ALT', type: ActionBarButtonType.modifier, value: 'alt'),
        ActionBarButton(id: 'cc-senter', label: 'S-Ent', type: ActionBarButtonType.shiftCombo, value: 'S-Enter'),
        ActionBarButton(id: 'cc-cj', label: 'C-J', type: ActionBarButtonType.ctrlCombo, value: 'C-j'),
        ActionBarButton(id: 'cc-bslash', label: '\\', type: ActionBarButtonType.literal, value: '\\'),
        ActionBarButton(id: 'cc-pgup', label: 'PgUp', type: ActionBarButtonType.specialKey, value: 'PPage'),
      ]),
    ],
  );

  // ---------------------------------------------------------------------------
  // Codex
  // ---------------------------------------------------------------------------

  static const codex = ActionBarProfile(
    id: codexId,
    name: 'Codex',
    isBuiltIn: false,
    groups: [
      ActionBarGroup(id: 'cx-quick', name: 'Quick', buttons: [
        ActionBarButton(id: 'cx-snippet', label: '⚡', type: ActionBarButtonType.action, value: 'snippet', iconName: 'bolt', description: 'Snippets'),
        ActionBarButton(id: 'cx-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape', longPressValue: 'Escape Escape'),
        ActionBarButton(id: 'cx-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab', longPressValue: 'BTab'),
        ActionBarButton(id: 'cx-cc', label: 'C-C', type: ActionBarButtonType.ctrlCombo, value: 'C-c'),
        ActionBarButton(id: 'cx-y', label: 'y', type: ActionBarButtonType.confirm, value: 'y'),
        ActionBarButton(id: 'cx-n', label: 'n', type: ActionBarButtonType.confirm, value: 'n'),
        ActionBarButton(id: 'cx-cg', label: 'C-G', type: ActionBarButtonType.ctrlCombo, value: 'C-g'),
      ]),
      ActionBarGroup(id: 'cx-nav', name: 'Navigate', buttons: [
        ActionBarButton(id: 'cx-left', label: '←', type: ActionBarButtonType.specialKey, value: 'Left', iconName: 'arrow_left'),
        ActionBarButton(id: 'cx-up', label: '↑', type: ActionBarButtonType.specialKey, value: 'Up', iconName: 'arrow_drop_up'),
        ActionBarButton(id: 'cx-down', label: '↓', type: ActionBarButtonType.specialKey, value: 'Down', iconName: 'arrow_drop_down'),
        ActionBarButton(id: 'cx-right', label: '→', type: ActionBarButtonType.specialKey, value: 'Right', iconName: 'arrow_right'),
        ActionBarButton(id: 'cx-escesc', label: 'EscEsc', type: ActionBarButtonType.specialKey, value: 'Escape Escape'),
      ]),
      ActionBarGroup(id: 'cx-ctrl', name: 'Ctrl', buttons: [
        ActionBarButton(id: 'cx-cd', label: 'C-D', type: ActionBarButtonType.ctrlCombo, value: 'C-d'),
        ActionBarButton(id: 'cx-cl', label: 'C-L', type: ActionBarButtonType.ctrlCombo, value: 'C-l'),
        ActionBarButton(id: 'cx-co', label: 'C-O', type: ActionBarButtonType.ctrlCombo, value: 'C-o'),
        ActionBarButton(id: 'cx-ctrl-mod', label: 'CTRL', type: ActionBarButtonType.modifier, value: 'ctrl'),
        ActionBarButton(id: 'cx-alt-mod', label: 'ALT', type: ActionBarButtonType.modifier, value: 'alt'),
      ]),
      ActionBarGroup(id: 'cx-edit', name: 'Edit', buttons: [
        ActionBarButton(id: 'cx-senter', label: 'S-Ent', type: ActionBarButtonType.shiftCombo, value: 'S-Enter'),
        ActionBarButton(id: 'cx-pgup', label: 'PgUp', type: ActionBarButtonType.specialKey, value: 'PPage'),
        ActionBarButton(id: 'cx-pgdn', label: 'PgDn', type: ActionBarButtonType.specialKey, value: 'NPage'),
      ]),
    ],
  );

  // ---------------------------------------------------------------------------
  // General Terminal
  // ---------------------------------------------------------------------------

  static const generalTerminal = ActionBarProfile(
    id: generalTerminalId,
    name: 'General Terminal',
    isBuiltIn: true,
    groups: [
      ActionBarGroup(id: 'gt-keys', name: 'Keys', buttons: [
        ActionBarButton(id: 'gt-snippet', label: '⚡', type: ActionBarButtonType.action, value: 'snippet', iconName: 'bolt', description: 'Snippets'),
        ActionBarButton(id: 'gt-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape'),
        ActionBarButton(id: 'gt-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab', longPressValue: 'BTab'),
        ActionBarButton(id: 'gt-ctrl-mod', label: 'CTRL', type: ActionBarButtonType.modifier, value: 'ctrl'),
        ActionBarButton(id: 'gt-alt-mod', label: 'ALT', type: ActionBarButtonType.modifier, value: 'alt'),
        ActionBarButton(id: 'gt-enter', label: 'Enter', type: ActionBarButtonType.specialKey, value: 'Enter'),
      ]),
      ActionBarGroup(id: 'gt-nav', name: 'Navigate', buttons: [
        ActionBarButton(id: 'gt-left', label: '←', type: ActionBarButtonType.specialKey, value: 'Left', iconName: 'arrow_left'),
        ActionBarButton(id: 'gt-up', label: '↑', type: ActionBarButtonType.specialKey, value: 'Up', iconName: 'arrow_drop_up'),
        ActionBarButton(id: 'gt-down', label: '↓', type: ActionBarButtonType.specialKey, value: 'Down', iconName: 'arrow_drop_down'),
        ActionBarButton(id: 'gt-right', label: '→', type: ActionBarButtonType.specialKey, value: 'Right', iconName: 'arrow_right'),
        ActionBarButton(id: 'gt-home', label: 'Home', type: ActionBarButtonType.specialKey, value: 'Home'),
        ActionBarButton(id: 'gt-end', label: 'End', type: ActionBarButtonType.specialKey, value: 'End'),
      ]),
      ActionBarGroup(id: 'gt-chars', name: 'Chars', buttons: [
        ActionBarButton(id: 'gt-slash', label: '/', type: ActionBarButtonType.literal, value: '/'),
        ActionBarButton(id: 'gt-pipe', label: '|', type: ActionBarButtonType.literal, value: '|'),
        ActionBarButton(id: 'gt-dash', label: '-', type: ActionBarButtonType.literal, value: '-'),
        ActionBarButton(id: 'gt-tilde', label: '~', type: ActionBarButtonType.literal, value: '~'),
        ActionBarButton(id: 'gt-under', label: '_', type: ActionBarButtonType.literal, value: '_'),
        ActionBarButton(id: 'gt-bslash', label: '\\', type: ActionBarButtonType.literal, value: '\\'),
      ]),
      ActionBarGroup(id: 'gt-page', name: 'Page', buttons: [
        ActionBarButton(id: 'gt-pgup', label: 'PgUp', type: ActionBarButtonType.specialKey, value: 'PPage'),
        ActionBarButton(id: 'gt-pgdn', label: 'PgDn', type: ActionBarButtonType.specialKey, value: 'NPage'),
        ActionBarButton(id: 'gt-f1', label: 'F1', type: ActionBarButtonType.specialKey, value: 'F1'),
        ActionBarButton(id: 'gt-f2', label: 'F2', type: ActionBarButtonType.specialKey, value: 'F2'),
        ActionBarButton(id: 'gt-f3', label: 'F3', type: ActionBarButtonType.specialKey, value: 'F3'),
        ActionBarButton(id: 'gt-f4', label: 'F4', type: ActionBarButtonType.specialKey, value: 'F4'),
      ]),
    ],
  );

  // ---------------------------------------------------------------------------
  // Tmux (prefix-chord action buttons)
  //
  // Buttons whose `value` contains a space are routed through
  // [TmuxCommands.sendKeySequence] by [TmuxBackend.sendSpecialKey], which
  // splits the string and passes each token as a separate positional
  // argument to `tmux send-keys`. That lets a single button fire a
  // full `C-b x` chord from inside an attached session — tmux sees the
  // prefix followed by the action key and reacts exactly as if the
  // user had typed both. Only works under the tmux backend; the raw
  // PTY backend would send the literal string, so users on that
  // backend should avoid this profile.
  // ---------------------------------------------------------------------------

  static const tmux = ActionBarProfile(
    id: tmuxId,
    name: 'Tmux',
    isBuiltIn: true,
    groups: [
      ActionBarGroup(id: 'tx-windows', name: 'Windows', buttons: [
        ActionBarButton(id: 'tx-win-new', label: 'New', type: ActionBarButtonType.specialKey, value: 'C-b c'),
        ActionBarButton(id: 'tx-win-next', label: 'Next', type: ActionBarButtonType.specialKey, value: 'C-b n'),
        ActionBarButton(id: 'tx-win-prev', label: 'Prev', type: ActionBarButtonType.specialKey, value: 'C-b p'),
        ActionBarButton(id: 'tx-win-list', label: 'List', type: ActionBarButtonType.specialKey, value: 'C-b w'),
        ActionBarButton(id: 'tx-win-rename', label: 'Rename', type: ActionBarButtonType.specialKey, value: 'C-b ,'),
        ActionBarButton(id: 'tx-win-kill', label: 'Kill', type: ActionBarButtonType.specialKey, value: 'C-b &'),
      ]),
      ActionBarGroup(id: 'tx-panes', name: 'Panes', buttons: [
        ActionBarButton(id: 'tx-pane-vsplit', label: 'VSplit', type: ActionBarButtonType.specialKey, value: 'C-b %'),
        ActionBarButton(id: 'tx-pane-hsplit', label: 'HSplit', type: ActionBarButtonType.specialKey, value: 'C-b "'),
        ActionBarButton(id: 'tx-pane-next', label: 'NextP', type: ActionBarButtonType.specialKey, value: 'C-b o'),
        ActionBarButton(id: 'tx-pane-kill', label: 'KillP', type: ActionBarButtonType.specialKey, value: 'C-b x'),
        ActionBarButton(id: 'tx-pane-zoom', label: 'Zoom', type: ActionBarButtonType.specialKey, value: 'C-b z'),
      ]),
      ActionBarGroup(id: 'tx-session', name: 'Session', buttons: [
        ActionBarButton(id: 'tx-sess-detach', label: 'Detach', type: ActionBarButtonType.specialKey, value: 'C-b d'),
        ActionBarButton(id: 'tx-sess-list', label: 'SessLs', type: ActionBarButtonType.specialKey, value: 'C-b s'),
        ActionBarButton(id: 'tx-sess-prompt', label: ':', type: ActionBarButtonType.specialKey, value: 'C-b :'),
        ActionBarButton(id: 'tx-sess-help', label: '?', type: ActionBarButtonType.specialKey, value: 'C-b ?'),
      ]),
      ActionBarGroup(id: 'tx-copy', name: 'Copy', buttons: [
        ActionBarButton(id: 'tx-copy-enter', label: 'Copy', type: ActionBarButtonType.specialKey, value: 'C-b ['),
        ActionBarButton(id: 'tx-copy-paste', label: 'Paste', type: ActionBarButtonType.specialKey, value: 'C-b ]'),
      ]),
    ],
  );

  // ---------------------------------------------------------------------------
  // Extra catalog entries — not shipped in any preset profile by default,
  // but available in the "add button" picker so users can drop every
  // standard keyboard key into a custom group without having to type
  // the tmux key name by hand. Values are chosen to be unique across
  // the whole built-in catalog so de-duplication in [buttonCatalog]
  // picks them up as additions, not collisions.
  // ---------------------------------------------------------------------------

  static const extraCatalogButtons = <ActionBarButton>[
    // Digits 0-9
    ActionBarButton(id: 'cat-digit-0', label: '0', type: ActionBarButtonType.literal, value: '0'),
    ActionBarButton(id: 'cat-digit-1', label: '1', type: ActionBarButtonType.literal, value: '1'),
    ActionBarButton(id: 'cat-digit-2', label: '2', type: ActionBarButtonType.literal, value: '2'),
    ActionBarButton(id: 'cat-digit-3', label: '3', type: ActionBarButtonType.literal, value: '3'),
    ActionBarButton(id: 'cat-digit-4', label: '4', type: ActionBarButtonType.literal, value: '4'),
    ActionBarButton(id: 'cat-digit-5', label: '5', type: ActionBarButtonType.literal, value: '5'),
    ActionBarButton(id: 'cat-digit-6', label: '6', type: ActionBarButtonType.literal, value: '6'),
    ActionBarButton(id: 'cat-digit-7', label: '7', type: ActionBarButtonType.literal, value: '7'),
    ActionBarButton(id: 'cat-digit-8', label: '8', type: ActionBarButtonType.literal, value: '8'),
    ActionBarButton(id: 'cat-digit-9', label: '9', type: ActionBarButtonType.literal, value: '9'),
    // Shift-number symbols
    ActionBarButton(id: 'cat-sym-excl', label: '!', type: ActionBarButtonType.literal, value: '!'),
    ActionBarButton(id: 'cat-sym-at', label: '@', type: ActionBarButtonType.literal, value: '@'),
    ActionBarButton(id: 'cat-sym-hash', label: '#', type: ActionBarButtonType.literal, value: '#'),
    ActionBarButton(id: 'cat-sym-dollar', label: '\$', type: ActionBarButtonType.literal, value: '\$'),
    ActionBarButton(id: 'cat-sym-pct', label: '%', type: ActionBarButtonType.literal, value: '%'),
    ActionBarButton(id: 'cat-sym-caret', label: '^', type: ActionBarButtonType.literal, value: '^'),
    ActionBarButton(id: 'cat-sym-amp', label: '&', type: ActionBarButtonType.literal, value: '&'),
    ActionBarButton(id: 'cat-sym-star', label: '*', type: ActionBarButtonType.literal, value: '*'),
    ActionBarButton(id: 'cat-sym-lparen', label: '(', type: ActionBarButtonType.literal, value: '('),
    ActionBarButton(id: 'cat-sym-rparen', label: ')', type: ActionBarButtonType.literal, value: ')'),
    // Brackets & quotes
    ActionBarButton(id: 'cat-sym-lbracket', label: '[', type: ActionBarButtonType.literal, value: '['),
    ActionBarButton(id: 'cat-sym-rbracket', label: ']', type: ActionBarButtonType.literal, value: ']'),
    ActionBarButton(id: 'cat-sym-lbrace', label: '{', type: ActionBarButtonType.literal, value: '{'),
    ActionBarButton(id: 'cat-sym-rbrace', label: '}', type: ActionBarButtonType.literal, value: '}'),
    ActionBarButton(id: 'cat-sym-lt', label: '<', type: ActionBarButtonType.literal, value: '<'),
    ActionBarButton(id: 'cat-sym-gt', label: '>', type: ActionBarButtonType.literal, value: '>'),
    ActionBarButton(id: 'cat-sym-dquote', label: '"', type: ActionBarButtonType.literal, value: '"'),
    ActionBarButton(id: 'cat-sym-squote', label: "'", type: ActionBarButtonType.literal, value: "'"),
    ActionBarButton(id: 'cat-sym-backtick', label: '`', type: ActionBarButtonType.literal, value: '`'),
    // Other punctuation
    ActionBarButton(id: 'cat-sym-colon', label: ':', type: ActionBarButtonType.literal, value: ':'),
    ActionBarButton(id: 'cat-sym-semi', label: ';', type: ActionBarButtonType.literal, value: ';'),
    ActionBarButton(id: 'cat-sym-comma', label: ',', type: ActionBarButtonType.literal, value: ','),
    ActionBarButton(id: 'cat-sym-dot', label: '.', type: ActionBarButtonType.literal, value: '.'),
    ActionBarButton(id: 'cat-sym-question', label: '?', type: ActionBarButtonType.literal, value: '?'),
    ActionBarButton(id: 'cat-sym-eq', label: '=', type: ActionBarButtonType.literal, value: '='),
    ActionBarButton(id: 'cat-sym-plus', label: '+', type: ActionBarButtonType.literal, value: '+'),
    // Special keys not in any preset
    ActionBarButton(id: 'cat-del', label: 'Del', type: ActionBarButtonType.specialKey, value: 'Delete'),
    ActionBarButton(id: 'cat-ins', label: 'Ins', type: ActionBarButtonType.specialKey, value: 'Insert'),
    ActionBarButton(id: 'cat-bspace', label: '⌫', type: ActionBarButtonType.specialKey, value: 'BSpace'),
    ActionBarButton(id: 'cat-space', label: 'Space', type: ActionBarButtonType.specialKey, value: 'Space'),
    // F5-F12 (F1-F4 already ship in General Terminal)
    ActionBarButton(id: 'cat-f5', label: 'F5', type: ActionBarButtonType.specialKey, value: 'F5'),
    ActionBarButton(id: 'cat-f6', label: 'F6', type: ActionBarButtonType.specialKey, value: 'F6'),
    ActionBarButton(id: 'cat-f7', label: 'F7', type: ActionBarButtonType.specialKey, value: 'F7'),
    ActionBarButton(id: 'cat-f8', label: 'F8', type: ActionBarButtonType.specialKey, value: 'F8'),
    ActionBarButton(id: 'cat-f9', label: 'F9', type: ActionBarButtonType.specialKey, value: 'F9'),
    ActionBarButton(id: 'cat-f10', label: 'F10', type: ActionBarButtonType.specialKey, value: 'F10'),
    ActionBarButton(id: 'cat-f11', label: 'F11', type: ActionBarButtonType.specialKey, value: 'F11'),
    ActionBarButton(id: 'cat-f12', label: 'F12', type: ActionBarButtonType.specialKey, value: 'F12'),
  ];
}

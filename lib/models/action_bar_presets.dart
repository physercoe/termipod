import 'action_bar_config.dart';

/// Built-in action bar profiles for different CLI tools
class ActionBarPresets {
  ActionBarPresets._();

  static const String claudeCodeId = 'claude-code';
  static const String codexId = 'codex';
  static const String kimiCodeId = 'kimi-code';
  static const String openCodeId = 'opencode';
  static const String aiderId = 'aider';
  static const String generalTerminalId = 'general-terminal';

  /// Default profile ID
  static const String defaultProfileId = generalTerminalId;

  /// All built-in profiles
  static List<ActionBarProfile> get all => [
        claudeCode,
        codex,
        kimiCode,
        openCode,
        aider,
        generalTerminal,
      ];

  /// Get a built-in profile by ID, returns null if not found
  static ActionBarProfile? getById(String id) {
    for (final profile in all) {
      if (profile.id == id) return profile;
    }
    return null;
  }

  /// Detect suggested profile from pane_current_command
  static String? detectProfileId(String? currentCommand) {
    if (currentCommand == null || currentCommand.isEmpty) return null;
    final cmd = currentCommand.toLowerCase();
    if (cmd.contains('claude')) return claudeCodeId;
    if (cmd.contains('codex')) return codexId;
    if (cmd.contains('kimi')) return kimiCodeId;
    if (cmd.contains('opencode')) return openCodeId;
    if (cmd.contains('aider')) return aiderId;
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
  // Kimi Code
  // ---------------------------------------------------------------------------

  static const kimiCode = ActionBarProfile(
    id: kimiCodeId,
    name: 'Kimi Code',
    isBuiltIn: false,
    groups: [
      ActionBarGroup(id: 'km-quick', name: 'Quick', buttons: [
        ActionBarButton(id: 'km-snippet', label: '⚡', type: ActionBarButtonType.action, value: 'snippet', iconName: 'bolt', description: 'Snippets'),
        ActionBarButton(id: 'km-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape'),
        ActionBarButton(id: 'km-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab', longPressValue: 'BTab'),
        ActionBarButton(id: 'km-cc', label: 'C-C', type: ActionBarButtonType.ctrlCombo, value: 'C-c'),
        ActionBarButton(id: 'km-cx', label: 'C-X', type: ActionBarButtonType.ctrlCombo, value: 'C-x'),
        ActionBarButton(id: 'km-y', label: 'y', type: ActionBarButtonType.confirm, value: 'y'),
        ActionBarButton(id: 'km-n', label: 'n', type: ActionBarButtonType.confirm, value: 'n'),
      ]),
      ActionBarGroup(id: 'km-nav', name: 'Navigate', buttons: [
        ActionBarButton(id: 'km-left', label: '←', type: ActionBarButtonType.specialKey, value: 'Left', iconName: 'arrow_left'),
        ActionBarButton(id: 'km-up', label: '↑', type: ActionBarButtonType.specialKey, value: 'Up', iconName: 'arrow_drop_up'),
        ActionBarButton(id: 'km-down', label: '↓', type: ActionBarButtonType.specialKey, value: 'Down', iconName: 'arrow_drop_down'),
        ActionBarButton(id: 'km-right', label: '→', type: ActionBarButtonType.specialKey, value: 'Right', iconName: 'arrow_right'),
        ActionBarButton(id: 'km-space', label: 'SPC', type: ActionBarButtonType.literal, value: ' '),
      ]),
      ActionBarGroup(id: 'km-ctrl', name: 'Ctrl', buttons: [
        ActionBarButton(id: 'km-cd', label: 'C-D', type: ActionBarButtonType.ctrlCombo, value: 'C-d'),
        ActionBarButton(id: 'km-cj', label: 'C-J', type: ActionBarButtonType.ctrlCombo, value: 'C-j'),
        ActionBarButton(id: 'km-co', label: 'C-O', type: ActionBarButtonType.ctrlCombo, value: 'C-o'),
        ActionBarButton(id: 'km-ce', label: 'C-E', type: ActionBarButtonType.ctrlCombo, value: 'C-e'),
        ActionBarButton(id: 'km-cv', label: 'C-V', type: ActionBarButtonType.ctrlCombo, value: 'C-v'),
      ]),
      ActionBarGroup(id: 'km-edit', name: 'Edit', buttons: [
        ActionBarButton(id: 'km-ctrl-mod', label: 'CTRL', type: ActionBarButtonType.modifier, value: 'ctrl'),
        ActionBarButton(id: 'km-alt-mod', label: 'ALT', type: ActionBarButtonType.modifier, value: 'alt'),
        ActionBarButton(id: 'km-senter', label: 'S-Ent', type: ActionBarButtonType.shiftCombo, value: 'S-Enter'),
      ]),
    ],
  );

  // ---------------------------------------------------------------------------
  // OpenCode
  // ---------------------------------------------------------------------------

  static const openCode = ActionBarProfile(
    id: openCodeId,
    name: 'OpenCode',
    isBuiltIn: false,
    groups: [
      ActionBarGroup(id: 'oc-quick', name: 'Quick', buttons: [
        ActionBarButton(id: 'oc-snippet', label: '⚡', type: ActionBarButtonType.action, value: 'snippet', iconName: 'bolt', description: 'Snippets'),
        ActionBarButton(id: 'oc-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape'),
        ActionBarButton(id: 'oc-cc', label: 'C-C', type: ActionBarButtonType.ctrlCombo, value: 'C-c'),
        ActionBarButton(id: 'oc-cp', label: 'C-P', type: ActionBarButtonType.ctrlCombo, value: 'C-p'),
        ActionBarButton(id: 'oc-y', label: 'y', type: ActionBarButtonType.confirm, value: 'y'),
        ActionBarButton(id: 'oc-n', label: 'n', type: ActionBarButtonType.confirm, value: 'n'),
      ]),
      ActionBarGroup(id: 'oc-nav', name: 'Navigate', buttons: [
        ActionBarButton(id: 'oc-left', label: '←', type: ActionBarButtonType.specialKey, value: 'Left', iconName: 'arrow_left'),
        ActionBarButton(id: 'oc-up', label: '↑', type: ActionBarButtonType.specialKey, value: 'Up', iconName: 'arrow_drop_up'),
        ActionBarButton(id: 'oc-down', label: '↓', type: ActionBarButtonType.specialKey, value: 'Down', iconName: 'arrow_drop_down'),
        ActionBarButton(id: 'oc-right', label: '→', type: ActionBarButtonType.specialKey, value: 'Right', iconName: 'arrow_right'),
        ActionBarButton(id: 'oc-pgup', label: 'PgUp', type: ActionBarButtonType.specialKey, value: 'PPage'),
        ActionBarButton(id: 'oc-pgdn', label: 'PgDn', type: ActionBarButtonType.specialKey, value: 'NPage'),
      ]),
      ActionBarGroup(id: 'oc-leader', name: 'Leader', buttons: [
        ActionBarButton(id: 'oc-cx', label: 'C-X', type: ActionBarButtonType.ctrlCombo, value: 'C-x'),
        ActionBarButton(id: 'oc-ln', label: 'n', type: ActionBarButtonType.literal, value: 'n'),
        ActionBarButton(id: 'oc-ll', label: 'l', type: ActionBarButtonType.literal, value: 'l'),
        ActionBarButton(id: 'oc-lc', label: 'c', type: ActionBarButtonType.literal, value: 'c'),
        ActionBarButton(id: 'oc-le', label: 'e', type: ActionBarButtonType.literal, value: 'e'),
        ActionBarButton(id: 'oc-lb', label: 'b', type: ActionBarButtonType.literal, value: 'b'),
      ]),
      ActionBarGroup(id: 'oc-edit', name: 'Edit', buttons: [
        ActionBarButton(id: 'oc-ctrl-mod', label: 'CTRL', type: ActionBarButtonType.modifier, value: 'ctrl'),
        ActionBarButton(id: 'oc-alt-mod', label: 'ALT', type: ActionBarButtonType.modifier, value: 'alt'),
        ActionBarButton(id: 'oc-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab', longPressValue: 'BTab'),
        ActionBarButton(id: 'oc-stab', label: 'S-Tab', type: ActionBarButtonType.shiftCombo, value: 'BTab'),
        ActionBarButton(id: 'oc-f2', label: 'F2', type: ActionBarButtonType.specialKey, value: 'F2'),
      ]),
    ],
  );

  // ---------------------------------------------------------------------------
  // Aider
  // ---------------------------------------------------------------------------

  static const aider = ActionBarProfile(
    id: aiderId,
    name: 'Aider',
    isBuiltIn: false,
    groups: [
      ActionBarGroup(id: 'ai-quick', name: 'Quick', buttons: [
        ActionBarButton(id: 'ai-snippet', label: '⚡', type: ActionBarButtonType.action, value: 'snippet', iconName: 'bolt', description: 'Snippets'),
        ActionBarButton(id: 'ai-esc', label: 'ESC', type: ActionBarButtonType.specialKey, value: 'Escape'),
        ActionBarButton(id: 'ai-tab', label: 'TAB', type: ActionBarButtonType.specialKey, value: 'Tab', longPressValue: 'BTab'),
        ActionBarButton(id: 'ai-cc', label: 'C-C', type: ActionBarButtonType.ctrlCombo, value: 'C-c'),
        ActionBarButton(id: 'ai-menter', label: 'M-Ent', type: ActionBarButtonType.altCombo, value: 'M-Enter'),
      ]),
      ActionBarGroup(id: 'ai-nav', name: 'Navigate', buttons: [
        ActionBarButton(id: 'ai-left', label: '←', type: ActionBarButtonType.specialKey, value: 'Left', iconName: 'arrow_left'),
        ActionBarButton(id: 'ai-up', label: '↑', type: ActionBarButtonType.specialKey, value: 'Up', iconName: 'arrow_drop_up'),
        ActionBarButton(id: 'ai-down', label: '↓', type: ActionBarButtonType.specialKey, value: 'Down', iconName: 'arrow_drop_down'),
        ActionBarButton(id: 'ai-right', label: '→', type: ActionBarButtonType.specialKey, value: 'Right', iconName: 'arrow_right'),
        ActionBarButton(id: 'ai-cr', label: 'C-R', type: ActionBarButtonType.ctrlCombo, value: 'C-r'),
      ]),
      ActionBarGroup(id: 'ai-emacs', name: 'Emacs', buttons: [
        ActionBarButton(id: 'ai-ca', label: 'C-A', type: ActionBarButtonType.ctrlCombo, value: 'C-a'),
        ActionBarButton(id: 'ai-ce', label: 'C-E', type: ActionBarButtonType.ctrlCombo, value: 'C-e'),
        ActionBarButton(id: 'ai-ck', label: 'C-K', type: ActionBarButtonType.ctrlCombo, value: 'C-k'),
        ActionBarButton(id: 'ai-cu', label: 'C-U', type: ActionBarButtonType.ctrlCombo, value: 'C-u'),
        ActionBarButton(id: 'ai-cy', label: 'C-Y', type: ActionBarButtonType.ctrlCombo, value: 'C-y'),
      ]),
      ActionBarGroup(id: 'ai-edit', name: 'Edit', buttons: [
        ActionBarButton(id: 'ai-ctrl-mod', label: 'CTRL', type: ActionBarButtonType.modifier, value: 'ctrl'),
        ActionBarButton(id: 'ai-alt-mod', label: 'ALT', type: ActionBarButtonType.modifier, value: 'alt'),
        ActionBarButton(id: 'ai-cd', label: 'C-D', type: ActionBarButtonType.ctrlCombo, value: 'C-d'),
        ActionBarButton(id: 'ai-cl', label: 'C-L', type: ActionBarButtonType.ctrlCombo, value: 'C-l'),
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
}

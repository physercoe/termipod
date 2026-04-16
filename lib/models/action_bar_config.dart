import 'dart:convert';

/// Button type in the action bar
enum ActionBarButtonType {
  /// Special terminal key: ESC, Tab, Enter, BSpace, Up, Down, Home, End, etc.
  specialKey,

  /// Literal character: /, |, -, ~, _, .
  literal,

  /// Ctrl combo: C-c, C-d, C-l, etc.
  ctrlCombo,

  /// Alt combo: M-Enter, M-p, etc.
  altCombo,

  /// Shift combo: S-Enter, S-Tab
  shiftCombo,

  /// Modifier toggle: ctrl, alt
  modifier,

  /// Action button: file_transfer, image_transfer, snippet, direct_input
  action,

  /// Confirm button (y/n): tap sends literal + Enter, long-press sends literal only
  confirm,
}

/// A single button in an action bar group
class ActionBarButton {
  final String id;
  final String label;
  final ActionBarButtonType type;

  /// Value to send: tmux key name, literal char, or action identifier
  final String value;

  /// Optional long-press variant value
  final String? longPressValue;

  /// Optional Material icon name (used instead of label text)
  final String? iconName;

  /// Human-readable description (e.g., "Kill line (to start)" for C-U).
  /// Editable by user. Presets ship with defaults.
  final String? description;

  const ActionBarButton({
    required this.id,
    required this.label,
    required this.type,
    required this.value,
    this.longPressValue,
    this.iconName,
    this.description,
  });

  ActionBarButton copyWith({
    String? id,
    String? label,
    ActionBarButtonType? type,
    String? value,
    String? longPressValue,
    String? iconName,
    String? description,
    bool clearLongPressValue = false,
    bool clearIconName = false,
    bool clearDescription = false,
  }) {
    return ActionBarButton(
      id: id ?? this.id,
      label: label ?? this.label,
      type: type ?? this.type,
      value: value ?? this.value,
      longPressValue:
          clearLongPressValue ? null : (longPressValue ?? this.longPressValue),
      iconName: clearIconName ? null : (iconName ?? this.iconName),
      description:
          clearDescription ? null : (description ?? this.description),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'label': label,
        'type': type.name,
        'value': value,
        if (longPressValue != null) 'longPressValue': longPressValue,
        if (iconName != null) 'iconName': iconName,
        if (description != null) 'description': description,
      };

  factory ActionBarButton.fromJson(Map<String, dynamic> json) {
    return ActionBarButton(
      id: json['id'] as String,
      label: json['label'] as String,
      type: ActionBarButtonType.values.byName(json['type'] as String),
      value: json['value'] as String,
      longPressValue: json['longPressValue'] as String?,
      iconName: json['iconName'] as String?,
      description: json['description'] as String?,
    );
  }

  /// Get the display description: user-set description, or built-in default.
  String get displayDescription => description ?? defaultDescriptions[value] ?? '';

  /// Built-in descriptions for common terminal keys and combos.
  /// Used as fallback when [description] is null.
  static const defaultDescriptions = <String, String>{
    // Special keys
    'Escape': 'Cancel / Back',
    'Tab': 'Autocomplete',
    'BTab': 'Shift+Tab / Reverse',
    'Enter': 'Execute / Confirm',
    'Space': 'Space',
    'BSpace': 'Backspace',
    'Home': 'Move to line start',
    'End': 'Move to line end',
    'Left': 'Move left',
    'Right': 'Move right',
    'Up': 'Previous / Scroll up',
    'Down': 'Next / Scroll down',
    'PPage': 'Page up / Scroll back',
    'NPage': 'Page down / Scroll forward',
    'F1': 'Help',
    'F2': 'Rename / Edit',
    'F3': 'Search',
    'F4': 'Close / End',
    'F5': 'Refresh / Reload',
    'F6': 'Function key 6',
    'F7': 'Function key 7',
    'F8': 'Function key 8',
    'F9': 'Function key 9',
    'F10': 'Menu / Function 10',
    'F11': 'Fullscreen / Function 11',
    'F12': 'Devtools / Function 12',
    'Delete': 'Delete character under cursor',
    'Insert': 'Insert / Overwrite toggle',
    'Escape Escape': 'Double Escape',
    // Ctrl combos
    'C-c': 'Interrupt / Cancel',
    'C-d': 'EOF / Exit',
    'C-l': 'Clear screen',
    'C-r': 'Reverse search history',
    'C-z': 'Suspend process',
    'C-u': 'Kill line (to start)',
    'C-k': 'Kill line (to end)',
    'C-y': 'Yank (paste killed text)',
    'C-a': 'Move to line start',
    'C-e': 'Move to line end',
    'C-j': 'Newline (literal)',
    'C-g': 'Cancel / Abort',
    'C-o': 'Accept line & fetch next',
    'C-p': 'Command palette',
    'C-x': 'Leader key / Cut',
    'C-v': 'Scroll down / Paste',
    'C-b': 'Scroll up / Back',
    // Ctrl combos — additional common bindings
    'C-f': 'Move right one char',
    'C-h': 'Backspace',
    'C-m': 'Carriage return',
    'C-n': 'Next history entry',
    'C-s': 'Forward history search',
    'C-t': 'Transpose characters',
    'C-w': 'Cut previous word',
    // Tmux prefix chords (sent via multi-arg send-keys)
    'C-b c': 'tmux: new window',
    'C-b n': 'tmux: next window',
    'C-b p': 'tmux: previous window',
    'C-b d': 'tmux: detach session',
    'C-b w': 'tmux: list windows',
    'C-b ,': 'tmux: rename window',
    'C-b &': 'tmux: kill window',
    'C-b %': 'tmux: split pane vertical',
    'C-b "': 'tmux: split pane horizontal',
    'C-b o': 'tmux: next pane',
    'C-b x': 'tmux: kill pane',
    'C-b z': 'tmux: toggle zoom',
    'C-b [': 'tmux: enter copy mode',
    'C-b ]': 'tmux: paste buffer',
    'C-b ?': 'tmux: list key bindings',
    'C-b :': 'tmux: command prompt',
    'C-b s': 'tmux: list sessions',
    // Shift combos
    'S-Enter': 'Newline (no execute)',
    'S-Tab': 'Reverse autocomplete',
    // Alt combos
    'M-Enter': 'Submit (Aider)',
    'M-p': 'Previous history',
    'M-t': 'Transpose words',
    'M-u': 'Undo (nano)',
    'M-e': 'Redo (nano)',
    'M-a': 'Set mark (nano)',
    'M-6': 'Copy line (nano)',
    'M-w': 'Next match (nano)',
    // Modifiers
    'ctrl': 'Hold Ctrl for next key',
    'alt': 'Hold Alt for next key',
    // Confirm keys (tap = literal+Enter, long-press = literal only)
    'y': 'Confirm — yes',
    'n': 'Decline — no',
    // Common literal characters
    '/': 'Forward slash',
    '|': 'Pipe',
    '-': 'Dash / hyphen',
    '~': 'Tilde — home directory',
    '_': 'Underscore',
    '\\': 'Backslash',
    // Actions
    'snippet': 'Open snippets',
    'file_transfer': 'Upload file',
    'image_transfer': 'Upload image',
    'direct_input': 'Toggle direct input',
  };
}

/// A named group of buttons (one swipeable page)
class ActionBarGroup {
  final String id;
  final String name;
  final List<ActionBarButton> buttons;

  const ActionBarGroup({
    required this.id,
    required this.name,
    required this.buttons,
  });

  ActionBarGroup copyWith({
    String? id,
    String? name,
    List<ActionBarButton>? buttons,
  }) {
    return ActionBarGroup(
      id: id ?? this.id,
      name: name ?? this.name,
      buttons: buttons ?? this.buttons,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'buttons': buttons.map((b) => b.toJson()).toList(),
      };

  factory ActionBarGroup.fromJson(Map<String, dynamic> json) {
    return ActionBarGroup(
      id: json['id'] as String,
      name: json['name'] as String,
      buttons: (json['buttons'] as List)
          .map((b) => ActionBarButton.fromJson(b as Map<String, dynamic>))
          .toList(),
    );
  }
}

/// A complete toolbar profile (built-in or custom)
class ActionBarProfile {
  final String id;
  final String name;
  final List<ActionBarGroup> groups;
  final bool isBuiltIn;

  const ActionBarProfile({
    required this.id,
    required this.name,
    required this.groups,
    this.isBuiltIn = false,
  });

  ActionBarProfile copyWith({
    String? id,
    String? name,
    List<ActionBarGroup>? groups,
    bool? isBuiltIn,
  }) {
    return ActionBarProfile(
      id: id ?? this.id,
      name: name ?? this.name,
      groups: groups ?? this.groups,
      isBuiltIn: isBuiltIn ?? this.isBuiltIn,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'groups': groups.map((g) => g.toJson()).toList(),
        'isBuiltIn': isBuiltIn,
      };

  factory ActionBarProfile.fromJson(Map<String, dynamic> json) {
    return ActionBarProfile(
      id: json['id'] as String,
      name: json['name'] as String,
      groups: (json['groups'] as List)
          .map((g) => ActionBarGroup.fromJson(g as Map<String, dynamic>))
          .toList(),
      isBuiltIn: json['isBuiltIn'] as bool? ?? false,
    );
  }

  /// Serialize a list of profiles to JSON string
  static String encodeList(List<ActionBarProfile> profiles) {
    return jsonEncode(profiles.map((p) => p.toJson()).toList());
  }

  /// Deserialize a list of profiles from JSON string
  static List<ActionBarProfile> decodeList(String jsonStr) {
    final list = jsonDecode(jsonStr) as List;
    return list
        .map((p) => ActionBarProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }
}

// Snippet and SnippetVariable classes are defined in
// lib/providers/snippet_provider.dart

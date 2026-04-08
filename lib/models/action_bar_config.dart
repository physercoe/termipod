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

  /// Action button: file_transfer, image_transfer, snippet, command_menu, direct_input
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

  const ActionBarButton({
    required this.id,
    required this.label,
    required this.type,
    required this.value,
    this.longPressValue,
    this.iconName,
  });

  ActionBarButton copyWith({
    String? id,
    String? label,
    ActionBarButtonType? type,
    String? value,
    String? longPressValue,
    String? iconName,
    bool clearLongPressValue = false,
    bool clearIconName = false,
  }) {
    return ActionBarButton(
      id: id ?? this.id,
      label: label ?? this.label,
      type: type ?? this.type,
      value: value ?? this.value,
      longPressValue:
          clearLongPressValue ? null : (longPressValue ?? this.longPressValue),
      iconName: clearIconName ? null : (iconName ?? this.iconName),
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'label': label,
        'type': type.name,
        'value': value,
        if (longPressValue != null) 'longPressValue': longPressValue,
        if (iconName != null) 'iconName': iconName,
      };

  factory ActionBarButton.fromJson(Map<String, dynamic> json) {
    return ActionBarButton(
      id: json['id'] as String,
      label: json['label'] as String,
      type: ActionBarButtonType.values.byName(json['type'] as String),
      value: json['value'] as String,
      longPressValue: json['longPressValue'] as String?,
      iconName: json['iconName'] as String?,
    );
  }
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

/// A slash command or menu item in the command palette
class CommandMenuItem {
  final String label;
  final String? description;
  final String command;
  final bool sendImmediately;
  final String category;

  const CommandMenuItem({
    required this.label,
    this.description,
    required this.command,
    this.sendImmediately = false,
    this.category = 'agent',
  });

  Map<String, dynamic> toJson() => {
        'label': label,
        if (description != null) 'description': description,
        'command': command,
        'sendImmediately': sendImmediately,
        'category': category,
      };

  factory CommandMenuItem.fromJson(Map<String, dynamic> json) {
    return CommandMenuItem(
      label: json['label'] as String,
      description: json['description'] as String?,
      command: json['command'] as String,
      sendImmediately: json['sendImmediately'] as bool? ?? false,
      category: json['category'] as String? ?? 'agent',
    );
  }
}

/// A complete toolbar profile (built-in or custom)
class ActionBarProfile {
  final String id;
  final String name;
  final List<ActionBarGroup> groups;
  final List<CommandMenuItem> slashCommands;
  final bool isBuiltIn;

  const ActionBarProfile({
    required this.id,
    required this.name,
    required this.groups,
    this.slashCommands = const [],
    this.isBuiltIn = false,
  });

  ActionBarProfile copyWith({
    String? id,
    String? name,
    List<ActionBarGroup>? groups,
    List<CommandMenuItem>? slashCommands,
    bool? isBuiltIn,
  }) {
    return ActionBarProfile(
      id: id ?? this.id,
      name: name ?? this.name,
      groups: groups ?? this.groups,
      slashCommands: slashCommands ?? this.slashCommands,
      isBuiltIn: isBuiltIn ?? this.isBuiltIn,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'groups': groups.map((g) => g.toJson()).toList(),
        'slashCommands': slashCommands.map((c) => c.toJson()).toList(),
        'isBuiltIn': isBuiltIn,
      };

  factory ActionBarProfile.fromJson(Map<String, dynamic> json) {
    return ActionBarProfile(
      id: json['id'] as String,
      name: json['name'] as String,
      groups: (json['groups'] as List)
          .map((g) => ActionBarGroup.fromJson(g as Map<String, dynamic>))
          .toList(),
      slashCommands: (json['slashCommands'] as List?)
              ?.map(
                  (c) => CommandMenuItem.fromJson(c as Map<String, dynamic>))
              .toList() ??
          [],
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

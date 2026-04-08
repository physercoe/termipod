import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/action_bar_config.dart';
import '../models/action_bar_presets.dart';

/// Action bar state
class ActionBarState {
  /// Active profile ID
  final String activeProfileId;

  /// All profiles (built-in + custom)
  final List<ActionBarProfile> profiles;

  /// Current page index (swipe position)
  final int currentPage;

  /// Whether CTRL modifier is armed
  final bool ctrlArmed;

  /// Whether ALT modifier is armed
  final bool altArmed;

  /// Whether CTRL modifier is locked (double-tap)
  final bool ctrlLocked;

  /// Whether ALT modifier is locked (double-tap)
  final bool altLocked;

  /// Input mode: true = compose (default), false = direct input
  final bool composeMode;

  /// Command history (recent composed inputs)
  final List<String> commandHistory;

  /// Suggested profile ID based on pane_current_command (null = no suggestion)
  final String? suggestedProfileId;

  const ActionBarState({
    this.activeProfileId = ActionBarPresets.defaultProfileId,
    this.profiles = const [],
    this.currentPage = 0,
    this.ctrlArmed = false,
    this.altArmed = false,
    this.ctrlLocked = false,
    this.altLocked = false,
    this.composeMode = true,
    this.commandHistory = const [],
    this.suggestedProfileId,
  });

  /// Get the active profile
  ActionBarProfile get activeProfile {
    for (final p in profiles) {
      if (p.id == activeProfileId) return p;
    }
    // Fallback to first profile or a default
    if (profiles.isNotEmpty) return profiles.first;
    return ActionBarPresets.claudeCode;
  }

  /// Get the active groups
  List<ActionBarGroup> get activeGroups => activeProfile.groups;

  /// Get the active slash commands
  List<CommandMenuItem> get activeSlashCommands => activeProfile.slashCommands;

  ActionBarState copyWith({
    String? activeProfileId,
    List<ActionBarProfile>? profiles,
    int? currentPage,
    bool? ctrlArmed,
    bool? altArmed,
    bool? ctrlLocked,
    bool? altLocked,
    bool? composeMode,
    List<String>? commandHistory,
    String? suggestedProfileId,
    bool clearSuggestion = false,
  }) {
    return ActionBarState(
      activeProfileId: activeProfileId ?? this.activeProfileId,
      profiles: profiles ?? this.profiles,
      currentPage: currentPage ?? this.currentPage,
      ctrlArmed: ctrlArmed ?? this.ctrlArmed,
      altArmed: altArmed ?? this.altArmed,
      ctrlLocked: ctrlLocked ?? this.ctrlLocked,
      altLocked: altLocked ?? this.altLocked,
      composeMode: composeMode ?? this.composeMode,
      commandHistory: commandHistory ?? this.commandHistory,
      suggestedProfileId: clearSuggestion
          ? null
          : (suggestedProfileId ?? this.suggestedProfileId),
    );
  }
}

// SharedPreferences keys
const _keyActiveProfile = 'settings_action_bar_active_profile';
const _keyCustomProfiles = 'settings_action_bar_custom_profiles';
const _keyDeletedPresets = 'settings_action_bar_deleted_presets';
const _keyComposeMode = 'settings_action_bar_compose_mode';
const _keyCommandHistory = 'settings_action_bar_command_history';
const _maxHistoryItems = 50;

class ActionBarNotifier extends Notifier<ActionBarState> {
  @override
  ActionBarState build() {
    _loadAsync();
    return ActionBarState(
      profiles: ActionBarPresets.all,
    );
  }

  Future<void> _loadAsync() async {
    final prefs = await SharedPreferences.getInstance();

    final activeId =
        prefs.getString(_keyActiveProfile) ?? ActionBarPresets.defaultProfileId;
    final composeMode = prefs.getBool(_keyComposeMode) ?? true;

    // Load custom profiles
    final customJson = prefs.getString(_keyCustomProfiles);
    List<ActionBarProfile> customProfiles = [];
    if (customJson != null) {
      try {
        customProfiles = ActionBarProfile.decodeList(customJson);
      } catch (_) {
        // Ignore corrupt data
      }
    }

    // Load deleted preset IDs
    final deletedJson = prefs.getString(_keyDeletedPresets);
    Set<String> deletedPresetIds = {};
    if (deletedJson != null) {
      try {
        deletedPresetIds = (jsonDecode(deletedJson) as List).cast<String>().toSet();
      } catch (_) {}
    }

    // Load command history
    final historyJson = prefs.getString(_keyCommandHistory);
    List<String> history = [];
    if (historyJson != null) {
      try {
        history = (jsonDecode(historyJson) as List).cast<String>();
      } catch (_) {
        // Ignore corrupt data
      }
    }

    // Merge: start with presets (excluding deleted), override with custom
    // versions (same ID), then append user-created profiles (new IDs).
    final customIds = customProfiles.map((p) => p.id).toSet();
    final presetIds = ActionBarPresets.all.map((p) => p.id).toSet();
    final allProfiles = <ActionBarProfile>[
      for (final preset in ActionBarPresets.all)
        if (!deletedPresetIds.contains(preset.id))
          if (customIds.contains(preset.id))
            customProfiles.firstWhere((p) => p.id == preset.id)
          else
            preset,
      // Append user-created profiles (IDs not in presets)
      for (final custom in customProfiles)
        if (!presetIds.contains(custom.id)) custom,
    ];

    state = state.copyWith(
      activeProfileId: activeId,
      profiles: allProfiles,
      composeMode: composeMode,
      commandHistory: history,
    );
  }

  // ---------------------------------------------------------------------------
  // Profile management
  // ---------------------------------------------------------------------------

  /// Switch active profile
  Future<void> setActiveProfile(String profileId) async {
    state = state.copyWith(activeProfileId: profileId, currentPage: 0);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyActiveProfile, profileId);
  }

  /// Add a custom profile
  Future<void> addCustomProfile(ActionBarProfile profile) async {
    final updated = [...state.profiles, profile];
    state = state.copyWith(profiles: updated);
    await _saveCustomProfiles();
  }

  /// Update an existing profile (custom only)
  Future<void> updateProfile(ActionBarProfile profile) async {
    final updated = state.profiles.map((p) {
      if (p.id == profile.id) return profile;
      return p;
    }).toList();
    state = state.copyWith(profiles: updated);
    await _saveCustomProfiles();
  }

  /// Delete a profile (preset or custom). Built-in profiles cannot be deleted.
  Future<void> deleteProfile(String profileId) async {
    // Prevent deleting the built-in General Terminal profile
    final profile = state.profiles.firstWhere(
      (p) => p.id == profileId,
      orElse: () => state.activeProfile,
    );
    if (profile.isBuiltIn) return;

    final updated = state.profiles.where((p) => p.id != profileId).toList();
    state = state.copyWith(profiles: updated);
    if (state.activeProfileId == profileId) {
      await setActiveProfile(ActionBarPresets.defaultProfileId);
    }

    // Track deleted presets so they don't reappear on reload
    final isPreset = ActionBarPresets.all.any((p) => p.id == profileId);
    if (isPreset) {
      await _addDeletedPreset(profileId);
    }
    await _saveCustomProfiles();
  }

  Future<void> _addDeletedPreset(String presetId) async {
    final prefs = await SharedPreferences.getInstance();
    final json = prefs.getString(_keyDeletedPresets);
    final ids = <String>{};
    if (json != null) {
      try {
        ids.addAll((jsonDecode(json) as List).cast<String>());
      } catch (_) {}
    }
    ids.add(presetId);
    await prefs.setString(_keyDeletedPresets, jsonEncode(ids.toList()));
  }

  /// Reset a built-in profile to its default configuration
  Future<void> resetProfileToDefault(String profileId) async {
    final builtIn = ActionBarPresets.getById(profileId);
    if (builtIn == null) return;
    final updated = state.profiles.map((p) {
      if (p.id == profileId) return builtIn;
      return p;
    }).toList();
    state = state.copyWith(profiles: updated);
    await _saveCustomProfiles();
  }

  Future<void> _saveCustomProfiles() async {
    final custom = state.profiles.where((p) => !p.isBuiltIn).toList();
    final prefs = await SharedPreferences.getInstance();
    if (custom.isEmpty) {
      await prefs.remove(_keyCustomProfiles);
    } else {
      await prefs.setString(
          _keyCustomProfiles, ActionBarProfile.encodeList(custom));
    }
  }

  // ---------------------------------------------------------------------------
  // Group editing (within active profile)
  // ---------------------------------------------------------------------------

  /// Reorder groups in the active profile
  Future<void> reorderGroups(int oldIndex, int newIndex) async {
    final groups = [...state.activeProfile.groups];
    final item = groups.removeAt(oldIndex);
    groups.insert(newIndex, item);
    final updated = state.activeProfile.copyWith(groups: groups);
    await updateProfile(updated);
  }

  /// Add a group to the active profile
  Future<void> addGroup(ActionBarGroup group) async {
    final groups = [...state.activeProfile.groups, group];
    final updated = state.activeProfile.copyWith(groups: groups);
    await updateProfile(updated);
  }

  /// Update a group in the active profile
  Future<void> updateGroup(ActionBarGroup group) async {
    final groups = state.activeProfile.groups.map((g) {
      if (g.id == group.id) return group;
      return g;
    }).toList();
    final updated = state.activeProfile.copyWith(groups: groups);
    await updateProfile(updated);
  }

  /// Delete a group from the active profile
  Future<void> deleteGroup(String groupId) async {
    final groups =
        state.activeProfile.groups.where((g) => g.id != groupId).toList();
    final updated = state.activeProfile.copyWith(groups: groups);
    await updateProfile(updated);
    // Adjust current page if needed
    if (state.currentPage >= groups.length && groups.isNotEmpty) {
      state = state.copyWith(currentPage: groups.length - 1);
    }
  }

  // ---------------------------------------------------------------------------
  // Page / modifier state
  // ---------------------------------------------------------------------------

  void setCurrentPage(int page) {
    state = state.copyWith(currentPage: page);
  }

  /// Toggle CTRL modifier. Returns true if now armed.
  bool toggleCtrl() {
    if (state.ctrlLocked) {
      // Unlock
      state = state.copyWith(ctrlArmed: false, ctrlLocked: false);
      return false;
    } else if (state.ctrlArmed) {
      // Already armed → lock (double-tap)
      state = state.copyWith(ctrlLocked: true);
      return true;
    } else {
      // Arm
      state = state.copyWith(ctrlArmed: true);
      return true;
    }
  }

  /// Toggle ALT modifier. Returns true if now armed.
  bool toggleAlt() {
    if (state.altLocked) {
      state = state.copyWith(altArmed: false, altLocked: false);
      return false;
    } else if (state.altArmed) {
      state = state.copyWith(altLocked: true);
      return true;
    } else {
      state = state.copyWith(altArmed: true);
      return true;
    }
  }

  /// Consume armed modifiers (after sending a combined key).
  /// Locked modifiers stay active.
  void consumeModifiers() {
    state = state.copyWith(
      ctrlArmed: state.ctrlLocked,
      altArmed: state.altLocked,
    );
  }

  /// Reset all modifiers
  void resetModifiers() {
    state = state.copyWith(
      ctrlArmed: false,
      altArmed: false,
      ctrlLocked: false,
      altLocked: false,
    );
  }

  /// Build tmux key string with current modifiers applied
  /// Returns null if no modifiers are armed, or the combined key string.
  String? applyModifiers(String baseKey) {
    if (!state.ctrlArmed && !state.altArmed) return null;
    final mods = <String>[];
    if (state.ctrlArmed) mods.add('C');
    if (state.altArmed) mods.add('M');
    consumeModifiers();
    return '${mods.join('-')}-$baseKey';
  }

  // ---------------------------------------------------------------------------
  // Input mode
  // ---------------------------------------------------------------------------

  Future<void> setComposeMode(bool compose) async {
    state = state.copyWith(composeMode: compose);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_keyComposeMode, compose);
  }

  void toggleInputMode() {
    setComposeMode(!state.composeMode);
  }

  // ---------------------------------------------------------------------------
  // Command history
  // ---------------------------------------------------------------------------

  Future<void> addToHistory(String command) async {
    if (command.trim().isEmpty) return;
    final history = [
      command,
      ...state.commandHistory.where((h) => h != command),
    ];
    final trimmed = history.length > _maxHistoryItems
        ? history.sublist(0, _maxHistoryItems)
        : history;
    state = state.copyWith(commandHistory: trimmed);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyCommandHistory, jsonEncode(trimmed));
  }

  Future<void> clearHistory() async {
    state = state.copyWith(commandHistory: []);
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_keyCommandHistory);
  }

  // ---------------------------------------------------------------------------
  // Profile auto-detection suggestion
  // ---------------------------------------------------------------------------

  /// Update suggestion based on pane_current_command
  void updateSuggestion(String? currentCommand) {
    final suggested = ActionBarPresets.detectProfileId(currentCommand);
    // Only suggest if different from current profile
    if (suggested != null && suggested != state.activeProfileId) {
      state = state.copyWith(suggestedProfileId: suggested);
    } else {
      state = state.copyWith(clearSuggestion: true);
    }
  }

  /// Dismiss the suggestion banner
  void dismissSuggestion() {
    state = state.copyWith(clearSuggestion: true);
  }

  /// Accept the suggestion: switch to suggested profile
  Future<void> acceptSuggestion() async {
    final suggested = state.suggestedProfileId;
    if (suggested != null) {
      await setActiveProfile(suggested);
    }
    state = state.copyWith(clearSuggestion: true);
  }
}

/// Action bar provider
final actionBarProvider =
    NotifierProvider<ActionBarNotifier, ActionBarState>(() {
  return ActionBarNotifier();
});

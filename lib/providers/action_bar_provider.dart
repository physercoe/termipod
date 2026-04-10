import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/action_bar_config.dart';
import '../models/action_bar_presets.dart';

/// Action bar state
class ActionBarState {
  /// Active profile ID — global default used by panels that don't have
  /// their own entry in [activeProfileByPanel] yet (e.g. a freshly opened
  /// pane before the user picks a profile or before auto-detect fires).
  final String activeProfileId;

  /// Per-panel profile override. Keyed by a caller-defined panel key
  /// (currently `${connectionId}|${paneId}`) → profile id. When a panel
  /// has an entry here, it wins over [activeProfileId]. A panel with no
  /// entry falls back to the global [activeProfileId], which means new
  /// panes inherit "the last profile the user chose somewhere" until
  /// they diverge.
  final Map<String, String> activeProfileByPanel;

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

  const ActionBarState({
    this.activeProfileId = ActionBarPresets.defaultProfileId,
    this.activeProfileByPanel = const {},
    this.profiles = const [],
    this.currentPage = 0,
    this.ctrlArmed = false,
    this.altArmed = false,
    this.ctrlLocked = false,
    this.altLocked = false,
    this.composeMode = true,
  });

  /// Resolve the effective profile id for a panel. When [panelKey] is
  /// null or has no override, falls back to [activeProfileId].
  String profileIdForPanel(String? panelKey) {
    if (panelKey == null) return activeProfileId;
    return activeProfileByPanel[panelKey] ?? activeProfileId;
  }

  /// Resolve the [ActionBarProfile] for a panel, using the same lookup
  /// rules as [profileIdForPanel] but hardened against missing/deleted
  /// profiles.
  ActionBarProfile profileForPanel(String? panelKey) {
    final id = profileIdForPanel(panelKey);
    for (final p in profiles) {
      if (p.id == id) return p;
    }
    if (profiles.isNotEmpty) return profiles.first;
    return ActionBarPresets.claudeCode;
  }

  /// Groups for a panel's effective profile.
  List<ActionBarGroup> groupsForPanel(String? panelKey) =>
      profileForPanel(panelKey).groups;

  /// Get the active profile (global default — prefer [profileForPanel]
  /// when a panel key is available).
  ActionBarProfile get activeProfile => profileForPanel(null);

  /// Get the active groups (global default — prefer [groupsForPanel]
  /// when a panel key is available).
  List<ActionBarGroup> get activeGroups => activeProfile.groups;

  ActionBarState copyWith({
    String? activeProfileId,
    Map<String, String>? activeProfileByPanel,
    List<ActionBarProfile>? profiles,
    int? currentPage,
    bool? ctrlArmed,
    bool? altArmed,
    bool? ctrlLocked,
    bool? altLocked,
    bool? composeMode,
  }) {
    return ActionBarState(
      activeProfileId: activeProfileId ?? this.activeProfileId,
      activeProfileByPanel:
          activeProfileByPanel ?? this.activeProfileByPanel,
      profiles: profiles ?? this.profiles,
      currentPage: currentPage ?? this.currentPage,
      ctrlArmed: ctrlArmed ?? this.ctrlArmed,
      altArmed: altArmed ?? this.altArmed,
      ctrlLocked: ctrlLocked ?? this.ctrlLocked,
      altLocked: altLocked ?? this.altLocked,
      composeMode: composeMode ?? this.composeMode,
    );
  }
}

// SharedPreferences keys
const _keyActiveProfile = 'settings_action_bar_active_profile';
const _keyCustomProfiles = 'settings_action_bar_custom_profiles';
const _keyDeletedPresets = 'settings_action_bar_deleted_presets';
const _keyComposeMode = 'settings_action_bar_compose_mode';
const _keyPanelProfiles = 'settings_action_bar_panel_profiles';

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

    // Load per-panel profile overrides
    final panelJson = prefs.getString(_keyPanelProfiles);
    Map<String, String> panelProfiles = {};
    if (panelJson != null) {
      try {
        panelProfiles = (jsonDecode(panelJson) as Map)
            .map((k, v) => MapEntry(k as String, v as String));
      } catch (_) {}
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

    // Prune panel overrides that reference profiles that no longer
    // exist (e.g. the user deleted a custom profile that some pane
    // was pinned to). Falling back to the global default is safer
    // than crashing on next render.
    final liveIds = allProfiles.map((p) => p.id).toSet();
    panelProfiles.removeWhere((_, id) => !liveIds.contains(id));

    state = state.copyWith(
      activeProfileId: activeId,
      activeProfileByPanel: panelProfiles,
      profiles: allProfiles,
      composeMode: composeMode,
    );
  }

  Future<void> _savePanelProfiles() async {
    final prefs = await SharedPreferences.getInstance();
    if (state.activeProfileByPanel.isEmpty) {
      await prefs.remove(_keyPanelProfiles);
    } else {
      await prefs.setString(
          _keyPanelProfiles, jsonEncode(state.activeProfileByPanel));
    }
  }

  // ---------------------------------------------------------------------------
  // Profile management
  // ---------------------------------------------------------------------------

  /// Switch the global default active profile. New panels (panels with
  /// no entry in [ActionBarState.activeProfileByPanel]) will use this.
  /// Panels that already have an override are left untouched — call
  /// [setActiveProfileForPanel] to change a specific panel.
  Future<void> setActiveProfile(String profileId) async {
    state = state.copyWith(activeProfileId: profileId, currentPage: 0);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyActiveProfile, profileId);
  }

  /// Switch the active profile for a single panel (by [panelKey]). The
  /// caller is responsible for building a stable key — the current
  /// convention is `${connectionId}|${paneId}`, so profiles are scoped
  /// per tmux pane across reconnects.
  ///
  /// This is the preferred entry point from the action bar / profile
  /// sheet when they know which pane they belong to. Use
  /// [setActiveProfile] only when there is no panel context (e.g. app
  /// startup default, first-run onboarding).
  Future<void> setActiveProfileForPanel(
      String panelKey, String profileId) async {
    final updated = Map<String, String>.from(state.activeProfileByPanel);
    updated[panelKey] = profileId;
    state = state.copyWith(
      activeProfileByPanel: updated,
      // Keep the global default in sync so brand-new panels inherit the
      // most recently chosen profile rather than whatever was saved at
      // app start.
      activeProfileId: profileId,
      currentPage: 0,
    );
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyActiveProfile, profileId);
    await _savePanelProfiles();
  }

  /// Remove a panel's override (used when a pane is closed so the map
  /// doesn't grow forever). Does nothing if there is no entry.
  Future<void> clearPanelProfile(String panelKey) async {
    if (!state.activeProfileByPanel.containsKey(panelKey)) return;
    final updated = Map<String, String>.from(state.activeProfileByPanel)
      ..remove(panelKey);
    state = state.copyWith(activeProfileByPanel: updated);
    await _savePanelProfiles();
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
    // Also drop any panel pins that still reference the now-gone
    // profile so those panels fall back to the global default.
    final prunedPanels = Map<String, String>.from(state.activeProfileByPanel)
      ..removeWhere((_, id) => id == profileId);
    state = state.copyWith(
      profiles: updated,
      activeProfileByPanel: prunedPanels,
    );
    if (state.activeProfileId == profileId) {
      await setActiveProfile(ActionBarPresets.defaultProfileId);
    }

    // Track deleted presets so they don't reappear on reload
    final isPreset = ActionBarPresets.all.any((p) => p.id == profileId);
    if (isPreset) {
      await _addDeletedPreset(profileId);
    }
    await _saveCustomProfiles();
    await _savePanelProfiles();
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
}

/// Action bar provider
final actionBarProvider =
    NotifierProvider<ActionBarNotifier, ActionBarState>(() {
  return ActionBarNotifier();
});

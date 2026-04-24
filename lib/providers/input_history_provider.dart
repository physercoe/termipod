import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Input history — composed text sent to the terminal via the action bar.
/// Distinct from session history (recent tmux sessions).
class InputHistoryState {
  /// Full command history (newest first)
  final List<String> items;

  final bool isLoading;

  const InputHistoryState({
    this.items = const [],
    this.isLoading = false,
  });

  /// Recent items (hot, last 10) for quick-access sheet
  List<String> get recent => items.length > 10 ? items.sublist(0, 10) : items;

  InputHistoryState copyWith({
    List<String>? items,
    bool? isLoading,
  }) {
    return InputHistoryState(
      items: items ?? this.items,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

const _storageKey = 'settings_action_bar_command_history';
const _maxHistoryItems = 200;

class InputHistoryNotifier extends Notifier<InputHistoryState> {
  @override
  InputHistoryState build() {
    _load();
    return const InputHistoryState(isLoading: true);
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final jsonStr = prefs.getString(_storageKey);
    List<String> loaded = [];
    if (jsonStr != null) {
      try {
        loaded = (jsonDecode(jsonStr) as List).cast<String>();
      } catch (_) {}
    }
    // Preserve any items that were added via add() while we were awaiting
    // SharedPreferences. Without this merge the post-await assignment would
    // clobber them, and the racing add()'s _save() would then write the
    // stale list back — so the user's send would never reach history.
    final existing = state.items;
    List<String> merged;
    if (existing.isEmpty) {
      merged = loaded;
    } else {
      // items list is newest-first — keep the racing entries at the front.
      merged = [
        ...existing,
        ...loaded.where((h) => !existing.contains(h)),
      ];
      if (merged.length > _maxHistoryItems) {
        merged = merged.sublist(0, _maxHistoryItems);
      }
    }
    state = InputHistoryState(items: merged);
    // If a race happened, re-persist so the merged view survives a cold
    // start even if the racing add()'s _save() already wrote the stale list.
    if (existing.isNotEmpty) {
      await _save();
    }
  }

  Future<void> _save() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_storageKey, jsonEncode(state.items));
  }

  /// Add a command to history (deduplicates, moves to front)
  Future<void> add(String command) async {
    if (command.trim().isEmpty) return;
    final items = [
      command,
      ...state.items.where((h) => h != command),
    ];
    final trimmed = items.length > _maxHistoryItems
        ? items.sublist(0, _maxHistoryItems)
        : items;
    state = state.copyWith(items: trimmed);
    await _save();
  }

  /// Clear all history
  Future<void> clear() async {
    state = state.copyWith(items: []);
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_storageKey);
  }

  /// Update a history item in place
  Future<void> update(String oldCommand, String newCommand) async {
    if (newCommand.trim().isEmpty) return;
    final items = state.items.map((h) => h == oldCommand ? newCommand : h).toList();
    state = state.copyWith(items: items);
    await _save();
  }

  /// Delete a single history item
  Future<void> delete(String command) async {
    state = state.copyWith(
      items: state.items.where((h) => h != command).toList(),
    );
    await _save();
  }
}

final inputHistoryProvider = NotifierProvider<InputHistoryNotifier, InputHistoryState>(
  InputHistoryNotifier.new,
);

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// History state — stores all composed inputs sent to terminal.
class HistoryState {
  /// Full command history (newest first)
  final List<String> items;

  final bool isLoading;

  const HistoryState({
    this.items = const [],
    this.isLoading = false,
  });

  /// Recent items (hot, last 10) for quick-access sheet
  List<String> get recent => items.length > 10 ? items.sublist(0, 10) : items;

  HistoryState copyWith({
    List<String>? items,
    bool? isLoading,
  }) {
    return HistoryState(
      items: items ?? this.items,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

const _storageKey = 'settings_action_bar_command_history';
const _maxHistoryItems = 200;

class HistoryNotifier extends Notifier<HistoryState> {
  @override
  HistoryState build() {
    _load();
    return const HistoryState(isLoading: true);
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final jsonStr = prefs.getString(_storageKey);
    List<String> items = [];
    if (jsonStr != null) {
      try {
        items = (jsonDecode(jsonStr) as List).cast<String>();
      } catch (_) {}
    }
    state = HistoryState(items: items);
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

  /// Delete a single history item
  Future<void> delete(String command) async {
    state = state.copyWith(
      items: state.items.where((h) => h != command).toList(),
    );
    await _save();
  }
}

final historyProvider = NotifierProvider<HistoryNotifier, HistoryState>(
  HistoryNotifier.new,
);

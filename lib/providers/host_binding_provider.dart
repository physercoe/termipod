import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Persistent mapping from a hub host id to the local Connection id that
/// drives it. Lets the host detail sheet jump straight into a terminal for
/// a hub-registered host without re-asking the user to pick or re-enter
/// connection details.
///
/// The hub never stores SSH secrets; secrets live only in the local
/// Connection (flutter_secure_storage). The binding is what lets us reach
/// the secret by hub host id.
class HostBindingsNotifier extends Notifier<Map<String, String>> {
  static const String _storageKey = 'hub_host_bindings';

  @override
  Map<String, String> build() {
    _load();
    return const <String, String>{};
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_storageKey);
    if (raw == null || raw.isEmpty) return;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map) {
        state = decoded.map((k, v) => MapEntry(k.toString(), v.toString()));
      }
    } catch (_) {
      // Corrupt value — drop it and start fresh rather than failing.
      await prefs.remove(_storageKey);
    }
  }

  Future<void> _save() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_storageKey, jsonEncode(state));
  }

  String? connectionIdFor(String hubHostId) => state[hubHostId];

  Future<void> bind(String hubHostId, String connectionId) async {
    state = {...state, hubHostId: connectionId};
    await _save();
  }

  Future<void> unbind(String hubHostId) async {
    if (!state.containsKey(hubHostId)) return;
    final next = {...state}..remove(hubHostId);
    state = next;
    await _save();
  }
}

final hostBindingsProvider =
    NotifierProvider<HostBindingsNotifier, Map<String, String>>(() {
  return HostBindingsNotifier();
});

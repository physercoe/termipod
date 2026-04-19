import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

/// Device-local saved form for the Spawn Agent dialog. Presets are per
/// user per device — team-shared presets already exist as server-side
/// templates under /v1/templates/agents.
///
/// Stored as a JSON array under `hub_spawn_presets`. Keeping it in
/// SharedPreferences (not secure storage) is fine: a preset is just the
/// handle/kind/YAML, no secrets.
class SpawnPreset {
  final String id;
  final String name;
  final String handle;
  final String kind;
  final String yaml;

  const SpawnPreset({
    required this.id,
    required this.name,
    required this.handle,
    required this.kind,
    required this.yaml,
  });

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'handle': handle,
        'kind': kind,
        'yaml': yaml,
      };

  factory SpawnPreset.fromJson(Map<String, dynamic> j) => SpawnPreset(
        id: j['id']?.toString() ?? '',
        name: j['name']?.toString() ?? '',
        handle: j['handle']?.toString() ?? '',
        kind: j['kind']?.toString() ?? '',
        yaml: j['yaml']?.toString() ?? '',
      );
}

const _kPresetsKey = 'hub_spawn_presets';

class SpawnPresetService {
  Future<List<SpawnPreset>> load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_kPresetsKey);
    if (raw == null || raw.isEmpty) return const [];
    try {
      final list = jsonDecode(raw) as List;
      return list
          .whereType<Map<String, dynamic>>()
          .map(SpawnPreset.fromJson)
          .toList();
    } catch (_) {
      return const [];
    }
  }

  Future<void> save(List<SpawnPreset> presets) async {
    final prefs = await SharedPreferences.getInstance();
    final raw = jsonEncode(presets.map((p) => p.toJson()).toList());
    await prefs.setString(_kPresetsKey, raw);
  }

  /// Append or replace by id. Caller generates the id (DateTime-based
  /// is fine — presets aren't synced cross-device).
  Future<List<SpawnPreset>> upsert(SpawnPreset preset) async {
    final items = [...await load()];
    final idx = items.indexWhere((p) => p.id == preset.id);
    if (idx < 0) {
      items.add(preset);
    } else {
      items[idx] = preset;
    }
    await save(items);
    return items;
  }

  Future<List<SpawnPreset>> delete(String id) async {
    final items = (await load()).where((p) => p.id != id).toList();
    await save(items);
    return items;
  }
}

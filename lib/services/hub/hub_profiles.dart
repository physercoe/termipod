import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Persistent storage for the user's saved hub connection profiles.
///
/// MVP shape: any number of profiles, one active at a time. Each profile
/// is a (baseUrl, teamId, token) triple plus a stable `id` and a
/// user-editable display `name`. Background SSE follows the active
/// profile only.
///
/// Storage layout:
/// - SharedPreferences `hub_profiles_v1` → JSON list of
///   `{id, name, baseUrl, teamId}`
/// - SharedPreferences `hub_active_profile_id` → id of the active
///   profile, or empty when no profiles exist
/// - flutter_secure_storage `hub_token_<id>` → the bearer token for the
///   profile with that id
///
/// Tokens stay out of the JSON list because they belong in the device
/// keychain, never in plain prefs.
class HubProfile {
  final String id;
  final String name;
  final String baseUrl;
  final String teamId;

  const HubProfile({
    required this.id,
    required this.name,
    required this.baseUrl,
    required this.teamId,
  });

  HubProfile copyWith({String? name, String? baseUrl, String? teamId}) =>
      HubProfile(
        id: id,
        name: name ?? this.name,
        baseUrl: baseUrl ?? this.baseUrl,
        teamId: teamId ?? this.teamId,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'baseUrl': baseUrl,
        'teamId': teamId,
      };

  factory HubProfile.fromJson(Map<String, dynamic> j) => HubProfile(
        id: (j['id'] ?? '').toString(),
        name: (j['name'] ?? '').toString(),
        baseUrl: (j['baseUrl'] ?? '').toString(),
        teamId: (j['teamId'] ?? '').toString(),
      );

  /// Default display name when the user hasn't set one. Renders as
  /// `team @ host` so the menu can disambiguate two profiles on the
  /// same hub or two hubs with the same team id.
  static String defaultName({required String baseUrl, required String teamId}) {
    final host = Uri.tryParse(baseUrl)?.host;
    if (host == null || host.isEmpty) return teamId;
    return '$teamId @ $host';
  }
}

/// Snapshot of all saved profiles + the active id at a point in time.
/// Returned by [HubProfileStore.load]; callers treat it as immutable.
class HubProfilesSnapshot {
  final List<HubProfile> profiles;
  final String? activeId;

  const HubProfilesSnapshot({this.profiles = const [], this.activeId});

  HubProfile? get active {
    final id = activeId;
    if (id == null || id.isEmpty) return null;
    for (final p in profiles) {
      if (p.id == id) return p;
    }
    return null;
  }

  bool get isEmpty => profiles.isEmpty;
}

const _kProfilesKey = 'hub_profiles_v1';
const _kActiveIdKey = 'hub_active_profile_id';
const _kTokenKeyPrefix = 'hub_token_';

// Legacy single-profile keys (pre-multi-profile). Read once on first
// launch of a build that has multi-profile, wrapped as a single profile,
// then deleted so subsequent launches bypass migration.
const _kLegacyBaseUrlKey = 'hub_base_url';
const _kLegacyTeamIdKey = 'hub_team_id';
const _kLegacyTokenKey = 'hub_token';

class HubProfileStore {
  final FlutterSecureStorage _secure;

  HubProfileStore({FlutterSecureStorage? secure})
      : _secure = secure ?? const FlutterSecureStorage();

  /// Reads the saved profiles + active id, migrating from the
  /// single-profile schema on first run if needed. Idempotent — once
  /// migrated, the legacy keys are gone and subsequent calls see only
  /// the new schema.
  Future<HubProfilesSnapshot> load() async {
    final prefs = await SharedPreferences.getInstance();
    await _migrateLegacyIfPresent(prefs);
    final raw = prefs.getString(_kProfilesKey);
    if (raw == null || raw.isEmpty) {
      return const HubProfilesSnapshot();
    }
    List<HubProfile> profiles;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! List) return const HubProfilesSnapshot();
      profiles = [
        for (final e in decoded)
          if (e is Map) HubProfile.fromJson(e.cast<String, dynamic>()),
      ];
    } catch (_) {
      return const HubProfilesSnapshot();
    }
    final activeId = prefs.getString(_kActiveIdKey);
    return HubProfilesSnapshot(profiles: profiles, activeId: activeId);
  }

  Future<void> _migrateLegacyIfPresent(SharedPreferences prefs) async {
    if (prefs.containsKey(_kProfilesKey)) return;
    final baseUrl = prefs.getString(_kLegacyBaseUrlKey) ?? '';
    final teamId = prefs.getString(_kLegacyTeamIdKey) ?? '';
    final token = await _secure.read(key: _kLegacyTokenKey) ?? '';
    if (baseUrl.isEmpty || teamId.isEmpty || token.isEmpty) {
      // First-time install with no legacy data — write an empty list so
      // we don't re-enter migration on every cold start.
      await prefs.setString(_kProfilesKey, '[]');
      return;
    }
    final id = _newId();
    final profile = HubProfile(
      id: id,
      name: HubProfile.defaultName(baseUrl: baseUrl, teamId: teamId),
      baseUrl: baseUrl,
      teamId: teamId,
    );
    await prefs.setString(_kProfilesKey, jsonEncode([profile.toJson()]));
    await prefs.setString(_kActiveIdKey, id);
    await _secure.write(key: '$_kTokenKeyPrefix$id', value: token);
    // Drop legacy keys only after the new ones are durable.
    await prefs.remove(_kLegacyBaseUrlKey);
    await prefs.remove(_kLegacyTeamIdKey);
    await _secure.delete(key: _kLegacyTokenKey);
  }

  /// Persist the profile list. Caller is responsible for deciding the
  /// active id alongside (use [setActive]).
  Future<void> saveProfiles(List<HubProfile> profiles) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(
      _kProfilesKey,
      jsonEncode([for (final p in profiles) p.toJson()]),
    );
  }

  Future<void> setActive(String? id) async {
    final prefs = await SharedPreferences.getInstance();
    if (id == null || id.isEmpty) {
      await prefs.remove(_kActiveIdKey);
    } else {
      await prefs.setString(_kActiveIdKey, id);
    }
  }

  Future<String?> readToken(String profileId) =>
      _secure.read(key: '$_kTokenKeyPrefix$profileId');

  Future<void> writeToken(String profileId, String token) =>
      _secure.write(key: '$_kTokenKeyPrefix$profileId', value: token);

  Future<void> deleteToken(String profileId) =>
      _secure.delete(key: '$_kTokenKeyPrefix$profileId');

  /// Wipe every saved profile + every per-profile token. Used by the
  /// "logout / forget hub" flow, which is coarser than per-profile
  /// delete.
  Future<void> wipeAll() async {
    final snap = await load();
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_kProfilesKey);
    await prefs.remove(_kActiveIdKey);
    for (final p in snap.profiles) {
      await deleteToken(p.id);
    }
  }

  String _newId() {
    // Profile ids are local-only. A microsecond timestamp is collision-
    // resistant within one device and human-debuggable in prefs.
    final us = DateTime.now().microsecondsSinceEpoch;
    return 'p_${us.toRadixString(36)}';
  }

  /// Generate a fresh profile id (exposed so the provider can mint an id
  /// before persisting).
  String newId() => _newId();
}

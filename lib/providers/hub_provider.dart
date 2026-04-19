import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../services/hub/hub_client.dart';

/// SharedPreferences keys for the hub configuration. The token is *not*
/// stored here — it lives in flutter_secure_storage under [_kHubTokenKey].
const _kHubBaseUrlKey = 'hub_base_url';
const _kHubTeamIdKey = 'hub_team_id';
const _kHubTokenKey = 'hub_token';

/// Sticky in-memory state for the hub tab. We persist baseUrl/teamId/token
/// on save and rehydrate on first access so the app survives cold starts
/// without a second bootstrap.
class HubState {
  final HubConfig? config;
  final bool loading;
  final String? error;

  final List<Map<String, dynamic>> attention;
  final List<Map<String, dynamic>> hosts;
  final List<Map<String, dynamic>> agents;
  final List<Map<String, dynamic>> projects;
  final List<Map<String, dynamic>> templates;
  final List<Map<String, dynamic>> spawns;

  /// Server-declared version from /v1/_info. Null until we've probed.
  final String? serverVersion;

  const HubState({
    this.config,
    this.loading = false,
    this.error,
    this.attention = const [],
    this.hosts = const [],
    this.agents = const [],
    this.projects = const [],
    this.templates = const [],
    this.spawns = const [],
    this.serverVersion,
  });

  bool get configured => config != null && config!.isValid;

  HubState copyWith({
    HubConfig? config,
    bool? loading,
    String? error,
    List<Map<String, dynamic>>? attention,
    List<Map<String, dynamic>>? hosts,
    List<Map<String, dynamic>>? agents,
    List<Map<String, dynamic>>? projects,
    List<Map<String, dynamic>>? templates,
    List<Map<String, dynamic>>? spawns,
    String? serverVersion,
    bool clearConfig = false,
    bool clearError = false,
  }) =>
      HubState(
        config: clearConfig ? null : (config ?? this.config),
        loading: loading ?? this.loading,
        error: clearError ? null : (error ?? this.error),
        attention: attention ?? this.attention,
        hosts: hosts ?? this.hosts,
        agents: agents ?? this.agents,
        projects: projects ?? this.projects,
        templates: templates ?? this.templates,
        spawns: spawns ?? this.spawns,
        serverVersion: serverVersion ?? this.serverVersion,
      );
}

class HubNotifier extends AsyncNotifier<HubState> {
  HubClient? _client;

  @override
  Future<HubState> build() async {
    ref.onDispose(() {
      _client?.close();
      _client = null;
    });
    return _loadConfig();
  }

  Future<HubState> _loadConfig() async {
    final prefs = await SharedPreferences.getInstance();
    final baseUrl = prefs.getString(_kHubBaseUrlKey) ?? '';
    final teamId = prefs.getString(_kHubTeamIdKey) ?? '';
    const secure = FlutterSecureStorage();
    final token = await secure.read(key: _kHubTokenKey) ?? '';
    if (baseUrl.isEmpty || teamId.isEmpty || token.isEmpty) {
      return const HubState();
    }
    final cfg = HubConfig(baseUrl: baseUrl, token: token, teamId: teamId);
    _client = HubClient(cfg);
    return HubState(config: cfg);
  }

  HubClient? get client => _client;

  /// Persist the config and refresh every list. Called from the bootstrap
  /// wizard after a successful probe.
  Future<void> saveConfig({
    required String baseUrl,
    required String token,
    required String teamId,
  }) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_kHubBaseUrlKey, baseUrl);
    await prefs.setString(_kHubTeamIdKey, teamId);
    const secure = FlutterSecureStorage();
    await secure.write(key: _kHubTokenKey, value: token);

    _client?.close();
    final cfg = HubConfig(baseUrl: baseUrl, token: token, teamId: teamId);
    _client = HubClient(cfg);

    state = AsyncData(HubState(config: cfg));
    await refreshAll();
  }

  Future<void> clearConfig() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_kHubBaseUrlKey);
    await prefs.remove(_kHubTeamIdKey);
    const secure = FlutterSecureStorage();
    await secure.delete(key: _kHubTokenKey);
    _client?.close();
    _client = null;
    state = const AsyncData(HubState());
  }

  /// One-shot fetch of everything the dashboard needs. Tabs show whatever
  /// snapshot was loaded last; pull-to-refresh on the hub screen re-runs
  /// this. We don't try to be clever about partial failures — if one list
  /// throws we surface the error and leave the rest on their previous
  /// values.
  Future<void> refreshAll() async {
    final client = _client;
    if (client == null) return;
    final prev = state.value ?? const HubState();
    state = AsyncData(prev.copyWith(loading: true, clearError: true));
    try {
      final results = await Future.wait([
        client.listAttention(status: 'open'),
        client.listHosts(),
        client.listAgents(),
        client.listProjects(),
        client.listTemplates(),
        client.listSpawns(),
      ]);
      state = AsyncData(prev.copyWith(
        loading: false,
        attention: results[0],
        hosts: results[1],
        agents: results[2],
        projects: results[3],
        templates: results[4],
        spawns: results[5],
      ));
    } on HubApiError catch (e) {
      state = AsyncData(prev.copyWith(loading: false, error: e.toString()));
    } catch (e) {
      state = AsyncData(prev.copyWith(loading: false, error: e.toString()));
    }
  }

  Future<void> decide(String id, String decision, {String? reason, String? by}) async {
    final client = _client;
    if (client == null) return;
    await client.decideAttention(id, decision: decision, reason: reason, by: by);
    await _reloadAttention();
  }

  Future<void> resolve(String id, {String? reason, String? by}) async {
    final client = _client;
    if (client == null) return;
    await client.resolveAttention(id, reason: reason, by: by);
    await _reloadAttention();
  }

  Future<void> _reloadAttention() async {
    final client = _client;
    if (client == null) return;
    final items = await client.listAttention(status: 'open');
    final prev = state.value ?? const HubState();
    state = AsyncData(prev.copyWith(attention: items));
  }
}

/// Top-level Riverpod provider for the hub connection and dashboard data.
final hubProvider = AsyncNotifierProvider<HubNotifier, HubState>(
  HubNotifier.new,
);

/// Summary of a hub event as surfaced in the feed tab. We decode the
/// `parts` array into a single human-readable line so the list stays
/// scannable on a phone — the terminal UI is where the raw stream belongs.
/// A code excerpt attached to an event. `path`, `line_from`, `line_to`, and
/// `content` are all optional — the hub only guarantees `content`. The
/// feed row renders any missing fields as blanks instead of dropping the
/// whole excerpt.
class HubExcerpt {
  final String path;
  final int? lineFrom;
  final int? lineTo;
  final String content;
  const HubExcerpt({
    required this.path,
    required this.lineFrom,
    required this.lineTo,
    required this.content,
  });

  factory HubExcerpt.fromMap(Map raw) {
    int? toInt(Object? v) {
      if (v is int) return v;
      if (v is num) return v.toInt();
      if (v is String) return int.tryParse(v);
      return null;
    }

    return HubExcerpt(
      path: raw['path']?.toString() ?? raw['file']?.toString() ?? '',
      lineFrom: toInt(raw['line_from']),
      lineTo: toInt(raw['line_to']),
      content: (raw['content'] as String?) ?? '',
    );
  }
}

class HubFeedEntry {
  final String id;
  final String type;
  final String fromId;
  final DateTime? ts;
  final String preview;
  final String channelId;
  final List<HubExcerpt> excerpts;

  const HubFeedEntry({
    required this.id,
    required this.type,
    required this.fromId,
    required this.ts,
    required this.preview,
    required this.channelId,
    this.excerpts = const [],
  });

  factory HubFeedEntry.fromEvent(Map<String, dynamic> evt) {
    final parts = (evt['parts'] as List?) ?? const [];
    final preview = _previewFromParts(parts);
    final excerpts = <HubExcerpt>[];
    for (final raw in parts) {
      if (raw is! Map) continue;
      if (raw['kind'] != 'excerpt') continue;
      final ex = raw['excerpt'];
      if (ex is Map) excerpts.add(HubExcerpt.fromMap(ex));
    }
    DateTime? ts;
    final raw = evt['ts'] as String?;
    if (raw != null) {
      ts = DateTime.tryParse(raw);
    }
    return HubFeedEntry(
      id: evt['id']?.toString() ?? '',
      type: evt['type']?.toString() ?? 'message',
      fromId: evt['from_id']?.toString() ?? '',
      ts: ts,
      preview: preview,
      channelId: evt['channel_id']?.toString() ?? '',
      excerpts: excerpts,
    );
  }

  static String _previewFromParts(List<dynamic> parts) {
    for (final raw in parts) {
      if (raw is! Map) continue;
      final kind = raw['kind'];
      if (kind == 'text' && raw['text'] is String) {
        final t = (raw['text'] as String).trim();
        if (t.isNotEmpty) return t;
      } else if (kind == 'file') {
        return '[file] ${raw['file']?['uri'] ?? ''}';
      } else if (kind == 'image') {
        return '[image]';
      } else if (kind == 'excerpt') {
        final ex = raw['excerpt'];
        if (ex is Map) {
          final from = ex['line_from'];
          final to = ex['line_to'];
          final content = (ex['content'] as String?) ?? '';
          final firstLine = content.split('\n').firstWhere(
                (l) => l.trim().isNotEmpty,
                orElse: () => '',
              );
          final range = (from != null && to != null) ? ' L$from-$to' : '';
          return '[excerpt$range] ${firstLine.trim()}'.trim();
        }
        return '[excerpt]';
      } else if (kind == 'data') {
        final d = raw['data'];
        if (d != null) return '[data] ${jsonEncode(d)}';
      }
    }
    return '';
  }
}

/// Live feed for a single (project, channel) pair. Opens an SSE stream
/// backed by [HubClient.streamEvents]. The list is capped so a chatty
/// channel doesn't blow memory on a phone.
class HubFeedNotifier extends Notifier<List<HubFeedEntry>> {
  StreamSubscription<Map<String, dynamic>>? _sub;
  static const _cap = 200;

  @override
  List<HubFeedEntry> build() {
    ref.onDispose(() {
      _sub?.cancel();
      _sub = null;
    });
    return const [];
  }

  /// Subscribe to [channelId] in [projectId] on the current hub. Any
  /// previous subscription is torn down first, so callers can switch
  /// channels without leaking sockets.
  void subscribe({
    required HubClient client,
    required String projectId,
    required String channelId,
  }) {
    _sub?.cancel();
    state = const [];
    _sub = client.streamEvents(projectId, channelId).listen(
      (evt) {
        final entry = HubFeedEntry.fromEvent(evt);
        final next = [entry, ...state];
        if (next.length > _cap) next.removeRange(_cap, next.length);
        state = next;
      },
      onError: (_) {
        // Intentionally silent — the hub screen shows a banner when the
        // top-level state has an error. A flaky channel shouldn't steal
        // the main error slot.
      },
      cancelOnError: false,
    );
  }

  void stop() {
    _sub?.cancel();
    _sub = null;
    state = const [];
  }
}

final hubFeedProvider =
    NotifierProvider<HubFeedNotifier, List<HubFeedEntry>>(HubFeedNotifier.new);

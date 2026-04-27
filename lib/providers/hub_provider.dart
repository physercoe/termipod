import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../services/hub/blob_bytes_cache.dart';
import '../services/hub/hub_client.dart';
import '../services/hub/hub_read_through.dart';
import '../services/hub/hub_snapshot_cache.dart';

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

  /// When non-null, at least one of the dashboard lists above was served
  /// from the offline snapshot cache because the live fetch failed. Holds
  /// the oldest `fetchedAt` across the stale results — the Projects screen
  /// renders `HubOfflineBanner(staleSince:)` when this is set.
  final DateTime? staleSince;

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
    this.staleSince,
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
    DateTime? staleSince,
    bool clearConfig = false,
    bool clearError = false,
    bool clearStale = false,
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
        staleSince: clearStale ? null : (staleSince ?? this.staleSince),
      );
}

class _DashResult {
  final List<Map<String, dynamic>> body;
  final DateTime? staleSince;
  final String? error;
  const _DashResult({required this.body, this.staleSince, this.error});
}

class HubNotifier extends AsyncNotifier<HubState> {
  HubClient? _client;

  /// On-disk last-known-good snapshots of hub list/get responses. Kept
  /// beside the client so screens can fall back to the latest cached body
  /// when the network is down. Commit #1 only instantiates + tears down;
  /// read-through wrapping of HubClient methods lands in commit #2.
  HubSnapshotCache? _cache;

  /// Content-addressed on-disk cache for `/v1/blobs/{sha}` bytes — the
  /// binary sibling of [_cache]. Persists run images / artifact previews
  /// so offline re-opens don't hit an empty placeholder.
  BlobBytesCache? _blobCache;

  /// In-memory cache of template bodies keyed by "$category/$name". Templates
  /// only change when an operator edits the YAML/MD on the hub, so caching
  /// aggressively and exposing a forceRefresh toggle is a better tradeoff
  /// than re-fetching on every tap. Cleared on refreshAll and clearConfig.
  final Map<String, String> _templateBodyCache = {};

  @override
  Future<HubState> build() async {
    ref.onDispose(() {
      _client?.close();
      _client = null;
      _cache?.close();
      _cache = null;
      _blobCache = null;
      _templateBodyCache.clear();
    });
    final initial = await _loadConfig();
    // Cold-start refresh: if SharedPreferences/secure storage already had a
    // valid hub config (the user has launched the app before, or upgraded
    // the APK over an existing install), saveConfig won't fire — and
    // without an explicit refresh, every tab that reads state.projects /
    // attention / hosts / agents shows empty until something else
    // schedules one. Activity escapes this because recentAuditProvider is
    // a standalone FutureProvider that fetches on mount; Projects, Me,
    // Hosts, Agents do not.
    //
    // Microtask-scheduled so this build() resolves first; refreshAll then
    // mutates state via setter, which is the supported pattern.
    if (initial.configured) {
      Future.microtask(refreshAll);
    }
    return initial;
  }

  /// Fetch a template body, serving from the in-memory cache when possible.
  /// Pass forceRefresh=true to bypass the cache (the viewer's Refresh icon).
  Future<String> getTemplateBody(
    String category,
    String name, {
    bool forceRefresh = false,
  }) async {
    final client = _client;
    if (client == null) {
      throw StateError('hub not configured');
    }
    final key = '$category/$name';
    if (!forceRefresh) {
      final cached = _templateBodyCache[key];
      if (cached != null) return cached;
    }
    final body = await client.getTemplate(category, name);
    _templateBodyCache[key] = body;
    return body;
  }

  /// Clear all template body cache entries. Called implicitly by refreshAll;
  /// expose for rare cases (e.g. after a template edit) where a caller needs
  /// to drop cached bytes without a full refresh.
  void clearTemplateBodyCache() => _templateBodyCache.clear();

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
    _cache = await _openCache();
    _blobCache = await _openBlobCache();
    _client!.snapshotCache = _cache;
    _client!.blobCache = _blobCache;
    // Cache-first cold start: read every dashboard endpoint's last
    // snapshot synchronously so the UI lights up with last-known-good
    // before the network refresh fires. The microtask-scheduled
    // refreshAll in build() then overwrites with fresh data — and the
    // refresh path's `clearStale` logic resets staleSince once each
    // endpoint succeeds.
    return _hydrateFromCache(cfg);
  }

  /// Read the six dashboard list snapshots from the on-disk cache and
  /// fold them into a HubState the UI can render immediately. Missing
  /// rows fall back to empty lists; oldest fetchedAt across hits drives
  /// the "Offline · last updated X" banner. No network here — the
  /// network refresh is scheduled separately so cache reads stay
  /// sub-millisecond and the splash screen doesn't block on them.
  Future<HubState> _hydrateFromCache(HubConfig cfg) async {
    final cache = _cache;
    if (cache == null) return HubState(config: cfg);
    final hubKey = hubCacheKey(baseUrl: cfg.baseUrl, teamId: cfg.teamId);
    final t = cfg.teamId;
    // Endpoint keys must match exactly what HubClient.list*Cached() pass
    // to readThrough, otherwise we read empty even when cache has data.
    // buildEndpointKey() canonicalizes query params the same way.
    final results = await Future.wait([
      cache.get(hubKey,
          buildEndpointKey('/v1/teams/$t/attention', {'status': 'open'})),
      cache.get(hubKey, '/v1/teams/$t/hosts'),
      cache.get(hubKey, '/v1/teams/$t/agents'),
      cache.get(hubKey, '/v1/teams/$t/projects'),
      cache.get(hubKey, '/v1/teams/$t/templates'),
      cache.get(hubKey, '/v1/teams/$t/agents/spawns'),
    ]);
    DateTime? oldest;
    for (final snap in results) {
      if (snap == null) continue;
      if (oldest == null || snap.fetchedAt.isBefore(oldest)) {
        oldest = snap.fetchedAt;
      }
    }
    return HubState(
      config: cfg,
      attention: _decodeCachedList(results[0]),
      hosts: _decodeCachedList(results[1]),
      agents: _decodeCachedList(results[2]),
      projects: _decodeCachedList(results[3]),
      templates: _decodeCachedList(results[4]),
      spawns: _decodeCachedList(results[5]),
      // Surfaced as "stale" until refreshAll confirms a fresh fetch and
      // clears it via clearStale. UI shows the offline banner during
      // this brief window only when the network is genuinely down — on
      // a healthy hub the banner blinks past too fast to register.
      staleSince: oldest,
    );
  }

  static List<Map<String, dynamic>> _decodeCachedList(HubSnapshot? snap) {
    if (snap == null) return const [];
    final body = snap.body;
    if (body is! List) return const [];
    return [
      for (final r in body)
        if (r is Map) r.cast<String, dynamic>(),
    ];
  }

  HubClient? get client => _client;

  /// Snapshot cache for this hub. Null until config is loaded. Exposed so
  /// commit #2 can wire HubClient to read-through on transport failures.
  HubSnapshotCache? get snapshotCache => _cache;

  Future<HubSnapshotCache> _openCache() async {
    final dir = await getApplicationDocumentsDirectory();
    return HubSnapshotCache(dbPath: '${dir.path}/hub_snapshots.db');
  }

  Future<BlobBytesCache> _openBlobCache() async {
    final dir = await getApplicationDocumentsDirectory();
    return BlobBytesCache(rootDir: '${dir.path}/hub_blobs');
  }

  /// Persist the config and refresh every list. Called from the bootstrap
  /// wizard after a successful probe.
  Future<void> saveConfig({
    required String baseUrl,
    required String token,
    required String teamId,
  }) async {
    final prevCfg = state.value?.config;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_kHubBaseUrlKey, baseUrl);
    await prefs.setString(_kHubTeamIdKey, teamId);
    const secure = FlutterSecureStorage();
    await secure.write(key: _kHubTokenKey, value: token);

    _client?.close();
    final cfg = HubConfig(baseUrl: baseUrl, token: token, teamId: teamId);
    _client = HubClient(cfg);
    _cache ??= await _openCache();
    _blobCache ??= await _openBlobCache();
    // Switching hub or team leaves the previous partition orphaned — wipe
    // it so snapshots can't leak across identities and don't eat disk
    // until LRU eventually reclaims the rows. Blob bytes are content-
    // addressed (sha) so they're safe to share across hubs; no wipe.
    if (prevCfg != null &&
        (prevCfg.baseUrl != baseUrl || prevCfg.teamId != teamId)) {
      await _cache!.wipeHub(
        hubCacheKey(baseUrl: prevCfg.baseUrl, teamId: prevCfg.teamId),
      );
    }
    _client!.snapshotCache = _cache;
    _client!.blobCache = _blobCache;

    state = AsyncData(HubState(config: cfg));
    await refreshAll();
  }

  Future<void> clearConfig() async {
    final prevCfg = state.value?.config;
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_kHubBaseUrlKey);
    await prefs.remove(_kHubTeamIdKey);
    const secure = FlutterSecureStorage();
    await secure.delete(key: _kHubTokenKey);
    _client?.close();
    _client = null;
    _templateBodyCache.clear();
    if (_cache != null && prevCfg != null) {
      await _cache!.wipeHub(
        hubCacheKey(baseUrl: prevCfg.baseUrl, teamId: prevCfg.teamId),
      );
    }
    // Logout is coarse: blob bytes were fetched under the departing
    // identity, so wipe the whole directory even though sha keys are
    // content-addressed — don't keep bytes we no longer have a token for.
    if (_blobCache != null) {
      await _blobCache!.wipeAll();
    }
    await _cache?.close();
    _cache = null;
    _blobCache = null;
    state = const AsyncData(HubState());
  }

  /// Nuke every offline snapshot + cached blob across every hub
  /// partition. Called from the Settings "Clear offline cache" row when
  /// the user wants a clean slate without also forgetting their hub
  /// URL/token. Returns `(snapshotRows, blobFiles)` so the UI can
  /// surface "Cleared N entries · M files". Opens the caches lazily if
  /// they weren't already loaded.
  Future<(int, int)> clearOfflineCache() async {
    _cache ??= await _openCache();
    _blobCache ??= await _openBlobCache();
    final rows = await _cache!.wipeAll();
    final files = await _blobCache!.wipeAll();
    _client?.snapshotCache = _cache;
    _client?.blobCache = _blobCache;
    return (rows, files);
  }

  /// One-shot fetch of everything the dashboard needs. Tabs show whatever
  /// snapshot was loaded last; pull-to-refresh on the hub screen re-runs
  /// this.
  ///
  /// Uses the `*Cached` read-through variants so a transport failure falls
  /// back to the last-known-good SQLite snapshot instead of leaving the
  /// list empty. Each endpoint resolves independently — if four succeed
  /// and two serve stale, the UI sees four fresh lists + two stale ones,
  /// and `state.staleSince` holds the oldest fetchedAt across the stale
  /// results so the Projects screen can show the offline banner.
  Future<void> refreshAll() async {
    final client = _client;
    if (client == null) return;
    final prev = state.value ?? const HubState();
    state = AsyncData(prev.copyWith(loading: true, clearError: true));
    // A refresh is the user asking "give me the latest of everything" — drop
    // cached template bodies so the next Templates-tab tap actually re-fetches.
    _templateBodyCache.clear();
    final results = await Future.wait([
      _resolveCached(prev.attention,
          () => client.listAttentionCached(status: 'open')),
      _resolveCached(prev.hosts, client.listHostsCached),
      _resolveCached(prev.agents, () => client.listAgentsCached()),
      _resolveCached(prev.projects, () => client.listProjectsCached()),
      _resolveCached(prev.templates, client.listTemplatesCached),
      _resolveCached(prev.spawns, client.listSpawnsCached),
    ]);
    DateTime? staleSince;
    final errors = <String>[];
    for (final r in results) {
      if (r.staleSince != null) {
        if (staleSince == null || r.staleSince!.isBefore(staleSince)) {
          staleSince = r.staleSince;
        }
      }
      if (r.error != null) errors.add(r.error!);
    }
    state = AsyncData(prev.copyWith(
      loading: false,
      attention: results[0].body,
      hosts: results[1].body,
      agents: results[2].body,
      projects: results[3].body,
      templates: results[4].body,
      spawns: results[5].body,
      staleSince: staleSince,
      clearStale: staleSince == null,
      error: errors.isEmpty ? null : errors.first,
      clearError: errors.isEmpty,
    ));
  }

  /// Run a cached fetch and reduce it to `(body, staleSince, error)`.
  /// Preserves the previous in-memory body when both the network AND the
  /// cache fail — otherwise a first-run offline open would wipe whatever
  /// we last showed. Errors are per-endpoint, not fatal to the refresh as
  /// a whole: the UI surfaces staleSince + keeps the list populated.
  Future<_DashResult> _resolveCached(
    List<Map<String, dynamic>> prevBody,
    Future<CachedResponse<List<Map<String, dynamic>>>> Function() fetch,
  ) async {
    try {
      final r = await fetch();
      return _DashResult(body: r.body, staleSince: r.staleSince);
    } catch (e) {
      return _DashResult(body: prevBody, error: e.toString());
    }
  }

  Future<void> decide(
    String id,
    String decision, {
    String? reason,
    String? by,
    String? optionId,
  }) async {
    final client = _client;
    if (client == null) return;
    await client.decideAttention(
      id,
      decision: decision,
      reason: reason,
      by: by,
      optionId: optionId,
    );
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

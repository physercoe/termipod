import 'dart:convert';
import 'dart:io';

import 'blob_bytes_cache.dart';
import 'hub_snapshot_cache.dart';

/// Connection settings for a Termipod Hub daemon.
///
/// Stored split between SharedPreferences (baseUrl/teamId, plus a pointer
/// to the secure-storage entry) and flutter_secure_storage (token). The
/// [HubConfig] value itself is ephemeral — built when needed, not persisted.
class HubConfig {
  final String baseUrl;
  final String token;
  final String teamId;

  const HubConfig({
    required this.baseUrl,
    required this.token,
    required this.teamId,
  });

  bool get isValid =>
      baseUrl.isNotEmpty && token.isNotEmpty && teamId.isNotEmpty;

  HubConfig copyWith({String? baseUrl, String? token, String? teamId}) =>
      HubConfig(
        baseUrl: baseUrl ?? this.baseUrl,
        token: token ?? this.token,
        teamId: teamId ?? this.teamId,
      );
}

/// Error thrown for non-2xx HTTP responses from the hub.
class HubApiError implements Exception {
  final int status;
  final String message;
  HubApiError(this.status, this.message);

  @override
  String toString() => 'HubApiError($status): $message';
}

/// Shared HTTP + cache transport for [HubClient] and its per-domain
/// sub-clients (see `docs/plans/hub-client-split.md`).
///
/// Owns the [HttpClient], the two optional caches, and the verb +
/// decoding plumbing every API method routes through. Deliberately dumb:
/// it builds a path, issues a request, and decodes JSON — parsing into
/// domain models happens in the provider layer. The verbs are public so
/// sub-clients in sibling libraries can call them; only [_http] is
/// private to the transport.
///
/// Auth: every call except `/v1/_info` sends `Authorization: Bearer
/// <token>`. The hub lets `/v1/_info` through unauthenticated so clients
/// can probe a candidate URL before the user has pasted a token (pass
/// `auth: false`).
class HubTransport {
  final HubConfig cfg;
  final HttpClient _http;

  /// Optional read-through cache for list/get responses. Set by the
  /// provider after construction so the transport can stay dumb about
  /// how/where the SQLite db lives. When null, the *Cached methods act
  /// as thin wrappers — no offline fallback, staleSince is always null.
  HubSnapshotCache? snapshotCache;

  /// Optional content-addressed cache for `/v1/blobs/{sha}` bytes. Set
  /// alongside [snapshotCache] by the provider. When null, downloadBlob
  /// degrades to a pure network fetch (pre-cache behaviour).
  BlobBytesCache? blobCache;

  HubTransport(this.cfg)
      : _http = HttpClient()
          ..connectionTimeout = const Duration(seconds: 8)
          ..idleTimeout = const Duration(seconds: 30);

  void close() {
    _http.close(force: true);
  }

  /// Partition key for the snapshot cache. Scoped by baseUrl + teamId so
  /// switching hubs or teams never surfaces another partition's rows.
  String get cacheHubKey =>
      hubCacheKey(baseUrl: cfg.baseUrl, teamId: cfg.teamId);

  List<Map<String, dynamic>> decodeListMaps(Object body) => [
        for (final r in body as List) (r as Map).cast<String, dynamic>(),
      ];

  Map<String, dynamic> decodeMap(Object body) =>
      (body as Map).cast<String, dynamic>();

  /// Drop every cached row whose endpoint key starts with [prefix] on the
  /// current hub partition. Called from mutation methods so the next read
  /// re-fetches instead of serving a pre-write snapshot. No-op when no
  /// cache is attached.
  Future<void> invalidate(String prefix) async {
    final c = snapshotCache;
    if (c == null) return;
    await c.invalidatePrefix(cacheHubKey, prefix);
  }

  Uri uri(String path, [Map<String, String>? query]) {
    final base = cfg.baseUrl.endsWith('/')
        ? cfg.baseUrl.substring(0, cfg.baseUrl.length - 1)
        : cfg.baseUrl;
    final u = Uri.parse('$base$path');
    if (query == null || query.isEmpty) return u;
    return u.replace(queryParameters: {...u.queryParameters, ...query});
  }

  Future<HttpClientRequest> open(
    String method,
    String path, {
    Map<String, String>? query,
    bool auth = true,
  }) async {
    final u = uri(path, query);
    final req = await _http.openUrl(method, u);
    req.headers.set(HttpHeaders.acceptHeader, 'application/json');
    if (auth && cfg.token.isNotEmpty) {
      req.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${cfg.token}');
    }
    return req;
  }

  Future<dynamic> readJson(HttpClientResponse resp) async {
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    if (body.isEmpty) return null;
    return jsonDecode(body);
  }

  Future<dynamic> get(String path,
      {Map<String, String>? query, bool auth = true}) async {
    final req = await open('GET', path, query: query, auth: auth);
    final resp = await req.close();
    return readJson(resp);
  }

  /// Convenience: GET a path expected to return a JSON array of objects,
  /// decoded to a list of maps (empty list when the body is null). The
  /// most common shape across the per-domain sub-clients.
  Future<List<Map<String, dynamic>>> listJson(String path,
      {Map<String, String>? query}) async {
    final out = await get(path, query: query);
    if (out == null) return const [];
    return (out as List).cast<Map<String, dynamic>>();
  }

  Future<dynamic> post(String path, Object body,
      {Map<String, String>? query}) async {
    final req = await open('POST', path, query: query);
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    return readJson(resp);
  }

  Future<dynamic> patch(String path, Object body) async {
    final req = await open('PATCH', path);
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    return readJson(resp);
  }

  Future<dynamic> put(String path, Object body) async {
    final req = await open('PUT', path);
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    return readJson(resp);
  }

  Future<void> delete(String path) async {
    final req = await open('DELETE', path);
    final resp = await req.close();
    // Drain body so the connection can be reused.
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
  }
}

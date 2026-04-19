import 'dart:async';
import 'dart:convert';
import 'dart:io';

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

/// Thin REST + SSE client for the Termipod Hub HTTP API.
///
/// The client is deliberately dumb — it takes a [HubConfig], issues HTTP
/// requests, and decodes JSON. Parsing into domain models happens in the
/// provider layer so screens can hot-reload without mangling the wire
/// schema.
///
/// Auth: every call except `/v1/_info` sends `Authorization: Bearer <token>`.
/// The hub lets `/v1/_info` through unauthenticated so clients can probe
/// a candidate URL before the user has pasted a token.
class HubClient {
  final HubConfig cfg;
  final HttpClient _http;

  HubClient(this.cfg)
      : _http = HttpClient()
          ..connectionTimeout = const Duration(seconds: 8)
          ..idleTimeout = const Duration(seconds: 30);

  void close() {
    _http.close(force: true);
  }

  Uri _uri(String path, [Map<String, String>? query]) {
    final base = cfg.baseUrl.endsWith('/')
        ? cfg.baseUrl.substring(0, cfg.baseUrl.length - 1)
        : cfg.baseUrl;
    final u = Uri.parse('$base$path');
    if (query == null || query.isEmpty) return u;
    return u.replace(queryParameters: {...u.queryParameters, ...query});
  }

  Future<HttpClientRequest> _open(
    String method,
    String path, {
    Map<String, String>? query,
    bool auth = true,
  }) async {
    final uri = _uri(path, query);
    final req = await _http.openUrl(method, uri);
    req.headers.set(HttpHeaders.acceptHeader, 'application/json');
    if (auth && cfg.token.isNotEmpty) {
      req.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${cfg.token}');
    }
    return req;
  }

  Future<dynamic> _readJson(HttpClientResponse resp) async {
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    if (body.isEmpty) return null;
    return jsonDecode(body);
  }

  Future<dynamic> _get(String path, {Map<String, String>? query, bool auth = true}) async {
    final req = await _open('GET', path, query: query, auth: auth);
    final resp = await req.close();
    return _readJson(resp);
  }

  Future<dynamic> _post(String path, Object body, {Map<String, String>? query}) async {
    final req = await _open('POST', path, query: query);
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    return _readJson(resp);
  }

  // ---- info / probe ----

  /// Probe the hub. Doesn't require a token, so we use it from the bootstrap
  /// wizard to validate the URL before the user pastes the token.
  Future<Map<String, dynamic>> getInfo() async {
    final out = await _get('/v1/_info', auth: false);
    return (out as Map).cast<String, dynamic>();
  }

  /// Probe with auth — fails fast if the token is wrong. Uses /hosts because
  /// it's cheap and always exists for a valid team.
  Future<void> verifyAuth() async {
    await _get('/v1/teams/${cfg.teamId}/hosts');
  }

  // ---- collections ----

  Future<List<Map<String, dynamic>>> listHosts() =>
      _listJson('/v1/teams/${cfg.teamId}/hosts');

  Future<List<Map<String, dynamic>>> listAgents() =>
      _listJson('/v1/teams/${cfg.teamId}/agents');

  /// Parent→child spawn edges. Each row has `parent_agent_id`,
  /// `child_agent_id`, `handle`, `kind`, `status`, plus the original
  /// spawn metadata. Used to render the agent org chart.
  Future<List<Map<String, dynamic>>> listSpawns() =>
      _listJson('/v1/teams/${cfg.teamId}/agents/spawns');

  Future<List<Map<String, dynamic>>> listProjects() =>
      _listJson('/v1/teams/${cfg.teamId}/projects');

  Future<List<Map<String, dynamic>>> listChannels(String projectId) =>
      _listJson('/v1/teams/${cfg.teamId}/projects/$projectId/channels');

  Future<List<Map<String, dynamic>>> listAttention({String? status}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/attention',
        query: status == null ? null : {'status': status},
      );

  Future<List<Map<String, dynamic>>> listTasks(
    String projectId, {
    String? status,
  }) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/projects/$projectId/tasks',
        query: status == null ? null : {'status': status},
      );

  Future<Map<String, dynamic>> getTask(String projectId, String taskId) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks/$taskId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> patchTask(
    String projectId,
    String taskId, {
    String? status,
    String? title,
    String? bodyMd,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (title != null) body['title'] = title;
    if (bodyMd != null) body['body_md'] = bodyMd;
    final req = await _open(
      'PATCH',
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks/$taskId',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    final out = await _readJson(resp);
    return (out as Map).cast<String, dynamic>();
  }

  Future<List<Map<String, dynamic>>> listTemplates() =>
      _listJson('/v1/teams/${cfg.teamId}/templates');

  /// Returns raw template body (YAML / markdown / JSON — the endpoint
  /// doesn't parse). Caller renders as text.
  Future<String> getTemplate(String category, String name) async {
    final req = await _open(
      'GET',
      '/v1/teams/${cfg.teamId}/templates/$category/$name',
    );
    final resp = await req.close();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final msg = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, msg);
    }
    return resp.transform(utf8.decoder).join();
  }

  Future<List<Map<String, dynamic>>> _listJson(
    String path, {
    Map<String, String>? query,
  }) async {
    final out = await _get(path, query: query);
    if (out == null) return const [];
    return (out as List).cast<Map<String, dynamic>>();
  }

  // ---- spawn ----

  /// Spawns a new agent on the given host using the provided YAML spec
  /// body. Returns either the spawned agent (`status: spawned`) or an
  /// approval handle (`status: pending_approval` + `attention_id`) when
  /// policy gates the action.
  Future<Map<String, dynamic>> spawnAgent({
    required String childHandle,
    required String kind,
    required String spawnSpecYaml,
    String? hostId,
    String? parentAgentId,
  }) async {
    final body = <String, dynamic>{
      'child_handle': childHandle,
      'kind': kind,
      'spawn_spec_yaml': spawnSpecYaml,
    };
    if (hostId != null && hostId.isNotEmpty) body['host_id'] = hostId;
    if (parentAgentId != null && parentAgentId.isNotEmpty) {
      body['parent_agent_id'] = parentAgentId;
    }
    final out = await _post('/v1/teams/${cfg.teamId}/agents/spawn', body);
    return (out as Map).cast<String, dynamic>();
  }

  // ---- attention actions ----

  Future<Map<String, dynamic>> decideAttention(
    String id, {
    required String decision,
    String? by,
    String? reason,
  }) async {
    final body = <String, dynamic>{'decision': decision};
    if (by != null && by.isNotEmpty) body['by'] = by;
    if (reason != null && reason.isNotEmpty) body['reason'] = reason;
    final out = await _post('/v1/teams/${cfg.teamId}/attention/$id/decide', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> resolveAttention(
    String id, {
    String? by,
    String? reason,
  }) async {
    final body = <String, dynamic>{};
    if (by != null && by.isNotEmpty) body['by'] = by;
    if (reason != null && reason.isNotEmpty) body['reason'] = reason;
    final out = await _post('/v1/teams/${cfg.teamId}/attention/$id/resolve', body);
    return (out as Map).cast<String, dynamic>();
  }

  // ---- SSE event stream ----

  /// Streams events for one channel as parsed JSON objects. The hub sends
  /// `data: {...}` frames separated by blank lines, plus `: ping` comments
  /// every 15s that we drop.
  ///
  /// Close the returned subscription to tear down the underlying socket —
  /// the generator cancels [HttpClientResponse.listen] cleanly when Flutter
  /// cancels the stream.
  Stream<Map<String, dynamic>> streamEvents(
    String projectId,
    String channelId, {
    String? since,
  }) async* {
    final req = await _open(
      'GET',
      '/v1/teams/${cfg.teamId}/projects/$projectId/channels/$channelId/stream',
      query: since == null ? null : {'since': since},
    );
    req.headers.set(HttpHeaders.acceptHeader, 'text/event-stream');
    req.headers.set(HttpHeaders.cacheControlHeader, 'no-cache');
    final resp = await req.close();
    if (resp.statusCode != 200) {
      final body = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, body);
    }

    final buffer = StringBuffer();
    await for (final chunk
        in resp.transform(utf8.decoder).transform(const LineSplitter())) {
      if (chunk.isEmpty) {
        final frame = buffer.toString();
        buffer.clear();
        final payload = _extractData(frame);
        if (payload == null) continue;
        try {
          final decoded = jsonDecode(payload);
          if (decoded is Map) {
            yield decoded.cast<String, dynamic>();
          }
        } catch (_) {
          // One malformed frame shouldn't kill the stream.
        }
        continue;
      }
      if (chunk.startsWith(':')) continue; // SSE comment / keepalive
      buffer.writeln(chunk);
    }
  }

  /// Pulls `data:` payloads out of a multi-line SSE frame. Comments (`:`
  /// prefix) and unknown fields are ignored per the WHATWG spec.
  String? _extractData(String frame) {
    final lines = frame.split('\n');
    final data = StringBuffer();
    for (final line in lines) {
      if (line.isEmpty || line.startsWith(':')) continue;
      if (line.startsWith('data:')) {
        final v = line.substring(5);
        data.writeln(v.startsWith(' ') ? v.substring(1) : v);
      }
    }
    if (data.isEmpty) return null;
    var out = data.toString();
    if (out.endsWith('\n')) out = out.substring(0, out.length - 1);
    return out;
  }
}

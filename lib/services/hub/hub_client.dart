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

  Future<dynamic> _patch(String path, Object body) async {
    final req = await _open('PATCH', path);
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    return _readJson(resp);
  }

  Future<void> _delete(String path) async {
    final req = await _open('DELETE', path);
    final resp = await req.close();
    // Drain body so the connection can be reused.
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
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

  /// Team-scope channels (project_id NULL, scope_kind='team'). `#hub-meta`
  /// is auto-seeded by hub init — it's the principal↔steward room.
  Future<List<Map<String, dynamic>>> listTeamChannels() =>
      _listJson('/v1/teams/${cfg.teamId}/channels');

  Future<Map<String, dynamic>> createTeamChannel(String name) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/channels',
      {'name': name},
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Humans don't have a dedicated table; they're tracked as `auth_tokens`
  /// rows with `scope.role='principal'`. This endpoint coalesces by
  /// `scope.handle`, returning one row per unique handle plus a bucket for
  /// unnamed tokens.
  Future<List<Map<String, dynamic>>> listPrincipals() =>
      _listJson('/v1/teams/${cfg.teamId}/principals');

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

  // ---- project / task / channel writes ----

  Future<Map<String, dynamic>> createProject({
    required String name,
    String? docsRoot,
    String? configYaml,
  }) async {
    final body = <String, dynamic>{'name': name};
    if (docsRoot != null && docsRoot.isNotEmpty) body['docs_root'] = docsRoot;
    if (configYaml != null && configYaml.isNotEmpty) {
      body['config_yaml'] = configYaml;
    }
    final out = await _post('/v1/teams/${cfg.teamId}/projects', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> archiveProject(String projectId) async {
    await _delete('/v1/teams/${cfg.teamId}/projects/$projectId');
  }

  Future<Map<String, dynamic>> createTask(
    String projectId, {
    required String title,
    String? bodyMd,
    String? assigneeId,
    String? parentTaskId,
    String? status,
  }) async {
    final body = <String, dynamic>{'title': title};
    if (bodyMd != null && bodyMd.isNotEmpty) body['body_md'] = bodyMd;
    if (assigneeId != null && assigneeId.isNotEmpty) {
      body['assignee_id'] = assigneeId;
    }
    if (parentTaskId != null && parentTaskId.isNotEmpty) {
      body['parent_task_id'] = parentTaskId;
    }
    if (status != null && status.isNotEmpty) body['status'] = status;
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> createChannel(
    String projectId,
    String name,
  ) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/channels',
      {'name': name},
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Shared POST for both project- and team-scope channels. [path] is the
  /// channel events URL the server mounts the reused [handlePostEvent] on.
  Future<Map<String, dynamic>> _postEvent(
    String path, {
    required String type,
    List<Map<String, dynamic>>? parts,
    String? fromId,
    List<String>? toIds,
    String? taskId,
    String? correlationId,
  }) async {
    final body = <String, dynamic>{'type': type};
    if (parts != null) body['parts'] = parts;
    if (fromId != null && fromId.isNotEmpty) body['from_id'] = fromId;
    if (toIds != null && toIds.isNotEmpty) body['to_ids'] = toIds;
    if (taskId != null && taskId.isNotEmpty) body['task_id'] = taskId;
    if (correlationId != null && correlationId.isNotEmpty) {
      body['correlation_id'] = correlationId;
    }
    final out = await _post(path, body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> postProjectChannelEvent(
    String projectId,
    String channelId, {
    required String type,
    List<Map<String, dynamic>>? parts,
    String? fromId,
    List<String>? toIds,
    String? taskId,
    String? correlationId,
  }) =>
      _postEvent(
        '/v1/teams/${cfg.teamId}/projects/$projectId/channels/$channelId/events',
        type: type,
        parts: parts,
        fromId: fromId,
        toIds: toIds,
        taskId: taskId,
        correlationId: correlationId,
      );

  Future<Map<String, dynamic>> postTeamChannelEvent(
    String channelId, {
    required String type,
    List<Map<String, dynamic>>? parts,
    String? fromId,
    List<String>? toIds,
    String? taskId,
    String? correlationId,
  }) =>
      _postEvent(
        '/v1/teams/${cfg.teamId}/channels/$channelId/events',
        type: type,
        parts: parts,
        fromId: fromId,
        toIds: toIds,
        taskId: taskId,
        correlationId: correlationId,
      );

  Future<List<Map<String, dynamic>>> listTeamChannelEvents(
    String channelId, {
    String? since,
    int? limit,
  }) {
    final q = <String, String>{};
    if (since != null && since.isNotEmpty) q['since'] = since;
    if (limit != null) q['limit'] = '$limit';
    return _listJson(
      '/v1/teams/${cfg.teamId}/channels/$channelId/events',
      query: q.isEmpty ? null : q,
    );
  }

  Future<List<Map<String, dynamic>>> listProjectChannelEvents(
    String projectId,
    String channelId, {
    String? since,
    int? limit,
  }) {
    final q = <String, String>{};
    if (since != null && since.isNotEmpty) q['since'] = since;
    if (limit != null) q['limit'] = '$limit';
    return _listJson(
      '/v1/teams/${cfg.teamId}/projects/$projectId/channels/$channelId/events',
      query: q.isEmpty ? null : q,
    );
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

  // ---- agent lifecycle ----

  /// Terminates an agent by patching status=terminated. The host-runner
  /// will pick up the kill on its next poll.
  Future<void> terminateAgent(String agentId) async {
    await _patch('/v1/teams/${cfg.teamId}/agents/$agentId',
        {'status': 'terminated'});
  }

  /// Enqueues a SIGSTOP against the agent's pane process group. Returns
  /// the command id so callers can poll status if needed.
  Future<Map<String, dynamic>> pauseAgent(String agentId) async {
    final out = await _post(
        '/v1/teams/${cfg.teamId}/agents/$agentId/pause', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> resumeAgent(String agentId) async {
    final out = await _post(
        '/v1/teams/${cfg.teamId}/agents/$agentId/resume', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Returns the most recent pane capture for this agent. Pass
  /// `refresh: true` to also enqueue a fresh capture; the current call
  /// still returns the previous cached capture — fetch again after a
  /// beat to see the new one.
  Future<Map<String, dynamic>> getAgentPane(
    String agentId, {
    bool refresh = false,
  }) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/agents/$agentId/pane',
      query: refresh ? {'refresh': '1'} : null,
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Reads the agent's markdown journal. Returns the raw markdown text;
  /// an empty string means the journal file hasn't been written yet.
  Future<String> readAgentJournal(String agentId) async {
    final req = await _open(
        'GET', '/v1/teams/${cfg.teamId}/agents/$agentId/journal');
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  /// Appends a markdown note to the agent's journal. The hub prepends a
  /// UTC timestamp header unless [header] is supplied.
  Future<void> appendAgentJournal(
    String agentId,
    String entry, {
    String? header,
  }) async {
    final body = <String, dynamic>{'entry': entry};
    if (header != null && header.isNotEmpty) body['header'] = header;
    await _post('/v1/teams/${cfg.teamId}/agents/$agentId/journal', body);
  }

  // ---- host lifecycle ----

  /// Removes a host row. The hub refuses if the host still has active
  /// agents (anything not terminated/failed); the UI should surface the
  /// 409 to the operator.
  Future<void> deleteHost(String hostId) async {
    await _delete('/v1/teams/${cfg.teamId}/hosts/$hostId');
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

  // ---- schedules (team-scoped) ----

  Future<List<Map<String, dynamic>>> listSchedules() =>
      _listJson('/v1/teams/${cfg.teamId}/schedules');

  /// Creates a cron schedule that spawns an agent when it fires. [spawn]
  /// is the full spawn spec map (child_handle/kind/spawn_spec_yaml/etc.)
  /// serialized as-is — see [spawnAgent] for the shape.
  Future<Map<String, dynamic>> createSchedule({
    required String name,
    required String cronExpr,
    required Map<String, dynamic> spawn,
    bool? enabled,
  }) async {
    final body = <String, dynamic>{
      'name': name,
      'cron_expr': cronExpr,
      'spawn': spawn,
    };
    if (enabled != null) body['enabled'] = enabled;
    final out = await _post('/v1/teams/${cfg.teamId}/schedules', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> patchSchedule(String id, {required bool enabled}) async {
    await _patch(
      '/v1/teams/${cfg.teamId}/schedules/$id',
      {'enabled': enabled},
    );
  }

  Future<void> deleteSchedule(String id) =>
      _delete('/v1/teams/${cfg.teamId}/schedules/$id');

  // ---- project docs (read-only) ----

  /// Flat list of doc entries under the project's docs_root. Each entry
  /// has `path` (relative), `size`, `mod_time`, and optional `is_dir`.
  /// Returns an empty list if the project has no docs_root configured.
  Future<List<Map<String, dynamic>>> listProjectDocs(String projectId) =>
      _listJson('/v1/teams/${cfg.teamId}/projects/$projectId/docs');

  /// Reads a single doc as a UTF-8 string. The hub serves any file type;
  /// caller decides how to render based on extension.
  Future<String> getProjectDoc(String projectId, String relPath) async {
    final req = await _open(
      'GET',
      '/v1/teams/${cfg.teamId}/projects/$projectId/docs/$relPath',
    );
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  // ---- blobs (content-addressed) ----

  /// Uploads raw bytes; returns `{sha256, size, mime}`. Dedup is automatic
  /// server-side — same bytes → same sha → no duplicate row. 25 MiB cap.
  Future<Map<String, dynamic>> uploadBlob(
    List<int> bytes, {
    String? mime,
  }) async {
    final req = await _open('POST', '/v1/blobs');
    req.headers.contentType = ContentType.parse(mime ?? 'application/octet-stream');
    req.add(bytes);
    final resp = await req.close();
    final out = await _readJson(resp);
    return (out as Map).cast<String, dynamic>();
  }

  /// Downloads blob bytes by sha. Caller is responsible for writing to
  /// disk / piping to share_plus; we keep the full payload in memory since
  /// the server caps uploads at 25 MiB anyway.
  Future<List<int>> downloadBlob(String sha) async {
    final req = await _open('GET', '/v1/blobs/$sha');
    final resp = await req.close();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final msg = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, msg);
    }
    final out = <int>[];
    await for (final chunk in resp) {
      out.addAll(chunk);
    }
    return out;
  }

  // ---- search ----

  /// Full-text search over event parts. Returns matching events newest-
  /// first, each with `id`, `received_ts`, `channel_id`, `type`, `from_id`,
  /// and `parts` (the original event parts JSON). The hub uses SQLite
  /// FTS5; the `q` string accepts FTS5 match syntax.
  Future<List<Map<String, dynamic>>> searchEvents(
    String q, {
    int? limit,
  }) {
    final query = <String, String>{'q': q};
    if (limit != null) query['limit'] = '$limit';
    return _listJson('/v1/search', query: query);
  }

  /// Lists audit events for the configured team, newest first. Each row
  /// has `id`, `ts`, `actor_kind`, `actor_handle`, `action`, `target_kind`,
  /// `target_id`, `summary`, and optional `meta`.
  ///
  /// [action] filters to an exact action string (e.g. `agent.spawn`).
  /// [since] is an ISO-8601 UTC timestamp lower bound. [limit] is clamped
  /// to 500 by the server.
  Future<List<Map<String, dynamic>>> listAuditEvents({
    String? action,
    String? since,
    int? limit,
  }) {
    final query = <String, String>{};
    if (action != null && action.isNotEmpty) query['action'] = action;
    if (since != null && since.isNotEmpty) query['since'] = since;
    if (limit != null) query['limit'] = '$limit';
    return _listJson(
      '/v1/teams/${cfg.teamId}/audit',
      query: query.isEmpty ? null : query,
    );
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
  }) =>
      _streamPath(
        '/v1/teams/${cfg.teamId}/projects/$projectId/channels/$channelId/stream',
        since: since,
      );

  Stream<Map<String, dynamic>> streamTeamEvents(
    String channelId, {
    String? since,
  }) =>
      _streamPath(
        '/v1/teams/${cfg.teamId}/channels/$channelId/stream',
        since: since,
      );

  Stream<Map<String, dynamic>> _streamPath(
    String path, {
    String? since,
  }) async* {
    final req = await _open(
      'GET',
      path,
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

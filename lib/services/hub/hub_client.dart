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

  Future<dynamic> _put(String path, Object body) async {
    final req = await _open('PUT', path);
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

  Future<List<Map<String, dynamic>>> listAgents({bool includeArchived = false}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/agents',
        query: includeArchived ? {'include_archived': '1'} : null,
      );

  /// Single-agent fetch. Includes `spawn_spec_yaml` + `spawn_authority`
  /// pulled from the agent_spawns join when the agent was created via
  /// /spawn. Agents created by other means (hand-crafted inserts) simply
  /// won't carry those fields.
  Future<Map<String, dynamic>> getAgent(String agentId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/agents/$agentId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Parent→child spawn edges. Each row has `parent_agent_id`,
  /// `child_agent_id`, `handle`, `kind`, `status`, plus the original
  /// spawn metadata. Used to render the agent org chart.
  Future<List<Map<String, dynamic>>> listSpawns() =>
      _listJson('/v1/teams/${cfg.teamId}/agents/spawns');

  Future<List<Map<String, dynamic>>> listProjects({bool? isTemplate}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/projects',
        query: isTemplate == null
            ? null
            : {'is_template': isTemplate ? 'true' : 'false'},
      );

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

  // ---- tokens (owner-only) ----

  /// Lists all tokens for the team (metadata only, no plaintext). Requires
  /// the caller to hold an owner-kind token; non-owners get 403.
  Future<List<Map<String, dynamic>>> listTokens() =>
      _listJson('/v1/teams/${cfg.teamId}/tokens');

  /// Issues a new token and returns the plaintext exactly once. Treat the
  /// response's `plaintext` field as write-once: show it to the user, then
  /// drop it from memory — the hub never stores it.
  Future<Map<String, dynamic>> issueToken({
    String kind = 'user',
    String role = 'principal',
    String? handle,
    String? expiresAt,
  }) async {
    final body = <String, dynamic>{'kind': kind, 'role': role};
    if (handle != null && handle.isNotEmpty) body['handle'] = handle;
    if (expiresAt != null && expiresAt.isNotEmpty) {
      body['expires_at'] = expiresAt;
    }
    final out = await _post('/v1/teams/${cfg.teamId}/tokens', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> revokeToken(String id) async {
    await _post(
      '/v1/teams/${cfg.teamId}/tokens/$id/revoke',
      const <String, dynamic>{},
    );
  }

  /// Fetches the raw team policy.yaml. Returns an empty string when the
  /// hub has no policy file yet — the editor treats that as a blank canvas.
  Future<String> getPolicy() async {
    final req = await _open('GET', '/v1/teams/${cfg.teamId}/policy');
    req.headers.set(HttpHeaders.acceptHeader, 'application/yaml');
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  /// Writes team policy.yaml atomically and triggers an in-memory reload.
  /// Parse errors are surfaced as HubApiError(400) so the caller can show
  /// the YAML diagnostic to the user without overwriting the good file.
  Future<void> putPolicy(String yaml) async {
    final req = await _open('PUT', '/v1/teams/${cfg.teamId}/policy');
    req.headers.contentType =
        ContentType('application', 'yaml', charset: 'utf-8');
    req.add(utf8.encode(yaml));
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
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
    // Blueprint §6.1 fields (P0.1):
    String? goal,
    String? kind, // 'goal' | 'standing'
    String? parentProjectId,
    String? templateId,
    Map<String, dynamic>? parameters,
    bool? isTemplate,
    int? budgetCents,
    String? stewardAgentId,
    String? onCreateTemplateId,
    Map<String, dynamic>? policyOverrides,
  }) async {
    final body = <String, dynamic>{'name': name};
    if (docsRoot != null && docsRoot.isNotEmpty) body['docs_root'] = docsRoot;
    if (configYaml != null && configYaml.isNotEmpty) {
      body['config_yaml'] = configYaml;
    }
    if (goal != null) body['goal'] = goal;
    if (kind != null) body['kind'] = kind;
    if (parentProjectId != null) body['parent_project_id'] = parentProjectId;
    if (templateId != null) body['template_id'] = templateId;
    if (parameters != null) body['parameters_json'] = parameters;
    if (isTemplate != null) body['is_template'] = isTemplate;
    if (budgetCents != null) body['budget_cents'] = budgetCents;
    if (stewardAgentId != null) body['steward_agent_id'] = stewardAgentId;
    if (onCreateTemplateId != null) {
      body['on_create_template_id'] = onCreateTemplateId;
    }
    if (policyOverrides != null) {
      body['policy_overrides_json'] = policyOverrides;
    }
    final out = await _post('/v1/teams/${cfg.teamId}/projects', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// PATCHes mutable project fields (P0.1). Pass only the fields you're
  /// changing — null-valued keys are omitted from the body.
  Future<Map<String, dynamic>> updateProject(
    String projectId, {
    String? name,
    String? goal,
    String? kind,
    String? templateId,
    Map<String, dynamic>? parameters,
    int? budgetCents,
    String? stewardAgentId,
    String? onCreateTemplateId,
    Map<String, dynamic>? policyOverrides,
    String? docsRoot,
  }) async {
    final body = <String, dynamic>{};
    if (name != null) body['name'] = name;
    if (goal != null) body['goal'] = goal;
    if (kind != null) body['kind'] = kind;
    if (templateId != null) body['template_id'] = templateId;
    if (parameters != null) body['parameters_json'] = parameters;
    if (budgetCents != null) body['budget_cents'] = budgetCents;
    if (stewardAgentId != null) body['steward_agent_id'] = stewardAgentId;
    if (onCreateTemplateId != null) {
      body['on_create_template_id'] = onCreateTemplateId;
    }
    if (policyOverrides != null) {
      body['policy_overrides_json'] = policyOverrides;
    }
    if (docsRoot != null) body['docs_root'] = docsRoot;
    final out = await _patch(
      '/v1/teams/${cfg.teamId}/projects/$projectId',
      body,
    );
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

  /// Soft-archives a terminated agent so it drops out of the live list.
  /// The row stays in the DB so audit history continues to resolve.
  /// Hub refuses with 409 if the agent is still live.
  Future<void> archiveAgent(String agentId) async {
    await _delete('/v1/teams/${cfg.teamId}/agents/$agentId');
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

  // ---- agent events (P1.7 AG-UI broker) ----

  /// Appends an event to the agent's per-agent queue. Producer defaults to
  /// `agent`; use `user` for approvals/input and `system` for status blips.
  /// Returns {id, seq, ts} so callers can order locally without re-listing.
  Future<Map<String, dynamic>> postAgentEvent(
    String agentId, {
    required String kind,
    String? producer,
    Map<String, dynamic>? payload,
  }) async {
    final body = <String, dynamic>{'kind': kind};
    if (producer != null && producer.isNotEmpty) body['producer'] = producer;
    if (payload != null) body['payload'] = payload;
    final out =
        await _post('/v1/teams/${cfg.teamId}/agents/$agentId/events', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Posts structured user input to an agent (P1.8). Lands in
  /// agent_events as producer='user' with kind='input.<kind>'; driver
  /// dispatch is the hub's job downstream. Returns {id, seq, ts}.
  Future<Map<String, dynamic>> postAgentInput(
    String agentId, {
    required String kind,
    String? body,
    String? decision,
    String? requestId,
    String? optionId,
    String? note,
    String? reason,
    String? documentId,
  }) async {
    final req = <String, dynamic>{'kind': kind};
    if (body != null) req['body'] = body;
    if (decision != null) req['decision'] = decision;
    if (requestId != null) req['request_id'] = requestId;
    if (optionId != null) req['option_id'] = optionId;
    if (note != null) req['note'] = note;
    if (reason != null) req['reason'] = reason;
    if (documentId != null) req['document_id'] = documentId;
    final out =
        await _post('/v1/teams/${cfg.teamId}/agents/$agentId/input', req);
    return (out as Map).cast<String, dynamic>();
  }

  /// Backfill events by monotonic seq. `since` is exclusive (seq > since).
  Future<List<Map<String, dynamic>>> listAgentEvents(
    String agentId, {
    int? since,
    int? limit,
  }) {
    final q = <String, String>{};
    if (since != null) q['since'] = '$since';
    if (limit != null) q['limit'] = '$limit';
    return _listJson(
      '/v1/teams/${cfg.teamId}/agents/$agentId/events',
      query: q.isEmpty ? null : q,
    );
  }

  /// SSE tail of the agent's event queue. Subscribes before replaying
  /// backfill from [sinceSeq] so no live event is missed in the gap.
  Stream<Map<String, dynamic>> streamAgentEvents(
    String agentId, {
    int? sinceSeq,
  }) =>
      _streamPath(
        '/v1/teams/${cfg.teamId}/agents/$agentId/stream',
        since: sinceSeq == null ? null : '$sinceSeq',
      );

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

  // ---- schedules (team-scoped, blueprint §6.3) ----
  //
  // Schedules trigger a plan from a template — they never spawn agents
  // directly (§7 forbidden pattern). Pre-P0.3 clients that POSTed a `spawn`
  // block will get 400; the wire shape is a hard break at alpha.

  Future<List<Map<String, dynamic>>> listSchedules({String? projectId}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/schedules',
        query: projectId == null ? null : {'project': projectId},
      );

  Future<Map<String, dynamic>> createSchedule({
    required String projectId,
    required String templateId,
    required String triggerKind, // 'cron' | 'manual' | 'on_create'
    String? cronExpr,
    Map<String, dynamic>? parameters,
    bool? enabled,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'template_id': templateId,
      'trigger_kind': triggerKind,
    };
    if (cronExpr != null && cronExpr.isNotEmpty) body['cron_expr'] = cronExpr;
    if (parameters != null) body['parameters_json'] = parameters;
    if (enabled != null) body['enabled'] = enabled;
    final out = await _post('/v1/teams/${cfg.teamId}/schedules', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// PATCHes an existing schedule. Only cron schedules accept [cronExpr].
  Future<void> patchSchedule(
    String id, {
    bool? enabled,
    String? cronExpr,
    Map<String, dynamic>? parameters,
  }) async {
    final body = <String, dynamic>{};
    if (enabled != null) body['enabled'] = enabled;
    if (cronExpr != null) body['cron_expr'] = cronExpr;
    if (parameters != null) body['parameters_json'] = parameters;
    await _patch('/v1/teams/${cfg.teamId}/schedules/$id', body);
  }

  Future<void> deleteSchedule(String id) =>
      _delete('/v1/teams/${cfg.teamId}/schedules/$id');

  /// Manually fires a schedule, creating a plan row. Works for any
  /// trigger_kind. Returns the new plan id.
  Future<String> runSchedule(String id) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/schedules/$id/run',
      const <String, dynamic>{},
    );
    return ((out as Map).cast<String, dynamic>()['plan_id'] ?? '').toString();
  }

  // ---- runs (blueprint §6.5) ----
  //
  // Runs are hub's lightweight record of a compute activity. Bulk data
  // (checkpoints, tb logs) lives on the producing host; hub only stores
  // name/status/metric URIs.

  Future<List<Map<String, dynamic>>> listRuns({
    String? projectId,
    String? status,
    int? limit,
  }) async {
    final q = <String, String>{};
    if (projectId != null && projectId.isNotEmpty) q['project'] = projectId;
    if (status != null && status.isNotEmpty) q['status'] = _runStatusToServer(status);
    if (limit != null) q['limit'] = '$limit';
    final rows = await _listJson(
      '/v1/teams/${cfg.teamId}/runs',
      query: q.isEmpty ? null : q,
    );
    return [for (final r in rows) _runRowToUI(r)];
  }

  Future<Map<String, dynamic>> getRun(String runId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/runs/$runId');
    return _runRowToUI((out as Map).cast<String, dynamic>());
  }

  Future<Map<String, dynamic>> createRun({
    required String projectId,
    required String kind, // e.g. 'train', 'eval', 'notebook'
    String? agentId,
    String? parentRunId,
    String? name,
    Map<String, dynamic>? metadata,
  }) async {
    final body = <String, dynamic>{'project_id': projectId, 'kind': kind};
    if (agentId != null) body['agent_id'] = agentId;
    if (parentRunId != null) body['parent_run_id'] = parentRunId;
    if (name != null) body['name'] = name;
    if (metadata != null) body['metadata_json'] = metadata;
    final out = await _post('/v1/teams/${cfg.teamId}/runs', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> completeRun(
    String runId, {
    required String status, // 'succeeded' | 'failed' | 'cancelled'
    String? summary,
  }) async {
    // Server vocabulary uses 'completed'; the rest of the mobile UI says
    // 'succeeded'. Translate at the boundary.
    final body = <String, dynamic>{'status': _runStatusToServer(status)};
    if (summary != null) body['summary'] = summary;
    await _post('/v1/teams/${cfg.teamId}/runs/$runId/complete', body);
  }

  // UI run-status 'succeeded' ↔ server 'completed'. All other values pass
  // through unchanged.
  String _runStatusToServer(String uiStatus) =>
      uiStatus == 'succeeded' ? 'completed' : uiStatus;

  Map<String, dynamic> _runRowToUI(Map<String, dynamic> row) {
    if (row['status'] == 'completed') {
      return {...row, 'status': 'succeeded'};
    }
    return row;
  }

  Future<void> attachRunMetricURI(
    String runId, {
    required String kind, // e.g. 'tensorboard', 'wandb'
    required String uri,
  }) async {
    await _post(
      '/v1/teams/${cfg.teamId}/runs/$runId/metric_uri',
      {'kind': kind, 'uri': uri},
    );
  }

  /// Pulls the run's metric digests — one row per metric name, each with
  /// a downsampled [[step, value], ...] points array. The host-runner
  /// metric poller (trackio/wandb/tensorboard) writes these; the mobile
  /// app renders them as inline sparklines. Bulk time-series stay on the
  /// host per blueprint §4.
  Future<List<Map<String, dynamic>>> getRunMetrics(String runId) =>
      _listJson('/v1/teams/${cfg.teamId}/runs/$runId/metrics');

  /// Lists a run's image-panel entries — the wandb "Images" equivalent.
  /// Each row carries a `metric_name` + `step` + `blob_sha`. The mobile UI
  /// groups by metric_name and fetches frame bytes lazily via
  /// [downloadBlob] as the slider is scrubbed.
  Future<List<Map<String, dynamic>>> getRunImages(
    String runId, {
    String? metric,
  }) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/runs/$runId/images',
        query: metric == null ? null : {'metric': metric},
      );

  /// Per-project sweep summary — one row per run in this project, each
  /// carrying {run_id, status, config_json (string), final_metrics
  /// (map name→last value), created_at}. Feeds the cross-run scatter
  /// panel (wandb "parallel coords" archetype) on the Project detail
  /// screen. Avoids the N+1 fan-out that listRuns + getRunMetrics would
  /// require.
  Future<List<Map<String, dynamic>>> getProjectSweepSummary(
          String projectId) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/projects/$projectId/sweep-summary',
      );

  /// Lists a run's histogram entries — the wandb "Distributions" panel.
  /// Each row is `{name, step, buckets: {edges, counts}, updated_at}`.
  /// The mobile widget groups by metric_name and renders a scrubber
  /// across steps so the distribution shift over training is visible.
  Future<List<Map<String, dynamic>>> getRunHistograms(
    String runId, {
    String? metric,
  }) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/runs/$runId/histograms',
        query: metric == null ? null : {'metric': metric},
      );

  /// Upserts histogram digests for a run. Body shape:
  ///   [{"name":..., "step":N, "buckets":{"edges":[...],"counts":[...]}}]
  /// Rows are keyed by (run, metric_name, step) — PUTs for the same
  /// triple replace the stored buckets. Used by host-runners that
  /// forward binned tensors from trackio/wandb. Not called from the
  /// mobile UI today; exposed so tests (and future import flows) can
  /// populate histograms without hitting the database directly.
  Future<void> putRunHistograms(
    String runId,
    List<Map<String, dynamic>> histograms,
  ) =>
      _put(
        '/v1/teams/${cfg.teamId}/runs/$runId/histograms',
        {'histograms': histograms},
      );

  // ---- documents + reviews (blueprint §6.7, §6.8) ----

  Future<List<Map<String, dynamic>>> listDocuments({String? projectId}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/documents',
        query: projectId == null ? null : {'project': projectId},
      );

  Future<Map<String, dynamic>> getDocument(String docId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/documents/$docId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Either [contentInline] or [artifactId] must be non-null (server enforces
  /// a XOR CHECK constraint).
  Future<Map<String, dynamic>> createDocument({
    required String projectId,
    required String kind, // e.g. 'report', 'design', 'note'
    required String title,
    String? contentInline,
    String? artifactId,
    String? authorAgentId,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'kind': kind,
      'title': title,
    };
    if (contentInline != null) body['content_inline'] = contentInline;
    if (artifactId != null) body['artifact_id'] = artifactId;
    if (authorAgentId != null) body['author_agent_id'] = authorAgentId;
    final out = await _post('/v1/teams/${cfg.teamId}/documents', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<List<Map<String, dynamic>>> listDocumentVersions(String docId) =>
      _listJson('/v1/teams/${cfg.teamId}/documents/$docId/versions');

  Future<List<Map<String, dynamic>>> listReviews({
    String? projectId,
    String? status, // UI value: 'pending' | 'approved' | 'rejected' | 'needs_changes'
  }) async {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) {
      // Backend reads `state` and expects 'request_changes', not 'needs_changes'.
      q['state'] = _reviewStateToServer(status);
    }
    final rows = await _listJson(
      '/v1/teams/${cfg.teamId}/reviews',
      query: q.isEmpty ? null : q,
    );
    return [for (final r in rows) _reviewRowToUI(r)];
  }

  Future<Map<String, dynamic>> getReview(String reviewId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/reviews/$reviewId');
    return _reviewRowToUI((out as Map).cast<String, dynamic>());
  }

  // Review state lives in the UI as 'needs_changes' but the backend column
  // holds 'request_changes'. Translate at the client boundary so callers
  // don't need to know the difference.
  String _reviewStateToServer(String uiState) =>
      uiState == 'needs_changes' ? 'request_changes' : uiState;

  Map<String, dynamic> _reviewRowToUI(Map<String, dynamic> row) {
    if (row['state'] == 'request_changes') {
      return {...row, 'state': 'needs_changes'};
    }
    return row;
  }

  Future<Map<String, dynamic>> createReview({
    required String projectId,
    required String targetKind, // 'document' | 'artifact'
    required String targetId,
    String? note,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'target_kind': targetKind,
      'target_id': targetId,
    };
    if (note != null && note.isNotEmpty) body['comment'] = note;
    final out = await _post('/v1/teams/${cfg.teamId}/reviews', body);
    return _reviewRowToUI((out as Map).cast<String, dynamic>());
  }

  Future<void> decideReview(
    String reviewId, {
    required String decision, // 'approved' | 'rejected' | 'needs_changes'
    String? note,
  }) async {
    final body = <String, dynamic>{'state': _reviewStateToServer(decision)};
    if (note != null) body['comment'] = note;
    await _post(
      '/v1/teams/${cfg.teamId}/reviews/$reviewId/decide',
      body,
    );
  }

  // ---- plans + plan_steps (blueprint §6.2) ----

  Future<List<Map<String, dynamic>>> listPlans({
    String? projectId,
    String? status,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) q['status'] = status;
    return _listJson(
      '/v1/teams/${cfg.teamId}/plans',
      query: q.isEmpty ? null : q,
    );
  }

  Future<Map<String, dynamic>> getPlan(String planId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/plans/$planId');
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> createPlan({
    required String projectId,
    String? templateId,
    int? version,
    Map<String, dynamic>? spec,
  }) async {
    final body = <String, dynamic>{'project_id': projectId};
    if (templateId != null) body['template_id'] = templateId;
    if (version != null) body['version'] = version;
    if (spec != null) body['spec_json'] = spec;
    final out = await _post('/v1/teams/${cfg.teamId}/plans', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> updatePlan(
    String planId, {
    String? status, // blueprint §6.2 lifecycle values
    Map<String, dynamic>? spec,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (spec != null) body['spec_json'] = spec;
    await _patch('/v1/teams/${cfg.teamId}/plans/$planId', body);
  }

  Future<List<Map<String, dynamic>>> listPlanSteps(String planId) =>
      _listJson('/v1/teams/${cfg.teamId}/plans/$planId/steps');

  Future<Map<String, dynamic>> createPlanStep(
    String planId, {
    required int phaseIdx,
    required int stepIdx,
    required String kind, // 'agent_spawn' | 'llm_call' | 'shell' | 'mcp_call' | 'human_decision'
    Map<String, dynamic>? spec,
  }) async {
    final body = <String, dynamic>{
      'phase_idx': phaseIdx,
      'step_idx': stepIdx,
      'kind': kind,
    };
    if (spec != null) body['spec_json'] = spec;
    final out = await _post(
      '/v1/teams/${cfg.teamId}/plans/$planId/steps',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> updatePlanStep(
    String planId,
    String stepId, {
    String? status,
    String? agentId,
    Map<String, dynamic>? inputRefs,
    Map<String, dynamic>? outputRefs,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (agentId != null) body['agent_id'] = agentId;
    if (inputRefs != null) body['input_refs_json'] = inputRefs;
    if (outputRefs != null) body['output_refs_json'] = outputRefs;
    await _patch(
      '/v1/teams/${cfg.teamId}/plans/$planId/steps/$stepId',
      body,
    );
  }

  // ---- host mutations (blueprint §5.3.2 / §5.3.3, P0.6) ----

  /// Non-secret SSH connection hints the hub stores to help the phone bind
  /// a hub-registered host to a local Connection. Secret keys (password,
  /// private_key, passphrase, secret, token) are rejected by the server.
  Future<void> updateHostSSHHint(
    String hostId,
    Map<String, dynamic> hint,
  ) async {
    await _patch(
      '/v1/teams/${cfg.teamId}/hosts/$hostId/ssh_hint',
      {'ssh_hint_json': hint},
    );
  }

  /// Replaces the host's capabilities map (binary presence/version). Probed
  /// by host-runner and heartbeated up; clients read this to drive the
  /// driving-mode fallback list.
  Future<void> updateHostCapabilities(
    String hostId,
    Map<String, dynamic> capabilities,
  ) async {
    final req = await _open(
      'PUT',
      '/v1/teams/${cfg.teamId}/hosts/$hostId/capabilities',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode({'capabilities_json': capabilities})));
    final resp = await req.close();
    await _readJson(resp);
  }

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

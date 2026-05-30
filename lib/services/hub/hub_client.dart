import 'dart:convert';
import 'dart:io';

import 'blob_bytes_cache.dart';
import 'hub_read_through.dart';
import 'admin_api.dart';
import 'attention_api.dart';
import 'blobs_api.dart';
import 'events_api.dart';
import 'hub_snapshot_cache.dart';
import 'hub_transport.dart';
import 'search_api.dart';
import 'system_api.dart';

// HubConfig, HubApiError, and HubTransport now live in hub_transport.dart;
// re-exported here so the many `import '.../hub_client.dart'` call sites
// keep resolving them unchanged (see docs/plans/hub-client-split.md).
export 'hub_transport.dart' show HubConfig, HubApiError, HubTransport;

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

  /// Shared HTTP + cache transport. Held privately; the per-domain
  /// sub-clients (see docs/plans/hub-client-split.md) are constructed
  /// against it as each wedge peels them off. The legacy method bodies
  /// below still reach it through the private `_get`/`_post`/… shims.
  final HubTransport _t;

  HubClient(this.cfg) : _t = HubTransport(cfg);

  /// The shared transport, exposed for the per-domain sub-clients.
  HubTransport get transport => _t;

  // Per-domain sub-clients (docs/plans/hub-client-split.md). The legacy
  // methods below delegate to these; call sites may also use them directly.

  /// Hub-level endpoints: probe/stats, insights, tokens, governance config.
  late final SystemApi system = SystemApi(_t);

  /// Content-addressed blob upload/download (+ disk-first cache).
  late final BlobsApi blobs = BlobsApi(_t);

  /// Full-text event search + the team audit-event feed.
  late final SearchApi search = SearchApi(_t);

  /// Server-Sent Events channel streaming.
  late final EventsApi events = EventsApi(_t);

  /// Attention queue: list (live + cached), decide/resolve, context.
  late final AttentionApi attention = AttentionApi(_t);

  /// Operator admin & ops: fleet shutdown/restart/update, DB vacuum,
  /// token rotation, cross-team audit, and the team policy.yaml editor.
  late final AdminApi admin = AdminApi(_t);

  /// Optional read-through cache for list/get responses. Set by the
  /// provider after construction; forwarded to the transport so the
  /// sub-clients and the legacy methods share one cache. When null, the
  /// *Cached methods act as thin wrappers — no offline fallback.
  HubSnapshotCache? get snapshotCache => _t.snapshotCache;
  set snapshotCache(HubSnapshotCache? v) => _t.snapshotCache = v;

  /// Optional content-addressed cache for `/v1/blobs/{sha}` bytes. When
  /// null, downloadBlob degrades to a pure network fetch.
  BlobBytesCache? get blobCache => _t.blobCache;
  set blobCache(BlobBytesCache? v) => _t.blobCache = v;

  void close() {
    _t.close();
  }

  // ---- transport shims ----
  //
  // Forward to [HubTransport] so the existing method bodies below stay
  // byte-for-byte unchanged while the per-domain sub-clients are peeled
  // off one wedge at a time. As each domain moves to its own *Api class
  // it calls `_t.<verb>` directly; the matching shim is removed once its
  // last caller has left.

  String get _cacheHubKey => _t.cacheHubKey;

  List<Map<String, dynamic>> _decodeListMaps(Object body) =>
      _t.decodeListMaps(body);

  Map<String, dynamic> _decodeMap(Object body) => _t.decodeMap(body);

  Future<void> _invalidate(String prefix) => _t.invalidate(prefix);

  Future<HttpClientRequest> _open(
    String method,
    String path, {
    Map<String, String>? query,
    bool auth = true,
  }) =>
      _t.open(method, path, query: query, auth: auth);

  Future<dynamic> _readJson(HttpClientResponse resp) => _t.readJson(resp);

  Future<dynamic> _get(String path,
          {Map<String, String>? query, bool auth = true}) =>
      _t.get(path, query: query, auth: auth);

  Future<dynamic> _post(String path, Object body,
          {Map<String, String>? query}) =>
      _t.post(path, body, query: query);

  Future<dynamic> _patch(String path, Object body) => _t.patch(path, body);

  Future<dynamic> _put(String path, Object body) => _t.put(path, body);

  Future<void> _delete(String path) => _t.delete(path);

  // ---- info / probe / insights → SystemApi (W2) ----

  Future<Map<String, dynamic>> getInfo() => system.getInfo();

  Future<void> verifyAuth() => system.verifyAuth();

  Future<Map<String, dynamic>> getHubStats() => system.getHubStats();

  Future<Map<String, dynamic>> getInsights({
    String? projectId,
    String? teamId,
    String? agentId,
    String? engine,
    String? hostId,
    bool stewardOnly = false,
    DateTime? since,
    DateTime? until,
  }) =>
      system.getInsights(
        projectId: projectId,
        teamId: teamId,
        agentId: agentId,
        engine: engine,
        hostId: hostId,
        stewardOnly: stewardOnly,
        since: since,
        until: until,
      );

  Future<CachedResponse<Map<String, dynamic>>> getInsightsCached({
    String? projectId,
    String? teamId,
    String? agentId,
    String? engine,
    String? hostId,
    bool stewardOnly = false,
    DateTime? since,
    DateTime? until,
  }) =>
      system.getInsightsCached(
        projectId: projectId,
        teamId: teamId,
        agentId: agentId,
        engine: engine,
        hostId: hostId,
        stewardOnly: stewardOnly,
        since: since,
        until: until,
      );

  // ---- collections ----

  Future<List<Map<String, dynamic>>> listHosts() =>
      _listJson('/v1/teams/${cfg.teamId}/hosts');

  /// Read-through variant of [listHosts]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listHostsCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/hosts',
        fetch: listHosts,
        decode: _decodeListMaps,
      );

  Future<List<Map<String, dynamic>>> listAgents({
    bool includeArchived = false,
    // Default hides terminated/failed/crashed rows so long-running teams
    // don't accumulate clutter in the agent list (v1.0.606). Pass true
    // for surfaces that need historical agents — Budget rollups need
    // them for accurate spend, the Archived screen needs them because
    // archived rows are usually terminal.
    bool includeTerminated = false,
    String? projectId,
  }) {
    final q = <String, String>{};
    if (includeArchived) q['include_archived'] = '1';
    if (includeTerminated) q['include_terminated'] = '1';
    if (projectId != null && projectId.isNotEmpty) {
      q['project_id'] = projectId;
    }
    return _listJson(
      '/v1/teams/${cfg.teamId}/agents',
      query: q.isEmpty ? null : q,
    );
  }

  /// Read-through variant of [listAgents]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAgentsCached({
    bool includeArchived = false,
    bool includeTerminated = false,
    String? projectId,
  }) {
    final q = <String, String>{};
    if (includeArchived) q['include_archived'] = '1';
    if (includeTerminated) q['include_terminated'] = '1';
    if (projectId != null && projectId.isNotEmpty) {
      q['project_id'] = projectId;
    }
    final query = q.isEmpty ? null : q;
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${cfg.teamId}/agents', query),
      fetch: () => listAgents(
        includeArchived: includeArchived,
        includeTerminated: includeTerminated,
        projectId: projectId,
      ),
      decode: _decodeListMaps,
    );
  }

  /// Single-agent fetch. Includes `spawn_spec_yaml` + `spawn_authority`
  /// pulled from the agent_spawns join when the agent was created via
  /// /spawn. Agents created by other means (hand-crafted inserts) simply
  /// won't carry those fields.
  Future<Map<String, dynamic>> getAgent(String agentId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/agents/$agentId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getAgent]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getAgentCached(
    String agentId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/agents/$agentId',
        fetch: () => getAgent(agentId),
        decode: _decodeMap,
      );

  /// Parent→child spawn edges. Each row has `parent_agent_id`,
  /// `child_agent_id`, `handle`, `kind`, `status`, plus the original
  /// spawn metadata. Used to render the agent org chart.
  Future<List<Map<String, dynamic>>> listSpawns() =>
      _listJson('/v1/teams/${cfg.teamId}/agents/spawns');

  /// Read-through variant of [listSpawns]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listSpawnsCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/agents/spawns',
        fetch: listSpawns,
        decode: _decodeListMaps,
      );

  Future<List<Map<String, dynamic>>> listProjects({bool? isTemplate}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/projects',
        query: isTemplate == null
            ? null
            : {'is_template': isTemplate ? 'true' : 'false'},
      );

  /// Fetches a single project by id (`/v1/teams/{team}/projects/{id}`).
  /// Used by pull-to-refresh on the project detail screen so the owner
  /// can pick up server-side resolution (overview_widget,
  /// phase_tiles_template, etc.) without re-loading the whole team list.
  Future<Map<String, dynamic>> getProject(String projectId) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [listProjects]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectsCached({
    bool? isTemplate,
  }) {
    final q = isTemplate == null
        ? null
        : {'is_template': isTemplate ? 'true' : 'false'};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${cfg.teamId}/projects', q),
      fetch: () => listProjects(isTemplate: isTemplate),
      decode: _decodeListMaps,
    );
  }

  Future<List<Map<String, dynamic>>> listChannels(String projectId) =>
      _listJson('/v1/teams/${cfg.teamId}/projects/$projectId/channels');

  /// Read-through variant of [listChannels]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listChannelsCached(
    String projectId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint:
            '/v1/teams/${cfg.teamId}/projects/$projectId/channels',
        fetch: () => listChannels(projectId),
        decode: _decodeListMaps,
      );

  /// Team-scope channels (project_id NULL, scope_kind='team'). `#hub-meta`
  /// is auto-seeded by hub init — it's the principal↔steward room.
  Future<List<Map<String, dynamic>>> listTeamChannels() =>
      _listJson('/v1/teams/${cfg.teamId}/channels');

  /// Read-through variant of [listTeamChannels]; see [listRunsCached] for
  /// the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listTeamChannelsCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/channels',
        fetch: listTeamChannels,
        decode: _decodeListMaps,
      );

  Future<Map<String, dynamic>> createTeamChannel(String name) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/channels',
      {'name': name},
    );
    await _invalidate('/v1/teams/${cfg.teamId}/channels');
    return (out as Map).cast<String, dynamic>();
  }

  /// Humans don't have a dedicated table; they're tracked as `auth_tokens`
  /// rows with `scope.role='principal'`. This endpoint coalesces by
  /// `scope.handle`, returning one row per unique handle plus a bucket for
  /// unnamed tokens.
  Future<List<Map<String, dynamic>>> listPrincipals() =>
      _listJson('/v1/teams/${cfg.teamId}/principals');

  /// Lists attention items for the team.
  ///
  /// `includeEscalated` (ADR-030 W19.6, v1.0.687-alpha) is forwarded
  /// as the `include_escalated` query param. In MVP the hub treats it
  /// as a no-op forward-compat hook (the baseline already returns all
  /// rows regardless of tier; widening has nothing to widen against
  /// until a `?tier=<t>` filter is wired). Phase 3 mobile passes
  /// `true` unconditionally so the contract is locked in before any
  /// tier-narrowing lands.
  Future<List<Map<String, dynamic>>> listAttention({
    String? status,
    bool includeEscalated = false,
  }) =>
      attention.listAttention(
        status: status,
        includeEscalated: includeEscalated,
      );

  // ---- sessions (W2-S1) ----
  //
  // Sessions are the durable conversational frame around a steward
  // (or any agent) — a transcript that survives a host-runner restart
  // because session_id is stamped on agent_events independent of the
  // agent process behind it. Status is active | paused | archived
  // (per ADR-009; legacy hubs may still emit open | interrupted | closed).

  Future<List<Map<String, dynamic>>> listSessions({String? status}) =>
      _listJson(
        '/v1/teams/${cfg.teamId}/sessions',
        query: status == null ? null : {'status': status},
      );

  /// Read-through variant of [listSessions]; see [listAttentionCached]
  /// for the offline-fallback contract. Sessions are the navigational
  /// primitive of the Stewards page — without this, an airplane-mode
  /// open would show "no stewards" even when prior fetches populated
  /// the cache.
  Future<CachedResponse<List<Map<String, dynamic>>>> listSessionsCached({
    String? status,
  }) {
    final q = status == null ? null : {'status': status};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint:
          buildEndpointKey('/v1/teams/${cfg.teamId}/sessions', q),
      fetch: () => listSessions(status: status),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getSession(String id) async {
    final out = await _get('/v1/teams/${cfg.teamId}/sessions/$id');
    return (out as Map).cast<String, dynamic>();
  }

  /// Per-session imputed cost breakdown (ADR-036 D8 chip 2 + tooltip).
  /// Returns `{session_id, total_usd, breakdown_by_model, tokens_by_model,
  /// missing_models, snapshot_date, origin, imputed}`. The `imputed: true`
  /// flag is always set and carries the subscription-disclaimer semantics
  /// (subscription users aren't actually billed per-token; the numbers
  /// are estimates against the public API rate sheet).
  ///
  /// Returns null on any error so the chip self-gates blank rather than
  /// stalling the UI (the parent GET inlines `session_cost_usd_imputed`
  /// for first-paint; this endpoint serves only the tooltip detail).
  Future<Map<String, dynamic>?> getSessionCost(String id) async {
    try {
      final out = await _get('/v1/teams/${cfg.teamId}/sessions/$id/cost');
      if (out is Map) return out.cast<String, dynamic>();
      return null;
    } catch (_) {
      return null;
    }
  }

  /// Opens a new session. Most callers will pass `agentId` to attach
  /// the session to an existing steward; `worktreePath` and
  /// `spawnSpecYaml` are needed when the resume flow (W2-S3) wants to
  /// respawn cleanly without the user re-picking engine/scope.
  Future<Map<String, dynamic>> openSession({
    String? title,
    String? scopeKind,
    String? scopeId,
    String? agentId,
    String? worktreePath,
    String? spawnSpecYaml,
  }) async {
    final body = <String, dynamic>{};
    if (title != null && title.isNotEmpty) body['title'] = title;
    if (scopeKind != null && scopeKind.isNotEmpty) body['scope_kind'] = scopeKind;
    if (scopeId != null && scopeId.isNotEmpty) body['scope_id'] = scopeId;
    if (agentId != null && agentId.isNotEmpty) body['agent_id'] = agentId;
    if (worktreePath != null && worktreePath.isNotEmpty) {
      body['worktree_path'] = worktreePath;
    }
    if (spawnSpecYaml != null && spawnSpecYaml.isNotEmpty) {
      body['spawn_spec_yaml'] = spawnSpecYaml;
    }
    final out = await _post('/v1/teams/${cfg.teamId}/sessions', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Renames a session. Empty title clears it back to the default
  /// "(untitled session)" rendering on mobile.
  Future<void> renameSession(String id, String title) async {
    await _patch('/v1/teams/${cfg.teamId}/sessions/$id', {'title': title});
  }

  /// Archives a session (was: closeSession). The hub's /close endpoint
  /// is kept as an alias by ADR-009 for one release; this calls
  /// /archive directly.
  Future<void> archiveSession(String id) async {
    // _post requires a non-null body; an empty map is the canonical
    // payload for endpoints that take none.
    await _post(
        '/v1/teams/${cfg.teamId}/sessions/$id/archive', const <String, dynamic>{});
  }

  /// Resumes a paused session: spawns a new agent with the same
  /// handle/kind/host as the session's prior current_agent_id,
  /// reusing the worktree_path and spawn_spec_yaml captured at open
  /// time. Returns `{session_id, new_agent_id, prior_agent_id, spawn_id}`.
  Future<Map<String, dynamic>> resumeSession(String id) async {
    final out = await _post(
        '/v1/teams/${cfg.teamId}/sessions/$id/resume',
        const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Phase 1.5c — full-text search across session transcripts.
  /// Returns a list of result rows shaped:
  ///   { event_id, session_id, scope_kind, scope_id, session_title,
  ///     seq, ts, kind, snippet }
  /// `query` is an FTS5 MATCH expression. `limit` defaults to 50.
  Future<List<Map<String, dynamic>>> searchSessions(
    String query, {
    int? limit,
  }) async {
    final q = <String, String>{'q': query};
    if (limit != null) q['limit'] = '$limit';
    final out = await _get(
      '/v1/teams/${cfg.teamId}/sessions/search',
      query: q,
    );
    return (out as List).cast<Map<String, dynamic>>();
  }

  /// Forks an archived session into a new active one (ADR-009 D4).
  /// The new session copies scope from the source and attaches to
  /// the team's live steward (or [agentId] if provided). Returns
  /// `{session_id, source_session_id, agent_id, scope_kind,
  /// scope_id, title}`.
  Future<Map<String, dynamic>> forkSession(
    String id, {
    String? agentId,
    String? title,
  }) async {
    final body = <String, dynamic>{};
    if (agentId != null && agentId.isNotEmpty) body['agent_id'] = agentId;
    if (title != null && title.isNotEmpty) body['title'] = title;
    final out = await _post(
        '/v1/teams/${cfg.teamId}/sessions/$id/fork', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Soft-deletes an archived session and clears its session_id from
  /// transcript / audit / attention rows. Hub refuses with 409 if
  /// the session is still active or paused (archive it first).
  /// Idempotent: deleting an already-deleted session returns 204.
  Future<void> deleteSession(String id) async {
    await _delete('/v1/teams/${cfg.teamId}/sessions/$id');
  }

  /// Read-through variant of [listAttention]; see [listRunsCached] for the
  /// offline-fallback contract. `includeEscalated` (ADR-030 W19.6)
  /// forwards through to the wire call.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAttentionCached({
    String? status,
    bool includeEscalated = false,
  }) =>
      attention.listAttentionCached(
        status: status,
        includeEscalated: includeEscalated,
      );

  Future<List<Map<String, dynamic>>> listTasks(
    String projectId, {
    String? status,
    String? priority,
    String? sort,
  }) {
    final q = <String, String>{};
    if (status != null && status.isNotEmpty) q['status'] = status;
    if (priority != null && priority.isNotEmpty) q['priority'] = priority;
    if (sort != null && sort.isNotEmpty) q['sort'] = sort;
    return _listJson(
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks',
      query: q.isEmpty ? null : q,
    );
  }

  Future<Map<String, dynamic>> getTask(String projectId, String taskId) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks/$taskId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [listTasks]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listTasksCached(
    String projectId, {
    String? status,
    String? priority,
    String? sort,
  }) {
    final q = <String, String>{};
    if (status != null && status.isNotEmpty) q['status'] = status;
    if (priority != null && priority.isNotEmpty) q['priority'] = priority;
    if (sort != null && sort.isNotEmpty) q['sort'] = sort;
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/projects/$projectId/tasks',
        q.isEmpty ? null : q,
      ),
      fetch: () => listTasks(
        projectId,
        status: status,
        priority: priority,
        sort: sort,
      ),
      decode: _decodeListMaps,
    );
  }

  /// Read-through variant of [getTask]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getTaskCached(
    String projectId,
    String taskId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint:
            '/v1/teams/${cfg.teamId}/projects/$projectId/tasks/$taskId',
        fetch: () => getTask(projectId, taskId),
        decode: _decodeMap,
      );

  Future<Map<String, dynamic>> patchTask(
    String projectId,
    String taskId, {
    String? status,
    String? title,
    String? bodyMd,
    String? priority,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (title != null) body['title'] = title;
    if (bodyMd != null) body['body_md'] = bodyMd;
    if (priority != null) body['priority'] = priority;
    final req = await _open(
      'PATCH',
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks/$taskId',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    final out = await _readJson(resp);
    await _invalidate('/v1/teams/${cfg.teamId}/projects/$projectId/tasks');
    // Hub PATCH returns 204 No Content; re-fetch to return the fresh row
    // so callers can setState with the updated task without a second trip.
    if (out == null) {
      return getTask(projectId, taskId);
    }
    return (out as Map).cast<String, dynamic>();
  }

  Future<List<Map<String, dynamic>>> listTemplates() =>
      _listJson('/v1/teams/${cfg.teamId}/templates');

  /// Read-through variant of [listTemplates]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listTemplatesCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/templates',
        fetch: listTemplates,
        decode: _decodeListMaps,
      );

  /// Returns raw template body (YAML / markdown / JSON — the endpoint
  /// doesn't parse). Caller renders as text.
  ///
  /// When [merged] is true, the server overlays the on-disk template
  /// onto the embedded built-in (disk wins per-key, missing keys fall
  /// through). Use this from spawn callers that need a complete spec
  /// even when the disk copy is stale. The editor calls without
  /// [merged] so user comments are preserved on round-trip.
  Future<String> getTemplate(
    String category,
    String name, {
    bool merged = false,
  }) async {
    final path = '/v1/teams/${cfg.teamId}/templates/$category/$name'
        '${merged ? '?merge=1' : ''}';
    final req = await _open('GET', path);
    final resp = await req.close();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final msg = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, msg);
    }
    return resp.transform(utf8.decoder).join();
  }

  /// Writes (creates or overwrites) a template file. Body is the raw
  /// editor contents — server treats yaml/markdown/json bytes verbatim.
  /// Returns server-confirmed `{category, name, size}`.
  Future<Map<String, dynamic>> putTemplate(
    String category,
    String name,
    String body,
  ) async {
    final req = await _open(
      'PUT',
      '/v1/teams/${cfg.teamId}/templates/$category/$name',
    );
    req.headers.contentType =
        ContentType('application', _mimeForName(name), charset: 'utf-8');
    req.add(utf8.encode(body));
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
    return (jsonDecode(raw) as Map).cast<String, dynamic>();
  }

  /// Re-walks the embedded templates FS and overwrites the on-disk copy
  /// with the bundled bytes. Use case: after a hub upgrade ships a fixed
  /// bundled template (e.g. ADR-029's close-out footer), the operator
  /// taps "Reset bundled templates" to pick up the new version without
  /// per-file deletes. User-only files (no embedded counterpart) are
  /// preserved — see hub-side `handleResetBundledTemplates` for the
  /// contract. Returns `{overwritten, created}` counts.
  Future<Map<String, dynamic>> resetBundledTemplates() async {
    final out = await _post(
        '/v1/teams/${cfg.teamId}/templates/reset', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Deletes a template file. The bundled defaults live in the embedded
  /// FS, so deleting a disk file falls back to the built-in on next read.
  Future<void> deleteTemplate(String category, String name) async {
    final req = await _open(
      'DELETE',
      '/v1/teams/${cfg.teamId}/templates/$category/$name',
    );
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
  }

  /// Lists known agent families: embedded defaults plus any operator-
  /// authored overrides. Each entry carries a `source` field
  /// ("embedded" | "override" | "custom") so the UI can render a chip.
  Future<List<Map<String, dynamic>>> listAgentFamilies() async {
    final out = await _get('/v1/teams/${cfg.teamId}/agent-families');
    final fams = (out as Map)['families'] as List? ?? const [];
    return fams.map((e) => (e as Map).cast<String, dynamic>()).toList();
  }

  /// Read-through variant of [listAgentFamilies]; see [listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>>
      listAgentFamiliesCached() =>
          readThrough<List<Map<String, dynamic>>>(
            cache: snapshotCache,
            hubKey: _cacheHubKey,
            endpoint: '/v1/teams/${cfg.teamId}/agent-families',
            fetch: listAgentFamilies,
            decode: _decodeListMaps,
          );

  /// Returns the structured record for one family. The `source` field
  /// disambiguates embedded vs. override vs. custom — callers gate the
  /// editor on this (embedded entries are read-only previews).
  Future<Map<String, dynamic>> getAgentFamily(String family) async {
    final out = await _get('/v1/teams/${cfg.teamId}/agent-families/$family');
    return (out as Map).cast<String, dynamic>();
  }

  /// Writes (creates or overwrites) an agent-family override. Body is
  /// raw YAML for a single family record (no `families:` wrapper).
  /// Server validates strictly — typos in keys or unknown modes 400.
  Future<Map<String, dynamic>> putAgentFamily(
    String family,
    String yamlBody,
  ) async {
    final req = await _open(
      'PUT',
      '/v1/teams/${cfg.teamId}/agent-families/$family',
    );
    req.headers.contentType =
        ContentType('application', 'yaml', charset: 'utf-8');
    req.add(utf8.encode(yamlBody));
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
    return (jsonDecode(raw) as Map).cast<String, dynamic>();
  }

  /// Wipes every agent-family override file so the team falls back to
  /// the embedded defaults. Counterpart to [resetBundledTemplates] —
  /// same "restore bundled defaults" semantic. Operator-authored
  /// custom families (no embedded counterpart) ARE deleted too; the
  /// mobile UI surfaces this in the confirmation dialog. Returns
  /// `{removed}` count.
  Future<Map<String, dynamic>> resetAgentFamilies() async {
    final out = await _post(
        '/v1/teams/${cfg.teamId}/agent-families/reset', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Deletes an agent-family override file. 409 from the backend means
  /// the family is embedded — the caller should disable via override
  /// instead of deleting.
  Future<void> deleteAgentFamily(String family) async {
    final req = await _open(
      'DELETE',
      '/v1/teams/${cfg.teamId}/agent-families/$family',
    );
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
  }

  /// Renames a template within its category. Server refuses overwrites
  /// (409) — UI must surface that as a user-visible error.
  Future<void> renameTemplate(
    String category,
    String name,
    String newName,
  ) async {
    final req = await _open(
      'PATCH',
      '/v1/teams/${cfg.teamId}/templates/$category/$name',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode({'new_name': newName})));
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
  }

  String _mimeForName(String n) {
    final lower = n.toLowerCase();
    if (lower.endsWith('.md')) return 'markdown';
    if (lower.endsWith('.yaml') || lower.endsWith('.yml')) return 'yaml';
    if (lower.endsWith('.json')) return 'json';
    return 'plain';
  }

  // ---- tokens + governance config → SystemApi (W2) ----

  Future<List<Map<String, dynamic>>> listTokens() => system.listTokens();

  Future<Map<String, dynamic>> issueToken({
    String kind = 'user',
    String role = 'principal',
    String? handle,
    String? expiresAt,
  }) =>
      system.issueToken(
        kind: kind,
        role: role,
        handle: handle,
        expiresAt: expiresAt,
      );

  Future<void> revokeToken(String id) => system.revokeToken(id);

  Future<String> getHubRolesConfig() => system.getHubRolesConfig();

  Future<String> putHubRolesConfig(String yaml) =>
      system.putHubRolesConfig(yaml);

  Future<String> resetHubRolesConfig() => system.resetHubRolesConfig();

  // ---- admin / ops fleet (ADR-028 Phase 5) ----
  //
  // All /v1/admin/* routes are owner-scope and hub-wide. A member token
  // gets HubApiError(403) — the Admin pane surfaces that as an
  // "owner token required" message rather than pre-probing the scope.

  /// Lists the fleet for the Admin pane. With [ping] the hub round-trips
  /// host.ping at each live host so each row reports the version it is
  /// actually running right now.
  Future<List<Map<String, dynamic>>> adminListHosts({bool ping = false}) =>
      admin.adminListHosts(ping: ping);

  Future<Map<String, dynamic>> adminHostShutdown(String hostId,
          {String? reason}) =>
      admin.adminHostShutdown(hostId, reason: reason);

  Future<Map<String, dynamic>> adminHostRestart(String hostId,
          {String? reason}) =>
      admin.adminHostRestart(hostId, reason: reason);

  Future<Map<String, dynamic>> adminHostUpdate(String hostId,
          {String? reason}) =>
      admin.adminHostUpdate(hostId, reason: reason);

  Future<Map<String, dynamic>> adminFleetShutdown({String? reason}) =>
      admin.adminFleetShutdown(reason: reason);

  Future<Map<String, dynamic>> adminFleetRestart({String? reason}) =>
      admin.adminFleetRestart(reason: reason);

  Future<Map<String, dynamic>> adminFleetUpdate({String? reason}) =>
      admin.adminFleetUpdate(reason: reason);

  Future<Map<String, dynamic>> adminDbVacuum() => admin.adminDbVacuum();

  Future<Map<String, dynamic>> adminRotateTokens({bool forceRevoke = false}) =>
      admin.adminRotateTokens(forceRevoke: forceRevoke);

  Future<List<Map<String, dynamic>>> adminListAudit({
    String? actionPrefix,
    String? targetKind,
    String? actor,
    String? since,
    int limit = 100,
  }) =>
      admin.adminListAudit(
        actionPrefix: actionPrefix,
        targetKind: targetKind,
        actor: actor,
        since: since,
        limit: limit,
      );

  Future<String> getPolicy() => admin.getPolicy();

  Future<Map<String, dynamic>> getPolicyKinds() => admin.getPolicyKinds();

  Future<void> putPolicy(String yaml) => admin.putPolicy(yaml);

  Future<List<Map<String, dynamic>>> _listJson(
    String path, {
    Map<String, String>? query,
  }) =>
      _t.listJson(path, query: query);

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
    await _invalidate('/v1/teams/${cfg.teamId}/projects');
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
    /// Per-phase tile composition override. Shape:
    /// `{"<phase>": ["documents", "outputs", ...]}`. Pass an empty map
    /// to clear the override (falls back to template YAML + chassis
    /// default). Pass null to leave the existing value untouched.
    Map<String, List<String>>? phaseTileOverrides,
    /// Per-phase hero (overview-widget) override (ADR-024 D10). Shape:
    /// `{"<phase>": "<hero_slug>"}`. Empty map → clear. Null → leave
    /// untouched. Vocab is the closed `kKnownOverviewWidgets` set;
    /// unknown slugs are accepted by the hub but fall back to the
    /// template-side resolution.
    Map<String, String>? overviewWidgetOverrides,

    /// Per-project loop-closure deadline override (ADR-034 amendment).
    /// Minutes. Pass 0 to clear the override (the project reverts to the
    /// hub default budget); null leaves the value unchanged.
    int? loopInactivityMinutes,
    int? loopAbsoluteCapMinutes,
  }) async {
    final body = <String, dynamic>{};
    if (name != null) body['name'] = name;
    if (goal != null) body['goal'] = goal;
    if (kind != null) body['kind'] = kind;
    if (templateId != null) body['template_id'] = templateId;
    if (parameters != null) body['parameters_json'] = parameters;
    if (budgetCents != null) body['budget_cents'] = budgetCents;
    if (loopInactivityMinutes != null) {
      body['loop_inactivity_minutes'] = loopInactivityMinutes;
    }
    if (loopAbsoluteCapMinutes != null) {
      body['loop_absolute_cap_minutes'] = loopAbsoluteCapMinutes;
    }
    if (stewardAgentId != null) body['steward_agent_id'] = stewardAgentId;
    if (onCreateTemplateId != null) {
      body['on_create_template_id'] = onCreateTemplateId;
    }
    if (policyOverrides != null) {
      body['policy_overrides_json'] = policyOverrides;
    }
    if (docsRoot != null) body['docs_root'] = docsRoot;
    if (phaseTileOverrides != null) {
      // Pass empty map through as null on the wire so the server
      // clears the column (consistent with nullRawJSON semantics).
      body['phase_tile_overrides'] =
          phaseTileOverrides.isEmpty ? null : phaseTileOverrides;
    }
    if (overviewWidgetOverrides != null) {
      body['overview_widget_overrides'] =
          overviewWidgetOverrides.isEmpty ? null : overviewWidgetOverrides;
    }
    final out = await _patch(
      '/v1/teams/${cfg.teamId}/projects/$projectId',
      body,
    );
    await _invalidate('/v1/teams/${cfg.teamId}/projects');
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> archiveProject(String projectId) async {
    await _delete('/v1/teams/${cfg.teamId}/projects/$projectId');
    await _invalidate('/v1/teams/${cfg.teamId}/projects');
  }

  /// Reads the current phase + template phase order + transition log
  /// for a project (lifecycle W1). Returns a payload of the shape
  /// `{project_id, phase, phases:[...], history:[...]}` — fields are
  /// empty for lifecycle-disabled projects.
  Future<Map<String, dynamic>> getProjectPhase(String projectId) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/phase',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Walks the project to the next phase in its template's phase order
  /// (lifecycle W1). Throws [HubApiError] with code 409 +
  /// `application/problem+json` body when required acceptance criteria
  /// for the current phase haven't been met. The returned payload has
  /// the same shape as [getProjectPhase].
  Future<Map<String, dynamic>> advanceProjectPhase(
    String projectId, {
    String? toPhase,
    String? reason,
  }) async {
    final body = <String, dynamic>{};
    if (toPhase != null && toPhase.isNotEmpty) body['to_phase'] = toPhase;
    if (reason != null && reason.isNotEmpty) body['reason'] = reason;
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/phase/advance',
      body,
    );
    await _invalidate('/v1/teams/${cfg.teamId}/projects');
    return (out as Map).cast<String, dynamic>();
  }

  /// Admin-only direct phase set (lifecycle W1, hydration / repair). Skips
  /// criteria gating; the audit row is `project.phase_set` (or
  /// `project.phase_reverted` when moving backwards in the template's
  /// order).
  Future<Map<String, dynamic>> setProjectPhase(
    String projectId,
    String phase,
  ) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/phase',
      {'phase': phase},
    );
    await _invalidate('/v1/teams/${cfg.teamId}/projects');
    return (out as Map).cast<String, dynamic>();
  }

  /// Reads the project steward's live state (lifecycle W3 — A3 §10.1).
  /// Cache-Control on the response is `private, no-cache`, so this
  /// always hits the network rather than the snapshot cache.
  /// Returns the JSON map verbatim — `{scope, agent_id, state,
  /// current_action?, handoff?}`.
  Future<Map<String, dynamic>> getStewardState(String projectId) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/steward/state',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Fetches the raw YAML body of a project template by name (W3 YAML
  /// reveal sheet). The hub serves this as `text/yaml` rather than
  /// JSON — caller receives the file contents as a UTF-8 string.
  /// Routes around _readJson because the response body is not JSON.
  Future<String> getProjectTemplateYaml(String name) async {
    final req = await _open(
      'GET',
      '/v1/teams/${cfg.teamId}/templates/projects/$name.yaml',
    );
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  Future<Map<String, dynamic>> createTask(
    String projectId, {
    required String title,
    String? bodyMd,
    String? assigneeId,
    String? parentTaskId,
    String? status,
    String? priority,
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
    if (priority != null && priority.isNotEmpty) body['priority'] = priority;
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/tasks',
      body,
    );
    await _invalidate('/v1/teams/${cfg.teamId}/projects/$projectId/tasks');
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
    await _invalidate('/v1/teams/${cfg.teamId}/projects/$projectId/channels');
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

  /// Read-through caches for channel-event bootstrap. Mirrors the
  /// agent_events tail caching: each call snapshots the most recent
  /// window-sized fetch under one cache row per (channel, limit). SSE
  /// then takes over for live updates into the screen's in-memory
  /// state, so the cache is only consulted on cold open / network
  /// failure. We deliberately omit `since` from the cache key — the
  /// "what did I last read here" snapshot is what offline UX wants;
  /// every distinct since-cursor would otherwise bloat the cache
  /// without any payback for offline reads.
  Future<CachedResponse<List<Map<String, dynamic>>>>
      listTeamChannelEventsCached(
    String channelId, {
    int? limit,
  }) {
    final q = limit == null ? null : {'limit': '$limit'};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/channels/$channelId/events',
        q,
      ),
      fetch: () => listTeamChannelEvents(channelId, limit: limit),
      decode: _decodeListMaps,
    );
  }

  Future<CachedResponse<List<Map<String, dynamic>>>>
      listProjectChannelEventsCached(
    String projectId,
    String channelId, {
    int? limit,
  }) {
    final q = limit == null ? null : {'limit': '$limit'};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/projects/$projectId/channels/$channelId/events',
        q,
      ),
      fetch: () =>
          listProjectChannelEvents(projectId, channelId, limit: limit),
      decode: _decodeListMaps,
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
    String? personaSeed,
    String? permissionMode,
    String? sessionId,
    bool autoOpenSession = false,
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
    if (personaSeed != null && personaSeed.trim().isNotEmpty) {
      body['persona_seed'] = personaSeed.trim();
    }
    if (permissionMode != null && permissionMode.isNotEmpty) {
      body['permission_mode'] = permissionMode;
    }
    // sessionId attaches the spawn to an existing session — used by
    // the "Replace steward" / "Switch engine" flow on the project
    // page so the new agent inherits the session's transcript while
    // the underlying claude/codex/etc. process is restarted with
    // the operator's new engine/model picks.
    if (sessionId != null && sessionId.isNotEmpty) {
      body['session_id'] = sessionId;
    }
    // auto_open_session is the multi-steward UX invariant ("every live
    // steward has a session"). When set + sessionId empty, the hub
    // opens a session pointing at the new agent inside the same tx so
    // the spawn is atomic. Ignored when sessionId is set (the swap
    // path already updates the named session in-tx).
    if (autoOpenSession) {
      body['auto_open_session'] = true;
    }
    final out = await _post('/v1/teams/${cfg.teamId}/agents/spawn', body);
    return (out as Map).cast<String, dynamic>();
  }

  // ---- general steward (singleton, W4) ----

  /// Ensures the team-scoped general steward (`steward.general.v1`,
  /// handle `@steward`) is running, spawning it on first call. The
  /// endpoint is idempotent: subsequent calls return the existing
  /// agent's id. Returns the server envelope verbatim — `agent_id`,
  /// `status`, `already_running`, plus `spawn_id` on first spawn.
  ///
  /// Surface: tap on the home-tab persistent steward card. The card
  /// avoids manual spawn-sheet UX for the always-on concierge — there
  /// is exactly one general steward per team, archived only by
  /// explicit director action.
  Future<Map<String, dynamic>> ensureGeneralSteward({String? hostId}) async {
    final body = <String, dynamic>{};
    if (hostId != null && hostId.isNotEmpty) {
      body['host_id'] = hostId;
    }
    final out = await _post(
      '/v1/teams/${cfg.teamId}/steward.general/ensure',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Ensures the named project's steward (per ADR-025) is running,
  /// spawning one if none exists. Idempotent. Returns the server
  /// envelope — `agent_id`, `project_id`, `status`, `already_running`,
  /// plus `spawn_id` on first spawn.
  ///
  /// `hostId` pins the spawn to a specific host; empty falls back to
  /// the hub's pickFirstHost. `permissionMode` ("skip" / "prompt")
  /// chooses the template's permission flag at spawn time; empty
  /// defaults to "skip" (matches the demo bootstrap).
  Future<Map<String, dynamic>> ensureProjectSteward({
    required String projectId,
    String? hostId,
    String? permissionMode,
  }) async {
    final body = <String, dynamic>{};
    if (hostId != null && hostId.isNotEmpty) {
      body['host_id'] = hostId;
    }
    if (permissionMode != null && permissionMode.isNotEmpty) {
      body['permission_mode'] = permissionMode;
    }
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/steward/ensure',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  // ---- agent lifecycle ----

  /// Terminates an agent by patching status=terminated. The host-runner
  /// will pick up the kill on its next poll.
  Future<void> terminateAgent(String agentId) async {
    await _patch('/v1/teams/${cfg.teamId}/agents/$agentId',
        {'status': 'terminated'});
  }

  /// Renames an agent (handle field). Used by the multi-steward UX to
  /// label stewards (research-steward, infra-steward, …). Server
  /// enforces the live-handle uniqueness — collisions surface as 409
  /// HubApiError so the caller can show "handle already in use".
  Future<void> renameAgent(String agentId, String newHandle) async {
    await _patch('/v1/teams/${cfg.teamId}/agents/$agentId',
        {'handle': newHandle});
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
  /// Post a user-side input event to an agent. `kind` selects the
  /// shape — `text` / `approval` / `answer` / `cancel` / `attach` —
  /// and the relevant fields ride alongside. `answer` is for inline
  /// replies to tool questions (e.g. AskUserQuestion); pass the
  /// originating tool_call id as [requestId] and the chosen reply as
  /// [body].
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
    // ADR-021 W2.1 — set_mode / set_model picker payload. The hub's
    // input handler routes by family.runtime_mode_switch[driving_mode]:
    // rpc/per_turn_argv land as input.* events the driver picks up;
    // respawn triggers respawn-with-mutated-spec. Mobile sends one
    // shape; the wire path varies per engine.
    String? modeId,
    String? modelId,
    // ADR-021 W4.1 — image content blocks alongside body. Each entry
    // is `{mime_type, data}` where data is base64. The hub validates
    // (mime allowlist, ≤5 MiB decoded, ≤3 images) and persists onto
    // payload_json["images"]; per-driver shape mapping lands in
    // W4.2-W4.5. UI surface (composer attach branch) lands in W4.6.
    List<Map<String, String>>? images,
    // artifact-type-registry W7.2 — non-image multimodal attachments.
    // PDFs are cross-engine (Claude document, Codex file_data, Gemini
    // inline_data); audio/video are Gemini-only. Each entry is
    // `{mime_type, data, filename}` with data base64.
    List<Map<String, String>>? pdfs,
    List<Map<String, String>>? audios,
    List<Map<String, String>>? videos,
    // v1.0.707 polish — when true and kind=='text', the hub bypasses
    // the principal-directive envelope wrap so the engine receives
    // the body verbatim. Used by mobile for engine-control slash
    // commands (/clear, /compact, /model …) — wrapping those in
    // "[directive from the principal]\n…\n\nReply in this chat…"
    // turns them into prose the engine ignores. The shape gate is
    // mobile-side (see ComposeBar's isSlashCommandBody); hub honours
    // the flag regardless. Ignored for non-text kinds.
    bool? raw,
  }) async {
    final req = <String, dynamic>{'kind': kind};
    if (body != null) req['body'] = body;
    if (decision != null) req['decision'] = decision;
    if (requestId != null) req['request_id'] = requestId;
    if (optionId != null) req['option_id'] = optionId;
    if (note != null) req['note'] = note;
    if (reason != null) req['reason'] = reason;
    if (documentId != null) req['document_id'] = documentId;
    if (modeId != null) req['mode_id'] = modeId;
    if (modelId != null) req['model_id'] = modelId;
    if (images != null && images.isNotEmpty) req['images'] = images;
    if (pdfs != null && pdfs.isNotEmpty) req['pdfs'] = pdfs;
    if (audios != null && audios.isNotEmpty) req['audios'] = audios;
    if (videos != null && videos.isNotEmpty) req['videos'] = videos;
    if (raw == true) req['raw'] = true;
    final out =
        await _post('/v1/teams/${cfg.teamId}/agents/$agentId/input', req);
    return (out as Map).cast<String, dynamic>();
  }

  /// Backfill events by monotonic seq. Modes (mutually exclusive,
  /// resolved server-side in the order before > tail > since):
  ///   - [since] (exclusive, seq > since, ASC) — incremental tail used
  ///     by SSE reconnect; default behavior when none of the others is set.
  ///   - [tail] true — newest [limit] events, DESC. Used by AgentFeed's
  ///     cold open so a long transcript shows the most recent turns
  ///     instead of the oldest.
  ///   - [before] (exclusive, seq < before, DESC) — load-older paging
  ///     trigger when the user scrolls past the top of the loaded set.
  /// `sessionId` scopes the result to one session — the new-session flow
  /// keeps the same agentId, so without this filter a fresh chat would
  /// replay the prior session's transcript.
  Future<List<Map<String, dynamic>>> listAgentEvents(
    String agentId, {
    int? since,
    int? before,
    String? beforeTs,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) {
    final q = <String, String>{};
    // Cursor precedence on the server is before_ts > before > tail > since.
    // before_ts is the session-scoped variant — seq is per-agent so it
    // can't order events across the agents that one resumed session
    // spans, but ts can.
    if (beforeTs != null && beforeTs.isNotEmpty) {
      q['before_ts'] = beforeTs;
    } else if (before != null) {
      q['before'] = '$before';
    } else if (tail) {
      q['tail'] = 'true';
    } else if (since != null) {
      q['since'] = '$since';
    }
    if (limit != null) q['limit'] = '$limit';
    if (sessionId != null && sessionId.isNotEmpty) q['session'] = sessionId;
    return _listJson(
      '/v1/teams/${cfg.teamId}/agents/$agentId/events',
      query: q.isEmpty ? null : q,
    );
  }

  /// Read-through variant of [listAgentEvents]; see [listAttentionCached]
  /// for the offline-fallback contract. Sessions are the one surface where
  /// dropping the transcript on a flaky network hurts most — opening an
  /// existing session offline now shows the last-seen transcript with an
  /// "Offline · last updated X" hint, instead of an empty error card.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAgentEventsCached(
    String agentId, {
    int? since,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) {
    final q = <String, String>{};
    if (tail) {
      q['tail'] = 'true';
    } else if (since != null) {
      q['since'] = '$since';
    }
    if (limit != null) q['limit'] = '$limit';
    if (sessionId != null && sessionId.isNotEmpty) q['session'] = sessionId;
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/agents/$agentId/events',
        q.isEmpty ? null : q,
      ),
      fetch: () => listAgentEvents(
        agentId,
        since: since,
        tail: tail,
        limit: limit,
        sessionId: sessionId,
      ),
      decode: _decodeListMaps,
    );
  }

  /// Cache-only read of agent events for cold-open render-first-paint.
  /// Returns null when no snapshot exists; otherwise serves the cached
  /// rows immediately without waiting on a network round-trip. Pair
  /// with [listAgentEventsCached] kicked off in parallel (and ignored
  /// on success — SSE with `since=<maxSeq>` does the actual delta
  /// catch-up) so the cache stays warm for next cold-open.
  ///
  /// ADR-006: cache-first beats network-first-with-fallback. The
  /// blocking `await fetch()` in [readThrough] is the wait users feel
  /// when opening a session; this method skips it.
  Future<CachedResponse<List<Map<String, dynamic>>>?>
      listAgentEventsCacheOnly(
    String agentId, {
    int? since,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) async {
    final cache = snapshotCache;
    if (cache == null) return null;
    final q = <String, String>{};
    if (tail) {
      q['tail'] = 'true';
    } else if (since != null) {
      q['since'] = '$since';
    }
    if (limit != null) q['limit'] = '$limit';
    if (sessionId != null && sessionId.isNotEmpty) q['session'] = sessionId;
    final endpoint = buildEndpointKey(
      '/v1/teams/${cfg.teamId}/agents/$agentId/events',
      q.isEmpty ? null : q,
    );
    final snap = await cache.get(_cacheHubKey, endpoint);
    if (snap == null) return null;
    return CachedResponse<List<Map<String, dynamic>>>(
      _decodeListMaps(snap.body),
      snap.fetchedAt,
    );
  }

  /// SSE tail of the agent's event queue → EventsApi (W4). Subscribes
  /// before replaying backfill from [sinceSeq] so no live event is missed.
  Stream<Map<String, dynamic>> streamAgentEvents(
    String agentId, {
    int? sinceSeq,
    String? sessionId,
  }) =>
      events.streamAgentEvents(
        agentId,
        sinceSeq: sinceSeq,
        sessionId: sessionId,
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
    String? optionId,
    String? body,
    bool override = false,
  }) =>
      attention.decideAttention(
        id,
        decision: decision,
        by: by,
        reason: reason,
        optionId: optionId,
        body: body,
        override: override,
      );

  Future<Map<String, dynamic>> getAttentionContext(String id) =>
      attention.getAttentionContext(id);

  Future<Map<String, dynamic>> resolveAttention(
    String id, {
    String? by,
    String? reason,
  }) =>
      attention.resolveAttention(id, by: by, reason: reason);

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

  /// Read-through variant of [listSchedules]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listSchedulesCached({
    String? projectId,
  }) {
    final q = projectId == null ? null : {'project': projectId};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${cfg.teamId}/schedules', q),
      fetch: () => listSchedules(projectId: projectId),
      decode: _decodeListMaps,
    );
  }

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

  /// Read-through variant of [listRuns] that serves the last cached
  /// snapshot if the network is down. Cache is keyed on the full URL
  /// (path + sorted query), so different filters get independent rows.
  Future<CachedResponse<List<Map<String, dynamic>>>> listRunsCached({
    String? projectId,
    String? status,
    int? limit,
  }) {
    final q = <String, String>{};
    if (projectId != null && projectId.isNotEmpty) q['project'] = projectId;
    if (status != null && status.isNotEmpty) {
      q['status'] = _runStatusToServer(status);
    }
    if (limit != null) q['limit'] = '$limit';
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/runs',
        q.isEmpty ? null : q,
      ),
      fetch: () => listRuns(
        projectId: projectId,
        status: status,
        limit: limit,
      ),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getRun(String runId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/runs/$runId');
    return _runRowToUI((out as Map).cast<String, dynamic>());
  }

  /// Read-through variant of [getRun]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getRunCached(
    String runId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/runs/$runId',
        fetch: () => getRun(runId),
        decode: _decodeMap,
      );

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
    await _invalidate('/v1/teams/${cfg.teamId}/runs');
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
    await _invalidate('/v1/teams/${cfg.teamId}/runs');
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

  /// Read-through variant of [getRunMetrics]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> getRunMetricsCached(
    String runId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/runs/$runId/metrics',
        fetch: () => getRunMetrics(runId),
        decode: _decodeListMaps,
      );

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

  /// Read-through variant of [getRunImages]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> getRunImagesCached(
    String runId, {
    String? metric,
  }) {
    final q = metric == null ? null : {'metric': metric};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/runs/$runId/images',
        q,
      ),
      fetch: () => getRunImages(runId, metric: metric),
      decode: _decodeListMaps,
    );
  }

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

  /// Read-through variant of [getProjectSweepSummary]; see [listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>>
      getProjectSweepSummaryCached(String projectId) =>
          readThrough<List<Map<String, dynamic>>>(
            cache: snapshotCache,
            hubKey: _cacheHubKey,
            endpoint:
                '/v1/teams/${cfg.teamId}/projects/$projectId/sweep-summary',
            fetch: () => getProjectSweepSummary(projectId),
            decode: _decodeListMaps,
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

  /// Read-through variant of [getRunHistograms]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> getRunHistogramsCached(
    String runId, {
    String? metric,
  }) {
    final q = metric == null ? null : {'metric': metric};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/runs/$runId/histograms',
        q,
      ),
      fetch: () => getRunHistograms(runId, metric: metric),
      decode: _decodeListMaps,
    );
  }

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

  /// Read-through variant of [listDocuments]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listDocumentsCached({
    String? projectId,
  }) {
    final q = projectId == null ? null : {'project': projectId};
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${cfg.teamId}/documents', q),
      fetch: () => listDocuments(projectId: projectId),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getDocument(String docId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/documents/$docId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getDocument]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getDocumentCached(
    String docId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/documents/$docId',
        fetch: () => getDocument(docId),
        decode: _decodeMap,
      );

  /// Either [contentInline] or [artifactId] must be non-null (server enforces
  /// a XOR CHECK constraint). Set [schemaId] for typed (W5a) documents —
  /// the hub then carries content_inline as a JSON sections blob and the
  /// kind allowlist is bypassed in favor of template-declared kinds.
  Future<Map<String, dynamic>> createDocument({
    required String projectId,
    required String kind, // e.g. 'report', 'design', 'note', 'proposal'
    required String title,
    String? schemaId,
    String? contentInline,
    String? artifactId,
    String? authorAgentId,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'kind': kind,
      'title': title,
    };
    if (schemaId != null && schemaId.isNotEmpty) body['schema_id'] = schemaId;
    if (contentInline != null) body['content_inline'] = contentInline;
    if (artifactId != null) body['artifact_id'] = artifactId;
    if (authorAgentId != null) body['author_agent_id'] = authorAgentId;
    final out = await _post('/v1/teams/${cfg.teamId}/documents', body);
    await _invalidate('/v1/teams/${cfg.teamId}/documents');
    return (out as Map).cast<String, dynamic>();
  }

  /// W5a — Structured Document Viewer (A4). Edits a single section's
  /// body. Pass [expectedLastAuthoredAt] (from the loaded section's
  /// `last_authored_at`) for optimistic concurrency; server returns 412
  /// ([HubApiError] with status=412) if the row's value disagrees,
  /// with a `server_section` payload the UI can use to show diff.
  Future<Map<String, dynamic>> patchDocumentSection({
    required String documentId,
    required String slug,
    required String body,
    String? expectedLastAuthoredAt,
    String? lastAuthoredBySessionId,
  }) async {
    final payload = <String, dynamic>{'body': body};
    if (expectedLastAuthoredAt != null && expectedLastAuthoredAt.isNotEmpty) {
      payload['expected_last_authored_at'] = expectedLastAuthoredAt;
    }
    if (lastAuthoredBySessionId != null &&
        lastAuthoredBySessionId.isNotEmpty) {
      payload['last_authored_by_session_id'] = lastAuthoredBySessionId;
    }
    final out = await _patch(
      '/v1/teams/${cfg.teamId}/documents/$documentId/sections/$slug',
      payload,
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/documents/$documentId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// W5a — POST /sections/{slug}/status. [status] is one of `empty`,
  /// `draft`, `ratified`. Returns the updated section payload.
  Future<Map<String, dynamic>> setDocumentSectionStatus({
    required String documentId,
    required String slug,
    required String status,
  }) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/documents/$documentId/sections/$slug/status',
      {'status': status},
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/documents/$documentId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  // ---- ADR-020 W1: document annotations ------------------------------
  // Director redline / comment / suggestion / question on a typed-doc
  // section. Append-only-on-content (resolve, don't delete; D3).

  /// GET /documents/{doc}/annotations.
  /// [section] filters to one slug; [status] is `open` (default),
  /// `resolved`, or `all`.
  Future<List<Map<String, dynamic>>> listAnnotations({
    required String documentId,
    String? section,
    String? status,
  }) async {
    final q = <String, String>{};
    if (section != null && section.isNotEmpty) q['section'] = section;
    if (status != null && status.isNotEmpty) q['status'] = status;
    final out = await _get(
      '/v1/teams/${cfg.teamId}/documents/$documentId/annotations',
      query: q.isEmpty ? null : q,
    );
    final m = (out as Map).cast<String, dynamic>();
    return (m['annotations'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
  }

  /// Read-through variant of [listAnnotations]; see [listRunsCached]
  /// for the offline-fallback contract. Annotation overlays use this
  /// so a director's notes on a section render even when the hub is
  /// unreachable.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAnnotationsCached({
    required String documentId,
    String? section,
    String? status,
  }) {
    final q = <String, String>{};
    if (section != null && section.isNotEmpty) q['section'] = section;
    if (status != null && status.isNotEmpty) q['status'] = status;
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/documents/$documentId/annotations',
        q.isEmpty ? null : q,
      ),
      fetch: () => listAnnotations(
        documentId: documentId,
        section: section,
        status: status,
      ),
      decode: (raw) {
        // Annotations return as {annotations: [...]} on the wire but the
        // raw fetch already unwraps to a list — match that shape so the
        // cached body and the live body decode identically.
        return _decodeListMaps(raw);
      },
    );
  }

  /// POST /documents/{doc}/annotations. [kind] defaults to `comment`.
  /// [charStart]/[charEnd] are optional in-section offsets.
  Future<Map<String, dynamic>> createAnnotation({
    required String documentId,
    required String sectionSlug,
    required String body,
    String kind = 'comment',
    int? charStart,
    int? charEnd,
  }) async {
    final payload = <String, dynamic>{
      'section_slug': sectionSlug,
      'body': body,
      'kind': kind,
    };
    if (charStart != null) payload['char_start'] = charStart;
    if (charEnd != null) payload['char_end'] = charEnd;
    final out = await _post(
      '/v1/teams/${cfg.teamId}/documents/$documentId/annotations',
      payload,
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/documents/$documentId/annotations',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// PATCH /annotations/{id}. Author-only on the server side; passing
  /// either [body] or [kind] (or both) updates the row.
  Future<Map<String, dynamic>> patchAnnotation({
    required String annotationId,
    String? body,
    String? kind,
  }) async {
    final payload = <String, dynamic>{};
    if (body != null) payload['body'] = body;
    if (kind != null) payload['kind'] = kind;
    final out = await _patch(
      '/v1/teams/${cfg.teamId}/annotations/$annotationId',
      payload,
    );
    final m = (out as Map).cast<String, dynamic>();
    await _invalidateAnnotationsForDoc(m);
    return m;
  }

  /// POST /annotations/{id}/resolve. Soft-close per ADR-020 D3.
  Future<Map<String, dynamic>> resolveAnnotation(String annotationId) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/annotations/$annotationId/resolve',
      const {},
    );
    final m = (out as Map).cast<String, dynamic>();
    await _invalidateAnnotationsForDoc(m);
    return m;
  }

  /// POST /annotations/{id}/reopen.
  Future<Map<String, dynamic>> reopenAnnotation(String annotationId) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/annotations/$annotationId/reopen',
      const {},
    );
    final m = (out as Map).cast<String, dynamic>();
    await _invalidateAnnotationsForDoc(m);
    return m;
  }

  /// PATCH/resolve/reopen all return the updated annotation row whose
  /// `document_id` field is the only handle the mutation methods have on
  /// the cache-key prefix. Pull it out and drop the matching list rows.
  Future<void> _invalidateAnnotationsForDoc(Map<String, dynamic> row) async {
    final docId = (row['document_id'] ?? '').toString();
    if (docId.isEmpty) return;
    await _invalidate(
      '/v1/teams/${cfg.teamId}/documents/$docId/annotations',
    );
  }

  // ---- W5b: deliverables + components + project overview --------------
  // A3 §4 + §5 + §9. Mobile uses these to render the Structured
  // Deliverable Viewer (A5) and to power the phase-summary navigation
  // off the phase ribbon.

  Future<List<Map<String, dynamic>>> listDeliverables({
    required String projectId,
    String? phase,
    String? state,
    bool includeComponents = false,
  }) async {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (state != null && state.isNotEmpty) q['state'] = state;
    if (includeComponents) q['include'] = 'components';
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables',
      query: q.isEmpty ? null : q,
    );
    final m = (out as Map).cast<String, dynamic>();
    final items = (m['items'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
    return items;
  }

  /// Read-through variant of [listDeliverables]; see [listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listDeliverablesCached({
    required String projectId,
    String? phase,
    String? state,
    bool includeComponents = false,
  }) {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (state != null && state.isNotEmpty) q['state'] = state;
    if (includeComponents) q['include'] = 'components';
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables',
        q.isEmpty ? null : q,
      ),
      fetch: () => listDeliverables(
        projectId: projectId,
        phase: phase,
        state: state,
        includeComponents: includeComponents,
      ),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getDeliverable({
    required String projectId,
    required String deliverableId,
  }) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables/$deliverableId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getDeliverable]; see [listRunsCached] for
  /// the offline-fallback contract. The structured deliverable viewer
  /// uses this so directors can re-open a deliverable without network.
  Future<CachedResponse<Map<String, dynamic>>> getDeliverableCached({
    required String projectId,
    required String deliverableId,
  }) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint:
            '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables/$deliverableId',
        fetch: () => getDeliverable(
          projectId: projectId,
          deliverableId: deliverableId,
        ),
        decode: _decodeMap,
      );

  Future<Map<String, dynamic>> ratifyDeliverable({
    required String projectId,
    required String deliverableId,
    String? rationale,
  }) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables/$deliverableId/ratify',
      {if (rationale != null && rationale.isNotEmpty) 'rationale': rationale},
    );
    await _invalidateProjectDeliverable(projectId, deliverableId);
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> unratifyDeliverable({
    required String projectId,
    required String deliverableId,
    String? reason,
  }) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables/$deliverableId/unratify',
      {if (reason != null && reason.isNotEmpty) 'rationale': reason},
    );
    await _invalidateProjectDeliverable(projectId, deliverableId);
    return (out as Map).cast<String, dynamic>();
  }

  /// ADR-020 W2 — POST /deliverables/{id}/send-back. Returns the
  /// updated deliverable wrapped with the new attention_item_id so the
  /// caller can show "Note sent — attention item created" with a
  /// deep-link if it wants. 409 when state is `ratified`; 422 when an
  /// annotation_id doesn't belong to one of this deliverable's docs.
  Future<Map<String, dynamic>> sendBackDeliverable({
    required String projectId,
    required String deliverableId,
    required String note,
    List<String> annotationIds = const [],
  }) async {
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables/$deliverableId/send-back',
      {
        'note': note,
        if (annotationIds.isNotEmpty) 'annotation_ids': annotationIds,
      },
    );
    await _invalidateProjectDeliverable(projectId, deliverableId);
    return (out as Map).cast<String, dynamic>();
  }

  /// Drop every cache row touched by a deliverable mutation: the
  /// deliverable itself, the project's deliverable list (any phase /
  /// state filter), the criteria list (ratification mutates criterion
  /// gates as a cascade), and the project overview snippet.
  Future<void> _invalidateProjectDeliverable(
    String projectId,
    String deliverableId,
  ) async {
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables/$deliverableId',
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables',
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/criteria',
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/overview',
    );
  }

  Future<Map<String, dynamic>> getProjectOverview(String projectId) async {
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/overview',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<CachedResponse<Map<String, dynamic>>> getProjectOverviewCached(
    String projectId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint:
            '/v1/teams/${cfg.teamId}/projects/$projectId/overview',
        fetch: () => getProjectOverview(projectId),
        decode: _decodeMap,
      );

  Future<List<Map<String, dynamic>>> listProjectCriteria({
    required String projectId,
    String? phase,
    String? deliverableId,
  }) async {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (deliverableId != null && deliverableId.isNotEmpty) {
      q['deliverable_id'] = deliverableId;
    }
    final out = await _get(
      '/v1/teams/${cfg.teamId}/projects/$projectId/criteria',
      query: q.isEmpty ? null : q,
    );
    final m = (out as Map).cast<String, dynamic>();
    return (m['items'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
  }

  /// Read-through variant of [listProjectCriteria]; see [listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectCriteriaCached({
    required String projectId,
    String? phase,
    String? deliverableId,
  }) {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (deliverableId != null && deliverableId.isNotEmpty) {
      q['deliverable_id'] = deliverableId;
    }
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/projects/$projectId/criteria',
        q.isEmpty ? null : q,
      ),
      fetch: () => listProjectCriteria(
        projectId: projectId,
        phase: phase,
        deliverableId: deliverableId,
      ),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> createCriterion({
    required String projectId,
    required String phase,
    required String kind,
    Map<String, dynamic>? body,
    String? deliverableId,
    bool? required,
    int? ord,
  }) async {
    final payload = <String, dynamic>{
      'phase': phase,
      'kind': kind,
      if (body != null) 'body': body,
      if (deliverableId != null && deliverableId.isNotEmpty)
        'deliverable_id': deliverableId,
      if (required != null) 'required': required,
      if (ord != null) 'ord': ord,
    };
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/criteria',
      payload,
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/overview',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> markCriterionMet({
    required String projectId,
    required String criterionId,
    String? evidenceRef,
    String? rationale,
  }) =>
      _markCriterion(projectId, criterionId, 'mark-met', evidenceRef, rationale);

  Future<Map<String, dynamic>> markCriterionFailed({
    required String projectId,
    required String criterionId,
    String? reason,
  }) =>
      _markCriterion(projectId, criterionId, 'mark-failed', null, reason);

  Future<Map<String, dynamic>> waiveCriterion({
    required String projectId,
    required String criterionId,
    String? reason,
  }) =>
      _markCriterion(projectId, criterionId, 'waive', null, reason);

  Future<Map<String, dynamic>> _markCriterion(
    String projectId,
    String criterionId,
    String action,
    String? evidenceRef,
    String? note,
  ) async {
    final payload = <String, dynamic>{};
    if (evidenceRef != null && evidenceRef.isNotEmpty) {
      payload['evidence_ref'] = evidenceRef;
    }
    if (note != null && note.isNotEmpty) {
      // mark-met treats it as rationale; mark-failed/waive treat it as
      // reason. The hub accepts both; sending both keeps shapes simple.
      payload['rationale'] = note;
      payload['reason'] = note;
    }
    final out = await _post(
      '/v1/teams/${cfg.teamId}/projects/$projectId/criteria/$criterionId/$action',
      payload,
    );
    // Criterion mutations can cascade into deliverable.ratified gate
    // state, so drop the project's deliverable + criteria + overview
    // caches as a unit.
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/deliverables',
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/criteria',
    );
    await _invalidate(
      '/v1/teams/${cfg.teamId}/projects/$projectId/overview',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<List<Map<String, dynamic>>> listDocumentVersions(String docId) =>
      _listJson('/v1/teams/${cfg.teamId}/documents/$docId/versions');

  /// Read-through variant of [listDocumentVersions]; see [listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listDocumentVersionsCached(
    String docId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/documents/$docId/versions',
        fetch: () => listDocumentVersions(docId),
        decode: _decodeListMaps,
      );

  /// Lists artifacts at team scope. Optional filters: [projectId], [runId],
  /// [kind]. Newest first. See blueprint §6.6 — artifacts are the
  /// content-addressed output surface for runs and standalone uploads.
  Future<List<Map<String, dynamic>>> listArtifacts({
    String? projectId,
    String? runId,
    String? kind,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (runId != null) q['run'] = runId;
    if (kind != null) q['kind'] = kind;
    return _listJson(
      '/v1/teams/${cfg.teamId}/artifacts',
      query: q.isEmpty ? null : q,
    );
  }

  Future<Map<String, dynamic>> getArtifact(String artifactId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/artifacts/$artifactId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [listArtifacts]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listArtifactsCached({
    String? projectId,
    String? runId,
    String? kind,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (runId != null) q['run'] = runId;
    if (kind != null) q['kind'] = kind;
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/artifacts',
        q.isEmpty ? null : q,
      ),
      fetch: () => listArtifacts(
        projectId: projectId,
        runId: runId,
        kind: kind,
      ),
      decode: _decodeListMaps,
    );
  }

  /// Read-through variant of [getArtifact]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getArtifactCached(
    String artifactId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/artifacts/$artifactId',
        fetch: () => getArtifact(artifactId),
        decode: _decodeMap,
      );

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

  /// Read-through variant of [listReviews]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listReviewsCached({
    String? projectId,
    String? status,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) q['state'] = _reviewStateToServer(status);
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/reviews',
        q.isEmpty ? null : q,
      ),
      fetch: () => listReviews(projectId: projectId, status: status),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getReview(String reviewId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/reviews/$reviewId');
    return _reviewRowToUI((out as Map).cast<String, dynamic>());
  }

  /// Read-through variant of [getReview]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getReviewCached(
    String reviewId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/reviews/$reviewId',
        fetch: () => getReview(reviewId),
        decode: _decodeMap,
      );

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
    await _invalidate('/v1/teams/${cfg.teamId}/reviews');
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
    await _invalidate('/v1/teams/${cfg.teamId}/reviews');
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

  /// Read-through variant of [listPlans]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listPlansCached({
    String? projectId,
    String? status,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) q['status'] = status;
    return readThrough<List<Map<String, dynamic>>>(
      cache: snapshotCache,
      hubKey: _cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${cfg.teamId}/plans',
        q.isEmpty ? null : q,
      ),
      fetch: () => listPlans(projectId: projectId, status: status),
      decode: _decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getPlan(String planId) async {
    final out = await _get('/v1/teams/${cfg.teamId}/plans/$planId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getPlan]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getPlanCached(
    String planId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/plans/$planId',
        fetch: () => getPlan(planId),
        decode: _decodeMap,
      );

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

  /// Read-through variant of [listPlanSteps]; see [listRunsCached] for
  /// the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listPlanStepsCached(
    String planId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/plans/$planId/steps',
        fetch: () => listPlanSteps(planId),
        decode: _decodeListMaps,
      );

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

  /// Read-through variant of [listProjectDocs]; see [listRunsCached] for
  /// the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectDocsCached(
    String projectId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: snapshotCache,
        hubKey: _cacheHubKey,
        endpoint: '/v1/teams/${cfg.teamId}/projects/$projectId/docs',
        fetch: () => listProjectDocs(projectId),
        decode: _decodeListMaps,
      );

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

  // ---- blobs (content-addressed) → BlobsApi (W3) ----

  Future<Map<String, dynamic>> uploadBlob(List<int> bytes, {String? mime}) =>
      blobs.uploadBlob(bytes, mime: mime);

  Future<List<int>> downloadBlob(String sha) => blobs.downloadBlob(sha);

  Future<List<int>> downloadBlobCached(String sha) =>
      blobs.downloadBlobCached(sha);

  // ---- search + audit → SearchApi (W3) ----

  Future<List<Map<String, dynamic>>> searchEvents(String q, {int? limit}) =>
      search.searchEvents(q, limit: limit);

  Future<List<Map<String, dynamic>>> listAuditEvents({
    String? action,
    String? since,
    String? projectId,
    int? limit,
  }) =>
      search.listAuditEvents(
        action: action,
        since: since,
        projectId: projectId,
        limit: limit,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>> listAuditEventsCached({
    String? action,
    String? since,
    String? projectId,
    int? limit,
  }) =>
      search.listAuditEventsCached(
        action: action,
        since: since,
        projectId: projectId,
        limit: limit,
      );

  // ---- SSE event stream → EventsApi (W4) ----

  Stream<Map<String, dynamic>> streamEvents(
    String projectId,
    String channelId, {
    String? since,
  }) =>
      events.streamEvents(projectId, channelId, since: since);

  Stream<Map<String, dynamic>> streamTeamEvents(
    String channelId, {
    String? since,
  }) =>
      events.streamTeamEvents(channelId, since: since);
}

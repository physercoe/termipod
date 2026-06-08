import 'dart:convert';

import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Projects, their channels, channel events, principals, and project docs.
///
/// A project is the unit of directed work (blueprint §6.1); channels are
/// its conversation rooms (plus the team-scope `#hub-meta` room), and
/// principals are the human handles tracked as `auth_tokens` rows.
/// Wedge W16 of `docs/plans/hub-client-split.md` — the final cleave of
/// the `HubClient` monolith.
class ProjectsApi {
  final HubTransport _t;
  ProjectsApi(this._t);

  // ---- projects ----

  Future<List<Map<String, dynamic>>> listProjects({bool? isTemplate}) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/projects',
        query: isTemplate == null
            ? null
            : {'is_template': isTemplate ? 'true' : 'false'},
      );

  /// Fetches a single project by id (`/v1/teams/{team}/projects/{id}`).
  /// Used by pull-to-refresh on the project detail screen so the owner
  /// can pick up server-side resolution (overview_widget,
  /// phase_tiles_template, etc.) without re-loading the whole team list.
  Future<Map<String, dynamic>> getProject(String projectId) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [listProjects]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectsCached({
    bool? isTemplate,
  }) {
    final q = isTemplate == null
        ? null
        : {'is_template': isTemplate ? 'true' : 'false'};
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${_t.cfg.teamId}/projects', q),
      fetch: () => listProjects(isTemplate: isTemplate),
      decode: _t.decodeListMaps,
    );
  }

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
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/projects', body);
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects');
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
    final out = await _t.patch(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId',
      body,
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects');
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> archiveProject(String projectId) async {
    await _t.delete('/v1/teams/${_t.cfg.teamId}/projects/$projectId');
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects');
  }

  // ---- project phase (lifecycle) ----

  /// Reads the current phase + template phase order + transition log
  /// for a project (lifecycle W1). Returns a payload of the shape
  /// `{project_id, phase, phases:[...], history:[...]}` — fields are
  /// empty for lifecycle-disabled projects.
  Future<Map<String, dynamic>> getProjectPhase(String projectId) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/phase',
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
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/phase/advance',
      body,
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects');
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
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/phase',
      {'phase': phase},
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects');
    return (out as Map).cast<String, dynamic>();
  }

  /// Starts a project (ADR-046 / WS4): spawns the project's bound domain
  /// steward. A project is created bound to a steward but not started; this
  /// is the explicit "begin work" gesture. Idempotent on the hub — a second
  /// call while the steward is already running returns HTTP 409 (surfaced as
  /// [HubApiError]) with the live agent in the body. Throws 422 when no
  /// steward is bound. The returned map is `{agent_id, spawn_id?, status,
  /// already_running, project_id}`.
  Future<Map<String, dynamic>> startProject(
    String projectId, {
    String? hostId,
  }) async {
    final body = <String, dynamic>{};
    if (hostId != null && hostId.isNotEmpty) body['host_id'] = hostId;
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/start',
      body,
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects');
    return (out as Map).cast<String, dynamic>();
  }

  /// Reads the project steward's live state (lifecycle W3 — A3 §10.1).
  /// Cache-Control on the response is `private, no-cache`, so this
  /// always hits the network rather than the snapshot cache.
  /// Returns the JSON map verbatim — `{scope, agent_id, state,
  /// current_action?, handoff?}`.
  Future<Map<String, dynamic>> getStewardState(String projectId) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/steward/state',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Fetches the raw YAML body of a project template by name (W3 YAML
  /// reveal sheet). The hub serves this as `text/yaml` rather than
  /// JSON — caller receives the file contents as a UTF-8 string.
  /// Routes around the JSON decode path because the body is not JSON.
  Future<String> getProjectTemplateYaml(String name) async {
    final req = await _t.open(
      'GET',
      '/v1/teams/${_t.cfg.teamId}/templates/projects/$name.yaml',
    );
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  // ---- channels ----

  Future<List<Map<String, dynamic>>> listChannels(String projectId) =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels');

  /// Read-through variant of [listChannels]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listChannelsCached(
    String projectId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint:
            '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels',
        fetch: () => listChannels(projectId),
        decode: _t.decodeListMaps,
      );

  /// Team-scope channels (project_id NULL, scope_kind='team'). `#hub-meta`
  /// is auto-seeded by hub init — it's the principal↔steward room.
  Future<List<Map<String, dynamic>>> listTeamChannels() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/channels');

  /// Read-through variant of [listTeamChannels]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listTeamChannelsCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/channels',
        fetch: listTeamChannels,
        decode: _t.decodeListMaps,
      );

  Future<Map<String, dynamic>> createTeamChannel(String name) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/channels',
      {'name': name},
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/channels');
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> createChannel(
    String projectId,
    String name,
  ) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels',
      {'name': name},
    );
    await _t.invalidate(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels');
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
    final out = await _t.post(path, body);
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
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels/$channelId/events',
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
        '/v1/teams/${_t.cfg.teamId}/channels/$channelId/events',
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
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/channels/$channelId/events',
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
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels/$channelId/events',
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
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/channels/$channelId/events',
        q,
      ),
      fetch: () => listTeamChannelEvents(channelId, limit: limit),
      decode: _t.decodeListMaps,
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
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/channels/$channelId/events',
        q,
      ),
      fetch: () =>
          listProjectChannelEvents(projectId, channelId, limit: limit),
      decode: _t.decodeListMaps,
    );
  }

  // ---- principals ----

  /// Humans don't have a dedicated table; they're tracked as `auth_tokens`
  /// rows with `scope.role='principal'`. This endpoint coalesces by
  /// `scope.handle`, returning one row per unique handle plus a bucket for
  /// unnamed tokens.
  Future<List<Map<String, dynamic>>> listPrincipals() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/principals');

  // ---- project docs ----

  /// Flat list of doc entries under the project's docs_root. Each entry
  /// has `path` (relative), `size`, `mod_time`, and optional `is_dir`.
  /// Returns an empty list if the project has no docs_root configured.
  Future<List<Map<String, dynamic>>> listProjectDocs(String projectId) =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/projects/$projectId/docs');

  /// Read-through variant of [listProjectDocs]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectDocsCached(
    String projectId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/projects/$projectId/docs',
        fetch: () => listProjectDocs(projectId),
        decode: _t.decodeListMaps,
      );

  /// Reads a single doc as a UTF-8 string. The hub serves any file type;
  /// caller decides how to render based on extension.
  Future<String> getProjectDoc(String projectId, String relPath) async {
    final req = await _t.open(
      'GET',
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/docs/$relPath',
    );
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }
}

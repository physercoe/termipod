import 'dart:convert';
import 'dart:io';

import 'blob_bytes_cache.dart';
import 'hub_read_through.dart';
import 'admin_api.dart';
import 'agents_api.dart';
import 'attention_api.dart';
import 'blobs_api.dart';
import 'deliverables_api.dart';
import 'documents_api.dart';
import 'events_api.dart';
import 'hosts_api.dart';
import 'hub_snapshot_cache.dart';
import 'hub_transport.dart';
import 'plans_api.dart';
import 'runs_api.dart';
import 'search_api.dart';
import 'sessions_api.dart';
import 'system_api.dart';
import 'tasks_api.dart';
import 'templates_api.dart';

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

  /// Host registry: list (live + cached), delete, SSH-hint + capabilities.
  late final HostsApi hosts = HostsApi(_t);

  /// Sessions: list (live + cached), open/rename/archive/resume/fork/
  /// delete, per-session cost, transcript search.
  late final SessionsApi sessions = SessionsApi(_t);

  /// Agents: list/get (live + cached), spawns, spawn, steward ensure,
  /// lifecycle (terminate/rename/archive/pause/resume), pane, journal,
  /// and the per-agent event queue (post/list/cache).
  late final AgentsApi agents = AgentsApi(_t);

  /// Runs + schedules: list/get (live + cached), create/complete, the
  /// metric/image/histogram/sweep digests, and schedule CRUD + fire.
  late final RunsApi runs = RunsApi(_t);

  /// Documents + their director annotations: list/get/create, typed-
  /// section edit + status, and the annotation overlay.
  late final DocumentsApi documents = DocumentsApi(_t);

  /// Project work-products: deliverables (+ratify/send-back), criteria,
  /// project overview, document versions, artifacts, and reviews.
  late final DeliverablesApi deliverables = DeliverablesApi(_t);

  /// Plans + plan steps: list/get (live + cached), create/update, steps.
  late final PlansApi plans = PlansApi(_t);

  /// Behavior-as-data config: project/agent templates + agent families.
  late final TemplatesApi templates = TemplatesApi(_t);

  /// Tasks (ADR-029): list/get (live + cached), create, patch.
  late final TasksApi tasks = TasksApi(_t);

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


  Future<void> _invalidate(String prefix) => _t.invalidate(prefix);

  Future<HttpClientRequest> _open(
    String method,
    String path, {
    Map<String, String>? query,
    bool auth = true,
  }) =>
      _t.open(method, path, query: query, auth: auth);


  Future<dynamic> _get(String path,
          {Map<String, String>? query, bool auth = true}) =>
      _t.get(path, query: query, auth: auth);

  Future<dynamic> _post(String path, Object body,
          {Map<String, String>? query}) =>
      _t.post(path, body, query: query);

  Future<dynamic> _patch(String path, Object body) => _t.patch(path, body);


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

  Future<List<Map<String, dynamic>>> listHosts() => hosts.listHosts();

  Future<CachedResponse<List<Map<String, dynamic>>>> listHostsCached() =>
      hosts.listHostsCached();

  Future<List<Map<String, dynamic>>> listAgents({
    bool includeArchived = false,
    bool includeTerminated = false,
    String? projectId,
  }) =>
      agents.listAgents(
        includeArchived: includeArchived,
        includeTerminated: includeTerminated,
        projectId: projectId,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>> listAgentsCached({
    bool includeArchived = false,
    bool includeTerminated = false,
    String? projectId,
  }) =>
      agents.listAgentsCached(
        includeArchived: includeArchived,
        includeTerminated: includeTerminated,
        projectId: projectId,
      );

  Future<Map<String, dynamic>> getAgent(String agentId) =>
      agents.getAgent(agentId);

  Future<CachedResponse<Map<String, dynamic>>> getAgentCached(String agentId) =>
      agents.getAgentCached(agentId);

  Future<List<Map<String, dynamic>>> listSpawns() => agents.listSpawns();

  Future<CachedResponse<List<Map<String, dynamic>>>> listSpawnsCached() =>
      agents.listSpawnsCached();

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
      sessions.listSessions(status: status);

  Future<CachedResponse<List<Map<String, dynamic>>>> listSessionsCached({
    String? status,
  }) =>
      sessions.listSessionsCached(status: status);

  Future<Map<String, dynamic>> getSession(String id) => sessions.getSession(id);

  Future<Map<String, dynamic>?> getSessionCost(String id) =>
      sessions.getSessionCost(id);

  Future<Map<String, dynamic>> openSession({
    String? title,
    String? scopeKind,
    String? scopeId,
    String? agentId,
    String? worktreePath,
    String? spawnSpecYaml,
  }) =>
      sessions.openSession(
        title: title,
        scopeKind: scopeKind,
        scopeId: scopeId,
        agentId: agentId,
        worktreePath: worktreePath,
        spawnSpecYaml: spawnSpecYaml,
      );

  Future<void> renameSession(String id, String title) =>
      sessions.renameSession(id, title);

  Future<void> archiveSession(String id) => sessions.archiveSession(id);

  Future<Map<String, dynamic>> resumeSession(String id) =>
      sessions.resumeSession(id);

  Future<List<Map<String, dynamic>>> searchSessions(String query, {int? limit}) =>
      sessions.searchSessions(query, limit: limit);

  Future<Map<String, dynamic>> forkSession(
    String id, {
    String? agentId,
    String? title,
  }) =>
      sessions.forkSession(id, agentId: agentId, title: title);

  Future<void> deleteSession(String id) => sessions.deleteSession(id);

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

  // ---- tasks → TasksApi (W15) ----

  Future<List<Map<String, dynamic>>> listTasks(
    String projectId, {
    String? status,
    String? priority,
    String? sort,
  }) =>
      tasks.listTasks(projectId,
          status: status, priority: priority, sort: sort);

  Future<Map<String, dynamic>> getTask(String projectId, String taskId) =>
      tasks.getTask(projectId, taskId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listTasksCached(
    String projectId, {
    String? status,
    String? priority,
    String? sort,
  }) =>
      tasks.listTasksCached(projectId,
          status: status, priority: priority, sort: sort);

  Future<CachedResponse<Map<String, dynamic>>> getTaskCached(
    String projectId,
    String taskId,
  ) =>
      tasks.getTaskCached(projectId, taskId);

  Future<Map<String, dynamic>> patchTask(
    String projectId,
    String taskId, {
    String? status,
    String? title,
    String? bodyMd,
    String? priority,
  }) =>
      tasks.patchTask(
        projectId,
        taskId,
        status: status,
        title: title,
        bodyMd: bodyMd,
        priority: priority,
      );

  // ---- templates + agent families → TemplatesApi (W14) ----

  Future<List<Map<String, dynamic>>> listTemplates() =>
      templates.listTemplates();

  Future<CachedResponse<List<Map<String, dynamic>>>> listTemplatesCached() =>
      templates.listTemplatesCached();

  Future<String> getTemplate(String category, String name,
          {bool merged = false}) =>
      templates.getTemplate(category, name, merged: merged);

  Future<Map<String, dynamic>> putTemplate(
          String category, String name, String body) =>
      templates.putTemplate(category, name, body);

  Future<Map<String, dynamic>> resetBundledTemplates() =>
      templates.resetBundledTemplates();

  Future<void> deleteTemplate(String category, String name) =>
      templates.deleteTemplate(category, name);

  Future<List<Map<String, dynamic>>> listAgentFamilies() =>
      templates.listAgentFamilies();

  Future<CachedResponse<List<Map<String, dynamic>>>>
      listAgentFamiliesCached() => templates.listAgentFamiliesCached();

  Future<Map<String, dynamic>> getAgentFamily(String family) =>
      templates.getAgentFamily(family);

  Future<Map<String, dynamic>> putAgentFamily(String family, String yamlBody) =>
      templates.putAgentFamily(family, yamlBody);

  Future<Map<String, dynamic>> resetAgentFamilies() =>
      templates.resetAgentFamilies();

  Future<void> deleteAgentFamily(String family) =>
      templates.deleteAgentFamily(family);

  Future<void> renameTemplate(String category, String name, String newName) =>
      templates.renameTemplate(category, name, newName);

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
  /// Routes around the JSON decode path because the body is not JSON.
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
  }) =>
      tasks.createTask(
        projectId,
        title: title,
        bodyMd: bodyMd,
        assigneeId: assigneeId,
        parentTaskId: parentTaskId,
        status: status,
        priority: priority,
      );

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

  // ---- spawn + steward → AgentsApi (W9) ----

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
  }) =>
      agents.spawnAgent(
        childHandle: childHandle,
        kind: kind,
        spawnSpecYaml: spawnSpecYaml,
        hostId: hostId,
        parentAgentId: parentAgentId,
        personaSeed: personaSeed,
        permissionMode: permissionMode,
        sessionId: sessionId,
        autoOpenSession: autoOpenSession,
      );

  Future<Map<String, dynamic>> ensureGeneralSteward({String? hostId}) =>
      agents.ensureGeneralSteward(hostId: hostId);

  Future<Map<String, dynamic>> ensureProjectSteward({
    required String projectId,
    String? hostId,
    String? permissionMode,
  }) =>
      agents.ensureProjectSteward(
        projectId: projectId,
        hostId: hostId,
        permissionMode: permissionMode,
      );

  // ---- agent lifecycle → AgentsApi (W9) ----

  Future<void> terminateAgent(String agentId) =>
      agents.terminateAgent(agentId);

  Future<void> renameAgent(String agentId, String newHandle) =>
      agents.renameAgent(agentId, newHandle);

  Future<void> archiveAgent(String agentId) => agents.archiveAgent(agentId);

  Future<Map<String, dynamic>> pauseAgent(String agentId) =>
      agents.pauseAgent(agentId);

  Future<Map<String, dynamic>> resumeAgent(String agentId) =>
      agents.resumeAgent(agentId);

  Future<Map<String, dynamic>> getAgentPane(String agentId,
          {bool refresh = false}) =>
      agents.getAgentPane(agentId, refresh: refresh);

  Future<String> readAgentJournal(String agentId) =>
      agents.readAgentJournal(agentId);

  Future<void> appendAgentJournal(String agentId, String entry,
          {String? header}) =>
      agents.appendAgentJournal(agentId, entry, header: header);

  // ---- agent events (P1.7 AG-UI broker) → AgentsApi (W9) ----

  Future<Map<String, dynamic>> postAgentEvent(
    String agentId, {
    required String kind,
    String? producer,
    Map<String, dynamic>? payload,
  }) =>
      agents.postAgentEvent(
        agentId,
        kind: kind,
        producer: producer,
        payload: payload,
      );

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
    String? modeId,
    String? modelId,
    List<Map<String, String>>? images,
    List<Map<String, String>>? pdfs,
    List<Map<String, String>>? audios,
    List<Map<String, String>>? videos,
    bool? raw,
  }) =>
      agents.postAgentInput(
        agentId,
        kind: kind,
        body: body,
        decision: decision,
        requestId: requestId,
        optionId: optionId,
        note: note,
        reason: reason,
        documentId: documentId,
        modeId: modeId,
        modelId: modelId,
        images: images,
        pdfs: pdfs,
        audios: audios,
        videos: videos,
        raw: raw,
      );

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
  }) =>
      agents.listAgentEvents(
        agentId,
        since: since,
        before: before,
        beforeTs: beforeTs,
        tail: tail,
        limit: limit,
        sessionId: sessionId,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>> listAgentEventsCached(
    String agentId, {
    int? since,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) =>
      agents.listAgentEventsCached(
        agentId,
        since: since,
        tail: tail,
        limit: limit,
        sessionId: sessionId,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>?> listAgentEventsCacheOnly(
    String agentId, {
    int? since,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) =>
      agents.listAgentEventsCacheOnly(
        agentId,
        since: since,
        tail: tail,
        limit: limit,
        sessionId: sessionId,
      );

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

  // ---- host lifecycle → HostsApi (W7) ----

  Future<void> deleteHost(String hostId) => hosts.deleteHost(hostId);

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

  // ---- schedules + runs → RunsApi (W10) ----

  Future<List<Map<String, dynamic>>> listSchedules({String? projectId}) =>
      runs.listSchedules(projectId: projectId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listSchedulesCached({
    String? projectId,
  }) =>
      runs.listSchedulesCached(projectId: projectId);

  Future<Map<String, dynamic>> createSchedule({
    required String projectId,
    required String templateId,
    required String triggerKind,
    String? cronExpr,
    Map<String, dynamic>? parameters,
    bool? enabled,
  }) =>
      runs.createSchedule(
        projectId: projectId,
        templateId: templateId,
        triggerKind: triggerKind,
        cronExpr: cronExpr,
        parameters: parameters,
        enabled: enabled,
      );

  Future<void> patchSchedule(
    String id, {
    bool? enabled,
    String? cronExpr,
    Map<String, dynamic>? parameters,
  }) =>
      runs.patchSchedule(
        id,
        enabled: enabled,
        cronExpr: cronExpr,
        parameters: parameters,
      );

  Future<void> deleteSchedule(String id) => runs.deleteSchedule(id);

  Future<String> runSchedule(String id) => runs.runSchedule(id);

  Future<List<Map<String, dynamic>>> listRuns({
    String? projectId,
    String? status,
    int? limit,
  }) =>
      runs.listRuns(projectId: projectId, status: status, limit: limit);

  Future<CachedResponse<List<Map<String, dynamic>>>> listRunsCached({
    String? projectId,
    String? status,
    int? limit,
  }) =>
      runs.listRunsCached(projectId: projectId, status: status, limit: limit);

  Future<Map<String, dynamic>> getRun(String runId) => runs.getRun(runId);

  Future<CachedResponse<Map<String, dynamic>>> getRunCached(String runId) =>
      runs.getRunCached(runId);

  Future<Map<String, dynamic>> createRun({
    required String projectId,
    required String kind,
    String? agentId,
    String? parentRunId,
    String? name,
    Map<String, dynamic>? metadata,
  }) =>
      runs.createRun(
        projectId: projectId,
        kind: kind,
        agentId: agentId,
        parentRunId: parentRunId,
        name: name,
        metadata: metadata,
      );

  Future<void> completeRun(String runId,
          {required String status, String? summary}) =>
      runs.completeRun(runId, status: status, summary: summary);

  Future<void> attachRunMetricURI(String runId,
          {required String kind, required String uri}) =>
      runs.attachRunMetricURI(runId, kind: kind, uri: uri);

  Future<List<Map<String, dynamic>>> getRunMetrics(String runId) =>
      runs.getRunMetrics(runId);

  Future<CachedResponse<List<Map<String, dynamic>>>> getRunMetricsCached(
          String runId) =>
      runs.getRunMetricsCached(runId);

  Future<List<Map<String, dynamic>>> getRunImages(String runId,
          {String? metric}) =>
      runs.getRunImages(runId, metric: metric);

  Future<CachedResponse<List<Map<String, dynamic>>>> getRunImagesCached(
          String runId, {String? metric}) =>
      runs.getRunImagesCached(runId, metric: metric);

  Future<List<Map<String, dynamic>>> getProjectSweepSummary(String projectId) =>
      runs.getProjectSweepSummary(projectId);

  Future<CachedResponse<List<Map<String, dynamic>>>>
      getProjectSweepSummaryCached(String projectId) =>
          runs.getProjectSweepSummaryCached(projectId);

  Future<List<Map<String, dynamic>>> getRunHistograms(String runId,
          {String? metric}) =>
      runs.getRunHistograms(runId, metric: metric);

  Future<CachedResponse<List<Map<String, dynamic>>>> getRunHistogramsCached(
          String runId, {String? metric}) =>
      runs.getRunHistogramsCached(runId, metric: metric);

  Future<void> putRunHistograms(
          String runId, List<Map<String, dynamic>> histograms) =>
      runs.putRunHistograms(runId, histograms);

  // ---- documents + annotations → DocumentsApi (W11) ----

  Future<List<Map<String, dynamic>>> listDocuments({String? projectId}) =>
      documents.listDocuments(projectId: projectId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listDocumentsCached({
    String? projectId,
  }) =>
      documents.listDocumentsCached(projectId: projectId);

  Future<Map<String, dynamic>> getDocument(String docId) =>
      documents.getDocument(docId);

  Future<CachedResponse<Map<String, dynamic>>> getDocumentCached(
          String docId) =>
      documents.getDocumentCached(docId);

  Future<Map<String, dynamic>> createDocument({
    required String projectId,
    required String kind,
    required String title,
    String? schemaId,
    String? contentInline,
    String? artifactId,
    String? authorAgentId,
  }) =>
      documents.createDocument(
        projectId: projectId,
        kind: kind,
        title: title,
        schemaId: schemaId,
        contentInline: contentInline,
        artifactId: artifactId,
        authorAgentId: authorAgentId,
      );

  Future<Map<String, dynamic>> patchDocumentSection({
    required String documentId,
    required String slug,
    required String body,
    String? expectedLastAuthoredAt,
    String? lastAuthoredBySessionId,
  }) =>
      documents.patchDocumentSection(
        documentId: documentId,
        slug: slug,
        body: body,
        expectedLastAuthoredAt: expectedLastAuthoredAt,
        lastAuthoredBySessionId: lastAuthoredBySessionId,
      );

  Future<Map<String, dynamic>> setDocumentSectionStatus({
    required String documentId,
    required String slug,
    required String status,
  }) =>
      documents.setDocumentSectionStatus(
        documentId: documentId,
        slug: slug,
        status: status,
      );

  Future<List<Map<String, dynamic>>> listAnnotations({
    required String documentId,
    String? section,
    String? status,
  }) =>
      documents.listAnnotations(
        documentId: documentId,
        section: section,
        status: status,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>> listAnnotationsCached({
    required String documentId,
    String? section,
    String? status,
  }) =>
      documents.listAnnotationsCached(
        documentId: documentId,
        section: section,
        status: status,
      );

  Future<Map<String, dynamic>> createAnnotation({
    required String documentId,
    required String sectionSlug,
    required String body,
    String kind = 'comment',
    int? charStart,
    int? charEnd,
  }) =>
      documents.createAnnotation(
        documentId: documentId,
        sectionSlug: sectionSlug,
        body: body,
        kind: kind,
        charStart: charStart,
        charEnd: charEnd,
      );

  Future<Map<String, dynamic>> patchAnnotation({
    required String annotationId,
    String? body,
    String? kind,
  }) =>
      documents.patchAnnotation(
        annotationId: annotationId,
        body: body,
        kind: kind,
      );

  Future<Map<String, dynamic>> resolveAnnotation(String annotationId) =>
      documents.resolveAnnotation(annotationId);

  Future<Map<String, dynamic>> reopenAnnotation(String annotationId) =>
      documents.reopenAnnotation(annotationId);

  // ---- deliverables/criteria/overview/versions/artifacts/reviews → DeliverablesApi (W12) ----

  Future<List<Map<String, dynamic>>> listDeliverables({
    required String projectId,
    String? phase,
    String? state,
    bool includeComponents = false,
  }) =>
      deliverables.listDeliverables(
        projectId: projectId,
        phase: phase,
        state: state,
        includeComponents: includeComponents,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>> listDeliverablesCached({
    required String projectId,
    String? phase,
    String? state,
    bool includeComponents = false,
  }) =>
      deliverables.listDeliverablesCached(
        projectId: projectId,
        phase: phase,
        state: state,
        includeComponents: includeComponents,
      );

  Future<Map<String, dynamic>> getDeliverable({
    required String projectId,
    required String deliverableId,
  }) =>
      deliverables.getDeliverable(
          projectId: projectId, deliverableId: deliverableId);

  Future<CachedResponse<Map<String, dynamic>>> getDeliverableCached({
    required String projectId,
    required String deliverableId,
  }) =>
      deliverables.getDeliverableCached(
          projectId: projectId, deliverableId: deliverableId);

  Future<Map<String, dynamic>> ratifyDeliverable({
    required String projectId,
    required String deliverableId,
    String? rationale,
  }) =>
      deliverables.ratifyDeliverable(
        projectId: projectId,
        deliverableId: deliverableId,
        rationale: rationale,
      );

  Future<Map<String, dynamic>> unratifyDeliverable({
    required String projectId,
    required String deliverableId,
    String? reason,
  }) =>
      deliverables.unratifyDeliverable(
        projectId: projectId,
        deliverableId: deliverableId,
        reason: reason,
      );

  Future<Map<String, dynamic>> sendBackDeliverable({
    required String projectId,
    required String deliverableId,
    required String note,
    List<String> annotationIds = const [],
  }) =>
      deliverables.sendBackDeliverable(
        projectId: projectId,
        deliverableId: deliverableId,
        note: note,
        annotationIds: annotationIds,
      );

  Future<Map<String, dynamic>> getProjectOverview(String projectId) =>
      deliverables.getProjectOverview(projectId);

  Future<CachedResponse<Map<String, dynamic>>> getProjectOverviewCached(
          String projectId) =>
      deliverables.getProjectOverviewCached(projectId);

  Future<List<Map<String, dynamic>>> listProjectCriteria({
    required String projectId,
    String? phase,
    String? deliverableId,
  }) =>
      deliverables.listProjectCriteria(
        projectId: projectId,
        phase: phase,
        deliverableId: deliverableId,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectCriteriaCached({
    required String projectId,
    String? phase,
    String? deliverableId,
  }) =>
      deliverables.listProjectCriteriaCached(
        projectId: projectId,
        phase: phase,
        deliverableId: deliverableId,
      );

  Future<Map<String, dynamic>> createCriterion({
    required String projectId,
    required String phase,
    required String kind,
    Map<String, dynamic>? body,
    String? deliverableId,
    bool? required,
    int? ord,
  }) =>
      deliverables.createCriterion(
        projectId: projectId,
        phase: phase,
        kind: kind,
        body: body,
        deliverableId: deliverableId,
        required: required,
        ord: ord,
      );

  Future<Map<String, dynamic>> markCriterionMet({
    required String projectId,
    required String criterionId,
    String? evidenceRef,
    String? rationale,
  }) =>
      deliverables.markCriterionMet(
        projectId: projectId,
        criterionId: criterionId,
        evidenceRef: evidenceRef,
        rationale: rationale,
      );

  Future<Map<String, dynamic>> markCriterionFailed({
    required String projectId,
    required String criterionId,
    String? reason,
  }) =>
      deliverables.markCriterionFailed(
        projectId: projectId,
        criterionId: criterionId,
        reason: reason,
      );

  Future<Map<String, dynamic>> waiveCriterion({
    required String projectId,
    required String criterionId,
    String? reason,
  }) =>
      deliverables.waiveCriterion(
        projectId: projectId,
        criterionId: criterionId,
        reason: reason,
      );

  Future<List<Map<String, dynamic>>> listDocumentVersions(String docId) =>
      deliverables.listDocumentVersions(docId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listDocumentVersionsCached(
          String docId) =>
      deliverables.listDocumentVersionsCached(docId);

  Future<List<Map<String, dynamic>>> listArtifacts({
    String? projectId,
    String? runId,
    String? kind,
  }) =>
      deliverables.listArtifacts(
          projectId: projectId, runId: runId, kind: kind);

  Future<Map<String, dynamic>> getArtifact(String artifactId) =>
      deliverables.getArtifact(artifactId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listArtifactsCached({
    String? projectId,
    String? runId,
    String? kind,
  }) =>
      deliverables.listArtifactsCached(
          projectId: projectId, runId: runId, kind: kind);

  Future<CachedResponse<Map<String, dynamic>>> getArtifactCached(
          String artifactId) =>
      deliverables.getArtifactCached(artifactId);

  Future<List<Map<String, dynamic>>> listReviews({
    String? projectId,
    String? status,
  }) =>
      deliverables.listReviews(projectId: projectId, status: status);

  Future<CachedResponse<List<Map<String, dynamic>>>> listReviewsCached({
    String? projectId,
    String? status,
  }) =>
      deliverables.listReviewsCached(projectId: projectId, status: status);

  Future<Map<String, dynamic>> getReview(String reviewId) =>
      deliverables.getReview(reviewId);

  Future<CachedResponse<Map<String, dynamic>>> getReviewCached(
          String reviewId) =>
      deliverables.getReviewCached(reviewId);

  Future<Map<String, dynamic>> createReview({
    required String projectId,
    required String targetKind,
    required String targetId,
    String? note,
  }) =>
      deliverables.createReview(
        projectId: projectId,
        targetKind: targetKind,
        targetId: targetId,
        note: note,
      );

  Future<void> decideReview(
    String reviewId, {
    required String decision,
    String? note,
  }) =>
      deliverables.decideReview(reviewId, decision: decision, note: note);

  // ---- plans + plan_steps → PlansApi (W13) ----

  Future<List<Map<String, dynamic>>> listPlans({
    String? projectId,
    String? status,
  }) =>
      plans.listPlans(projectId: projectId, status: status);

  Future<CachedResponse<List<Map<String, dynamic>>>> listPlansCached({
    String? projectId,
    String? status,
  }) =>
      plans.listPlansCached(projectId: projectId, status: status);

  Future<Map<String, dynamic>> getPlan(String planId) => plans.getPlan(planId);

  Future<CachedResponse<Map<String, dynamic>>> getPlanCached(String planId) =>
      plans.getPlanCached(planId);

  Future<Map<String, dynamic>> createPlan({
    required String projectId,
    String? templateId,
    int? version,
    Map<String, dynamic>? spec,
  }) =>
      plans.createPlan(
        projectId: projectId,
        templateId: templateId,
        version: version,
        spec: spec,
      );

  Future<void> updatePlan(
    String planId, {
    String? status,
    Map<String, dynamic>? spec,
  }) =>
      plans.updatePlan(planId, status: status, spec: spec);

  Future<List<Map<String, dynamic>>> listPlanSteps(String planId) =>
      plans.listPlanSteps(planId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listPlanStepsCached(
          String planId) =>
      plans.listPlanStepsCached(planId);

  Future<Map<String, dynamic>> createPlanStep(
    String planId, {
    required int phaseIdx,
    required int stepIdx,
    required String kind,
    Map<String, dynamic>? spec,
  }) =>
      plans.createPlanStep(
        planId,
        phaseIdx: phaseIdx,
        stepIdx: stepIdx,
        kind: kind,
        spec: spec,
      );

  Future<void> updatePlanStep(
    String planId,
    String stepId, {
    String? status,
    String? agentId,
    Map<String, dynamic>? inputRefs,
    Map<String, dynamic>? outputRefs,
  }) =>
      plans.updatePlanStep(
        planId,
        stepId,
        status: status,
        agentId: agentId,
        inputRefs: inputRefs,
        outputRefs: outputRefs,
      );

  // ---- host mutations → HostsApi (W7) ----

  Future<void> updateHostSSHHint(String hostId, Map<String, dynamic> hint) =>
      hosts.updateHostSSHHint(hostId, hint);

  Future<void> updateHostCapabilities(
    String hostId,
    Map<String, dynamic> capabilities,
  ) =>
      hosts.updateHostCapabilities(hostId, capabilities);

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

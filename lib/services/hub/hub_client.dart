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
import 'projects_api.dart';
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

  /// Shared HTTP + cache transport. Held privately and injected into each
  /// per-domain sub-client (see docs/plans/hub-client-split.md). With W16
  /// every method body now lives in a sub-client; `HubClient` is a pure
  /// facade of delegators.
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

  /// Projects + their channels, channel events, principals, project docs.
  late final ProjectsApi projects = ProjectsApi(_t);

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

  // The per-domain method bodies have all moved to their *Api sub-clients
  // (docs/plans/hub-client-split.md); the legacy private transport shims
  // (`_get`/`_post`/…) they used are gone now that W16 (ProjectsApi)
  // removed their last callers. Reach the transport via `_t.<verb>`.

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
      projects.listProjects(isTemplate: isTemplate);

  Future<Map<String, dynamic>> getProject(String projectId) =>
      projects.getProject(projectId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectsCached({
    bool? isTemplate,
  }) =>
      projects.listProjectsCached(isTemplate: isTemplate);

  Future<List<Map<String, dynamic>>> listChannels(String projectId) =>
      projects.listChannels(projectId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listChannelsCached(
    String projectId,
  ) =>
      projects.listChannelsCached(projectId);

  Future<List<Map<String, dynamic>>> listTeamChannels() =>
      projects.listTeamChannels();

  Future<CachedResponse<List<Map<String, dynamic>>>> listTeamChannelsCached() =>
      projects.listTeamChannelsCached();

  Future<Map<String, dynamic>> createTeamChannel(String name) =>
      projects.createTeamChannel(name);

  Future<List<Map<String, dynamic>>> listPrincipals() =>
      projects.listPrincipals();

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

  // ---- project / task / channel writes ----

  Future<Map<String, dynamic>> createProject({
    required String name,
    String? docsRoot,
    String? configYaml,
    String? goal,
    String? kind,
    String? parentProjectId,
    String? templateId,
    Map<String, dynamic>? parameters,
    bool? isTemplate,
    int? budgetCents,
    String? stewardAgentId,
    String? onCreateTemplateId,
    Map<String, dynamic>? policyOverrides,
  }) =>
      projects.createProject(
        name: name,
        docsRoot: docsRoot,
        configYaml: configYaml,
        goal: goal,
        kind: kind,
        parentProjectId: parentProjectId,
        templateId: templateId,
        parameters: parameters,
        isTemplate: isTemplate,
        budgetCents: budgetCents,
        stewardAgentId: stewardAgentId,
        onCreateTemplateId: onCreateTemplateId,
        policyOverrides: policyOverrides,
      );

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
    Map<String, List<String>>? phaseTileOverrides,
    Map<String, String>? overviewWidgetOverrides,
    int? loopInactivityMinutes,
    int? loopAbsoluteCapMinutes,
  }) =>
      projects.updateProject(
        projectId,
        name: name,
        goal: goal,
        kind: kind,
        templateId: templateId,
        parameters: parameters,
        budgetCents: budgetCents,
        stewardAgentId: stewardAgentId,
        onCreateTemplateId: onCreateTemplateId,
        policyOverrides: policyOverrides,
        docsRoot: docsRoot,
        phaseTileOverrides: phaseTileOverrides,
        overviewWidgetOverrides: overviewWidgetOverrides,
        loopInactivityMinutes: loopInactivityMinutes,
        loopAbsoluteCapMinutes: loopAbsoluteCapMinutes,
      );

  Future<void> archiveProject(String projectId) =>
      projects.archiveProject(projectId);

  Future<Map<String, dynamic>> getProjectPhase(String projectId) =>
      projects.getProjectPhase(projectId);

  Future<Map<String, dynamic>> advanceProjectPhase(
    String projectId, {
    String? toPhase,
    String? reason,
  }) =>
      projects.advanceProjectPhase(projectId, toPhase: toPhase, reason: reason);

  Future<Map<String, dynamic>> setProjectPhase(
    String projectId,
    String phase,
  ) =>
      projects.setProjectPhase(projectId, phase);

  Future<Map<String, dynamic>> getStewardState(String projectId) =>
      projects.getStewardState(projectId);

  Future<String> getProjectTemplateYaml(String name) =>
      projects.getProjectTemplateYaml(name);

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
  ) =>
      projects.createChannel(projectId, name);

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
      projects.postProjectChannelEvent(
        projectId,
        channelId,
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
      projects.postTeamChannelEvent(
        channelId,
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
  }) =>
      projects.listTeamChannelEvents(channelId, since: since, limit: limit);

  Future<List<Map<String, dynamic>>> listProjectChannelEvents(
    String projectId,
    String channelId, {
    String? since,
    int? limit,
  }) =>
      projects.listProjectChannelEvents(
        projectId,
        channelId,
        since: since,
        limit: limit,
      );

  Future<CachedResponse<List<Map<String, dynamic>>>>
      listTeamChannelEventsCached(
    String channelId, {
    int? limit,
  }) =>
          projects.listTeamChannelEventsCached(channelId, limit: limit);

  Future<CachedResponse<List<Map<String, dynamic>>>>
      listProjectChannelEventsCached(
    String projectId,
    String channelId, {
    int? limit,
  }) =>
          projects.listProjectChannelEventsCached(
            projectId,
            channelId,
            limit: limit,
          );

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

  Future<void> stopAgent(String agentId) => agents.stopAgent(agentId);

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

  Future<List<Map<String, dynamic>>> listProjectDocs(String projectId) =>
      projects.listProjectDocs(projectId);

  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectDocsCached(
    String projectId,
  ) =>
      projects.listProjectDocsCached(projectId);

  Future<String> getProjectDoc(String projectId, String relPath) =>
      projects.getProjectDoc(projectId, relPath);

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

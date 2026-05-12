import 'package:flutter/material.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../screens/insights/insights_screen.dart';
import '../../screens/projects/artifacts_screen.dart' show ArtifactsScreen;
import '../../screens/projects/documents_screen.dart'
    show DocumentDetailScreen, DocumentsScreen;
import '../../screens/projects/plan_viewer_screen.dart'
    show PlanViewerScreen;
import '../../screens/projects/plans_screen.dart' show PlansScreen;
import '../../screens/projects/project_detail_screen.dart';
import '../../screens/projects/projects_screen.dart'
    show openAgentDetail, openHostDetail;
import '../../screens/projects/runs_screen.dart'
    show RunsScreen, RunDetailScreen;
import '../../screens/projects/task_detail_screen.dart'
    show TaskDetailScreen;
import '../../screens/sessions/sessions_screen.dart' show SessionChatScreen;

/// uri_router.dart — single dispatcher for `termipod://` URIs.
///
/// Used by two callers:
///   - The legacy `DeepLinkService` for external `termipod://` URLs
///     opened via the Android intent filter (cold-start + warm-link).
///   - The agent-driven mobile UI prototype's SSE listener — the
///     steward emits `mobile.intent { kind: "navigate", uri: ... }`
///     on its bus channel, mobile receives, calls `navigateToUri`.
///
/// Per `discussions/agent-driven-mobile-ui.md` §4.3, URIs are the
/// public addressing schema: one parser, one dispatcher, two callers
/// (external + steward). The router itself is read-only — it does
/// not mutate hub state. Write intents are out of scope for the
/// prototype.
///
/// URI grammar:
///
///   termipod://projects                                → Projects tab
///   termipod://activity[?filter=<f>]                   → Activity tab
///   termipod://me                                      → Me tab
///   termipod://hosts                                   → Hosts tab
///   termipod://settings                                → Settings tab
///   termipod://project/<id>                            → ProjectDetail (Overview)
///   termipod://project/<id>/{overview|activity|agents|tasks|files}
///                                                      → ProjectDetail tab-anchored
///   termipod://project/<id>/agents/<aid>               → open Agent sheet
///   termipod://project/<id>/tasks/<tid>                → push Task Detail
///   termipod://project/<id>/documents                  → push project docs list
///   termipod://project/<id>/documents/<docId>          → push Document Detail
///   termipod://project/<id>/plans                      → push Plans list
///   termipod://project/<id>/plans/<plId>               → push Plan Viewer
///   termipod://project/<id>/runs                       → push Runs list
///   termipod://project/<id>/runs/<rid>                 → push Run Detail
///   termipod://project/<id>/artifacts                  → push Artifacts list
///   termipod://document/<docId>                        → push Document Detail
///   termipod://run/<rid>                               → push Run Detail
///   termipod://host/<idOrName>                         → open Host sheet
///   termipod://session/<id>                            → push Session Chat
///   termipod://agent/<id>[/transcript]                 → open Agent sheet
///   termipod://insights[?scope=<k>&id=<x>]             → push Insights screen
///
/// **Name vs id.** Steward agents tend to know hostnames/handles, not
/// ULIDs. For URIs where this matters (currently `host/<x>`) the
/// router tries id-match first, then falls back to a case-insensitive
/// `name`/`hostname` match. For project/document/agent ids we keep
/// strict id-matching — those are referenced by the steward only
/// after it has fetched the entity (so id is in hand).
///
/// Unknown shapes return false; the caller may surface "unsupported
/// route" to the user. Forward-compat: new URI shapes can be added
/// without breaking older app builds — the router falls through.

/// Result of attempting to navigate to a URI. `ok` is true when the
/// router recognised the path and dispatched; `label` is a short
/// human-readable form for the steward-did-this banner ("Project: X",
/// "Activity feed", "Insights · stewards").
class NavigateResult {
  final bool ok;
  final String label;
  const NavigateResult(this.ok, this.label);

  static const NavigateResult unknown = NavigateResult(false, '');
}

/// Top-level dispatcher. Caller-provided `hub` snapshot + `setTab`
/// callback keep this function ref-agnostic — works from both
/// `WidgetRef` (ConsumerWidget) and `Ref` (Notifier) callers
/// without coupling to either type.
///
/// `refreshHub` (optional) lets the router recover from a missing
/// project / session / agent in the local cache by re-fetching the hub
/// snapshot and retrying the lookup once. This matters most for the
/// **steward-created-then-navigated** flow: the steward MCP tool that
/// creates an entity returns immediately and emits a `mobile.intent`
/// for the new id, but the mobile client's local snapshot hasn't
/// observed the create yet. Without a refresh-retry path the very
/// first navigate after creation always fails.
Future<NavigateResult> navigateToUri(
  BuildContext context,
  Uri uri, {
  required HubState? hub,
  required void Function(int index) setTab,
  Future<HubState?> Function()? refreshHub,
}) async {
  if (uri.scheme != 'termipod' && uri.scheme != 'muxpod') {
    return NavigateResult.unknown;
  }

  final host = uri.host.toLowerCase();
  final segments = uri.pathSegments;
  final qp = uri.queryParameters;

  switch (host) {
    case 'projects':
      setTab(0);
      return const NavigateResult(true, 'Projects');
    case 'activity':
      setTab(1);
      final filter = qp['filter'];
      return NavigateResult(true,
          filter != null ? 'Activity · $filter' : 'Activity');
    case 'me':
      setTab(2);
      return const NavigateResult(true, 'Me');
    case 'hosts':
      setTab(3);
      return const NavigateResult(true, 'Hosts');
    case 'settings':
      setTab(4);
      return const NavigateResult(true, 'Settings');

    case 'project':
      // termipod://project/<id>[/<sub-route>[/<sub-id>]]
      if (segments.isEmpty) return NavigateResult.unknown;
      final projectId = segments[0];
      if (segments.length >= 2) {
        final sub = segments[1].toLowerCase();
        final subId = segments.length >= 3 ? segments[2] : '';
        return _dispatchProjectSubRoute(
          context,
          projectId,
          sub,
          subId,
          hub: hub,
          setTab: setTab,
          refreshHub: refreshHub,
        );
      }
      return _openProject(context, projectId,
          hub: hub, setTab: setTab, refreshHub: refreshHub);

    case 'document':
      if (segments.isEmpty) return NavigateResult.unknown;
      return _openDocument(context, segments[0], setTab: setTab);

    case 'run':
      if (segments.isEmpty) return NavigateResult.unknown;
      return _openRun(context, segments[0], setTab: setTab);

    case 'host':
      if (segments.isEmpty) return NavigateResult.unknown;
      return _openHost(context, segments[0],
          hub: hub, setTab: setTab, refreshHub: refreshHub);

    case 'session':
      if (segments.isEmpty) return NavigateResult.unknown;
      final sessionId = segments[0];
      return _openSession(context, sessionId, hub: hub);

    case 'agent':
      if (segments.isEmpty) return NavigateResult.unknown;
      final agentId = segments[0];
      return _openAgent(context, agentId,
          hub: hub, refreshHub: refreshHub);

    case 'insights':
      return _openInsights(context, qp, hub: hub);
  }
  return NavigateResult.unknown;
}

/// Tab order locked by ProjectDetailScreen IA §6.2.
const Map<String, int> _projectTabIndex = {
  'overview': 0,
  'activity': 1,
  'agents': 2,
  'tasks': 3,
  'files': 4,
};

Future<NavigateResult> _dispatchProjectSubRoute(
  BuildContext context,
  String projectId,
  String sub,
  String subId, {
  required HubState? hub,
  required void Function(int index) setTab,
  required Future<HubState?> Function()? refreshHub,
}) async {
  // Detail screens that don't need the project in cache — push them
  // directly. The detail screen fetches its own record by id.
  switch (sub) {
    case 'documents':
      if (subId.isNotEmpty) {
        return _openDocument(context, subId, setTab: setTab);
      }
      return _openProjectDocuments(context, projectId, setTab: setTab);
    case 'tasks':
      if (subId.isNotEmpty) {
        return _openTask(context, projectId, subId, setTab: setTab);
      }
      // Tab-anchor onto ProjectDetail.
      return _openProject(context, projectId,
          hub: hub,
          setTab: setTab,
          refreshHub: refreshHub,
          tab: _projectTabIndex['tasks']);
    case 'agents':
      if (subId.isNotEmpty) {
        return _openAgent(context, subId,
            hub: hub, refreshHub: refreshHub);
      }
      return _openProject(context, projectId,
          hub: hub,
          setTab: setTab,
          refreshHub: refreshHub,
          tab: _projectTabIndex['agents']);
    case 'plans':
      if (subId.isNotEmpty) {
        return _openPlan(context, projectId, subId, setTab: setTab);
      }
      return _openPlansList(context, projectId, setTab: setTab);
    case 'runs':
      if (subId.isNotEmpty) {
        return _openRun(context, subId, setTab: setTab);
      }
      return _openRunsList(context, projectId, setTab: setTab);
    case 'artifacts':
      // Artifacts don't have a public-id detail route yet; ignore subId
      // for now and surface the list scoped to the project.
      return _openArtifactsList(context, projectId, setTab: setTab);
    case 'overview':
    case 'activity':
    case 'files':
      return _openProject(context, projectId,
          hub: hub,
          setTab: setTab,
          refreshHub: refreshHub,
          tab: _projectTabIndex[sub]);
  }
  return NavigateResult.unknown;
}

/// Lookup a record by `id` in a list of map snapshots; empty map on
/// miss. Pulled out as a helper because every entity dispatch follows
/// the same shape and we now retry-after-refresh.
Map<String, dynamic> _findById(
  List<Map<String, dynamic>>? list, String id) {
  if (list == null) return const <String, dynamic>{};
  return list.firstWhere(
    (e) => (e['id'] ?? '').toString() == id,
    orElse: () => const <String, dynamic>{},
  );
}

Future<NavigateResult> _openProject(
  BuildContext context,
  String projectId, {
  required HubState? hub,
  required void Function(int index) setTab,
  Future<HubState?> Function()? refreshHub,
  int? tab,
}) async {
  // Find the project record so ProjectDetailScreen has the data it
  // needs. Steward-created projects often miss on the first lookup
  // because mobile's snapshot hasn't observed the create yet — try
  // a refresh once before giving up.
  var match = _findById(hub?.projects, projectId);
  if (match.isEmpty && refreshHub != null) {
    final fresh = await refreshHub();
    match = _findById(fresh?.projects, projectId);
  }
  if (match.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  // Switch to Projects tab so the back stack reads naturally:
  // Projects tab → Project detail.
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => ProjectDetailScreen(
        project: match,
        initialTab: tab ?? 0,
      ),
    ),
  );
  final name = (match['name']?.toString() ?? projectId).trim();
  return NavigateResult(true, 'Project: ${name.isEmpty ? projectId : name}');
}

Future<NavigateResult> _openDocument(
  BuildContext context,
  String documentId, {
  required void Function(int index) setTab,
}) async {
  if (documentId.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  // Anchor the back stack to the Projects tab so the user lands back
  // there if they pop the document — matches the steward's mental
  // model of "the doc lives inside a project."
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => DocumentDetailScreen(documentId: documentId),
    ),
  );
  return const NavigateResult(true, 'Document');
}

Future<NavigateResult> _openProjectDocuments(
  BuildContext context,
  String projectId, {
  required void Function(int index) setTab,
}) async {
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => DocumentsScreen(projectId: projectId),
    ),
  );
  return const NavigateResult(true, 'Documents');
}

Future<NavigateResult> _openTask(
  BuildContext context,
  String projectId,
  String taskId, {
  required void Function(int index) setTab,
}) async {
  if (projectId.isEmpty || taskId.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) =>
          TaskDetailScreen(projectId: projectId, taskId: taskId),
    ),
  );
  return const NavigateResult(true, 'Task');
}

Future<NavigateResult> _openPlan(
  BuildContext context,
  String projectId,
  String planId, {
  required void Function(int index) setTab,
}) async {
  if (projectId.isEmpty || planId.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) =>
          PlanViewerScreen(projectId: projectId, planId: planId),
    ),
  );
  return const NavigateResult(true, 'Plan');
}

Future<NavigateResult> _openPlansList(
  BuildContext context,
  String projectId, {
  required void Function(int index) setTab,
}) async {
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => PlansScreen(projectId: projectId),
    ),
  );
  return const NavigateResult(true, 'Plans');
}

Future<NavigateResult> _openRun(
  BuildContext context,
  String runId, {
  required void Function(int index) setTab,
}) async {
  if (runId.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => RunDetailScreen(runId: runId),
    ),
  );
  return const NavigateResult(true, 'Run');
}

Future<NavigateResult> _openRunsList(
  BuildContext context,
  String projectId, {
  required void Function(int index) setTab,
}) async {
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => RunsScreen(projectId: projectId),
    ),
  );
  return const NavigateResult(true, 'Runs');
}

Future<NavigateResult> _openArtifactsList(
  BuildContext context,
  String projectId, {
  required void Function(int index) setTab,
}) async {
  if (!context.mounted) return NavigateResult.unknown;
  setTab(0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => ArtifactsScreen(projectId: projectId),
    ),
  );
  return const NavigateResult(true, 'Artifacts');
}

Future<NavigateResult> _openHost(
  BuildContext context,
  String idOrName, {
  required HubState? hub,
  required void Function(int index) setTab,
  required Future<HubState?> Function()? refreshHub,
}) async {
  if (idOrName.isEmpty) return NavigateResult.unknown;
  var match = _findHost(hub?.hosts, idOrName);
  if (match.isEmpty && refreshHub != null) {
    final fresh = await refreshHub();
    match = _findHost(fresh?.hosts, idOrName);
  }
  if (match.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  setTab(3);
  openHostDetail(context, match);
  final name = (match['name'] ?? match['hostname'] ?? idOrName).toString();
  return NavigateResult(true, 'Host: $name');
}

/// Host lookup tolerates either a hash id or a user-readable
/// `name`/`hostname` label. Steward agents tend to know hostnames,
/// not ULIDs — accepting both means the URI grammar is usable from
/// LLM output without forcing the model to memorise opaque ids.
Map<String, dynamic> _findHost(
  List<Map<String, dynamic>>? hosts, String idOrName) {
  if (hosts == null || hosts.isEmpty) return const <String, dynamic>{};
  final id = idOrName;
  final lower = idOrName.toLowerCase();
  // Pass 1: exact id match.
  for (final h in hosts) {
    if ((h['id'] ?? '').toString() == id) return h;
  }
  // Pass 2: case-insensitive name / hostname match.
  for (final h in hosts) {
    final name = (h['name'] ?? '').toString().toLowerCase();
    final hostname = (h['hostname'] ?? '').toString().toLowerCase();
    if (name == lower || hostname == lower) return h;
  }
  return const <String, dynamic>{};
}

Future<NavigateResult> _openSession(
  BuildContext context,
  String sessionId, {
  required HubState? hub,
}) async {
  if (hub == null) return NavigateResult.unknown;
  // Sessions live in sessionsProvider, but the chat ctor only
  // requires the ids + a title. For the prototype we accept "" as
  // fallback agent id — SessionChatScreen renders without an
  // agent id when needed.
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => SessionChatScreen(
        sessionId: sessionId,
        agentId: '',
        title: 'Session',
      ),
    ),
  );
  return const NavigateResult(true, 'Session');
}

Future<NavigateResult> _openAgent(
  BuildContext context,
  String agentId, {
  required HubState? hub,
  Future<HubState?> Function()? refreshHub,
}) async {
  var agent = _findById(hub?.agents, agentId);
  if (agent.isEmpty && refreshHub != null) {
    final fresh = await refreshHub();
    agent = _findById(fresh?.agents, agentId);
  }
  if (agent.isEmpty) return NavigateResult.unknown;
  if (!context.mounted) return NavigateResult.unknown;
  openAgentDetail(context, agent);
  final handle = (agent['handle'] ?? '').toString();
  return NavigateResult(true, 'Agent: ${handle.isEmpty ? agentId : handle}');
}

Future<NavigateResult> _openInsights(
  BuildContext context,
  Map<String, String> qp, {
  required HubState? hub,
}) async {
  final teamId = hub?.config?.teamId ?? '';
  // Default scope: team_stewards if no scope param (matches the
  // common "show me steward insights" intent shape).
  final scopeKind = qp['scope'] ?? 'team_stewards';
  final scopeId = qp['id'] ?? teamId;
  if (scopeId.isEmpty) return NavigateResult.unknown;

  InsightsScope scope;
  switch (scopeKind) {
    case 'team':
      scope = InsightsScope.team(scopeId);
    case 'team_stewards':
      scope = InsightsScope.teamStewards(scopeId);
    case 'project':
      scope = InsightsScope.project(scopeId);
    case 'agent':
      scope = InsightsScope.agent(scopeId);
    case 'engine':
      scope = InsightsScope.engine(scopeId);
    case 'host':
      scope = InsightsScope.host(scopeId);
    default:
      return NavigateResult.unknown;
  }

  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(builder: (_) => InsightsScreen(scope: scope)),
  );
  return NavigateResult(true, 'Insights · $scopeKind');
}

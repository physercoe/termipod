import 'package:flutter/material.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../screens/insights/insights_screen.dart';
import '../../screens/projects/documents_screen.dart'
    show DocumentDetailScreen, DocumentsScreen;
import '../../screens/projects/project_detail_screen.dart';
import '../../screens/projects/projects_screen.dart' show openAgentDetail;
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
/// URI grammar (v1 prototype):
///
///   termipod://projects                            → switch to Projects tab
///   termipod://activity[?filter=<f>]               → switch to Activity tab
///   termipod://me                                  → switch to Me tab
///   termipod://hosts                               → switch to Hosts tab
///   termipod://settings                            → switch to Settings tab
///   termipod://project/<id>[?tab=<t>]              → push Project Detail
///   termipod://project/<id>/documents              → push project docs list
///   termipod://project/<id>/documents/<docId>      → push Document Detail
///   termipod://document/<docId>                    → push Document Detail
///   termipod://session/<id>                        → push Session Chat
///   termipod://agent/<id>[/transcript]             → open Agent Detail sheet
///   termipod://insights[?scope=<k>&id=<x>]         → push Insights screen
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
      // termipod://project/<id>[/<sub-route>/<sub-id>]
      if (segments.isEmpty) return NavigateResult.unknown;
      final projectId = segments[0];
      if (segments.length >= 2) {
        final sub = segments[1].toLowerCase();
        if (sub == 'documents') {
          if (segments.length >= 3 && segments[2].isNotEmpty) {
            // …/documents/<docId> → push DocumentDetail directly.
            // The detail screen fetches the doc itself, so we don't
            // need the project in the local cache.
            return _openDocument(context, segments[2], setTab: setTab);
          }
          // …/documents → push the project-scoped documents list.
          return _openProjectDocuments(context, projectId, setTab: setTab);
        }
      }
      return _openProject(context, projectId,
          hub: hub, setTab: setTab, refreshHub: refreshHub);

    case 'document':
      if (segments.isEmpty) return NavigateResult.unknown;
      return _openDocument(context, segments[0], setTab: setTab);

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
      builder: (_) => ProjectDetailScreen(project: match),
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

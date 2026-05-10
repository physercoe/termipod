import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../providers/insights_provider.dart';
import '../../screens/home_screen.dart';
import '../../screens/insights/insights_screen.dart';
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
///   termipod://projects                     → switch to Projects tab
///   termipod://activity[?filter=<f>]        → switch to Activity tab
///   termipod://me                           → switch to Me tab
///   termipod://hosts                        → switch to Hosts tab
///   termipod://settings                     → switch to Settings tab
///   termipod://project/<id>[?tab=<t>]       → push Project Detail
///   termipod://session/<id>                 → push Session Chat
///   termipod://agent/<id>[/transcript]      → open Agent Detail sheet
///   termipod://insights[?scope=<k>&id=<x>]  → push Insights screen
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

/// Top-level dispatcher. Returns `NavigateResult` so the caller can
/// decide whether to surface the success banner ("steward → X") or
/// an error toast.
NavigateResult navigateToUri(BuildContext context, WidgetRef ref, Uri uri) {
  if (uri.scheme != 'termipod' && uri.scheme != 'muxpod') {
    return NavigateResult.unknown;
  }

  final host = uri.host.toLowerCase();
  final segments = uri.pathSegments;
  final qp = uri.queryParameters;

  switch (host) {
    case 'projects':
      _setTab(ref, 0);
      return const NavigateResult(true, 'Projects');
    case 'activity':
      _setTab(ref, 1);
      final filter = qp['filter'];
      return NavigateResult(true,
          filter != null ? 'Activity · $filter' : 'Activity');
    case 'me':
      _setTab(ref, 2);
      return const NavigateResult(true, 'Me');
    case 'hosts':
      _setTab(ref, 3);
      return const NavigateResult(true, 'Hosts');
    case 'settings':
      _setTab(ref, 4);
      return const NavigateResult(true, 'Settings');

    case 'project':
      // termipod://project/<id>[/...]
      if (segments.isEmpty) return NavigateResult.unknown;
      final projectId = segments[0];
      return _openProject(context, ref, projectId);

    case 'session':
      if (segments.isEmpty) return NavigateResult.unknown;
      final sessionId = segments[0];
      return _openSession(context, ref, sessionId);

    case 'agent':
      if (segments.isEmpty) return NavigateResult.unknown;
      final agentId = segments[0];
      return _openAgent(context, ref, agentId);

    case 'insights':
      return _openInsights(context, ref, qp);
  }
  return NavigateResult.unknown;
}

void _setTab(WidgetRef ref, int index) {
  ref.read(currentTabProvider.notifier).setTab(index);
}

NavigateResult _openProject(
    BuildContext context, WidgetRef ref, String projectId) {
  final hub = ref.read(hubProvider).value;
  if (hub == null) return NavigateResult.unknown;
  // Find the project record so ProjectDetailScreen has the data it
  // needs. If not loaded, surface an unknown — the caller can show
  // a "project not found in cache" toast.
  final match = hub.projects.firstWhere(
    (p) => (p['id'] ?? '').toString() == projectId,
    orElse: () => const <String, dynamic>{},
  );
  if (match.isEmpty) return NavigateResult.unknown;
  // Switch to Projects tab so the back stack reads naturally:
  // Projects tab → Project detail.
  _setTab(ref, 0);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => ProjectDetailScreen(project: match),
    ),
  );
  final name = (match['name']?.toString() ?? projectId).trim();
  return NavigateResult(true, 'Project: ${name.isEmpty ? projectId : name}');
}

NavigateResult _openSession(
    BuildContext context, WidgetRef ref, String sessionId) {
  final hub = ref.read(hubProvider).value;
  if (hub == null) return NavigateResult.unknown;
  // Sessions live in sessionsProvider, but the chat ctor only
  // requires the ids + a title. We resolve agent_id by walking the
  // hub state's sessions snapshot if present; otherwise the chat
  // screen handles missing data on its own.
  final agentId = _resolveAgentForSession(ref, sessionId);
  Navigator.of(context, rootNavigator: true).push(
    MaterialPageRoute(
      builder: (_) => SessionChatScreen(
        sessionId: sessionId,
        agentId: agentId,
        title: 'Session',
      ),
    ),
  );
  return const NavigateResult(true, 'Session');
}

String _resolveAgentForSession(WidgetRef ref, String sessionId) {
  // Walking sessionsProvider would be cleaner but pulls in an extra
  // provider import; for the prototype we accept "" as fallback —
  // SessionChatScreen renders without an agent id when needed.
  return '';
}

NavigateResult _openAgent(
    BuildContext context, WidgetRef ref, String agentId) {
  final hub = ref.read(hubProvider).value;
  if (hub == null) return NavigateResult.unknown;
  final agent = hub.agents.firstWhere(
    (a) => (a['id'] ?? '').toString() == agentId,
    orElse: () => const <String, dynamic>{},
  );
  if (agent.isEmpty) return NavigateResult.unknown;
  openAgentDetail(context, agent);
  final handle = (agent['handle'] ?? '').toString();
  return NavigateResult(true, 'Agent: ${handle.isEmpty ? agentId : handle}');
}

NavigateResult _openInsights(
    BuildContext context, WidgetRef ref, Map<String, String> qp) {
  final hub = ref.read(hubProvider).value;
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

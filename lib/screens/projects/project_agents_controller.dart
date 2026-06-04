import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../services/hub/agent_status.dart';

/// Orchestration seam for the project Agents view (WS2 of
/// docs/plans/internal-techdebt-cleanup.md). The live+stopped row
/// reconciliation, resumability resolution, and refresh fan-out live here as
/// pure functions + a provider so they can be unit-tested without a widget
/// harness; the `_AgentsView` widget keeps view composition only.
///
/// The app reads hub entities as `Map<String, dynamic>` by design (no DTOs);
/// these functions take and return those maps unchanged.

/// Terminated agents bound to [projectId], fetched directly because the warm
/// hub roster (`hubState.agents`) hides terminated rows. [projectAgentRows]
/// filters these to the *resumable* ones (session paused → "stopped") and
/// lists them alongside the live agents, so a Stop doesn't make a worker
/// vanish from the project (it's recoverable work, not history — the history
/// page deliberately drops it). Re-runs whenever the hub roster changes — a
/// Stop/Resume calls refreshAll — so the list self-heals after a lifecycle
/// action.
final projectTerminatedAgentsProvider = FutureProvider.family<
    List<Map<String, dynamic>>, String>((ref, projectId) async {
  ref.watch(hubProvider); // refetch on roster change (post Stop/Resume refreshAll)
  final client = ref.read(hubProvider.notifier).client;
  if (client == null || projectId.isEmpty) return const [];
  final all =
      await client.listAgents(includeTerminated: true, projectId: projectId);
  return all
      .where((a) =>
          (a['project_id'] ?? '').toString() == projectId &&
          (a['status'] ?? '').toString() == 'terminated')
      .toList();
});

/// The live agent rows for [projectId] — warm-roster agents whose `project_id`
/// matches. The warm roster excludes terminated/archived rows already.
List<Map<String, dynamic>> projectLiveAgentRows(
        List<Map<String, dynamic>> all, String projectId) =>
    all
        .where((a) => (a['project_id'] ?? '').toString() == projectId)
        .toList();

/// The stopped rows the warm roster hides: terminated agents whose session is
/// paused (→ resumable, label "stopped"), deduped against [liveIds]. An
/// archived (permanent) or session-less terminated agent is *not* included —
/// that's history, surfaced on the archived-agents page instead.
List<Map<String, dynamic>> projectStoppedAgentRows({
  required List<Map<String, dynamic>> terminated,
  required Set<String> liveIds,
  required SessionsState? sessions,
}) {
  return terminated.where((a) {
    final id = (a['id'] ?? '').toString();
    if (liveIds.contains(id)) return false;
    return agentResumability(sessionStatusForAgent(sessions, id)) ==
        AgentResumability.resumable;
  }).toList();
}

/// The full ordered Agents-view row list: live agents first, then the
/// stopped-resumable agents the warm roster hides. This is the pure core of
/// `_AgentsView.build` — given the same inputs it returns the byte-identical
/// list the widget used to assemble inline.
List<Map<String, dynamic>> projectAgentRows({
  required List<Map<String, dynamic>> all,
  required List<Map<String, dynamic>> terminated,
  required SessionsState? sessions,
  required String projectId,
}) {
  final live = projectLiveAgentRows(all, projectId);
  final liveIds = live.map((a) => (a['id'] ?? '').toString()).toSet();
  final stopped = projectStoppedAgentRows(
    terminated: terminated,
    liveIds: liveIds,
    sessions: sessions,
  );
  return [...live, ...stopped];
}

/// Refresh fan-out for the Agents view: re-fetch the roster, the sessions
/// snapshot (resumability lives there), and the stopped-agents fetch, so a
/// pull-to-refresh reconciles all three. Ordering matters — the terminated
/// fetch is invalidated first so it re-runs against the refreshed roster.
Future<void> refreshProjectAgents(WidgetRef ref, String projectId) async {
  ref.invalidate(projectTerminatedAgentsProvider(projectId));
  await ref.read(hubProvider.notifier).refreshAll();
  await ref.read(sessionsProvider.notifier).refresh();
}

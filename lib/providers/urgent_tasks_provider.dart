import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Cross-project urgent-task counter for the Me tab.
///
/// The hub has no team-scoped task list endpoint yet — each project
/// owns its task table. Rather than bolting one on for W3, we fan out
/// across the project list that [hubProvider] already loads and tally
/// open rows with `priority=urgent`. Project count is small in practice
/// (MVP teams run <~20 projects) and [listTasksCached] serves from the
/// snapshot cache between polls so the fan-out does not block the UI.
///
/// "Open" here excludes `done` — an urgent task that is already done
/// isn't asking for attention, just for history.
class UrgentTasksSummary {
  final int count;
  final List<UrgentTaskRow> top;
  const UrgentTasksSummary({required this.count, required this.top});

  static const UrgentTasksSummary empty = UrgentTasksSummary(count: 0, top: []);
}

class UrgentTaskRow {
  final String taskId;
  final String projectId;
  final String projectName;
  final String title;
  final String status;
  const UrgentTaskRow({
    required this.taskId,
    required this.projectId,
    required this.projectName,
    required this.title,
    required this.status,
  });
}

final urgentTasksProvider =
    FutureProvider.autoDispose<UrgentTasksSummary>((ref) async {
  final client = ref.watch(hubProvider.notifier).client;
  final hub = ref.watch(hubProvider).value;
  if (client == null || hub == null || hub.projects.isEmpty) {
    return UrgentTasksSummary.empty;
  }
  int count = 0;
  final rows = <UrgentTaskRow>[];
  for (final p in hub.projects) {
    final projectId = (p['id'] ?? '').toString();
    if (projectId.isEmpty) continue;
    final projectName = (p['name'] ?? projectId).toString();
    try {
      final cached = await client.listTasksCached(
        projectId,
        priority: 'urgent',
      );
      for (final t in cached.body) {
        final status = (t['status'] ?? '').toString();
        if (status == 'done') continue;
        count += 1;
        if (rows.length < 5) {
          rows.add(UrgentTaskRow(
            taskId: (t['id'] ?? '').toString(),
            projectId: projectId,
            projectName: projectName,
            title: (t['title'] ?? '').toString(),
            status: status,
          ));
        }
      }
    } catch (_) {
      // Skip failing projects rather than poison the digest; the
      // per-project Tasks screen will surface the real error.
    }
  }
  return UrgentTasksSummary(count: count, top: rows);
});

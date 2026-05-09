import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Snapshot of a project Insights response. The mobile panel renders
/// every block off [body], with [staleSince] carrying the snapshot
/// fetch time when we fell back to the offline cache (cache-first per
/// ADR-006).
class InsightsState {
  final Map<String, dynamic>? body;
  final DateTime? staleSince;
  final String? error;

  const InsightsState({
    this.body,
    this.staleSince,
    this.error,
  });
}

/// Family provider keyed by `projectId`. Empty id resolves to an empty
/// state so callers can `watch` unconditionally without short-circuiting.
/// `autoDispose` so closing a project detail screen frees the snapshot.
final insightsProvider =
    FutureProvider.autoDispose.family<InsightsState, String>(
  (ref, projectId) async {
    if (projectId.isEmpty) return const InsightsState();
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return const InsightsState();
    try {
      final res = await client.getInsightsCached(projectId: projectId);
      return InsightsState(body: res.body, staleSince: res.staleSince);
    } catch (e) {
      return InsightsState(error: e.toString());
    }
  },
);

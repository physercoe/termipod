import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Snapshot of a project Insights response. The mobile panel renders
/// every block off [body], with [staleSince] carrying the snapshot
/// fetch time when we fell back to the offline cache (cache-first per
/// ADR-006).
class InsightsState {
  final Map<String, dynamic>? body;
  final DateTime? staleSince;
  final bool loading;
  final String? error;

  const InsightsState({
    this.body,
    this.staleSince,
    this.loading = false,
    this.error,
  });

  InsightsState copyWith({
    Map<String, dynamic>? body,
    DateTime? staleSince,
    bool? loading,
    String? error,
    bool clearError = false,
    bool clearStale = false,
  }) {
    return InsightsState(
      body: body ?? this.body,
      staleSince: clearStale ? null : (staleSince ?? this.staleSince),
      loading: loading ?? this.loading,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

class InsightsNotifier extends FamilyAsyncNotifier<InsightsState, String> {
  @override
  Future<InsightsState> build(String projectId) async {
    if (projectId.isEmpty) {
      return const InsightsState();
    }
    return _fetch(projectId);
  }

  Future<InsightsState> _fetch(String projectId) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return const InsightsState();
    try {
      final res = await client.getInsightsCached(projectId: projectId);
      return InsightsState(
        body: res.body,
        staleSince: res.staleSince,
        loading: false,
      );
    } catch (e) {
      return InsightsState(error: e.toString());
    }
  }

  /// Pull-to-refresh and post-mutation reloads call this. Doesn't clear
  /// the prior body so the tile stays rendered while the refresh is in
  /// flight; the staleSince banner clears once the fresh response lands.
  Future<void> refresh(String projectId) async {
    state = AsyncData(state.value?.copyWith(loading: true) ??
        const InsightsState(loading: true));
    final next = await _fetch(projectId);
    state = AsyncData(next);
  }
}

final insightsProvider = AsyncNotifierProvider.family<
    InsightsNotifier, InsightsState, String>(InsightsNotifier.new);

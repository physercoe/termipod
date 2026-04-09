import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'active_session_provider.dart';
import 'connection_provider.dart';

/// 最終アクセス日時でソートされたセッション履歴プロバイダー
/// lastAccessedAt降順（最新が先頭）でソート
///
/// Raw PTY connections are filtered out here — they have no tmux sessions,
/// so any ActiveSession entries for them would be stale leftovers from a
/// previous tmux-mode config, and shouldn't surface in the Recent Sessions
/// list on the dashboard.
final sessionHistoryProvider = Provider<List<ActiveSession>>((ref) {
  final state = ref.watch(activeSessionsProvider);
  final connections = ref.watch(connectionsProvider).connections;

  // Build an id->isRawMode lookup so we can filter in O(n).
  final rawIds = <String>{
    for (final c in connections)
      if (c.isRawMode) c.id,
  };

  final filtered = state.sessions.where((s) => !rawIds.contains(s.connectionId));

  final sorted = [...filtered]..sort((a, b) {
    // lastAccessedAtがなければconnectedAtにフォールバック
    final aTime = a.lastAccessedAt ?? a.connectedAt;
    final bTime = b.lastAccessedAt ?? b.connectedAt;
    return bTime.compareTo(aTime); // 降順
  });

  return sorted;
});

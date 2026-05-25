import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/providers/active_session_provider.dart';
import 'package:termipod/providers/connection_provider.dart';

/// Regression coverage for the cold-start race that destructively wiped
/// the persisted active-sessions cache.
///
/// Background. `activeSessionsProvider` and `connectionsProvider` both
/// async-load on first build via `SharedPreferences.getInstance()`,
/// and their resolution order is non-deterministic. The previous
/// `_loadFromStorage` implementation pruned rows whose `connectionId`
/// no longer existed in `connectionsProvider.connections` AND
/// `_saveToStorage()`'d the pruned result — if `connectionsProvider`
/// was still `isLoading: true` (empty view) at prune time, every row
/// got treated as orphaned and the cache was overwritten with `[]`.
///
/// The fix has two invariants:
///   1. Never persist from the load path. Disk is read-only from
///      `_loadFromStorage`.
///   2. Gate the in-memory prune on `connectionsProvider.isLoading ==
///      false`. If connections hasn't settled, keep every persisted
///      row in memory; the next legitimate mutation rewrites the file
///      after connections has hydrated.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Map<String, Object?> seedSessionRow({
    required String connectionId,
    String sessionName = 'work',
  }) {
    final now = DateTime.now().toIso8601String();
    return {
      'connectionId': connectionId,
      'connectionName': 'host-$connectionId',
      'host': '10.0.0.1',
      'sessionName': sessionName,
      'windowCount': 1,
      'connectedAt': now,
      'isAttached': true,
      'lastAccessedAt': now,
    };
  }

  Map<String, Object?> seedConnectionRow(String id) {
    final now = DateTime.now().toIso8601String();
    // Connection.fromJson is tolerant of extra/missing optional fields;
    // we provide the minimum required by the model.
    return {
      'id': id,
      'name': 'live-$id',
      'host': '10.0.0.1',
      'port': 22,
      'username': 'me',
      'createdAt': now,
    };
  }

  // Drain the microtask queue. Two yields covers the
  // `SharedPreferences.getInstance()` future and the inner state
  // update. We deliberately drain more than once to avoid relying on
  // a single yield being enough.
  Future<void> drain() async {
    for (var i = 0; i < 6; i++) {
      await Future<void>.delayed(Duration.zero);
    }
  }

  group('activeSessionsProvider — cold-start load', () {
    test(
        'load preserves the persisted file even when no connections are known',
        () async {
      // Two pre-seeded rows for connections that have NOT been
      // hydrated into `connectionsProvider`. Under the pre-fix
      // implementation, the load-time prune treated both as orphans
      // and overwrote disk with `[]`.
      final seeded = [
        seedSessionRow(connectionId: 'conn-a'),
        seedSessionRow(connectionId: 'conn-b', sessionName: 'play'),
      ];
      SharedPreferences.setMockInitialValues({
        'active_sessions': jsonEncode(seeded),
        // `connections` key intentionally omitted — leaves
        // ConnectionsNotifier with an empty (post-load) connections
        // list. The fix must still keep disk intact.
      });

      final container = ProviderContainer();
      addTearDown(container.dispose);

      container.read(activeSessionsProvider);
      await drain();

      // Disk must be intact — the load path is read-only.
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString('active_sessions');
      expect(raw, isNotNull,
          reason: 'persisted cache must survive the load-time prune');
      final decoded = jsonDecode(raw!) as List<dynamic>;
      expect(decoded, hasLength(2),
          reason: 'no rows should be deleted from disk during load');
      final ids = decoded.map((e) => (e as Map)['connectionId']).toSet();
      expect(ids, equals({'conn-a', 'conn-b'}));
    });

    test('in-memory prune removes orphans without overwriting the file',
        () async {
      // Both keys seeded. `conn-a` exists in connections, `conn-orphan`
      // does not. After connections hydrates, the in-memory state
      // should filter to `conn-a` only — but the disk file must keep
      // both rows.
      SharedPreferences.setMockInitialValues({
        'active_sessions': jsonEncode([
          seedSessionRow(connectionId: 'conn-a'),
          seedSessionRow(connectionId: 'conn-orphan', sessionName: 'play'),
        ]),
        'connections': jsonEncode([seedConnectionRow('conn-a')]),
      });

      final container = ProviderContainer();
      addTearDown(container.dispose);

      // Force connections to hydrate first so the prune sees a settled
      // `isLoading: false` view. Without this, the test would race the
      // two providers' async loads and become flaky.
      container.read(connectionsProvider);
      await drain();
      expect(container.read(connectionsProvider).isLoading, isFalse,
          reason: 'connections must hydrate before we trigger the prune');

      container.read(activeSessionsProvider);
      await drain();

      // In-memory view filtered to the live row only.
      final state = container.read(activeSessionsProvider);
      expect(state.sessions, hasLength(1));
      expect(state.sessions.single.connectionId, 'conn-a');

      // Disk still has BOTH rows — prune is render-side only.
      final prefs = await SharedPreferences.getInstance();
      final raw =
          jsonDecode(prefs.getString('active_sessions')!) as List<dynamic>;
      expect(raw, hasLength(2),
          reason: 'load-time prune must never write to disk');
    });

    test('load is a no-op when no key is persisted', () async {
      SharedPreferences.setMockInitialValues(<String, Object>{});

      final container = ProviderContainer();
      addTearDown(container.dispose);

      container.read(activeSessionsProvider);
      await drain();

      final state = container.read(activeSessionsProvider);
      expect(state.sessions, isEmpty);

      // No spurious empty-list write either.
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.containsKey('active_sessions'), isFalse);
    });
  });
}

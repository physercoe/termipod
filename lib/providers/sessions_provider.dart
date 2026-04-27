import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Sessions list state — sourced from the hub's sessions endpoint and
/// bucketed for the UI into active vs previous. Active means status
/// is open or interrupted (the latter being a session whose underlying
/// agent died after a host restart, ready for resume per W2-S3).
/// Previous covers closed sessions.
class SessionsState {
  final List<Map<String, dynamic>> active;
  final List<Map<String, dynamic>> previous;
  const SessionsState({
    this.active = const [],
    this.previous = const [],
  });

  bool get isEmpty => active.isEmpty && previous.isEmpty;
}

class SessionsNotifier extends AsyncNotifier<SessionsState> {
  @override
  Future<SessionsState> build() async {
    final hub = ref.watch(hubProvider);
    final hubState = hub.value;
    final client = hubState == null
        ? null
        : ref.read(hubProvider.notifier).client;
    if (client == null) return const SessionsState();
    // Cached read-through: cold open offline still surfaces the last
    // known stewards. Same fallback contract as projects/hosts/etc.
    final cached = await client.listSessionsCached();
    final all = cached.body;
    final active = <Map<String, dynamic>>[];
    final previous = <Map<String, dynamic>>[];
    for (final s in all) {
      final status = (s['status'] ?? '').toString();
      if (status == 'open' || status == 'interrupted') {
        active.add(s);
      } else {
        previous.add(s);
      }
    }
    return SessionsState(active: active, previous: previous);
  }

  /// Force a refresh — useful after open/close/resume.
  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(build);
  }

  Future<void> close(String id) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    await client.closeSession(id);
    await refresh();
  }

  /// Resumes an interrupted session and refreshes the list. Returns
  /// the new agent id so the caller can navigate into the session
  /// chat without waiting for the next refresh tick.
  Future<String?> resume(String id) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return null;
    final out = await client.resumeSession(id);
    await refresh();
    return out['new_agent_id']?.toString();
  }

  /// Renames a session. Empty `title` clears the row's title back to
  /// "(untitled session)".
  Future<void> rename(String id, String title) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    await client.renameSession(id, title);
    await refresh();
  }

  /// Soft-deletes a closed session. Hub refuses if the session is
  /// still open or interrupted; close it first via [close] in that
  /// case.
  Future<void> delete(String id) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    await client.deleteSession(id);
    await refresh();
  }
}

final sessionsProvider =
    AsyncNotifierProvider<SessionsNotifier, SessionsState>(SessionsNotifier.new);

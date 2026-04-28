import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Sessions list state — sourced from the hub's sessions endpoint and
/// bucketed for the UI into active vs previous. "Active" buckets
/// `active` and `paused` sessions (the latter being one whose
/// underlying agent died after a host restart, ready for resume per
/// W2-S3). "Previous" covers `archived` sessions.
///
/// Per ADR-009 the hub emits the new vocabulary `active / paused /
/// archived / deleted`. Old strings (`open / interrupted / closed`)
/// are still tolerated here for the brief window where a not-yet-
/// migrated hub talks to a new app build.
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
      if (_isLive(status)) {
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

  Future<void> archive(String id) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    await client.archiveSession(id);
    await refresh();
  }

  /// Resumes a paused session and refreshes the list. Returns the new
  /// agent id so the caller can navigate into the session chat
  /// without waiting for the next refresh tick.
  Future<String?> resume(String id) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return null;
    final out = await client.resumeSession(id);
    await refresh();
    return out['new_agent_id']?.toString();
  }

  /// Forks an archived session into a new active one (ADR-009 D4).
  /// Returns the new session payload so the caller can navigate
  /// directly into the chat without waiting for the next refresh.
  Future<Map<String, dynamic>?> fork(String id, {String? agentId}) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return null;
    final out = await client.forkSession(id, agentId: agentId);
    await refresh();
    return out;
  }

  /// Renames a session. Empty `title` clears the row's title back to
  /// "(untitled session)".
  Future<void> rename(String id, String title) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    await client.renameSession(id, title);
    await refresh();
  }

  /// Soft-deletes an archived session. Hub refuses if the session is
  /// still active or paused; archive it first via [archive] in that
  /// case.
  Future<void> delete(String id) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    await client.deleteSession(id);
    await refresh();
  }
}

/// Returns true for session statuses where the conversation is live
/// (engine attached) or paused (engine detached but resumable).
/// Tolerates both the new ADR-009 vocabulary (`active`, `paused`)
/// and the legacy strings (`open`, `interrupted`) for the brief
/// rollout window.
bool _isLive(String status) =>
    status == 'active' ||
    status == 'paused' ||
    status == 'open' ||
    status == 'interrupted';

final sessionsProvider =
    AsyncNotifierProvider<SessionsNotifier, SessionsState>(SessionsNotifier.new);

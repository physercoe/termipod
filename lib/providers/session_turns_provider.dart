import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// The session's turn index (ADR-038 §3 / plan P2) — the structure index the
/// analysis surface lists and jumps from. Keyed by session id; `autoDispose`
/// so leaving the view frees it. Empty id / no client resolves to an empty
/// list so callers can `watch` unconditionally. The hub backfills the index
/// lazily on read, so this stays current for live runs; the surface refreshes
/// by invalidating the provider (shared pull-to-refresh with the digest).
final sessionTurnsProvider =
    FutureProvider.autoDispose.family<List<Map<String, dynamic>>, String>(
  (ref, sessionId) async {
    if (sessionId.isEmpty) return const [];
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return const [];
    return client.getSessionTurns(sessionId);
  },
);

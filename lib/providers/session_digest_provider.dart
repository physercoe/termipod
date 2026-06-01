import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'hub_provider.dart';

/// Snapshot of a session's run digest (ADR-038 §5) — the canonical
/// summary the analysis surface renders. [body] is the digest map;
/// [staleSince] carries the snapshot fetch time when we fell back to the
/// offline cache (cache-first per ADR-006), so the report card can label
/// itself "as of `<ts>`" rather than implying live freshness.
class SessionDigestState {
  final Map<String, dynamic>? body;
  final DateTime? staleSince;
  final String? error;

  const SessionDigestState({this.body, this.staleSince, this.error});
}

/// Family provider keyed by session id. `autoDispose` so leaving the
/// analysis view frees the snapshot. Empty id resolves to an empty state
/// so callers can `watch` unconditionally. The digest is lazily
/// (re)computed hub-side on read, so this stays current for live runs —
/// the surface refreshes by invalidating this provider (pull-to-refresh).
final sessionDigestProvider =
    FutureProvider.autoDispose.family<SessionDigestState, String>(
  (ref, sessionId) async {
    if (sessionId.isEmpty) return const SessionDigestState();
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return const SessionDigestState();
    try {
      final res = await client.getSessionDigestCached(sessionId);
      return SessionDigestState(body: res.body, staleSince: res.staleSince);
    } catch (e) {
      return SessionDigestState(error: e.toString());
    }
  },
);

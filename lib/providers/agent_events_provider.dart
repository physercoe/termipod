import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/hub/hub_client.dart';
import 'hub_provider.dart';

/// agent_events_provider.dart — shared data layer for an agent's
/// event stream (P1 of `docs/plans/agent-events-shared-provider.md`,
/// pre-MVP).
///
/// Owns the SSE subscription, the cache-then-refresh backfill, the
/// reconnect-with-backoff timer, the in-memory event ring buffer
/// and the dedup set for one `(agentId, sessionId)` pair. Multiple
/// consumers (today: the steward overlay; post-MVP: AgentFeed in
/// Sessions chat) subscribe via Riverpod's family selector and
/// share the lifecycle.
///
/// Key design points:
///
/// - **`autoDispose`** — when no consumer watches, the SSE
///   subscription closes and the buffer is freed. Collapsing the
///   overlay puck while no Sessions chat is open releases the
///   steward stream automatically.
/// - **Window cap = 200 events.** Matches `agent_feed.dart`'s
///   `_pageSize` so AgentFeed's eventual migration (P2) doesn't
///   introduce regressions in the cold-open paint. The overlay
///   slices the tail it needs (currently last 20).
/// - **Resubscribe behaviour** — when a new consumer mounts and
///   we already have events loaded from a prior consumer, we
///   trust the existing window and only re-fetch if
///   `staleSince != null` (i.e. the live network was unreachable
///   on the previous attempt). This avoids burning bandwidth
///   on every consumer-mount churn.
/// - **Reconnect** — exponential backoff capped at 16 s, mirrors
///   `agent_feed.dart`'s established pattern. Idle-drop signatures
///   (proxy timeouts, carrier NAT) suppress the user-facing
///   banner; only real connectivity failures surface.
///
/// What this provider deliberately does NOT do:
///   - Demuxing event kinds (consumer's responsibility).
///   - Rendering — provides raw event maps; consumers shape them.
///   - Persistence beyond what `HubSnapshotCache` already does.
///   - `session.init` one-shot backfill (stays in `agent_feed.dart`
///     as a UI-routing concern).
///   - Mode/model picker payload reporting (UI-routing concern).
///
/// See also: ADR-023 (agent-driven mobile UI), the parent plan,
/// and `lib/widgets/agent_feed.dart` for the established lifecycle
/// patterns this provider preserves.

/// Window cap. Each `(agentId, sessionId)` keeps at most this
/// many events in memory. Matches `AgentFeed._pageSize` so P2
/// (AgentFeed migration) doesn't regress cold-open paging.
const int _maxWindow = 200;

/// Backfill request size on bootstrap. Same value as window cap so
/// the first paint shows the freshest 200 events.
const int _backfillLimit = 200;

/// Banner grace before surfacing "stream dropped" to consumers via
/// `staleSince`. A successful resubscribe within this window cancels
/// the indicator so brief idle reaps don't flicker the UI.
const Duration _bannerGrace = Duration(seconds: 3);

/// Identifies one event stream by `(agentId, sessionId)`. `sessionId`
/// nullable — when null the consumer is asking for ALL of the
/// agent's events regardless of session (used by the overlay
/// historically, though current overlay always supplies a session
/// after backfill resolves it).
@immutable
class AgentEventsKey {
  final String agentId;
  final String? sessionId;
  const AgentEventsKey(this.agentId, this.sessionId);

  @override
  bool operator ==(Object other) =>
      other is AgentEventsKey &&
      other.agentId == agentId &&
      other.sessionId == sessionId;

  @override
  int get hashCode => Object.hash(agentId, sessionId);

  @override
  String toString() =>
      'AgentEventsKey($agentId${sessionId == null ? '' : ', $sessionId'})';
}

/// State for one `(agentId, sessionId)` pair. Provider listeners
/// rebuild on any change; selectors (`select`) can narrow to one
/// field where churn matters.
@immutable
class AgentEventsState {
  /// ASC by `seq`. May not start at seq=1 if older history hasn't
  /// been paged in via `loadOlder()` (post-MVP for AgentFeed).
  final List<Map<String, dynamic>> events;

  /// True while the FIRST backfill is in flight. Cache-paint may
  /// have populated `events` already — `events.isNotEmpty +
  /// loading == true` is valid during the cache→network gap.
  final bool loading;

  /// Set once a `loadOlder()` call returned fewer rows than
  /// requested. AgentFeed uses this to disable its older-history
  /// pager. Always false in P1 (loadOlder is a stub).
  final bool atHead;

  /// Non-null when the live network is unreachable and we're
  /// serving cached / stale data. Drives "Offline · last updated X"
  /// banners.
  final DateTime? staleSince;

  /// Set on a hard backfill failure (transport / 4xx). Live SSE
  /// drops use staleSince + the per-consumer reconnect retry; only
  /// genuine bootstrap failures land here.
  final String? error;

  /// `seq` cursor of the highest-seq event in `events`. Live SSE
  /// resumes from this on reconnect.
  final int? maxSeq;

  const AgentEventsState({
    this.events = const [],
    this.loading = true,
    this.atHead = false,
    this.staleSince,
    this.error,
    this.maxSeq,
  });

  AgentEventsState copyWith({
    List<Map<String, dynamic>>? events,
    bool? loading,
    bool? atHead,
    DateTime? staleSince,
    String? error,
    int? maxSeq,
    bool clearStaleSince = false,
    bool clearError = false,
  }) {
    return AgentEventsState(
      events: events ?? this.events,
      loading: loading ?? this.loading,
      atHead: atHead ?? this.atHead,
      staleSince:
          clearStaleSince ? null : (staleSince ?? this.staleSince),
      error: clearError ? null : (error ?? this.error),
      maxSeq: maxSeq ?? this.maxSeq,
    );
  }
}

/// Notifier owning the lifecycle for one `(agentId, sessionId)`.
/// Matches the codebase's existing `Notifier<State>` + constructor
/// arg pattern (see `ssh_provider.dart`, `tmux_provider.dart`,
/// `file_transfer_provider.dart`) rather than the
/// `AutoDisposeFamilyNotifier<S, A>` variant — the autoDispose
/// behaviour comes from `NotifierProvider.autoDispose.family` at
/// the bottom of this file.
class AgentEventsNotifier extends Notifier<AgentEventsState> {
  final AgentEventsKey key;
  AgentEventsNotifier(this.key);

  StreamSubscription<Map<String, dynamic>>? _sub;
  Timer? _reconnect;
  Timer? _bannerGraceTimer;
  int _reconnectAttempt = 0;
  /// Globally-unique event ids we've already ingested. Bounded to
  /// `_maxWindow` like `events` itself.
  final _ids = <String>{};

  @override
  AgentEventsState build() {
    ref.onDispose(_close);
    // Fire-and-forget: the build() return value gives consumers the
    // initial loading state; bootstrap mutates state once data lands.
    Future.microtask(_bootstrap);
    return const AgentEventsState();
  }

  // ---------------------------------------------------------------
  // Bootstrap
  // ---------------------------------------------------------------

  Future<void> _bootstrap() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      state = state.copyWith(
        loading: false,
        error: 'Hub not configured',
      );
      return;
    }

    // 1. Cache-only first paint — instant render from disk if a
    //    snapshot exists. Mirrors `agent_feed.dart` line 561.
    var paintedFromCache = false;
    try {
      final cacheOnly = await client.listAgentEventsCacheOnly(
        key.agentId,
        tail: true,
        limit: _backfillLimit,
        sessionId: key.sessionId,
      );
      if (cacheOnly != null) {
        final asc = cacheOnly.body.reversed.toList(growable: false);
        _seedFromList(asc, staleSince: cacheOnly.staleSince);
        paintedFromCache = true;
      }
    } catch (_) {
      // Cache read failed; fall through to network-first below.
    }

    // 2. Cache-then-refresh — network-first with cache fallback.
    try {
      final cached = await client.listAgentEventsCached(
        key.agentId,
        tail: true,
        limit: _backfillLimit,
        sessionId: key.sessionId,
      );
      if (paintedFromCache) {
        // Cache-paint + SSE will already be delivering live state
        // through ingestion below. Don't re-seed `events` here —
        // SSE has been delivering since cache paint, and replacing
        // would lose the delta. Just clear the stale pill.
        state = state.copyWith(
          loading: false,
          staleSince: cached.staleSince,
          clearStaleSince: cached.staleSince == null,
        );
      } else {
        final asc = cached.body.reversed.toList(growable: false);
        _seedFromList(asc, staleSince: cached.staleSince);
        state = state.copyWith(loading: false);
      }
    } on HubApiError catch (e) {
      if (paintedFromCache) {
        // Keep the cache paint; the user has a usable view. Surface
        // the live failure through staleSince but not as a hard
        // error.
        state = state.copyWith(
          loading: false,
          staleSince: state.staleSince ?? DateTime.now().toUtc(),
        );
      } else {
        state = state.copyWith(
          loading: false,
          error: 'Backfill failed (${e.status})',
        );
      }
    } catch (e) {
      if (paintedFromCache) {
        state = state.copyWith(
          loading: false,
          staleSince: state.staleSince ?? DateTime.now().toUtc(),
        );
      } else {
        state = state.copyWith(
          loading: false,
          error: 'Backfill failed: $e',
        );
      }
    }

    // 3. Subscribe to live SSE. Even if backfill errored, we still
    //    subscribe — the agent may be silent right now but produce
    //    events momentarily, and the user shouldn't have to refresh
    //    to see them.
    _subscribe(client);
  }

  void _seedFromList(
    List<Map<String, dynamic>> asc, {
    DateTime? staleSince,
  }) {
    final cap = math.min(asc.length, _maxWindow);
    final window = asc.length > _maxWindow
        ? asc.sublist(asc.length - _maxWindow)
        : List<Map<String, dynamic>>.from(asc);
    _ids.clear();
    for (final e in window) {
      final id = (e['id'] ?? '').toString();
      if (id.isNotEmpty) _ids.add(id);
    }
    int? maxSeq;
    for (final e in window) {
      final s = (e['seq'] as num?)?.toInt();
      if (s == null) continue;
      if (maxSeq == null || s > maxSeq) maxSeq = s;
    }
    state = state.copyWith(
      events: window,
      maxSeq: maxSeq,
      staleSince: staleSince,
      clearStaleSince: staleSince == null,
    );
    if (kDebugMode) {
      // ignore: avoid_print
      print('[agent-events] seeded ${window.length} events for $key '
          '(maxSeq=$maxSeq, stale=$staleSince, dropped=${asc.length - cap})');
    }
  }

  // ---------------------------------------------------------------
  // Live SSE subscribe + reconnect
  // ---------------------------------------------------------------

  void _subscribe(HubClient client) {
    _sub?.cancel();
    final sinceCursor = state.maxSeq;
    _sub = client
        .streamAgentEvents(
          key.agentId,
          sinceSeq: sinceCursor,
          sessionId: key.sessionId,
        )
        .listen(
      _ingest,
      onError: (Object e) {
        _scheduleReconnect(
          client,
          reason: '$e',
          isError: true,
        );
      },
      onDone: () {
        _scheduleReconnect(
          client,
          reason: 'stream closed',
          isError: false,
        );
      },
    );
  }

  void _ingest(Map<String, dynamic> evt) {
    final id = (evt['id'] ?? '').toString();
    if (id.isNotEmpty && !_ids.add(id)) {
      // Duplicate (e.g. SSE replay-on-reconnect); skip.
      return;
    }
    final next = List<Map<String, dynamic>>.from(state.events)..add(evt);
    if (next.length > _maxWindow) {
      // Trim from the head; also trim _ids so the dedup set doesn't
      // leak unboundedly across long-lived sessions.
      final dropped = next.sublist(0, next.length - _maxWindow);
      next.removeRange(0, dropped.length);
      for (final d in dropped) {
        final did = (d['id'] ?? '').toString();
        if (did.isNotEmpty) _ids.remove(did);
      }
    }
    final s = (evt['seq'] as num?)?.toInt();
    final newMaxSeq =
        (s != null && (state.maxSeq == null || s > state.maxSeq!))
            ? s
            : state.maxSeq;
    state = state.copyWith(
      events: next,
      maxSeq: newMaxSeq,
    );
    // Successful ingestion implies a healthy connection. Clear any
    // stale banner / reconnect counter that lingered from earlier
    // drops.
    if (state.staleSince != null) {
      state = state.copyWith(clearStaleSince: true);
    }
    _reconnectAttempt = 0;
    _bannerGraceTimer?.cancel();
    _bannerGraceTimer = null;
  }

  void _scheduleReconnect(
    HubClient client, {
    required String reason,
    required bool isError,
  }) {
    final delaySecs = math.min(16, 1 << _reconnectAttempt);
    _reconnectAttempt += 1;

    // Mirror agent_feed's idle-drop signature suppression — proxy
    // timeouts and carrier NAT reaps don't deserve a banner.
    final shouldSurface = isError && !_isIdleDropSignature(reason);
    if (shouldSurface) {
      // Use staleSince as the "connection unhealthy" signal. The
      // banner-grace timer postpones surfacing; a successful
      // resubscribe within the grace window cancels it.
      _bannerGraceTimer?.cancel();
      _bannerGraceTimer = Timer(_bannerGrace, () {
        if (state.staleSince == null) {
          state = state.copyWith(staleSince: DateTime.now().toUtc());
        }
      });
    }

    _reconnect?.cancel();
    _reconnect = Timer(Duration(seconds: delaySecs), () {
      _bannerGraceTimer?.cancel();
      _bannerGraceTimer = null;
      _subscribe(client);
    });

    if (kDebugMode) {
      // ignore: avoid_print
      print('[agent-events] reconnect scheduled in ${delaySecs}s for $key '
          '(reason=$reason, surface=$shouldSurface)');
    }
  }

  static bool _isIdleDropSignature(String reason) {
    final lc = reason.toLowerCase();
    return lc.contains('connection closed') ||
        lc.contains('connection reset') ||
        lc.contains('connection abort') ||
        lc.contains('connection terminated') ||
        lc.contains('http2streamlimit') ||
        lc.contains('stream closed') ||
        lc.contains('before full body received');
  }

  // ---------------------------------------------------------------
  // Public methods
  // ---------------------------------------------------------------

  /// Forces a fresh cache-then-refresh + resubscribe. Used by the
  /// "Refresh" surfaces (e.g. AgentFeed pull-to-refresh in P2). In
  /// P1 not yet exercised by any consumer.
  Future<void> refresh() async {
    _sub?.cancel();
    _reconnect?.cancel();
    _bannerGraceTimer?.cancel();
    state = state.copyWith(loading: true, clearError: true);
    await _bootstrap();
  }

  /// Loads older events. Stub for P1 — needed for P2 (AgentFeed
  /// migration) when older-history pagination moves out of the
  /// widget. Returns false to indicate no-op so future call-sites
  /// can branch on the return value.
  Future<bool> loadOlder() async {
    return false;
  }

  // ---------------------------------------------------------------
  // Cleanup
  // ---------------------------------------------------------------

  void _close() {
    _sub?.cancel();
    _reconnect?.cancel();
    _bannerGraceTimer?.cancel();
  }
}

final agentEventsProvider = NotifierProvider.autoDispose
    .family<AgentEventsNotifier, AgentEventsState, AgentEventsKey>(
  (key) => AgentEventsNotifier(key),
);

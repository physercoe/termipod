import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../screens/home_screen.dart';
import '../../services/deep_link/uri_router.dart';
import '../../services/hub/hub_client.dart';
import '../../services/steward_handle.dart';

/// Number of recent events to pre-load into the overlay chat on
/// cold open (W1 — overlay-history-and-snippets plan, B2). 50 events
/// covers ~8–15 conversational turns in practice; tune after demo
/// usage.
const int _backfillLimit = 50;

/// Max in-memory message rolling window for the overlay's compact
/// chat. The Sessions screen owns the full transcript; the overlay's
/// purpose is the recent directive context, not the entire log.
/// 20 messages ≈ 10 turns of user-prompt → steward-response.
const int _overlayMessageCap = 20;

/// steward_overlay_controller.dart — owns the lifecycle of the overlay's
/// connection to the team's general steward.
///
/// Responsibilities:
///   1. Ensure the general steward is running (call ensureGeneralSteward
///      on first build; idempotent fast path on subsequent builds).
///   2. Resolve the steward's active session id.
///   3. Subscribe to the steward's SSE event stream.
///   4. Demultiplex incoming events into:
///        - chat-renderable messages (text frames, tool-call summaries)
///        - mobile.intent navigation events (dispatched via uri_router
///          + a snackbar banner so the user sees what the steward did).
///   5. Send user input via postAgentInput.
///
/// Two consumers:
///   - StewardOverlayChat (UI) reads `messages` + calls sendUserText
///   - StewardOverlay (the shell) is informed of intent dispatches
///     via the global navigator key so the toast can render even when
///     the chat panel is collapsed.

enum OverlayChatRole { user, steward, system }

/// Records what the steward DID via a `mobile.intent` event. Distinct
/// from a free-form steward text reply: this is a structured action
/// the bubble renders as a compact past-tense pill with tap-through.
///
/// `verb` is the action label ("navigated to", "created", "edited",
/// "wrote") so future intent kinds beyond navigation render with the
/// right grammar. v1 only exercises navigation; the field exists so
/// new actions don't require a model migration.
@immutable
class OverlayIntentAction {
  final String verb;
  final String target;
  final String uri;
  const OverlayIntentAction({
    required this.verb,
    required this.target,
    required this.uri,
  });
}

@immutable
class OverlayChatMessage {
  final OverlayChatRole role;
  final String text;
  final String? note; // small footnote (e.g. URI for intent)
  final DateTime ts;
  /// Non-null when this message represents a `mobile.intent` event.
  /// Renders as a compact pill with tap-through to re-fire the URI
  /// rather than a generic text bubble.
  final OverlayIntentAction? intentAction;
  const OverlayChatMessage({
    required this.role,
    required this.text,
    this.note,
    required this.ts,
    this.intentAction,
  });
}

@immutable
class StewardOverlayState {
  /// Resolved general-steward agent id, null while ensuring/loading.
  final String? agentId;
  /// Active session id for that agent. Empty until resolved.
  final String sessionId;
  /// Rolling chat history. Newest at the end.
  final List<OverlayChatMessage> messages;
  /// Last error (auth, network, ensure-spawn). Cleared on next success.
  final String? error;

  const StewardOverlayState({
    this.agentId,
    this.sessionId = '',
    this.messages = const [],
    this.error,
  });

  StewardOverlayState copyWith({
    String? agentId,
    String? sessionId,
    List<OverlayChatMessage>? messages,
    String? error,
    bool clearError = false,
  }) {
    return StewardOverlayState(
      agentId: agentId ?? this.agentId,
      sessionId: sessionId ?? this.sessionId,
      messages: messages ?? this.messages,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// Global navigator key — wired into MaterialApp so the controller can
/// dispatch URI navigations + show snackbars from outside the regular
/// widget tree (the SSE listener fires asynchronously and may run
/// while the chat panel is collapsed).
final overlayNavigatorKeyProvider =
    Provider<GlobalKey<NavigatorState>>((_) => GlobalKey<NavigatorState>());

class StewardOverlayController extends Notifier<StewardOverlayState> {
  StreamSubscription<Map<String, dynamic>>? _sub;
  bool _initStarted = false;

  @override
  StewardOverlayState build() {
    ref.onDispose(() {
      _sub?.cancel();
    });
    return const StewardOverlayState();
  }

  /// Lazily ensures the general steward + opens its event stream.
  /// Safe to call repeatedly; only the first call does the work.
  Future<void> ensureStarted() async {
    if (_initStarted) return;
    _initStarted = true;
    await _bootstrap();
  }

  Future<void> _bootstrap() async {
    final hub = ref.read(hubProvider).value;
    final client = ref.read(hubProvider.notifier).client;
    if (hub == null || client == null) {
      state = state.copyWith(error: 'Hub not configured');
      return;
    }
    try {
      // 1. Ensure-spawn — fast path returns existing agent id.
      final res = await client.ensureGeneralSteward();
      final agentId = (res['agent_id'] ?? '').toString();
      if (agentId.isEmpty) {
        state = state.copyWith(error: 'Steward agent_id missing in response');
        return;
      }

      // 2. Refresh sessions so we can find the steward's active one.
      await ref.read(sessionsProvider.notifier).refresh();
      final sessions = ref.read(sessionsProvider).value;
      String sessionId = '';
      if (sessions != null) {
        for (final s in sessions.active) {
          if ((s['current_agent_id'] ?? '').toString() == agentId) {
            sessionId = (s['id'] ?? '').toString();
            break;
          }
        }
      }

      // 3. History backfill (W1 — overlay-history-and-snippets plan).
      //    Pull the last `_backfillLimit` events for this agent +
      //    session via the read-through cache so cold-open paints
      //    instantly from disk, then refreshes from the network.
      //    Events are returned newest-first (tail: true / seq DESC);
      //    reverse to ASC so the chat reads oldest→newest. Failures
      //    surface as `messages` empty + `error` set; we still set
      //    agentId so the user can chat live.
      final hydrated = await _backfillMessages(client, agentId, sessionId);
      final sinceCursor = _maxSeq(hydrated.events);

      // 4. Commit ready-state (B6 — agentId set AFTER hydration so
      //    the spinner stays up until the panel has something to
      //    show). The error from a failed backfill is non-fatal —
      //    we attach it as a system note so the user sees why the
      //    panel is empty, but proceed with live streaming anyway.
      state = state.copyWith(
        agentId: agentId,
        sessionId: sessionId,
        messages: hydrated.messages,
        clearError: true,
      );
      if (hydrated.warning != null) {
        _appendMessage(OverlayChatMessage(
          role: OverlayChatRole.system,
          text: hydrated.warning!,
          ts: DateTime.now(),
        ));
      }

      // 5. Subscribe to the steward's SSE stream. Both the backfill
      //    (above) and the live stream are filtered to the SAME
      //    session (B3) so the panel doesn't mix current and prior
      //    sessions. mobile.intent events still reach us because
      //    the hub publishes them on the agent bus key with the
      //    current session id.
      _sub?.cancel();
      _sub = client
          .streamAgentEvents(
            agentId,
            sinceSeq: sinceCursor,
            sessionId: sessionId.isEmpty ? null : sessionId,
          )
          .listen(
        _handleEvent,
        onError: (Object e) {
          _appendMessage(OverlayChatMessage(
            role: OverlayChatRole.system,
            text: 'Steward stream errored: $e',
            ts: DateTime.now(),
          ));
          state = state.copyWith(error: 'Stream error: $e');
        },
        onDone: () {
          _appendMessage(OverlayChatMessage(
            role: OverlayChatRole.system,
            text: 'Steward stream closed',
            ts: DateTime.now(),
          ));
        },
      );
    } catch (e) {
      state = state.copyWith(error: 'Bootstrap failed: $e');
    }
  }

  /// W1 backfill — read the recent agent_events history through the
  /// read-through cache (B4) and fold it into `OverlayChatMessage`s.
  /// Returns the messages, the source events (for sinceSeq cursor
  /// computation), and a non-fatal warning if the network fetch
  /// failed so the caller can surface it.
  Future<_BackfillResult> _backfillMessages(
    HubClient client,
    String agentId,
    String sessionId,
  ) async {
    try {
      final cached = await client.listAgentEventsCached(
        agentId,
        tail: true,
        limit: _backfillLimit,
        sessionId: sessionId.isEmpty ? null : sessionId,
      );
      // Tail mode returns seq DESC (newest first). Chat reads
      // oldest → newest, so reverse before hydrating.
      final asc = cached.body.reversed.toList(growable: false);
      final messages = _hydrateFromEvents(asc);
      final warning = cached.staleSince != null
          ? 'Showing cached history (offline)'
          : null;
      return _BackfillResult(
        messages: messages,
        events: asc,
        warning: warning,
      );
    } catch (e) {
      // Network + cache both unavailable; not fatal — let the user
      // chat with live state. The warning line in the transcript
      // explains the empty backfill.
      return _BackfillResult(
        messages: const [],
        events: const [],
        warning: 'Could not load history: $e',
      );
    }
  }

  /// Demuxes a list of agent_events into `OverlayChatMessage`s via
  /// the same `_eventToMessage` folder as the live `_handleEvent`
  /// path — guarantees backfill and live render produce the same
  /// bubble shapes, including past `mobile.intent` events as
  /// past-tense pills (W4 reverse-of-B5: intents are the most
  /// informative directive signal and should appear on replay).
  List<OverlayChatMessage> _hydrateFromEvents(
    List<Map<String, dynamic>> events,
  ) {
    final out = <OverlayChatMessage>[];
    for (final evt in events) {
      final msg = _eventToMessage(evt);
      if (msg != null) out.add(msg);
    }
    return out;
  }

  /// Returns the maximum `seq` across the given events, or null when
  /// the list is empty. Used as the `sinceSeq` cursor for the live
  /// SSE subscription so the hub doesn't replay frames we already
  /// hydrated from cache.
  int? _maxSeq(List<Map<String, dynamic>> events) {
    int? maxSeq;
    for (final e in events) {
      final s = (e['seq'] as num?)?.toInt();
      if (s == null) continue;
      if (maxSeq == null || s > maxSeq) maxSeq = s;
    }
    return maxSeq;
  }

  DateTime? _parseEventTs(Map<String, dynamic> evt) {
    final raw = evt['ts'];
    if (raw is String && raw.isNotEmpty) {
      return DateTime.tryParse(raw);
    }
    return null;
  }

  /// Demultiplex one incoming SSE frame.
  void _handleEvent(Map<String, dynamic> evt) {
    final kind = (evt['kind'] ?? '').toString();
    if (kDebugMode) {
      // ignore: avoid_print
      print('[steward-overlay] evt kind=$kind keys=${evt.keys.toList()}');
    }
    if (kind == 'mobile.intent') {
      // Two paths for live intents: render the message via
      // `_eventToMessage` (so live + replay produce the same bubble
      // shape — this is the W4 reverse-of-B5 decision: past intents
      // ARE the most informative directive signal and should appear
      // on cold-open backfill too) AND fire the live navigation +
      // snackbar via `_dispatchIntentLive`.
      final msg = _eventToMessage(evt);
      if (msg != null) _appendMessage(msg);
      _dispatchIntentLive(evt);
      return;
    }
    final msg = _eventToMessage(evt);
    if (msg != null) _appendMessage(msg);
  }

  /// Folds a single agent_events frame into a chat message, or null
  /// if the kind/producer combination shouldn't render.
  ///
  /// Shared between the live `_handleEvent` path and the W1 cold-open
  /// `_hydrateFromEvents` backfill path so both render the panel
  /// consistently — typing a message during live use and re-opening
  /// the app after a restart produce the same bubble shape.
  ///
  /// W2: `kind == 'input.text'` with `producer == 'user'` renders as
  /// a user bubble (own input). Other producers (a2a, system) are
  /// skipped — the overlay is a directive UI, not a full transcript
  /// (compact-chat axiom).
  OverlayChatMessage? _eventToMessage(Map<String, dynamic> evt) {
    final kind = (evt['kind'] ?? '').toString();
    final producer = (evt['producer'] ?? '').toString();
    final ts = _parseEventTs(evt) ?? DateTime.now();

    if (kind == 'text') {
      final text = _extractText(evt);
      if (text == null || text.isEmpty) return null;
      return OverlayChatMessage(
        role: OverlayChatRole.steward,
        text: text,
        ts: ts,
      );
    }

    if (kind == 'input.text' && producer == 'user') {
      final text = _extractInputText(evt);
      if (text == null || text.isEmpty) return null;
      return OverlayChatMessage(
        role: OverlayChatRole.user,
        text: text,
        ts: ts,
      );
    }

    if (kind == 'mobile.intent') {
      return _intentToMessage(evt, ts);
    }

    return null;
  }

  /// Builds a chat message representing a `mobile.intent` event —
  /// distinct from a free-form steward text reply because the bubble
  /// renders as a compact past-tense pill ("Steward → Insights ·
  /// 14:32") with tap-through to re-fire the URI.
  ///
  /// The verb is currently "→" (arrow-style) because v1 intents are
  /// navigation-only. As the steward gains write capabilities (create,
  /// edit, write artifacts) the event payload will carry an `action`
  /// field; this method should switch on it to emit the right verb.
  /// For now `verb` defaults to "→" so the bubble shape doesn't
  /// require model changes when new actions land.
  OverlayChatMessage? _intentToMessage(
    Map<String, dynamic> evt,
    DateTime ts,
  ) {
    final uriStr = (evt['uri'] ?? '').toString();
    if (uriStr.isEmpty) return null;
    final uri = Uri.tryParse(uriStr);
    if (uri == null) return null;
    final hub = ref.read(hubProvider).value;
    final target = _describeIntentTarget(uri, hub);
    return OverlayChatMessage(
      role: OverlayChatRole.system,
      text: 'Steward → $target',
      note: uriStr,
      ts: ts,
      intentAction: OverlayIntentAction(
        verb: '→',
        target: target,
        uri: uriStr,
      ),
    );
  }

  /// Best-effort human label for a `mobile.intent` URI on replay.
  /// Live intents use `navigateToUri`'s richer `NavigateResult.label`
  /// (which can include filter parameters etc.); on replay we don't
  /// re-evaluate the route since side effects mustn't fire, so we
  /// reconstruct a coarser label from the URI structure. The exact
  /// label can drift if the destination was renamed/deleted between
  /// the event and now — acceptable for a recent-history view.
  String _describeIntentTarget(Uri uri, HubState? hub) {
    final host = uri.host.toLowerCase();
    final segs = uri.pathSegments;
    switch (host) {
      case 'projects':
        return 'Projects';
      case 'activity':
        final filter = uri.queryParameters['filter'];
        return filter == null ? 'Activity' : 'Activity · $filter';
      case 'me':
        return 'Me';
      case 'hosts':
        return 'Hosts';
      case 'settings':
        return 'Settings';
      case 'insights':
        return 'Insights';
      case 'project':
        if (segs.isEmpty) return 'a project';
        // If the hub snapshot has the name, use it; otherwise the id.
        final id = segs[0];
        if (hub != null) {
          for (final p in hub.projects) {
            if ((p['id'] ?? '').toString() == id) {
              final name = (p['name'] ?? '').toString();
              if (name.isNotEmpty) return name;
            }
          }
        }
        return 'project $id';
      case 'session':
        return segs.isEmpty ? 'a session' : 'session ${segs[0]}';
      case 'agent':
        return segs.isEmpty ? 'an agent' : 'agent ${segs[0]}';
      default:
        return uri.toString();
    }
  }

  /// Extracts surface text from a `kind == 'text'` agent_events
  /// frame. Hub publishes the assistant text under `evt['payload']`
  /// (not `evt['body']` — that earlier shape never existed); for
  /// claude-sdk text frames the payload is
  /// `{"text": "...", "message_id": "..."}`. We only render `text`
  /// kinds for the steward role — thoughts/tool_calls/usage frames
  /// are background noise for the overlay's compact chat.
  String? _extractText(Map<String, dynamic> evt) {
    if ((evt['kind'] ?? '').toString() != 'text') return null;
    final payload = evt['payload'];
    if (payload is String) return payload;
    if (payload is Map) {
      final t = payload['text'];
      if (t is String) return t;
    }
    return null;
  }

  /// Extracts the user's typed text from a `kind == 'input.text'`
  /// agent_events frame. The hub's `postAgentInput` handler stores
  /// the body under `payload['body']` (see `handlers_agent_input.go`
  /// — it serializes the request `{kind, body}` into the events
  /// table's `payload_json`). Some older code paths used
  /// `payload['text']` — accept either for forward-compat.
  String? _extractInputText(Map<String, dynamic> evt) {
    if ((evt['kind'] ?? '').toString() != 'input.text') return null;
    final payload = evt['payload'];
    if (payload is String) return payload;
    if (payload is Map) {
      final body = payload['body'];
      if (body is String && body.isNotEmpty) return body;
      final text = payload['text'];
      if (text is String && text.isNotEmpty) return text;
    }
    return null;
  }

  void _appendMessage(OverlayChatMessage msg) {
    final next = List<OverlayChatMessage>.from(state.messages)..add(msg);
    if (next.length > _overlayMessageCap) {
      next.removeRange(0, next.length - _overlayMessageCap);
    }
    state = state.copyWith(messages: next);
  }

  /// Live-only side effects for a `mobile.intent` event: navigate +
  /// snackbar. The chat-message append is the responsibility of
  /// `_eventToMessage` / `_handleEvent` — this method is purely
  /// about the UI dispatch that happens AS the intent fires. On
  /// replay (cold-open backfill) this method is never called; on
  /// live SSE it runs alongside the message append.
  void _dispatchIntentLive(Map<String, dynamic> evt) {
    final uriStr = (evt['uri'] ?? '').toString();
    if (uriStr.isEmpty) return;
    final uri = Uri.tryParse(uriStr);
    if (uri == null) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'mobile.intent uri unparseable: $uriStr',
        ts: DateTime.now(),
      ));
      return;
    }
    final navKey = ref.read(overlayNavigatorKeyProvider);
    final ctx = navKey.currentContext;
    if (ctx == null) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'Navigator not ready; intent dropped',
        note: uriStr,
        ts: DateTime.now(),
      ));
      return;
    }
    final hub = ref.read(hubProvider).value;
    final result = navigateToUri(
      ctx,
      uri,
      hub: hub,
      setTab: (index) =>
          ref.read(currentTabProvider.notifier).setTab(index),
    );
    if (!result.ok) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'Steward could not navigate to $uriStr',
        ts: DateTime.now(),
      ));
      return;
    }
    // Snackbar surfaces the nav even when the chat panel is
    // collapsed. The chat bubble is already appended by
    // `_handleEvent` via `_eventToMessage` — no double-append here.
    final messenger = ScaffoldMessenger.maybeOf(ctx);
    messenger?.hideCurrentSnackBar();
    messenger?.showSnackBar(
      SnackBar(
        duration: const Duration(seconds: 2),
        behavior: SnackBarBehavior.floating,
        content: Text('Steward → ${result.label}'),
      ),
    );
  }

  /// Send the user's text to the general steward.
  ///
  /// **Note (W2 — Option A):** we deliberately do NOT pre-echo the
  /// user's text into the local message list. The hub publishes the
  /// `kind == 'input.text' producer == 'user'` event back to us via
  /// SSE, and `_handleEvent` renders it through the same
  /// `_eventToMessage` folder used for cold-open backfill. Going
  /// through one path means typing during live use vs reopening the
  /// app after a restart produce identical bubble shapes — no
  /// dedup needed, no risk of "live render diverged from replay."
  /// Cost: one SSE round-trip latency before the user's bubble
  /// appears (~100-300 ms typical). If QA flags that as laggy we
  /// can switch to id-based dedup (Option B in the wedge plan).
  Future<void> sendUserText(String text) async {
    final agentId = state.agentId;
    if (agentId == null) {
      throw StateError('Steward not yet ready');
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      throw StateError('Hub not configured');
    }
    await client.postAgentInput(
      agentId,
      kind: 'text',
      body: text,
    );
  }
}

final stewardOverlayControllerProvider =
    NotifierProvider<StewardOverlayController, StewardOverlayState>(
  StewardOverlayController.new,
);

/// Helper used by main.dart to opt the rest of the app into the
/// overlay being mounted at the root navigator. Keeps the wiring
/// out of HomeScreen.
String describeStewardHandle(String? handle) => stewardLabel(handle);

/// Internal value type for `_backfillMessages` — bundles the hydrated
/// chat messages with the source events (so the caller can derive
/// the `sinceSeq` cursor) and an optional warning to surface.
class _BackfillResult {
  final List<OverlayChatMessage> messages;
  final List<Map<String, dynamic>> events;
  final String? warning;
  const _BackfillResult({
    required this.messages,
    required this.events,
    this.warning,
  });
}

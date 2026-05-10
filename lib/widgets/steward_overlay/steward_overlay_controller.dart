import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/agent_events_provider.dart';
import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../screens/home_screen.dart';
import '../../services/deep_link/uri_router.dart';
import '../../services/steward_handle.dart';

/// Max in-memory message rolling window for the overlay's compact
/// chat. The Sessions screen owns the full transcript; the overlay's
/// purpose is the recent directive context, not the entire log.
/// 20 messages ≈ 10 turns of user-prompt → steward-response.
const int _overlayMessageCap = 20;

/// Cap on `_processedIds` so the overlay's dedup set doesn't grow
/// unboundedly across long-lived sessions. The agent_events shared
/// provider trims its window at 200; once an event is trimmed there
/// it will never come back to us, so 300 is a comfortable cushion.
const int _processedIdsCap = 300;

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
  /// Manual subscription handle for the shared agent_events provider.
  /// Set up by `ensureStarted` after the steward agent + session
  /// resolve; closed in dispose.
  ProviderSubscription<AgentEventsState>? _eventsSub;
  bool _initStarted = false;

  /// Event ids we've already folded into `state.messages`. The shared
  /// provider may re-emit its full events list on each state change
  /// (after a reconnect, for example); the dedup set ensures we
  /// process each event exactly once.
  final _processedIds = <String>{};

  /// True after the FIRST non-empty events arrival has been folded.
  /// Used to gate live side effects (snackbar + URI navigation) so
  /// past `mobile.intent` events from the cache-paint don't fire
  /// "as if the steward is navigating now."
  bool _liveDispatchArmed = false;

  /// Last `staleSince` value we surfaced as a system note, so we
  /// don't spam the transcript on every state change while the
  /// connection is unhealthy.
  DateTime? _lastSurfacedStaleSince;

  /// Last error string we surfaced as a system note, with the same
  /// suppression goal as `_lastSurfacedStaleSince`.
  String? _lastSurfacedError;

  @override
  StewardOverlayState build() {
    ref.onDispose(() {
      _eventsSub?.close();
    });
    return const StewardOverlayState();
  }

  /// Lazily ensures the general steward + attaches to the shared
  /// agent_events provider (P1 of agent-events-shared-provider plan).
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

      // 3. Commit agent + session ids. UI's loading spinner uses
      //    `agentId == null` as the "still resolving" gate (B6 from
      //    the original plan); once we set it, the chat surface
      //    transitions to live render. Messages will populate via
      //    the events listener below.
      state = state.copyWith(
        agentId: agentId,
        sessionId: sessionId,
        clearError: true,
      );

      // 4. Attach to the shared agent_events provider for this
      //    (agentId, sessionId). The provider does the cache-only
      //    first paint, the cache-then-refresh backfill, the SSE
      //    subscribe with sinceSeq cursor, and the
      //    reconnect-with-backoff. The overlay is now a pure
      //    consumer — no SSE handling, no backfill HTTP, no
      //    reconnect logic of its own.
      _eventsSub?.close();
      final key = AgentEventsKey(
        agentId,
        sessionId.isEmpty ? null : sessionId,
      );
      _eventsSub = ref.listenManual<AgentEventsState>(
        agentEventsProvider(key),
        _onEventsState,
        fireImmediately: true,
      );
    } catch (e) {
      state = state.copyWith(error: 'Bootstrap failed: $e');
    }
  }

  /// Fold the shared provider's state into the overlay's messages.
  /// Process new events (those not yet in `_processedIds`) through
  /// the same `_eventToMessage` folder used by the cold-open
  /// backfill, append to messages, fire live URI dispatch for new
  /// `mobile.intent` events ONLY after the first event-set has
  /// been processed (so cache-paint replay doesn't navigate as if
  /// the steward is acting right now).
  void _onEventsState(
    AgentEventsState? prev,
    AgentEventsState next,
  ) {
    // Surface provider-level connection issues as transcript notes
    // (matches v1.0.474 behaviour where stream errors and stale
    // cache produced visible system messages — now sourced from
    // the provider's `staleSince` / `error` fields, not duplicated
    // here).
    _surfaceConnectionState(next);

    // Process new events. The shared provider may emit its full
    // events list on each state change; `_processedIds` is the
    // dedup boundary.
    for (final evt in next.events) {
      final id = (evt['id'] ?? '').toString();
      if (id.isEmpty || !_processedIds.add(id)) continue;
      if (_processedIds.length > _processedIdsCap) {
        // Trim oldest — Set has insertion-order in Dart; remove the
        // first entry. Cheap; fires only at the cap boundary.
        _processedIds.remove(_processedIds.first);
      }
      final msg = _eventToMessage(evt);
      if (msg != null) _appendMessage(msg);
      if (_liveDispatchArmed &&
          (evt['kind'] ?? '').toString() == 'mobile.intent') {
        _dispatchIntentLive(evt);
      }
    }
    if (!_liveDispatchArmed && next.events.isNotEmpty) {
      _liveDispatchArmed = true;
    }
  }

  void _surfaceConnectionState(AgentEventsState s) {
    // staleSince — surface once per transition into stale. Do not
    // re-surface on every state change while still stale.
    if (s.staleSince != null && s.staleSince != _lastSurfacedStaleSince) {
      _lastSurfacedStaleSince = s.staleSince;
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'Showing cached history (offline)',
        ts: DateTime.now(),
      ));
    }
    if (s.staleSince == null && _lastSurfacedStaleSince != null) {
      // Connection recovered; reset the latch so a future drop
      // re-surfaces.
      _lastSurfacedStaleSince = null;
    }
    // error — same single-surface contract.
    if (s.error != null && s.error != _lastSurfacedError) {
      _lastSurfacedError = s.error;
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'Steward stream: ${s.error}',
        ts: DateTime.now(),
      ));
    }
    if (s.error == null && _lastSurfacedError != null) {
      _lastSurfacedError = null;
    }
  }

  DateTime? _parseEventTs(Map<String, dynamic> evt) {
    final raw = evt['ts'];
    if (raw is String && raw.isNotEmpty) {
      return DateTime.tryParse(raw);
    }
    return null;
  }

  /// Folds a single agent_events frame into a chat message, or null
  /// if the kind/producer combination shouldn't render.
  ///
  /// Called from `_onEventsState` (the shared agent_events provider
  /// listener) for each new event. Live and cold-open replay use
  /// the same folder so cold-open and live-typing produce identical
  /// bubble shapes.
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
  /// `_eventToMessage` (called by `_onEventsState` for both cold-open
  /// replay and live ingestion). This method is purely about the UI
  /// dispatch that happens AS the intent fires; gated by
  /// `_liveDispatchArmed` so cold-open replay doesn't navigate as
  /// if the steward is acting now.
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
    // `_onEventsState` via `_eventToMessage` — no double-append here.
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
  /// SSE; `_onEventsState` ingests it from the shared agent_events
  /// provider through the same `_eventToMessage` folder used for
  /// cold-open backfill. Going through one path means typing during
  /// live use vs reopening the app after a restart produce identical
  /// bubble shapes — no dedup needed, no risk of "live render
  /// diverged from replay." Cost: one SSE round-trip latency before
  /// the user's bubble appears (~100-300 ms typical). If QA flags
  /// that as laggy we can switch to id-based dedup (Option B).
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

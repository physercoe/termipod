import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../screens/home_screen.dart';
import '../../services/deep_link/uri_router.dart';
import '../../services/steward_handle.dart';

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

@immutable
class OverlayChatMessage {
  final OverlayChatRole role;
  final String text;
  final String? note; // small footnote (e.g. "navigated to X")
  final DateTime ts;
  const OverlayChatMessage({
    required this.role,
    required this.text,
    this.note,
    required this.ts,
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

      state = state.copyWith(
        agentId: agentId,
        sessionId: sessionId,
        clearError: true,
      );

      // 3. Subscribe to the steward's SSE stream. We listen to the
      //    full agent stream (no session filter) so mobile.intent
      //    events — which the hub publishes on the agent bus key
      //    regardless of session — reach us.
      _sub?.cancel();
      _sub = client
          .streamAgentEvents(agentId, sessionId: null)
          .listen(_handleEvent, onError: (e) {
        state = state.copyWith(error: 'Stream error: $e');
      });
    } catch (e) {
      state = state.copyWith(error: 'Bootstrap failed: $e');
    }
  }

  /// Demultiplex one incoming SSE frame.
  void _handleEvent(Map<String, dynamic> evt) {
    final kind = (evt['kind'] ?? '').toString();
    if (kind == 'mobile.intent') {
      _dispatchIntent(evt);
      return;
    }
    // Plain text from steward (claude-sdk text frames). The exact
    // wire shape varies by engine; we look at common payload paths.
    final maybeText = _extractText(evt);
    if (maybeText != null && maybeText.isNotEmpty) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.steward,
        text: maybeText,
        ts: DateTime.now(),
      ));
    }
  }

  String? _extractText(Map<String, dynamic> evt) {
    // claude-sdk emits kind=text with body as a plain string.
    if ((evt['kind'] ?? '') == 'text') {
      final body = evt['body'];
      if (body is String) return body;
    }
    // turn.result events carry no useful surface text for the chat —
    // skip. Tool-call frames likewise don't render here (the user
    // sees the navigation toast instead).
    return null;
  }

  void _appendMessage(OverlayChatMessage msg) {
    final next = List<OverlayChatMessage>.from(state.messages)..add(msg);
    // Keep the rolling window bounded so the overlay doesn't balloon
    // memory in long sessions; the full transcript lives in Sessions.
    const maxKeep = 100;
    if (next.length > maxKeep) {
      next.removeRange(0, next.length - maxKeep);
    }
    state = state.copyWith(messages: next);
  }

  void _dispatchIntent(Map<String, dynamic> evt) {
    final uriStr = (evt['uri'] ?? '').toString();
    if (uriStr.isEmpty) return;
    final uri = Uri.tryParse(uriStr);
    if (uri == null) return;
    final navKey = ref.read(overlayNavigatorKeyProvider);
    final ctx = navKey.currentContext;
    if (ctx == null) return;
    final hub = ref.read(hubProvider).value;
    final result = navigateToUri(
      ctx,
      uri,
      hub: hub,
      setTab: (index) =>
          ref.read(currentTabProvider.notifier).setTab(index),
    );
    if (result.ok) {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'Steward → ${result.label}',
        note: uriStr,
        ts: DateTime.now(),
      ));
      // Also surface a brief snackbar so the user sees the navigation
      // even when the chat panel is collapsed.
      final messenger = ScaffoldMessenger.maybeOf(ctx);
      messenger?.hideCurrentSnackBar();
      messenger?.showSnackBar(
        SnackBar(
          duration: const Duration(seconds: 2),
          behavior: SnackBarBehavior.floating,
          content: Text('Steward → ${result.label}'),
        ),
      );
    } else {
      _appendMessage(OverlayChatMessage(
        role: OverlayChatRole.system,
        text: 'Steward could not navigate to $uriStr',
        ts: DateTime.now(),
      ));
    }
  }

  /// Send the user's text to the general steward.
  Future<void> sendUserText(String text) async {
    final agentId = state.agentId;
    if (agentId == null) {
      throw StateError('Steward not yet ready');
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      throw StateError('Hub not configured');
    }
    _appendMessage(OverlayChatMessage(
      role: OverlayChatRole.user,
      text: text,
      ts: DateTime.now(),
    ));
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

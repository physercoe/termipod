import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/settings_provider.dart';
import '../../providers/voice_settings_provider.dart';
import '../../screens/home_screen.dart';
import '../../screens/sessions/sessions_screen.dart';
import '../../services/voice/cloud_stt.dart';
import '../../services/voice/recording_controller.dart';
import '../../services/voice/voice_recording_session.dart';
import '../../theme/design_colors.dart';
import 'steward_overlay_chat.dart';
import 'steward_overlay_controller.dart';
import 'voice_recording_hud.dart';

/// Steward overlay shell — a persistent, draggable chat surface that
/// stays visible across all routes (Projects / Activity / Me /
/// Hosts / Settings + pushed routes), per
/// `discussions/agent-driven-mobile-ui.md` §4.1.
///
/// Two visual states:
///   - **Puck** (collapsed): a small steward avatar. Tap → expand,
///     drag → reposition.
///   - **Panel** (expanded): a free-floating chat panel. Header is
///     the drag handle (move it anywhere on screen). Bottom-right
///     corner has a resize grip. Both position + size persist via
///     `settings_provider` so the layout survives app restarts.
///
/// Mounted ONCE at the app root via `MaterialApp.builder`. There is
/// only ever one overlay instance regardless of which route is on
/// top of the Navigator stack.
class StewardOverlay extends ConsumerStatefulWidget {
  /// The child is the rest of the app — pages are rendered beneath
  /// the overlay puck. Both compose into a `Stack` whose order is
  /// (child, then overlay) so the overlay paints on top.
  final Widget child;

  const StewardOverlay({super.key, required this.child});

  @override
  ConsumerState<StewardOverlay> createState() => _StewardOverlayState();
}

class _StewardOverlayState extends ConsumerState<StewardOverlay> {
  /// Puck position in screen coordinates. Initialised from settings
  /// (if persisted) or computed defaults on first build.
  Offset? _puckOffset;

  /// Free-floating panel rect (left/top/width/height). Same lifecycle
  /// as `_puckOffset` — null until hydrated from settings or defaults.
  Rect? _panelRect;

  /// True when the user is actively dragging the puck — debounces
  /// the tap-to-expand so a drag-end doesn't accidentally toggle.
  bool _draggedThisGesture = false;

  /// Expanded vs collapsed.
  bool _expanded = false;

  // Mode A — puck long-press voice recording state. Lives on the
  // overlay because the puck is the affordance; HUD is positioned
  // relative to the puck's current offset.
  VoiceRecordingSession? _voiceSession;
  StreamSubscription<VoiceSessionEvent>? _voiceSub;
  String _voiceTranscript = '';
  DateTime? _voiceStartTime;
  Timer? _voiceTickTimer;
  Duration _voiceElapsed = Duration.zero;
  bool _voiceStarting = false;

  static const double _puckSize = 56;
  static const double _margin = 16;
  static const double _minPanelW = 260;
  static const double _minPanelH = 200;
  static const double _resizeHandle = 28;

  /// Compute the default puck offset (bottom-right, above the bottom
  /// nav). Used the first time the overlay mounts on a fresh install.
  Offset _defaultPuckOffset(Size screen) => Offset(
        screen.width - _puckSize - _margin,
        screen.height - _puckSize - _margin - 80,
      );

  /// Compute the default panel rect (anchored bottom, ~55% height,
  /// 12 px side margins) — matches the v1.0.464 prototype layout.
  Rect _defaultPanelRect(Size screen) {
    final width = screen.width - 24;
    final height = (screen.height * 0.55).clamp(_minPanelH, screen.height - 40);
    final left = 12.0;
    final top = screen.height - height - 12 - 24; // small bottom safe margin
    return Rect.fromLTWH(left, top, width, height);
  }

  /// Lazy hydrate from settings (or defaults) the first time we have
  /// real screen constraints. Settings load is async, so the very
  /// first frame may use defaults; once settings populate the
  /// SwitchListTile + Riverpod-watch above will rebuild this widget
  /// and we re-evaluate.
  void _ensureInitial(Size screen, AppSettings settings) {
    if (_puckOffset == null) {
      final px = settings.stewardOverlayPuckX;
      final py = settings.stewardOverlayPuckY;
      _puckOffset = (px != null && py != null)
          ? Offset(px, py)
          : _defaultPuckOffset(screen);
    }
    if (_panelRect == null) {
      final l = settings.stewardOverlayPanelLeft;
      final t = settings.stewardOverlayPanelTop;
      final w = settings.stewardOverlayPanelWidth;
      final h = settings.stewardOverlayPanelHeight;
      _panelRect = (l != null && t != null && w != null && h != null)
          ? Rect.fromLTWH(l, t, w, h)
          : _defaultPanelRect(screen);
    }
    // Clamp to current viewport in case the user rotated or the
    // foldable was unfolded since last save.
    _puckOffset = Offset(
      _puckOffset!.dx.clamp(0.0, (screen.width - _puckSize).clamp(0.0, double.infinity)),
      _puckOffset!.dy.clamp(0.0, (screen.height - _puckSize).clamp(0.0, double.infinity)),
    );
    final pr = _panelRect!;
    final clampedW = pr.width.clamp(_minPanelW, screen.width);
    final clampedH = pr.height.clamp(_minPanelH, screen.height);
    final clampedL = pr.left.clamp(0.0, (screen.width - clampedW).clamp(0.0, double.infinity));
    final clampedT = pr.top.clamp(0.0, (screen.height - clampedH).clamp(0.0, double.infinity));
    _panelRect = Rect.fromLTWH(clampedL, clampedT, clampedW, clampedH);
  }

  void _toggleExpanded() {
    setState(() => _expanded = !_expanded);
  }

  void _collapse() {
    if (_expanded) setState(() => _expanded = false);
  }

  void _persistPuck() {
    final off = _puckOffset;
    if (off == null) return;
    ref.read(settingsProvider.notifier)
        .setStewardOverlayPuckPosition(off.dx, off.dy);
  }

  void _persistPanel() {
    final r = _panelRect;
    if (r == null) return;
    ref.read(settingsProvider.notifier)
        .setStewardOverlayPanelRect(r.left, r.top, r.width, r.height);
  }

  // ===== Mode A — puck long-press voice recording =====

  Future<VoiceRecordingSession?> _buildVoiceSession() async {
    final settings = ref.read(voiceSettingsProvider);
    if (!settings.isReady) return null;
    final apiKey =
        await ref.read(voiceSettingsProvider.notifier).readApiKey();
    if (apiKey == null || apiKey.isEmpty) return null;
    return VoiceRecordingSession(
      recording: RecordingController(),
      cloudStt: AlibabaWebSocketStt(
        apiKey: apiKey,
        region: settings.region,
        model: settings.model,
      ),
      languageHints: settings.languageHints,
    );
  }

  Future<void> _onPuckLongPressStart() async {
    if (_voiceSession != null || _voiceStarting) return;
    setState(() => _voiceStarting = true);
    VoiceRecordingSession? session;
    try {
      session = await _buildVoiceSession();
    } catch (e) {
      _showSnack('Voice unavailable: $e');
    } finally {
      if (mounted) setState(() => _voiceStarting = false);
    }
    if (session == null || !mounted) {
      await session?.dispose();
      return;
    }
    _voiceTranscript = '';
    _voiceStartTime = DateTime.now();
    _voiceElapsed = Duration.zero;
    _voiceSession = session;
    _voiceSub = session.events.listen(_onVoiceEvent);
    _voiceTickTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (!mounted || _voiceStartTime == null) return;
      setState(() => _voiceElapsed = DateTime.now().difference(_voiceStartTime!));
    });
    try {
      await session.start();
      if (mounted) setState(() {});
    } catch (e) {
      _showSnack('Mic unavailable: $e');
      await _cleanupVoiceSession();
    }
  }

  void _onVoiceEvent(VoiceSessionEvent e) {
    if (!mounted) return;
    switch (e.kind) {
      case VoiceSessionEventKind.transcriptUpdated:
        setState(() => _voiceTranscript = e.text);
      case VoiceSessionEventKind.completed:
        final text = e.text.trim();
        final autoSend =
            ref.read(voiceSettingsProvider).autoSendPuckTranscripts;
        if (text.isNotEmpty) {
          if (autoSend) {
            _autoSendTranscript(text);
          } else {
            // Review fallback — open the panel and surface the text in
            // a SnackBar so the tester can re-enter it for now. A
            // first-class pre-fill into the chat input is a v1.0.537+
            // follow-up (needs an injection signal from this state
            // into the chat input controller).
            setState(() => _expanded = true);
            _showSnack('Voice → review: "$text" (panel opened)');
          }
        }
        _cleanupVoiceSession();
      case VoiceSessionEventKind.cancelled:
        _cleanupVoiceSession();
      case VoiceSessionEventKind.maxDurationReached:
        // Auto-stop is called by the session; completed event follows.
        break;
      case VoiceSessionEventKind.error:
        _showSnack('Voice error: ${e.error}');
        _cleanupVoiceSession();
    }
  }

  Future<void> _autoSendTranscript(String text) async {
    try {
      await ref
          .read(stewardOverlayControllerProvider.notifier)
          .sendUserText(text);
      _showSnack('Sent: "${text.length > 60 ? '${text.substring(0, 60)}…' : text}"');
    } catch (e) {
      _showSnack('Send failed: $e');
    }
  }

  Future<void> _onPuckLongPressEnd() async {
    await _voiceSession?.stop();
  }

  void _onPuckLongPressMoveUpdate(LongPressMoveUpdateDetails d) {
    if (d.offsetFromOrigin.distance > 80) {
      _voiceSession?.cancel();
    }
  }

  Future<void> _cleanupVoiceSession() async {
    _voiceTickTimer?.cancel();
    _voiceTickTimer = null;
    await _voiceSub?.cancel();
    _voiceSub = null;
    final s = _voiceSession;
    _voiceSession = null;
    _voiceStartTime = null;
    if (mounted) setState(() {});
    await s?.dispose();
  }

  void _showSnack(String msg) {
    if (!mounted) return;
    final messenger = ScaffoldMessenger.maybeOf(context);
    messenger?.showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  void dispose() {
    _voiceTickTimer?.cancel();
    _voiceSub?.cancel();
    _voiceSession?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // Watch the layout fields so a settings reset (e.g. via a future
    // "Reset overlay layout" button) re-applies without restart.
    final settings = ref.watch(settingsProvider);
    // Keyboard inset — when the IME is up we shift the panel up so the
    // chat input stays visible. Non-persistent (snaps back when IME
    // closes); we don't write to settings or _panelRect.
    final keyboardInset = MediaQuery.of(context).viewInsets.bottom;
    return LayoutBuilder(
      builder: (ctx, constraints) {
        _ensureInitial(constraints.biggest, settings);
        var pr = _panelRect!;
        // Shift up if the IME would cover the panel bottom. We don't
        // squeeze the height — keeping the saved height stable means
        // the chat surface doesn't reflow on every IME open/close.
        if (keyboardInset > 0) {
          final visibleBottom = constraints.maxHeight - keyboardInset;
          if (pr.bottom > visibleBottom) {
            final shift = pr.bottom - visibleBottom + 12;
            final newTop = (pr.top - shift).clamp(0.0, double.infinity);
            pr = Rect.fromLTWH(pr.left, newTop, pr.width, pr.height);
          }
        }
        // Non-modal layout — no barrier, no scrim. The panel coexists
        // with the underlying page so the user can keep tapping bottom
        // nav, scrolling lists, and reading content while the steward
        // chat stays visible. Dismissal is explicit: X in panel header,
        // or tap the puck (which toggles expand/collapse).
        return Stack(
          children: [
            widget.child,
            if (_expanded)
              Positioned(
                left: pr.left,
                top: pr.top,
                width: pr.width,
                height: pr.height,
                child: _ExpandedPanel(
                  onClose: _collapse,
                  panelOpacity: settings.stewardOverlayPanelOpacity,
                  onHeaderDrag: (delta) {
                    setState(() {
                      final next = _panelRect!.translate(delta.dx, delta.dy);
                      final maxL = (constraints.maxWidth - next.width)
                          .clamp(0.0, double.infinity);
                      final maxT = (constraints.maxHeight - next.height)
                          .clamp(0.0, double.infinity);
                      _panelRect = Rect.fromLTWH(
                        next.left.clamp(0.0, maxL),
                        next.top.clamp(0.0, maxT),
                        next.width,
                        next.height,
                      );
                    });
                  },
                  onHeaderDragEnd: _persistPanel,
                  onResize: (delta) {
                    setState(() {
                      final cur = _panelRect!;
                      final nw = (cur.width + delta.dx)
                          .clamp(_minPanelW, constraints.maxWidth - cur.left);
                      final nh = (cur.height + delta.dy).clamp(
                          _minPanelH, constraints.maxHeight - cur.top);
                      _panelRect =
                          Rect.fromLTWH(cur.left, cur.top, nw, nh);
                    });
                  },
                  onResizeEnd: _persistPanel,
                  resizeHandleSize: _resizeHandle,
                ),
              ),
            // Puck is HIDDEN while the panel is expanded. Two reasons:
            //   (1) Hit-testing — the persistent puck floats above the
            //       panel and can overlap chat surface (chips, input,
            //       send button). Stack paints later children on top,
            //       so taps on the input area get eaten by the puck →
            //       panel collapses instead of focusing the TextField,
            //       and IME never attaches. (Root cause for the
            //       v1.0.478 "no system IME" QA report.)
            //   (2) Redundancy — the panel header has its own close X;
            //       the puck adds nothing while expanded.
            // The puck reappears when the panel closes via _collapse().
            if (!_expanded)
              Positioned(
                left: _puckOffset!.dx,
                top: _puckOffset!.dy,
                child: _Puck(
                  size: _puckSize,
                  recording: _voiceSession != null,
                  onTap: () {
                    if (!_draggedThisGesture) _toggleExpanded();
                    _draggedThisGesture = false;
                  },
                  onPanStart: () => _draggedThisGesture = false,
                  onPanUpdate: (delta) {
                    setState(() {
                      _draggedThisGesture = true;
                      final next = (_puckOffset ?? Offset.zero) + delta;
                      final maxX = constraints.maxWidth - _puckSize;
                      final maxY = constraints.maxHeight - _puckSize;
                      _puckOffset = Offset(
                        next.dx.clamp(0.0, maxX),
                        next.dy.clamp(0.0, maxY),
                      );
                    });
                  },
                  onPanEnd: _persistPuck,
                  onLongPressStart: _onPuckLongPressStart,
                  onLongPressEnd: _onPuckLongPressEnd,
                  onLongPressMoveUpdate: _onPuckLongPressMoveUpdate,
                ),
              ),
            if (_voiceSession != null && !_expanded)
              _positionedRecordingHud(constraints.biggest),
          ],
        );
      },
    );
  }

  /// Positions the recording HUD relative to the puck, flipping above
  /// or below depending on which side has more room. The v1.0.540 HUD
  /// is bigger (~340 × 175) so the placement offsets accommodate it.
  Widget _positionedRecordingHud(Size screen) {
    final puck = _puckOffset ?? Offset.zero;
    const hudWidth = 340.0;
    const hudHeight = 175.0;
    const hudGap = 12.0;
    final placeAbove = puck.dy > hudHeight + 24;
    final hudTop = placeAbove
        ? (puck.dy - hudHeight - hudGap)
        : (puck.dy + _puckSize + hudGap);
    var hudLeft = puck.dx + _puckSize / 2 - hudWidth / 2;
    hudLeft = hudLeft.clamp(
        8.0, (screen.width - hudWidth - 8).clamp(8.0, double.infinity));
    return Positioned(
      left: hudLeft,
      top: hudTop.clamp(
          8.0, (screen.height - hudHeight - 8).clamp(8.0, double.infinity)),
      child: IgnorePointer(
        // Don't let the HUD intercept the puck long-press gesture.
        child: VoiceRecordingHud(
          transcript: _voiceTranscript,
          elapsed: _voiceElapsed,
        ),
      ),
    );
  }
}

/// The collapsed puck — small circular avatar with the steward's
/// initial. Tap toggles expansion; drag relocates; long-press starts a
/// Mode A voice recording (Path C plan §Mode A).
class _Puck extends StatelessWidget {
  final double size;
  final VoidCallback onTap;
  final VoidCallback onPanStart;
  final ValueChanged<Offset> onPanUpdate;
  final VoidCallback onPanEnd;
  final VoidCallback? onLongPressStart;
  final VoidCallback? onLongPressEnd;
  final ValueChanged<LongPressMoveUpdateDetails>? onLongPressMoveUpdate;

  /// When non-null, paints a red ring around the puck to signal Mode A
  /// recording is in progress.
  final bool recording;

  const _Puck({
    required this.size,
    required this.onTap,
    required this.onPanStart,
    required this.onPanUpdate,
    required this.onPanEnd,
    this.onLongPressStart,
    this.onLongPressEnd,
    this.onLongPressMoveUpdate,
    this.recording = false,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      onPanStart: (_) => onPanStart(),
      onPanUpdate: (d) => onPanUpdate(d.delta),
      onPanEnd: (_) => onPanEnd(),
      onLongPressStart:
          onLongPressStart == null ? null : (_) => onLongPressStart!(),
      onLongPressEnd:
          onLongPressEnd == null ? null : (_) => onLongPressEnd!(),
      onLongPressMoveUpdate: onLongPressMoveUpdate,
      child: Container(
        width: size,
        height: size,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          border: recording
              ? Border.all(color: DesignColors.error, width: 3)
              : null,
        ),
        child: Material(
          elevation: 6,
          shape: const CircleBorder(),
          color: DesignColors.primary,
          child: Center(
            child: Icon(
              recording ? Icons.mic : Icons.support_agent_outlined,
              color: isDark ? Colors.white : Colors.white,
              size: 28,
            ),
          ),
        ),
      ),
    );
  }
}

/// The expanded panel — free-floating chat surface. Header is the
/// drag handle, bottom-right corner is the resize grip.
class _ExpandedPanel extends StatelessWidget {
  final VoidCallback onClose;
  final ValueChanged<Offset> onHeaderDrag;
  final VoidCallback onHeaderDragEnd;
  final ValueChanged<Offset> onResize;
  final VoidCallback onResizeEnd;
  final double resizeHandleSize;
  final double panelOpacity;

  const _ExpandedPanel({
    required this.onClose,
    required this.onHeaderDrag,
    required this.onHeaderDragEnd,
    required this.onResize,
    required this.onResizeEnd,
    required this.resizeHandleSize,
    required this.panelOpacity,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final base = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    // Opacity applies to the BACKGROUND ONLY (not the children) so the
    // chat text stays fully readable while the underlying page peeks
    // through the panel surface. Wrapping in Opacity() instead would
    // fade messages too — wrong for a chat surface.
    return Stack(
      children: [
        Positioned.fill(
          child: Container(
            decoration: BoxDecoration(
              color: base.withValues(alpha: panelOpacity.clamp(0.5, 1.0)),
              borderRadius: BorderRadius.circular(16),
              border: Border.all(
                color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
              ),
              boxShadow: [
                BoxShadow(
                  color: Colors.black.withValues(alpha: 0.18),
                  blurRadius: 18,
                  offset: const Offset(0, 4),
                ),
              ],
            ),
            // The Material ancestor here is what stops Flutter from
            // drawing yellow "missing Material" double underlines under
            // every Text inside the panel. The overlay is mounted via
            // `MaterialApp.builder` and lives OUTSIDE the Navigator's
            // Material/Scaffold scope, so descendant Text widgets would
            // otherwise have no DefaultTextStyle ancestor. `transparency`
            // means it doesn't paint anything itself — the parent
            // Container still owns the colour, border, and shadow.
            child: Material(
              type: MaterialType.transparency,
              child: Column(
                children: [
                  _PanelHeader(
                    onClose: onClose,
                    onDrag: onHeaderDrag,
                    onDragEnd: onHeaderDragEnd,
                  ),
                  const Divider(height: 1),
                  Expanded(
                      child: StewardOverlayChat(onCloseRequested: onClose)),
                ],
              ),
            ),
          ),
        ),
        // Bottom-right resize grip — overlaid above the panel border
        // so it's visible regardless of the header / chat content.
        Positioned(
          right: 0,
          bottom: 0,
          width: resizeHandleSize,
          height: resizeHandleSize,
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onPanUpdate: (d) => onResize(d.delta),
            onPanEnd: (_) => onResizeEnd(),
            child: MouseRegion(
              cursor: SystemMouseCursors.resizeDownRight,
              child: Container(
                margin: const EdgeInsets.all(4),
                decoration: BoxDecoration(
                  color: (isDark ? Colors.white : Colors.black)
                      .withValues(alpha: 0.08),
                  borderRadius: const BorderRadius.only(
                    bottomRight: Radius.circular(14),
                    topLeft: Radius.circular(8),
                  ),
                ),
                child: Icon(
                  Icons.south_east,
                  size: 16,
                  color: isDark
                      ? Colors.white70
                      : DesignColors.textPrimary.withValues(alpha: 0.6),
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

class _PanelHeader extends ConsumerWidget {
  final VoidCallback onClose;
  final ValueChanged<Offset> onDrag;
  final VoidCallback onDragEnd;
  const _PanelHeader({
    required this.onClose,
    required this.onDrag,
    required this.onDragEnd,
  });

  /// Counts pending attention items raised by the steward agent.
  /// Filters by `agent_id` matching the steward's current agentId
  /// AND status either 'open' or 'pending' (the hub uses both
  /// across kinds). Empty list when no steward yet, when there are
  /// no pending items, or before hub bootstrap.
  int _stewardAttentionCount(WidgetRef ref) {
    final overlay = ref.watch(stewardOverlayControllerProvider);
    final agentId = overlay.agentId;
    if (agentId == null) return 0;
    final hub = ref.watch(hubProvider).value;
    if (hub == null) return 0;
    var n = 0;
    for (final a in hub.attention) {
      if ((a['agent_id'] ?? '').toString() != agentId) continue;
      final s = (a['status'] ?? '').toString();
      if (s == 'open' || s == 'pending') n++;
    }
    return n;
  }

  void _openFullSession(BuildContext context, WidgetRef ref) {
    final overlay = ref.read(stewardOverlayControllerProvider);
    final agentId = overlay.agentId;
    final sessionId = overlay.sessionId;
    if (agentId == null || sessionId.isEmpty) return;
    // The panel's BuildContext sits OUTSIDE the inner Navigator
    // (overlay is mounted via MaterialApp.builder, which wraps the
    // Navigator widget). Navigator.of(context) from here either
    // misses the inner Navigator entirely or resolves to the wrong
    // one. Use the shared overlayNavigatorKeyProvider — same key
    // MaterialApp.navigatorKey was overridden with — which is the
    // pattern the live mobile.intent dispatch path already uses.
    final navState = ref.read(overlayNavigatorKeyProvider).currentState;
    if (navState == null) return;
    navState.push(
      MaterialPageRoute(
        builder: (_) => SessionChatScreen(
          sessionId: sessionId,
          agentId: agentId,
          title: 'Steward',
        ),
      ),
    );
    onClose();
  }

  void _openAttention(BuildContext context, WidgetRef ref) {
    // Switch to the Me tab where attention items live, then collapse
    // the overlay so the user can see them. Doesn't filter to the
    // steward — keeps the navigation predictable; the user can scan
    // the list themselves.
    ref.read(currentTabProvider.notifier).setTab(2);
    onClose();
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final attentionN = _stewardAttentionCount(ref);
    final overlayState = ref.watch(stewardOverlayControllerProvider);
    final canOpenFullSession = overlayState.agentId != null &&
        overlayState.sessionId.isNotEmpty;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onPanUpdate: (d) => onDrag(d.delta),
      onPanEnd: (_) => onDragEnd(),
      child: MouseRegion(
        cursor: SystemMouseCursors.move,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 10, 6, 10),
          child: Row(
            children: [
              Icon(
                Icons.drag_indicator,
                size: 16,
                color: DesignColors.primary.withValues(alpha: 0.7),
              ),
              const SizedBox(width: 6),
              const Icon(
                Icons.support_agent_outlined,
                size: 18,
                color: DesignColors.primary,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  'Steward',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              if (attentionN > 0)
                _AttentionBadge(
                  count: attentionN,
                  onTap: () => _openAttention(context, ref),
                ),
              IconButton(
                tooltip: canOpenFullSession
                    ? 'Open full session'
                    : 'Steward not ready',
                iconSize: 18,
                icon: const Icon(Icons.open_in_new),
                onPressed: canOpenFullSession
                    ? () => _openFullSession(context, ref)
                    : null,
              ),
              IconButton(
                tooltip: 'Close',
                iconSize: 20,
                icon: const Icon(Icons.close),
                onPressed: onClose,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Compact attention pill in the panel header. Shows when the
/// steward has raised one or more pending attention items; tap
/// jumps to the Me tab so the user can see them in context.
/// Stays out of the main chat surface (W4 — overlay is the recent
/// directive context, NOT a notification queue).
class _AttentionBadge extends StatelessWidget {
  final int count;
  final VoidCallback onTap;
  const _AttentionBadge({required this.count, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(right: 4),
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: onTap,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            decoration: BoxDecoration(
              color: DesignColors.error.withValues(alpha: 0.15),
              borderRadius: BorderRadius.circular(12),
              border: Border.all(
                color: DesignColors.error.withValues(alpha: 0.45),
                width: 1,
              ),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.notifications_active_outlined,
                  size: 12,
                  color: DesignColors.error,
                ),
                const SizedBox(width: 4),
                Text(
                  '$count',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                    color: DesignColors.error,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

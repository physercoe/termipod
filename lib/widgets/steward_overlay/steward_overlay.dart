import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/settings_provider.dart';
import '../../theme/design_colors.dart';
import 'steward_overlay_chat.dart';

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

  @override
  Widget build(BuildContext context) {
    // Watch the layout fields so a settings reset (e.g. via a future
    // "Reset overlay layout" button) re-applies without restart.
    final settings = ref.watch(settingsProvider);
    return LayoutBuilder(
      builder: (ctx, constraints) {
        _ensureInitial(constraints.biggest, settings);
        final pr = _panelRect!;
        return Stack(
          children: [
            widget.child,
            if (_expanded)
              Positioned.fill(
                child: GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: _collapse,
                  child: Container(
                    color: Colors.black.withValues(alpha: 0.18),
                  ),
                ),
              ),
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
            Positioned(
              left: _puckOffset!.dx,
              top: _puckOffset!.dy,
              child: _Puck(
                size: _puckSize,
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
              ),
            ),
          ],
        );
      },
    );
  }
}

/// The collapsed puck — small circular avatar with the steward's
/// initial. Tap toggles expansion; drag relocates.
class _Puck extends StatelessWidget {
  final double size;
  final VoidCallback onTap;
  final VoidCallback onPanStart;
  final ValueChanged<Offset> onPanUpdate;
  final VoidCallback onPanEnd;

  const _Puck({
    required this.size,
    required this.onTap,
    required this.onPanStart,
    required this.onPanUpdate,
    required this.onPanEnd,
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
      child: Material(
        elevation: 6,
        shape: const CircleBorder(),
        color: DesignColors.primary,
        child: SizedBox(
          width: size,
          height: size,
          child: Center(
            child: Icon(
              Icons.support_agent_outlined,
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
            child: Column(
              children: [
                _PanelHeader(
                  onClose: onClose,
                  onDrag: onHeaderDrag,
                  onDragEnd: onHeaderDragEnd,
                ),
                const Divider(height: 1),
                Expanded(child: StewardOverlayChat(onCloseRequested: onClose)),
              ],
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

class _PanelHeader extends StatelessWidget {
  final VoidCallback onClose;
  final ValueChanged<Offset> onDrag;
  final VoidCallback onDragEnd;
  const _PanelHeader({
    required this.onClose,
    required this.onDrag,
    required this.onDragEnd,
  });

  @override
  Widget build(BuildContext context) {
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

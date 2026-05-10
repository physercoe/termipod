import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';
import 'steward_overlay_chat.dart';

/// SteWard overlay shell — a persistent, draggable chat surface that
/// stays visible across all routes (Projects / Activity / Me /
/// Hosts / Settings + pushed routes), per
/// `discussions/agent-driven-mobile-ui.md` §4.1.
///
/// Two visual states:
///   - **Puck** (collapsed): a small steward avatar. Tap → expand.
///   - **Panel** (expanded): a half-height chat panel with the
///     general steward's transcript + input. Tap close → collapse.
///
/// The puck is draggable; its position is stored in this widget's
/// State so it survives route pushes/pops. Across app restarts the
/// position resets to the bottom-right corner — persistence to
/// shared_preferences is a follow-up if users complain.
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
  /// Puck position in screen coordinates. Initialised lazily on
  /// first build once we know the screen size — defaults to the
  /// bottom-right with a comfortable margin.
  Offset? _puckOffset;

  /// Expanded vs collapsed.
  bool _expanded = false;

  /// Drag tracking — when the user drags the puck we update
  /// `_puckOffset` continuously. Tapping (no drag) toggles expand.
  bool _draggedThisGesture = false;

  static const double _puckSize = 56;
  static const double _margin = 16;

  void _ensureInitialPosition(Size screen) {
    if (_puckOffset != null) return;
    _puckOffset = Offset(
      screen.width - _puckSize - _margin,
      screen.height - _puckSize - _margin - 80, // above the bottom nav
    );
  }

  void _toggleExpanded() {
    setState(() => _expanded = !_expanded);
  }

  void _collapse() {
    if (_expanded) setState(() => _expanded = false);
  }

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (ctx, constraints) {
        _ensureInitialPosition(constraints.biggest);
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
                left: 12,
                right: 12,
                bottom: 12,
                child: SafeArea(
                  child: _ExpandedPanel(onClose: _collapse),
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

  const _Puck({
    required this.size,
    required this.onTap,
    required this.onPanStart,
    required this.onPanUpdate,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      onPanStart: (_) => onPanStart(),
      onPanUpdate: (d) => onPanUpdate(d.delta),
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

/// The expanded panel — half-height chat surface anchored at the
/// bottom of the screen. Holds the transcript + input.
class _ExpandedPanel extends StatelessWidget {
  final VoidCallback onClose;
  const _ExpandedPanel({required this.onClose});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final media = MediaQuery.of(context);
    final maxHeight = media.size.height * 0.55;
    return Container(
      height: maxHeight,
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
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
          _PanelHeader(onClose: onClose),
          const Divider(height: 1),
          Expanded(child: StewardOverlayChat(onCloseRequested: onClose)),
        ],
      ),
    );
  }
}

class _PanelHeader extends StatelessWidget {
  final VoidCallback onClose;
  const _PanelHeader({required this.onClose});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 10, 6, 10),
      child: Row(
        children: [
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
    );
  }
}

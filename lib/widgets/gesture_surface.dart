import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/design_colors.dart';

/// Full-screen gesture overlay for terminal area.
///
/// When active, intercepts all touch events on the terminal and maps them
/// to terminal keys:
/// - Swipe L/R/U/D → arrow keys
/// - Double-tap → Tab
/// - Two-finger tap → Enter
/// - Long-press → paste from clipboard
/// - Three-finger tap → Esc
/// - Pinch → zoom (passed through)
class GestureSurface extends StatefulWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final void Function(String text) onPaste;
  final VoidCallback onDeactivate;
  final bool haptic;

  const GestureSurface({
    super.key,
    required this.onSpecialKeyPressed,
    required this.onPaste,
    required this.onDeactivate,
    this.haptic = true,
  });

  @override
  State<GestureSurface> createState() => _GestureSurfaceState();
}

class _GestureSurfaceState extends State<GestureSurface>
    with SingleTickerProviderStateMixin {
  // Swipe detection
  Offset? _panStart;
  static const _swipeThreshold = 30.0;

  // Double-tap detection
  DateTime? _lastTapTime;
  static const _doubleTapWindow = Duration(milliseconds: 300);

  // Multi-finger detection
  int _pointerCount = 0;
  bool _multiFingerHandled = false;

  // Border pulse animation
  late final AnimationController _pulseController;
  late final Animation<double> _pulseAnimation;

  @override
  void initState() {
    super.initState();
    _pulseController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1500),
    )..repeat(reverse: true);
    _pulseAnimation = Tween<double>(begin: 0.3, end: 0.8).animate(
      CurvedAnimation(parent: _pulseController, curve: Curves.easeInOut),
    );
  }

  @override
  void dispose() {
    _pulseController.dispose();
    super.dispose();
  }

  void _onPanStart(DragStartDetails details) {
    _panStart = details.localPosition;
  }

  void _onPanUpdate(DragUpdateDetails details) {
    if (_panStart == null) return;
    final delta = details.localPosition - _panStart!;

    if (delta.distance < _swipeThreshold) return;

    // Determine direction
    String direction;
    if (delta.dx.abs() > delta.dy.abs()) {
      direction = delta.dx > 0 ? 'Right' : 'Left';
    } else {
      direction = delta.dy > 0 ? 'Down' : 'Up';
    }

    widget.onSpecialKeyPressed(direction);
    if (widget.haptic) HapticFeedback.lightImpact();

    // Reset start point for continuous swiping
    _panStart = details.localPosition;
  }

  void _onPanEnd(DragEndDetails _) {
    _panStart = null;
  }

  void _onTap() {
    final now = DateTime.now();
    if (_lastTapTime != null &&
        now.difference(_lastTapTime!) < _doubleTapWindow) {
      // Double-tap → Tab
      widget.onSpecialKeyPressed('Tab');
      if (widget.haptic) HapticFeedback.lightImpact();
      _lastTapTime = null;
    } else {
      _lastTapTime = now;
    }
  }

  void _onLongPress() async {
    // Long-press → paste from clipboard
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    if (data?.text != null && data!.text!.isNotEmpty) {
      widget.onPaste(data.text!);
      if (widget.haptic) HapticFeedback.mediumImpact();
    }
  }

  void _handlePointerDown(PointerDownEvent _) {
    _pointerCount++;
    _multiFingerHandled = false;
  }

  void _handlePointerUp(PointerUpEvent _) {
    // Check multi-finger taps on release
    if (!_multiFingerHandled) {
      if (_pointerCount == 2) {
        // Two-finger tap → Enter
        widget.onSpecialKeyPressed('Enter');
        if (widget.haptic) HapticFeedback.lightImpact();
        _multiFingerHandled = true;
      } else if (_pointerCount >= 3) {
        // Three-finger tap → Esc
        widget.onSpecialKeyPressed('Escape');
        if (widget.haptic) HapticFeedback.mediumImpact();
        _multiFingerHandled = true;
      }
    }
    _pointerCount--;
    if (_pointerCount < 0) _pointerCount = 0;
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _pulseAnimation,
      builder: (context, child) {
        return Container(
          decoration: BoxDecoration(
            border: Border.all(
              color: DesignColors.primary.withValues(alpha: _pulseAnimation.value),
              width: 3,
            ),
          ),
          child: child,
        );
      },
      child: Listener(
        onPointerDown: _handlePointerDown,
        onPointerUp: _handlePointerUp,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: _onTap,
          onLongPress: _onLongPress,
          onPanStart: _onPanStart,
          onPanUpdate: _onPanUpdate,
          onPanEnd: _onPanEnd,
          child: Stack(
            children: [
              // "GESTURE" badge
              Positioned(
                top: 8,
                left: 8,
                child: Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                  decoration: BoxDecoration(
                    color: DesignColors.primary.withValues(alpha: 0.2),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(
                      color: DesignColors.primary.withValues(alpha: 0.5),
                    ),
                  ),
                  child: Text(
                    'GESTURE',
                    style: TextStyle(
                      color: DesignColors.primary,
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 1,
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

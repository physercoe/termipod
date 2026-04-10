import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/design_colors.dart';

/// Floating draggable D-pad overlay for terminal navigation.
///
/// Tap outer quadrants for arrow keys, tap center for the configured center
/// key (default Enter). Long-press for auto-repeat. Drag to reposition.
/// Designed for large screens (foldable/tablet) and Claude Code approve flow.
class FloatingJoystick extends StatefulWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final bool haptic;
  final int repeatRate;
  /// Outer radius in logical pixels. Diameter = 2 * size.
  final double size;
  /// Tmux key name sent when the center zone is tapped.
  final String centerKey;

  const FloatingJoystick({
    super.key,
    required this.onSpecialKeyPressed,
    this.haptic = true,
    this.repeatRate = 80,
    this.size = 64.0,
    this.centerKey = 'Enter',
  });

  @override
  State<FloatingJoystick> createState() => _FloatingJoystickState();
}

class _FloatingJoystickState extends State<FloatingJoystick> {
  double _right = 16;
  // Vertical position is computed lazily on first build (MediaQuery isn't
  // available in initState). The default sits just above the action bar
  // stack (nav pad + action bar + compose bar ≈ 170dp) so the joystick
  // lands directly under the user's right thumb in natural phone grip —
  // easier to reach than the old screen-vertical-middle default.
  double? _bottom;

  // Center zone radius is derived proportionally from the outer radius so the
  // center stays a reasonable touch target at all sizes.
  double get _outerRadius => widget.size;
  double get _centerRadius => widget.size * 0.375;
  // Minimum drag distance (px) before treating gesture as reposition.
  static const _dragThreshold = 16.0;

  String? _activeZone;
  Timer? _repeatTimer;
  // Tap flash: after a successful tap we keep the zone highlighted for a
  // brief moment and pulse a confirmation ring, so the user can see that
  // the press registered. Without this the highlight disappears within a
  // frame of touch-up and feels like nothing happened.
  Timer? _flashTimer;
  bool _flashActive = false;

  // Gesture tracking
  Offset _touchStartGlobal = Offset.zero;
  double _startRight = 0;
  double _startBottom = 0;
  double _totalDrag = 0;
  bool _isRepositioning = false;
  bool _hasFired = false; // Whether we've sent a key for this gesture

  /// Returns the tmux key name for the zone under [localPosition], or null
  /// if the touch is outside the circle.
  String? _hitTest(Offset localPosition) {
    final center = Offset(_outerRadius, _outerRadius);
    final offset = localPosition - center;
    final distance = offset.distance;

    if (distance <= _centerRadius) return widget.centerKey;
    if (distance > _outerRadius) return null;

    final angle = math.atan2(offset.dy, offset.dx);
    if (angle > -math.pi / 4 && angle <= math.pi / 4) return 'Right';
    if (angle > math.pi / 4 && angle <= 3 * math.pi / 4) return 'Down';
    if (angle > -3 * math.pi / 4 && angle <= -math.pi / 4) return 'Up';
    return 'Left';
  }

  void _onTapDown(TapDownDetails details) {
    // Highlight the zone under the finger on tap down
    final zone = _hitTest(details.localPosition);
    setState(() => _activeZone = zone);
  }

  void _onTap() {
    // Fire the key for the last highlighted zone
    final zone = _activeZone;
    if (zone != null) {
      widget.onSpecialKeyPressed(zone);
      // Stronger haptic so the confirmation is unmistakable.
      if (widget.haptic) HapticFeedback.mediumImpact();

      // Hold the highlight for ~220ms so the user gets a clear visual flash.
      // The flash flag asks the painter to draw an intensified highlight +
      // confirmation ring during this window.
      _flashTimer?.cancel();
      setState(() => _flashActive = true);
      _flashTimer = Timer(const Duration(milliseconds: 220), () {
        if (!mounted) return;
        setState(() {
          _activeZone = null;
          _flashActive = false;
        });
      });
      return;
    }
    setState(() => _activeZone = null);
  }

  void _onTapCancel() {
    _flashTimer?.cancel();
    setState(() {
      _activeZone = null;
      _flashActive = false;
    });
  }

  void _onPanStart(DragStartDetails details) {
    _touchStartGlobal = details.globalPosition;
    _startRight = _right;
    // build() seeds `_bottom` before any gesture can occur, so the bang is safe.
    _startBottom = _bottom ?? 0;
    _totalDrag = 0;
    _isRepositioning = false;
    _hasFired = false;

    // A new gesture cancels any in-flight tap flash.
    _flashTimer?.cancel();
    _flashActive = false;

    // Immediately show which zone is under the finger
    final zone = _hitTest(details.localPosition);
    setState(() => _activeZone = zone);
  }

  void _onPanUpdate(DragUpdateDetails details) {
    _totalDrag += details.delta.distance;

    if (_totalDrag > _dragThreshold && !_hasFired) {
      // Crossed drag threshold without having fired a key — reposition mode
      _isRepositioning = true;
      _stopRepeat();
      setState(() => _activeZone = null);
    }

    if (_isRepositioning) {
      final delta = details.globalPosition - _touchStartGlobal;
      setState(() {
        _right = (_startRight - delta.dx).clamp(0.0, 2000.0);
        _bottom = (_startBottom - delta.dy).clamp(0.0, 2000.0);
      });
    }
  }

  void _onPanEnd(DragEndDetails _) {
    // Pure taps are handled by onTap — pan only reaches here after movement,
    // which means the gesture was a reposition or a long-press drag.
    _stopRepeat();
    _isRepositioning = false;
    setState(() => _activeZone = null);
  }

  void _onLongPress(Offset localPosition) {
    if (_isRepositioning) return;
    final zone = _hitTest(localPosition);
    if (zone == null) return;

    // Fire immediately + start repeat
    _hasFired = true;
    widget.onSpecialKeyPressed(zone);
    if (widget.haptic) HapticFeedback.lightImpact();

    _repeatTimer = Timer.periodic(
      Duration(milliseconds: widget.repeatRate),
      (_) {
        if (widget.haptic) HapticFeedback.selectionClick();
        widget.onSpecialKeyPressed(zone);
      },
    );
  }

  void _stopRepeat() {
    _repeatTimer?.cancel();
    _repeatTimer = null;
  }

  /// Abbreviated label shown inside the center zone.
  static String _centerLabel(String tmuxKey) {
    switch (tmuxKey) {
      case 'Enter':
        return 'ENT';
      case 'Escape':
        return 'ESC';
      case 'Tab':
        return 'TAB';
      case 'BSpace':
        return 'BS';
      case 'Space':
        return 'SPC';
    }
    if (tmuxKey.startsWith('C-') && tmuxKey.length == 3) {
      return '^${tmuxKey[2].toUpperCase()}';
    }
    // Truncate anything longer than 3 chars to keep the glyph inside the dot.
    return tmuxKey.length <= 3 ? tmuxKey.toUpperCase() : tmuxKey.substring(0, 3).toUpperCase();
  }

  @override
  void dispose() {
    _flashTimer?.cancel();
    _stopRepeat();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    // Lazily position the joystick on first build. Place it just above
    // the action bar stack so it sits under the right thumb at rest —
    // ~180dp from the bottom leaves ~20dp of breathing room above the
    // action bar while still being well within thumb reach.
    _bottom ??= 180.0;

    return Positioned(
      right: _right,
      bottom: _bottom,
      child: GestureDetector(
        onTapDown: _onTapDown,
        onTap: _onTap,
        onTapCancel: _onTapCancel,
        onPanStart: _onPanStart,
        onPanUpdate: _onPanUpdate,
        onPanEnd: _onPanEnd,
        onLongPressStart: (details) => _onLongPress(details.localPosition),
        onLongPressEnd: (_) {
          _stopRepeat();
          setState(() => _activeZone = null);
        },
        child: SizedBox(
          width: _outerRadius * 2,
          height: _outerRadius * 2,
          child: CustomPaint(
            painter: _DpadPainter(
              isDark: isDark,
              activeZone: _activeZone,
              centerKey: widget.centerKey,
              centerRadius: _centerRadius,
              flashActive: _flashActive,
            ),
            child: Center(
              child: Text(
                _centerLabel(widget.centerKey),
                style: TextStyle(
                  fontSize: widget.size * 0.18,
                  fontWeight: FontWeight.w700,
                  color: (_flashActive && _activeZone == widget.centerKey)
                      ? DesignColors.primary
                      : (isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight)
                          .withValues(
                              alpha: _activeZone == widget.centerKey ? 0.85 : 0.45),
                  letterSpacing: 0.5,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _DpadPainter extends CustomPainter {
  final bool isDark;
  final String? activeZone;
  final String centerKey;
  final double centerRadius;
  final bool flashActive;

  _DpadPainter({
    required this.isDark,
    required this.activeZone,
    required this.centerKey,
    required this.centerRadius,
    this.flashActive = false,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final center = Offset(size.width / 2, size.height / 2);
    final outerR = size.width / 2;
    final centerR = centerRadius;

    final bgColor = (isDark ? DesignColors.keyBackground : DesignColors.keyBackgroundLight)
        .withValues(alpha: 0.85);
    final borderColor = (isDark ? DesignColors.borderDark : DesignColors.borderLight)
        .withValues(alpha: 0.6);
    final textColor = isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;

    // Background circle
    canvas.drawCircle(center, outerR, Paint()..color = bgColor);

    // Highlight active zone.
    // During the post-tap flash window we intensify the fill, add a bright
    // rim on the zone boundary, and draw a confirmation ring around the
    // whole pad so the tap feels unmistakable.
    if (activeZone != null) {
      final fillAlpha = flashActive ? 0.55 : 0.25;
      final highlightColor = DesignColors.primary.withValues(alpha: fillAlpha);
      if (activeZone == centerKey) {
        canvas.drawCircle(center, centerR, Paint()..color = highlightColor);
        if (flashActive) {
          canvas.drawCircle(
            center,
            centerR,
            Paint()
              ..color = DesignColors.primary.withValues(alpha: 0.9)
              ..style = PaintingStyle.stroke
              ..strokeWidth = 2.0,
          );
        }
      } else {
        final startAngle = switch (activeZone) {
          'Right' => -math.pi / 4,
          'Down' => math.pi / 4,
          'Left' => 3 * math.pi / 4,
          'Up' => -3 * math.pi / 4,
          _ => 0.0,
        };
        final path = Path()
          ..moveTo(center.dx, center.dy)
          ..arcTo(
            Rect.fromCircle(center: center, radius: outerR),
            startAngle,
            math.pi / 2,
            false,
          )
          ..close();
        canvas.drawPath(path, Paint()..color = highlightColor);
        if (flashActive) {
          // Bright outer arc on the pressed quadrant.
          final arcRect = Rect.fromCircle(center: center, radius: outerR - 2);
          canvas.drawArc(
            arcRect,
            startAngle,
            math.pi / 2,
            false,
            Paint()
              ..color = DesignColors.primary.withValues(alpha: 0.9)
              ..style = PaintingStyle.stroke
              ..strokeWidth = 2.5
              ..strokeCap = StrokeCap.round,
          );
        }
      }
    }

    // Confirmation ring — only during the tap flash window.
    if (flashActive) {
      canvas.drawCircle(
        center,
        outerR - 1,
        Paint()
          ..color = DesignColors.primary.withValues(alpha: 0.75)
          ..style = PaintingStyle.stroke
          ..strokeWidth = 2.0,
      );
    }

    // Outer ring
    canvas.drawCircle(
      center,
      outerR - 1,
      Paint()
        ..color = borderColor
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.5,
    );

    // Center circle border
    canvas.drawCircle(
      center,
      centerR,
      Paint()
        ..color = borderColor.withValues(alpha: 0.4)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1,
    );

    // Divider lines between quadrants
    final dividerPaint = Paint()
      ..color = borderColor.withValues(alpha: 0.3)
      ..strokeWidth = 0.5;
    for (final angle in [math.pi / 4, 3 * math.pi / 4, -math.pi / 4, -3 * math.pi / 4]) {
      final inner = center + Offset(math.cos(angle) * centerR, math.sin(angle) * centerR);
      final outer = center + Offset(math.cos(angle) * (outerR - 2), math.sin(angle) * (outerR - 2));
      canvas.drawLine(inner, outer, dividerPaint);
    }

    // Arrow chevrons in each quadrant
    final arrowPaint = Paint()
      ..color = textColor.withValues(alpha: 0.35)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2.0
      ..strokeCap = StrokeCap.round;

    final dist = outerR * 0.70;
    final sz = outerR * 0.125;

    _drawChevron(canvas, center + Offset(0, -dist), sz, -math.pi / 2, arrowPaint);
    _drawChevron(canvas, center + Offset(0, dist), sz, math.pi / 2, arrowPaint);
    _drawChevron(canvas, center + Offset(-dist, 0), sz, math.pi, arrowPaint);
    _drawChevron(canvas, center + Offset(dist, 0), sz, 0, arrowPaint);
  }

  void _drawChevron(Canvas canvas, Offset tip, double size, double angle, Paint paint) {
    final dx = math.cos(angle) * size;
    final dy = math.sin(angle) * size;
    final px = -dy * 0.7;
    final py = dx * 0.7;

    final path = Path()
      ..moveTo(tip.dx - dx + px, tip.dy - dy + py)
      ..lineTo(tip.dx, tip.dy)
      ..lineTo(tip.dx - dx - px, tip.dy - dy - py);
    canvas.drawPath(path, paint);
  }

  @override
  bool shouldRepaint(_DpadPainter old) =>
      activeZone != old.activeZone ||
      isDark != old.isDark ||
      centerKey != old.centerKey ||
      centerRadius != old.centerRadius ||
      flashActive != old.flashActive;
}

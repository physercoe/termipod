import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/design_colors.dart';

/// Floating draggable joystick overlay for terminal navigation.
///
/// Designed for large screens (foldable/tablet) where the bottom-right corner
/// of the terminal is often empty. Center tap sends Enter, drag sends arrow
/// keys — ideal for Claude Code approve/reject flow.
class FloatingJoystick extends StatefulWidget {
  final void Function(String tmuxKey) onSpecialKeyPressed;
  final bool haptic;
  final int repeatRate;

  const FloatingJoystick({
    super.key,
    required this.onSpecialKeyPressed,
    this.haptic = true,
    this.repeatRate = 80,
  });

  @override
  State<FloatingJoystick> createState() => _FloatingJoystickState();
}

class _FloatingJoystickState extends State<FloatingJoystick> {
  // Position (bottom-right by default)
  double _right = 16;
  double _bottom = 8;

  // Joystick state
  static const _outerRadius = 48.0;
  static const _innerRadius = 20.0;
  static const _deadZone = 10.0;

  Offset _thumbOffset = Offset.zero;
  String? _activeDirection;
  Timer? _repeatTimer;
  bool _isDraggingPosition = false;

  // For distinguishing position drag from joystick input
  Offset? _positionDragStart;
  Offset? _positionDragStartPos;

  void _onPanStart(DragStartDetails details) {
    final center = const Offset(_outerRadius, _outerRadius);
    final distance = (details.localPosition - center).distance;

    // If touch starts near the edge, treat as position drag
    if (distance > _outerRadius - 8) {
      _isDraggingPosition = true;
      _positionDragStart = details.globalPosition;
      _positionDragStartPos = Offset(_right, _bottom);
      return;
    }
    _isDraggingPosition = false;
  }

  void _onPanUpdate(DragUpdateDetails details) {
    if (_isDraggingPosition) {
      // Move the entire widget
      final delta = details.globalPosition - _positionDragStart!;
      setState(() {
        _right = (_positionDragStartPos!.dx - delta.dx).clamp(0.0, 1000.0);
        _bottom = (_positionDragStartPos!.dy - delta.dy).clamp(0.0, 1000.0);
      });
      return;
    }

    // Joystick input
    final center = const Offset(_outerRadius, _outerRadius);
    final local = details.localPosition - center;
    final distance = local.distance;
    final maxRadius = _outerRadius - _innerRadius;

    final clamped = distance > maxRadius ? local / distance * maxRadius : local;
    setState(() => _thumbOffset = clamped);

    if (distance < _deadZone) {
      _stopRepeat();
      _activeDirection = null;
      return;
    }

    final angle = math.atan2(local.dy, local.dx);
    String direction;
    if (angle > -math.pi / 4 && angle <= math.pi / 4) {
      direction = 'Right';
    } else if (angle > math.pi / 4 && angle <= 3 * math.pi / 4) {
      direction = 'Down';
    } else if (angle > -3 * math.pi / 4 && angle <= -math.pi / 4) {
      direction = 'Up';
    } else {
      direction = 'Left';
    }

    if (direction != _activeDirection) {
      _activeDirection = direction;
      _stopRepeat();
      widget.onSpecialKeyPressed(direction);
      if (widget.haptic) HapticFeedback.lightImpact();
      _repeatTimer = Timer.periodic(
        Duration(milliseconds: widget.repeatRate),
        (_) {
          if (widget.haptic) HapticFeedback.selectionClick();
          widget.onSpecialKeyPressed(direction);
        },
      );
    }
  }

  void _onPanEnd(DragEndDetails _) {
    _isDraggingPosition = false;
    _stopRepeat();
    _activeDirection = null;
    setState(() => _thumbOffset = Offset.zero);
  }

  void _onTap() {
    // Center tap = Enter
    widget.onSpecialKeyPressed('Enter');
    if (widget.haptic) HapticFeedback.mediumImpact();
  }

  void _stopRepeat() {
    _repeatTimer?.cancel();
    _repeatTimer = null;
  }

  @override
  void dispose() {
    _stopRepeat();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Positioned(
      right: _right,
      bottom: _bottom,
      child: GestureDetector(
        onTap: _onTap,
        onPanStart: _onPanStart,
        onPanUpdate: _onPanUpdate,
        onPanEnd: _onPanEnd,
        onPanCancel: () {
          _isDraggingPosition = false;
          _stopRepeat();
          _activeDirection = null;
          setState(() => _thumbOffset = Offset.zero);
        },
        child: SizedBox(
          width: _outerRadius * 2,
          height: _outerRadius * 2,
          child: CustomPaint(
            painter: _FloatingJoystickPainter(
              isDark: isDark,
              thumbOffset: _thumbOffset,
              isActive: _activeDirection != null,
            ),
            child: Center(
              child: Text(
                'ENT',
                style: TextStyle(
                  fontSize: 9,
                  fontWeight: FontWeight.w700,
                  color: (isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight)
                      .withValues(alpha: 0.4),
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

class _FloatingJoystickPainter extends CustomPainter {
  final bool isDark;
  final Offset thumbOffset;
  final bool isActive;

  _FloatingJoystickPainter({
    required this.isDark,
    required this.thumbOffset,
    required this.isActive,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final center = Offset(size.width / 2, size.height / 2);
    final outerRadius = size.width / 2;
    const innerRadius = _FloatingJoystickState._innerRadius;

    // Outer circle (semi-transparent background)
    canvas.drawCircle(
      center,
      outerRadius,
      Paint()
        ..color = (isDark ? DesignColors.keyBackground : DesignColors.keyBackgroundLight)
            .withValues(alpha: 0.85),
    );

    // Outer ring
    canvas.drawCircle(
      center,
      outerRadius - 1,
      Paint()
        ..color = (isDark ? DesignColors.borderDark : DesignColors.borderLight)
            .withValues(alpha: 0.6)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.5,
    );

    // Direction indicators (small arrows)
    final arrowColor = (isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight)
        .withValues(alpha: 0.2);
    final arrowPaint = Paint()
      ..color = arrowColor
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5
      ..strokeCap = StrokeCap.round;

    const arrowSize = 6.0;
    const arrowDist = 34.0;

    // Up arrow
    _drawArrow(canvas, center + const Offset(0, -arrowDist), arrowSize, 0, arrowPaint);
    // Down arrow
    _drawArrow(canvas, center + const Offset(0, arrowDist), arrowSize, math.pi, arrowPaint);
    // Left arrow
    _drawArrow(canvas, center + const Offset(-arrowDist, 0), arrowSize, math.pi / 2, arrowPaint);
    // Right arrow
    _drawArrow(canvas, center + const Offset(arrowDist, 0), arrowSize, -math.pi / 2, arrowPaint);

    // Inner thumb
    final thumbColor = isActive
        ? DesignColors.primary
        : (isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight)
            .withValues(alpha: 0.5);

    canvas.drawCircle(
      center + thumbOffset,
      innerRadius,
      Paint()..color = thumbColor.withValues(alpha: 0.3),
    );
    canvas.drawCircle(
      center + thumbOffset,
      innerRadius,
      Paint()
        ..color = thumbColor.withValues(alpha: 0.6)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.5,
    );
  }

  void _drawArrow(Canvas canvas, Offset center, double size, double rotation, Paint paint) {
    final path = Path();
    // Chevron pointing up, rotated
    path.moveTo(center.dx - size * math.cos(rotation + math.pi / 4),
        center.dy - size * math.sin(rotation + math.pi / 4));
    path.lineTo(center.dx, center.dy);
    path.lineTo(center.dx + size * math.cos(rotation - math.pi / 4),
        center.dy + size * math.sin(rotation - math.pi / 4));
    canvas.drawPath(path, paint);
  }

  @override
  bool shouldRepaint(_FloatingJoystickPainter old) =>
      thumbOffset != old.thumbOffset || isActive != old.isActive || isDark != old.isDark;
}

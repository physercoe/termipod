import 'package:flutter/material.dart';

import '../../../services/tmux/tmux_parser.dart';

/// ペインレイアウトを描画するCustomPainter
///
/// tmuxから取得したpane_left/pane_topを使用して
/// 実際のレイアウトを正確に再現する
class PaneLayoutPainter extends CustomPainter {
  final List<TmuxPane> panes;
  final String? activePaneId;
  final Color activeColor;
  final bool isDark;

  PaneLayoutPainter({
    required this.panes,
    this.activePaneId,
    required this.activeColor,
    required this.isDark,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (panes.isEmpty) return;

    int maxRight = 0;
    int maxBottom = 0;
    for (final pane in panes) {
      final right = pane.left + pane.width;
      final bottom = pane.top + pane.height;
      if (right > maxRight) maxRight = right;
      if (bottom > maxBottom) maxBottom = bottom;
    }

    if (maxRight == 0 || maxBottom == 0) return;

    final scaleX = size.width / maxRight;
    final scaleY = size.height / maxBottom;
    final gap = 1.0;

    for (final pane in panes) {
      final isActive = pane.id == activePaneId;

      final left = pane.left * scaleX;
      final top = pane.top * scaleY;
      final width = pane.width * scaleX - gap;
      final height = pane.height * scaleY - gap;

      final rect = Rect.fromLTWH(left, top, width, height);

      final bgPaint = Paint()
        ..color = isActive
            ? activeColor.withValues(alpha: 0.3)
            : (isDark ? Colors.black45 : Colors.grey.shade300);
      canvas.drawRect(rect, bgPaint);

      final borderPaint = Paint()
        ..color = isActive ? activeColor : (isDark ? Colors.white30 : Colors.grey.shade500)
        ..style = PaintingStyle.stroke
        ..strokeWidth = isActive ? 1.5 : 1.0;
      canvas.drawRect(rect, borderPaint);
    }
  }

  @override
  bool shouldRepaint(covariant PaneLayoutPainter oldDelegate) {
    return panes != oldDelegate.panes ||
        activePaneId != oldDelegate.activePaneId ||
        activeColor != oldDelegate.activeColor ||
        isDark != oldDelegate.isDark;
  }
}

/// 右分割アイコン: 左に既存ペイン、右に新ペイン（+マーク付き）
class SplitRightIconPainter extends CustomPainter {
  final Color color;

  SplitRightIconPainter({required this.color});

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..strokeWidth = 1.5
      ..style = PaintingStyle.stroke;

    final w = size.width;
    final h = size.height;
    final pad = w * 0.1;
    final mid = w * 0.5;

    canvas.drawRRect(
      RRect.fromRectAndRadius(
        Rect.fromLTWH(pad, pad, w - pad * 2, h - pad * 2),
        const Radius.circular(2),
      ),
      paint,
    );

    canvas.drawLine(Offset(mid, pad), Offset(mid, h - pad), paint);

    final plusPaint = Paint()
      ..color = color
      ..strokeWidth = 2.0
      ..strokeCap = StrokeCap.round;
    final cx = mid + (w - pad - mid) / 2;
    final cy = h / 2;
    final plusSize = w * 0.12;
    canvas.drawLine(Offset(cx - plusSize, cy), Offset(cx + plusSize, cy), plusPaint);
    canvas.drawLine(Offset(cx, cy - plusSize), Offset(cx, cy + plusSize), plusPaint);
  }

  @override
  bool shouldRepaint(covariant SplitRightIconPainter oldDelegate) =>
      color != oldDelegate.color;
}

/// 下分割アイコン: 上に既存ペイン、下に新ペイン（+マーク付き）
class SplitDownIconPainter extends CustomPainter {
  final Color color;

  SplitDownIconPainter({required this.color});

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..strokeWidth = 1.5
      ..style = PaintingStyle.stroke;

    final w = size.width;
    final h = size.height;
    final pad = w * 0.1;
    final mid = h * 0.5;

    canvas.drawRRect(
      RRect.fromRectAndRadius(
        Rect.fromLTWH(pad, pad, w - pad * 2, h - pad * 2),
        const Radius.circular(2),
      ),
      paint,
    );

    canvas.drawLine(Offset(pad, mid), Offset(w - pad, mid), paint);

    final plusPaint = Paint()
      ..color = color
      ..strokeWidth = 2.0
      ..strokeCap = StrokeCap.round;
    final cx = w / 2;
    final cy = mid + (h - pad - mid) / 2;
    final plusSize = w * 0.12;
    canvas.drawLine(Offset(cx - plusSize, cy), Offset(cx + plusSize, cy), plusPaint);
    canvas.drawLine(Offset(cx, cy - plusSize), Offset(cx, cy + plusSize), plusPaint);
  }

  @override
  bool shouldRepaint(covariant SplitDownIconPainter oldDelegate) =>
      color != oldDelegate.color;
}

import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// W5b — Deliverable-scope state pip (A5 §7). Three states like the
/// section pip plus an `in-review` middle state that the section pip
/// doesn't carry: a director may move a deliverable to `in-review` to
/// signal "ready for me to ratify" without ratifying yet.
///
/// - `draft`      → open circle, muted, "draft"
/// - `in-review`  → half-filled circle, amber, "in review"
/// - `ratified`   → filled circle, terminal green, "ratified"
///
/// Reuses the same painter idiom as the section pip; a different file so
/// the section pip's enum stays a closed 3-state value to match A4.
enum DeliverableState { draft, inReview, ratified }

DeliverableState parseDeliverableState(String? raw) {
  switch ((raw ?? '').toLowerCase()) {
    case 'ratified':
      return DeliverableState.ratified;
    case 'in-review':
    case 'in_review':
      return DeliverableState.inReview;
    default:
      return DeliverableState.draft;
  }
}

class DeliverableStatePip extends StatelessWidget {
  final DeliverableState state;
  final double size;
  final bool showLabel;
  const DeliverableStatePip({
    super.key,
    required this.state,
    this.size = 12,
    this.showLabel = true,
  });

  @override
  Widget build(BuildContext context) {
    final color = _colorFor(state);
    final glyph = _glyphFor(state, color);
    if (!showLabel) return glyph;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        glyph,
        const SizedBox(width: 6),
        Text(
          _labelFor(state),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w700,
            color: color,
            letterSpacing: 0.4,
          ),
        ),
      ],
    );
  }

  Widget _glyphFor(DeliverableState s, Color color) {
    final dim = size;
    switch (s) {
      case DeliverableState.draft:
        return Container(
          width: dim,
          height: dim,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: color, width: 1.5),
          ),
        );
      case DeliverableState.inReview:
        return SizedBox(
          width: dim,
          height: dim,
          child: CustomPaint(painter: _DeliverableHalfPipPainter(color: color)),
        );
      case DeliverableState.ratified:
        return Container(
          width: dim,
          height: dim,
          decoration: BoxDecoration(shape: BoxShape.circle, color: color),
        );
    }
  }

  static Color _colorFor(DeliverableState s) {
    switch (s) {
      case DeliverableState.draft:
        return DesignColors.textMuted;
      case DeliverableState.inReview:
        return DesignColors.warning;
      case DeliverableState.ratified:
        return DesignColors.terminalGreen;
    }
  }

  static String _labelFor(DeliverableState s) {
    switch (s) {
      case DeliverableState.draft:
        return 'draft';
      case DeliverableState.inReview:
        return 'in review';
      case DeliverableState.ratified:
        return 'ratified';
    }
  }
}

class _DeliverableHalfPipPainter extends CustomPainter {
  final Color color;
  const _DeliverableHalfPipPainter({required this.color});

  @override
  void paint(Canvas canvas, Size size) {
    final r = size.width / 2;
    final center = Offset(r, r);
    final ring = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5;
    final fill = Paint()
      ..color = color
      ..style = PaintingStyle.fill;
    final rect = Rect.fromCircle(center: center, radius: r - 0.75);
    canvas.drawArc(rect, -1.5708, 3.14159, true, fill);
    canvas.drawCircle(center, r - 0.75, ring);
  }

  @override
  bool shouldRepaint(covariant _DeliverableHalfPipPainter old) =>
      old.color != color;
}

import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// Three-state visual encoding for typed-document sections (W5a / A4
/// §6). Mirrors the table in `structured-document-viewer.md`:
///
/// - `empty`     → open circle, muted gray, "empty"
/// - `draft`     → half-filled circle, amber/warning, "draft"
/// - `ratified`  → filled circle, terminal green, "ratified"
///
/// Color tokens come from the existing design palette — no new tokens
/// introduced. Pip + label are always rendered together so the
/// affordance is never color-only (accessibility, A4 §13).
enum SectionState { empty, draft, ratified }

SectionState parseSectionState(String? raw) {
  switch ((raw ?? '').toLowerCase()) {
    case 'ratified':
      return SectionState.ratified;
    case 'draft':
      return SectionState.draft;
    default:
      return SectionState.empty;
  }
}

class SectionStatePip extends StatelessWidget {
  final SectionState state;
  final double size;
  final bool showLabel;
  const SectionStatePip({
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

  Widget _glyphFor(SectionState s, Color color) {
    final dim = size;
    switch (s) {
      case SectionState.empty:
        // Open circle.
        return Container(
          width: dim,
          height: dim,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            border: Border.all(color: color, width: 1.5),
          ),
        );
      case SectionState.draft:
        // Half-filled — outer ring + half-disc clipper.
        return SizedBox(
          width: dim,
          height: dim,
          child: CustomPaint(painter: _HalfFilledPipPainter(color: color)),
        );
      case SectionState.ratified:
        return Container(
          width: dim,
          height: dim,
          decoration: BoxDecoration(shape: BoxShape.circle, color: color),
        );
    }
  }

  static Color _colorFor(SectionState s) {
    switch (s) {
      case SectionState.empty:
        return DesignColors.textMuted;
      case SectionState.draft:
        return DesignColors.warning;
      case SectionState.ratified:
        return DesignColors.terminalGreen;
    }
  }

  static String _labelFor(SectionState s) {
    switch (s) {
      case SectionState.empty:
        return 'empty';
      case SectionState.draft:
        return 'draft';
      case SectionState.ratified:
        return 'ratified';
    }
  }
}

class _HalfFilledPipPainter extends CustomPainter {
  final Color color;
  const _HalfFilledPipPainter({required this.color});

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
    // Right half filled (3π/2 → π/2 sweep π); ring on top.
    final rect = Rect.fromCircle(center: center, radius: r - 0.75);
    canvas.drawArc(rect, -1.5708, 3.14159, true, fill);
    canvas.drawCircle(center, r - 0.75, ring);
  }

  @override
  bool shouldRepaint(covariant _HalfFilledPipPainter old) =>
      old.color != color;
}

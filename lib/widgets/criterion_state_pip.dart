import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// W6 — Acceptance-criterion state pip (A5 §8.2). Four states; reuses
/// the section/deliverable pip palette so the affordance feels uniform
/// across the lifecycle viewers:
///
/// - `pending` → open warning ring, "pending"
/// - `met`     → filled green disc, "met"
/// - `failed`  → filled red disc, "failed"
/// - `waived`  → filled muted disc, "waived"
enum CriterionState { pending, met, failed, waived }

CriterionState parseCriterionState(String? raw) {
  switch ((raw ?? '').toLowerCase()) {
    case 'met':
      return CriterionState.met;
    case 'failed':
      return CriterionState.failed;
    case 'waived':
      return CriterionState.waived;
    default:
      return CriterionState.pending;
  }
}

class CriterionStatePip extends StatelessWidget {
  final CriterionState state;
  final double size;
  final bool showLabel;
  const CriterionStatePip({
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

  Widget _glyphFor(CriterionState s, Color color) {
    final dim = size;
    if (s == CriterionState.pending) {
      return Container(
        width: dim,
        height: dim,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          border: Border.all(color: color, width: 1.5),
        ),
      );
    }
    return Container(
      width: dim,
      height: dim,
      decoration: BoxDecoration(shape: BoxShape.circle, color: color),
    );
  }

  static Color _colorFor(CriterionState s) {
    switch (s) {
      case CriterionState.pending:
        return DesignColors.warning;
      case CriterionState.met:
        return DesignColors.terminalGreen;
      case CriterionState.failed:
        return DesignColors.error;
      case CriterionState.waived:
        return DesignColors.textMuted;
    }
  }

  static String _labelFor(CriterionState s) {
    switch (s) {
      case CriterionState.pending:
        return 'pending';
      case CriterionState.met:
        return 'met';
      case CriterionState.failed:
        return 'failed';
      case CriterionState.waived:
        return 'waived';
    }
  }
}

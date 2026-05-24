/// ADR-030 Phase 3 — shared visual + parsing helpers for the per-kind
/// propose cards (W15-W18). Extracted once W16 (the second card) made
/// the duplication concrete; W17/W18 inherit these for free.
///
/// Exports:
/// - [decodeJsonObject] — defensive JSON-or-Map decoder for
///   change_spec / target_ref / executed wire fields (the hub usually
///   ships them as decoded maps, but a stringified upstream payload
///   should not crash the card).
/// - [StalledPill] — the top pill shown on the stalled-variant of any
///   per-kind card. Pure presentation; the "Stuck for Nh" duration
///   is computed at the caller (the row's `escalated_at` isn't yet
///   exposed on the wire — when W19.6-mobile lands the digest card,
///   the duration string flows in via a named param).
/// - [TransitionChip] — the from-to chip pair pattern shared between
///   deliverable.set_state (state names), phase.advance (phase names),
///   and task.set_status (status names). One chip widget; the caller
///   passes label + emphasis colour family.
library;

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';

Map<String, dynamic> decodeJsonObject(dynamic raw) {
  if (raw == null) return const {};
  if (raw is Map<String, dynamic>) return raw;
  if (raw is String) {
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) return decoded;
    } catch (_) {
      return const {};
    }
  }
  return const {};
}

/// Distinct colour families for the transition chips. Each per-kind
/// card picks one so the user can tell propose-kinds apart at a glance
/// even with the same body shape.
enum TransitionChipFamily {
  /// W15 deliverable.set_state — green (state transitions are ratify-
  /// adjacent, "going live").
  state,

  /// W16 phase.advance — indigo (phase walks signal project-stage
  /// progress; not "deliverable goes live" but "team moves forward").
  phase,

  /// W17 task.set_status — slate (status changes are routine; less
  /// visual weight than state/phase).
  status,
}

class TransitionChip extends StatelessWidget {
  final String label;
  final TransitionChipFamily family;
  final bool emphasis;
  const TransitionChip({
    super.key,
    required this.label,
    required this.family,
    required this.emphasis,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final (bg, fg) = _palette(isDark);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          fontWeight: emphasis ? FontWeight.w600 : FontWeight.w400,
          color: fg,
        ),
      ),
    );
  }

  (Color, Color) _palette(bool isDark) {
    if (!emphasis) {
      return (
        isDark ? Colors.grey.shade800 : Colors.grey.shade200,
        isDark ? Colors.grey.shade200 : Colors.grey.shade800,
      );
    }
    switch (family) {
      case TransitionChipFamily.state:
        return (
          isDark ? Colors.green.shade900 : Colors.green.shade100,
          isDark ? Colors.green.shade200 : Colors.green.shade900,
        );
      case TransitionChipFamily.phase:
        return (
          isDark ? Colors.indigo.shade900 : Colors.indigo.shade100,
          isDark ? Colors.indigo.shade100 : Colors.indigo.shade900,
        );
      case TransitionChipFamily.status:
        return (
          isDark ? Colors.blueGrey.shade700 : Colors.blueGrey.shade100,
          isDark ? Colors.blueGrey.shade100 : Colors.blueGrey.shade900,
        );
    }
  }
}

class StalledPill extends StatelessWidget {
  final String addressee;
  const StalledPill({super.key, required this.addressee});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? Colors.amber.shade900 : Colors.amber.shade100;
    final fg = isDark ? Colors.amber.shade100 : Colors.amber.shade900;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.schedule, size: 12, color: fg),
          const SizedBox(width: 4),
          Text(
            addressee.isEmpty
                ? 'Stuck — awaiting decision'
                : 'Stuck — addressed to $addressee',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              color: fg,
            ),
          ),
        ],
      ),
    );
  }
}

/// Convenience pair: rectangular transition-frame containing
/// `from → to` chips. Used by all three state-shaped cards
/// (deliverable, phase, task). Pass an empty `fromLabel` to render
/// the `→ to` half-arrow that phase.advance allows when the
/// optimistic-concurrency check is omitted.
class TransitionFrame extends StatelessWidget {
  final String fromLabel;
  final String toLabel;
  final TransitionChipFamily family;
  const TransitionFrame({
    super.key,
    required this.fromLabel,
    required this.toLabel,
    required this.family,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        border: Border.all(color: mutedColor.withValues(alpha: 0.35)),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (fromLabel.isNotEmpty) ...[
            TransitionChip(label: fromLabel, family: family, emphasis: false),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 6),
              child: Icon(Icons.arrow_forward, size: 12, color: mutedColor),
            ),
          ] else
            Padding(
              padding: const EdgeInsets.only(right: 6),
              child: Icon(Icons.arrow_forward, size: 12, color: mutedColor),
            ),
          TransitionChip(label: toLabel, family: family, emphasis: true),
        ],
      ),
    );
  }
}

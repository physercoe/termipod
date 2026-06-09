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
import '../../../theme/tokens.dart';

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
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final base = _base(isDark);
    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: Spacing.s8, vertical: Spacing.s2),
      decoration: BoxDecoration(
        color: base.withValues(alpha: 0.16),
        borderRadius: Radii.xsBorder,
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
          fontWeight: emphasis ? FontWeight.w600 : FontWeight.w400,
          color: base,
        ),
      ),
    );
  }

  /// The semantic base color for this chip; the chip tints its background
  /// from it (ADR-047 D-5). Non-emphasis chips read as neutral muted text.
  Color _base(bool isDark) {
    if (!emphasis) {
      return isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    }
    return switch (family) {
      TransitionChipFamily.state => DesignColors.success,
      TransitionChipFamily.phase => DesignColors.info,
      TransitionChipFamily.status => DesignColors.slate,
    };
  }
}

class StalledPill extends StatelessWidget {
  final String addressee;
  const StalledPill({super.key, required this.addressee});

  @override
  Widget build(BuildContext context) {
    const base = DesignColors.warning;
    return Container(
      padding: const EdgeInsets.symmetric(
          horizontal: Spacing.s8, vertical: Spacing.s4),
      decoration: BoxDecoration(
        color: base.withValues(alpha: 0.16),
        borderRadius: Radii.xsBorder,
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.schedule, size: IconSizes.sm, color: base),
          const SizedBox(width: Spacing.s4),
          Text(
            addressee.isEmpty
                ? 'Stuck — awaiting decision'
                : 'Stuck — addressed to $addressee',
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
              fontWeight: FontWeight.w600,
              color: base,
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
      padding: const EdgeInsets.symmetric(
          horizontal: Spacing.s8, vertical: Spacing.s4),
      decoration: BoxDecoration(
        border: Border.all(color: mutedColor.withValues(alpha: 0.35)),
        borderRadius: Radii.smBorder,
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (fromLabel.isNotEmpty) ...[
            TransitionChip(label: fromLabel, family: family, emphasis: false),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: Spacing.s8),
              child:
                  Icon(Icons.arrow_forward, size: IconSizes.sm, color: mutedColor),
            ),
          ] else
            Padding(
              padding: const EdgeInsets.only(right: Spacing.s8),
              child:
                  Icon(Icons.arrow_forward, size: IconSizes.sm, color: mutedColor),
            ),
          TransitionChip(label: toLabel, family: family, emphasis: true),
        ],
      ),
    );
  }
}

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/vocab_provider.dart';
import '../services/vocab/vocab_axis.dart';
import '../theme/design_colors.dart';
import '../theme/tokens.dart';

/// Tiny pill used to mark artifacts authored or initiated by the steward
/// agent — channel messages, attention items, audit rows — per
/// `docs/ia-redesign.md` §11 Wedge 7.
///
/// [fromId] can be a raw sender id (e.g. `@steward`, `agent:abc-steward`)
/// or an actor handle. The badge renders when the value equals or ends
/// with `steward`, so both hub-minted agent ids and handle-only actors
/// match the same predicate.
class StewardBadge extends ConsumerWidget {
  const StewardBadge({super.key});

  static bool matches(String fromId) {
    if (fromId.isEmpty) return false;
    final lower = fromId.toLowerCase();
    return lower == 'steward' ||
        lower == '@steward' ||
        lower.endsWith(':steward') ||
        lower.endsWith('/steward') ||
        lower.endsWith('-steward');
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final label = ref.watch(vocabularyProvider).term(VocabAxis.roleSteward).lower;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: Spacing.s2),
      margin: const EdgeInsets.only(left: Spacing.s8),
      decoration: BoxDecoration(
        color: DesignColors.primary.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(
          color: DesignColors.primary.withValues(alpha: 0.45),
        ),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.smart_toy_outlined,
              size: 10, color: DesignColors.primary),
          const SizedBox(width: 3),
          Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
              fontWeight: FontWeight.w700,
              color: DesignColors.primary,
              letterSpacing: 0.3,
            ),
          ),
        ],
      ),
    );
  }
}

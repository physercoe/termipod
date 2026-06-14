import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../l10n/app_localizations.dart';
import '../../../theme/design_colors.dart';
import '../../../theme/tokens.dart';
import 'propose_addressee.dart';

/// ADR-030 W19.6 (mobile half) — top-of-Me digest card surfacing the
/// count of stalled propose rows (escalation_state != 'none' but the
/// viewer is NOT the addressee). Renders the D-7 Option 2′ signal
/// walk's effect at-a-glance so the principal sees stalled decisions
/// without scrolling the Requests filter.
///
/// Hidden when [stalledCount] is 0 — no card is rendered (caller
/// short-circuits via [hasStalledDecisions]).
///
/// Tap toggles a "stalled only" filter state held by [stalledFilterProvider].
/// When ON, the Me-page list narrows further to rows whose
/// `escalation_state != 'none'` (AND-combined with the active
/// chip-filter).
class StalledDecisionsDigest extends ConsumerWidget {
  final int stalledCount;
  final int stalledOverDayCount;
  const StalledDecisionsDigest({
    super.key,
    required this.stalledCount,
    required this.stalledOverDayCount,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (stalledCount == 0) return const SizedBox.shrink();
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final isFilterActive = ref.watch(stalledFilterProvider);
    final warnContainer = isDark
        ? DesignColors.warningContainer
        : DesignColors.warningContainerLight;
    final accentBg =
        isFilterActive ? warnContainer : warnContainer.withValues(alpha: 0.4);
    final accentFg = isDark
        ? DesignColors.onWarningContainer
        : DesignColors.onWarningContainerLight;
    final l10n = AppLocalizations.of(context)!;
    final subtitle = _subtitle(l10n);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: InkWell(
        onTap: () => ref.read(stalledFilterProvider.notifier).toggle(),
        borderRadius: BorderRadius.circular(8),
        child: Container(
          padding: const EdgeInsets.fromLTRB(12, Spacing.s8, 12, Spacing.s8),
          decoration: BoxDecoration(
            color: accentBg,
            borderRadius: BorderRadius.circular(8),
            border: Border.all(
              color: accentFg.withValues(alpha: isFilterActive ? 0.5 : 0.2),
              width: isFilterActive ? 1.5 : 1,
            ),
          ),
          child: Row(
            children: [
              Icon(Icons.schedule, size: 18, color: accentFg),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Text(
                          isFilterActive
                              ? 'Showing stalled decisions'
                              : 'Stalled decisions',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            fontWeight: FontWeight.w700,
                            color: accentFg,
                          ),
                        ),
                        const SizedBox(width: 8),
                        _CountBadge(count: stalledCount, fg: accentFg),
                      ],
                    ),
                    const SizedBox(height: 2),
                    Text(
                      subtitle,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: FontSizes.label,
                        color: mutedColor,
                      ),
                    ),
                  ],
                ),
              ),
              Icon(
                isFilterActive ? Icons.filter_alt : Icons.chevron_right,
                size: 16,
                color: mutedColor,
              ),
            ],
          ),
        ),
      ),
    );
  }

  String _subtitle(AppLocalizations l10n) {
    if (stalledOverDayCount > 0 && stalledCount > stalledOverDayCount) {
      final younger = stalledCount - stalledOverDayCount;
      return '$younger stalled at stewards · $stalledOverDayCount stalled with you. Tap to filter.';
    }
    if (stalledOverDayCount > 0) {
      return '$stalledOverDayCount stalled with you. Tap to filter.';
    }
    return l10n.stalledDigestTapHint;
  }
}

class _CountBadge extends StatelessWidget {
  final int count;
  final Color fg;
  const _CountBadge({required this.count, required this.fg});
  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: 2),
      decoration: BoxDecoration(
        color: fg.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        '$count',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          color: fg,
        ),
      ),
    );
  }
}

/// Toggle for the "stalled only" filter narrowing the Me-page items.
/// State persists for the lifetime of the page (NotifierProvider —
/// not AsyncNotifier; no persistence). Defaults to OFF so the card
/// surfaces stalled-count without immediately narrowing the list.
class StalledFilterNotifier extends Notifier<bool> {
  @override
  bool build() => false;
  void toggle() => state = !state;
  void set(bool v) => state = v;
}

final stalledFilterProvider =
    NotifierProvider<StalledFilterNotifier, bool>(StalledFilterNotifier.new);

/// Returns true when there's at least one stalled propose row in the
/// attention items list. Caller (me_screen.dart) uses this to decide
/// whether to render the digest card.
bool hasStalledDecisions(List<Map<String, dynamic>> items) {
  for (final a in items) {
    if (isStalledPropose(a)) return true;
  }
  return false;
}

/// Count of stalled propose rows in the attention items list. The
/// digest card shows this as the badge.
int stalledDecisionsCount(List<Map<String, dynamic>> items) {
  var n = 0;
  for (final a in items) {
    if (isStalledPropose(a)) n++;
  }
  return n;
}

/// Count of stalled propose rows that have walked all the way to the
/// principal tier (`escalation_state == 'escalated_principal'`).
/// Surfaces separately in the digest subtitle so the principal sees
/// "N stalled with you" distinct from "N stalled at stewards".
int stalledOverDayDecisionsCount(List<Map<String, dynamic>> items) {
  var n = 0;
  for (final a in items) {
    if ((a['escalation_state'] ?? '').toString() == 'escalated_principal') {
      n++;
    }
  }
  return n;
}

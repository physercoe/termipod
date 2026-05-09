import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/insights_provider.dart';
import '../providers/me_stats_provider.dart';
import '../screens/insights/insights_screen.dart';
import '../theme/design_colors.dart';

/// Me-tab Stats card (Phase 2 W3 of insights-phase-2.md). Shows
/// today's team-wide token spend + Δ% vs the prior 7-day average,
/// keyed on the supplied `teamId`. Tap → fullscreen [InsightsScreen]
/// scoped to team.
///
/// Compact by design — managers want a glance, not a dashboard. The
/// full breakdown is one tap away on the Insights screen.
class MeStatsCard extends ConsumerWidget {
  final String teamId;
  const MeStatsCard({super.key, required this.teamId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (teamId.isEmpty) return const SizedBox.shrink();
    final async = ref.watch(meTeamSpendDeltaProvider(teamId));
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;

    final state = async.value;
    if (state == null) {
      // First load: keep height predictable so the digest above
      // doesn't jiggle when the card lands.
      return const SizedBox(height: 0);
    }
    if (state.error != null) {
      return _ErrorChip(message: state.error!, muted: muted);
    }
    if (!state.hasData) {
      // No traffic on this team — showing 0 tokens with no Δ would
      // be noise. Hide until something flows.
      return const SizedBox.shrink();
    }

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: () => Navigator.of(context).push(MaterialPageRoute(
          builder: (_) =>
              InsightsScreen(scope: InsightsScope.team(teamId)),
        )),
        child: Container(
          padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
          decoration: BoxDecoration(
            color: cardBg,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: border),
          ),
          child: Row(
            children: [
              Icon(Icons.insights_outlined, size: 18, color: muted),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'TODAY · TEAM SPEND',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 9,
                        fontWeight: FontWeight.w700,
                        letterSpacing: 0.6,
                        color: muted,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Row(
                      crossAxisAlignment: CrossAxisAlignment.baseline,
                      textBaseline: TextBaseline.alphabetic,
                      children: [
                        Text(
                          _humanTokens(state.todayTokens),
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 22,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        const SizedBox(width: 8),
                        Text(
                          'tokens',
                          style: GoogleFonts.jetBrainsMono(
                            fontSize: 11,
                            color: muted,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 2),
                    _DeltaLine(
                        deltaPct: state.deltaPct,
                        priorAvg: state.prior7dAvgTokens,
                        muted: muted),
                  ],
                ),
              ),
              Icon(Icons.chevron_right, size: 20, color: muted),
            ],
          ),
        ),
      ),
    );
  }
}

class _DeltaLine extends StatelessWidget {
  final double? deltaPct;
  final double priorAvg;
  final Color muted;
  const _DeltaLine(
      {required this.deltaPct,
      required this.priorAvg,
      required this.muted});

  @override
  Widget build(BuildContext context) {
    final priorLabel =
        priorAvg <= 0 ? '—' : '${_humanTokens(priorAvg.round())}/day';
    if (deltaPct == null) {
      return Text(
        'prior 7d avg $priorLabel',
        style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
      );
    }
    final up = deltaPct! >= 0;
    final color = up ? DesignColors.warning : DesignColors.success;
    final sign = up ? '+' : '';
    return Row(
      children: [
        Icon(up ? Icons.arrow_upward : Icons.arrow_downward,
            size: 12, color: color),
        const SizedBox(width: 2),
        Text(
          '$sign${deltaPct!.toStringAsFixed(0)}%',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: color,
          ),
        ),
        const SizedBox(width: 6),
        Flexible(
          child: Text(
            'vs prior 7d avg ($priorLabel)',
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
          ),
        ),
      ],
    );
  }
}

class _ErrorChip extends StatelessWidget {
  final String message;
  final Color muted;
  const _ErrorChip({required this.message, required this.muted});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        decoration: BoxDecoration(
          color: DesignColors.error.withValues(alpha: 0.08),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Row(
          children: [
            Icon(Icons.error_outline,
                size: 14, color: DesignColors.error),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                'Stats unavailable',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.error,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

String _humanTokens(int n) {
  if (n < 1000) return n.toString();
  if (n < 10000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(0)}k';
  return '${(n / 1000000).toStringAsFixed(1)}M';
}

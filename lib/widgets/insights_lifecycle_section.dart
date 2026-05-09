import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/insights_provider.dart';
import '../theme/design_colors.dart';

/// Project lifecycle drilldown — Phase 2 W5d of insights-phase-2.md.
/// Reads `body['lifecycle']` from `/v1/insights`, which the hub
/// populates only for project scope. Other scopes won't have the
/// field and the section silently hides.
///
/// Three sub-sections:
///   - **Phase timeline** — one row per phase the project has
///     entered, with duration. The trailing phase's duration is
///     open-ended (running to now()) so the user sees how long the
///     project has been parked there.
///   - **Deliverable ratification** — count + rate. Backed by
///     `deliverables.ratification_state`.
///   - **Criterion pass-rate** — count + rate, plus a stuck count
///     pulled from `acceptance_criteria.state='failed'` (the
///     actionable bucket). Pending criteria aren't "stuck" — they're
///     normal idle state.
class InsightsLifecycleSection extends ConsumerWidget {
  final InsightsScope scope;
  const InsightsLifecycleSection({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(insightsProvider(scope));
    final body = async.value?.body;
    if (body == null) return const SizedBox.shrink();

    final lifecycle = body['lifecycle'];
    if (lifecycle is! Map) return const SizedBox.shrink();
    final l = lifecycle.cast<String, dynamic>();

    final phases = <_PhaseRow>[];
    final phasesRaw = l['phases'];
    if (phasesRaw is List) {
      for (final p in phasesRaw) {
        if (p is! Map) continue;
        final m = p.cast<String, dynamic>();
        phases.add(_PhaseRow(
          phase: (m['phase'] ?? '').toString(),
          enteredAt: (m['entered_at'] ?? '').toString(),
          durationS: _int(m['duration_s']),
        ));
      }
    }
    final currentPhase = (l['current_phase'] ?? '').toString();
    final delivTotal = _int(l['deliverables_total']);
    final delivRatified = _int(l['deliverables_ratified']);
    final ratificationRate = _double(l['ratification_rate']);
    final critTotal = _int(l['criteria_total']);
    final critMet = _int(l['criteria_met']);
    final passRate = _double(l['criterion_pass_rate']);
    final stuck = _int(l['stuck_count']);

    if (phases.isEmpty && delivTotal == 0 && critTotal == 0) {
      return const SizedBox.shrink();
    }

    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;

    return Padding(
      padding: const EdgeInsets.only(top: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Header(label: 'LIFECYCLE'),
          if (phases.isNotEmpty)
            _PhaseTimeline(
              phases: phases,
              currentPhase: currentPhase,
              cardBg: cardBg,
              border: border,
              muted: muted,
            ),
          if (delivTotal > 0 || critTotal > 0) ...[
            if (phases.isNotEmpty) const SizedBox(height: 10),
            Container(
              decoration: BoxDecoration(
                color: cardBg,
                borderRadius: BorderRadius.circular(12),
                border: Border.all(color: border),
              ),
              padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (delivTotal > 0) ...[
                    _RateRow(
                      label: 'Deliverables ratified',
                      countLabel: '$delivRatified/$delivTotal',
                      rate: ratificationRate,
                      muted: muted,
                    ),
                    if (critTotal > 0)
                      Divider(
                        height: 16,
                        thickness: 1,
                        color: border.withValues(alpha: 0.5),
                      ),
                  ],
                  if (critTotal > 0) ...[
                    _RateRow(
                      label: 'Criteria met',
                      countLabel: '$critMet/$critTotal',
                      rate: passRate,
                      muted: muted,
                    ),
                    if (stuck > 0)
                      Padding(
                        padding: const EdgeInsets.only(top: 8),
                        child: Row(
                          children: [
                            Icon(Icons.warning_amber_rounded,
                                size: 14, color: DesignColors.warning),
                            const SizedBox(width: 6),
                            Text(
                              '$stuck stuck — clear with the steward',
                              style: GoogleFonts.jetBrainsMono(
                                fontSize: 11,
                                color: DesignColors.warning,
                              ),
                            ),
                          ],
                        ),
                      ),
                  ],
                ],
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _PhaseRow {
  final String phase;
  final String enteredAt;
  final int durationS;
  const _PhaseRow({
    required this.phase,
    required this.enteredAt,
    required this.durationS,
  });
}

class _Header extends StatelessWidget {
  final String label;
  const _Header({required this.label});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
      child: Text(
        label,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          color: muted,
          letterSpacing: 0.8,
        ),
      ),
    );
  }
}

class _PhaseTimeline extends StatelessWidget {
  final List<_PhaseRow> phases;
  final String currentPhase;
  final Color cardBg;
  final Color border;
  final Color muted;
  const _PhaseTimeline({
    required this.phases,
    required this.currentPhase,
    required this.cardBg,
    required this.border,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    final maxDuration = phases.fold<int>(
        0, (m, r) => r.durationS > m ? r.durationS : m);
    return Container(
      decoration: BoxDecoration(
        color: cardBg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
      child: Column(
        children: [
          for (var i = 0; i < phases.length; i++) ...[
            if (i > 0)
              Divider(
                height: 12,
                thickness: 1,
                color: border.withValues(alpha: 0.5),
              ),
            _PhaseRowWidget(
              row: phases[i],
              isCurrent: phases[i].phase == currentPhase,
              maxDuration: maxDuration,
              muted: muted,
            ),
          ],
        ],
      ),
    );
  }
}

class _PhaseRowWidget extends StatelessWidget {
  final _PhaseRow row;
  final bool isCurrent;
  final int maxDuration;
  final Color muted;
  const _PhaseRowWidget({
    required this.row,
    required this.isCurrent,
    required this.maxDuration,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    final share = maxDuration == 0 ? 0.0 : row.durationS / maxDuration;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Row(
                  children: [
                    if (isCurrent) ...[
                      Container(
                        width: 6,
                        height: 6,
                        decoration: const BoxDecoration(
                          color: DesignColors.success,
                          shape: BoxShape.circle,
                        ),
                      ),
                      const SizedBox(width: 6),
                    ],
                    Text(
                      row.phase,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ],
                ),
              ),
              Text(
                _humanDuration(row.durationS),
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, fontWeight: FontWeight.w700),
              ),
            ],
          ),
          const SizedBox(height: 4),
          ClipRRect(
            borderRadius: BorderRadius.circular(2),
            child: LinearProgressIndicator(
              minHeight: 4,
              value: share.clamp(0.0, 1.0),
              backgroundColor: muted.withValues(alpha: 0.15),
              valueColor: AlwaysStoppedAnimation(
                  isCurrent ? DesignColors.success : DesignColors.primary),
            ),
          ),
        ],
      ),
    );
  }
}

class _RateRow extends StatelessWidget {
  final String label;
  final String countLabel;
  final double rate;
  final Color muted;
  const _RateRow({
    required this.label,
    required this.countLabel,
    required this.rate,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    final color = _accentForRate(rate);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                label,
                style:
                    GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
              ),
            ),
            Text(
              countLabel,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, fontWeight: FontWeight.w600),
            ),
            const SizedBox(width: 8),
            Text(
              '${(rate * 100).toStringAsFixed(0)}%',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                fontWeight: FontWeight.w700,
                color: color,
              ),
            ),
          ],
        ),
        const SizedBox(height: 4),
        ClipRRect(
          borderRadius: BorderRadius.circular(2),
          child: LinearProgressIndicator(
            minHeight: 4,
            value: rate.clamp(0.0, 1.0),
            backgroundColor: muted.withValues(alpha: 0.15),
            valueColor: AlwaysStoppedAnimation(color),
          ),
        ),
      ],
    );
  }
}

Color _accentForRate(double rate) {
  if (rate >= 0.85) return DesignColors.success;
  if (rate >= 0.5) return DesignColors.warning;
  return DesignColors.error;
}

int _int(Object? v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

double _double(Object? v) {
  if (v is double) return v;
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0.0;
  return 0.0;
}

String _humanDuration(int seconds) {
  if (seconds <= 0) return '—';
  final d = seconds ~/ 86400;
  final h = (seconds % 86400) ~/ 3600;
  final m = (seconds % 3600) ~/ 60;
  if (d > 0) return '${d}d ${h}h';
  if (h > 0) return '${h}h ${m}m';
  if (m > 0) return '${m}m';
  return '${seconds}s';
}

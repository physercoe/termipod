import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
import '../theme/design_colors.dart';
import '../theme/tokens.dart';
import 'app_chip.dart';

/// Foldable run-report dashboard (ADR-038 / agent-run-analysis-mode plan
/// P1). Renders the per-run **digest** as an overview card over the
/// navigable transcript: outcome, turns, duration, cost, errors, tool
/// success, model breakdown, latency. Collapses to a one-line summary so
/// the log below gets full height — insight *is* analysis, one surface.
///
/// Driven by the digest map (the `sessions/{id}/digest` shape), so it has
/// no network concern of its own and is trivially widget-testable. Tapping
/// the errors stat invokes [onJumpToSeq] with the first error anchor when a
/// host wires navigation (the random-access seek lands in P2; the callback
/// is optional so P1 renders standalone).
class RunReportCard extends StatefulWidget {
  final Map<String, dynamic> digest;

  /// Snapshot age when served from the offline cache; null ⇒ live/current.
  final DateTime? staleSince;

  /// True while the run is still live/idle (not terminated) — drives the
  /// "as of `<ts>` · live" affordance vs. a static report.
  final bool live;

  /// Optional: tapping a navigation anchor (an error/tool/turn seq) asks
  /// the host to seek the transcript there. Wired in P2.
  final void Function(int seq)? onJumpToSeq;

  final bool initiallyExpanded;

  const RunReportCard({
    super.key,
    required this.digest,
    this.staleSince,
    this.live = false,
    this.onJumpToSeq,
    this.initiallyExpanded = true,
  });

  @override
  State<RunReportCard> createState() => _RunReportCardState();
}

class _RunReportCardState extends State<RunReportCard> {
  late bool _expanded = widget.initiallyExpanded;

  int _int(String key) {
    final v = widget.digest[key];
    if (v is int) return v;
    if (v is double) return v.toInt();
    if (v is String) return int.tryParse(v) ?? 0;
    return 0;
  }

  double _double(String key) {
    final v = widget.digest[key];
    if (v is num) return v.toDouble();
    if (v is String) return double.tryParse(v) ?? 0;
    return 0;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;

    final events = _int('event_count');
    final turns = _int('turn_count');
    final errors = _int('error_count');
    final toolTotal = _int('tool_total');
    final toolFailed = _int('tool_failed');
    final cost = _double('cost_usd');
    final durationMs = _int('duration_ms');
    // active_ms = real running time (sum of turn durations); duration_ms = the
    // full first→last span including idle waits between turns. Prefer active in
    // the headline — it's the "time actually spent running" the operator cares
    // about. Falls back to the span when the hub didn't supply it (older hub).
    final activeMs = _int('active_ms');
    final outcome = (widget.digest['outcome'] ?? '').toString();

    final summary = _summaryLine(
      l10n: l10n,
      outcome: outcome,
      turns: turns,
      durationMs: activeMs > 0 ? activeMs : durationMs,
      cost: cost,
      events: events,
    );
    final (outcomeIcon, outcomeColor) = _outcomeBadge(outcome, errors);

    return Container(
      margin: const EdgeInsets.fromLTRB(12, Spacing.s8, 12, Spacing.s8),
      decoration: BoxDecoration(
        color: cardBg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Header — always visible; the one-line summary when collapsed.
          InkWell(
            borderRadius: BorderRadius.circular(12),
            onTap: () => setState(() => _expanded = !_expanded),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(Spacing.s12, Spacing.s12, Spacing.s8, Spacing.s12),
              child: Row(
                children: [
                  Icon(outcomeIcon, size: 18, color: outcomeColor),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      summary,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: isDark
                            ? DesignColors.textPrimary
                            : DesignColors.textPrimaryLight,
                      ),
                    ),
                  ),
                  if (errors > 0) ...[
                    AppStatusChip(label: l10n.runErrorCountChip(errors), color: DesignColors.error),
                    const SizedBox(width: 6),
                  ],
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 20,
                    color: muted,
                  ),
                ],
              ),
            ),
          ),
          if (_expanded) ...[
            Divider(height: 1, color: border),
            Padding(
              padding: const EdgeInsets.fromLTRB(Spacing.s12, 12, Spacing.s12, Spacing.s12),
              child: _body(context, muted, isDark, events: events,
                  turns: turns, errors: errors, toolTotal: toolTotal,
                  toolFailed: toolFailed, cost: cost, durationMs: durationMs,
                  activeMs: activeMs),
            ),
          ],
        ],
      ),
    );
  }

  Widget _body(
    BuildContext context,
    Color muted,
    bool isDark, {
    required int events,
    required int turns,
    required int errors,
    required int toolTotal,
    required int toolFailed,
    required double cost,
    required int durationMs,
    required int activeMs,
  }) {
    final l10n = AppLocalizations.of(context)!;
    final latency = widget.digest['latency'];
    final p50 = latency is Map ? _numAsInt(latency['p50_ms']) : 0;
    final p95 = latency is Map ? _numAsInt(latency['p95_ms']) : 0;
    final byModel = widget.digest['by_model'];
    final firstErrorSeq = _firstErrorSeq();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Wrap(
          spacing: 18,
          runSpacing: 12,
          children: [
            _Stat(label: l10n.statEvents, value: '$events', muted: muted),
            _Stat(label: l10n.statTurns, value: '$turns', muted: muted),
            // Real running time (sum of turn durations) — what the operator
            // means by "time spent". Shown when the hub supplies it.
            if (activeMs > 0)
              _Stat(
                  label: l10n.statActive,
                  value: _fmtDuration(activeMs),
                  muted: muted),
            // Full wall-clock span (first→last event, idle gaps included).
            _Stat(
                label: activeMs > 0 ? l10n.statElapsed : l10n.statDuration,
                value: _fmtDuration(durationMs),
                muted: muted),
            _Stat(
                label: l10n.statCost,
                value: cost > 0 ? '\$${cost.toStringAsFixed(2)}' : '—',
                muted: muted),
            _Stat(
              label: l10n.statTools,
              value: toolTotal > 0
                  ? '${toolTotal - toolFailed}/$toolTotal'
                  : '—',
              muted: muted,
              valueColor: toolFailed > 0 ? DesignColors.warning : null,
            ),
            _Stat(
              label: l10n.statErrors,
              value: '$errors',
              muted: muted,
              valueColor: errors > 0 ? DesignColors.error : null,
              onTap: (errors > 0 && firstErrorSeq != null &&
                      widget.onJumpToSeq != null)
                  ? () => widget.onJumpToSeq!(firstErrorSeq)
                  : null,
            ),
            if (p50 > 0 || p95 > 0)
              _Stat(
                  label: l10n.statLatency,
                  value: '${_fmtDuration(p50)} / ${_fmtDuration(p95)}',
                  muted: muted),
          ],
        ),
        if (byModel is Map && byModel.isNotEmpty) ...[
          const SizedBox(height: 14),
          Text(l10n.statModels,
              style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  letterSpacing: 0.4,
                  color: muted)),
          const SizedBox(height: 6),
          ...byModel.entries.map((e) => _modelRow(l10n, e.key.toString(),
              e.value is Map ? (e.value as Map).cast<String, dynamic>() : const {},
              muted, isDark)),
        ],
        const SizedBox(height: 12),
        _footer(l10n, muted),
      ],
    );
  }

  Widget _modelRow(AppLocalizations l10n, String model,
      Map<String, dynamic> m, Color muted, bool isDark) {
    final inTok = _numAsInt(m['in']);
    final outTok = _numAsInt(m['out']);
    return Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Row(
        children: [
          Expanded(
            child: Text(model,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                    fontSize: 12,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight)),
          ),
          Text(l10n.runModelTokens(_fmtTokens(inTok), _fmtTokens(outTok)),
              style: TextStyle(fontSize: 11, color: muted)),
        ],
      ),
    );
  }

  Widget _footer(AppLocalizations l10n, Color muted) {
    final lastTs = (widget.digest['last_ts'] ?? '').toString();
    final parts = <String>[];
    if (widget.live) {
      parts.add(l10n.runFooterLive);
    }
    if (widget.staleSince != null) {
      parts.add(l10n.runFooterCached);
    }
    final when = _fmtClock(lastTs);
    final label = [
      if (when.isNotEmpty) l10n.runFooterAsOf(when),
      ...parts,
    ].join(' · ');
    if (label.isEmpty) return const SizedBox.shrink();
    return Row(
      children: [
        Icon(widget.live ? Icons.sync : Icons.history,
            size: 12, color: muted),
        const SizedBox(width: 5),
        Text(label, style: TextStyle(fontSize: 11, color: muted)),
      ],
    );
  }

  // The first error anchor as a session_ordinal (ADR-042) — unique across a
  // resumed session's agents. Falls back to the per-agent sample_seqs for
  // pre-migration digests (single-agent runs, where seq is unambiguous).
  int? _firstErrorSeq() {
    final errs = widget.digest['errors'];
    if (errs is! Map) return null;
    int? best;
    for (final v in errs.values) {
      if (v is! Map) continue;
      final ords = v['sample_ordinals'];
      final samples = (ords is List && ords.isNotEmpty) ? ords : v['sample_seqs'];
      if (samples is List && samples.isNotEmpty) {
        final s = _numAsInt(samples.first);
        if (best == null || s < best) best = s;
      }
    }
    return best;
  }

  String _summaryLine({
    required AppLocalizations l10n,
    required String outcome,
    required int turns,
    required int durationMs,
    required double cost,
    required int events,
  }) {
    if (events == 0) return l10n.runNoActivity;
    final bits = <String>[
      if (outcome.isNotEmpty) outcome,
      l10n.runTurnsCount(turns),
      _fmtDuration(durationMs),
      if (cost > 0) '\$${cost.toStringAsFixed(2)}',
    ];
    return bits.join(' · ');
  }

  (IconData, Color) _outcomeBadge(String outcome, int errors) {
    switch (outcome) {
      case 'done':
        return (Icons.check_circle_outline, DesignColors.success);
      case 'cancelled':
        return (Icons.cancel_outlined, DesignColors.textMuted);
      case 'blocked':
        return (Icons.block, DesignColors.warning);
      case 'error':
        return (Icons.error_outline, DesignColors.error);
    }
    if (errors > 0) {
      return (Icons.warning_amber_rounded, DesignColors.warning);
    }
    return (Icons.assessment_outlined, DesignColors.primary);
  }
}

int _numAsInt(dynamic v) {
  if (v is int) return v;
  if (v is double) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

String _fmtDuration(int ms) {
  if (ms <= 0) return '—';
  final s = ms ~/ 1000;
  if (s < 60) return '${s}s';
  final m = s ~/ 60;
  final rem = s % 60;
  if (m < 60) return rem == 0 ? '${m}m' : '${m}m${rem}s';
  final h = m ~/ 60;
  final mm = m % 60;
  return mm == 0 ? '${h}h' : '${h}h${mm}m';
}

String _fmtTokens(int n) {
  if (n < 1000) return '$n';
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(n < 10000 ? 1 : 0)}k';
  return '${(n / 1000000).toStringAsFixed(1)}M';
}

/// HH:MM from an ISO timestamp; empty if unparseable.
String _fmtClock(String iso) {
  if (iso.isEmpty) return '';
  final t = DateTime.tryParse(iso);
  if (t == null) return '';
  final l = t.toLocal();
  final h = l.hour.toString().padLeft(2, '0');
  final m = l.minute.toString().padLeft(2, '0');
  return '$h:$m';
}

class _Stat extends StatelessWidget {
  final String label;
  final String value;
  final Color muted;
  final Color? valueColor;
  final VoidCallback? onTap;

  const _Stat({
    required this.label,
    required this.value,
    required this.muted,
    this.valueColor,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final body = Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(value,
            style: TextStyle(
              fontSize: 16,
              fontWeight: FontWeight.w700,
              color: valueColor ??
                  (isDark
                      ? DesignColors.textPrimary
                      : DesignColors.textPrimaryLight),
            )),
        const SizedBox(height: 2),
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(label,
                style: TextStyle(
                    fontSize: FontSizes.label, letterSpacing: 0.3, color: muted)),
            if (onTap != null) ...[
              const SizedBox(width: 3),
              Icon(Icons.my_location, size: 11, color: muted),
            ],
          ],
        ),
      ],
    );
    if (onTap == null) return body;
    return InkWell(
      onTap: onTap,
      borderRadius: Radii.smBorder,
      child: Padding(padding: const EdgeInsets.all(2), child: body),
    );
  }
}


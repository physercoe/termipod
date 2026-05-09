import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/insights_provider.dart';
import '../theme/design_colors.dart';

/// First Tier-2 drilldown — engine + model breakdown
/// (insights-phase-2.md W5a). Reads `by_engine` and `by_model` from
/// the same `/v1/insights` response the [InsightsPanel] already
/// fetched, so this is pure-mobile cost; no second hub round-trip.
///
/// Two rolled-up sections:
///   - **Engine arbitrage** — one row per `agents.kind` (claude-code,
///     gemini-cli, codex). Tokens, turns, tokens/turn. The per-turn
///     token volume is the actionable signal: a steady tokens/turn at
///     a per-engine price differential is what "should I pivot to a
///     cheaper engine" asks.
///   - **Model breakdown** — same rows, but split by model name as
///     reported in `agent_events` payloads (claude-opus-4-7,
///     gemini-3-flash-preview, …). Mostly answers "where in the
///     mixed-model engine is the spend going."
///
/// Empty maps render an inline empty-state instead of an empty card,
/// since blank cards under a "BREAKDOWN" header look broken.
class InsightsBreakdownSection extends ConsumerWidget {
  final InsightsScope scope;
  const InsightsBreakdownSection({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(insightsProvider(scope));
    final body = async.value?.body;
    if (body == null) return const SizedBox.shrink();

    final byEngineRaw = body['by_engine'];
    final byModelRaw = body['by_model'];
    final engineRows = _parseAggMap(byEngineRaw);
    final modelRows = _parseAggMap(byModelRaw);

    if (engineRows.isEmpty && modelRows.isEmpty) {
      return const SizedBox.shrink();
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (engineRows.isNotEmpty) ...[
          const _Header(label: 'BY ENGINE'),
          _BreakdownTable(rows: engineRows),
          const SizedBox(height: 16),
        ],
        if (modelRows.isNotEmpty) ...[
          const _Header(label: 'BY MODEL'),
          _BreakdownTable(rows: modelRows),
        ],
      ],
    );
  }

  /// `by_engine` / `by_model` are JSON object maps from name → agg.
  /// We sort descending by total tokens so the dominant cost center
  /// is on top — the "first thing your eye lands on should be where
  /// the money is going" rule.
  List<_BreakdownRow> _parseAggMap(Object? raw) {
    if (raw is! Map) return const [];
    final out = <_BreakdownRow>[];
    raw.forEach((k, v) {
      if (v is! Map) return;
      final m = v.cast<String, dynamic>();
      final tokensIn = _int(m['tokens_in']);
      final tokensOut = _int(m['tokens_out']);
      final turns = _int(m['turns']);
      final total = tokensIn + tokensOut;
      if (total == 0 && turns == 0) return;
      out.add(_BreakdownRow(
        name: k.toString(),
        tokens: total,
        turns: turns,
      ));
    });
    out.sort((a, b) => b.tokens.compareTo(a.tokens));
    return out;
  }
}

class _BreakdownRow {
  final String name;
  final int tokens;
  final int turns;
  const _BreakdownRow({
    required this.name,
    required this.tokens,
    required this.turns,
  });

  /// Tokens/turn — proxy for "how chatty is each turn." Once the
  /// pricing table lands (post-MVP per ADR-022) this becomes the
  /// numerator of $/turn; for now token density is the comparable
  /// metric across engines.
  int get tokensPerTurn => turns > 0 ? (tokens / turns).round() : 0;
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

class _BreakdownTable extends StatelessWidget {
  final List<_BreakdownRow> rows;
  const _BreakdownTable({required this.rows});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    final maxTokens =
        rows.fold<int>(0, (m, r) => r.tokens > m ? r.tokens : m);

    return Container(
      decoration: BoxDecoration(
        color: cardBg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      padding: const EdgeInsets.fromLTRB(14, 8, 14, 8),
      child: Column(
        children: [
          for (var i = 0; i < rows.length; i++) ...[
            if (i > 0)
              Divider(
                  height: 12, thickness: 1, color: border.withValues(alpha: 0.5)),
            _Row(
              row: rows[i],
              maxTokens: maxTokens,
              muted: muted,
            ),
          ],
        ],
      ),
    );
  }
}

class _Row extends StatelessWidget {
  final _BreakdownRow row;
  final int maxTokens;
  final Color muted;
  const _Row({
    required this.row,
    required this.maxTokens,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    final share = maxTokens == 0 ? 0.0 : row.tokens / maxTokens;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  row.name,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Text(
                _human(row.tokens),
                style: GoogleFonts.jetBrainsMono(
                    fontSize: 11, fontWeight: FontWeight.w700),
              ),
            ],
          ),
          const SizedBox(height: 4),
          // Visual share bar — relative to the max in the section so
          // the dominant row maxes out and the rest scale proportionally.
          ClipRRect(
            borderRadius: BorderRadius.circular(2),
            child: LinearProgressIndicator(
              minHeight: 4,
              value: share.clamp(0.0, 1.0),
              backgroundColor: muted.withValues(alpha: 0.15),
              valueColor:
                  const AlwaysStoppedAnimation(DesignColors.primary),
            ),
          ),
          const SizedBox(height: 3),
          Text(
            row.turns > 0
                ? '${row.turns} turns · ${_human(row.tokensPerTurn)}/turn'
                : 'no turns',
            style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
          ),
        ],
      ),
    );
  }
}

int _int(Object? v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

String _human(int n) {
  if (n < 1000) return n.toString();
  if (n < 10000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(0)}k';
  return '${(n / 1000000).toStringAsFixed(1)}M';
}

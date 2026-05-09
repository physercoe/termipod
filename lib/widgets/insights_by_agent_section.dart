import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/insights_provider.dart';
import '../screens/insights/insights_screen.dart';
import '../services/steward_handle.dart';
import '../theme/design_colors.dart';

/// Per-agent breakdown — one row per agent active in the scope, tap
/// any row to drill into that single agent's InsightsScreen.
///
/// Reads `by_agent` from the same `/v1/insights` response the panel
/// already fetched (no second hub round-trip). The hub omits the
/// dimension on agent scope (degenerate); on every other scope —
/// project / team / teamStewards / engine / host — rows are sorted
/// by tokens_in desc so the highest spender appears first.
///
/// Steward-handle agents get a small badge so the steward fleet stays
/// visually distinct from worker agents on a mixed list.
class InsightsByAgentSection extends ConsumerWidget {
  final InsightsScope scope;
  const InsightsByAgentSection({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(insightsProvider(scope));
    final body = async.value?.body;
    if (body == null) return const SizedBox.shrink();

    final raw = body['by_agent'];
    if (raw is! List || raw.isEmpty) return const SizedBox.shrink();

    final rows = <_AgentRow>[];
    for (final entry in raw) {
      if (entry is! Map) continue;
      final m = entry.cast<String, dynamic>();
      final tokensIn = _int(m['tokens_in']);
      final tokensOut = _int(m['tokens_out']);
      final turns = _int(m['turns']);
      final errors = _int(m['errors']);
      final agentId = (m['agent_id'] ?? '').toString();
      if (agentId.isEmpty) continue;
      rows.add(_AgentRow(
        agentId: agentId,
        handle: (m['handle'] ?? '').toString(),
        engine: (m['engine'] ?? '').toString(),
        status: (m['status'] ?? '').toString(),
        tokens: tokensIn + tokensOut,
        turns: turns,
        errors: errors,
      ));
    }
    if (rows.isEmpty) return const SizedBox.shrink();

    return Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const _Header(label: 'BY AGENT'),
          _AgentTable(rows: rows),
        ],
      ),
    );
  }
}

class _AgentRow {
  final String agentId;
  final String handle;
  final String engine;
  final String status;
  final int tokens;
  final int turns;
  final int errors;

  const _AgentRow({
    required this.agentId,
    required this.handle,
    required this.engine,
    required this.status,
    required this.tokens,
    required this.turns,
    required this.errors,
  });

  bool get isSteward => isStewardHandle(handle);

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

class _AgentTable extends StatelessWidget {
  final List<_AgentRow> rows;
  const _AgentTable({required this.rows});

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
      padding: const EdgeInsets.fromLTRB(4, 4, 4, 4),
      child: Column(
        children: [
          for (var i = 0; i < rows.length; i++) ...[
            if (i > 0)
              Divider(
                  height: 1,
                  thickness: 1,
                  color: border.withValues(alpha: 0.5)),
            _AgentTile(
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

class _AgentTile extends StatelessWidget {
  final _AgentRow row;
  final int maxTokens;
  final Color muted;
  const _AgentTile({
    required this.row,
    required this.maxTokens,
    required this.muted,
  });

  @override
  Widget build(BuildContext context) {
    final share = maxTokens == 0 ? 0.0 : row.tokens / maxTokens;
    final label = row.handle.isEmpty ? row.agentId : row.handle;
    final running = row.status == 'running';
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () {
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => InsightsScreen(
            scope: InsightsScope.agent(row.agentId),
          ),
        ));
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  width: 6,
                  height: 6,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: running
                        ? DesignColors.success
                        : muted.withValues(alpha: 0.6),
                  ),
                ),
                const SizedBox(width: 8),
                if (row.isSteward)
                  Padding(
                    padding: const EdgeInsets.only(right: 6),
                    child: Icon(
                      Icons.support_agent_outlined,
                      size: 14,
                      color: DesignColors.primary,
                    ),
                  ),
                Expanded(
                  child: Text(
                    label,
                    overflow: TextOverflow.ellipsis,
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
                const SizedBox(width: 6),
                Icon(Icons.chevron_right, size: 16, color: muted),
              ],
            ),
            const SizedBox(height: 4),
            ClipRRect(
              borderRadius: BorderRadius.circular(2),
              child: LinearProgressIndicator(
                minHeight: 3,
                value: share.clamp(0.0, 1.0),
                backgroundColor: muted.withValues(alpha: 0.15),
                valueColor:
                    const AlwaysStoppedAnimation(DesignColors.primary),
              ),
            ),
            const SizedBox(height: 3),
            Text(
              _subtitle(row),
              style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
            ),
          ],
        ),
      ),
    );
  }

  String _subtitle(_AgentRow row) {
    final parts = <String>[];
    if (row.engine.isNotEmpty && row.engine != 'unknown') {
      parts.add(row.engine);
    }
    if (row.turns > 0) {
      parts.add('${row.turns} turns · ${_human(row.tokensPerTurn)}/turn');
    } else {
      parts.add('no turns');
    }
    if (row.errors > 0) {
      parts.add('${row.errors} failed');
    }
    return parts.join(' · ');
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

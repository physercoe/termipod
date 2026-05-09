import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/insights_provider.dart';
import '../theme/design_colors.dart';

/// Tool-call efficiency section — Phase 2 W5c of insights-phase-2.md.
/// Renders the `tools` block from `/v1/insights`:
///
///   - tool_calls — total `agent_events.kind='tool_call'` rows in scope.
///   - tools_per_turn — proxy for "how chatty is each turn." Aligns
///     with the engine arbitrage signal in W5a; useful when comparing
///     engine A vs engine B on the same project.
///   - approval_rate — % of resolved approval_requests that the
///     director approved. Operational signal: a low rate suggests the
///     gate is firing too aggressively (or the agent is doing things
///     the principal doesn't want).
///
/// Hides itself when the scope produced zero tool calls AND zero
/// approvals — empty-state cards under a tool-call header read as
/// broken UI rather than as "no traffic."
class InsightsToolsSection extends ConsumerWidget {
  final InsightsScope scope;
  const InsightsToolsSection({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(insightsProvider(scope));
    final body = async.value?.body;
    if (body == null) return const SizedBox.shrink();

    final tools = body['tools'];
    if (tools is! Map) return const SizedBox.shrink();
    final t = tools.cast<String, dynamic>();
    final toolCalls = _int(t['tool_calls']);
    final toolsPerTurn = _double(t['tools_per_turn']);
    final approvalsTotal = _int(t['approvals_total']);
    final approvalsApproved = _int(t['approvals_approved']);
    final approvalRate = _double(t['approval_rate']);

    if (toolCalls == 0 && approvalsTotal == 0) {
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
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 8),
            child: Text(
              'TOOL CALLS',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                color: muted,
                letterSpacing: 0.8,
              ),
            ),
          ),
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
                _StatRow(
                  label: 'Total calls',
                  value: _human(toolCalls),
                  muted: muted,
                ),
                _StatRow(
                  label: 'Per turn',
                  value: toolsPerTurn > 0
                      ? toolsPerTurn.toStringAsFixed(2)
                      : '—',
                  muted: muted,
                ),
                if (approvalsTotal > 0) ...[
                  Divider(
                    height: 16,
                    thickness: 1,
                    color: border.withValues(alpha: 0.5),
                  ),
                  _StatRow(
                    label: 'Approvals',
                    value:
                        '$approvalsApproved/$approvalsTotal approved',
                    muted: muted,
                  ),
                  _StatRow(
                    label: 'Approval rate',
                    value: '${(approvalRate * 100).toStringAsFixed(0)}%',
                    muted: muted,
                    accent: _approvalAccent(approvalRate),
                  ),
                  const SizedBox(height: 6),
                  ClipRRect(
                    borderRadius: BorderRadius.circular(2),
                    child: LinearProgressIndicator(
                      minHeight: 4,
                      value: approvalRate.clamp(0.0, 1.0),
                      backgroundColor: muted.withValues(alpha: 0.15),
                      valueColor: AlwaysStoppedAnimation(
                          _approvalAccent(approvalRate)),
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  /// Lower approval rate = louder warning; gate firings the user
  /// rejects suggest the agent's policy is misaligned.
  static Color _approvalAccent(double rate) {
    if (rate >= 0.85) return DesignColors.success;
    if (rate >= 0.5) return DesignColors.warning;
    return DesignColors.error;
  }
}

class _StatRow extends StatelessWidget {
  final String label;
  final String value;
  final Color muted;
  final Color? accent;
  const _StatRow({
    required this.label,
    required this.value,
    required this.muted,
    this.accent,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          SizedBox(
            width: 110,
            child: Text(
              label,
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                fontWeight: FontWeight.w700,
                color: accent,
              ),
            ),
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

double _double(Object? v) {
  if (v is double) return v;
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0.0;
  return 0.0;
}

String _human(int n) {
  if (n < 1000) return n.toString();
  if (n < 10000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(0)}k';
  return '${(n / 1000000).toStringAsFixed(1)}M';
}

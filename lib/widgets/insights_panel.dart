import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/insights_provider.dart';
import '../theme/design_colors.dart';

// Re-export the scope value object so callers that already imported
// insights_panel.dart can construct an `InsightsScope.project(...)`
// without a second import. Keeps the call-site one-liner clean.
export '../providers/insights_provider.dart' show InsightsScope, InsightsScopeKind;

/// Five Tier-1 tiles glanced into a detail screen's Overview.
/// ADR-022 D3 + insights-phase-1.md §3 W2 + insights-phase-2 W1:
/// spend / latency / errors / concurrency at the supplied scope.
/// Cache-first per ADR-006 — render the snapshot immediately, fire
/// the live request in the background, swap when it lands. Stale
/// banner appears when the cached body is the only thing we have to
/// show.
///
/// Project Detail passes `InsightsScope.project(...)` today; future
/// callers (Hosts Detail, Agent Detail per Phase 2 W4) pass their own
/// scope without the panel needing to know.
class InsightsPanel extends ConsumerWidget {
  final InsightsScope scope;

  const InsightsPanel({super.key, required this.scope});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (scope.isEmpty) return const SizedBox.shrink();
    final async = ref.watch(insightsProvider(scope));
    final value = async.value;
    final body = value?.body;

    if (body == null) {
      // First load (no cache): keep height predictable so the section
      // below doesn't pop in. Loading state is silent — the rest of the
      // Overview is already rendering.
      return const SizedBox.shrink();
    }

    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final spend = body['spend'] as Map?;
    final latency = body['latency'] as Map?;
    final errors = body['errors'] as Map?;
    final concurrency = body['concurrency'] as Map?;

    final showStale = (value?.staleSince) != null &&
        DateTime.now().difference(value!.staleSince!) > const Duration(seconds: 60);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(left: 4, bottom: 6),
          child: Row(
            children: [
              Icon(Icons.insights_outlined, size: 16, color: muted),
              const SizedBox(width: 6),
              Text(
                'INSIGHTS · 24h',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.6,
                  color: muted,
                ),
              ),
              if (showStale) ...[
                const SizedBox(width: 8),
                Text(
                  '· stale',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: muted,
                  ),
                ),
              ],
            ],
          ),
        ),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            _MetricTile(
              label: 'tokens',
              primary: _shortNum(_int(spend?['tokens_in']) +
                  _int(spend?['tokens_out'])),
              secondary:
                  '${_shortNum(_int(spend?['tokens_in']))}↓ · ${_shortNum(_int(spend?['tokens_out']))}↑',
            ),
            _MetricTile(
              label: 'cache hits',
              primary: _shortNum(_int(spend?['cache_read'])),
              secondary: '${_shortNum(_int(spend?['cache_create']))} new',
            ),
            _MetricTile(
              label: 'turn p95',
              primary: _msHuman(_int(latency?['turn_p95_ms'])),
              secondary: 'p50 ${_msHuman(_int(latency?['turn_p50_ms']))}',
            ),
            _MetricTile(
              label: 'errors',
              primary: _int(errors?['failed_turns']).toString(),
              secondary: '${_int(errors?['open_attention'])} attention',
              warn: _int(errors?['failed_turns']) > 0,
            ),
            _MetricTile(
              label: 'live',
              primary: _int(concurrency?['active_agents']).toString(),
              secondary:
                  '${_int(concurrency?['open_sessions'])} sessions',
            ),
          ],
        ),
      ],
    );
  }
}

class _MetricTile extends StatelessWidget {
  final String label;
  final String primary;
  final String secondary;
  final bool warn;
  const _MetricTile({
    required this.label,
    required this.primary,
    required this.secondary,
    this.warn = false,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final cardBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final accent = warn ? DesignColors.error : DesignColors.primary;
    return Container(
      width: 104,
      padding: const EdgeInsets.fromLTRB(10, 8, 10, 9),
      decoration: BoxDecoration(
        color: cardBg,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label.toUpperCase(),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 9,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.6,
              color: muted,
            ),
          ),
          const SizedBox(height: 2),
          Text(
            primary,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 18,
              fontWeight: FontWeight.w700,
              color: accent,
            ),
          ),
          const SizedBox(height: 1),
          Text(
            secondary,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: muted,
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

String _shortNum(int n) {
  if (n < 1000) return n.toString();
  if (n < 10000) return '${(n / 1000).toStringAsFixed(1)}k';
  if (n < 1000000) return '${(n / 1000).toStringAsFixed(0)}k';
  return '${(n / 1000000).toStringAsFixed(1)}M';
}

String _msHuman(int ms) {
  if (ms <= 0) return '—';
  if (ms < 1000) return '${ms}ms';
  if (ms < 10000) return '${(ms / 1000).toStringAsFixed(1)}s';
  return '${(ms / 1000).toStringAsFixed(0)}s';
}

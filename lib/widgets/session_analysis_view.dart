import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/session_digest_provider.dart';
import '../theme/design_colors.dart';
import 'agent_feed.dart';
import 'run_report_card.dart';

/// The analysis surface (agent-run-analysis-mode plan P1): a foldable
/// run-report dashboard (from the session **digest** — ADR-038 §5) over the
/// full-screen navigable transcript. Insight *is* analysis, so this is the
/// `View ▾ → Insights` body — no separate route. Available for any run; the
/// report card labels itself "as of `<ts>` · live" while the run is ongoing.
///
/// The dashboard is foldable so the log reclaims height; the log is the same
/// `AgentFeed(dense: false)` the Feed tab renders, here driven by the digest
/// (true event count, full-run errors) rather than the loaded window. The
/// random-access loader + filtered views land in P2.
class SessionAnalysisView extends ConsumerWidget {
  final String agentId;
  final String sessionId;

  /// True while the run is live/idle (not terminated) — passed to the
  /// report card's "as of `<ts>` · live" affordance.
  final bool live;

  const SessionAnalysisView({
    super.key,
    required this.agentId,
    required this.sessionId,
    this.live = false,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final digest = ref.watch(sessionDigestProvider(sessionId));

    final card = digest.when(
      loading: () => const SizedBox.shrink(),
      error: (_, __) => const SizedBox.shrink(),
      data: (state) {
        final body = state.body;
        if (body == null || body.isEmpty) return const SizedBox.shrink();
        return RunReportCard(
          digest: body,
          staleSince: state.staleSince,
          live: live,
        );
      },
    );

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        card,
        Divider(height: 1, color: border),
        Expanded(
          child: RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(sessionDigestProvider(sessionId));
              await ref.read(sessionDigestProvider(sessionId).future);
            },
            child: AgentFeed(
              agentId: agentId,
              sessionId: sessionId,
              dense: false,
            ),
          ),
        ),
      ],
    );
  }
}

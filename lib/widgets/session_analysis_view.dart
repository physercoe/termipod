import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/session_digest_provider.dart';
import '../theme/design_colors.dart';
import 'agent_feed.dart';
import 'run_report_card.dart';

/// The analysis surface (agent-run-analysis-mode plan P1/P2): a foldable
/// run-report dashboard (from the session **digest** — ADR-038 §5) over the
/// full-screen navigable transcript. Insight *is* analysis, so this is the
/// `View ▾ → Insights` body — no separate route. Available for any run; the
/// report card labels itself "as of `<ts>` · live" while the run is ongoing.
///
/// The dashboard is foldable so the log reclaims height; the log is the same
/// `AgentFeed(dense: false)` the Feed tab renders, here driven by the digest
/// (true event count, full-run errors) rather than the loaded window.
///
/// P2 — the dashboard and the feed are siblings, so a tapped dashboard stat
/// (the Errors stat → the first error's seq) drives the transcript through an
/// [AgentFeedSeekController]: the card requests a jump, the feed pages toward
/// the anchor and highlights it. The random-access loader + filtered views
/// land alongside.
class SessionAnalysisView extends ConsumerStatefulWidget {
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
  ConsumerState<SessionAnalysisView> createState() =>
      _SessionAnalysisViewState();
}

class _SessionAnalysisViewState extends ConsumerState<SessionAnalysisView> {
  // The jump channel from the dashboard down into the feed. Owned here so
  // both the RunReportCard (requester) and the AgentFeed (responder) share
  // one instance for the view's lifetime.
  final AgentFeedSeekController _seek = AgentFeedSeekController();

  @override
  void dispose() {
    _seek.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final digest = ref.watch(sessionDigestProvider(widget.sessionId));

    final card = digest.when(
      loading: () => const SizedBox.shrink(),
      error: (_, __) => const SizedBox.shrink(),
      data: (state) {
        final body = state.body;
        if (body == null || body.isEmpty) return const SizedBox.shrink();
        return RunReportCard(
          digest: body,
          staleSince: state.staleSince,
          live: widget.live,
          // Tapping the Errors stat jumps the transcript below to the
          // first error anchor (plan P2).
          onJumpToSeq: _seek.seekTo,
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
              ref.invalidate(sessionDigestProvider(widget.sessionId));
              await ref.read(sessionDigestProvider(widget.sessionId).future);
            },
            child: AgentFeed(
              agentId: widget.agentId,
              sessionId: widget.sessionId,
              dense: false,
              seekController: _seek,
            ),
          ),
        ),
      ],
    );
  }
}

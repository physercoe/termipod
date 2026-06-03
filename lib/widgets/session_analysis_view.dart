import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/session_digest_provider.dart';
import '../providers/session_turns_provider.dart';
import '../theme/design_colors.dart';
import 'insight_transcript.dart';
import 'run_report_card.dart';
import 'transcript/seek_controller.dart';

/// The analysis surface (agent-run-analysis-mode plan P1/P2): a foldable
/// run-report dashboard (from the session **digest** — ADR-038 §5) over the
/// full-screen navigable transcript. Insight *is* analysis, so this is the
/// `View ▾ → Insights` body — no separate route. Available for any run; the
/// report card labels itself "as of `<ts>` · live" while the run is ongoing.
///
/// The dashboard is foldable so the log reclaims height; the log is the
/// [InsightTranscript] — the sealed / random-access transcript mode (ADR-040),
/// driven by the digest (true event count, full-run errors) rather than the
/// loaded window.
///
/// P2 — the dashboard and the transcript are siblings, so a tapped dashboard
/// stat (the Errors stat → the first error's seq) drives the transcript through
/// a [TranscriptSeekController]: the card requests a jump, the transcript
/// window-resets around the anchor and highlights it. The random-access loader
/// + filtered views land alongside.
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
  // The jump channel from the dashboard down into the transcript. Owned here so
  // both the RunReportCard (requester) and the InsightTranscript (responder)
  // share one instance for the view's lifetime.
  final TranscriptSeekController _seek = TranscriptSeekController();

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
    final turns = ref.watch(sessionTurnsProvider(widget.sessionId));

    // The run-lifetime total drives the feed's "event N of M" position; null
    // until the digest resolves (the feed falls back to the loaded max).
    final digestBody =
        digest.maybeWhen(data: (s) => s.body, orElse: () => null);
    final totalEvents = (digestBody?['event_count'] as num?)?.toInt();

    // Full-run minimap anchors: every error in the run (the digest's per-class
    // sample seqs) + every turn start (the turn index). The minimap renders
    // these whole-run, and a tap jumps to a seq that may not be loaded yet.
    // seq → ts for every anchor that carries a timestamp, so a minimap tick or
    // the Errors stat can jump via the (ts, seq) window reset (O(log n)) rather
    // than the bounded page-walk. Turn starts carry start_ts; errors now carry
    // sample_ts aligned 1:1 with sample_seqs (older digests are seq-only → those
    // anchors fall back to the page-walk).
    final runAnchorTs = <int, String>{};
    final runErrorSeqs = <int>[];
    // seq → error class (tool_error / failed_turn / error:<type>), so the
    // Errors lens can render the whole-run error list straight from the digest
    // with no event-body fetch (ADR-039 P2). Iterate entries (not values) to
    // keep the class key.
    final runErrorClasses = <int, String>{};
    // seq → per-error headline label (the failing tool's name, the error type,
    // or "" for a failed turn — digest schema v3 `sample_labels`). Lets the
    // Errors lens headline a row with "Bash" instead of the generic class.
    final runErrorLabels = <int, String>{};
    final errs = digestBody?['errors'];
    if (errs is Map) {
      for (final entry in errs.entries) {
        final cls = entry.key.toString();
        final v = entry.value;
        if (v is! Map) continue;
        final seqs = v['sample_seqs'];
        if (seqs is! List) continue;
        final tss = v['sample_ts'];
        final labels = v['sample_labels'];
        for (var i = 0; i < seqs.length; i++) {
          final s = seqs[i];
          if (s is! num) continue;
          final seq = s.toInt();
          runErrorSeqs.add(seq);
          runErrorClasses[seq] = cls;
          if (tss is List && i < tss.length) {
            final ts = (tss[i] ?? '').toString();
            if (ts.isNotEmpty) runAnchorTs[seq] = ts;
          }
          if (labels is List && i < labels.length) {
            final label = (labels[i] ?? '').toString();
            if (label.isNotEmpty) runErrorLabels[seq] = label;
          }
        }
      }
    }
    final turnRows = turns.maybeWhen(
      data: (rows) => rows,
      orElse: () => const <Map<String, dynamic>>[],
    );
    final runTurnSeqs = <int>[];
    for (final r in turnRows) {
      final seq = (r['start_seq'] as num?)?.toInt() ?? 0;
      if (seq <= 0) continue;
      runTurnSeqs.add(seq);
      final ts = (r['start_ts'] ?? '').toString();
      if (ts.isNotEmpty) runAnchorTs[seq] = ts;
    }

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
          // Tapping the Errors stat jumps the transcript below to the first
          // error anchor — now with the error's ts (sample_ts) so it takes the
          // random-access reset, not the page-walk.
          onJumpToSeq: (seq) => _seek.seekTo(seq, ts: runAnchorTs[seq]),
        );
      },
    );

    // The digest-backed turn index is no longer a standalone disclosure row:
    // P5 point 6 folds it into the transcript funnel as the **Turns lens**, so
    // the full turn list (status/duration/tools/errors, tap-to-jump) renders as
    // summary rows like the Errors lens. The rows flow down as `runTurns`; the
    // funnel is the single "filter card view for all the items".

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        card,
        Divider(height: 1, color: border),
        Expanded(
          child: RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(sessionDigestProvider(widget.sessionId));
              ref.invalidate(sessionTurnsProvider(widget.sessionId));
              await ref.read(sessionDigestProvider(widget.sessionId).future);
            },
            child: InsightTranscript(
              agentId: widget.agentId,
              sessionId: widget.sessionId,
              seekController: _seek,
              totalEventCount: totalEvents,
              runErrorSeqs: runErrorSeqs,
              runErrorClasses: runErrorClasses,
              runErrorLabels: runErrorLabels,
              runTurnSeqs: runTurnSeqs,
              runTurns: turnRows,
              runAnchorTs: runAnchorTs,
            ),
          ),
        ),
      ],
    );
  }
}

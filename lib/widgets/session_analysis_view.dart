import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/session_digest_provider.dart';
import '../providers/session_turns_provider.dart';
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
    final turns = ref.watch(sessionTurnsProvider(widget.sessionId));

    // The run-lifetime total drives the feed's "event N of M" position; null
    // until the digest resolves (the feed falls back to the loaded max).
    final digestBody =
        digest.maybeWhen(data: (s) => s.body, orElse: () => null);
    final totalEvents = (digestBody?['event_count'] as num?)?.toInt();

    // Full-run minimap anchors: every error in the run (the digest's per-class
    // sample seqs) + every turn start (the turn index). The minimap renders
    // these whole-run, and a tap jumps to a seq that may not be loaded yet.
    final runErrorSeqs = <int>[];
    final errs = digestBody?['errors'];
    if (errs is Map) {
      for (final v in errs.values) {
        if (v is Map && v['sample_seqs'] is List) {
          for (final s in (v['sample_seqs'] as List)) {
            if (s is num) runErrorSeqs.add(s.toInt());
          }
        }
      }
    }
    final runTurnSeqs = turns.maybeWhen(
      data: (rows) => rows
          .map((r) => (r['start_seq'] as num?)?.toInt() ?? 0)
          .where((s) => s > 0)
          .toList(),
      orElse: () => <int>[],
    );
    // seq → ts for the turn anchors, so a minimap turn-tick tap can take the
    // (ts, seq) window reset (O(log n)) instead of the bounded page-walk. The
    // turn rows already pass their own start_ts through onJump; this gives the
    // minimap the same fast path. Errors are absent (their sample seqs are
    // ts-less) and fall back to the page-walk.
    final runAnchorTs = turns.maybeWhen(
      data: (rows) => <int, String>{
        for (final r in rows)
          if (((r['start_seq'] as num?)?.toInt() ?? 0) > 0 &&
              (r['start_ts'] ?? '').toString().isNotEmpty)
            (r['start_seq'] as num).toInt(): (r['start_ts']).toString(),
      },
      orElse: () => <int, String>{},
    );

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

    // The digest-backed turn index (plan P2): a foldable structure index over
    // the whole run — each row jumps the transcript to the turn's start_seq
    // via the shared seek controller. Full-run complete (the materialized
    // agent_turns rows), not a filter of the loaded window.
    final turnsSection = turns.maybeWhen(
      data: (rows) => rows.isEmpty
          ? const SizedBox.shrink()
          // Carry start_ts so the feed's random-access loader can window
          // around the turn via the (ts, seq) keyset instead of the page-walk.
          : _TurnsDisclosure(
              rows: rows,
              onJump: (seq, ts) => _seek.seekTo(seq, ts: ts),
            ),
      orElse: () => const SizedBox.shrink(),
    );

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        card,
        turnsSection,
        Divider(height: 1, color: border),
        Expanded(
          child: RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(sessionDigestProvider(widget.sessionId));
              ref.invalidate(sessionTurnsProvider(widget.sessionId));
              await ref.read(sessionDigestProvider(widget.sessionId).future);
            },
            child: AgentFeed(
              agentId: widget.agentId,
              sessionId: widget.sessionId,
              dense: false,
              seekController: _seek,
              totalEventCount: totalEvents,
              runErrorSeqs: runErrorSeqs,
              runTurnSeqs: runTurnSeqs,
              runAnchorTs: runAnchorTs,
              // The analysis surface owns the random-access loader; the Feed
              // tab leaves this false and keeps its live-tail loader.
              randomAccess: true,
            ),
          ),
        ),
      ],
    );
  }
}

/// Plan P2 — the digest-backed "Turns" structure index: a collapsed-by-default
/// disclosure (so the log keeps its height) whose rows list every turn in the
/// run and jump the transcript to the turn's `start_seq` on tap. The list is
/// bounded-height with its own scroll so a long run can't push the feed away.
class _TurnsDisclosure extends StatefulWidget {
  final List<Map<String, dynamic>> rows;
  final void Function(int seq, String? ts) onJump;
  const _TurnsDisclosure({required this.rows, required this.onJump});

  @override
  State<_TurnsDisclosure> createState() => _TurnsDisclosureState();
}

class _TurnsDisclosureState extends State<_TurnsDisclosure> {
  bool _open = false;

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final fg =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;

    final header = InkWell(
      onTap: () => setState(() => _open = !_open),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          children: [
            Icon(_open ? Icons.expand_less : Icons.expand_more,
                size: 18, color: muted),
            const SizedBox(width: 6),
            Text('Turns (${widget.rows.length})',
                style: TextStyle(
                    fontSize: 13, fontWeight: FontWeight.w600, color: fg)),
          ],
        ),
      ),
    );

    if (!_open) return header;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        header,
        ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 220),
          child: ListView.builder(
            padding: EdgeInsets.zero,
            shrinkWrap: true,
            itemCount: widget.rows.length,
            itemBuilder: (context, i) => _TurnRow(
              row: widget.rows[i],
              onJump: widget.onJump,
            ),
          ),
        ),
      ],
    );
  }
}

class _TurnRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final void Function(int seq, String? ts) onJump;
  const _TurnRow({required this.row, required this.onJump});

  static int _asInt(Object? v) => (v is num) ? v.toInt() : 0;

  String _fmtDuration(int ms) {
    if (ms <= 0) return '';
    if (ms < 1000) return '${ms}ms';
    final s = ms / 1000.0;
    if (s < 60) return '${s.toStringAsFixed(s < 10 ? 1 : 0)}s';
    final m = (s / 60).floor();
    return '${m}m${(s % 60).round()}s';
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final fg =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;

    final idx = _asInt(row['idx']);
    final startSeq = _asInt(row['start_seq']);
    final startTs = (row['start_ts'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final open = row['open'] == true;
    final durMs = _asInt(row['duration_ms']);
    final toolCount = _asInt(row['tool_count']);
    final toolFailed = _asInt(row['tool_failed']);
    final errorCount = _asInt(row['error_count']);

    final bad = status == 'error' || status == 'failed' || errorCount > 0;
    final dot = open
        ? muted
        : (bad ? DesignColors.error : DesignColors.success);

    final parts = <String>[];
    final dur = _fmtDuration(durMs);
    if (dur.isNotEmpty) parts.add(dur);
    if (toolCount > 0) parts.add('tools ${toolCount - toolFailed}/$toolCount');
    if (open) parts.add('live');

    return InkWell(
      onTap: () => onJump(startSeq, startTs.isEmpty ? null : startTs),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        child: Row(
          children: [
            Container(
              width: 7,
              height: 7,
              margin: const EdgeInsets.only(right: 8),
              decoration: BoxDecoration(color: dot, shape: BoxShape.circle),
            ),
            Text('Turn ${idx + 1}',
                style: TextStyle(
                    fontSize: 12, fontWeight: FontWeight.w500, color: fg)),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                parts.join(' · '),
                style: TextStyle(fontSize: 11, color: muted),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            if (errorCount > 0) ...[
              const SizedBox(width: 6),
              Text('⚠$errorCount',
                  style: const TextStyle(
                      fontSize: 11, color: DesignColors.error)),
            ],
          ],
        ),
      ),
    );
  }
}

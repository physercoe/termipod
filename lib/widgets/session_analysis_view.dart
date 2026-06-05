import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/session_digest_provider.dart';
import '../providers/session_turns_provider.dart';
import '../theme/design_colors.dart';
import 'insight_transcript.dart';
import 'run_report_card.dart';
import 'sessions_rail.dart';
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

  /// Whether to render the left Sessions rail (handle + overlay) inside this
  /// view. False when a host (e.g. the session chat screen) already provides a
  /// screen-level rail across all its views, so this view doesn't double it.
  final bool showRail;

  const SessionAnalysisView({
    super.key,
    required this.agentId,
    required this.sessionId,
    this.live = false,
    this.showRail = true,
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

  // The *active* analysed run. Seeded from the widget, but the left Sessions
  // rail (ADR-041 §4) can retarget it to a project sibling / another session
  // without leaving the surface; the digest/turns providers re-resolve on the
  // new session id and the transcript re-keys.
  late String _agentId = widget.agentId;
  late String _sessionId = widget.sessionId;
  late bool _live = widget.live;
  // The two workbench drawers are owned here so they stay mutually exclusive —
  // only one overlay at a time (ADR-041 §5). Opening either closes the other.
  bool _railOpen = false;
  bool _navigatorOpen = false;

  void _openRail() => setState(() {
        _railOpen = true;
        _navigatorOpen = false;
      });

  void _setNavigatorOpen(bool open) => setState(() {
        _navigatorOpen = open;
        if (open) _railOpen = false;
      });

  @override
  void didUpdateWidget(covariant SessionAnalysisView oldWidget) {
    super.didUpdateWidget(oldWidget);
    // An external navigation (the host pushed a different run into this widget)
    // wins over a rail retarget — re-sync the active target.
    if (oldWidget.agentId != widget.agentId ||
        oldWidget.sessionId != widget.sessionId) {
      _agentId = widget.agentId;
      _sessionId = widget.sessionId;
      _live = widget.live;
    }
  }

  void _retarget(String agentId, String sessionId, bool live) {
    if (sessionId.isEmpty || (agentId == _agentId && sessionId == _sessionId)) {
      return;
    }
    setState(() {
      _agentId = agentId;
      _sessionId = sessionId;
      _live = live;
    });
  }

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
    final digest = ref.watch(sessionDigestProvider(_sessionId));
    final turns = ref.watch(sessionTurnsProvider(_sessionId));

    // The run-lifetime total drives the feed's "event N of M" position; null
    // until the digest resolves (the feed falls back to the loaded max).
    final digestBody =
        digest.maybeWhen(data: (s) => s.body, orElse: () => null);
    final totalEvents = (digestBody?['event_count'] as num?)?.toInt();

    // Full-run navigation anchors, keyed by the dense `session_ordinal`
    // (ADR-042): every error in the run (the digest's per-class sample
    // ordinals) + every turn start (the turn index's start_ordinal). The
    // ordinal is unique across the agents a resumed session spans, so an anchor
    // never collides with another agent's same-numbered seq — the resume/
    // navigator wrong-row bug. Pre-migration digests carry only sample_seqs /
    // start_seq; we fall back to those so an old run still navigates (its
    // single-agent seqs are unambiguous anyway).
    //
    // ordinal → ts for every anchor that carries a timestamp (turn start_ts,
    // error sample_ts) — surfaced for the outline rows' relative-time labels.
    final runAnchorTs = <int, String>{};
    final runErrorOrdinals = <int>[];
    // ordinal → error class (tool_error / failed_turn / error:<type>), so the
    // Errors lens can render the whole-run error list straight from the digest
    // with no event-body fetch (ADR-039 P2). Iterate entries (not values) to
    // keep the class key.
    final runErrorClasses = <int, String>{};
    // ordinal → per-error headline label (the failing tool's name, the error
    // type, or "" for a failed turn — digest schema v3 `sample_labels`). Lets
    // the Errors lens headline a row with "Bash" instead of the generic class.
    final runErrorLabels = <int, String>{};
    final errs = digestBody?['errors'];
    if (errs is Map) {
      for (final entry in errs.entries) {
        final cls = entry.key.toString();
        final v = entry.value;
        if (v is! Map) continue;
        // Prefer the session ordinals; fall back to the per-agent seqs for
        // pre-ADR-042 digests. The two lists are 1:1 with sample_ts/labels.
        final ords = v['sample_ordinals'];
        final seqs = v['sample_seqs'];
        final tss = v['sample_ts'];
        final labels = v['sample_labels'];
        final count = (ords is List)
            ? ords.length
            : (seqs is List ? seqs.length : 0);
        for (var i = 0; i < count; i++) {
          var anchor = 0;
          if (ords is List && i < ords.length && ords[i] is num) {
            anchor = (ords[i] as num).toInt();
          }
          if (anchor <= 0 && seqs is List && i < seqs.length && seqs[i] is num) {
            anchor = (seqs[i] as num).toInt();
          }
          if (anchor <= 0) continue;
          runErrorOrdinals.add(anchor);
          runErrorClasses[anchor] = cls;
          if (tss is List && i < tss.length) {
            final ts = (tss[i] ?? '').toString();
            if (ts.isNotEmpty) runAnchorTs[anchor] = ts;
          }
          if (labels is List && i < labels.length) {
            final label = (labels[i] ?? '').toString();
            if (label.isNotEmpty) runErrorLabels[anchor] = label;
          }
        }
      }
    }
    final turnRows = turns.maybeWhen(
      data: (rows) => rows,
      orElse: () => const <Map<String, dynamic>>[],
    );
    final runTurnOrdinals = <int>[];
    for (final r in turnRows) {
      final ord = (r['start_ordinal'] as num?)?.toInt() ?? 0;
      final seq = (r['start_seq'] as num?)?.toInt() ?? 0;
      final anchor = ord > 0 ? ord : seq; // ordinal, seq fallback (old digests)
      if (anchor <= 0) continue;
      runTurnOrdinals.add(anchor);
      final ts = (r['start_ts'] ?? '').toString();
      if (ts.isNotEmpty) runAnchorTs[anchor] = ts;
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
          live: _live,
          // Tapping the Errors stat jumps the transcript below to the first
          // error anchor (its session_ordinal — ADR-042), driving the dense
          // ordinal-keyset window reset; the ts rides along for the highlight.
          onJumpToSeq: (ord) => _seek.seekTo(ord, ts: runAnchorTs[ord]),
        );
      },
    );

    // The digest-backed turn index is no longer a standalone disclosure row:
    // P5 point 6 folds it into the transcript funnel as the **Turns lens**, so
    // the full turn list (status/duration/tools/errors, tap-to-jump) renders as
    // summary rows like the Errors lens. The rows flow down as `runTurns`; the
    // funnel is the single "filter card view for all the items".

    final column = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        card,
        Divider(height: 1, color: border),
        Expanded(
          child: RefreshIndicator(
            onRefresh: () async {
              ref.invalidate(sessionDigestProvider(_sessionId));
              ref.invalidate(sessionTurnsProvider(_sessionId));
              await ref.read(sessionDigestProvider(_sessionId).future);
            },
            child: InsightTranscript(
              // Re-key on the active run so a rail retarget rebuilds the
              // transcript's `(ts, seq)` buffer fresh for the new session.
              key: ValueKey('$_agentId/$_sessionId'),
              agentId: _agentId,
              sessionId: _sessionId,
              navigatorOpen: _navigatorOpen,
              onNavigatorOpenChanged: _setNavigatorOpen,
              seekController: _seek,
              totalEventCount: totalEvents,
              runErrorOrdinals: runErrorOrdinals,
              runErrorClasses: runErrorClasses,
              runErrorLabels: runErrorLabels,
              runTurnOrdinals: runTurnOrdinals,
              runTurns: turnRows,
              runAnchorTs: runAnchorTs,
            ),
          ),
        ),
      ],
    );

    // When a host provides a screen-level rail across all its views, this view
    // omits its own (no double handle / overlay).
    if (!widget.showRail) return column;

    // The left "Sessions" rail (ADR-041 §4) overlays the surface phone-first: a
    // left-edge pull handle opens a scoped switcher; picking a run retargets
    // the whole view (dashboard + transcript + outline) via [_retarget].
    return Stack(
      children: [
        column,
        // Left-edge pull handle, vertically centred so it clears the dashboard
        // card and the transcript's top-left funnel. Hidden while the Navigator
        // is open so it doesn't float over that drawer's scrim (only one drawer
        // shows at a time — ADR-041 §5).
        if (!_navigatorOpen)
          Positioned(
            left: 0,
            top: 0,
            bottom: 0,
            child: Center(
              child: SessionsRailHandle(
                onTap: _openRail,
              ),
            ),
          ),
        if (_railOpen)
          SessionsRail(
            agentId: _agentId,
            onSelect: _retarget,
            onClose: () => setState(() => _railOpen = false),
          ),
      ],
    );
  }
}

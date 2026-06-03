// InsightTranscript — the sealed / random-access transcript mode (ADR-040 P3).
//
// The Insight surface reads a run as a *sealed dataset*, not a live
// conversation: a snapshot taken on entry, navigated by random access. It owns
// its own event buffer fed by the `(ts, seq)` keyset loader, the seek
// orchestration (anchor + funnel-run jumps + "view in context"), and the
// lens-as-query engine (the whole-run Errors list, the turns/errors funnel, the
// minimap, the N/M ordinal + stepper). It deliberately has **no composer, no
// telemetry strip, and no live SSE tail** — the live feed (`live_feed.dart`,
// renamed `LiveFeed` in P4) owns those; Insight's dashboard is the digest
// RunReportCard rendered by its host. A live run is handled snapshot-on-entry +
// manual refresh (the host's RefreshIndicator re-pulls the digest; the
// transcript re-snapshots on re-entry) — ADR-040 §E.
//
// This is the per-mode decoupling of the former flag-switched `AgentFeed`: the
// random-access code paths that lived behind `widget.randomAccess == true` and
// `widget.dense == false` are lifted here with those flags resolved to their
// Insight values; the live paths stay in `live_feed.dart`. Shared substrate
// (`transcript/`): FoldMaps (cards), TranscriptSeek (landing), RandomAccessLoader
// (the keyset fetch), the FeedLens predicate + funnel/minimap/stepper widgets.
import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';
import 'transcript/event_card.dart';
import 'transcript/feed_misc.dart';
import 'transcript/fold_maps.dart';
import 'transcript/feed_reducer.dart';
import 'transcript/random_access_loader.dart';
import 'transcript/seek_controller.dart';
import 'transcript/transcript_seek.dart';
import 'transcript/interaction_cards.dart';

/// Renders the Insight transcript for [agentId] / [sessionId] — a full-screen,
/// random-access view of one session's `agent_events`, driven by the digest
/// (whole-run total, error samples, turn index) rather than the loaded window.
///
/// Always session-scoped (analysis is per-session) and always full-screen
/// (the lens bar + minimap + stepper render unconditionally). The host
/// (`session_analysis_view.dart`) supplies the whole-run anchor lists from the
/// digest and shares a [TranscriptSeekController] so a tapped dashboard
/// stat / turn row jumps the transcript.
class InsightTranscript extends ConsumerStatefulWidget {
  final String agentId;
  final String sessionId;
  final EdgeInsetsGeometry padding;

  /// Lets a sibling (the run-report dashboard / turn index) jump the transcript
  /// to a seq. The transcript resets the window around the anchor `(ts, seq)`
  /// when it's off-window, then anchors + highlights the row.
  final TranscriptSeekController? seekController;

  /// The run-lifetime total event count (from the digest), for the monotonic
  /// "event N of M" position. Per-agent seq is the dense 1-based run ordinal, so
  /// N (the viewport-top seq) is the honest position for a single-agent run.
  final int? totalEventCount;

  /// Full-run minimap anchors — the digest's per-class error sample seqs. The
  /// minimap renders these whole-run (positioned by `seq / total`); a tap routes
  /// through the random-access seek so a failure anywhere in the run is one tap
  /// away, not just the loaded slice.
  final List<int>? runErrorSeqs;

  /// `seq → error class` (tool_error / failed_turn / error:<type>) for every
  /// whole-run error — lets the Errors lens render the COMPLETE error list as
  /// summary rows (class + time) with no event-body fetch.
  final Map<int, String>? runErrorClasses;

  /// `seq → headline label` for each whole-run error: the failing tool's name
  /// ("Bash"), the error type, or absent for a failed turn (digest schema v3
  /// `sample_labels`). Missing → fall back to the class label.
  final Map<int, String>? runErrorLabels;

  /// Whole-run turn-start seqs (the turn index's `start_seq`s) — the turns
  /// funnel + the minimap's turn ticks.
  final List<int>? runTurnSeqs;

  /// `seq → ts` for the run anchors that carry a timestamp (turn `start_ts`,
  /// error `sample_ts`). A jump with a ts takes the O(log n) `(ts, seq)` window
  /// reset; a ts-less anchor (older digest) falls back to the bounded
  /// page-walk.
  final Map<int, String>? runAnchorTs;

  const InsightTranscript({
    super.key,
    required this.agentId,
    required this.sessionId,
    this.padding = const EdgeInsets.all(12),
    this.seekController,
    this.totalEventCount,
    this.runErrorSeqs,
    this.runErrorClasses,
    this.runErrorLabels,
    this.runTurnSeqs,
    this.runAnchorTs,
  });

  @override
  ConsumerState<InsightTranscript> createState() => _InsightTranscriptState();
}

class _InsightTranscriptState extends ConsumerState<InsightTranscript> {
  // The sealed window. Loaded by the `(ts, seq)` keyset (around-anchor / page
  // older / page newer) — no live tail, no SSE.
  final List<Map<String, dynamic>> _events = [];
  // De-dup key. Globally unique across agents (a resumed session spans
  // agents, where per-agent seq can collide).
  final Set<String> _ids = <String>{};
  // Content-stable dedupe keys for replay-ingest filtering (a session/load
  // replay re-streams turns under fresh ids/seqs).
  final Set<String> _replayKeys = <String>{};
  int _maxSeq = 0;
  // Smallest loaded seq; the load-older floor.
  int _minSeq = 0;
  // Oldest loaded ts — the session-scoped load-older cursor.
  String _oldestTs = '';
  String? _error;
  bool _loading = true;
  static const int _pageSize = 200;
  bool _loadingOlder = false;
  bool _atHead = false;
  // Whether the loaded window reaches the live tail. A random-access reset to a
  // mid-run anchor sets it false, which arms the forward pager [_maybeLoadNewer].
  bool _windowHasTail = true;
  bool _loadingNewer = false;
  // Newest loaded (ts, seq) — the forward pager's cursor.
  String _newestTs = '';
  int _newestSeq = 0;
  // Set when the snapshot falls back to the offline cache; surfaces a banner
  // with the snapshot timestamp.
  DateTime? _staleSince;
  final ScrollController _scroll = ScrollController();
  // Tail-follow here only governs whether a jump-to-latest control shows and
  // whether load-newer fires near the bottom; there's no live tail to follow.
  bool _followTail = true;
  // Reveal debug-fidelity kinds under an explicit toggle (Ctrl+O parity).
  bool _verbose = false;
  // The landing engine (ADR-040 P2a): the seek GlobalKey, realized-row window
  // sentinels, and programmatic-scroll guard. Bound in initState.
  late final TranscriptSeek _seek;
  // The seq the transcript is anchored to (the active lens match / external
  // jump). Null = no anchor. The matching card gets [_seek.seekKey] + a tinted
  // border while [_seekHighlight] holds.
  int? _activeSeekSeq;
  bool _seekHighlight = false;
  Timer? _seekHighlightTimer;
  // Single-select transcript lens (Errors / Turns / Text / Tools / All).
  FeedLens _lens = FeedLens.all;
  // Generation of the last external seek serviced (so a controller notify for a
  // seq we already jumped to doesn't re-fire, but a fresh seekTo does).
  int _lastSeekGeneration = 0;
  // Viewport-top position (0..1) for the minimap indicator.
  double _viewFrac = 1.0;
  // A seq the user asked to view "in context": tapped from a filtered card, it
  // switches to All and (in build, once the unfiltered list is back) seeks the
  // row so the surrounding turns are visible.
  int? _pendingContextSeq;
  // When true, the pending-context jump keeps the active lens (the run-anchor
  // funnel stepper, which steps within turns/errors).
  bool _pendingContextKeepLens = false;
  // The funnel stepper's position within the full-run anchor list (turns /
  // errors). Decoupled from [_activeSeekSeq] because the landing row (nearest
  // visible >= anchor) may differ from the anchor seq (a hidden turn.start).
  int? _funnelRunIdx;
  // ADR-039 P2 — the Errors summary list's fixed row extent (lets the funnel
  // stepper scroll to a row by index).
  static const double _kErrorRowExtent = 52.0;

  @override
  void initState() {
    super.initState();
    _seek = TranscriptSeek(scroll: _scroll, isActive: () => mounted);
    _bootstrap();
    _scroll.addListener(_onScroll);
    widget.seekController?.addListener(_onSeekRequest);
  }

  @override
  void didUpdateWidget(covariant InsightTranscript oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.seekController != widget.seekController) {
      oldWidget.seekController?.removeListener(_onSeekRequest);
      widget.seekController?.addListener(_onSeekRequest);
    }
  }

  @override
  void dispose() {
    _seekHighlightTimer?.cancel();
    widget.seekController?.removeListener(_onSeekRequest);
    _scroll.dispose();
    super.dispose();
  }

  // ── Snapshot bootstrap ────────────────────────────────────────────────────

  /// Snapshot-on-entry (ADR-040 §E): one read-through fetch of the tail window
  /// (newest [_pageSize] events) so the surface opens on the latest turns, with
  /// a cache fallback on a network blip. No SSE subscribe — the run is a sealed
  /// dataset here; the host's RefreshIndicator re-pulls the digest and re-entry
  /// re-snapshots the transcript.
  Future<void> _bootstrap() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _error = 'Not connected to a hub';
        _loading = false;
      });
      return;
    }
    try {
      final cached = await client.listAgentEventsCached(
        widget.agentId,
        tail: true,
        limit: _pageSize,
        sessionId: widget.sessionId,
      );
      if (!mounted) return;
      _ingestSnapshot(cached.body);
      _atHead = cached.body.length < _pageSize;
      setState(() {
        _loading = false;
        _staleSince = cached.staleSince;
      });
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _error = 'Feed error (${e.status})';
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = 'Feed error: $e';
        _loading = false;
      });
    }
  }

  /// Replace [_events] with [snapshot] (server-DESC, displayed ASC) and refresh
  /// the bookkeeping. Drops replay-tagged text/thought (their cumulative text
  /// drifts from the live copy, so content-dedup misses and the user would see
  /// double cards); stable-ID kinds keep their replay entries.
  void _ingestSnapshot(List<Map<String, dynamic>> snapshot) {
    final ascending = snapshot.reversed.toList();
    final filtered = <Map<String, dynamic>>[];
    for (final e in ascending) {
      if (agentEventIsReplay(e)) {
        final kind = (e['kind'] ?? '').toString();
        if (kind == 'text' || kind == 'thought') continue;
      }
      filtered.add(e);
    }
    _events
      ..clear()
      ..addAll(filtered);
    _ids.clear();
    _replayKeys.clear();
    _maxSeq = 0;
    _minSeq = 0;
    _oldestTs = '';
    for (final e in _events) {
      final id = (e['id'] ?? '').toString();
      if (id.isNotEmpty) _ids.add(id);
      final replayKey = agentEventReplayKey(e);
      if (replayKey != null) _replayKeys.add(replayKey);
      final seq = (e['seq'] as num?)?.toInt() ?? 0;
      if (seq > _maxSeq) _maxSeq = seq;
      if (_minSeq == 0 || seq < _minSeq) _minSeq = seq;
      final ts = (e['ts'] ?? '').toString();
      if (ts.isNotEmpty && (_oldestTs.isEmpty || ts.compareTo(_oldestTs) < 0)) {
        _oldestTs = ts;
      }
    }
  }

  // ── Random-access loader ──────────────────────────────────────────────────

  /// Bind a [RandomAccessLoader] to this agent + session. Cheap per call — it
  /// holds no state, just the bound `(ts, seq)` keyset fetch closure.
  RandomAccessLoader _randomAccessLoader(HubClient client) => RandomAccessLoader(
        pageSize: _pageSize,
        fetch: ({
          String? beforeTs,
          int? beforeSeq,
          String? afterTs,
          int? afterSeq,
          required int limit,
        }) =>
            client.listAgentEvents(
              widget.agentId,
              sessionId: widget.sessionId,
              beforeTs: beforeTs,
              beforeSeq: beforeSeq,
              afterTs: afterTs,
              afterSeq: afterSeq,
              limit: limit,
            ),
      );

  /// Random-access window reset: replace the loaded window with one block
  /// fetched *around* the anchor `(ts, seq)` — the backward half (before the
  /// key, DESC) and the forward half (the anchor and after, ASC). After a reset
  /// the window may not reach the tail, so [_windowHasTail] goes false (arming
  /// the forward pager).
  Future<void> _resetWindowAround(int seq, String ts,
      {bool keepLens = false}) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final window = await _randomAccessLoader(client).fetchAround(seq, ts);
      if (!mounted) return;
      if (window.isEmpty) {
        // Anchor out of range — leave the current window rather than yanking
        // the viewport to the top.
        return;
      }
      _ingestWindow(window.ascending);
      setState(() {
        _atHead = window.reachedHead;
        _windowHasTail = window.reachedTail;
        _followTail = false;
        _loading = false;
        _error = null;
      });
      // Land on the anchor once the new (mid-list, unrealised) window lays out —
      // route through the convergent index seek, not the ensureVisible-only
      // [_seekToSeq] which would silently no-op here.
      if (keepLens) {
        _landOnSeqKeepLens(seq);
      } else {
        _landOnSeq(seq);
      }
    } catch (_) {
      // Network blip — leave the existing window; the caller can retry.
    }
  }

  /// Replace [_events] with a fresh, contiguous [ascending] window (the
  /// random-access reset). Distinct from [_ingestSnapshot] because it also
  /// tracks the *newest* loaded `(ts, seq)` — the forward pager's cursor.
  void _ingestWindow(List<Map<String, dynamic>> ascending) {
    final filtered = <Map<String, dynamic>>[];
    for (final e in ascending) {
      if (agentEventIsReplay(e)) {
        final kind = (e['kind'] ?? '').toString();
        if (kind == 'text' || kind == 'thought') continue;
      }
      filtered.add(e);
    }
    setState(() {
      _events
        ..clear()
        ..addAll(filtered);
      _ids.clear();
      _replayKeys.clear();
      _maxSeq = 0;
      _minSeq = 0;
      _oldestTs = '';
      _newestTs = '';
      _newestSeq = 0;
      for (final e in _events) {
        final id = (e['id'] ?? '').toString();
        if (id.isNotEmpty) _ids.add(id);
        final replayKey = agentEventReplayKey(e);
        if (replayKey != null) _replayKeys.add(replayKey);
        final s = (e['seq'] as num?)?.toInt() ?? 0;
        if (s > _maxSeq) _maxSeq = s;
        if (_minSeq == 0 || s < _minSeq) _minSeq = s;
        final t = (e['ts'] ?? '').toString();
        if (t.isNotEmpty && (_oldestTs.isEmpty || t.compareTo(_oldestTs) < 0)) {
          _oldestTs = t;
        }
        if (t.compareTo(_newestTs) > 0 || (t == _newestTs && s > _newestSeq)) {
          _newestTs = t;
          _newestSeq = s;
        }
      }
    });
  }

  /// Forward pager — the complement of [_maybeLoadOlder], armed only after a
  /// random-access reset has left the window short of the tail. Pages the next
  /// block *newer* than the loaded edge and appends it; a short page means we've
  /// reached the tail, so [_windowHasTail] flips true.
  Future<void> _maybeLoadNewer() async {
    if (_loadingNewer || _windowHasTail) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null || _newestTs.isEmpty) return;
    setState(() => _loadingNewer = true);
    try {
      final page =
          await _randomAccessLoader(client).fetchNewer(_newestTs, _newestSeq);
      if (!mounted) return;
      final added = <Map<String, dynamic>>[];
      for (final e in page.events) {
        final id = (e['id'] ?? '').toString();
        if (id.isNotEmpty && !_ids.add(id)) continue;
        if (agentEventIsReplay(e)) {
          final kind = (e['kind'] ?? '').toString();
          if (kind == 'text' || kind == 'thought') continue;
        }
        added.add(e);
      }
      setState(() {
        _events.addAll(added);
        for (final e in added) {
          final s = (e['seq'] as num?)?.toInt() ?? 0;
          if (s > _maxSeq) _maxSeq = s;
          final t = (e['ts'] ?? '').toString();
          if (t.compareTo(_newestTs) > 0 ||
              (t == _newestTs && s > _newestSeq)) {
            _newestTs = t;
            _newestSeq = s;
          }
          final replayKey = agentEventReplayKey(e);
          if (replayKey != null) _replayKeys.add(replayKey);
        }
        if (page.reachedTail) _windowHasTail = true;
      });
    } catch (_) {
      // Silent: the next scroll re-triggers.
    } finally {
      if (mounted) setState(() => _loadingNewer = false);
    }
  }

  Future<void> _maybeLoadOlder() async {
    if (_loadingOlder || _atHead) return;
    if (_oldestTs.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _loadingOlder = true);
    final priorOldestTs = _oldestTs;
    final priorMaxExtent =
        _scroll.hasClients ? _scroll.position.maxScrollExtent : 0.0;
    final priorPixels = _scroll.hasClients ? _scroll.position.pixels : 0.0;
    try {
      final older = await client.listAgentEvents(
        widget.agentId,
        beforeTs: priorOldestTs,
        // Pair the ts cursor with the seq tiebreak so same-ts events at the
        // window floor aren't dropped (the (ts, seq) keyset).
        beforeSeq: _minSeq,
        limit: _pageSize,
        sessionId: widget.sessionId,
      );
      if (!mounted) return;
      // Server returns DESC; flip to ASC so the prepend keeps "older above".
      final ascending = <Map<String, dynamic>>[];
      for (final e in older.reversed) {
        final id = (e['id'] ?? '').toString();
        if (id.isNotEmpty && !_ids.add(id)) continue;
        if (agentEventIsReplay(e)) {
          final kind = (e['kind'] ?? '').toString();
          if (kind == 'text' || kind == 'thought') continue;
        }
        ascending.add(e);
      }
      setState(() {
        _events.insertAll(0, ascending);
        for (final e in ascending) {
          final seq = (e['seq'] as num?)?.toInt() ?? 0;
          if (_minSeq == 0 || seq < _minSeq) _minSeq = seq;
          final ts = (e['ts'] ?? '').toString();
          if (ts.isNotEmpty &&
              (_oldestTs.isEmpty || ts.compareTo(_oldestTs) < 0)) {
            _oldestTs = ts;
          }
          final replayKey = agentEventReplayKey(e);
          if (replayKey != null) _replayKeys.add(replayKey);
        }
        _atHead = older.length < _pageSize;
      });
      // Anchor the viewport to the same logical row so the prepend doesn't yank
      // the user upward — shift by the height delta once the new frame lays out.
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!_scroll.hasClients) return;
        final delta = _scroll.position.maxScrollExtent - priorMaxExtent;
        if (delta > 0) _scroll.jumpTo(priorPixels + delta);
      });
    } catch (_) {
      // Silent: the user can swipe again.
    } finally {
      if (mounted) setState(() => _loadingOlder = false);
    }
  }

  // ── Scroll / position ─────────────────────────────────────────────────────

  void _onScroll() {
    if (!_scroll.hasClients) return;
    final maxExt = _scroll.position.maxScrollExtent;
    final atBottom = _scroll.position.pixels >= maxExt - 40;
    final frac = maxExt <= 0
        ? 1.0
        : (_scroll.position.pixels / maxExt).clamp(0.0, 1.0);
    if ((frac - _viewFrac).abs() > 0.01) {
      setState(() => _viewFrac = frac);
    }
    // A programmatic scroll (seek/scrub/jump) must not flip tail-follow or kick
    // the pager — it can momentarily land near the bottom (the "jump to end"
    // bug).
    if (_seek.isProgrammatic) return;
    // The Errors summary list is a digest-only list with no _events paging.
    if (_errorsSummaryMode) return;
    // Window short of the tail: the bottom edge pages *newer* (the forward
    // complement of load-older at the top). We're not at the live tail yet.
    final atTrueTail = atBottom && _windowHasTail;
    if (!_windowHasTail && _scroll.position.pixels >= maxExt - 120) {
      _maybeLoadNewer();
    }
    if (_followTail != atTrueTail) {
      setState(() {
        _followTail = atTrueTail;
        if (atTrueTail) {
          // Reset the turn-stepper anchor so the next `‹` starts from the
          // newest prompt rather than wherever we last jumped.
          _activeSeekSeq = null;
        }
      });
    }
    if (_scroll.position.pixels <= 120) _maybeLoadOlder();
  }

  /// The monotonic "event N of M" position. N is read straight from the
  /// top-built row's run ordinal (the seq) — exact, monotonic, and (unlike a
  /// viewFrac interpolation) doesn't lurch when the window grows by a page. M is
  /// the digest's run-lifetime total.
  ({int n, int m})? _logPosition() {
    final total = widget.totalEventCount;
    if (total != null && total > 0 && _seek.lastTopBuiltSeq > 0) {
      final n = _seek.lastTopBuiltSeq.clamp(1, total);
      return (n: n, m: total);
    }
    return feedLogPosition(
      minSeq: _minSeq,
      maxSeq: _maxSeq,
      viewFrac: _viewFrac,
      totalEventCount: total,
    );
  }

  /// True when the minimap should render whole-run anchors (from the digest +
  /// turn index) — i.e. the host supplied a run total and at least one anchor.
  bool get _runAnchorMode =>
      widget.totalEventCount != null &&
      ((widget.runErrorSeqs?.isNotEmpty ?? false) ||
          (widget.runTurnSeqs?.isNotEmpty ?? false));

  /// The minimap's position indicator: in whole-run anchor mode it tracks the
  /// run ordinal (N/M); otherwise the within-loaded-window fraction.
  double _minimapViewportFrac() {
    if (_runAnchorMode) {
      final p = _logPosition();
      if (p != null && p.m > 0) return (p.n / p.m).clamp(0.0, 1.0);
    }
    return _viewFrac;
  }

  // ── Errors-as-query (the whole-run error list) ────────────────────────────

  /// The Errors lens renders the WHOLE-RUN error list as digest-only summary
  /// rows (class + time), not a client filter over the loaded window: exact,
  /// never-empty, no event-body fetch. Gated to errors-present.
  bool _isErrorsSummaryLens(FeedLens lens) =>
      lens == FeedLens.errors && (widget.runErrorSeqs?.isNotEmpty ?? false);
  bool get _errorsSummaryMode => _isErrorsSummaryLens(_lens);
  // The run's error seqs, ascending so the newest sits at the bottom.
  List<int> _sortedErrorSeqs() => [...?widget.runErrorSeqs]..sort();

  // ── Seek primitives (the landing engine drivers) ──────────────────────────

  void _onSeekRequest() {
    final c = widget.seekController;
    if (c == null) return;
    if (c.generation == _lastSeekGeneration) return;
    _lastSeekGeneration = c.generation;
    final seq = c.seq;
    if (seq != null) _handleExternalSeek(seq, c.ts);
  }

  /// Jump the transcript to [seq] (the dashboard / structure index). If loaded,
  /// anchor it directly. Otherwise, with a known [ts], reset the window around
  /// the anchor `(ts, seq)` (O(log n), reachable at any depth); a ts-less anchor
  /// (older digest) falls back to the bounded page-walk older toward it.
  Future<void> _handleExternalSeek(int seq, [String? ts]) async {
    if (_seqIsLoaded(seq)) {
      _landOnSeq(seq);
      return;
    }
    if (ts != null && ts.isNotEmpty) {
      await _resetWindowAround(seq, ts);
      return;
    }
    const maxPages = 12; // _pageSize(200) × 12 = 2400 events of headroom.
    for (var i = 0; i < maxPages; i++) {
      if (_atHead) break;
      await _maybeLoadOlder();
      if (!mounted) return;
      if (_seqIsLoaded(seq)) break;
    }
    if (!mounted) return;
    if (_seqIsLoaded(seq)) _landOnSeq(seq);
    // Else: a ts-less anchor beyond the page-walk cap — leave the viewport put
    // (yanking to the top here was the "minimap tap jumps to the top" bug).
  }

  /// Land on [seq] robustly even when its row isn't realised: defers to the
  /// convergent index seek via the pending-context mechanism (a rebuild finds
  /// the row's index in the lensed list and binary-searches onto it). The anchor
  /// may be a hidden marker (an ACP `turn.start` isn't rendered), so the
  /// consumer lands on the nearest visible row at or after it. Resets the lens
  /// to All so the landing row has its surrounding context.
  void _landOnSeq(int seq) => _jumpToContext(seq);

  /// Land on [seq] but keep the active lens (run-anchor funnel stepping).
  void _landOnSeqKeepLens(int seq) => _jumpToContext(seq, keepLens: true);

  bool _seqIsLoaded(int seq) =>
      _events.any((e) => (e['seq'] as num?)?.toInt() == seq);

  void _scrollToTail() {
    if (!_scroll.hasClients) return;
    _scroll.jumpTo(_scroll.position.maxScrollExtent);
  }

  void _jumpToLatest() {
    // If a random-access reset left the window short of the live tail, the tail
    // isn't loaded — re-bootstrap it first.
    if (!_windowHasTail) {
      _rebootstrapTail();
      return;
    }
    _scrollToTail();
    setState(() => _followTail = true);
  }

  /// Re-fetch the live-tail window after a random-access reset, restoring the
  /// tail-anchored state. The inverse of [_resetWindowAround].
  Future<void> _rebootstrapTail() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final tail = await client.listAgentEvents(
        widget.agentId,
        tail: true,
        limit: _pageSize,
        sessionId: widget.sessionId,
      );
      if (!mounted) return;
      _ingestWindow(tail.reversed.toList());
      setState(() {
        _atHead = tail.length < _pageSize;
        _windowHasTail = true;
        _followTail = true;
      });
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
    } catch (_) {
      // Leave the current window in place on a network blip.
    }
  }

  /// Jump to the oldest *loaded* row (top of the list). We deliberately don't
  /// kick the pager here (letting the load-older anchor-jump fire mid-animation
  /// made ⤒ flicker); the natural top-of-scroll trigger pages more once the
  /// animation settles at 0.
  void _jumpToOldestLoaded() {
    if (!_scroll.hasClients) return;
    setState(() => _followTail = false);
    _seek.animateProgrammatic(() => _scroll.animateTo(
          0,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        ));
  }

  /// Continuous scrub from the right-edge minimap drag. jumpTo (not animateTo)
  /// so the viewport tracks the finger directly.
  void _scrubTo(double frac) {
    if (!_scroll.hasClients) return;
    if (_followTail) setState(() => _followTail = false);
    final pos = _scroll.position;
    _seek.jumpProgrammatic(
        () => _scroll.jumpTo(frac.clamp(0.0, 1.0) * pos.maxScrollExtent));
  }

  /// Anchor on [seq] via `ensureVisible` over the built row (cold-open style
  /// landing). The target must already be loaded; the post-frame read no-ops
  /// gracefully if its row isn't realised (use [_seekToLoadedIndex] for that).
  void _seekToSeq(int seq) {
    setState(() {
      _activeSeekSeq = seq;
      _followTail = false;
      _seekHighlight = true;
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final ctx = _seek.seekKey.currentContext;
      if (ctx == null) return;
      _seek.animateProgrammatic(() => Scrollable.ensureVisible(
            ctx,
            alignment: 0.3,
            duration: const Duration(milliseconds: 300),
            curve: Curves.easeOut,
          ));
    });
    _seekHighlightTimer?.cancel();
    _seekHighlightTimer = Timer(const Duration(milliseconds: 1200), () {
      if (!mounted) return;
      setState(() => _seekHighlight = false);
    });
  }

  /// Jump from the right-edge minimap. Scrolls the controller *proportionally*
  /// to the tick's vertical fraction so every tap visibly moves the viewport
  /// (`ensureVisible` silently no-ops on a lazy, unrealised row — the exact
  /// far-away ticks the minimap exists to reach), then best-effort fine-tunes
  /// onto the row once it's built.
  void _seekToFrac(double frac, int seq) {
    setState(() {
      _activeSeekSeq = seq;
      _followTail = false;
      _seekHighlight = true;
    });
    if (_scroll.hasClients) {
      final pos = _scroll.position;
      final target = frac.clamp(0.0, 1.0) * pos.maxScrollExtent;
      _seek.animateProgrammatic(() => _scroll.animateTo(
            target,
            duration: const Duration(milliseconds: 300),
            curve: Curves.easeOut,
          ));
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        final ctx = _seek.seekKey.currentContext;
        if (ctx == null) return;
        _seek.animateProgrammatic(() => Scrollable.ensureVisible(
              ctx,
              alignment: 0.3,
              duration: const Duration(milliseconds: 200),
              curve: Curves.easeOut,
            ));
      });
    }
    _seekHighlightTimer?.cancel();
    _seekHighlightTimer = Timer(const Duration(milliseconds: 1400), () {
      if (!mounted) return;
      setState(() => _seekHighlight = false);
    });
  }

  /// Seek precisely onto the loaded row at [idx] (in the current lensed list)
  /// identified by [seq]. Binary-searches the scroll offset using the realized-
  /// row window (the [TranscriptSeek] landing engine) as feedback, so it lands
  /// exactly on the target regardless of row-height variance.
  void _seekToLoadedIndex(int idx, int seq) {
    if (!_scroll.hasClients) {
      _seekToSeq(seq);
      return;
    }
    setState(() {
      _activeSeekSeq = seq;
      _followTail = false;
      _seekHighlight = true;
    });
    _seek.landOnIndex(idx);
    _seekHighlightTimer?.cancel();
    _seekHighlightTimer = Timer(const Duration(milliseconds: 1600), () {
      if (!mounted) return;
      setState(() => _seekHighlight = false);
    });
  }

  /// Seek to the row at [idx] in the rendered [lensed] list (the turn-nav
  /// stepper). Routes through the convergent index seek so it lands exactly
  /// regardless of row-height variance.
  void _seekToLensedIndex(int idx, List<Map<String, dynamic>> lensed) {
    if (idx < 0 || idx >= lensed.length) return;
    final seq = (lensed[idx]['seq'] as num?)?.toInt() ?? 0;
    _seekToLoadedIndex(idx, seq);
  }

  // ── Lens / funnel orchestration ───────────────────────────────────────────

  /// The whole-run anchor list backing a lens's funnel — the digest's turn
  /// starts / error samples (sorted to run order). Null for lenses with no
  /// whole-run index (text / tools / all).
  List<int>? _runAnchorListFor(FeedLens lens) {
    if (!_runAnchorMode) return null;
    final raw = lens == FeedLens.turns
        ? widget.runTurnSeqs
        : (lens == FeedLens.errors ? widget.runErrorSeqs : null);
    if (raw == null || raw.isEmpty) return null;
    return [...raw]..sort();
  }

  /// Switch the active lens. Resets the seek anchor; a non-`all` lens jumps to
  /// its newest match (the newest error/turn is usually what you're debugging).
  void _setLens(FeedLens lens) {
    setState(() {
      _lens = lens;
      _activeSeekSeq = null;
      _funnelRunIdx = null;
      if (lens != FeedLens.all) _followTail = true;
    });
    // The Errors lens is the whole-run error list (digest-only summary rows). On
    // entry just show it scrolled to the newest error (bottom); no _events reset.
    if (_isErrorsSummaryLens(lens)) {
      final n = _sortedErrorSeqs().length;
      setState(() => _funnelRunIdx = n - 1);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
      return;
    }
    // The other run-anchor lens (turns) jumps to its NEWEST match on entry.
    // Without this the filtered view is empty whenever no match sits in the
    // loaded tail window — and an empty list has no scroll extent, so
    // "scroll up to load older" can never fire. The jump random-access-resets
    // around the newest anchor (reachable at any depth).
    final runList = _runAnchorListFor(lens);
    if (runList != null) {
      _funnelRunJump(runList.length - 1, runList.last);
      return;
    }
    if (lens != FeedLens.all) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
    }
  }

  /// Clear the active filter and land on [seq] in the full transcript, so a
  /// match found in a filtered view can be read with its surrounding context.
  /// The seek runs in build once `_lens == all` has put the row back in the
  /// list. With [keepLens] the active lens is preserved (the run-anchor funnel
  /// stepper stays in turns/errors).
  void _jumpToContext(int seq, {bool keepLens = false}) {
    setState(() {
      if (!keepLens) _lens = FeedLens.all;
      _followTail = false;
      _pendingContextSeq = seq;
      _pendingContextKeepLens = keepLens;
    });
  }

  /// Funnel stepper jump driven by the full-run anchor list (turns / errors).
  /// Records the run-list position, then lands on the anchor: a clean window
  /// reset around its `(ts, seq)` if it isn't loaded, else a convergent seek —
  /// both keeping the lens.
  void _funnelRunJump(int idx, int seq) {
    setState(() {
      _funnelRunIdx = idx;
      _activeSeekSeq = seq;
      _followTail = false;
    });
    if (_seqIsLoaded(seq)) {
      _landOnSeqKeepLens(seq);
      return;
    }
    final ts = widget.runAnchorTs?[seq];
    if (ts != null && ts.isNotEmpty) {
      _resetWindowAround(seq, ts, keepLens: true);
      return;
    }
    _landOnSeqKeepLens(seq);
  }

  /// Step the funnel cursor to match [i] of [matchSeqs] — the SINGLE entry point
  /// shared by the top-left funnel pill AND the bottom-left stepper, so the two
  /// can never drift onto different cursors. A whole-run lens (turns/errors)
  /// routes through [_funnelRunJump]; text/tools route through the
  /// loaded-window convergent seek.
  void _funnelStep(int i, List<int> matchSeqs, {required bool usesRunList}) {
    if (i < 0 || i >= matchSeqs.length) return;
    // In the Errors summary list the stepper just scrolls the fixed-extent list
    // to row i (matchSeqs == the sorted run error seqs == the rendered rows).
    if (_errorsSummaryMode) {
      setState(() => _funnelRunIdx = i);
      if (_scroll.hasClients) {
        final off =
            (i * _kErrorRowExtent).clamp(0.0, _scroll.position.maxScrollExtent);
        _scroll.animateTo(off,
            duration: const Duration(milliseconds: 200), curve: Curves.easeOut);
      }
      return;
    }
    if (usesRunList) {
      _funnelRunJump(i, matchSeqs[i]);
    } else {
      _seekToLoadedIndex(i, matchSeqs[i]);
    }
  }

  /// The Errors lens as the run's COMPLETE error list: digest-only summary rows
  /// (class + relative time), exact + never-empty + no event-body fetch. A tap
  /// jumps to that error in full context. Fixed [_kErrorRowExtent] so the funnel
  /// stepper scrolls to a row by index.
  Widget _buildErrorsSummaryList() {
    final seqs = _sortedErrorSeqs();
    final active = _funnelRunIdx;
    return ListView.builder(
      controller: _scroll,
      padding: EdgeInsets.zero,
      itemExtent: _kErrorRowExtent,
      itemCount: seqs.length,
      itemBuilder: (ctx, i) {
        final seq = seqs[i];
        final ts = widget.runAnchorTs?[seq];
        return ErrorSummaryRow(
          ordinal: i + 1,
          errorClass: widget.runErrorClasses?[seq] ?? 'error',
          label: widget.runErrorLabels?[seq],
          ts: ts,
          active: active == i,
          onTap: () {
            setState(() => _funnelRunIdx = i);
            _handleExternalSeek(seq, ts);
          },
        );
      },
    );
  }

  /// "Jump to any event" scrubber. The position pill (event N / M) is tappable:
  /// it opens a slider over the whole run [1, M] and, on confirm, random-access-
  /// windows onto the chosen ordinal. The minimap only reaches anchors
  /// (turns/errors); this reaches any position.
  void _openJumpSheet() {
    final pos = _logPosition();
    if (pos == null || pos.m <= 1) return;
    final m = pos.m;
    double val = pos.n.toDouble().clamp(1.0, m.toDouble()).toDouble();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    showModalBottomSheet<void>(
      context: context,
      backgroundColor:
          isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
      builder: (sheetCtx) => StatefulBuilder(
        builder: (sheetCtx, setSheet) {
          final fg =
              isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
          final muted =
              isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
          return SafeArea(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    'Jump to event ${val.round()} of $m',
                    style: TextStyle(
                        fontSize: 13, fontWeight: FontWeight.w600, color: fg),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'Scrub anywhere in the run, then jump.',
                    style: TextStyle(fontSize: 11, color: muted),
                  ),
                  Slider(
                    min: 1,
                    max: m.toDouble(),
                    value: val.clamp(1.0, m.toDouble()).toDouble(),
                    label: '${val.round()}',
                    divisions: m > 1 ? math.min(m - 1, 1000) : null,
                    onChanged: (v) => setSheet(() => val = v),
                  ),
                  Align(
                    alignment: Alignment.centerRight,
                    child: TextButton(
                      onPressed: () {
                        Navigator.of(sheetCtx).pop();
                        _jumpToOrdinal(val.round());
                      },
                      child: const Text('Jump'),
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }

  /// Random-access jump to an arbitrary run ordinal [n] (1-based). Per-agent seq
  /// is the dense run ordinal, so ordinal n is the event at seq == n; we fetch
  /// that one event to recover its ts, then drive the `(ts, seq)` window reset.
  Future<void> _jumpToOrdinal(int n) async {
    if (_seqIsLoaded(n)) {
      _landOnSeq(n);
      return;
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      // `before: n + 1` (agent-scoped) → newest row with seq <= n, i.e. the
      // event at ordinal n. One row; cheap.
      final rows = await client.listAgentEvents(
        widget.agentId,
        before: n + 1,
        limit: 1,
      );
      if (!mounted || rows.isEmpty) return;
      final e = rows.first;
      final seq = (e['seq'] as num?)?.toInt() ?? n;
      final ts = (e['ts'] ?? '').toString();
      if (ts.isEmpty) {
        await _handleExternalSeek(seq);
        return;
      }
      await _resetWindowAround(seq, ts);
    } catch (_) {
      // Network blip — leave the window in place; the user can retry.
    }
  }

  // ── Build ─────────────────────────────────────────────────────────────────

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    if (_events.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _error ??
                'No events yet — the transcript lights up once the agent '
                    'produces text, tool calls, or completions.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color:
                  isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
            ),
          ),
        ),
      );
    }
    // The per-event fold (tool names / results / updates / resolved approvals)
    // — the lineage maps cards render from and lens predicates read (shared
    // substrate, ADR-040). Local bindings keep the downstream call sites tidy.
    final fold = FoldMaps.fromEvents(_events);
    final toolNames = fold.toolNames;
    final resolvedApprovals = fold.resolvedApprovals;
    final toolUpdates = fold.toolUpdates;
    final toolResults = fold.toolResults;
    // Build the visible event list: drop folded-in kinds (tool_call_update,
    // paired tool_result, session.init, verbose-gated), then collapse streaming
    // partials by message_id.
    final filtered = <Map<String, dynamic>>[
      for (final e in _events)
        if (!isHiddenInFeed(e, toolNames, verbose: _verbose)) e,
    ];
    final visible = collapseStreamingPartials(filtered);
    // Narrow the visible transcript to one family (the lens). Runs AFTER folding
    // so the Errors lens reads a tool_call's resolved status from the same
    // toolResults/toolUpdates maps the card does.
    final lensed = _lens == FeedLens.all
        ? visible
        : [
            for (final e in visible)
              if (agentEventMatchesLens(e, _lens, toolResults, toolUpdates)) e,
          ];
    // Reset the landing engine's realized-row window for this frame; the
    // itemBuilder repopulates it during layout and a convergent seek reads it
    // back. Also snapshots the last frame's top-built seq for the position pill.
    _seek.beginFrame(lensed.length);
    // Consume a pending "view in context" request: now that the lens is back to
    // All (so lensed == visible), find the row's index and seek once this frame
    // lays out. Convergent index seek (height-agnostic).
    if (_pendingContextSeq != null &&
        (_pendingContextKeepLens || _lens == FeedLens.all)) {
      final target = _pendingContextSeq!;
      _pendingContextSeq = null;
      _pendingContextKeepLens = false;
      var idx = lensed.indexWhere((e) => (e['seq'] as num?)?.toInt() == target);
      if (idx < 0) {
        // No exact row — the anchor may be a hidden marker (ACP turn.start) or
        // filtered out. Land on the nearest visible row at or after it.
        var bestSeq = 1 << 30;
        for (var i = 0; i < lensed.length; i++) {
          final s = (lensed[i]['seq'] as num?)?.toInt() ?? 0;
          if (s >= target && s < bestSeq) {
            bestSeq = s;
            idx = i;
          }
        }
      }
      if (idx >= 0) {
        final landSeq = (lensed[idx]['seq'] as num?)?.toInt() ?? target;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _seekToLoadedIndex(idx, landSeq);
        });
      }
    }
    // The turns/errors funnel is driven by the *whole-run* anchor lists (the
    // digest), not the loaded window: the count equals the insight data and is
    // stable as more events page in, and a jump random-access-seeks to the exact
    // anchor even when it isn't loaded. Text/Tools have no whole-run index, so
    // they stay loaded-window (the convergent seek lands them accurately).
    final bool funnelUsesRunList = _runAnchorMode &&
        (_lens == FeedLens.turns || _lens == FeedLens.errors);
    List<int> matchSeqs;
    if (_lens == FeedLens.all) {
      matchSeqs = const <int>[];
    } else if (funnelUsesRunList) {
      final runList = (_lens == FeedLens.turns
              ? widget.runTurnSeqs
              : widget.runErrorSeqs) ??
          const <int>[];
      // Error samples arrive grouped by class — sort to run order so the stepper
      // walks the transcript monotonically. Copy before sorting.
      matchSeqs = [...runList]..sort();
    } else {
      matchSeqs = [for (final e in lensed) (e['seq'] as num?)?.toInt() ?? 0];
    }
    // 1-based position of the active match. Default to the newest when there's
    // no explicit anchor.
    int matchIndex = 0;
    if (matchSeqs.isNotEmpty) {
      if (funnelUsesRunList) {
        matchIndex = (_funnelRunIdx != null &&
                _funnelRunIdx! >= 0 &&
                _funnelRunIdx! < matchSeqs.length)
            ? _funnelRunIdx! + 1
            : matchSeqs.length;
      } else {
        var idx =
            _activeSeekSeq == null ? -1 : matchSeqs.indexOf(_activeSeekSeq!);
        if (idx < 0) idx = matchSeqs.length - 1;
        matchIndex = idx + 1;
      }
    }
    // Per-lens counts for the funnel menu + minimap marks (a faint tick per
    // tool_call / turn anchor, a red tick per error) over the WHOLE loaded
    // transcript (or whole-run anchors in run-anchor mode).
    final lensCounts = <FeedLens, int>{
      for (final l in FeedLens.values)
        l: l == FeedLens.all
            ? visible.length
            : (_runAnchorMode && l == FeedLens.turns)
                ? (widget.runTurnSeqs?.length ?? 0)
                : (_runAnchorMode && l == FeedLens.errors)
                    ? (widget.runErrorSeqs?.length ?? 0)
                    : visible
                        .where((e) => agentEventMatchesLens(
                            e, l, toolResults, toolUpdates))
                        .length,
    };
    final minimapMarks = <FeedMinimapMark>[];
    final turnAnchorIdx = turnAnchorIndices(lensed);
    final turnIdxSet = turnAnchorIdx.toSet();
    final lensedDenom = (lensed.length - 1) <= 0 ? 1 : lensed.length - 1;
    if (_runAnchorMode) {
      // Full-run minimap: anchors come from the digest (every error) + the turn
      // index (every turn start), positioned by their run ordinal (seq / total).
      for (final a in feedRunAnchorMarks(
        errorSeqs: widget.runErrorSeqs ?? const <int>[],
        turnSeqs: widget.runTurnSeqs ?? const <int>[],
        total: widget.totalEventCount!,
      )) {
        minimapMarks.add(FeedMinimapMark(
          frac: a.frac,
          seq: a.seq,
          isError: a.isError,
          // Match the transcript card: a turn anchor is an input.text prompt —
          // colour it with the prompt card's accent (terminalYellow). Errors red.
          color: a.isError
              ? DesignColors.error
              : agentEventAccent('input.text', 'user'),
        ));
      }
    } else {
      // Ticks track the list on screen (lensed) so the minimap is populated in
      // EVERY view: a faint tick per tool call OR turn anchor, a red tick per
      // error.
      for (var i = 0; i < lensed.length; i++) {
        final e = lensed[i];
        final isErr = agentEventIsError(e, toolResults, toolUpdates);
        final isTool = (e['kind'] ?? '').toString() == 'tool_call';
        if (!isErr && !isTool && !turnIdxSet.contains(i)) continue;
        minimapMarks.add(FeedMinimapMark(
          frac: i / lensedDenom,
          seq: (e['seq'] as num?)?.toInt() ?? 0,
          isError: isErr,
          color: isErr
              ? DesignColors.error
              : agentEventAccent((e['kind'] ?? '').toString(),
                  (e['producer'] ?? 'agent').toString()),
        ));
      }
    }
    // The bottom-left stepper steps a DIFFERENT unit per view:
    //   All view      → inbound prompts (turn anchors)
    //   filtered view → the matches shown (every lensed row)
    final stepAnchorIdx = _lens == FeedLens.all
        ? turnAnchorIdx
        : [for (var i = 0; i < lensed.length; i++) i];
    final stepSeqs = [
      for (final i in stepAnchorIdx) (lensed[i]['seq'] as num?)?.toInt() ?? 0,
    ];
    final stepUnit = _lens == FeedLens.all
        ? 'prompt'
        : (_lens == FeedLens.errors
            ? 'error'
            : (_lens == FeedLens.text
                ? 'message'
                : (_lens == FeedLens.turns ? 'turn' : 'tool')));
    // Step relative to the last seek anchor. prevK = last anchor strictly older
    // than the anchor; nextK = first strictly newer.
    final ref = _activeSeekSeq;
    int? prevStepK;
    int? nextStepK;
    for (var k = 0; k < stepSeqs.length; k++) {
      if (ref == null || stepSeqs[k] < ref) prevStepK = k;
      if (nextStepK == null && ref != null && stepSeqs[k] > ref) nextStepK = k;
    }
    // Count the verbose-gated events so the toggle can advertise its value.
    int hiddenForVerbose = 0;
    if (!_verbose) {
      for (final e in _events) {
        final kind = (e['kind'] ?? '').toString();
        if (isVerboseOnly(kind, e['payload'])) hiddenForVerbose++;
      }
    }
    final verboseChip = (_verbose || hiddenForVerbose > 0)
        ? VerboseToggleChip(
            verbose: _verbose,
            hiddenCount: hiddenForVerbose,
            onToggle: () => setState(() => _verbose = !_verbose),
          )
        : null;
    return Column(
      children: [
        if (_staleSince != null) OfflineBanner(staleSince: _staleSince!),
        if (_loadingOlder)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 4),
            child: Center(
              child: SizedBox(
                width: 14,
                height: 14,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            ),
          ),
        Expanded(
          child: Stack(
            children: [
              _errorsSummaryMode
                  ? _buildErrorsSummaryList()
                  : ListView.separated(
                      controller: _scroll,
                      padding: widget.padding,
                      itemCount: lensed.length,
                      separatorBuilder: (_, _) => const SizedBox(height: 8),
                      itemBuilder: (ctx, i) {
                        final ev = lensed[i];
                        final builtSeq = (ev['seq'] as num?)?.toInt() ?? 0;
                        _seek.recordBuiltRow(i, builtSeq);
                        final isTarget = _activeSeekSeq != null &&
                            (ev['seq'] as num?)?.toInt() == _activeSeekSeq;
                        Widget card = AgentEventCard(
                          key: isTarget ? _seek.seekKey : null,
                          event: ev,
                          toolNames: toolNames,
                          toolUpdates: toolUpdates,
                          toolResults: toolResults,
                          resolvedApprovals: resolvedApprovals,
                          agentId: widget.agentId,
                        );
                        // In a filtered view, give each card a "view in context"
                        // affordance: tap to clear the filter and land on this
                        // row in the full transcript.
                        if (_lens != FeedLens.all) {
                          final seq = (ev['seq'] as num?)?.toInt();
                          if (seq != null) {
                            card = Stack(
                              children: [
                                card,
                                Positioned(
                                  top: 2,
                                  left: 2,
                                  child: ContextJumpButton(
                                      onTap: () => _jumpToContext(seq)),
                                ),
                              ],
                            );
                          }
                        }
                        if (isTarget && _seekHighlight) {
                          return AnimatedContainer(
                            duration: const Duration(milliseconds: 400),
                            decoration: BoxDecoration(
                              border: Border.all(
                                color: DesignColors.primary
                                    .withValues(alpha: 0.6),
                                width: 2,
                              ),
                              borderRadius: BorderRadius.circular(10),
                            ),
                            padding: const EdgeInsets.all(2),
                            child: card,
                          );
                        }
                        return card;
                      },
                    ),
              // Inline approval cards: a live run viewed in Insight can still
              // have an open permission/select request — pin it to the bottom so
              // it's in context with the latest turn. Self-filter by agent_id.
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    PendingSelections(agentId: widget.agentId),
                    PendingPermissionPrompts(agentId: widget.agentId),
                  ],
                ),
              ),
              if (_error != null)
                Positioned(
                  left: 0,
                  right: 0,
                  bottom: 0,
                  child: Container(
                    color: DesignColors.error.withValues(alpha: 0.12),
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 6),
                    child: Text(
                      _error!,
                      style: GoogleFonts.jetBrainsMono(
                          fontSize: 11, color: DesignColors.error),
                    ),
                  ),
                ),
              if (!_followTail)
                Positioned(
                  left: 0,
                  right: 0,
                  bottom: 12,
                  child: Center(
                    child: NewEventsPill(count: 0, onTap: _jumpToLatest),
                  ),
                ),
              // When a lens filters out every loaded event, tell the user it's
              // the filter (older matches may exist above) so they can scroll up
              // or clear it.
              if (_lens != FeedLens.all && lensed.isEmpty && !_errorsSummaryMode)
                Center(
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
                    child: Text(
                      'No ${FeedFilterControl.labelFor(_lens)} events in the '
                      'loaded transcript — scroll up to load older, or clear '
                      'the filter.',
                      textAlign: TextAlign.center,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 12,
                        color: isDark
                            ? DesignColors.textMuted
                            : DesignColors.textMutedLight,
                      ),
                    ),
                  ),
                ),
              // Transcript filter funnel / combined filter+jump pill, floating
              // top-left. Carries per-lens counts in its menu.
              Positioned(
                top: 6,
                left: 6,
                child: FeedFilterControl(
                  lens: _lens,
                  matchCount: matchSeqs.length,
                  matchIndex: matchIndex,
                  canPrev: matchIndex > 1,
                  canNext: matchIndex >= 1 && matchIndex < matchSeqs.length,
                  onSelectLens: _setLens,
                  counts: lensCounts,
                  onPrev: () {
                    if (matchIndex > 1) {
                      _funnelStep(matchIndex - 2, matchSeqs,
                          usesRunList: funnelUsesRunList);
                    }
                  },
                  onNext: () {
                    if (matchIndex >= 1 && matchIndex < matchSeqs.length) {
                      _funnelStep(matchIndex, matchSeqs,
                          usesRunList: funnelUsesRunList);
                    }
                  },
                ),
              ),
              // Monotonic "event N of M" position. On the random-access surface
              // it's a control (tap → "jump to any event" scrubber). Suppressed
              // in the Errors summary list (the funnel N/M + the list are the
              // nav there).
              if (!_errorsSummaryMode &&
                  widget.totalEventCount != null &&
                  _logPosition() != null)
                Positioned(
                  top: 8,
                  left: 0,
                  right: 0,
                  child: Center(
                    child: FeedPositionPill(
                      pos: _logPosition()!,
                      onTap: _openJumpSheet,
                    ),
                  ),
                ),
              // Right-edge minimap: a tick per tool call / turn anchor
              // (card-coloured) + a red tick per error, plus a viewport
              // indicator. In full-run anchor mode the ticks are whole-run and a
              // tap routes through the random-access seek.
              if (!_errorsSummaryMode)
                Positioned(
                  top: 8,
                  right: 4,
                  bottom: 12,
                  width: 28,
                  child: FeedMinimap(
                    marks: minimapMarks,
                    onJump: _runAnchorMode
                        ? (frac, seq) {
                            _handleExternalSeek(seq, widget.runAnchorTs?[seq]);
                          }
                        : _seekToFrac,
                    onScrub: _runAnchorMode ? null : _scrubTo,
                    viewportFrac: _minimapViewportFrac(),
                  ),
                ),
              // Bottom-left stepper: ⤒ top-of-loaded, ‹/› previous/next of the
              // current view's unit. Always actionable: prev falls back to
              // paging older (then top), next to jumping to the tail.
              Positioned(
                left: 6,
                bottom: 12,
                child: TurnStepperPill(
                  unit: stepUnit,
                  onOldest: _jumpToOldestLoaded,
                  onPrevTurn: (_lens != FeedLens.all && matchSeqs.isNotEmpty)
                      ? (matchIndex > 1
                          ? () => _funnelStep(matchIndex - 2, matchSeqs,
                              usesRunList: funnelUsesRunList)
                          : (!_atHead
                              ? () {
                                  _maybeLoadOlder();
                                }
                              : _jumpToOldestLoaded))
                      : (prevStepK != null
                          ? () => _seekToLensedIndex(
                              stepAnchorIdx[prevStepK!], lensed)
                          : (!_atHead
                              ? () {
                                  _maybeLoadOlder();
                                }
                              : _jumpToOldestLoaded)),
                  onNextTurn: (_lens != FeedLens.all && matchSeqs.isNotEmpty)
                      ? (matchIndex < matchSeqs.length
                          ? () => _funnelStep(matchIndex, matchSeqs,
                              usesRunList: funnelUsesRunList)
                          : _jumpToLatest)
                      : (nextStepK != null
                          ? () => _seekToLensedIndex(
                              stepAnchorIdx[nextStepK!], lensed)
                          : _jumpToLatest),
                ),
              ),
              // Verbose toggle, top-right (shifted left to clear the minimap
              // lane).
              if (verboseChip != null)
                Positioned(
                  top: 6,
                  right: 30,
                  child: verboseChip,
                ),
            ],
          ),
        ),
      ],
    );
  }
}

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

  /// The full whole-run turn rows (the `agent_turns` index: `idx`, `start_seq`,
  /// `start_ts`, `status`, `open`, `duration_ms`, `tool_count`, `tool_failed`,
  /// `error_count`). Lets the **Turns lens render the complete turn list** as
  /// summary rows (P5 point 6) — the digest-backed structure index folded into
  /// the funnel, replacing the old standalone `_TurnsDisclosure` row. Null /
  /// empty → the Turns lens falls back to stepping turn starts in the loaded
  /// window.
  final List<Map<String, dynamic>>? runTurns;

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
    this.runTurns,
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
  // The Navigator drawer's Turns / Errors tabs scroll independently of the
  // transcript window — each owns its controller.
  final ScrollController _navTurnsScroll = ScrollController();
  final ScrollController _navErrorsScroll = ScrollController();
  // R4 — the Text/Tools filter pages the WHOLE run via a `kind=` keyset buffer
  // (ADR-039 point 3), distinct from the main `_events` live-tail window: a
  // text/tool match anywhere in the run is reachable, not just in the loaded
  // slice. Built on entering a kind lens; scrolled up via `fetchOlder`. A card
  // tap → "view in context" hands back to the main window at that seq.
  final List<Map<String, dynamic>> _lensEvents = [];
  final Set<String> _lensIds = <String>{};
  final ScrollController _lensScroll = ScrollController();
  FeedLens? _lensLoadedFor; // which lens the buffer currently holds (null=none)
  bool _lensLoading = false;
  bool _lensAtHead = false;
  String _lensOldestTs = '';
  int _lensMinSeq = 0;
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
  // Single-select transcript CARD FILTER (All / Text / Tools). Turns and Errors
  // are no longer lenses — they are the Navigator outline (ADR-041). The
  // [FeedLens] enum keeps those values for the minimap/anchor predicates, but
  // they are not offered as selectable filters.
  FeedLens _lens = FeedLens.all;
  // The lenses the funnel offers — a pure card filter (ADR-041 §1).
  static const List<FeedLens> _kLensFilter = [
    FeedLens.all,
    FeedLens.text,
    FeedLens.tools,
  ];
  // The right "Navigator" drawer (ADR-041 §2): the structural outline you jump
  // *from* — Turns / Errors tabs (Map arrives in R2). Phone-first overlay.
  bool _navigatorOpen = false;
  // Generation of the last external seek serviced (so a controller notify for a
  // seq we already jumped to doesn't re-fire, but a fresh seekTo does).
  int _lastSeekGeneration = 0;
  // Viewport-top position (0..1) for the minimap indicator.
  double _viewFrac = 1.0;
  // A seq the user asked to view "in context": tapped from a filtered card, it
  // switches to All and (in build, once the unfiltered list is back) seeks the
  // row so the surrounding turns are visible.
  int? _pendingContextSeq;
  // The Navigator outline rows' fixed extents (let the list lay out cheaply).
  static const double _kErrorRowExtent = 52.0;
  static const double _kTurnRowExtent = 52.0;

  @override
  void initState() {
    super.initState();
    _seek = TranscriptSeek(scroll: _scroll, isActive: () => mounted);
    _bootstrap();
    _scroll.addListener(_onScroll);
    _lensScroll.addListener(_onLensScroll);
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
    _navTurnsScroll.dispose();
    _navErrorsScroll.dispose();
    _lensScroll.dispose();
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
  Future<void> _resetWindowAround(int seq, String ts) async {
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
      _landOnSeq(seq);
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

  // ── Text/Tools lens buffer (the whole-run `kind=` keyset paging) ──────────

  /// A [RandomAccessLoader] bound to this agent/session AND a `kind` set — the
  /// substrate for the Text/Tools filter paging the whole run (ADR-039). Only
  /// [RandomAccessLoader.fetchOlder] is used (scroll-up through the homogeneous
  /// kind set); the newest page is a direct tail fetch.
  RandomAccessLoader _lensLoader(HubClient client, List<String> kinds) =>
      RandomAccessLoader(
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
              kinds: kinds,
            ),
      );

  /// Build the whole-run buffer for a kind lens (Text / Tools) from its newest
  /// page. Replaces any prior buffer; a later lens switch that wins the race is
  /// detected via [_lensLoadedFor]. The server `kind=` set is a SUPERSET of the
  /// rendered lens, so build re-applies [agentEventMatchesLens] over the buffer.
  Future<void> _loadLensBuffer(FeedLens lens) async {
    final kindSet = feedLensKinds(lens);
    if (kindSet == null) return; // all / errors are not kind-paged
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _lensLoadedFor = lens;
      _lensLoading = true;
      _lensEvents.clear();
      _lensIds.clear();
      _lensAtHead = false;
      _lensOldestTs = '';
      _lensMinSeq = 0;
    });
    try {
      final page = await client.listAgentEvents(
        widget.agentId,
        sessionId: widget.sessionId,
        tail: true,
        limit: _pageSize,
        kinds: kindSet.toList(),
      );
      // A newer lens switch may have superseded this load.
      if (!mounted || _lensLoadedFor != lens) return;
      _ingestLensPage(page.reversed.toList(), prepend: false);
      setState(() {
        _lensAtHead = page.length < _pageSize;
        _lensLoading = false;
      });
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _lensScroll.hasClients) {
          _lensScroll.jumpTo(_lensScroll.position.maxScrollExtent);
        }
      });
    } catch (_) {
      if (mounted) setState(() => _lensLoading = false);
    }
  }

  /// Merge a fetched [ascending] page into the lens buffer (dedupe by id, drop
  /// replay text/thought like the main window), then refresh the load-older
  /// cursor from the buffer's oldest row (it's kept contiguous + ascending).
  void _ingestLensPage(List<Map<String, dynamic>> ascending,
      {required bool prepend}) {
    final add = <Map<String, dynamic>>[];
    for (final e in ascending) {
      final id = (e['id'] ?? '').toString();
      if (id.isNotEmpty && !_lensIds.add(id)) continue;
      if (agentEventIsReplay(e)) {
        final kind = (e['kind'] ?? '').toString();
        if (kind == 'text' || kind == 'thought') continue;
      }
      add.add(e);
    }
    setState(() {
      if (prepend) {
        _lensEvents.insertAll(0, add);
      } else {
        _lensEvents.addAll(add);
      }
      if (_lensEvents.isNotEmpty) {
        _lensOldestTs = (_lensEvents.first['ts'] ?? '').toString();
        _lensMinSeq = (_lensEvents.first['seq'] as num?)?.toInt() ?? 0;
      }
    });
  }

  /// Page the next older block of kind-filtered matches when the lens list
  /// scrolls near its top, anchoring the viewport so the prepend doesn't jump.
  Future<void> _loadOlderLens() async {
    final lens = _lensLoadedFor;
    if (_lensLoading || _lensAtHead || _lensOldestTs.isEmpty || lens == null) {
      return;
    }
    final kindSet = feedLensKinds(lens);
    if (kindSet == null) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _lensLoading = true);
    final priorMax =
        _lensScroll.hasClients ? _lensScroll.position.maxScrollExtent : 0.0;
    final priorPixels =
        _lensScroll.hasClients ? _lensScroll.position.pixels : 0.0;
    try {
      final page = await _lensLoader(client, kindSet.toList())
          .fetchOlder(_lensOldestTs, _lensMinSeq);
      if (!mounted) return;
      _ingestLensPage(page.ascending, prepend: true);
      setState(() {
        _lensAtHead = page.reachedHead;
        _lensLoading = false;
      });
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!_lensScroll.hasClients) return;
        final delta = _lensScroll.position.maxScrollExtent - priorMax;
        if (delta > 0) _lensScroll.jumpTo(priorPixels + delta);
      });
    } catch (_) {
      if (mounted) setState(() => _lensLoading = false);
    }
  }

  void _onLensScroll() {
    if (!_lensScroll.hasClients) return;
    if (_lensScroll.position.pixels <= 120) _loadOlderLens();
  }

  /// Hand a lens-buffer card back to the main transcript "in context": clear
  /// the buffer + lens, then land the main window on [seq] (resetting it around
  /// the anchor if the seq isn't in the loaded slice). Fixes the old "view in
  /// context jumps to the wrong row" when the match was outside the window.
  void _viewInContext(int seq, String? ts) {
    setState(() {
      _lens = FeedLens.all;
      _lensEvents.clear();
      _lensIds.clear();
      _lensLoadedFor = null;
    });
    _handleExternalSeek(seq, ts);
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

  // ── Outline data (the Navigator's Turns / Errors tabs) ───────────────────

  // The run's error seqs, ascending so the newest sits at the bottom.
  List<int> _sortedErrorSeqs() => [...?widget.runErrorSeqs]..sort();

  // The turn rows with a real start_seq, ascending so the newest sits at the
  // bottom like the chat transcript. The start_seq>0 filter mirrors how
  // `runTurnSeqs` is built (the minimap anchor list), so the rendered rows stay
  // index-aligned with the minimap ticks.
  List<Map<String, dynamic>> _sortedTurnRows() {
    final rows = [
      for (final r in (widget.runTurns ?? const <Map<String, dynamic>>[]))
        if (((r['start_seq'] as num?)?.toInt() ?? 0) > 0) r,
    ];
    rows.sort((a, b) => ((a['start_seq'] as num?)?.toInt() ?? 0)
        .compareTo((b['start_seq'] as num?)?.toInt() ?? 0));
    return rows;
  }

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

  /// Continuous scrub from the Map-tab minimap drag. jumpTo (not animateTo)
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

  // ── Lens / funnel orchestration ───────────────────────────────────────────

  /// Switch the active card filter (All / Text / Tools). Text/Tools page the
  /// WHOLE run via the `kind=` keyset buffer (ADR-039 point 3) — distinct from
  /// the main live-tail window — so a match anywhere in the run is reachable.
  /// Turns/Errors are not lenses — they live in the Navigator outline (ADR-041).
  void _setLens(FeedLens lens) {
    setState(() {
      _lens = lens;
      _activeSeekSeq = null;
    });
    if (lens == FeedLens.all) {
      // Drop the buffer; the main window owns the All view.
      setState(() {
        _lensEvents.clear();
        _lensIds.clear();
        _lensLoadedFor = null;
        _lensLoading = false;
      });
      return;
    }
    // Build (or reuse) the kind-filtered whole-run buffer.
    if (_lensLoadedFor != lens) {
      _loadLensBuffer(lens);
    } else {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _lensScroll.hasClients) {
          _lensScroll.jumpTo(_lensScroll.position.maxScrollExtent);
        }
      });
    }
  }

  /// Clear the active filter and land on [seq] in the full transcript, so a
  /// match found in a filtered view can be read with its surrounding context.
  /// The seek runs in build once `_lens == all` has put the row back in the
  /// list.
  void _jumpToContext(int seq) {
    setState(() {
      _lens = FeedLens.all;
      _followTail = false;
      _pendingContextSeq = seq;
    });
  }

  /// The Navigator **Errors** tab — the run's COMPLETE error list as digest-only
  /// summary rows (class + relative time), exact + never-empty + no event-body
  /// fetch. An outline you jump *from*: a tap closes the drawer and lands the
  /// transcript on that error in full context (ADR-041 §2). Owns its own scroll
  /// controller [ctl] — it is a drawer list, NOT the transcript window.
  Widget _buildNavErrorsList(ScrollController ctl) {
    final seqs = _sortedErrorSeqs();
    if (seqs.isEmpty) return _navEmpty('No errors — a clean run.');
    return ListView.builder(
      controller: ctl,
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
          active: _activeSeekSeq == seq,
          onTap: () => _jumpFromOutline(seq, ts),
        );
      },
    );
  }

  /// The Navigator **Turns** tab — the run's COMPLETE turn list as digest-backed
  /// summary rows (status · duration · tools · errors), exact + never-empty. An
  /// outline you jump *from*: a tap closes the drawer and lands the transcript on
  /// that turn's start in full context (ADR-041 §2). Owns its own scroll
  /// controller [ctl].
  Widget _buildNavTurnsList(ScrollController ctl) {
    final rows = _sortedTurnRows();
    if (rows.isEmpty) return _navEmpty('No turns recorded yet.');
    return ListView.builder(
      controller: ctl,
      padding: EdgeInsets.zero,
      itemExtent: _kTurnRowExtent,
      itemCount: rows.length,
      itemBuilder: (ctx, i) {
        final r = rows[i];
        final seq = (r['start_seq'] as num?)?.toInt() ?? 0;
        final ts0 = (r['start_ts'] ?? '').toString();
        // Reorder so the null-aware index sits in the false branch — `?[` right
        // after a ternary `?` trips the Dart parser.
        final ts = ts0.isNotEmpty ? ts0 : widget.runAnchorTs?[seq];
        return TurnSummaryRow(
          // Prefer the digest's own turn ordinal (`idx`, 0-based) so the label
          // matches the run-report card; fall back to the list position.
          ordinal: ((r['idx'] as num?)?.toInt() ?? i) + 1,
          status: (r['status'] ?? '').toString(),
          open: r['open'] == true,
          durationMs: (r['duration_ms'] as num?)?.toInt() ?? 0,
          toolCount: (r['tool_count'] as num?)?.toInt() ?? 0,
          toolFailed: (r['tool_failed'] as num?)?.toInt() ?? 0,
          errorCount: (r['error_count'] as num?)?.toInt() ?? 0,
          ts: ts,
          active: _activeSeekSeq == seq,
          onTap: () => _jumpFromOutline(seq, ts),
        );
      },
    );
  }

  /// Outline-row tap (Turns / Errors): close the Navigator drawer and land the
  /// transcript on [seq] in full card context.
  void _jumpFromOutline(int seq, String? ts) {
    setState(() => _navigatorOpen = false);
    _handleExternalSeek(seq, ts);
  }

  /// The Navigator **Map** tab — the whole-run minimap (ADR-041 §3): a vertical
  /// colour-coded overview (tool/turn ticks + red error ticks + a viewport
  /// indicator). Tapping a tick closes the drawer and lands the transcript on
  /// that landmark; the "Jump to event…" button opens the arbitrary-ordinal
  /// slider. This replaces the old floating right-edge lane that collided with
  /// the card's top-right control. [marks] are computed in build (they depend on
  /// the lensed window / digest anchors).
  Widget _buildNavMap(List<FeedMinimapMark> marks) {
    final pos = _logPosition();
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
          child: SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              icon: const Icon(Icons.my_location, size: 16),
              label: Text(pos != null
                  ? 'Jump to event ${pos.n} / ${pos.m}'
                  : 'Jump to event…'),
              onPressed: (pos != null && pos.m > 1)
                  ? () {
                      setState(() => _navigatorOpen = false);
                      _openJumpSheet();
                    }
                  : null,
            ),
          ),
        ),
        Expanded(
          child: marks.isEmpty
              ? _navEmpty('No tool calls, turns, or errors to map yet.')
              : Padding(
                  padding: const EdgeInsets.fromLTRB(20, 4, 20, 12),
                  child: FeedMinimap(
                    marks: marks,
                    // Tap a tick → close the drawer, land on that landmark.
                    onJump: (frac, seq) {
                      setState(() => _navigatorOpen = false);
                      if (_runAnchorMode) {
                        _handleExternalSeek(seq, widget.runAnchorTs?[seq]);
                      } else {
                        _seekToFrac(frac, seq);
                      }
                    },
                    // Continuous scrub only in loaded-window mode (run-anchor
                    // mode jumps by landmark); the slider above covers arbitrary
                    // positions either way.
                    onScrub: _runAnchorMode ? null : _scrubTo,
                    viewportFrac: _minimapViewportFrac(),
                  ),
                ),
        ),
      ],
    );
  }

  Widget _navEmpty(String message) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          message,
          textAlign: TextAlign.center,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
          ),
        ),
      ),
    );
  }

  /// "Jump to any event" scrubber, opened from the Navigator's Map tab: a slider
  /// over the whole run [1, M] that, on confirm, random-access-windows onto the
  /// chosen ordinal. The minimap ticks only reach anchors (turns/errors/tools);
  /// this reaches any position.
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

  // ── Navigator drawer (the structural outline) ─────────────────────────────

  /// The right "Navigator" drawer (ADR-041 §2), as a phone-first overlay since
  /// [InsightTranscript] is embedded (no Scaffold of its own): a tap-to-dismiss
  /// scrim + a right-aligned panel with the **Turns | Errors** outline tabs.
  /// Each tab is a whole-run structural index you jump *from* — a row tap closes
  /// the drawer and lands the transcript on that seq in full context. (R2 adds a
  /// Map tab here and retires the floating minimap.)
  Widget _buildNavigatorOverlay(List<FeedMinimapMark> minimapMarks) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final panelBg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final fg =
        isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final width =
        math.min(340.0, MediaQuery.of(context).size.width * 0.86);
    final errorCount = widget.runErrorSeqs?.length ?? 0;
    final turnCount = _sortedTurnRows().length;
    return Positioned.fill(
      child: Stack(
        children: [
          // Scrim — tap anywhere outside the panel to dismiss.
          GestureDetector(
            onTap: () => setState(() => _navigatorOpen = false),
            child: Container(color: Colors.black.withValues(alpha: 0.45)),
          ),
          Align(
            alignment: Alignment.centerRight,
            child: Material(
              color: panelBg,
              elevation: 12,
              child: SizedBox(
                width: width,
                height: double.infinity,
                child: SafeArea(
                  left: false,
                  child: DefaultTabController(
                    length: 3,
                    child: Column(
                      children: [
                        Padding(
                          padding:
                              const EdgeInsets.fromLTRB(14, 10, 6, 4),
                          child: Row(
                            children: [
                              Icon(Icons.toc, size: 18, color: muted),
                              const SizedBox(width: 8),
                              Text(
                                'Navigator',
                                style: GoogleFonts.spaceGrotesk(
                                  fontSize: 14,
                                  fontWeight: FontWeight.w700,
                                  color: fg,
                                ),
                              ),
                              const Spacer(),
                              IconButton(
                                tooltip: 'Close',
                                visualDensity: VisualDensity.compact,
                                icon: Icon(Icons.close,
                                    size: 18, color: muted),
                                onPressed: () => setState(
                                    () => _navigatorOpen = false),
                              ),
                            ],
                          ),
                        ),
                        TabBar(
                          labelColor: DesignColors.primary,
                          unselectedLabelColor: muted,
                          indicatorColor: DesignColors.primary,
                          labelStyle: GoogleFonts.spaceGrotesk(
                              fontSize: 12, fontWeight: FontWeight.w700),
                          tabs: [
                            Tab(text: 'Turns ($turnCount)'),
                            Tab(text: 'Errors ($errorCount)'),
                            const Tab(text: 'Map'),
                          ],
                        ),
                        Divider(height: 1, color: border),
                        Expanded(
                          child: TabBarView(
                            children: [
                              _buildNavTurnsList(_navTurnsScroll),
                              _buildNavErrorsList(_navErrorsScroll),
                              _buildNavMap(minimapMarks),
                            ],
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
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
    // The center list's source: the All view reads the main live-tail window
    // (`_events`); a Text/Tools filter reads its whole-run `kind=` keyset buffer
    // (`_lensEvents`, R4) so a match anywhere in the run is reachable, not just
    // the loaded slice.
    final bool isLensView = _lens != FeedLens.all;
    final src = isLensView ? _lensEvents : _events;
    // The per-event fold (tool names / results / updates / resolved approvals)
    // — the lineage maps cards render from and lens predicates read (shared
    // substrate, ADR-040). Folded over the active source so tool_result pairing
    // works inside the buffer too (the Tools kind set carries tool_result /
    // tool_call_update). Local bindings keep the downstream call sites tidy.
    final fold = FoldMaps.fromEvents(src);
    final toolNames = fold.toolNames;
    final resolvedApprovals = fold.resolvedApprovals;
    final toolUpdates = fold.toolUpdates;
    final toolResults = fold.toolResults;
    // Build the visible event list: drop folded-in kinds (tool_call_update,
    // paired tool_result, session.init, verbose-gated), then collapse streaming
    // partials by message_id.
    final filtered = <Map<String, dynamic>>[
      for (final e in src)
        if (!isHiddenInFeed(e, toolNames, verbose: _verbose)) e,
    ];
    final visible = collapseStreamingPartials(filtered);
    // Narrow to one family (the lens). The server `kind=` set is a SUPERSET of
    // the rendered lens, so re-apply the predicate over the buffer. Runs AFTER
    // folding so a tool_call's resolved status reads from the same
    // toolResults/toolUpdates maps the card does.
    final lensed = !isLensView
        ? visible
        : [
            for (final e in visible)
              if (agentEventMatchesLens(e, _lens, toolResults, toolUpdates)) e,
          ];
    // Reset the landing engine's realized-row window for this frame; the
    // itemBuilder repopulates it during layout and a convergent seek reads it
    // back. Also snapshots the last frame's top-built seq for the Map tab's
    // "event N / M" readout.
    _seek.beginFrame(lensed.length);
    // Consume a pending "view in context" request: now that the lens is back to
    // All (so lensed == visible), find the row's index and seek once this frame
    // lays out. Convergent index seek (height-agnostic).
    if (_pendingContextSeq != null && _lens == FeedLens.all) {
      final target = _pendingContextSeq!;
      _pendingContextSeq = null;
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
    // The funnel is a pure card filter (All / Text / Tools): Text/Tools page the
    // whole run as a scrollable buffer, so the pill shows the loaded match count
    // and a clear button — no position cursor, no stepper (ADR-041). Turns and
    // Errors navigation lives in the Navigator outline.
    final int lensMatchCount = isLensView ? lensed.length : 0;
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
        if (_loadingOlder || (isLensView && _lensLoading && lensed.isNotEmpty))
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
              // The transcript always renders cards — Turns/Errors are the
              // Navigator outline, never a list that replaces the stream
              // (ADR-041 §1–2). The All view scrolls the main window; a
              // Text/Tools filter scrolls its own whole-run buffer (R4).
              ListView.separated(
                controller: isLensView ? _lensScroll : _scroll,
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
                  // affordance: tap → clear the filter and land the main window
                  // on this row (resetting it around the anchor if the match is
                  // outside the loaded slice).
                  if (isLensView) {
                    final seq = (ev['seq'] as num?)?.toInt();
                    if (seq != null) {
                      final ts = (ev['ts'] ?? '').toString();
                      card = Stack(
                        children: [
                          card,
                          Positioned(
                            top: 2,
                            left: 2,
                            child: ContextJumpButton(
                                onTap: () => _viewInContext(seq, ts)),
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
                          color: DesignColors.primary.withValues(alpha: 0.6),
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
              if (!isLensView && !_followTail)
                Positioned(
                  left: 0,
                  right: 0,
                  bottom: 12,
                  child: Center(
                    child: NewEventsPill(count: 0, onTap: _jumpToLatest),
                  ),
                ),
              // The Text/Tools buffer covers the whole run: an empty lensed list
              // means the run has no such events (once loaded) — not "scroll to
              // find more". While the buffer is still fetching, show a spinner.
              if (isLensView && lensed.isEmpty)
                Center(
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
                    child: _lensLoading
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : Text(
                            'No ${FeedFilterControl.labelFor(_lens)} events in '
                            'this run.',
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
              // Card-filter funnel (All / Text / Tools), floating top-left.
              // Text/Tools page the whole run as a scrollable buffer, so the
              // pill shows the loaded match count + a clear button — no cursor,
              // no stepper. Structural navigation is the Navigator's job.
              Positioned(
                top: 6,
                left: 6,
                child: FeedFilterControl(
                  lens: _lens,
                  matchCount: lensMatchCount,
                  matchIndex: 0,
                  canPrev: false,
                  canNext: false,
                  onSelectLens: _setLens,
                  counts: lensCounts,
                  selectableLenses: _kLensFilter,
                  showStepper: false,
                  onPrev: () {},
                  onNext: () {},
                ),
              ),
              // Navigator open-handle, top-right. Opens the structural outline +
              // Map drawer (Turns / Errors / Map). The minimap now lives in the
              // Map tab — no floating right-edge lane, no card-control collision,
              // no bottom stepper, no N/M pill (ADR-041 §3, §6).
              Positioned(
                top: 6,
                right: 6,
                child: _NavigatorHandle(
                  onTap: () => setState(() => _navigatorOpen = true),
                ),
              ),
              // Verbose toggle, top-right (shifted left to clear the Navigator
              // handle).
              if (verboseChip != null)
                Positioned(
                  top: 6,
                  right: 52,
                  child: verboseChip,
                ),
              // The Navigator drawer overlay (phone-first): a scrim + a
              // right-aligned panel with the Turns / Errors / Map tabs.
              if (_navigatorOpen) _buildNavigatorOverlay(minimapMarks),
            ],
          ),
        ),
      ],
    );
  }
}

/// The Navigator open-handle — a compact round button that opens the structural
/// outline drawer (Turns / Errors). Sits top-right, clear of the minimap lane.
class _NavigatorHandle extends StatelessWidget {
  final VoidCallback onTap;
  const _NavigatorHandle({required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Material(
      color: bg,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: BorderSide(color: border),
      ),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(8),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 8),
          child: Icon(Icons.toc, size: 18, color: muted),
        ),
      ),
    );
  }
}

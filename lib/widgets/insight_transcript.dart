// InsightTranscript — the sealed / random-access transcript mode (ADR-040 P3).
//
// The Insight surface reads a run as a *sealed dataset*, not a live
// conversation: a snapshot taken on entry, navigated by random access. It owns
// its own event buffer fed by the dense `session_ordinal` keyset loader
// (ADR-042 P4), the seek orchestration (anchor + funnel-run jumps + "view in
// context"), and the
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

import '../l10n/app_localizations.dart';
import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';
import '../theme/tokens.dart';
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

  /// The run-lifetime total event count (from the session digest, summed across
  /// the session's agents), for the monotonic "event N of M" position. The dense
  /// `session_ordinal` is the 1-based run position, so N (the viewport-top
  /// ordinal) is the honest position across a resume too (ADR-042).
  final int? totalEventCount;

  /// Full-run navigation anchors — the digest's per-class error sample
  /// **ordinals** (`session_ordinal`, ADR-042). The minimap renders these
  /// whole-run (positioned by `ordinal / total`); a tap routes through the
  /// random-access seek so a failure anywhere in the run is one tap away, not
  /// just the loaded slice. Unique across a resumed session's agents.
  final List<int>? runErrorOrdinals;

  /// `ordinal → error class` (tool_error / failed_turn / error:<type>) for every
  /// whole-run error — lets the Errors lens render the COMPLETE error list as
  /// summary rows (class + time) with no event-body fetch.
  final Map<int, String>? runErrorClasses;

  /// `ordinal → headline label` for each whole-run error: the failing tool's
  /// name ("Bash"), the error type, or absent for a failed turn (digest schema
  /// v3 `sample_labels`). Missing → fall back to the class label.
  final Map<int, String>? runErrorLabels;

  /// Whole-run turn-start **ordinals** (the turn index's `start_ordinal`s) —
  /// the turns outline + the minimap's turn ticks.
  final List<int>? runTurnOrdinals;

  /// The full whole-run turn rows (the `agent_turns` index: `idx`, `start_seq`,
  /// `start_ts`, `status`, `open`, `duration_ms`, `tool_count`, `tool_failed`,
  /// `error_count`). Lets the **Turns lens render the complete turn list** as
  /// summary rows (P5 point 6) — the digest-backed structure index folded into
  /// the funnel, replacing the old standalone `_TurnsDisclosure` row. Null /
  /// empty → the Turns lens falls back to stepping turn starts in the loaded
  /// window.
  final List<Map<String, dynamic>>? runTurns;

  /// `ordinal → ts` for the run anchors that carry a timestamp (turn
  /// `start_ts`, error `sample_ts`) — used for the outline rows' relative-time
  /// labels and the seek highlight. The window reset itself keysets on the
  /// ordinal alone (ADR-042), so the ts is no longer load-bearing.
  final Map<int, String>? runAnchorTs;

  /// The right Navigator drawer's open state, **lifted to the host** so the
  /// left Sessions rail and the right Navigator stay mutually exclusive (only
  /// one overlay at a time — ADR-041 §5). The host owns the bool; the handle /
  /// scrim / close button report changes via [onNavigatorOpenChanged].
  final bool navigatorOpen;
  final ValueChanged<bool> onNavigatorOpenChanged;

  const InsightTranscript({
    super.key,
    required this.agentId,
    required this.sessionId,
    required this.navigatorOpen,
    required this.onNavigatorOpenChanged,
    this.padding = const EdgeInsets.all(12),
    this.seekController,
    this.totalEventCount,
    this.runErrorOrdinals,
    this.runErrorClasses,
    this.runErrorLabels,
    this.runTurnOrdinals,
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
  // The loaded window's coordinate bounds, in dense `session_ordinal` space
  // (ADR-042) — the session-unique coordinate the keyset loader pages on, so a
  // resumed session (whose agents' seqs collide) windows + lands correctly.
  int _maxOrd = 0;
  // Smallest loaded ordinal; the load-older floor (the `before_ordinal` cursor).
  int _minOrd = 0;
  String? _error;
  bool _loading = true;
  static const int _pageSize = 200;
  bool _loadingOlder = false;
  bool _atHead = false;
  // Whether the loaded window reaches the live tail. A random-access reset to a
  // mid-run anchor sets it false, which arms the forward pager [_maybeLoadNewer].
  bool _windowHasTail = true;
  bool _loadingNewer = false;
  // Newest loaded ordinal — the forward pager's `after_ordinal` cursor.
  int _newestOrd = 0;
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
  // Oldest loaded ordinal in the lens buffer — its `before_ordinal` load-older
  // cursor.
  int _lensMinOrd = 0;
  // Tail-follow here only governs whether a jump-to-latest control shows and
  // whether load-newer fires near the bottom; there's no live tail to follow.
  bool _followTail = true;
  // Reveal debug-fidelity kinds under an explicit toggle (Ctrl+O parity).
  bool _verbose = false;
  // The landing engine (ADR-040 P2a): the seek GlobalKey, realized-row window
  // sentinels, and programmatic-scroll guard. Bound in initState.
  late final TranscriptSeek _seek;
  // The ordinal the transcript is anchored to (the active lens match / external
  // jump). Null = no anchor. The matching card gets [_seek.seekKey] + a tinted
  // border while [_seekHighlight] holds.
  int? _activeSeekOrd;
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
  // *from* — Turns / Errors / Map tabs. Phone-first overlay. Its open state is
  // lifted to the host (`widget.navigatorOpen`) so the left Sessions rail and
  // this drawer stay mutually exclusive; toggles go through
  // `widget.onNavigatorOpenChanged`.
  // Generation of the last external seek serviced (so a controller notify for a
  // seq we already jumped to doesn't re-fire, but a fresh seekTo does).
  int _lastSeekGeneration = 0;
  // Viewport-top position (0..1) for the minimap indicator.
  double _viewFrac = 1.0;
  // An ordinal the user asked to view "in context": tapped from a filtered
  // card, it switches to All and (in build, once the unfiltered list is back)
  // seeks the row so the surrounding turns are visible.
  int? _pendingContextOrd;
  // The Navigator outline rows' fixed extents (let the list lay out cheaply).
  static const double _kErrorRowExtent = 52.0;
  static const double _kTurnRowExtent = 52.0;

  /// The session-scoped coordinate of a row: the dense `session_ordinal`
  /// (ADR-042), unique across the agents a resumed session spans. Falls back to
  /// the per-agent `seq` for pre-migration rows that lack an ordinal — those
  /// degrade to the old single-agent behavior (seq is unambiguous there).
  static int _ordOf(Map<String, dynamic> e) {
    final o = (e['session_ordinal'] as num?)?.toInt() ?? 0;
    if (o > 0) return o;
    return (e['seq'] as num?)?.toInt() ?? 0;
  }

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
    _maxOrd = 0;
    _minOrd = 0;
    for (final e in _events) {
      final id = (e['id'] ?? '').toString();
      if (id.isNotEmpty) _ids.add(id);
      final replayKey = agentEventReplayKey(e);
      if (replayKey != null) _replayKeys.add(replayKey);
      final ord = _ordOf(e);
      if (ord > _maxOrd) _maxOrd = ord;
      if (ord > 0 && (_minOrd == 0 || ord < _minOrd)) _minOrd = ord;
    }
  }

  // ── Random-access loader ──────────────────────────────────────────────────

  /// Bind a [RandomAccessLoader] to this agent + session. Cheap per call — it
  /// holds no state, just the bound `(ts, seq)` keyset fetch closure.
  RandomAccessLoader _randomAccessLoader(HubClient client) => RandomAccessLoader(
        pageSize: _pageSize,
        fetch: ({
          int? beforeOrdinal,
          int? afterOrdinal,
          required int limit,
        }) =>
            client.listAgentEvents(
              widget.agentId,
              sessionId: widget.sessionId,
              beforeOrdinal: beforeOrdinal,
              afterOrdinal: afterOrdinal,
              limit: limit,
            ),
      );

  /// Random-access window reset: replace the loaded window with one block
  /// fetched *around* the anchor [ordinal] — the backward half (before the
  /// ordinal, DESC) and the forward half (the anchor and after, ASC). The dense
  /// `session_ordinal` is a single session-unique cursor, so this lands on the
  /// right row even after a resume (where per-agent seqs collide). After a reset
  /// the window may not reach the tail, so [_windowHasTail] goes false (arming
  /// the forward pager).
  Future<void> _resetWindowAround(int ordinal) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final window = await _randomAccessLoader(client).fetchAround(ordinal);
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
      // [_seekToOrd] which would silently no-op here.
      _landOnOrd(ordinal);
    } catch (_) {
      // Network blip — leave the existing window; the caller can retry.
    }
  }

  /// Replace [_events] with a fresh, contiguous [ascending] window (the
  /// random-access reset). Distinct from [_ingestSnapshot] because it also
  /// tracks the *newest* loaded ordinal — the forward pager's cursor.
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
      _maxOrd = 0;
      _minOrd = 0;
      _newestOrd = 0;
      for (final e in _events) {
        final id = (e['id'] ?? '').toString();
        if (id.isNotEmpty) _ids.add(id);
        final replayKey = agentEventReplayKey(e);
        if (replayKey != null) _replayKeys.add(replayKey);
        final o = _ordOf(e);
        if (o > _maxOrd) _maxOrd = o;
        if (o > 0 && (_minOrd == 0 || o < _minOrd)) _minOrd = o;
        if (o > _newestOrd) _newestOrd = o;
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
    if (client == null || _newestOrd == 0) return;
    setState(() => _loadingNewer = true);
    try {
      final page = await _randomAccessLoader(client).fetchNewer(_newestOrd);
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
          final o = _ordOf(e);
          if (o > _maxOrd) _maxOrd = o;
          if (o > _newestOrd) _newestOrd = o;
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
    if (_minOrd == 0) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _loadingOlder = true);
    final priorMinOrd = _minOrd;
    final priorMaxExtent =
        _scroll.hasClients ? _scroll.position.maxScrollExtent : 0.0;
    final priorPixels = _scroll.hasClients ? _scroll.position.pixels : 0.0;
    try {
      final older = await client.listAgentEvents(
        widget.agentId,
        // The dense `session_ordinal` keyset (ADR-042): one session-unique
        // cursor, so the load-older floor never drops or duplicates a sibling
        // across a resume boundary the way the (ts, seq) tiebreak could.
        beforeOrdinal: priorMinOrd,
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
          final ord = _ordOf(e);
          if (ord > 0 && (_minOrd == 0 || ord < _minOrd)) _minOrd = ord;
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
          int? beforeOrdinal,
          int? afterOrdinal,
          required int limit,
        }) =>
            client.listAgentEvents(
              widget.agentId,
              sessionId: widget.sessionId,
              beforeOrdinal: beforeOrdinal,
              afterOrdinal: afterOrdinal,
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
      _lensMinOrd = 0;
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
        _lensMinOrd = _ordOf(_lensEvents.first);
      }
    });
  }

  /// Page the next older block of kind-filtered matches when the lens list
  /// scrolls near its top, anchoring the viewport so the prepend doesn't jump.
  Future<void> _loadOlderLens() async {
    final lens = _lensLoadedFor;
    if (_lensLoading || _lensAtHead || _lensMinOrd == 0 || lens == null) {
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
      final page =
          await _lensLoader(client, kindSet.toList()).fetchOlder(_lensMinOrd);
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
  /// the buffer + lens, then land the main window on [ordinal] (resetting it
  /// around the anchor if the ordinal isn't in the loaded slice). Fixes the old
  /// "view in context jumps to the wrong row" when the match was outside the
  /// window.
  void _viewInContext(int ordinal) {
    setState(() {
      _lens = FeedLens.all;
      _lensEvents.clear();
      _lensIds.clear();
      _lensLoadedFor = null;
    });
    _handleExternalSeek(ordinal);
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
          _activeSeekOrd = null;
        }
      });
    }
    if (_scroll.position.pixels <= 120) _maybeLoadOlder();
  }

  /// The monotonic "event N of M" position. N is read straight from the
  /// top-built row's `session_ordinal` (ADR-042) — exact, monotonic, and
  /// (unlike a viewFrac interpolation) doesn't lurch when the window grows by a
  /// page, and unlike the per-agent seq it stays the true run position across a
  /// resume. M is the session digest's run-lifetime total.
  ({int n, int m})? _logPosition() {
    final total = widget.totalEventCount;
    if (total != null && total > 0 && _seek.lastTopBuiltSeq > 0) {
      final n = _seek.lastTopBuiltSeq.clamp(1, total);
      return (n: n, m: total);
    }
    return feedLogPosition(
      minSeq: _minOrd,
      maxSeq: _maxOrd,
      viewFrac: _viewFrac,
      totalEventCount: total,
    );
  }

  /// True when the minimap should render whole-run anchors (from the digest +
  /// turn index) — i.e. the host supplied a run total and at least one anchor.
  bool get _runAnchorMode =>
      widget.totalEventCount != null &&
      ((widget.runErrorOrdinals?.isNotEmpty ?? false) ||
          (widget.runTurnOrdinals?.isNotEmpty ?? false));

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

  // The run's error ordinals, ascending so the newest sits at the bottom.
  List<int> _sortedErrorOrdinals() => [...?widget.runErrorOrdinals]..sort();

  // The turn row's navigation anchor: its `start_ordinal` (ADR-042), falling
  // back to the per-agent `start_seq` for pre-migration digests.
  static int _turnAnchorOf(Map<String, dynamic> r) {
    final o = (r['start_ordinal'] as num?)?.toInt() ?? 0;
    if (o > 0) return o;
    return (r['start_seq'] as num?)?.toInt() ?? 0;
  }

  // The turn rows with a real anchor, ascending so the newest sits at the
  // bottom like the chat transcript. The anchor>0 filter mirrors how
  // `runTurnOrdinals` is built (the minimap anchor list), so the rendered rows
  // stay index-aligned with the minimap ticks.
  List<Map<String, dynamic>> _sortedTurnRows() {
    final rows = [
      for (final r in (widget.runTurns ?? const <Map<String, dynamic>>[]))
        if (_turnAnchorOf(r) > 0) r,
    ];
    rows.sort((a, b) => _turnAnchorOf(a).compareTo(_turnAnchorOf(b)));
    return rows;
  }

  // ── Seek primitives (the landing engine drivers) ──────────────────────────

  void _onSeekRequest() {
    final c = widget.seekController;
    if (c == null) return;
    if (c.generation == _lastSeekGeneration) return;
    _lastSeekGeneration = c.generation;
    final ord = c.seq; // the controller carries a session_ordinal (ADR-042)
    if (ord != null) _handleExternalSeek(ord);
  }

  /// Jump the transcript to the anchor at [ordinal] (the dashboard / structure
  /// index). If loaded, anchor it directly. Otherwise reset the window around
  /// the ordinal — the dense `session_ordinal` keyset reaches any depth in
  /// O(log n) and lands on the right row across a resume, so there is no
  /// ts-fallback page-walk to mis-target.
  Future<void> _handleExternalSeek(int ordinal) async {
    if (_ordIsLoaded(ordinal)) {
      _landOnOrd(ordinal);
      return;
    }
    await _resetWindowAround(ordinal);
  }

  /// Land on [ordinal] robustly even when its row isn't realised: defers to the
  /// convergent index seek via the pending-context mechanism (a rebuild finds
  /// the row's index in the lensed list and binary-searches onto it). The anchor
  /// may be a hidden marker (an ACP `turn.start` isn't rendered), so the
  /// consumer lands on the nearest visible row at or after it. Resets the lens
  /// to All so the landing row has its surrounding context.
  void _landOnOrd(int ordinal) => _jumpToContext(ordinal);

  bool _ordIsLoaded(int ordinal) => _events.any((e) => _ordOf(e) == ordinal);

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

  /// Anchor on [ordinal] via `ensureVisible` over the built row (cold-open
  /// style landing). The target must already be loaded; the post-frame read
  /// no-ops gracefully if its row isn't realised (use [_seekToLoadedIndex] for
  /// that).
  void _seekToOrd(int ordinal) {
    setState(() {
      _activeSeekOrd = ordinal;
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
  /// onto the row once it's built. [ordinal] is the tapped tick's coordinate.
  void _seekToFrac(double frac, int ordinal) {
    setState(() {
      _activeSeekOrd = ordinal;
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
  /// identified by [ordinal]. Binary-searches the scroll offset using the
  /// realized-row window (the [TranscriptSeek] landing engine) as feedback, so
  /// it lands exactly on the target regardless of row-height variance.
  void _seekToLoadedIndex(int idx, int ordinal) {
    if (!_scroll.hasClients) {
      _seekToOrd(ordinal);
      return;
    }
    setState(() {
      _activeSeekOrd = ordinal;
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
      _activeSeekOrd = null;
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

  /// Clear the active filter and land on [ordinal] in the full transcript, so a
  /// match found in a filtered view can be read with its surrounding context.
  /// The seek runs in build once `_lens == all` has put the row back in the
  /// list.
  void _jumpToContext(int ordinal) {
    setState(() {
      _lens = FeedLens.all;
      _followTail = false;
      _pendingContextOrd = ordinal;
    });
  }

  /// The Navigator **Errors** tab — the run's COMPLETE error list as digest-only
  /// summary rows (class + relative time), exact + never-empty + no event-body
  /// fetch. An outline you jump *from*: a tap closes the drawer and lands the
  /// transcript on that error in full context (ADR-041 §2). Owns its own scroll
  /// controller [ctl] — it is a drawer list, NOT the transcript window.
  Widget _buildNavErrorsList(ScrollController ctl) {
    final ords = _sortedErrorOrdinals();
    if (ords.isEmpty) return _navEmpty('No errors — a clean run.');
    return ListView.builder(
      controller: ctl,
      padding: EdgeInsets.zero,
      itemExtent: _kErrorRowExtent,
      itemCount: ords.length,
      itemBuilder: (ctx, i) {
        final ord = ords[i];
        final ts = widget.runAnchorTs?[ord];
        return ErrorSummaryRow(
          ordinal: i + 1,
          errorClass: widget.runErrorClasses?[ord] ?? 'error',
          label: widget.runErrorLabels?[ord],
          ts: ts,
          active: _activeSeekOrd == ord,
          onTap: () => _jumpFromOutline(ord),
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
        final ord = _turnAnchorOf(r);
        final ts0 = (r['start_ts'] ?? '').toString();
        // Reorder so the null-aware index sits in the false branch — `?[` right
        // after a ternary `?` trips the Dart parser.
        final ts = ts0.isNotEmpty ? ts0 : widget.runAnchorTs?[ord];
        return TurnSummaryRow(
          // Label from the sorted-list position (1-based). The digest's own
          // `idx` is a PER-AGENT counter that resets to 0 on every session
          // resume (#63) — using it makes labels restart and duplicate
          // (1,2,3,1,2,3,…). `_sortedTurnRows` already orders every turn in the
          // session by its session-scoped `start_ordinal`, so `i + 1` is a
          // stable, monotonic 1..N turn number across resume boundaries.
          ordinal: i + 1,
          status: (r['status'] ?? '').toString(),
          open: r['open'] == true,
          durationMs: (r['duration_ms'] as num?)?.toInt() ?? 0,
          toolCount: (r['tool_count'] as num?)?.toInt() ?? 0,
          toolFailed: (r['tool_failed'] as num?)?.toInt() ?? 0,
          errorCount: (r['error_count'] as num?)?.toInt() ?? 0,
          ts: ts,
          active: _activeSeekOrd == ord,
          onTap: () => _jumpFromOutline(ord),
        );
      },
    );
  }

  /// Outline-row tap (Turns / Errors): land the transcript on [ordinal] in full
  /// card context. The Navigator drawer **stays open** (ADR-041 §4) so the user
  /// can keep stepping the outline; they close it explicitly when done.
  void _jumpFromOutline(int ordinal) {
    _handleExternalSeek(ordinal);
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
          padding: const EdgeInsets.fromLTRB(12, Spacing.s8, 12, Spacing.s8),
          child: SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              icon: const Icon(Icons.my_location, size: 16),
              label: Text(pos != null
                  ? 'Jump to event ${pos.n} / ${pos.m}'
                  : 'Jump to event…'),
              onPressed: (pos != null && pos.m > 1) ? _openJumpSheet : null,
            ),
          ),
        ),
        Expanded(
          child: marks.isEmpty
              ? _navEmpty('No tool calls, turns, or errors to map yet.')
              : Padding(
                  padding: const EdgeInsets.fromLTRB(Spacing.s16, 4, Spacing.s16, 12),
                  child: FeedMinimap(
                    marks: marks,
                    // Tap a tick → land on that landmark. The drawer stays open
                    // (ADR-041 §4); the user closes it explicitly.
                    // In run-anchor mode the tick's coordinate is a
                    // session_ordinal (ADR-042); in loaded-window mode it is the
                    // built row's ordinal — both route through the same seek.
                    onJump: (frac, ord) {
                      if (_runAnchorMode) {
                        _handleExternalSeek(ord);
                      } else {
                        _seekToFrac(frac, ord);
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
              padding: const EdgeInsets.fromLTRB(16, Spacing.s12, 16, 16),
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

  /// Random-access jump to an arbitrary run position [n] (1-based). [n] *is* the
  /// `session_ordinal` (ADR-042) — the dense, session-unique coordinate — so the
  /// jump is a direct ordinal-keyset window reset, no seq→ts recovery hop.
  Future<void> _jumpToOrdinal(int n) async {
    if (_ordIsLoaded(n)) {
      _landOnOrd(n);
      return;
    }
    await _resetWindowAround(n);
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
    final errorCount = widget.runErrorOrdinals?.length ?? 0;
    final turnCount = _sortedTurnRows().length;
    return Positioned.fill(
      child: Stack(
        children: [
          // Scrim — tap anywhere outside the panel to dismiss.
          GestureDetector(
            onTap: () => widget.onNavigatorOpenChanged(false),
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
                              const EdgeInsets.fromLTRB(Spacing.s12, Spacing.s8, Spacing.s8, 4),
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
                                onPressed: () =>
                                    widget.onNavigatorOpenChanged(false),
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
    final l10n = AppLocalizations.of(context)!;
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
    if (_pendingContextOrd != null && _lens == FeedLens.all) {
      final target = _pendingContextOrd!;
      _pendingContextOrd = null;
      var idx = lensed.indexWhere((e) => _ordOf(e) == target);
      if (idx < 0) {
        // No exact row — the anchor may be a hidden marker (ACP turn.start) or
        // filtered out. Land on the nearest visible row at or after it.
        var bestOrd = 1 << 30;
        for (var i = 0; i < lensed.length; i++) {
          final o = _ordOf(lensed[i]);
          if (o >= target && o < bestOrd) {
            bestOrd = o;
            idx = i;
          }
        }
      }
      if (idx >= 0) {
        final landOrd = _ordOf(lensed[idx]);
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _seekToLoadedIndex(idx, landOrd > 0 ? landOrd : target);
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
                ? (widget.runTurnOrdinals?.length ?? 0)
                : (_runAnchorMode && l == FeedLens.errors)
                    ? (widget.runErrorOrdinals?.length ?? 0)
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
      // index (every turn start), positioned by their run ordinal
      // (session_ordinal / total, ADR-042). The mark's `seq` field carries the
      // ordinal — the seek treats it as the session coordinate.
      for (final a in feedRunAnchorMarks(
        errorSeqs: widget.runErrorOrdinals ?? const <int>[],
        turnSeqs: widget.runTurnOrdinals ?? const <int>[],
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
          seq: _ordOf(e),
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
                  final builtOrd = _ordOf(ev);
                  _seek.recordBuiltRow(i, builtOrd);
                  final isTarget =
                      _activeSeekOrd != null && builtOrd == _activeSeekOrd;
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
                    final ord = _ordOf(ev);
                    if (ord > 0) {
                      card = Stack(
                        children: [
                          card,
                          Positioned(
                            top: 2,
                            left: 2,
                            child: ContextJumpButton(
                                onTap: () => _viewInContext(ord)),
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
                        borderRadius: Radii.mdBorder,
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
                        horizontal: 12, vertical: Spacing.s8),
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
                            l10n.noLensEventsRun(
                                FeedFilterControl.labelFor(l10n, _lens)),
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
                  onTap: () => widget.onNavigatorOpenChanged(true),
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
              if (widget.navigatorOpen) _buildNavigatorOverlay(minimapMarks),
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

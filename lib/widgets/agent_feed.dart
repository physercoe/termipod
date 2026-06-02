// AgentFeed — mobile renderer for the hub's agent_events stream
// (blueprint P2.1). Subscribes to SSE, backfills any seq the user
// missed, and lays each event out as a typed card (text, tool_call,
// tool_result, completion, lifecycle, …). Unknown kinds fall through
// to a raw JSON card so the transcript is never silently dropped.
import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';
import 'agent_compose.dart';
import 'agent_feed/event_card.dart';
import 'agent_feed/feed_misc.dart';
import 'agent_feed/feed_reducer.dart';
import 'agent_feed/feed_render.dart';
import 'agent_feed/random_access_loader.dart';
import 'agent_feed/interaction_cards.dart';
import 'agent_feed/telemetry_strip.dart';
import 'session_details_sheet.dart';

// W0 (docs/plans/agent-feed-split.md): the reducer/formatter layer now
// lives in agent_feed/feed_reducer.dart. Re-export it so the ten
// agent_feed_* reducer tests (which import this file) resolve unchanged
// and external callers keep their single import surface.
export 'agent_feed/feed_reducer.dart';

/// Drives an external jump-to-seq into an [AgentFeed] from a sibling — the
/// analysis-mode payoff (plan P2): the run-report dashboard and the
/// structure-index rows live *above* the feed, so a tapped error/turn/tool
/// needs a channel down into the feed's seek. The feed listens; [seekTo]
/// bumps a generation counter so re-requesting the *same* seq still re-fires
/// (a second tap on the same error jumps again). The feed resolves the seq
/// against its loaded window, paging toward it if the anchor is older than
/// what's loaded (bounded), then anchors+highlights the row.
class AgentFeedSeekController extends ChangeNotifier {
  int? _seq;
  String? _ts;
  int _generation = 0;

  /// The most recently requested seq, or null before any request.
  int? get seq => _seq;

  /// The anchor's timestamp, when the caller knows it (the Turns index carries
  /// `start_ts`). The session-scoped random-access loader needs it to window
  /// around the anchor via the `(ts, seq)` keyset; a seq-only request (the
  /// Errors stat) falls back to the bounded page-walk.
  String? get ts => _ts;

  /// Increments on every [seekTo]; lets the feed distinguish a fresh
  /// request for the same seq from a no-op rebuild.
  int get generation => _generation;

  void seekTo(int seq, {String? ts}) {
    _seq = seq;
    _ts = ts;
    _generation++;
    notifyListeners();
  }
}

/// Renders a live, scrollable feed of agent_events for [agentId]. Keeps
/// its own seq cursor so reconnects don't replay the whole history. The
/// first frame is the in-DB backfill fetched via listAgentEvents; after
/// that, new frames arrive through streamAgentEvents.
///
/// When [sessionId] is set, the backfill and live stream are filtered
/// to one session. The new-session flow keeps the same agent_id while
/// opening a fresh session row, so an unfiltered Feed would replay the
/// prior closed session's transcript into the "fresh" chat.
///
/// [onSessionInit] fires whenever the latest session.init payload
/// changes (cold open + every reconnect). Used by SessionChatScreen
/// to lift the model/permission/tools/mcp summary into the AppBar so
/// the transcript itself doesn't burn a row of vertical real estate
/// on a fixed-shape header.
class AgentFeed extends ConsumerStatefulWidget {
  final String agentId;
  final String? sessionId;
  final EdgeInsetsGeometry padding;
  final void Function(Map<String, dynamic> payload)? onSessionInit;
  /// Fires whenever the feed's accumulated mode/model state changes
  /// (cold open, every system event with currentModeId/currentModelId,
  /// reconnects). Null while the agent hasn't advertised either
  /// capability — the SessionChatScreen AppBar uses that to hide its
  /// picker icon. The bound onPickMode / onPickModel callbacks here
  /// route through the same hub_client postAgentInput the inline
  /// strip used to call, so the parent can render the picker without
  /// the feed body losing its vertical real estate to the chip strip.
  final void Function(ModeModelPickerData? data)? onModeModelChanged;
  /// When set, after the cold-open backfill resolves the feed scrolls
  /// to and briefly highlights the event whose seq matches. Used by
  /// "Open in chat" from the approval-detail screen so the principal
  /// lands at the agent's turn that raised the request, not at the
  /// generic tail. The seq must lie within the cold-open page
  /// (_pageSize=200 newest); older events fall through to the
  /// default tail-scroll. Null = default tail behavior.
  final int? initialSeq;
  /// ADR-036 W6 — fires whenever the latest status_line frame's
  /// `session_name` field changes value. Used by SessionChatScreen
  /// to show claude's auto-derived label (e.g. "List directory
  /// files") as a sticky-header fallback when the user hasn't set
  /// a title. NEVER persisted: the parent only renders the hint;
  /// the hub `sessions.title` column stays under user control.
  /// Null = no name carried yet (cold open) or claude cleared it
  /// (rotation across `/clear`). User-set titles always win at the
  /// parent — this callback only sources the candidate.
  final void Function(String? name)? onSessionNameHint;
  /// v1.0.706 polish — fires whenever the latest status_line payload
  /// changes (every new status_line event, ~10s claude cadence).
  /// Used by SessionChatScreen to surface live mutable state
  /// (effort.level, output_style.name, thinking.enabled, fast_mode)
  /// in the session-details sheet — session.init only carries these
  /// at spawn time, and a mid-session `/style` or `/thinking` toggle
  /// has since changed them. The hub `agent_events.status_line`
  /// table preserves the full series; this callback just forwards
  /// the most recent snapshot. Null = no frame yet.
  final void Function(Map<String, dynamic>? payload)? onStatusLineChanged;
  /// P3 (docs/plans/agent-transcript-debug-and-header-parity.md) —
  /// responsive disclosure by container. `true` (default) is the
  /// constrained host: the lens lives in a floating funnel → combined
  /// filter/jump pill, no minimap. `false` is a full-screen host: the
  /// lens unfolds to a horizontal *bar* with per-lens counts and a
  /// right-edge minimap (turn ticks + red error ticks, tap to jump).
  final bool dense;
  /// When set (and [dense]), a floating expand affordance pushes the
  /// caller's dedicated full-screen transcript route. Null hides it —
  /// hosts that are already full-screen pass `dense: false` instead.
  final VoidCallback? onExpand;
  /// Plan P2 — lets a sibling (the analysis-mode run-report dashboard /
  /// structure index) jump the feed to a seq. The feed pages toward the
  /// anchor if it's older than the loaded window (bounded), then anchors +
  /// highlights it. Null = no external seek channel.
  final AgentFeedSeekController? seekController;
  /// Plan P2 — the run-lifetime total event count (from the digest), used to
  /// render a monotonic "event N of M" position over the full-screen log:
  /// the loaded window is only a slice, so M is the true total. Per-agent seq
  /// is the dense 1-based run ordinal, so N (≈ the viewport-top seq) is the
  /// honest position for a single-agent run. Null / full-screen-only.
  final int? totalEventCount;
  /// Plan P2 — full-run minimap anchors (from the digest's `errors_json`
  /// sample seqs + the turn index's `start_seq`s). When supplied (with
  /// [totalEventCount]), the right-edge minimap switches from loaded-window
  /// ticks to *whole-run* anchors positioned by `seq / total`, and a tap
  /// routes through the random-access seek — so a failure or turn anywhere in
  /// the run is one tap away, not just the loaded slice. Null = loaded-window
  /// minimap (the plain full-screen feed, which has no digest).
  final List<int>? runErrorSeqs;
  /// ADR-039 P2 — `seq → error class` (tool_error / failed_turn / error:<type>)
  /// for every whole-run error, straight from the digest. Lets the Errors lens
  /// render the COMPLETE error list as summary rows (class + time) with no
  /// event-body fetch; a tap jumps to the error in full context. Null on the
  /// plain Feed (no digest).
  final Map<int, String>? runErrorClasses;
  final List<int>? runTurnSeqs;
  /// Plan P2 — `seq → ts` for the run anchors that carry a timestamp (the turn
  /// index's `start_ts`). A minimap tap only has the anchor's seq; with the ts
  /// the jump can take the O(log n) `(ts, seq)` window reset instead of the
  /// bounded page-walk. Error anchors have no ts (the digest's sample seqs are
  /// ts-less), so they're absent here and fall back to the page-walk. Null =
  /// loaded-window minimap (no digest).
  final Map<int, String>? runAnchorTs;
  /// Plan P2 — enables the analysis-owned **random-access loader**: a jump to
  /// an off-window anchor *resets* the window around it (`(ts, seq)` keyset)
  /// instead of walking from the tail, and the feed gains a forward pager. Set
  /// only by the Insights surface; the live-tail Feed leaves it false, so every
  /// random-access code path is inert there (no regression).
  final bool randomAccess;
  const AgentFeed({
    super.key,
    required this.agentId,
    this.sessionId,
    this.padding = const EdgeInsets.all(12),
    this.onSessionInit,
    this.onModeModelChanged,
    this.initialSeq,
    this.onSessionNameHint,
    this.onStatusLineChanged,
    this.dense = true,
    this.onExpand,
    this.seekController,
    this.totalEventCount,
    this.runErrorSeqs,
    this.runErrorClasses,
    this.runTurnSeqs,
    this.runAnchorTs,
    this.randomAccess = false,
  });

  @override
  ConsumerState<AgentFeed> createState() => _AgentFeedState();
}

class _AgentFeedState extends ConsumerState<AgentFeed> {
  final List<Map<String, dynamic>> _events = [];
  // Event ids we've ingested. The de-dup key. Used instead of seq when
  // the feed is session-scoped: a resumed session spans multiple agents
  // and seq is per-agent, so seq values can collide between the prior
  // agent's history and the new agent's live events. id is globally
  // unique. We keep _maxSeq for the agent-only path's incremental SSE
  // backfill cursor (since=<seq>); the session path uses ts.
  final Set<String> _ids = <String>{};

  // Content-stable dedupe keys for events already in [_events]. Used
  // by the W1.3 replay-ingest filter: a session/load replay re-streams
  // historical turns under fresh agent_event ids and seqs, so the
  // existing _ids dedup misses them — we need to match on payload
  // content. Populated alongside _ids in [_ingestSnapshot] /
  // [_loadOlder] / SSE add. Events without a derivable key (raw,
  // lifecycle, system, plan/diff without stable id) don't add to the
  // set and pass through replay unchanged.
  final Set<String> _replayKeys = <String>{};
  int _maxSeq = 0;
  // Smallest seq we've loaded so the "load older" pager can ask for
  // anything strictly before it. 0 once we've reached the head of the
  // transcript (no older page to fetch). When the feed is session-scoped
  // the pager uses [_oldestTs] instead.
  int _minSeq = 0;
  // Oldest ts we've loaded — the load-older cursor for session-scoped
  // feeds. Empty until cold open returns at least one event.
  String _oldestTs = '';
  String? _error;
  bool _loading = true;
  // Cold open uses tail mode so a long transcript shows the most recent
  // turns instead of the oldest. Bootstrap and load-older both pull
  // [_pageSize] rows per page so the user can keep scrolling backward.
  static const int _pageSize = 200;
  // True while a load-older fetch is in flight; suppresses duplicate
  // triggers from the scroll listener firing rapidly near the top.
  bool _loadingOlder = false;
  // True once a load-older fetch returns fewer than _pageSize rows —
  // we've reached the start of the session, no more pages exist.
  bool _atHead = false;
  // Plan P2 (random-access loader) — whether the loaded window reaches the
  // live tail. True for the normal tail-anchored window (and ALWAYS true in
  // the live-tail Feed, which never resets). A random-access reset to a
  // mid-run anchor sets it false, which (a) arms the forward pager
  // [_maybeLoadNewer] and (b) silences the SSE tail-append so a non-contiguous
  // live event can't graft onto a mid-run window. Re-armed true when the
  // forward pager reaches the tail or a jump-to-latest re-bootstraps it.
  bool _windowHasTail = true;
  // True while a forward (load-newer) fetch is in flight; the random-access
  // complement of _loadingOlder.
  bool _loadingNewer = false;
  // Newest loaded (ts, seq) — the forward pager's cursor. Only maintained on
  // the random-access path (the live-tail loader pages backward + SSE-tails,
  // so it never needs a forward cursor).
  String _newestTs = '';
  int _newestSeq = 0;
  // When the bootstrap fetch falls back to the offline cache (server
  // unreachable, 5xx, etc.), we keep the cached transcript visible and
  // surface a banner with the snapshot timestamp. Cleared the moment a
  // live SSE event arrives so the user can tell the feed is current
  // again.
  DateTime? _staleSince;
  // session_id we last forwarded to onSessionInit; only re-fire when it
  // changes (i.e. when the agent reconnects with a new ACP/Claude
  // session id) so we don't churn the parent's setState on every event.
  String? _lastReportedInitSid;
  StreamSubscription<Map<String, dynamic>>? _sub;
  final ScrollController _scroll = ScrollController();
  bool _followTail = true;
  // W1.B (steward UX): hide debug-fidelity kinds by default and reveal
  // them under an explicit toggle (matches Anthropic's Ctrl+O verbose
  // toggle behavior, and Happy's "transcript styled, raw on demand"
  // pattern). Default off — most users want a chat surface, not a
  // protocol trace. Per-AgentFeed instance, not global.
  bool _verbose = false;
  // Reconnect bookkeeping: exponential backoff (1, 2, 4, 8, 16s cap) so a
  // flaky hub connection doesn't hammer the server. Reset to 0 the moment
  // we successfully receive an event.
  int _reconnectAttempt = 0;
  Timer? _reconnectTimer;
  // SSE drops are common for benign reasons (network blip, app suspend,
  // proxy idle timeout). The banner is noisy when drops self-heal in <5s
  // because `sinceSeq` ensures no events are missed. Defer the banner
  // by [_bannerGrace] so quick recoveries are invisible to the user.
  Timer? _bannerGraceTimer;
  static const Duration _bannerGrace = Duration(seconds: 5);
  // Counter for events that arrived while the user had scrolled away
  // from the tail. Powers the "N new ↓" pill so users know the feed is
  // alive without being yanked back to the bottom.
  int _newWhileAway = 0;
  // Anchor for the "scroll to specific seq" feature. Attached to
  // whichever AgentEventCard matches [_activeSeekSeq] during build;
  // ensureVisible needs a real BuildContext, hence the key. Drives both
  // the cold-open initialSeq deep-link and the lens match-stepper.
  final GlobalKey _seekKey = GlobalKey();
  // The seq the feed is currently anchored to (cold-open initialSeq, or
  // the active lens match). Null = no anchor (tail-follow). The matching
  // card gets [_seekKey] + a tinted border while [_seekHighlight] holds.
  int? _activeSeekSeq;
  // True for ~1.2s after a successful seek so the matched event renders
  // with a tinted border, telling the user where they landed. Cleared
  // by [_seekHighlightTimer].
  bool _seekHighlight = false;
  Timer? _seekHighlightTimer;
  // Single-select transcript lens (P1 — docs/plans/agent-transcript-
  // debug-and-header-parity.md). Ephemeral per-AgentFeed instance: resets
  // when the surface closes so a stale filter never surprises a returning
  // user. Orthogonal to [_verbose] (depth, not family).
  FeedLens _lens = FeedLens.all;

  // ADR-036 W4-c — periodic poll of GET /sessions/{id}/cost for the
  // session-cost chip and its per-model-breakdown tooltip. Set on the
  // first poll, refreshed every [_sessionCostPollInterval]. Null until
  // the first response (chip self-gates on null per D9). The whole
  // map carries: `total_usd`, `breakdown_by_model`, `tokens_by_model`,
  // `missing_models`, `snapshot_date`, `origin`, `imputed`.
  Map<String, dynamic>? _sessionCost;
  Timer? _sessionCostTimer;
  // 15s cadence matches statusLine's ~10s; longer would feel laggy
  // when watching a chunky tool-use turn in real time, shorter is
  // wasted polling. Each fetch is ~one cheap aggregation query on the
  // hub side, so 15s is comfortable headroom.
  static const Duration _sessionCostPollInterval = Duration(seconds: 15);

  // Generation of the last external seek we serviced — so a controller
  // notification for a seq we already jumped to (e.g. a parent rebuild
  // re-attaching the listener) doesn't re-fire, but a fresh seekTo (which
  // bumps the generation) does.
  int _lastSeekGeneration = 0;

  @override
  void initState() {
    super.initState();
    _bootstrap();
    _scroll.addListener(_onScroll);
    widget.seekController?.addListener(_onSeekRequest);
    _startSessionCostPolling();
  }

  @override
  void didUpdateWidget(covariant AgentFeed oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.sessionId != widget.sessionId) {
      // Session swap: nuke the prior cost so the chip self-gates
      // until the new session's first poll lands, then restart the
      // timer rooted at the new id.
      _sessionCost = null;
      _startSessionCostPolling();
    }
    if (oldWidget.seekController != widget.seekController) {
      oldWidget.seekController?.removeListener(_onSeekRequest);
      widget.seekController?.addListener(_onSeekRequest);
    }
  }

  @override
  void dispose() {
    _reconnectTimer?.cancel();
    _bannerGraceTimer?.cancel();
    _sessionCostTimer?.cancel();
    _seekHighlightTimer?.cancel();
    widget.seekController?.removeListener(_onSeekRequest);
    _sub?.cancel();
    _scroll.dispose();
    super.dispose();
  }

  // Controller fired a jump request. Dedup on generation so only a genuine
  // new seekTo triggers a seek.
  void _onSeekRequest() {
    final c = widget.seekController;
    if (c == null) return;
    if (c.generation == _lastSeekGeneration) return;
    _lastSeekGeneration = c.generation;
    final seq = c.seq;
    if (seq != null) _handleExternalSeek(seq, c.ts);
  }

  /// Jump the feed to [seq] (the analysis dashboard / structure index). If the
  /// seq is already loaded we anchor it directly. Otherwise:
  ///   - In [AgentFeed.randomAccess] mode with a known [ts] (the Insights
  ///     surface, which carries the anchor's timestamp), we **reset the window
  ///     around the anchor** — one block before + one after via the `(ts, seq)`
  ///     keyset — O(log n), reachable at any depth, decoupled from the tail.
  ///   - Otherwise we fall back to the bounded page-walk: page older toward the
  ///     anchor (the live-tail window is contiguous from the tail), capped so a
  ///     far/unreachable anchor can't load forever.
  Future<void> _handleExternalSeek(int seq, [String? ts]) async {
    if (_seqIsLoaded(seq)) {
      // Robust landing: after a lazy prepend the row is usually off-screen, so
      // route through the convergent index seek (build resolves the row's index
      // and binary-searches the offset onto it) rather than the ensureVisible-
      // only [_seekToSeq], which no-ops on an unrealised row.
      _landOnSeq(seq);
      return;
    }
    final sessionScoped = (widget.sessionId ?? '').isNotEmpty;
    if (widget.randomAccess && sessionScoped && ts != null && ts.isNotEmpty) {
      await _resetWindowAround(seq, ts);
      return;
    }
    const maxPages = 12; // _pageSize(200) × 12 = 2400 events of headroom.
    for (var i = 0; i < maxPages; i++) {
      if (_atHead) break;
      // Agent scope: once the window's floor has passed the target, paging
      // further up can't reveal it (it's newer than the loaded floor, i.e.
      // we jumped away from the tail) — stop and fall back.
      if (!sessionScoped && _minSeq > 0 && _minSeq <= seq) break;
      await _maybeLoadOlder();
      if (!mounted) return;
      if (_seqIsLoaded(seq)) break;
    }
    if (!mounted) return;
    if (_seqIsLoaded(seq)) {
      _landOnSeq(seq);
    }
    // Else: a ts-less anchor (older digest) beyond the page-walk cap — leave
    // the viewport where it is. Yanking to the top here was the "minimap tap
    // jumps to the top" bug; with anchor timestamps (turns + errors now carry
    // them) the reset path above handles every reachable anchor instead.
  }

  /// Land on [seq] robustly even when its row isn't currently realised. Defers
  /// to the convergent index seek via the pending-context mechanism: a rebuild
  /// finds the row's index in the laid-out (lensed) list and binary-searches
  /// the scroll offset onto it ([_seekToLoadedIndex]/[_convergeToIndex]), which
  /// — unlike [_seekToSeq]'s lone `ensureVisible` — works on an off-screen,
  /// lazily-built row. The anchor may be a hidden marker (an ACP `turn.start`
  /// isn't rendered), so the consumer lands on the nearest visible row at or
  /// after it. Resets the lens to All so the landing row has its surrounding
  /// context. This is the shared landing for every external/random-access jump.
  void _landOnSeq(int seq) => _jumpToContext(seq);

  bool _seqIsLoaded(int seq) =>
      _events.any((e) => (e['seq'] as num?)?.toInt() == seq);

  /// Bind a [RandomAccessLoader] to this feed's `agentId` + session scope. The
  /// loader owns the `(ts, seq)` keyset fetch shapes (around-anchor / page-
  /// newer); the State keeps the buffer + setState. Cheap to build per call —
  /// it holds no state, just the bound fetch closure.
  RandomAccessLoader _randomAccessLoader(HubClient client, String sid) =>
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
              sessionId: sid,
              beforeTs: beforeTs,
              beforeSeq: beforeSeq,
              afterTs: afterTs,
              afterSeq: afterSeq,
              limit: limit,
            ),
      );

  /// Random-access window reset (plan P2): replace the loaded window with one
  /// block fetched *around* the anchor `(ts, seq)` — the backward half (events
  /// before the anchor key, DESC) and the forward half (the anchor and after,
  /// ASC) via the session `(ts, seq)` compound keyset. Decoupled from the
  /// live-tail loader: after a reset the window may not reach the tail, so
  /// [_windowHasTail] goes false, which arms the forward pager and silences
  /// the SSE tail-append (a mid-run window must not grow a non-contiguous live
  /// event). Only ever runs in [AgentFeed.randomAccess] mode, so the Feed view
  /// never enters this path.
  Future<void> _resetWindowAround(int seq, String ts,
      {bool keepLens = false}) async {
    final sid = widget.sessionId;
    final client = ref.read(hubProvider.notifier).client;
    if (sid == null || client == null) return;
    try {
      final window = await _randomAccessLoader(client, sid).fetchAround(seq, ts);
      if (!mounted) return;
      if (window.isEmpty) {
        // Nothing came back (anchor out of range) — leave the current window
        // untouched rather than yanking the viewport to the top.
        return;
      }
      _ingestWindow(window.ascending);
      setState(() {
        _atHead = window.reachedHead; // reached the start
        _windowHasTail = window.reachedTail; // reached the live tail
        _followTail = false;
        _loading = false;
        _error = null;
      });
      // Land on the anchor once the new window lays out. The window was just
      // swapped wholesale, so the anchor's row sits mid-list and isn't
      // realised — route through the convergent index seek (which resolves the
      // nearest-visible row >= the anchor and binary-searches onto it) rather
      // than the ensureVisible-only [_seekToSeq], which would silently no-op
      // here. This was the "turn jump lands wrong / does nothing" bug.
      if (keepLens) {
        _landOnSeqKeepLens(seq);
      } else {
        _landOnSeq(seq);
      }
    } catch (_) {
      // Network blip — leave the existing window; the caller can retry.
    }
  }

  /// Replace [_events] with a fresh, contiguous [ascending] window (used by the
  /// random-access reset). Distinct from [_ingestSnapshot] (the live-tail cold
  /// open) because it also tracks the *newest* loaded `(ts, seq)` — the forward
  /// pager's cursor, which the tail-anchored loader never needs.
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
        // Newest by (ts, seq) — the forward cursor.
        if (t.compareTo(_newestTs) > 0 ||
            (t == _newestTs && s > _newestSeq)) {
          _newestTs = t;
          _newestSeq = s;
        }
      }
    });
  }

  /// Forward pager (plan P2) — the complement of [_maybeLoadOlder], armed only
  /// after a random-access reset has left the window short of the tail. Pages
  /// the next block *newer* than the loaded edge via the `(ts, seq)` keyset and
  /// appends it. When a short page comes back we've reached the live tail, so
  /// [_windowHasTail] flips true (re-enabling SSE append). Inert outside
  /// [AgentFeed.randomAccess] (the Feed view never sets [_windowHasTail] false).
  Future<void> _maybeLoadNewer() async {
    if (_loadingNewer || _windowHasTail) return;
    final sid = widget.sessionId;
    final client = ref.read(hubProvider.notifier).client;
    if (sid == null || client == null || _newestTs.isEmpty) return;
    setState(() => _loadingNewer = true);
    try {
      final page = await _randomAccessLoader(client, sid)
          .fetchNewer(_newestTs, _newestSeq);
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
        // A short page means there's nothing newer left — we've rejoined the
        // live tail, so live SSE frames may append again.
        if (page.reachedTail) _windowHasTail = true;
      });
    } catch (_) {
      // Silent: the next scroll re-triggers.
    } finally {
      if (mounted) setState(() => _loadingNewer = false);
    }
  }

  /// (Re)start the periodic session-cost poll. No-ops when there is no
  /// sessionId scope — the chip suppresses itself in that case anyway
  /// (the endpoint requires a session id). Performs an immediate fetch
  /// so the chip lights up on first paint without waiting a full
  /// interval.
  void _startSessionCostPolling() {
    _sessionCostTimer?.cancel();
    final sid = widget.sessionId;
    if (sid == null || sid.isEmpty) return;
    _fetchSessionCost(sid);
    _sessionCostTimer = Timer.periodic(_sessionCostPollInterval, (_) {
      if (!mounted) return;
      _fetchSessionCost(sid);
    });
  }

  Future<void> _fetchSessionCost(String sid) async {
    try {
      // hubProvider.client is nullable (HubNotifier exposes null
      // before init / after sign-out). Skip the poll silently — the
      // chip self-gates on the null cache; the next timer tick will
      // re-attempt once the client is up.
      final client = ref.read(hubProvider.notifier).client;
      if (client == null) return;
      final out = await client.getSessionCost(sid);
      if (!mounted) return;
      // sessionId might have flipped while the request was in flight
      // — drop the response in that case so we don't stamp the old
      // session's cost onto the new chip.
      if (widget.sessionId != sid) return;
      setState(() => _sessionCost = out);
    } catch (_) {
      // Swallow — the chip self-gates on null and a transient hub blip
      // shouldn't blank a previously-good number. Leave _sessionCost
      // at whatever it last held.
    }
  }

  // User scrolling up away from the tail should stop auto-follow so
  // incoming events don't yank them back to the bottom mid-read. Any
  // scroll back within ~40px of the bottom re-enables it.
  //
  // Reaching the top edge (within ~120px) triggers the load-older
  // pager — _maybeLoadOlder dedupes against in-flight fetches and the
  // _atHead flag, so the listener can fire as often as the gesture
  // wants without piling up requests.
  void _onScroll() {
    if (!_scroll.hasClients) return;
    final maxExt = _scroll.position.maxScrollExtent;
    final atBottom = _scroll.position.pixels >= maxExt - 40;
    final frac = maxExt <= 0
        ? 1.0
        : (_scroll.position.pixels / maxExt).clamp(0.0, 1.0);
    // Update the minimap position indicator. Coarse threshold (~1%) so a
    // scroll doesn't rebuild the feed on every pixel — matches the cadence
    // of the old integer scroll-percent.
    if ((frac - _viewFrac).abs() > 0.01) {
      setState(() => _viewFrac = frac);
    }
    // CRITICAL: only let a *user* scroll flip tail-follow. A programmatic
    // scroll (a seek/scrub/jump) can momentarily land near the bottom — if
    // that re-enabled _followTail, the next live event would yank the user
    // to the end (the "jump to end" tester bug). During programmatic
    // motion we touch neither _followTail nor the load pager.
    if (_programmaticScroll) return;
    // ADR-039 P2 — the Errors summary list is a digest-only list with no
    // _events paging; scrolling it must not trigger load-older/newer or
    // tail-follow on the (unrelated) _events window.
    if (_errorsSummaryMode) return;
    // Random-access window short of the tail (plan P2): the bottom edge pages
    // *newer* — the forward complement of load-older at the top. We're not at
    // the live tail yet, so this is NOT a tail-follow point. For the Feed
    // (randomAccess false, _windowHasTail always true) atTrueTail == atBottom,
    // so the live-tail behaviour below is unchanged.
    final atTrueTail = atBottom && (_windowHasTail || !widget.randomAccess);
    if (widget.randomAccess &&
        !_windowHasTail &&
        _scroll.position.pixels >= maxExt - 120) {
      _maybeLoadNewer();
    }
    if (_followTail != atTrueTail) {
      setState(() {
        _followTail = atTrueTail;
        // Returning to the tail clears the pending-event counter; the
        // pill disappears on the same frame.
        if (atTrueTail) {
          _newWhileAway = 0;
          // Reset the turn-stepper anchor so the next `‹` starts from the
          // newest prompt rather than wherever we last jumped.
          _activeSeekSeq = null;
        }
      });
    }
    if (_scroll.position.pixels <= 120) _maybeLoadOlder();
  }

  // Viewport-top position (0..1) for the minimap indicator.
  double _viewFrac = 1.0;

  /// Plan P2 — the monotonic "event N of M" position for the full-screen log.
  /// The loaded window is a contiguous tail slice spanning seqs
  /// [_minSeq, _maxSeq]; the viewport-top fraction [_viewFrac] interpolates
  /// linearly across it, and per-agent seq is the dense run ordinal, so N is
  /// the viewport-top seq. M is the digest's run-lifetime total (fallback: the
  /// newest loaded seq before the digest resolves). Returns null until there's
  /// a window to position within. N is clamped into [1, M] so a multi-agent
  /// session (per-agent seq isn't a run-wide ordinal) still reads sanely.
  ({int n, int m})? _logPosition() {
    final total = widget.totalEventCount;
    // Insight/random-access: read N straight from the top-built row's run
    // ordinal (the seq). It's exact and monotonic and — crucially — doesn't
    // lurch when the window grows by a load-older/newer page (the viewFrac
    // interpolation does, because both the fraction and the [_minSeq, _maxSeq]
    // span shift under it). Falls back to the interpolation until the first
    // layout records a top seq.
    if (widget.randomAccess && total != null && total > 0 && _lastTopBuiltSeq > 0) {
      final n = _lastTopBuiltSeq.clamp(1, total);
      return (n: n, m: total);
    }
    return feedLogPosition(
      minSeq: _minSeq,
      maxSeq: _maxSeq,
      viewFrac: _viewFrac,
      totalEventCount: total,
    );
  }

  /// True when the full-screen minimap should render whole-run anchors (from
  /// the digest + turn index) rather than loaded-window ticks — i.e. the
  /// analysis surface supplied both a run total and at least one anchor.
  bool get _runAnchorMode =>
      widget.totalEventCount != null &&
      ((widget.runErrorSeqs?.isNotEmpty ?? false) ||
          (widget.runTurnSeqs?.isNotEmpty ?? false));

  // ADR-039 P2 — the Errors lens renders the WHOLE-RUN error list as digest-only
  // summary rows (class + time), not a client filter over the loaded window:
  // exact, never-empty, no event-body fetch. A tap jumps to the error in full
  // context. Gated to the random-access Insight surface with errors present.
  static const double _kErrorRowExtent = 52.0;
  bool _isErrorsSummaryLens(FeedLens lens) =>
      widget.randomAccess &&
      lens == FeedLens.errors &&
      (widget.runErrorSeqs?.isNotEmpty ?? false);
  bool get _errorsSummaryMode => _isErrorsSummaryLens(_lens);
  // The run's error seqs, ascending (oldest→newest) so the newest sits at the
  // bottom like the chat transcript. Built once per build that needs it.
  List<int> _sortedErrorSeqs() => [...?widget.runErrorSeqs]..sort();

  /// The minimap's position indicator. In full-run anchor mode it tracks the
  /// run ordinal (N/M) so the thumb sits where the viewport is *in the whole
  /// run*; otherwise it tracks the within-loaded-window fraction.
  double _minimapViewportFrac() {
    if (_runAnchorMode) {
      final p = _logPosition();
      if (p != null && p.m > 0) return (p.n / p.m).clamp(0.0, 1.0);
    }
    return _viewFrac;
  }
  // >0 while a seek/scrub/jump drives the scroll, so [_onScroll] doesn't
  // mistake the programmatic motion for the user reaching the tail. A depth
  // counter (not a bool) so overlapping programmatic scrolls — a seek's
  // animateTo followed by its ensureVisible — keep the guard up until ALL
  // of them finish.
  bool get _programmaticScroll => _programmaticScrollDepth > 0;
  int _programmaticScrollDepth = 0;

  // The realized (built) row-index window of the ListView, refreshed every
  // layout from the itemBuilder. A jump-to-known-row seek uses this as
  // feedback to binary-search the scroll offset onto a target index without
  // assuming uniform row heights (see [_seekToLoadedIndex]). Reset at the
  // top of the build that owns the list; -1 / length are empty sentinels.
  int _minBuiltIdx = 0;
  int _maxBuiltIdx = -1;
  // Length of the current lensed (rendered) list, snapshotted each build. The
  // convergent index seek reads it to seed its first probe *proportionally*
  // (idx / count → offset) so a structural jump lands within a screen of the
  // target on the first frame — and to land the card by a final proportional
  // jump if the realized-window feedback can't fully converge, so a jump never
  // leaves the card off-screen ("card not in viewport").
  int _lensedCount = 0;
  // Plan P2 (accurate Insight position) — the smallest seq among the rows
  // realised in the current build (the topmost on-screen card ≈ the viewport
  // top). [_topBuiltSeq] accumulates during a frame's layout; [_lastTopBuiltSeq]
  // snapshots it at the next build, so [_logPosition] can read a stable value.
  // Per-agent seq is the dense run ordinal, so this is the honest "event N":
  // a *seq* (not a pixel fraction), it stays put when load-older/newer grows
  // the window (the same event keeps the same seq) and moves monotonically with
  // scroll — fixing the non-monotonic jump the viewFrac interpolation showed.
  int _topBuiltSeq = 0;
  int _lastTopBuiltSeq = 0;

  // Mark a synchronous scroll (jumpTo) as programmatic; clear after the
  // frame it lands on.
  void _jumpProgrammatic(void Function() body) {
    _programmaticScrollDepth++;
    body();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_programmaticScrollDepth > 0) _programmaticScrollDepth--;
    });
  }

  // Mark an animated scroll as programmatic for its whole duration —
  // animateTo spans many frames, each firing [_onScroll], so the flag must
  // hold until the animation future completes (a one-frame guard would let
  // mid-animation ticks flip tail-follow).
  void _animateProgrammatic(Future<void> Function() run) {
    _programmaticScrollDepth++;
    run().whenComplete(() {
      if (_programmaticScrollDepth > 0) _programmaticScrollDepth--;
    });
  }

  // Pull the latest session.init for this agent regardless of which
  // session it was emitted into. Used to keep the AppBar chip alive
  // across "new session" opens — claude only emits session.init once
  // per process start, and it lands in whichever session was open at
  // that moment. Without this fallback, every new session opened
  // against an existing steward shows an empty AppBar. Best-effort:
  // failures are silent because the chip is decorative (it doesn't
  // affect message delivery).
  Future<void> _maybeBackfillSessionInit(HubClient client) async {
    if (latestSessionInitPayload(_events) != null) return;
    if (widget.onSessionInit == null) return;
    try {
      // Pull the agent's tail across ALL sessions; merge every
      // session.init payload we find in chronological order (newer
      // events overwrite earlier fields, earlier-only fields persist).
      // Page size is small because session.init is rare. Walk forward
      // so the merge order matches the live build() path (see
      // latestSessionInitPayload in feed_reducer.dart for the rationale).
      final any = await client.listAgentEvents(
        widget.agentId,
        tail: true,
        limit: 200,
        // No sessionId — that's the whole point of the fallback.
      );
      if (!mounted) return;
      Map<String, dynamic>? merged;
      for (final e in any) {
        if ((e['kind'] ?? '').toString() != 'session.init') continue;
        final p = e['payload'];
        if (p is! Map) continue;
        final m = p.cast<String, dynamic>();
        if (merged == null) {
          merged = Map<String, dynamic>.from(m);
        } else {
          merged.addAll(m);
        }
      }
      if (merged == null) return;
      final sid = (merged['session_id'] ?? '').toString();
      final model = (merged['model'] ?? '').toString();
      final key = '$sid|$model';
      if (key == _lastReportedInitSid) return;
      _lastReportedInitSid = key;
      final payload = merged;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) widget.onSessionInit?.call(payload);
      });
    } catch (_) {
      // Silent — chip is decorative.
    }
  }

  Future<void> _maybeLoadOlder() async {
    if (_loadingOlder || _atHead) return;
    // Page-cursor preconditions: agent-scoped uses _minSeq (per-agent
    // monotonic; >1 means there's at least one older row), session-scoped
    // uses _oldestTs (a non-empty ts means we have at least one event
    // we can paginate before).
    final sessionScoped = (widget.sessionId ?? '').isNotEmpty;
    if (!sessionScoped && _minSeq <= 1) return;
    if (sessionScoped && _oldestTs.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() => _loadingOlder = true);
    final priorMinSeq = _minSeq;
    final priorOldestTs = _oldestTs;
    final priorMaxExtent =
        _scroll.hasClients ? _scroll.position.maxScrollExtent : 0.0;
    final priorPixels = _scroll.hasClients ? _scroll.position.pixels : 0.0;
    try {
      final older = await client.listAgentEvents(
        widget.agentId,
        before: sessionScoped ? null : priorMinSeq,
        beforeTs: sessionScoped ? priorOldestTs : null,
        // In random-access mode, pair the load-older ts cursor with the
        // seq tiebreak so same-ts events at the window floor aren't dropped
        // (the (ts, seq) keyset). Gated → the Feed's load-older is untouched.
        beforeSeq:
            (widget.randomAccess && sessionScoped) ? _minSeq : null,
        limit: _pageSize,
        sessionId: widget.sessionId,
      );
      if (!mounted) return;
      // Server returns DESC; flip to ASC so the prepend keeps the
      // chat's "older-above" invariant. Filter dupes by id (session
      // pagination over ts can produce overlap on equal-ts rows) and
      // drop replay-tagged text/thought (see _ingestSnapshot for the
      // duplicate-card rationale).
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
      // Anchor the viewport to the same logical row so prepending
      // doesn't visually yank the user upward — once the new frame
      // lays out, shift by the height delta.
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!_scroll.hasClients) return;
        final delta = _scroll.position.maxScrollExtent - priorMaxExtent;
        if (delta > 0) _scroll.jumpTo(priorPixels + delta);
      });
    } catch (_) {
      // Silent: the user can swipe again, and the next SSE frame will
      // refresh tail anyway. A persistent failure shows up in the
      // existing _error banner via _scheduleReconnect.
    } finally {
      if (mounted) setState(() => _loadingOlder = false);
    }
  }

  void _jumpToLatest() {
    // Plan P2: if a random-access reset left the window short of the live tail,
    // the tail isn't loaded — scrolling to the loaded bottom wouldn't reach it.
    // Re-bootstrap the tail window first, then follow it.
    if (widget.randomAccess && !_windowHasTail) {
      _rebootstrapTail();
      return;
    }
    _scrollToTail();
    setState(() {
      _followTail = true;
      _newWhileAway = 0;
    });
  }

  /// Re-fetch the live-tail window after a random-access reset, restoring the
  /// normal tail-anchored, SSE-followed state. The inverse of
  /// [_resetWindowAround]: it puts the window back on the tail so live frames
  /// append again ([_windowHasTail] = true).
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
        _newWhileAway = 0;
      });
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
    } catch (_) {
      // Leave the current window in place on a network blip.
    }
  }

  /// Jump to the oldest *loaded* row (top of the list). The transcript is
  /// a contiguous tail-anchored window — there's no "page 1" to fetch
  /// directly without breaking that contiguity — so "oldest" means the top
  /// of what's loaded. We deliberately do NOT kick the pager here: letting
  /// the load-older anchor-jump fire mid-animation was what made ⤒ flicker
  /// and bounce. The natural top-of-scroll trigger pages more once the
  /// animation settles at 0.
  void _jumpToOldestLoaded() {
    if (!_scroll.hasClients) return;
    setState(() => _followTail = false);
    _animateProgrammatic(() => _scroll.animateTo(
          0,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        ));
  }

  /// Continuous scrub from the right-edge minimap drag. jumpTo (not
  /// animateTo) so the viewport tracks the finger directly.
  void _scrubTo(double frac) {
    if (!_scroll.hasClients) return;
    if (_followTail) setState(() => _followTail = false);
    final pos = _scroll.position;
    _jumpProgrammatic(
        () => _scroll.jumpTo(frac.clamp(0.0, 1.0) * pos.maxScrollExtent));
  }

  // Replace _events with [snapshot] (server-DESC, displayed ASC) and
  // refresh the bookkeeping (_ids, _maxSeq, _minSeq, _oldestTs).
  // Used by both the cache-paint and network-refresh halves of
  // _bootstrap; pulled out so they stay in lockstep on the rules
  // for seq tracking.
  void _ingestSnapshot(List<Map<String, dynamic>> snapshot) {
    final ascending = snapshot.reversed.toList();
    // Filter out replay-tagged text/thought events at snapshot time.
    // The hub persists every agent_event, including the replay frames
    // gemini-cli streams during session/load — and the cumulative text
    // for those replays drifts from the live cumulative text (different
    // whitespace, different chunk count), so the content-based replay
    // dedup misses and the user sees double thought cards. The live
    // (non-replay) text/thought entries are the authoritative copy.
    // Stable-ID kinds (tool_call / approval_request) keep their replay
    // entries; their dedup-by-id is robust.
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

  Future<void> _bootstrap() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _error = 'Not connected to a hub';
        _loading = false;
      });
      return;
    }
    // Cache-first (ADR-006): render whatever the snapshot cache holds
    // before the network call returns, so the user sees the transcript
    // they had last time inside one frame. SSE with `since=<maxSeq>`
    // catches the delta. The background refresh below keeps the cache
    // warm for next cold-open without blocking the UI.
    var paintedFromCache = false;
    try {
      final cacheOnly = await client.listAgentEventsCacheOnly(
        widget.agentId,
        tail: true,
        limit: _pageSize,
        sessionId: widget.sessionId,
      );
      if (!mounted) return;
      if (cacheOnly != null && cacheOnly.body.isNotEmpty) {
        _ingestSnapshot(cacheOnly.body);
        _atHead = cacheOnly.body.length < _pageSize;
        setState(() {
          _loading = false;
          // Mark stale right now — refresh below will clear it the
          // moment fresh data lands.
          _staleSince = cacheOnly.staleSince ?? DateTime.now();
        });
        _subscribe(client);
        paintedFromCache = true;
        // Pin to the tail on first paint; the background refresh is
        // a cache top-up, not a viewport reset, so we only do this
        // once on the cache paint to avoid yanking the user later.
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (widget.initialSeq != null && _trySeekInitialSeq()) return;
          _scrollToTail();
        });
        _maybeBackfillSessionInit(client);
      }
    } catch (_) {
      // Cache read failed — fall through to network-first below.
    }
    try {
      // Read-through cache + tail mode: cold open returns the newest
      // [_pageSize] events in seq DESC, so a 5k-event session shows
      // the latest turns instead of the oldest 1k. Reverse to ASC for
      // display so "older above, newer below" still holds. SSE then
      // takes over once the hub pushes a frame.
      final cached = await client.listAgentEventsCached(
        widget.agentId,
        tail: true,
        limit: _pageSize,
        sessionId: widget.sessionId,
      );
      if (!mounted) return;
      if (paintedFromCache) {
        // Cache+SSE already painted live state and the read-through
        // call just refreshed the on-disk cache for next cold-open.
        // Don't re-ingest in-memory: SSE has been delivering events
        // since the cache paint, and replacing _events would lose
        // any delta that arrived in this window. Just clear the
        // stale pill so the offline hint goes away.
        setState(() => _staleSince = cached.staleSince);
        return;
      }
      _ingestSnapshot(cached.body);
      // If the first page already smaller than our request, nothing
      // older exists to load — no point spinning the pager later.
      _atHead = cached.body.length < _pageSize;
      setState(() {
        _loading = false;
        _staleSince = cached.staleSince;
      });
      _subscribe(client);
      // Give the first-frame layout a tick, then either jump to the
      // requested seq (if the caller passed one and it's in the loaded
      // page) or pin to the tail.
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (widget.initialSeq != null && _trySeekInitialSeq()) return;
        _scrollToTail();
      });
      // session.init is a one-shot event from the agent process. A
      // freshly-opened session against an existing steward has none —
      // the init event lives in the prior closed session. Pull the
      // agent's most recent session.init regardless of session filter
      // so the AppBar chip stays informative across "new session" and
      // resume flows. Cheap: one extra HTTP call on cold open, only
      // fires when the in-scope feed lacks an init.
      _maybeBackfillSessionInit(client);
    } on HubApiError catch (e) {
      if (!mounted) return;
      // If the cache already painted, the user has a usable view and
      // we don't want a blocking error card to clobber it. Keep the
      // stale pill (set during cache paint) so they know it's not
      // live; the SSE reconnect loop will surface a separate banner
      // if the live tail also fails.
      if (paintedFromCache) return;
      setState(() {
        _error = 'Feed error (${e.status})';
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      if (paintedFromCache) return;
      setState(() {
        _error = 'Feed error: $e';
        _loading = false;
      });
    }
  }

  void _subscribe(HubClient client) {
    _reconnectTimer?.cancel();
    _sub?.cancel();
    // SSE is bus-keyed on widget.agentId — only the current agent's
    // events flow in. The since cursor is therefore per-agent. For
    // session-scoped feeds the cold-open page may carry rows from a
    // prior agent (resume case), so _maxSeq across all loaded events
    // is too high and would silently skip the new agent's first turns.
    // Compute the cursor from current-agent rows only.
    int sinceCursor = _maxSeq;
    if ((widget.sessionId ?? '').isNotEmpty) {
      sinceCursor = 0;
      for (final e in _events) {
        if ((e['agent_id'] ?? '').toString() != widget.agentId) continue;
        final s = (e['seq'] as num?)?.toInt() ?? 0;
        if (s > sinceCursor) sinceCursor = s;
      }
    }
    _sub = client
        .streamAgentEvents(
          widget.agentId,
          sinceSeq: sinceCursor,
          sessionId: widget.sessionId,
        )
        .listen((evt) {
      if (!mounted) return;
      // De-dup by event id — globally unique, works across agents (the
      // session-scoped feed mixes events from prior + current agent).
      final id = (evt['id'] ?? '').toString();
      if (id.isNotEmpty && !_ids.add(id)) return;
      // ADR-021 W1.3: drop session/load replay frames whose content is
      // already in the cached transcript. The id-dedup above doesn't
      // catch these — replay events get fresh hub-side ids when the
      // resumed agent re-emits them — so we content-key on payload
      // shape. Non-replay events bypass this filter regardless of
      // content; live duplicates are fine to render and are already
      // rare given the id-dedup.
      final replayKey = agentEventReplayKey(evt);
      if (agentEventIsReplay(evt)) {
        // text/thought replay frames carry slightly different
        // formatting from the live cumulative chunks (gemini-cli's
        // session/load reflows whitespace; the trailing chunk that
        // contained tool-call JSON is absent), so the content-based
        // replayKey doesn't match what's in _replayKeys and the dedup
        // misses. The session-scoped snapshot already pulled the live
        // versions, so any text/thought arriving with replay:true is
        // redundant by construction — drop unconditionally.
        final kind = (evt['kind'] ?? '').toString();
        if (kind == 'text' || kind == 'thought') return;
        // tool_call / approval_request have stable IDs across replay
        // and live, so the exact-key dedup still works.
        if (replayKey != null && _replayKeys.contains(replayKey)) return;
      }
      if (replayKey != null) _replayKeys.add(replayKey);
      // Plan P2: a random-access window that hasn't paged back to the live
      // tail must NOT append live frames — they belong at the tail (not
      // loaded), so grafting them here would break contiguity and the
      // position math. Drop the frame; the forward pager / jump-to-latest
      // re-joins the tail when the user heads back down. Still note the
      // stream is alive. Inert for the Feed (randomAccess false → always tail).
      if (widget.randomAccess && !_windowHasTail) {
        _reconnectAttempt = 0;
        _bannerGraceTimer?.cancel();
        _bannerGraceTimer = null;
        if (_staleSince != null || _error != null) {
          setState(() {
            _staleSince = null;
            _error = null;
          });
        }
        return;
      }
      final seq = (evt['seq'] as num?)?.toInt() ?? 0;
      // First successful delivery after a drop clears the banner, the
      // backoff counter, and any "Offline · last updated" pill — the
      // feed is live again the moment SSE pushes a frame.
      final clearedError = _error != null;
      setState(() {
        _events.add(evt);
        if (seq > _maxSeq) _maxSeq = seq;
        if (clearedError) _error = null;
        _staleSince = null;
        if (!_followTail) _newWhileAway += 1;
      });
      _reconnectAttempt = 0;
      // Cancel the pending banner: we recovered before the grace period
      // expired, so the user never needed to see "stream dropped".
      _bannerGraceTimer?.cancel();
      _bannerGraceTimer = null;
      if (_followTail) {
        WidgetsBinding.instance.addPostFrameCallback((_) => _scrollToTail());
      }
    }, onError: (e) {
      _scheduleReconnect(client, reason: '$e', isError: true);
    }, onDone: () {
      _scheduleReconnect(client, reason: 'stream closed', isError: false);
    });
  }

  void _scheduleReconnect(
    HubClient client, {
    required String reason,
    required bool isError,
  }) {
    if (!mounted) return;
    // Cap at 16s — fast enough that a recovered hub is picked up quickly,
    // slow enough that a genuinely-down hub doesn't get hammered.
    final delaySecs = math.min(16, 1 << _reconnectAttempt);
    _reconnectAttempt += 1;
    // Empty SSE closes (onDone with no error) are idle artifacts —
    // proxy idle timeout, mobile-carrier keepalive, app suspend —
    // and don't represent a drop the user can act on. The agent
    // simply has nothing to emit; reconnecting in the background is
    // sufficient. Earlier this code threshold-counted empty cycles
    // and surfaced the banner after 3, which lit up "Stream dropped"
    // on a finished-and-idle session that was working perfectly.
    //
    // Some onError signatures are also idle artifacts in disguise:
    // dart:io's HttpClient surfaces `HttpException: Connection
    // closed before full body received` (and similar variants) when
    // the underlying TCP connection is reaped by Android dozing,
    // carrier NAT timeouts, or load-balancer idle ceilings — none
    // of which the user can act on. Treat the well-known idle
    // signatures as banner-suppressed too; reconnect runs in the
    // background regardless. Genuine connectivity loss surfaces as
    // SocketException("Network is unreachable") / hostname-resolution
    // failures which still trip the banner.
    final shouldShowBanner = isError && !isIdleDropSignature(reason);
    if (shouldShowBanner &&
        (_bannerGraceTimer == null || !_bannerGraceTimer!.isActive)) {
      // Schedule the banner grace-period instead of showing immediately.
      // A successful resubscribe within [_bannerGrace] cancels this timer
      // and the user never sees the drop. Repeated drops within the same
      // window leave the original timer in place so the user sees one
      // banner, not flicker.
      _bannerGraceTimer = Timer(_bannerGrace, () {
        if (!mounted) return;
        setState(() => _error =
            'Stream dropped ($reason) · retrying in ${delaySecs}s');
      });
    }
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(Duration(seconds: delaySecs), () {
      if (!mounted) return;
      // Successful resubscribe attempts clear any stale banner —
      // even if no events flow, the connection is alive and "Stream
      // dropped" is no longer accurate.
      if (_error != null) {
        setState(() => _error = null);
      }
      _bannerGraceTimer?.cancel();
      _bannerGraceTimer = null;
      _subscribe(client);
    });
  }

  void _scrollToTail() {
    if (!_scroll.hasClients) return;
    _scroll.jumpTo(_scroll.position.maxScrollExtent);
  }

  /// Cold-open deep-link: if [widget.initialSeq] is loaded, anchor the
  /// feed on it. Returns true when the seq is present (caller skips the
  /// default tail-scroll); false when it isn't in the loaded page (caller
  /// falls back to _scrollToTail).
  bool _trySeekInitialSeq() {
    final target = widget.initialSeq;
    if (target == null) return false;
    final hit = _events.any((e) => (e['seq'] as num?)?.toInt() == target);
    if (!hit) return false;
    _seekToSeq(target);
    return true;
  }

  /// Anchor the feed on [seq]: scroll the matching card into view and
  /// highlight it for ~1.2s. Used by both the cold-open initialSeq
  /// deep-link and the lens match-stepper. Jumps on seq (never list
  /// index) because the feed pages older events lazily — the seq is the
  /// stable identity across prepends. The target must already be loaded
  /// (the stepper only offers seqs from the loaded+lensed list); the
  /// post-frame read of [_seekKey] no-ops gracefully if it isn't.
  /// Uses Scrollable.ensureVisible, which handles non-uniform row
  /// heights without a positioned-list dependency.
  void _seekToSeq(int seq) {
    setState(() {
      _activeSeekSeq = seq;
      // Anchored on a specific row — stop tail-follow until the user
      // scrolls back near the bottom themselves.
      _followTail = false;
      _seekHighlight = true;
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final ctx = _seekKey.currentContext;
      if (ctx == null) return;
      _animateProgrammatic(() => Scrollable.ensureVisible(
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

  /// Jump from the right-edge minimap. Unlike [_seekToSeq] (which relies
  /// on `ensureVisible` over a built row and silently no-ops when the
  /// target is a lazy ListView child that isn't currently realized — the
  /// exact far-away ticks the minimap exists to reach), this scrolls the
  /// controller *proportionally* to the tick's vertical fraction so every
  /// tap visibly moves the viewport, then best-effort fine-tunes onto the
  /// row once it's built. [seq] still drives the landing highlight.
  void _seekToFrac(double frac, int seq) {
    setState(() {
      _activeSeekSeq = seq;
      _followTail = false;
      _seekHighlight = true;
    });
    if (_scroll.hasClients) {
      final pos = _scroll.position;
      final target = frac.clamp(0.0, 1.0) * pos.maxScrollExtent;
      _animateProgrammatic(() => _scroll.animateTo(
            target,
            duration: const Duration(milliseconds: 300),
            curve: Curves.easeOut,
          ));
      // Once the proportional scroll has realized the target row, nudge
      // it into a comfortable position. No-ops harmlessly if still
      // off-screen (the proportional landing already put it close).
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        final ctx = _seekKey.currentContext;
        if (ctx == null) return;
        _animateProgrammatic(() => Scrollable.ensureVisible(
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

  /// Seek precisely onto the loaded row at [idx] (in the current lensed
  /// list) identified by [seq]. Unlike [_seekToFrac] — which maps the
  /// item-index fraction straight onto a pixel offset and so misses badly
  /// when rows have very different heights (a one-line text card vs. a tall
  /// tool dump) — this binary-searches the scroll offset using the actual
  /// realized-row window ([_minBuiltIdx]/[_maxBuiltIdx]) as feedback, so it
  /// lands exactly on the target regardless of height variance. Used by the
  /// "view in full transcript" jump and the turn-nav stepper, which both
  /// target a *known* row and need to be exact.
  void _seekToLoadedIndex(int idx, int seq) {
    if (!_scroll.hasClients) {
      // No viewport yet — fall back to the ensureVisible-only seek.
      _seekToSeq(seq);
      return;
    }
    setState(() {
      _activeSeekSeq = seq;
      _followTail = false;
      _seekHighlight = true;
    });
    // One guard increment held across the WHOLE convergence (many frames of
    // jumpTo + a final ensureVisible), released once in [_releaseProgrammatic]
    // — so no mid-seek frame flips tail-follow (the "jump to end" bug).
    _programmaticScrollDepth++;
    // Start after this setState's rebuild has attached _seekKey to the new
    // target, so the realized-window read reflects the right frame.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scroll.hasClients) {
        _releaseProgrammatic();
        return;
      }
      _convergeToIndex(idx, 0.0, _scroll.position.maxScrollExtent, 0);
    });
    _seekHighlightTimer?.cancel();
    _seekHighlightTimer = Timer(const Duration(milliseconds: 1600), () {
      if (!mounted) return;
      setState(() => _seekHighlight = false);
    });
  }

  // Land row [idx] in the viewport by index (option 1+2,
  // docs/discussions/insight-navigation-fixed-pages.md §10). Offset rises
  // monotonically with index in this (non-reversed) list, so the realized
  // window brackets the target: idx below it → scroll up (hi=mid), above it →
  // scroll down (lo=mid). Two refinements over a plain [lo,hi] bisection make
  // this land the card RELIABLY rather than approximately:
  //   1. The first probe is *proportional* (idx / count → offset), not the
  //      [0,max] midpoint, so the target usually realizes on the first frame —
  //      a far jump no longer needs the full log₂(extent) bisection budget.
  //   2. On the iteration cap it NEVER bails to nothing: it does a final
  //      proportional jump + best-effort ensureVisible, so the card is brought
  //      into view even when the realized-window feedback can't fully converge
  //      (the "card not in viewport" bug was this branch releasing with a null
  //      seek context and no scroll).
  void _convergeToIndex(int idx, double lo, double hi, int iter) {
    if (!mounted || !_scroll.hasClients) {
      _releaseProgrammatic();
      return;
    }
    final max = _scroll.position.maxScrollExtent;
    final ctx = _seekKey.currentContext;
    // ctx != null inline in the `if` so flow analysis promotes it (no `!`).
    if (idx >= _minBuiltIdx && idx <= _maxBuiltIdx && ctx != null) {
      Scrollable.ensureVisible(
        ctx,
        alignment: 0.3,
        duration: const Duration(milliseconds: 220),
        curve: Curves.easeOut,
      ).whenComplete(_releaseProgrammatic);
      return;
    }
    if (iter >= 14) {
      // Converged as far as the realized-window feedback allows. Guarantee the
      // card lands rather than leaving the viewport off-target.
      if (ctx != null) {
        Scrollable.ensureVisible(
          ctx,
          alignment: 0.3,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        ).whenComplete(_releaseProgrammatic);
        return;
      }
      if (_lensedCount > 1) {
        _scroll.jumpTo(((idx / (_lensedCount - 1)) * max).clamp(0.0, max));
      }
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) {
          _releaseProgrammatic();
          return;
        }
        final c2 = _seekKey.currentContext;
        if (c2 != null) {
          Scrollable.ensureVisible(
            c2,
            alignment: 0.3,
            duration: const Duration(milliseconds: 200),
            curve: Curves.easeOut,
          ).whenComplete(_releaseProgrammatic);
        } else {
          _releaseProgrammatic();
        }
      });
      return;
    }
    // First probe proportional (lands within ~a screen so the target usually
    // realizes immediately); thereafter bisect using the realized window.
    final double mid;
    if (iter == 0 && _lensedCount > 1) {
      mid = ((idx / (_lensedCount - 1)) * max).clamp(0.0, max);
    } else {
      mid = ((lo + hi) / 2).clamp(0.0, max);
    }
    // Reset the realized-window sentinels so the post-jump layout reports
    // ONLY the new viewport. Critical: jumpTo re-runs the itemBuilder (which
    // grows the window) but NOT build() (where the reset otherwise lives), so
    // without this the window accumulates the UNION of every viewport visited
    // during the search — the bound test then finds idx already "inside" the
    // union, never narrows, and the seek stalls or lands on the wrong row.
    // The failure is intermittent and worse after lazy-loading, when the
    // longer, height-varied list makes the union span the target more often.
    _minBuiltIdx = 1 << 30;
    _maxBuiltIdx = -1;
    _scroll.jumpTo(mid);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scroll.hasClients) {
        _releaseProgrammatic();
        return;
      }
      var nlo = lo;
      var nhi = hi;
      if (idx < _minBuiltIdx) {
        nhi = mid; // target is above the realized window — scroll up
      } else if (idx > _maxBuiltIdx) {
        nlo = mid; // target is below — scroll down
      }
      _convergeToIndex(idx, nlo, nhi, iter + 1);
    });
  }

  void _releaseProgrammatic() {
    if (_programmaticScrollDepth > 0) _programmaticScrollDepth--;
  }

  /// The whole-run anchor list backing a lens's funnel in Insight mode — the
  /// digest's turn starts / error samples (sorted to run order). Null for the
  /// lenses with no whole-run index (text / tools / all). Mirrors the
  /// `funnelUsesRunList` selection in build.
  List<int>? _runAnchorListFor(FeedLens lens) {
    if (!_runAnchorMode) return null;
    final raw = lens == FeedLens.turns
        ? widget.runTurnSeqs
        : (lens == FeedLens.errors ? widget.runErrorSeqs : null);
    if (raw == null || raw.isEmpty) return null;
    return [...raw]..sort();
  }

  /// Switch the active lens. Resets the seek anchor; for a non-`all`
  /// lens, pins to the tail so the user lands on the most recent match
  /// (the newest error is usually what you're debugging).
  void _setLens(FeedLens lens) {
    setState(() {
      _lens = lens;
      _activeSeekSeq = null;
      // Reset the run-anchor funnel position so a fresh lens starts at its
      // newest match rather than wherever a prior lens left the stepper.
      _funnelRunIdx = null;
      if (lens != FeedLens.all) _followTail = true;
    });
    // ADR-039 P2 — the Errors lens is the whole-run error list (digest-only
    // summary rows), not a client filter over the loaded window. On entry just
    // show it scrolled to the newest error (bottom); no _events reset.
    if (_isErrorsSummaryLens(lens)) {
      final n = _sortedErrorSeqs().length;
      setState(() => _funnelRunIdx = n - 1); // newest selected
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
      return;
    }
    // ADR-039 P1b — the OTHER run-anchor lens (turns) jumps to its NEWEST match
    // on entry. Without this the filtered view is empty whenever no match sits
    // in the loaded tail window — and an empty list has no scroll extent, so
    // "scroll up to load older" can never fire. The jump random-access-resets
    // the window around the newest anchor (reachable at any depth), so the list
    // shows that match and the funnel/stepper walk older from there.
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

  // A seq the user asked to view "in context": tapped from a filtered card,
  // it switches to the All lens and (in build, once the unfiltered list is
  // back) seeks to that row so the surrounding turns are visible.
  int? _pendingContextSeq;
  // When true, the pending context jump keeps the active lens instead of
  // resetting to All — used by the run-anchor funnel stepper, which steps
  // within the turns/errors lens (see [_funnelRunJump]).
  bool _pendingContextKeepLens = false;
  // The funnel stepper's position within the full-run anchor list (turns /
  // errors) when driving from the digest in Insight mode. Decoupled from
  // [_activeSeekSeq] because the landing row (nearest visible >= anchor) may
  // differ from the anchor seq (e.g. a hidden turn.start), which would break
  // an indexOf-based position. Null = default to the newest match.
  int? _funnelRunIdx;

  /// Clear the active filter and land on [seq] in the full transcript, so a
  /// match found in a filtered view can be read with its surrounding
  /// context. The seek itself runs in build once `_lens == all` has put the
  /// row back in the list (we know its index there, so it works even if the
  /// row isn't currently realised). With [keepLens] the active lens is
  /// preserved (the run-anchor funnel stepper stays in turns/errors).
  void _jumpToContext(int seq, {bool keepLens = false}) {
    setState(() {
      if (!keepLens) _lens = FeedLens.all;
      _followTail = false;
      _pendingContextSeq = seq;
      _pendingContextKeepLens = keepLens;
    });
  }

  /// Land on [seq] but keep the active lens (run-anchor funnel stepping).
  void _landOnSeqKeepLens(int seq) => _jumpToContext(seq, keepLens: true);

  /// Funnel stepper jump in Insight mode, driven by the full-run anchor list
  /// (turns / errors). Records the run-list position, then lands on the
  /// anchor: a clean window reset around its `(ts, seq)` if it isn't loaded
  /// (reachable at any depth), else a convergent seek — both keeping the lens.
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
    if (widget.randomAccess &&
        (widget.sessionId ?? '').isNotEmpty &&
        ts != null &&
        ts.isNotEmpty) {
      // Fire-and-forget: resets the window then lands (keeping the lens).
      _resetWindowAround(seq, ts, keepLens: true);
      return;
    }
    // No ts (older digest) — best-effort within the loaded window.
    _landOnSeqKeepLens(seq);
  }

  /// Step the funnel cursor to match [i] of [matchSeqs]. The SINGLE entry point
  /// shared by the top-left funnel pill (`FeedFilterControl`) AND the
  /// bottom-left full-screen stepper (`TurnStepperPill`) so the two can never
  /// drift onto different cursors — the "tap the stepper, the funnel N/M
  /// doesn't move / they step different units" bug. A whole-run lens
  /// (turns/errors) routes through [_funnelRunJump] (the cursor is
  /// [_funnelRunIdx], reachable at any depth); text/tools route through the
  /// loaded-window convergent seek (the cursor is [_activeSeekSeq]). Either
  /// way the position the funnel reads back ([matchIndex]) is the one this
  /// moved.
  void _funnelStep(int i, List<int> matchSeqs, {required bool usesRunList}) {
    if (i < 0 || i >= matchSeqs.length) return;
    // ADR-039 P2 — in the Errors summary list the stepper just scrolls the
    // fixed-extent list to row i (matchSeqs == the sorted run error seqs ==
    // the rendered rows), not a window reset. The funnel N/M reads _funnelRunIdx.
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

  /// ADR-039 P2 — the Errors lens as the run's COMPLETE error list: digest-only
  /// summary rows (class + relative time), exact + never-empty + no event-body
  /// fetch. A tap jumps to that error in full context (switches to All and
  /// lands via the `(ts, seq)` window reset). Fixed [_kErrorRowExtent] so the
  /// funnel stepper scrolls to a row by index ([_funnelStep]).
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
          ts: ts,
          active: active == i,
          onTap: () {
            setState(() => _funnelRunIdx = i);
            // Switch to All + land on the error in context (reuses the
            // random-access reset; the row's ts makes it an O(log n) jump).
            _handleExternalSeek(seq, ts);
          },
        );
      },
    );
  }

  /// Plan P2 — "jump to any event" scrubber. The position pill (event N / M) is
  /// tappable on the random-access surface: it opens a slider over the whole
  /// run [1, M] and, on confirm, random-access-windows onto the chosen ordinal.
  /// This is the answer to "how do I jump to an arbitrary event" — the minimap
  /// only reaches anchors (turns/errors), this reaches any position.
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
          final fg = isDark
              ? DesignColors.textPrimary
              : DesignColors.textPrimaryLight;
          final muted = isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight;
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
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: fg),
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

  /// Random-access jump to an arbitrary run ordinal [n] (1-based). Per-agent
  /// seq is the dense run ordinal, so ordinal n is the event at seq == n; we
  /// fetch that one event (agent-scoped seq lookup) to recover its ts, then
  /// drive the `(ts, seq)` window reset. Honest for single-agent runs — the
  /// same assumption the N / M pill makes (a resumed multi-agent session has
  /// non-dense per-agent seqs, so the landing is approximate there).
  Future<void> _jumpToOrdinal(int n) async {
    if (_seqIsLoaded(n)) {
      _landOnSeq(n);
      return;
    }
    final sid = widget.sessionId;
    final client = ref.read(hubProvider.notifier).client;
    if (sid == null || sid.isEmpty || client == null) return;
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

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    // Source slash/mention pickers (W-UI-4) from session.init when
    // available. Mentions blend agents + tools + skills since claude
    // accepts @-references for any of them; an empty session still
    // gets a working composer (the strips hide themselves).
    final initForCompose = latestSessionInitPayload(_events);
    // Lift session.init to the parent (AppBar) once per change so a
    // session header doesn't take up a transcript row on mobile. We
    // key on session_id + model so the chip refreshes when an engine
    // posts session_id first (the adapter's startup emit) and the
    // model later (mapped from a transcript-side signal): antigravity
    // works this way — the adapter emits session.init with just
    // session_id once it resolves the conversation, and the mapper
    // later emits a second session.init with `model` extracted from
    // agy's <USER_SETTINGS_CHANGE> block on step 0. Pre-fix the gate
    // compared session_id alone, so the second emit didn't refire and
    // the chip's model pill stayed blank.
    if (initForCompose != null) {
      final sid = (initForCompose['session_id'] ?? '').toString();
      final model = (initForCompose['model'] ?? '').toString();
      final key = '$sid|$model';
      if (key != _lastReportedInitSid) {
        _lastReportedInitSid = key;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) widget.onSessionInit?.call(initForCompose);
        });
      }
    }
    final composeSlash = stringList(initForCompose?['slash_commands']);
    final composeMentions = <String>[
      ...stringList(initForCompose?['agents']),
      ...stringList(initForCompose?['tools']),
      ...stringList(initForCompose?['skills']),
    ];
    // Agent-busy signal for the composer's cancel-on-send overlay:
    // the latest event's kind tells us whether the current turn has
    // wrapped. turn.result / completion / lifecycle:exited are the
    // terminal markers; anything else means "the agent is still
    // producing output for the in-flight turn". Composer only renders
    // the cancel button when the user has already typed something —
    // so this flag matters only in the predictive-input scenario.
    //
    // In random-access (Insight) mode a jump replaces the window with a
    // *mid-run* slice whose last loaded event is mid-turn — so agentIsBusy
    // would read "busy" off a slice that isn't the live tail. Only trust the
    // signal when the window actually reaches the tail; otherwise the run's
    // real state isn't loaded here, so report not-busy.
    final isAgentBusy = (widget.randomAccess && !_windowHasTail)
        ? false
        : agentIsBusy(_events);
    final compose = AgentCompose(
      agentId: widget.agentId,
      slashCommands: composeSlash,
      mentions: composeMentions,
      isAgentBusy: isAgentBusy,
    );
    if (_events.isEmpty) {
      return Column(
        children: [
          Expanded(
            child: Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  _error ??
                      'No events yet — the feed lights up once the agent '
                      'produces text, tool calls, or completions.',
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
          ),
          compose,
        ],
      );
    }
    // Precompute tool_use_id → tool name so tool_result cards can show
    // "tool: git_log" in their header instead of a bare id. Cheap — only
    // scans tool_call events, and the Feed is O(dozens) of events.
    final toolNames = <String, String>{};
    // Already-answered approval requests: any input.approval the user has
    // sent gets its request_id recorded here so the matching
    // approval_request card renders in a disabled/resolved state instead
    // of offering the buttons again.
    final resolvedApprovals = <String, String>{}; // request_id → decision
    // Latest tool_call_update per toolCallId, folded into the parent
    // tool_call card below. Individual tool_call_update events are hidden
    // from the feed — rendering every progress tick floods the list.
    final toolUpdates = <String, Map<String, dynamic>>{};
    // tool_use_id → tool_result event (full row, so we can also surface ts).
    // The tool_call card pulls its matching result from here; bare
    // tool_result cards drop out of the feed because the lineage is now
    // expressed inside one card per call.
    final toolResults = <String, Map<String, dynamic>>{};
    for (final e in _events) {
      final kind = (e['kind'] ?? '').toString();
      final p = e['payload'];
      if (p is! Map) continue;
      if (kind == 'tool_call') {
        final id = p['id']?.toString() ?? '';
        final name = p['name']?.toString() ?? '';
        if (id.isNotEmpty && name.isNotEmpty) toolNames[id] = name;
      } else if (kind == 'tool_call_update') {
        final id = (p['toolCallId'] ?? p['tool_call_id'] ?? '').toString();
        if (id.isNotEmpty) toolUpdates[id] = p.cast<String, dynamic>();
      } else if (kind == 'tool_result') {
        final id = p['tool_use_id']?.toString() ?? '';
        if (id.isNotEmpty) toolResults[id] = e.cast<String, dynamic>();
      } else if (kind == 'input.approval') {
        final rid = p['request_id']?.toString() ?? '';
        final dec = p['decision']?.toString() ?? '';
        if (rid.isNotEmpty) resolvedApprovals[rid] = dec;
      }
    }
    // session.init is now reported up to the parent (AppBar) via the
    // onSessionInit callback above; nothing inline needs the payload.
    // Telemetry strip inputs: cumulative cost from all turn.result
    // events, per-model token totals aggregated from turn.result.by_model
    // (claude's modelUsage, normalized by driver_stdio.go — keys: input,
    // output, cache_read, cache_create, cost_usd per model name), and
    // latest rate_limit. We sum across all completed turns so the strip
    // shows session-wide usage, not just the most recent turn.
    //
    // by_model is the right source because claude can spawn sub-agents
    // (e.g. Haiku for small tasks under an Opus parent), each with its
    // own token totals. The bare `usage` event only carries the parent's
    // last-message numbers and undercounts when sub-agents are active.
    double totalCostUsd = 0.0;
    final modelTotals = <String, ModelTokens>{};
    Map<String, dynamic>? latestRateLimit;
    int turnCount = 0;
    // Codex publishes cumulative session totals on each
    // thread/tokenUsage/updated notification (kind=usage in the
    // typed vocabulary), tagged with `cumulative: true|"true"` and
    // `engine: <name>` by the frame profile. Claude's per-message
    // usage events lack the marker; they're ignored here and the
    // authoritative claude source is turn.result.by_model. The
    // latest cumulative event replaces — it's not a delta — so we
    // track it separately and fold it in once below.
    ModelTokens? cumulativeUsage;
    String cumulativeBucketKey = 'agent';
    // Latest known context-window stats (codex's
    // thread/tokenUsage/updated carries both modelContextWindow and the
    // cumulative total). The window can change mid-session if codex
    // hot-swaps models, so we always track the most recent values.
    int? latestContextWindow;
    int? latestContextUsed;
    // Per-message usage snapshot (claude-code path, v1.0.662). The
    // driver emits a `kind=usage` event per assistant message with
    // input + cache_read + cache_create token counts for THAT message
    // alone (not cumulative). The most recent event wins — its sum
    // equals the API call's prompt size, which equals what claude's
    // own `/context` slash command reports as "current context".
    // Replaces a pre-v1.0.662 fallback that summed per-turn
    // `by_model.input + cache_read + cache_create` across every API
    // call within a turn — for a turn with many tool-use iterations
    // that double-counted by N×, producing absurd >1M numbers on
    // long sessions.
    int? perMessageInput;
    int? perMessageCacheRead;
    int? perMessageCacheCreate;
    // v1.0.668: also capture output + model so we can synthesise a
    // ModelTokens entry for the token-flow pill. M4 doesn't emit
    // turn.result.by_model (the driver-of-record source for
    // modelTotals), so the pill stayed blank even though every
    // assistant message carried full usage. SET semantics here
    // (overwrite, not sum) — same anti-double-count rule as the
    // context-chip path.
    int? perMessageOutput;
    String? perMessageModel;
    for (final e in _events) {
      final kind = (e['kind'] ?? '').toString();
      final p = e['payload'];
      if (p is! Map) continue;
      if (kind == 'turn.result') {
        turnCount += 1;
        final c = p['cost_usd'];
        if (c is num) totalCostUsd += c.toDouble();
        final byModel = p['by_model'];
        if (byModel is Map) {
          for (final entry in byModel.entries) {
            final v = entry.value;
            if (v is! Map) continue;
            final tot = modelTotals.putIfAbsent(
                entry.key.toString(), ModelTokens.empty);
            tot.add(v.cast<String, dynamic>());
          }
        }
      } else if (kind == 'rate_limit') {
        latestRateLimit = p.cast<String, dynamic>();
      } else if (kind == 'usage' && isCumulativeUsage(p)) {
        // Cumulative session totals (codex shape). The latest
        // notification supersedes; we don't sum. Claude's per-
        // message usage events lack the `cumulative` marker and
        // are handled in the next branch.
        final t = ModelTokens.empty();
        t.input = (p['input_tokens'] as num?)?.toInt() ?? 0;
        t.output = (p['output_tokens'] as num?)?.toInt() ?? 0;
        t.cacheRead = (p['cached_input_tokens'] as num?)?.toInt() ?? 0;
        cumulativeUsage = t;
        // Use the engine tag the profile sets on cumulative events
        // so the bucket key in the telemetry tooltip reads as a real
        // engine name rather than an empty string. Default falls back
        // to 'agent' if the upstream profile didn't tag.
        final engineTag = (p['engine'] as String?) ?? 'agent';
        cumulativeBucketKey = engineTag;
        // Context-window snapshot rides on the same event. For "fill"
        // we want the most recent turn's token count — that's what
        // the model sees the context filled with on the NEXT turn.
        // Codex's tokenUsage frame carries both `.total.*` (cumulative
        // across all turns, grows boundlessly) and `.last.*` (just
        // the most recent turn). The profile emits `last_total_tokens`
        // from `.last.totalTokens`; mobile prefers it when present
        // and falls back to `total_tokens` for legacy events on disk
        // (pre-v1.0.712 codex usage rows that only carry cumulative).
        //
        // The earlier comment on this branch claimed cumulative
        // matched codex's TUI statusline — that was wrong, codex's
        // statusline shows the per-turn last count. A long codex
        // session previously showed wildly inflated "context fill"
        // numbers (e.g. 169K/258K on a session whose actual fill was
        // ~19K) — the v1.0.712 smoke regression that prompted this
        // fix.
        final cw = (p['context_window'] as num?)?.toInt() ?? 0;
        final lastUsed = (p['last_total_tokens'] as num?)?.toInt();
        final cumulativeUsed = (p['total_tokens'] as num?)?.toInt() ?? 0;
        final used = lastUsed ?? cumulativeUsed;
        if (cw > 0) latestContextWindow = cw;
        if (used > 0) latestContextUsed = used;
      } else if (kind == 'usage') {
        // Per-message usage (claude-code path, v1.0.662). NOT
        // cumulative — each event reports the API call's prompt
        // size on its own; later events overwrite earlier ones.
        // Sum on display = input + cache_read + cache_create =
        // the claude `/context` number.
        final i = (p['input_tokens'] as num?)?.toInt();
        final cr = (p['cache_read'] as num?)?.toInt() ??
            (p['cache_read_input_tokens'] as num?)?.toInt();
        final cc = (p['cache_create'] as num?)?.toInt() ??
            (p['cache_creation_input_tokens'] as num?)?.toInt();
        if (i != null) perMessageInput = i;
        if (cr != null) perMessageCacheRead = cr;
        if (cc != null) perMessageCacheCreate = cc;
        // v1.0.668: also capture output + model so we can synthesise
        // a modelTotals entry below for the token-flow pill.
        final o = (p['output_tokens'] as num?)?.toInt();
        if (o != null) perMessageOutput = o;
        final m = (p['model'] as String?);
        if (m != null && m.isNotEmpty) perMessageModel = m;
        // v1.0.667: pick up `context_window` if present. The M4
        // mapper attaches it (derived from model name) so mobile can
        // render the context-utilisation chip. Without it the chip
        // suppresses itself (cw <= 0 → no tile). The cumulative
        // branch above already handled this; the per-message branch
        // didn't, leaving the chip blank for claude-code spawns
        // even though usage was flowing.
        final cw = (p['context_window'] as num?)?.toInt() ?? 0;
        if (cw > 0) latestContextWindow = cw;
      } else if (kind == 'status_line') {
        // v1.0.720 — antigravity's M4 path emits ONLY status_line
        // events for token + context-window data (no `usage` events
        // from the transcript — the agy transcript doesn't carry
        // token counts; statusLine is the authoritative source per
        // the antigravity statusLine research §2.4).
        //
        // The nested `context_window.current_usage` block is shape-
        // identical to claude-code's `usage` block, so we shape-shift
        // here: extract from that nested path and feed the same
        // perMessageInput / perMessageCacheRead / perMessageCacheCreate
        // state the claude-code branch above writes. The downstream
        // chip strip (latestInput / billableInput / token-flow pill)
        // then renders identically for antigravity without per-engine
        // branching at the render layer.
        //
        // claude-code stewards also receive status_line events but
        // also emit `usage` events from JSONL — the `usage` branch
        // above wins on latest-write semantics. So this branch is
        // additive and degrades cleanly for all engines that ship
        // a statusLine.
        final cw = p['context_window'];
        if (cw is Map) {
          final cur = cw['current_usage'];
          if (cur is Map) {
            // Same field names as claude-code's usage block (verified
            // on the dev host 2026-05-26; research doc §2.3 + §2.4).
            final i = (cur['input_tokens'] as num?)?.toInt();
            final cr =
                (cur['cache_read_input_tokens'] as num?)?.toInt();
            final cc =
                (cur['cache_creation_input_tokens'] as num?)?.toInt();
            final o = (cur['output_tokens'] as num?)?.toInt();
            if (i != null) perMessageInput = i;
            if (cr != null) perMessageCacheRead = cr;
            if (cc != null) perMessageCacheCreate = cc;
            if (o != null) perMessageOutput = o;
          }
          // antigravity carries the model's static context size at
          // `context_window.context_window_size`. claude-code carries
          // it as `usage.context_window`. Same semantic; different
          // path. Latest-wins set, same shape as the `usage` branch.
          final sz = (cw['context_window_size'] as num?)?.toInt() ?? 0;
          if (sz > 0) latestContextWindow = sz;
        }
        // Capture model from session.init-style top-level model field
        // (antigravity statusLine carries {model: {id, display_name}}).
        // claude-code's status_line carries the same shape, so this
        // is engine-agnostic.
        final m = p['model'];
        if (m is Map) {
          final n = (m['display_name'] as String?) ??
              (m['id'] as String?) ??
              (m['name'] as String?);
          if (n != null && n.isNotEmpty) perMessageModel = n;
        } else if (m is String && m.isNotEmpty) {
          perMessageModel = m;
        }
      }
    }
    // If no by_model rows arrived (codex's turn/completed doesn't
    // ship them), surface the cumulative usage as a single bucket.
    // The bucket key is shown in the tile's tooltip so we tag it
    // with the engine name rather than leaving it blank.
    if (modelTotals.isEmpty && cumulativeUsage != null) {
      modelTotals[cumulativeBucketKey] = cumulativeUsage;
    }
    // v1.0.668: synthesise a modelTotals entry from per-message usage
    // when no other source populated one. claude-code M4 doesn't emit
    // turn.result.by_model, so without this the token-flow pill
    // (which gates on modelTotals.isNotEmpty) stayed suppressed even
    // though every assistant message carried full usage. SET
    // semantics — `latestInput / latestCacheRead / latestCacheCreate`
    // get the per-message snapshot directly, and we DO NOT increment
    // `input / output / cacheRead / cacheCreate` (the cumulative
    // fields), because per-message events would otherwise sum across
    // a turn's many tool-use iterations and double-count by N× (the
    // pre-v1.0.662 1M-tokens bug).
    if (modelTotals.isEmpty &&
        (perMessageInput != null || perMessageOutput != null)) {
      final t = ModelTokens.empty();
      // Snapshot fields drive the chip; cumulative fields stay 0 so
      // the SUM-on-display logic in TelemetryStrip reads only the
      // per-message values via billableInput / output.
      t.latestInput = perMessageInput ?? 0;
      t.latestCacheRead = perMessageCacheRead ?? 0;
      t.latestCacheCreate = perMessageCacheCreate ?? 0;
      // Token-flow pill reads `billableInput` (input + cacheCreate)
      // and `output`. Populate them as snapshot too — they're meant
      // to reflect what the user paid for on the LATEST message, not
      // a session-wide aggregate that diverges from per-call usage.
      t.input = perMessageInput ?? 0;
      t.output = perMessageOutput ?? 0;
      t.cacheRead = perMessageCacheRead ?? 0;
      t.cacheCreate = perMessageCacheCreate ?? 0;
      final cw = latestContextWindow;
      if (cw != null && cw > 0) {
        t.contextWindow = cw;
      }
      modelTotals[perMessageModel ?? 'claude-code'] = t;
    }
    // Claude path for context window: the codex `usage` event already
    // populated latestContextWindow / latestContextUsed when present.
    // For claude (which carries the data per-model on turn.result and
    // does not emit cumulative `usage` events), pick the dominant
    // model from modelTotals — the one with the most output, since
    // sub-agents like Haiku produce trivial output relative to the
    // main agent. Use that model's contextWindow as capacity.
    if (latestContextWindow == null && modelTotals.isNotEmpty) {
      String? mainModel;
      var bestOutput = -1;
      modelTotals.forEach((name, t) {
        if (t.contextWindow > 0 && t.output > bestOutput) {
          mainModel = name;
          bestOutput = t.output;
        }
      });
      if (mainModel != null) {
        final t = modelTotals[mainModel]!;
        latestContextWindow = t.contextWindow;
      }
    }
    // For "used" prefer the per-message usage event (v1.0.662) over
    // the per-turn by_model snapshot. The per-message event reports
    // ONE API call's prompt — the right answer. The by_model
    // snapshot's `latestInput + latestCacheRead + latestCacheCreate`
    // double-counted within a multi-tool-use turn (every Bash/Read
    // iteration produced its own API call, all summed). Fall back
    // to the by_model snapshot only when the per-message stream is
    // absent (older drivers, future engines).
    if (latestContextUsed == null) {
      if (perMessageInput != null ||
          perMessageCacheRead != null ||
          perMessageCacheCreate != null) {
        final used = (perMessageInput ?? 0) +
            (perMessageCacheRead ?? 0) +
            (perMessageCacheCreate ?? 0);
        if (used > 0) latestContextUsed = used;
      } else if (modelTotals.isNotEmpty) {
        // Best-effort fallback for engines that don't emit per-message
        // usage. Pick the dominant model; reuse its latestInput +
        // latestCacheRead + latestCacheCreate. Accurate when the turn
        // had one API call; over-counted when the turn had many.
        String? mainModel;
        var bestOutput = -1;
        modelTotals.forEach((name, t) {
          if (t.output > bestOutput) {
            mainModel = name;
            bestOutput = t.output;
          }
        });
        if (mainModel != null) {
          final t = modelTotals[mainModel]!;
          final used =
              t.latestInput + t.latestCacheRead + t.latestCacheCreate;
          if (used > 0) latestContextUsed = used;
        }
      }
    }
    // ADR-036 W4-a — process-cost extracted from the latest
    // status_line frame's cost.total_cost_usd. Null when no
    // status_line has carried a cost block yet (cold-open race,
    // older claude versions, or operator removed the install).
    final processCostUsd = processCostFromEvents(_events);
    // ADR-036 W4-c — session-cost imputed by the hub. Polled out-of-
    // band on the _sessionCostTimer; null until the first response
    // lands (or when sessionId is unset).
    double? sessionCostUsdImputed;
    final scRaw = _sessionCost?['total_usd'];
    if (scRaw is num && (scRaw > 0 || (_sessionCost?['tokens_by_model'] is Map
        && (_sessionCost?['tokens_by_model'] as Map).isNotEmpty))) {
      sessionCostUsdImputed = scRaw.toDouble();
    }
    // ADR-036 W5 — rate_limits sub-block from the latest status_line
    // frame. Null until first status_line lands; either window may
    // still be absent on a given frame (tile self-gates per window).
    final rateLimitsFromStatus = rateLimitsFromEvents(_events);
    // ADR-036 W6 — exceeds_200k_tokens alarm. True iff the latest
    // status_line carries the cap-breach signal; null/false suppress
    // the tile entirely.
    final exceeds200k = exceeds200kFromEvents(_events);
    final hasTelemetry = turnCount > 0 ||
        modelTotals.isNotEmpty ||
        latestRateLimit != null ||
        latestContextWindow != null ||
        processCostUsd != null ||
        sessionCostUsdImputed != null ||
        rateLimitsFromStatus != null ||
        (exceeds200k == true);
    // Build the visible event list: drop folded-in kinds.
    //   tool_call_update — folded into parent tool_call card.
    //   tool_result      — paired with parent tool_call by tool_use_id;
    //                      orphaned results (no matching call) still render
    //                      so a one-off tool_result isn't silently swallowed.
    //   session.init     — surfaced in the sticky header above.
    //   debug-only kinds — gated by _verbose toggle (W1.B).
    // After filtering, collapse codex's streaming partials by
    // message_id — a single chatbot-style row that grows in place
    // instead of N stacked rows.
    final filtered = <Map<String, dynamic>>[
      for (final e in _events)
        if (!isHiddenInFeed(e, toolNames, verbose: _verbose)) e,
    ];
    final visible = collapseStreamingPartials(filtered);
    // P1 lens (docs/plans/agent-transcript-debug-and-header-parity.md):
    // narrow the visible feed to one family so a long run can be
    // debugged. Runs AFTER folding so the Errors lens reads a tool_call's
    // resolved status from the same toolResults/toolUpdates maps the card
    // does. Match-stepping is seq-anchored over this loaded+lensed list;
    // older matches join as the user scrolls up and the list grows.
    final lensed = _lens == FeedLens.all
        ? visible
        : [
            for (final e in visible)
              if (agentEventMatchesLens(e, _lens, toolResults, toolUpdates))
                e,
          ];
    // Reset the realized-row window for this frame's list; the itemBuilder
    // repopulates it during layout, and a convergent seek reads it back.
    // Snapshot the last frame's top-built seq first (for the Insight position
    // readout) before clearing the accumulator for this frame.
    if (_topBuiltSeq > 0) _lastTopBuiltSeq = _topBuiltSeq;
    _topBuiltSeq = 0;
    _minBuiltIdx = lensed.length;
    _maxBuiltIdx = -1;
    _lensedCount = lensed.length;
    // Consume a pending "view in context" request: now that the lens is
    // back to All (so [lensed] == [visible]), find the row's index in the
    // unfiltered list and seek to it once this frame lays out. Convergent
    // index seek (height-agnostic) so it lands exactly on the row even when
    // it isn't currently realised.
    if (_pendingContextSeq != null &&
        (_pendingContextKeepLens || _lens == FeedLens.all)) {
      final target = _pendingContextSeq!;
      _pendingContextSeq = null;
      _pendingContextKeepLens = false;
      var idx =
          lensed.indexWhere((e) => (e['seq'] as num?)?.toInt() == target);
      if (idx < 0) {
        // No exact row — the anchor may be a hidden marker (e.g. an ACP
        // `turn.start`, which isn't rendered) or filtered out. Land on the
        // nearest visible row at or after it: the turn's first shown event.
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
    // In Insight mode the turns/errors funnel is driven by the *whole-run*
    // anchor lists (the digest's turn index + error samples), not the loaded
    // window: so the count equals the insight data and is stable as more
    // events page in, and a jump random-access-seeks to the exact anchor even
    // when it isn't loaded. Text/Tools have no whole-run index, so they stay
    // loaded-window (the convergent seek lands them accurately).
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
      // Error samples arrive grouped by class — sort to run order so the
      // stepper walks the transcript monotonically. (Turn starts are already
      // ascending.) Copy before sorting; never mutate the widget's list.
      matchSeqs = [...runList]..sort();
    } else {
      matchSeqs = [for (final e in lensed) (e['seq'] as num?)?.toInt() ?? 0];
    }
    // 1-based position of the active match. Default to the newest when
    // there's no explicit anchor (fresh lens activation) so the pill
    // reads N/N and the steppers walk backward into history.
    int matchIndex = 0;
    if (matchSeqs.isNotEmpty) {
      if (funnelUsesRunList) {
        // The run-anchor stepper tracks its own position (decoupled from the
        // landed row, which may be a nearest-visible substitute).
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
    // P3 — full-screen-only chrome (dense=false): per-lens counts for the
    // lens bar + minimap marks (a faint tick per tool_call, a red tick
    // per error) over the WHOLE loaded transcript. Skipped in the dense
    // path so a constrained host pays nothing for it.
    Map<FeedLens, int> lensCounts = const {};
    final minimapMarks = <FeedMinimapMark>[];
    // Turn-nav state (full-screen only). Anchors are the inbound prompts
    // in the rendered list; the stepper walks between them. See
    // [turnAnchorIndices] / [isTurnAnchorEvent].
    List<int> turnAnchorIdx = const [];
    final lensedDenom = (lensed.length - 1) <= 0 ? 1 : lensed.length - 1;
    if (!widget.dense) {
      lensCounts = {
        for (final l in FeedLens.values)
          l: l == FeedLens.all
              ? visible.length
              // In Insight mode, turns/errors report their whole-run totals
              // (the digest) so the dropdown count matches the funnel pill and
              // the insight data — not the loaded-window slice.
              : (_runAnchorMode && l == FeedLens.turns)
                  ? (widget.runTurnSeqs?.length ?? 0)
                  : (_runAnchorMode && l == FeedLens.errors)
                      ? (widget.runErrorSeqs?.length ?? 0)
                      : visible
                          .where((e) => agentEventMatchesLens(
                              e, l, toolResults, toolUpdates))
                          .length,
      };
      turnAnchorIdx = turnAnchorIndices(lensed);
      final turnIdxSet = turnAnchorIdx.toSet();
      if (_runAnchorMode) {
        // Full-run minimap: anchors come from the digest (every error in the
        // run) + the turn index (every turn start), positioned by their run
        // ordinal (`seq / total`) rather than their index in the loaded slice.
        // So the strip is a true whole-run overview, and a tap jumps to a seq
        // that may not be loaded yet (routed through the random-access seek).
        for (final a in feedRunAnchorMarks(
          errorSeqs: widget.runErrorSeqs ?? const <int>[],
          turnSeqs: widget.runTurnSeqs ?? const <int>[],
          total: widget.totalEventCount!,
        )) {
          minimapMarks.add(FeedMinimapMark(
            frac: a.frac,
            seq: a.seq,
            isError: a.isError,
            // Match the transcript card: a turn anchor is the turn's start
            // event — an `input.text` user prompt — so colour it with the same
            // accent the prompt card paints (terminalYellow), not a flat grey.
            // Errors stay red. So the strip reads as a colour-coded shrink of
            // the feed, like the loaded-window minimap.
            color: a.isError
                ? DesignColors.error
                : agentEventAccent('input.text', 'user'),
          ));
        }
      } else {
        // Ticks track the list actually on screen (`lensed`) so the minimap
        // is populated in EVERY view (item: "no minimap for turn/text view"),
        // not only where tool/error rows exist: a faint tick per tool call OR
        // turn anchor, a red tick per error. A tick's fraction maps straight
        // to scroll offset for the tap-jump pre-scroll (see [_seekToFrac]).
        for (var i = 0; i < lensed.length; i++) {
          final e = lensed[i];
          final isErr = agentEventIsError(e, toolResults, toolUpdates);
          final isTool = (e['kind'] ?? '').toString() == 'tool_call';
          if (!isErr && !isTool && !turnIdxSet.contains(i)) continue;
          minimapMarks.add(FeedMinimapMark(
            frac: i / lensedDenom,
            seq: (e['seq'] as num?)?.toInt() ?? 0,
            isError: isErr,
            // Tick colour matches the transcript card (agentEventAccent),
            // so the strip reads as a colour-coded shrink of the feed.
            color: isErr
                ? DesignColors.error
                : agentEventAccent((e['kind'] ?? '').toString(),
                    (e['producer'] ?? 'agent').toString()),
          ));
        }
      }
    }
    // The bottom-left stepper steps a DIFFERENT unit per view, so `‹/›`
    // always mean something here:
    //   All view      → inbound prompts (turn anchors)
    //   filtered view → the matches shown (every lensed row) — so in the
    //                   Errors view it's prev/next error, in Text prev/next
    //                   message, etc.
    final stepAnchorIdx = _lens == FeedLens.all
        ? turnAnchorIdx
        : [for (var i = 0; i < lensed.length; i++) i];
    final stepSeqs = [
      for (final i in stepAnchorIdx) (lensed[i]['seq'] as num?)?.toInt() ?? 0,
    ];
    // Human label for the stepped unit (drives the button tooltips).
    final stepUnit = _lens == FeedLens.all
        ? 'prompt'
        : (_lens == FeedLens.errors
            ? 'error'
            : (_lens == FeedLens.text
                ? 'message'
                : (_lens == FeedLens.turns ? 'turn' : 'tool')));
    // Step relative to the last seek anchor (set by every jump). prevK =
    // last anchor strictly older than the anchor; nextK = first strictly
    // newer. Null at the ends → fall back (no wrap-around). With no anchor
    // yet, `ref` is open-ended so prev lands on the newest.
    final ref = _activeSeekSeq;
    int? prevStepK;
    int? nextStepK;
    for (var k = 0; k < stepSeqs.length; k++) {
      if (ref == null || stepSeqs[k] < ref) prevStepK = k;
      if (nextStepK == null && ref != null && stepSeqs[k] > ref) nextStepK = k;
    }
    // Count the verbose-gated events so the toggle can advertise its
    // value — "Show debug (12)" carries more signal than a bare button.
    int hiddenForVerbose = 0;
    if (!_verbose) {
      for (final e in _events) {
        final kind = (e['kind'] ?? '').toString();
        if (isVerboseOnly(kind, e['payload'])) hiddenForVerbose++;
      }
    }
    // ADR-021 W2.5 — mode/model picker is now hung off the parent's
    // AppBar via [onModeModelChanged] (lifted out of the body so it
    // doesn't burn a row of vertical space above every transcript).
    // The callback fires from the post-build microtask below to avoid
    // a setState-during-build on the parent.
    _maybeFireModeModelChanged();
    // ADR-036 W6 — also forward the latest session_name hint so the
    // parent's AppBar title can fall back to claude's auto-derived
    // label when the user hasn't set one. Reads the same events
    // already scanned for the chip pair; cheap incremental work.
    _maybeFireSessionNameHint(sessionNameFromEvents(_events));
    // v1.0.706 — forward the latest status_line payload to the
    // SessionChatScreen so the session-details sheet can show live
    // mutable state (effort, thinking, fast_mode, output_style).
    _maybeFireStatusLineChanged(latestStatusLinePayload(_events));
    // Verbose toggle chip, shared between the dense (top-right, beside the
    // expand button) and full-screen (shifted to clear the minimap) hosts.
    // Built once so both placements stay identical.
    final verboseChip = (_verbose || hiddenForVerbose > 0)
        ? VerboseToggleChip(
            verbose: _verbose,
            hiddenCount: hiddenForVerbose,
            onToggle: () => setState(() => _verbose = !_verbose),
          )
        : null;
    return Column(
      children: [
        // session.init is rendered in the parent AppBar via the
        // onSessionInit callback (the SessionInitChip widget lives in
        // session_details_sheet.dart). We intentionally don't render
        // it inline — the info is fixed for the session and burning a
        // full transcript row on mobile wasn't worth it.
        if (hasTelemetry)
          TelemetryStrip(
            totalCostUsd: totalCostUsd,
            turnCount: turnCount,
            modelTotals: modelTotals,
            rateLimit: latestRateLimit,
            contextWindow: latestContextWindow,
            contextUsed: latestContextUsed,
            processCostUsd: processCostUsd,
            sessionCostUsdImputed: sessionCostUsdImputed,
            sessionCostDetail: _sessionCost,
            rateLimitsFromStatus: rateLimitsFromStatus,
            exceeds200kAlarm: exceeds200k == true,
          ),
        if (_staleSince != null) OfflineBanner(staleSince: _staleSince!),
        // (The full-screen lens *bar* row was removed — it ate a vertical
        // row; full-screen now uses the same floating funnel as the dense
        // host, with per-lens counts in its menu. See the funnel below.)
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
                  // Record the realized-row window for convergent seeks, and
                  // the smallest realised seq (topmost card) for the Insight
                  // position readout.
                  if (i < _minBuiltIdx) _minBuiltIdx = i;
                  if (i > _maxBuiltIdx) _maxBuiltIdx = i;
                  final ev = lensed[i];
                  final builtSeq = (ev['seq'] as num?)?.toInt() ?? 0;
                  if (builtSeq > 0 &&
                      (_topBuiltSeq == 0 || builtSeq < _topBuiltSeq)) {
                    _topBuiltSeq = builtSeq;
                  }
                  final isTarget = _activeSeekSeq != null &&
                      (ev['seq'] as num?)?.toInt() == _activeSeekSeq;
                  Widget card = AgentEventCard(
                    key: isTarget ? _seekKey : null,
                    event: ev,
                    toolNames: toolNames,
                    toolUpdates: toolUpdates,
                    toolResults: toolResults,
                    resolvedApprovals: resolvedApprovals,
                    agentId: widget.agentId,
                  );
                  // In a filtered view, give each card a "view in context"
                  // affordance: tap it to clear the filter and land on this
                  // row in the full transcript so the surrounding turns are
                  // visible.
                  if (_lens != FeedLens.all) {
                    final seq = (ev['seq'] as num?)?.toInt();
                    if (seq != null) {
                      card = Stack(
                        children: [
                          card,
                          // Top-LEFT, not right: the right edge sits under
                          // the minimap column, where the two overlapped.
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
              // Inline approval cards (W1.A): when the agent has called
              // permission_prompt for a tier ≥ significant tool, the hub
              // posts an open attention_item. Pin it to the bottom of the
              // event list so the user sees it in context with the latest
              // turn — the agent is paused waiting for a decision, and
              // hiding the card behind a tab would invert the urgency.
              //
              // PendingSelections handles the parallel kind=select case
              // for request_select (multi-choice). Both cards filter by
              // agent_id so a prompt for a different steward doesn't
              // appear here.
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
                    child: NewEventsPill(
                      count: _newWhileAway,
                      onTap: _jumpToLatest,
                    ),
                  ),
                ),
              // When a lens filters out every loaded event, the empty
              // ListView would read as "no transcript". Tell the user
              // it's the filter — and that older matches may exist
              // above — so they can scroll up or clear it.
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
              // Transcript filter: funnel (rest) / combined filter+jump
              // pill (active) floating top-left — the mirror of the verbose
              // chip opposite. Floats over the Stack; never eats a row. Now
              // in BOTH hosts: full-screen dropped its lens-bar row and uses
              // this funnel too, carrying per-lens counts in its menu.
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
                  counts: widget.dense ? null : lensCounts,
                  // Prev = older (one step up the lensed list); next =
                  // newer (one step down). matchIndex is 1-based. matchSeqs
                  // is built 1:1 from the lensed list, so its index IS the
                  // lensed-list index — land via the height-agnostic
                  // convergent seek ([_seekToLoadedIndex]) rather than
                  // [_seekToSeq]'s lone ensureVisible, which silently no-ops
                  // when the match row isn't currently realised (the common
                  // case for a far jump in the full-screen transcript).
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
              // Monotonic "event N of M" position. N is the viewport-top run
              // ordinal, M the digest's run total — so the readout reflects the
              // whole run, not just the loaded window. Gated on a supplied
              // total (the analysis surface) so the plain full-screen feed,
              // which has no digest, doesn't show a loaded-max denominator that
              // would imply older unloaded events don't exist. On the random-
              // access surface it's a control (tap → "jump to any event"
              // scrubber); otherwise it self-wraps in an IgnorePointer so it
              // never steals a tap from the funnel/minimap.
              // (Suppressed in the Errors summary list — the position pill +
              // minimap describe the transcript window, which the digest-only
              // error list isn't; the funnel N/M + the list itself are the nav.)
              if (!widget.dense &&
                  !_errorsSummaryMode &&
                  widget.totalEventCount != null &&
                  _logPosition() != null)
                Positioned(
                  top: 8,
                  left: 0,
                  right: 0,
                  child: Center(
                    child: FeedPositionPill(
                      pos: _logPosition()!,
                      onTap: widget.randomAccess ? _openJumpSheet : null,
                    ),
                  ),
                ),
              // Right-edge minimap (full-screen only): a tick per tool call
              // / turn anchor (card-coloured) + a red tick per error, plus a
              // viewport indicator. Tap jumps to the nearest error, drag
              // scrubs. Always rendered in full-screen (every view) so the
              // strip is a consistent scrubber. In full-run anchor mode the
              // ticks are whole-run (digest + turn index) and a tap routes
              // through the random-access seek (the target may not be loaded).
              if (!widget.dense && !_errorsSummaryMode)
                Positioned(
                  top: 8,
                  right: 4,
                  bottom: 12,
                  // 28px (was 20) — a 20px strip at the screen edge was a
                  // near-unhittable tap target (and brushed the system edge-
                  // swipe zone); 28px gives the tap room without crowding.
                  width: 28,
                  child: FeedMinimap(
                    marks: minimapMarks,
                    onJump: _runAnchorMode
                        ? (frac, seq) {
                            // Turn anchors carry a ts (→ O(log n) window reset);
                            // error anchors don't and fall back to the page-walk.
                            _handleExternalSeek(
                                seq, widget.runAnchorTs?[seq]);
                          }
                        : _seekToFrac,
                    onScrub: _runAnchorMode ? null : _scrubTo,
                    viewportFrac: _minimapViewportFrac(),
                  ),
                ),
              // Full-screen stepper: floats bottom-left. ⤒ top-of-loaded,
              // ‹/› previous/next of the current view's unit ([stepUnit] —
              // prompt in All, error in Errors, message in Text, …). Always
              // actionable: prev falls back to paging older (then top), next
              // to jumping to the tail — so it never dead-ends.
              if (!widget.dense)
                Positioned(
                  left: 6,
                  bottom: 12,
                  child: TurnStepperPill(
                    unit: stepUnit,
                    onOldest: _jumpToOldestLoaded,
                    // In a filtered lens the bottom stepper IS the funnel
                    // stepper — same matchSeqs + cursor via [_funnelStep] — so
                    // tapping it moves the funnel's N/M and they never disagree
                    // (the "stepper and funnel don't align" bug). Only the All
                    // view keeps its own turn-anchor stepping (the funnel is
                    // inactive there). At the ends it still falls back to
                    // page-older / jump-to-tail so it never dead-ends.
                    onPrevTurn: (_lens != FeedLens.all && matchSeqs.isNotEmpty)
                        ? (matchIndex > 1
                            ? () => _funnelStep(matchIndex - 2, matchSeqs,
                                usesRunList: funnelUsesRunList)
                            : (!_atHead
                                ? () { _maybeLoadOlder(); }
                                : _jumpToOldestLoaded))
                        : (prevStepK != null
                            ? () => _seekToLensedIndex(
                                stepAnchorIdx[prevStepK!], lensed)
                            : (!_atHead
                                ? () { _maybeLoadOlder(); }
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
              // Top-right floating controls: expand (dense only) + verbose
              // toggle, in ONE row so they can't overlap (the previous
              // fixed-offset stacking collided once the verbose chip widened
              // with its hidden-count). Full-screen has no expand and shifts
              // right to clear the minimap lane (20px col at right:4 → left
              // edge ~right:24).
              if (verboseChip != null ||
                  (widget.dense && widget.onExpand != null))
                Positioned(
                  top: 6,
                  right: widget.dense ? 6 : 30,
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (widget.dense && widget.onExpand != null)
                        ExpandFeedButton(onTap: widget.onExpand!),
                      if (widget.dense &&
                          widget.onExpand != null &&
                          verboseChip != null)
                        const SizedBox(width: 6),
                      if (verboseChip != null) verboseChip,
                    ],
                  ),
                ),
            ],
          ),
        ),
        // (Turn stepper now floats bottom-left inside the Stack above,
        // replacing the old full-width footer row.)
        compose,
      ],
    );
  }

  /// Seek to the row at [idx] in the rendered [lensed] list — used by the
  /// turn-nav stepper. Routes through the convergent index seek so it lands
  /// exactly on the target row regardless of row-height variance (the old
  /// proportional [_seekToFrac] overshot on non-uniform transcripts).
  void _seekToLensedIndex(int idx, List<Map<String, dynamic>> lensed) {
    if (idx < 0 || idx >= lensed.length) return;
    final seq = (lensed[idx]['seq'] as num?)?.toInt() ?? 0;
    _seekToLoadedIndex(idx, seq);
  }

  /// ADR-021 W2.5 — find the latest mode + model state advertised by
  /// the agent. ACP gemini emits these as `kind=system, producer=system`
  /// notifications keyed by `sessionUpdate=current_mode_update` /
  /// `current_model_update` (driver_acp.go line ~924). We sniff payload
  /// keys rather than the upstream subtype so claude/codex (which carry
  /// `model` directly on session.init) can also surface in the picker
  /// once W2.3 / hub routing land for them.
  ///
  /// Returns null when no agent has advertised mode + model lists. The
  /// AppBar icon hides itself in that case rather than rendering an
  /// empty picker.
  ModeModelPickerData? _latestModeModelData() {
    final raw = modeModelStateFromEvents(_events);
    if (raw == null) return null;
    return ModeModelPickerData(
      currentMode: raw['currentMode'] as String?,
      availableModes:
          (raw['availableModes'] as List).cast<Map<String, dynamic>>(),
      currentModel: raw['currentModel'] as String?,
      availableModels:
          (raw['availableModels'] as List).cast<Map<String, dynamic>>(),
      onPickMode: _onSetMode,
      onPickModel: _onSetModel,
    );
  }

  // Signature of the most recent payload we forwarded via
  // [onModeModelChanged]. Cheap fingerprint over the four fields the
  // parent renders — id-based, so picker option re-orderings without a
  // current-id change don't trigger a redundant setState upstream.
  String? _lastModeModelSig;

  // Compute a stable signature for change detection.
  String _modeModelSig(ModeModelPickerData? d) {
    if (d == null) return '';
    final modeIds = d.availableModes
        .map((m) => m['id']?.toString() ?? '')
        .join('|');
    // Models carry `modelId` (mode entries carry `id`) per ACP spec —
    // match _ModeModelPicker._buildChip so the signature recomputes
    // when the model id changes on kimi-shape responses (W7).
    final modelIds = d.availableModels
        .map((m) => (m['modelId'] ?? m['id'])?.toString() ?? '')
        .join('|');
    return '${d.currentMode ?? ''}::$modeIds::${d.currentModel ?? ''}::$modelIds';
  }

  // Fire the parent callback when the picker payload changes. Called
  // from build() — schedules the actual notify on a post-frame
  // callback so the parent's setState doesn't fire while AgentFeed is
  // still building.
  void _maybeFireModeModelChanged() {
    final cb = widget.onModeModelChanged;
    if (cb == null) return;
    final next = _latestModeModelData();
    final sig = _modeModelSig(next);
    if (sig == _lastModeModelSig) return;
    _lastModeModelSig = sig;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      cb(next);
    });
  }

  // ADR-036 W6 — fire the session-name-hint callback when the
  // session_name field surfaced by status_line changes (or first
  // arrives). Same deferred-post-frame pattern as
  // _maybeFireModeModelChanged so the parent's setState doesn't
  // collide with this widget's build. We track the LAST hint we
  // forwarded (not the current reducer value) so a flap from
  // "name" → null → "name" still fires both transitions (null is a
  // valid hint, meaning "claude cleared the auto-derived label
  // across a /clear").
  String? _lastSessionNameHint;
  bool _lastSessionNameHintSet = false;
  void _maybeFireSessionNameHint(String? hint) {
    final cb = widget.onSessionNameHint;
    if (cb == null) return;
    if (_lastSessionNameHintSet && _lastSessionNameHint == hint) return;
    _lastSessionNameHint = hint;
    _lastSessionNameHintSet = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      cb(hint);
    });
  }

  // v1.0.706 — same deferred-post-frame pattern for status_line
  // forwarding. We dedupe on identity of the payload reference (the
  // reducer returns the SAME Map instance for the same latest event
  // — when a new status_line lands, _events grows and a new payload
  // reference replaces the cached one). Equality-on-content would
  // be wrong here: a frame with identical content but a new event
  // is still a refresh-tick the parent might want to know about (the
  // sheet's "rates as of X seconds ago" annotation, future).
  Map<String, dynamic>? _lastStatusLinePayload;
  bool _lastStatusLinePayloadSet = false;
  void _maybeFireStatusLineChanged(Map<String, dynamic>? payload) {
    final cb = widget.onStatusLineChanged;
    if (cb == null) return;
    if (_lastStatusLinePayloadSet &&
        identical(_lastStatusLinePayload, payload)) return;
    _lastStatusLinePayload = payload;
    _lastStatusLinePayloadSet = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      cb(payload);
    });
  }

  Future<void> _onSetMode(String modeId) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.postAgentInput(widget.agentId,
          kind: 'set_mode', modeId: modeId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('mode → $modeId'),
          duration: const Duration(seconds: 2),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('set_mode failed: $e'),
          duration: const Duration(seconds: 4),
        ),
      );
    }
  }

  Future<void> _onSetModel(String modelId) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.postAgentInput(widget.agentId,
          kind: 'set_model', modelId: modelId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('model → $modelId'),
          duration: const Duration(seconds: 2),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('set_model failed: $e'),
          duration: const Duration(seconds: 4),
        ),
      );
    }
  }

}


// LiveFeed — mobile renderer for the hub's live agent_events stream
// (blueprint P2.1). Subscribes to SSE, backfills any seq the user
// missed, and lays each event out as a typed card (text, tool_call,
// tool_result, completion, lifecycle, …). Unknown kinds fall through
// to a raw JSON card so the transcript is never silently dropped.
//
// This is the live-conversation half of the former flag-switched AgentFeed
// (ADR-040): the sealed / random-access half lives in `insight_transcript.dart`
// (`InsightTranscript`). LiveFeed keeps the live-tail loader, the composer, the
// telemetry strip, and the loaded-window lens / stepper.
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
import 'agent_compose.dart';
import 'transcript/event_card.dart';
import 'transcript/feed_misc.dart';
import 'transcript/feed_telemetry.dart';
import 'transcript/fold_maps.dart';
import 'transcript/feed_reducer.dart';
import 'transcript/tool_renderers.dart';
import 'transcript/transcript_seek.dart';
import 'transcript/interaction_cards.dart';
import 'transcript/telemetry_strip.dart';
import 'session_details_sheet.dart';

// W0 (docs/plans/agent-feed-split.md): the reducer/formatter layer now
// lives in transcript/feed_reducer.dart (ADR-040 substrate rename). Re-export
// it so the reducer tests (which import this file) resolve unchanged and
// external callers keep their single import surface.
export 'transcript/feed_reducer.dart';

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
class LiveFeed extends ConsumerStatefulWidget {
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
  /// filter/jump pill. `false` is a full-screen host: the lens unfolds
  /// to a horizontal *bar* with per-lens counts.
  final bool dense;
  /// When set (and [dense]), a floating expand affordance pushes the
  /// caller's dedicated full-screen transcript route. Null hides it —
  /// hosts that are already full-screen pass `dense: false` instead.
  final VoidCallback? onExpand;
  const LiveFeed({
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
  });

  @override
  ConsumerState<LiveFeed> createState() => _LiveFeedState();
}

class _LiveFeedState extends ConsumerState<LiveFeed> {
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
  // protocol trace. Per-LiveFeed instance, not global.
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
  // The landing engine (ADR-040 P2a): owns the seek GlobalKey (attached to the
  // card matching [_activeSeekSeq] during build), the realized-row window
  // sentinels, and the programmatic-scroll guard. Bound in initState. Drives
  // the cold-open initialSeq deep-link and the lens match-stepper landings.
  late final TranscriptSeek _seek;
  // The seq the feed is currently anchored to (cold-open initialSeq, or
  // the active lens match). Null = no anchor (tail-follow). The matching
  // card gets [_seek.seekKey] + a tinted border while [_seekHighlight] holds.
  int? _activeSeekSeq;
  // True for ~1.2s after a successful seek so the matched event renders
  // with a tinted border, telling the user where they landed. Cleared
  // by [_seekHighlightTimer].
  bool _seekHighlight = false;
  Timer? _seekHighlightTimer;
  // Single-select transcript lens (P1 — docs/plans/agent-transcript-
  // debug-and-header-parity.md). Ephemeral per-LiveFeed instance: resets
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

  @override
  void initState() {
    super.initState();
    // The landing engine (ADR-040 P2a) owns the realized-window sentinels, the
    // programmatic-scroll guard, and the seek GlobalKey; it's bound to this
    // feed's scroll controller + mounted state.
    _seek = TranscriptSeek(scroll: _scroll, isActive: () => mounted);
    _bootstrap();
    _scroll.addListener(_onScroll);
    _startSessionCostPolling();
  }

  @override
  void didUpdateWidget(covariant LiveFeed oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.sessionId != widget.sessionId) {
      // Session swap: nuke the prior cost so the chip self-gates
      // until the new session's first poll lands, then restart the
      // timer rooted at the new id.
      _sessionCost = null;
      _startSessionCostPolling();
    }
  }

  @override
  void dispose() {
    _reconnectTimer?.cancel();
    _bannerGraceTimer?.cancel();
    _sessionCostTimer?.cancel();
    _seekHighlightTimer?.cancel();
    _sub?.cancel();
    _scroll.dispose();
    super.dispose();
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
    // CRITICAL: only let a *user* scroll flip tail-follow. A programmatic
    // scroll (a seek/scrub/jump) can momentarily land near the bottom — if
    // that re-enabled _followTail, the next live event would yank the user
    // to the end (the "jump to end" tester bug). During programmatic
    // motion we touch neither _followTail nor the load pager.
    if (_seek.isProgrammatic) return;
    if (_followTail != atBottom) {
      setState(() {
        _followTail = atBottom;
        // Returning to the tail clears the pending-event counter; the
        // pill disappears on the same frame.
        if (atBottom) {
          _newWhileAway = 0;
          // Reset the turn-stepper anchor so the next `‹` starts from the
          // newest prompt rather than wherever we last jumped.
          _activeSeekSeq = null;
        }
      });
    }
    if (_scroll.position.pixels <= 120) _maybeLoadOlder();
  }

  // The programmatic-scroll guard, the realized-row-window sentinels, and the
  // jump/animate helpers all moved to [TranscriptSeek] (`_seek`) — ADR-040 P2a.

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
      // Query session.init events SPECIFICALLY (server-side kind filter),
      // across ALL sessions. The previous unfiltered tail(200) missed the
      // chip on any session longer than ~200 events: session.init is the
      // session's FIRST event, so on a long-running M2 session it has long
      // since scrolled out of the tail window — the backfill found nothing
      // and the AppBar chip stayed empty. The kind filter returns the
      // newest session.init regardless of how much else has flowed since.
      // Small limit because session.init is rare (one per process start;
      // a resumed session may carry a few across respawns).
      final inits = await client.listAgentEvents(
        widget.agentId,
        tail: true,
        limit: 25,
        kinds: const ['session.init'],
        // No sessionId — chip stays informative across "new session" /
        // resume flows where the init lives in a sibling session.
      );
      if (!mounted) return;
      // tail returns newest-first (DESC); merge oldest→newest so newer
      // fields win and earlier-only fields persist, matching the live
      // build() path (latestSessionInitPayload in feed_reducer.dart).
      Map<String, dynamic>? merged;
      for (final e in inits.reversed) {
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
    _scrollToTail();
    setState(() {
      _followTail = true;
      _newWhileAway = 0;
    });
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
  /// post-frame read of the seek key no-ops gracefully if it isn't.
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

  /// Seek precisely onto the loaded row at [idx] (in the current display
  /// list — post-lens, post-grouping) identified by [seq]. A bare item-index fraction mapped straight
  /// onto a pixel offset misses badly when rows have very different heights
  /// (a one-line text card vs. a tall tool dump), so instead this
  /// binary-searches the scroll offset using the actual
  /// realized-row window (the [TranscriptSeek] landing engine) as feedback, so it
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
    // The landing engine (ADR-040 P2a) holds the programmatic-scroll guard
    // across the whole convergence and runs it after this setState's rebuild
    // has attached the seek key to the new target.
    _seek.landOnIndex(idx);
    _seekHighlightTimer?.cancel();
    _seekHighlightTimer = Timer(const Duration(milliseconds: 1600), () {
      if (!mounted) return;
      setState(() => _seekHighlight = false);
    });
  }

  /// Switch the active lens. Resets the seek anchor; for a non-`all` lens, pins
  /// to the tail so the user lands on the most recent match (the newest error
  /// is usually what you're debugging).
  void _setLens(FeedLens lens) {
    setState(() {
      _lens = lens;
      _activeSeekSeq = null;
      if (lens != FeedLens.all) _followTail = true;
    });
    if (lens != FeedLens.all) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _scrollToTail();
      });
    }
  }

  // A seq the user asked to view "in context": tapped from a filtered card, it
  // switches to the All lens and (in build, once the unfiltered list is back)
  // seeks to that row so the surrounding turns are visible.
  int? _pendingContextSeq;

  /// Clear the active filter and land on [seq] in the full transcript, so a
  /// match found in a filtered view can be read with its surrounding context.
  /// The seek itself runs in build once `_lens == all` has put the row back in
  /// the list (we know its index there, so it works even if the row isn't
  /// currently realised).
  void _jumpToContext(int seq) {
    setState(() {
      _lens = FeedLens.all;
      _followTail = false;
      _pendingContextSeq = seq;
    });
  }

  /// Step the funnel cursor to match [i] of [matchSeqs] — driven by the
  /// top-left funnel pill (`FeedFilterControl`). The match list is the
  /// loaded+lensed window; the convergent index seek lands the row exactly
  /// (height-agnostic).
  void _funnelStep(int i, List<int> matchSeqs) {
    if (i < 0 || i >= matchSeqs.length) return;
    _seekToLoadedIndex(i, matchSeqs[i]);
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    final l10n = AppLocalizations.of(context)!;
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
    final isAgentBusy = agentIsBusy(_events);
    final Widget compose = AgentCompose(
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
    // The per-event fold (tool names / results / updates / resolved approvals)
    // — the lineage maps cards render from and lens predicates read. Extracted
    // to the shared substrate (ADR-040): pure over the loaded window, so both
    // transcript modes share it. Local bindings keep the downstream call sites
    // unchanged.
    final fold = FoldMaps.fromEvents(_events);
    final toolNames = fold.toolNames;
    final resolvedApprovals = fold.resolvedApprovals; // request_id → decision
    final toolUpdates = fold.toolUpdates;
    final toolResults = fold.toolResults;
    // session.init is now reported up to the parent (AppBar) via the
    // onSessionInit callback above; nothing inline needs the payload.
    // The telemetry rollup (LiveFeed-only — Insight draws its dashboard from
    // the digest RunReportCard and hides the strip). Extracted verbatim to the
    // shared substrate (ADR-040 P1b); local bindings keep the TelemetryStrip +
    // hasTelemetry call sites unchanged.
    final tele = FeedTelemetry.fromEvents(_events, _sessionCost);
    final totalCostUsd = tele.totalCostUsd;
    final modelTotals = tele.modelTotals;
    final turnCount = tele.turnCount;
    final latestRateLimit = tele.latestRateLimit;
    final latestContextWindow = tele.latestContextWindow;
    final latestContextUsed = tele.latestContextUsed;
    final processCostUsd = tele.processCostUsd;
    final sessionCostUsdImputed = tele.sessionCostUsdImputed;
    final rateLimitsFromStatus = tele.rateLimitsFromStatus;
    final exceeds200k = tele.exceeds200k;
    final hasTelemetry = tele.hasTelemetry;
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
    // P1 tool-call grouping (agent-transcript-redesign §6 P1, decision
    // §7.3): a run of ≥2 consecutive tool_calls renders as ONE group
    // card. Pure render-layer transform over the post-lens list — the
    // lens predicates, FoldMaps, counts, and busy inference above are
    // untouched, and a lens change regroups the filtered list
    // naturally. Everything below (row building, seek indices,
    // match-stepping) walks [display]; [lensed] stays the event-level
    // source for the empty-filter check and lens counts.
    final display = groupConsecutiveToolCalls(lensed);
    // Reset the landing engine's realized-row window for this frame's list;
    // the itemBuilder repopulates it during layout (via [_seek.recordBuiltRow])
    // and a convergent seek reads it back. Also snapshots the last frame's
    // top-built seq for the Insight position readout.
    _seek.beginFrame(display.length);
    // Consume a pending "view in context" request: now that the lens is
    // back to All (so [lensed] == [visible]), find the row's index in the
    // unfiltered list and seek to it once this frame lays out. Convergent
    // index seek (height-agnostic) so it lands exactly on the row even when
    // it isn't currently realised. The lookup is display-row based: a
    // target seq INSIDE a tool-call group lands on the group row.
    if (_pendingContextSeq != null && _lens == FeedLens.all) {
      final target = _pendingContextSeq!;
      _pendingContextSeq = null;
      var idx = display.indexWhere((item) => item.containsSeq(target));
      if (idx < 0) {
        // No exact row — the anchor may be a hidden marker (e.g. an ACP
        // `turn.start`, which isn't rendered) or filtered out. Land on the
        // nearest visible row at or after it: the turn's first shown event.
        var bestSeq = 1 << 30;
        for (var i = 0; i < display.length; i++) {
          final s = display[i].anchorSeq;
          if (s >= target && s < bestSeq) {
            bestSeq = s;
            idx = i;
          }
        }
      }
      if (idx >= 0) {
        final landSeq = display[idx].anchorSeq;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _seekToLoadedIndex(idx, landSeq);
        });
      }
    }
    // The lens funnel/stepper walk the loaded+lensed window. matchSeqs is the
    // 1:1 anchor-seq list of the rendered display rows (a group's anchor is
    // its first member's seq); older matches join as the user scrolls up and
    // the list grows.
    final List<int> matchSeqs = _lens == FeedLens.all
        ? const <int>[]
        : [for (final item in display) item.anchorSeq];
    // 1-based position of the active match. Default to the newest when there's
    // no explicit anchor (fresh lens activation) so the pill reads N/N and the
    // steppers walk backward into history.
    int matchIndex = 0;
    if (matchSeqs.isNotEmpty) {
      var idx =
          _activeSeekSeq == null ? -1 : matchSeqs.indexOf(_activeSeekSeq!);
      if (idx < 0) idx = matchSeqs.length - 1;
      matchIndex = idx + 1;
    }
    // P3 — full-screen-only chrome (dense=false): per-lens counts for the
    // lens bar over the WHOLE loaded transcript. Skipped in the dense
    // path so a constrained host pays nothing for it.
    Map<FeedLens, int> lensCounts = const {};
    if (!widget.dense) {
      lensCounts = {
        for (final l in FeedLens.values)
          l: l == FeedLens.all
              ? visible.length
              : visible
                  .where((e) =>
                      agentEventMatchesLens(e, l, toolResults, toolUpdates))
                  .length,
      };
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
    // expand button) and full-screen hosts. Built once so both placements
    // stay identical.
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
              ListView.separated(
                controller: _scroll,
                padding: widget.padding,
                itemCount: display.length,
                separatorBuilder: (_, _) => const SizedBox(height: 8),
                itemBuilder: (ctx, i) {
                  final item = display[i];
                  final builtSeq = item.anchorSeq;
                  // Record the realized-row window for convergent seeks, and
                  // the smallest realised seq (topmost card) for the Insight
                  // position readout.
                  _seek.recordBuiltRow(i, builtSeq);
                  final isTarget =
                      _activeSeekSeq != null && item.containsSeq(_activeSeekSeq!);
                  // A tool-call GROUP renders as one card keyed by its
                  // run's anchor seq — the key keeps the group's
                  // collapse/row-expansion State alive across rebuilds
                  // as new events stream in (decision §7.3: collapse is
                  // user opt-in per group instance). Everything else is
                  // the existing per-event card.
                  Widget card = item.isGroup
                      ? ToolCallGroupCard(
                          key: isTarget
                              ? _seek.seekKey
                              : ValueKey('tool-group-${item.anchorSeq}'),
                          group: item.group!,
                          toolUpdates: toolUpdates,
                          toolResults: toolResults,
                        )
                      : AgentEventCard(
                          key: isTarget ? _seek.seekKey : null,
                          event: item.event!,
                          toolNames: toolNames,
                          toolUpdates: toolUpdates,
                          toolResults: toolResults,
                          resolvedApprovals: resolvedApprovals,
                          agentId: widget.agentId,
                        );
                  // In a filtered view, give each card a "view in context"
                  // affordance: tap it to clear the filter and land on this
                  // row in the full transcript so the surrounding turns are
                  // visible. A group jumps on its anchor seq; the All-lens
                  // lookup matches any member, so it lands on the group.
                  if (_lens != FeedLens.all) {
                    final seq = item.anchorSeq;
                    if (seq > 0) {
                      card = Stack(
                        children: [
                          card,
                          // Top-LEFT, not right, to clear the card's own
                          // top-right controls.
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
                        borderRadius: Radii.mdBorder,
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
                        horizontal: 12, vertical: Spacing.s8),
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
              if (_lens != FeedLens.all && lensed.isEmpty)
                Center(
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
                    child: Text(
                      l10n.noLensEventsLoaded(
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
                  // Prev = older (one step up the display list); next =
                  // newer (one step down). matchIndex is 1-based. matchSeqs
                  // is built 1:1 from the display rows (post-grouping), so
                  // its index IS the display-list index — land via the
                  // height-agnostic
                  // convergent seek ([_seekToLoadedIndex]) rather than
                  // [_seekToSeq]'s lone ensureVisible, which silently no-ops
                  // when the match row isn't currently realised (the common
                  // case for a far jump in the full-screen transcript).
                  onPrev: () {
                    if (matchIndex > 1) {
                      _funnelStep(matchIndex - 2, matchSeqs);
                    }
                  },
                  onNext: () {
                    if (matchIndex >= 1 && matchIndex < matchSeqs.length) {
                      _funnelStep(matchIndex, matchSeqs);
                    }
                  },
                ),
              ),
              // Top-right floating controls: expand (dense only) + verbose
              // toggle, in ONE row so they can't overlap (the previous
              // fixed-offset stacking collided once the verbose chip widened
              // with its hidden-count).
              if (verboseChip != null ||
                  (widget.dense && widget.onExpand != null))
                Positioned(
                  top: 6,
                  right: 6,
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
        compose,
      ],
    );
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
  // callback so the parent's setState doesn't fire while LiveFeed is
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
    final l10n = AppLocalizations.of(context)!;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.postAgentInput(widget.agentId,
          kind: 'set_mode', modeId: modeId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.liveFeedModeSet(modeId)),
          duration: const Duration(seconds: 2),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.liveFeedSetModeFailed(e.toString())),
          duration: const Duration(seconds: 4),
        ),
      );
    }
  }

  Future<void> _onSetModel(String modelId) async {
    final l10n = AppLocalizations.of(context)!;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.postAgentInput(widget.agentId,
          kind: 'set_model', modelId: modelId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.liveFeedModelSet(modelId)),
          duration: const Duration(seconds: 2),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.liveFeedSetModelFailed(e.toString())),
          duration: const Duration(seconds: 4),
        ),
      );
    }
  }

}


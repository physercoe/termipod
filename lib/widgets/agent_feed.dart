// AgentFeed — mobile renderer for the hub's agent_events stream
// (blueprint P2.1). Subscribes to SSE, backfills any seq the user
// missed, and lays each event out as a typed card (text, tool_call,
// tool_result, completion, lifecycle, …). Unknown kinds fall through
// to a raw JSON card so the transcript is never silently dropped.
import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:markdown/markdown.dart' as md;

import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';
import 'agent_compose.dart';
import 'agent_feed/feed_misc.dart';
import 'agent_feed/feed_reducer.dart';
import 'agent_feed/feed_render.dart';
import 'agent_feed/telemetry_strip.dart';
import 'markdown_builders.dart';
import 'session_details_sheet.dart';

// W0 (docs/plans/agent-feed-split.md): the reducer/formatter layer now
// lives in agent_feed/feed_reducer.dart. Re-export it so the ten
// agent_feed_* reducer tests (which import this file) resolve unchanged
// and external callers keep their single import surface.
export 'agent_feed/feed_reducer.dart';

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
  // Anchor for the "scroll to specific seq" feature (initialSeq).
  // Attached to whichever AgentEventCard matches the target during
  // build; ensureVisible needs a real BuildContext, hence the key.
  final GlobalKey _initialSeqKey = GlobalKey();
  // True for ~1.2s after a successful jump-to-seq so the matched event
  // renders with a tinted border, telling the user where they landed.
  // Cleared by a delayed setState; never re-fires within a session.
  bool _initialSeqHighlight = false;

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
    _bootstrap();
    _scroll.addListener(_onScroll);
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
  }

  @override
  void dispose() {
    _reconnectTimer?.cancel();
    _bannerGraceTimer?.cancel();
    _sessionCostTimer?.cancel();
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
  // Latest scroll progress in 0..100. Refreshed on every scroll tick;
  // the jump-to-tail pill renders this so users have a sense of where
  // they are in long sessions ("3% — top of the loaded transcript",
  // "82% — almost back at tail").
  int _scrollPercent = 100;

  void _onScroll() {
    if (!_scroll.hasClients) return;
    final atBottom = _scroll.position.pixels >=
        _scroll.position.maxScrollExtent - 40;
    final maxExt = _scroll.position.maxScrollExtent;
    final pct = maxExt <= 0
        ? 100
        : ((_scroll.position.pixels / maxExt) * 100).clamp(0, 100).round();
    final percentChanged = pct != _scrollPercent;
    if (_followTail != atBottom || percentChanged) {
      setState(() {
        _followTail = atBottom;
        _scrollPercent = pct;
        // Returning to the tail clears the pending-event counter; the
        // pill disappears on the same frame.
        if (atBottom) _newWhileAway = 0;
      });
    }
    if (_scroll.position.pixels <= 120) _maybeLoadOlder();
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

  /// Tries to scroll the matched-seq event into view + highlight it.
  /// Returns true if the target was reachable; false when the seq
  /// isn't in the loaded page or the keyed widget hasn't built yet
  /// (caller falls back to _scrollToTail). Uses Scrollable.ensureVisible
  /// which handles non-uniform row heights without a positioned-list
  /// dependency.
  bool _trySeekInitialSeq() {
    final target = widget.initialSeq;
    if (target == null) return false;
    final hit = _events.any((e) => (e['seq'] as num?)?.toInt() == target);
    if (!hit) return false;
    final ctx = _initialSeqKey.currentContext;
    if (ctx == null) return false;
    Scrollable.ensureVisible(
      ctx,
      alignment: 0.3,
      duration: const Duration(milliseconds: 300),
      curve: Curves.easeOut,
    );
    // We landed on a specific row, so the user is anchored — disable
    // tail-follow until they scroll back near the bottom themselves.
    setState(() {
      _followTail = false;
      _initialSeqHighlight = true;
    });
    Timer(const Duration(milliseconds: 1200), () {
      if (!mounted) return;
      setState(() => _initialSeqHighlight = false);
    });
    return true;
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
    final isAgentBusy = agentIsBusy(_events);
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
                itemCount: visible.length,
                separatorBuilder: (_, _) => const SizedBox(height: 8),
                itemBuilder: (ctx, i) {
                  final ev = visible[i];
                  final isTarget = widget.initialSeq != null &&
                      (ev['seq'] as num?)?.toInt() == widget.initialSeq;
                  final card = AgentEventCard(
                    key: isTarget ? _initialSeqKey : null,
                    event: ev,
                    toolNames: toolNames,
                    toolUpdates: toolUpdates,
                    toolResults: toolResults,
                    resolvedApprovals: resolvedApprovals,
                    agentId: widget.agentId,
                  );
                  if (isTarget && _initialSeqHighlight) {
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
              // _PendingSelections handles the parallel kind=select case
              // for request_select (multi-choice). Both cards filter by
              // agent_id so a prompt for a different steward doesn't
              // appear here.
              const Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    _PendingSelections(),
                    _PendingPermissionPrompts(),
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
                      scrollPercent: _scrollPercent,
                      onTap: _jumpToLatest,
                    ),
                  ),
                ),
              // Verbose toggle: tiny floating chip in the top-right.
              // Replaces the previous full-row strip — the row was
              // mostly whitespace + a long descriptive label, eating
              // a transcript line on every steward chat. Only renders
              // when there's actually something to toggle (events
              // hidden, or already in verbose mode).
              if (_verbose || hiddenForVerbose > 0)
                Positioned(
                  top: 6,
                  right: 6,
                  child: VerboseToggleChip(
                    verbose: _verbose,
                    hiddenCount: hiddenForVerbose,
                    onToggle: () => setState(() => _verbose = !_verbose),
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

/// W1.A — inline tool-call approval surface for the steward chat.
///
/// Reads open `kind=permission_prompt` attention items from
/// hubProvider, filters to ones whose pending payload's `agent_id`
/// matches this AgentFeed's agentId, renders a card per pending
/// item. Each card shows the tier (significant / strategic),
/// tool name + input preview, and Approve / Deny buttons.
///
/// Strategic tier requires a typed reason and is non-default-yes —
/// the user must explicitly tap Approve, no Enter-to-confirm. This
/// is the load-bearing difference between "user delegated this
/// scope" and "user genuinely intervened".
///
/// Trivial / Routine tiers don't reach here: the server short-
/// circuits them in mcpPermissionPrompt with an audit-only allow.
class _PendingPermissionPrompts extends ConsumerWidget {
  const _PendingPermissionPrompts();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final feed = context.findAncestorStateOfType<_AgentFeedState>();
    final agentId = feed?.widget.agentId ?? '';
    final attention = ref
            .watch(hubProvider.select((s) => s.value?.attention)) ??
        const <Map<String, dynamic>>[];
    final pending = <Map<String, dynamic>>[];
    for (final a in attention) {
      if ((a['kind'] ?? '').toString() != 'permission_prompt') continue;
      if ((a['status'] ?? '').toString() != 'open') continue;
      final payload = _payloadOf(a);
      if (agentId.isNotEmpty &&
          (payload['agent_id'] ?? '').toString() != agentId) {
        continue;
      }
      pending.add(a);
    }
    if (pending.isEmpty) return const SizedBox.shrink();
    return Material(
      color: Colors.transparent,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final a in pending)
            _PermissionPromptCard(
              attention: a,
              payload: _payloadOf(a),
            ),
        ],
      ),
    );
  }

  static Map<String, dynamic> _payloadOf(Map<String, dynamic> attention) {
    final raw = attention['pending_payload'];
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {}
    }
    return const {};
  }
}

class _PermissionPromptCard extends ConsumerStatefulWidget {
  final Map<String, dynamic> attention;
  final Map<String, dynamic> payload;
  const _PermissionPromptCard({
    required this.attention,
    required this.payload,
  });

  @override
  ConsumerState<_PermissionPromptCard> createState() =>
      _PermissionPromptCardState();
}

class _PermissionPromptCardState
    extends ConsumerState<_PermissionPromptCard> {
  bool _sending = false;
  String? _error;
  final TextEditingController _reasonCtrl = TextEditingController();

  @override
  void dispose() {
    _reasonCtrl.dispose();
    super.dispose();
  }

  String get _tier =>
      (widget.payload['tier'] ?? 'significant').toString().toLowerCase();
  bool get _isStrategic => _tier == 'strategic';

  /// ADR-027 W8: dialog_type discriminator on permission_prompt
  /// attention items. Defaults to tool_permission for backward
  /// compatibility with rows that pre-date the discriminator.
  ///
  ///   tool_permission (default) — render tool name + input preview
  ///   plan_approval             — render plan_body as markdown
  ///   compaction                — render "Compact context now?" prompt
  ///
  /// user_question is NOT handled here — it goes through
  /// approval_request agent_events + _ApprovalCard, not attention_items.
  String get _dialogType =>
      (widget.payload['dialog_type'] ?? 'tool_permission').toString();

  Future<void> _decide(String decision) async {
    final id = (widget.attention['id'] ?? '').toString();
    if (id.isEmpty) return;
    final reason = _reasonCtrl.text.trim();
    if (_isStrategic && decision == 'approve' && reason.isEmpty) {
      setState(() => _error = 'Strategic-tier approvals require a reason');
      return;
    }
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.decideAttention(
        id,
        decision: decision,
        reason: reason.isEmpty ? null : reason,
      );
      // refreshAll picks up the resolved attention so this card vanishes
      // on the next provider tick. Cheaper than a per-item invalidate
      // and consistent with the pattern used by other decide flows.
      await ref.read(hubProvider.notifier).refreshAll();
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final scheme = Theme.of(context).colorScheme;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;

    // ADR-027 W8: per-dialog_type title + body + button labels.
    final dialogType = _dialogType;
    final toolName = (widget.payload['tool_name'] ?? 'tool').toString();
    final input = widget.payload['input'];
    final inputText = input == null
        ? ''
        : (input is String ? input : feedJsonPretty(input));
    final tierColor = switch (_tier) {
      'strategic' => DesignColors.error,
      'significant' => DesignColors.warning,
      _ => DesignColors.primary,
    };
    final String headerTitle;
    final String approveLabel;
    final String denyLabel;
    switch (dialogType) {
      case 'plan_approval':
        headerTitle = 'Approve plan?';
        approveLabel = 'Approve';
        denyLabel = 'Reject';
        break;
      case 'compaction':
        headerTitle = 'Compact context?';
        approveLabel = 'Compact';
        denyLabel = 'Defer';
        break;
      default:
        headerTitle = 'Approve $toolName?';
        approveLabel = _isStrategic ? 'Approve (strategic)' : 'Approve';
        denyLabel = 'Deny';
    }
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: scheme.surface,
        border: Border.all(color: tierColor.withValues(alpha: 0.6), width: 1.5),
        borderRadius: BorderRadius.circular(10),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.08),
            blurRadius: 8,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Icon(Icons.gavel, size: 16, color: tierColor),
              const SizedBox(width: 6),
              Text(
                _tier.toUpperCase(),
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 10,
                  fontWeight: FontWeight.w800,
                  color: tierColor,
                  letterSpacing: 0.6,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  headerTitle,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          // Body — per dialog_type. ADR-027 W8.
          if (dialogType == 'plan_approval') ...[
            const SizedBox(height: 6),
            _PlanApprovalBody(
              planBody: (widget.payload['plan_body'] ?? '').toString(),
            ),
          ] else if (dialogType == 'compaction') ...[
            const SizedBox(height: 6),
            _CompactionBody(
              trigger: (widget.payload['trigger'] ?? '').toString(),
              customInstructions:
                  (widget.payload['custom_instructions'] ?? '').toString(),
            ),
          ] else if (inputText.isNotEmpty) ...[
            const SizedBox(height: 6),
            CollapsibleMono(text: inputText),
          ],
          if (_isStrategic) ...[
            const SizedBox(height: 8),
            TextField(
              controller: _reasonCtrl,
              enabled: !_sending,
              minLines: 1,
              maxLines: 3,
              style: GoogleFonts.jetBrainsMono(fontSize: 11),
              decoration: InputDecoration(
                isDense: true,
                hintText: 'Reason required for strategic-tier approvals',
                hintStyle: GoogleFonts.jetBrainsMono(
                    fontSize: 11, color: muted),
                contentPadding: const EdgeInsets.symmetric(
                    horizontal: 10, vertical: 8),
                border: const OutlineInputBorder(),
              ),
            ),
          ],
          if (_error != null) ...[
            const SizedBox(height: 6),
            Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ],
          const SizedBox(height: 8),
          Row(
            children: [
              FilledButton.tonal(
                onPressed: _sending ? null : () => _decide('reject'),
                style: FilledButton.styleFrom(
                  backgroundColor: DesignColors.error.withValues(alpha: 0.15),
                  foregroundColor: DesignColors.error,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(denyLabel),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: _sending ? null : () => _decide('approve'),
                style: FilledButton.styleFrom(
                  backgroundColor: dialogType == 'plan_approval'
                      ? DesignColors.primary
                      : (_isStrategic
                          ? DesignColors.error
                          : DesignColors.success),
                  foregroundColor: Colors.white,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(approveLabel),
              ),
              const Spacer(),
              if (_sending)
                const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// ADR-027 W8: body widget for permission_prompt rows whose
/// dialog_type is "plan_approval". Renders the agent's proposed plan
/// (carried in payload.plan_body) as markdown so the principal can
/// scan it inside the approval card. Empty plan_body falls back to a
/// muted placeholder rather than rendering an empty body.
class _PlanApprovalBody extends StatelessWidget {
  final String planBody;
  const _PlanApprovalBody({required this.planBody});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    if (planBody.trim().isEmpty) {
      return Text(
        '(no plan body provided)',
        style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
      );
    }
    final textColor = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    return ConstrainedBox(
      constraints: const BoxConstraints(maxHeight: 360),
      child: SingleChildScrollView(
        child: MarkdownBody(
          data: planBody,
          selectable: true,
          shrinkWrap: true,
          styleSheet: MarkdownStyleSheet(
            p: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              height: 1.4,
              color: textColor,
            ),
            code: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: textColor,
            ),
            listBullet: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: textColor,
            ),
          ),
        ),
      ),
    );
  }
}

/// ADR-027 W8: body widget for permission_prompt rows whose
/// dialog_type is "compaction". Surfaces the trigger source (manual /
/// auto) + any custom instructions claude shipped with the
/// PreCompact hook payload so the principal can decide whether to
/// allow the context collapse now or defer.
class _CompactionBody extends StatelessWidget {
  final String trigger;
  final String customInstructions;
  const _CompactionBody({
    required this.trigger,
    required this.customInstructions,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final body = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final triggerLabel = trigger.isEmpty ? '(unspecified)' : trigger;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          'Trigger: $triggerLabel',
          style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
        ),
        if (customInstructions.trim().isNotEmpty) ...[
          const SizedBox(height: 4),
          Text(
            customInstructions,
            style: GoogleFonts.spaceGrotesk(fontSize: 12, color: body),
          ),
        ],
      ],
    );
  }
}

/// Inline selection card for kind=select attention items raised by the
/// agent we're watching. The agent's request_select MCP call long-polls
/// for the user's pick; surfacing this card in the chat keeps the round-
/// trip in one place instead of forcing a trip to the Me page.
class _PendingSelections extends ConsumerWidget {
  const _PendingSelections();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final feed = context.findAncestorStateOfType<_AgentFeedState>();
    final agentId = feed?.widget.agentId ?? '';
    final attention = ref
            .watch(hubProvider.select((s) => s.value?.attention)) ??
        const <Map<String, dynamic>>[];
    final pending = <Map<String, dynamic>>[];
    for (final a in attention) {
      if ((a['kind'] ?? '').toString() != 'select') continue;
      if ((a['status'] ?? '').toString() != 'open') continue;
      final payload = _payloadOf(a);
      if (agentId.isNotEmpty &&
          (payload['agent_id'] ?? '').toString() != agentId) {
        continue;
      }
      pending.add(a);
    }
    if (pending.isEmpty) return const SizedBox.shrink();
    return Material(
      color: Colors.transparent,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final a in pending)
            _SelectionCard(attention: a, payload: _payloadOf(a)),
        ],
      ),
    );
  }

  static Map<String, dynamic> _payloadOf(Map<String, dynamic> attention) {
    final raw = attention['pending_payload'];
    if (raw is Map) return raw.cast<String, dynamic>();
    if (raw is String && raw.isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map) return decoded.cast<String, dynamic>();
      } catch (_) {}
    }
    return const {};
  }
}

class _SelectionCard extends ConsumerStatefulWidget {
  final Map<String, dynamic> attention;
  final Map<String, dynamic> payload;
  const _SelectionCard({required this.attention, required this.payload});

  @override
  ConsumerState<_SelectionCard> createState() => _SelectionCardState();
}

class _SelectionCardState extends ConsumerState<_SelectionCard> {
  bool _sending = false;
  String? _error;
  // Header label is "SELECT" — the action is "pick one of these
  // labelled options", which is sharper than the old generic "decision".

  Future<void> _pick(String? optionId, {String decision = 'approve'}) async {
    final id = (widget.attention['id'] ?? '').toString();
    if (id.isEmpty) return;
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await ref.read(hubProvider.notifier).decide(
            id,
            decision,
            by: '@mobile',
            optionId: optionId,
          );
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed (${e.status})');
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'Decide failed: $e');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final question = (widget.payload['question'] ??
            widget.attention['summary'] ??
            'Selection needed')
        .toString();
    final optionsRaw = widget.payload['options'];
    final options = optionsRaw is List
        ? [for (final v in optionsRaw) v.toString()]
        : const <String>[];
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
      decoration: BoxDecoration(
        color: scheme.surface,
        border: Border.all(
          color: DesignColors.primary.withValues(alpha: 0.6),
          width: 1.5,
        ),
        borderRadius: BorderRadius.circular(10),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withValues(alpha: 0.08),
            blurRadius: 8,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Icon(Icons.help_outline,
                  size: 16, color: DesignColors.primary),
              const SizedBox(width: 6),
              Text(
                'SELECT',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 10,
                  fontWeight: FontWeight.w800,
                  color: DesignColors.primary,
                  letterSpacing: 0.6,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  question,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          if (options.isEmpty)
            Text(
              'No options provided. Tap Approve or Reject below.',
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
            )
          else
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final opt in options)
                  FilledButton.tonal(
                    onPressed: _sending ? null : () => _pick(opt),
                    style: FilledButton.styleFrom(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 6),
                      minimumSize: const Size(0, 32),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Text(opt),
                  ),
              ],
            ),
          if (_error != null) ...[
            const SizedBox(height: 6),
            Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ],
          const SizedBox(height: 8),
          Row(
            children: [
              FilledButton.tonal(
                onPressed:
                    _sending ? null : () => _pick(null, decision: 'reject'),
                style: FilledButton.styleFrom(
                  backgroundColor:
                      DesignColors.error.withValues(alpha: 0.15),
                  foregroundColor: DesignColors.error,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: const Text('Reject'),
              ),
              if (options.isEmpty) ...[
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: _sending ? null : () => _pick(null),
                  style: FilledButton.styleFrom(
                    backgroundColor: DesignColors.success,
                    foregroundColor: Colors.white,
                    padding: const EdgeInsets.symmetric(
                        horizontal: 14, vertical: 6),
                    minimumSize: const Size(0, 32),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: const Text('Approve'),
                ),
              ],
              const Spacer(),
              if (_sending)
                const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Per-event card. The kind drives which fields get a first-class
/// treatment; everything else falls through to a raw JSON block so we
/// stay forward-compatible with new event kinds the hub emits.
class AgentEventCard extends StatefulWidget {
  final Map<String, dynamic> event;
  // tool_use_id → tool name map, built by the Feed from all visible
  // tool_call events so tool_result cards can show the human name
  // instead of a 24-char id. Empty when no context is available.
  final Map<String, String> toolNames;
  // toolCallId → latest tool_call_update payload. The tool_call body
  // pulls status/content from here so progress ticks don't need their
  // own card.
  final Map<String, Map<String, dynamic>> toolUpdates;
  // tool_use_id → matching tool_result event. The tool_call card folds
  // its result inline (lineage cards, W-UI-2) so each call is one
  // expandable surface — pending while no result, success/error once
  // it arrives. Orphaned results (no parent tool_call) are not in this
  // map and still render as standalone cards.
  final Map<String, Map<String, dynamic>> toolResults;
  // request_id → prior decision. Present entries mean the user already
  // answered this approval, so we render the chip but not the buttons.
  final Map<String, String> resolvedApprovals;
  // Needed for the approval card so it can call postAgentInput.
  final String? agentId;
  const AgentEventCard({
    super.key,
    required this.event,
    this.toolNames = const {},
    this.toolUpdates = const {},
    this.toolResults = const {},
    this.resolvedApprovals = const {},
    this.agentId,
  });

  @override
  State<AgentEventCard> createState() => _AgentEventCardState();

  // Builds the clipboard payload for a given card. The principal hits
  // copy on a transcript tile most often to drop content into a bug
  // report, a follow-up prompt to a different agent, or a doc — so
  // prefer the *rendered* content (text, tool args, json body) over
  // the wrapping event metadata. For unknown kinds, fall through to
  // pretty JSON so nothing is silently lost.
  static String _copyTextFor(
    String kind,
    Map<String, dynamic> payload,
    Map<String, dynamic> event,
  ) {
    String s;
    switch (kind) {
      case 'text':
      case 'thought':
        s = (payload['text'] ?? '').toString();
        break;
      case 'tool_call':
        final name = (payload['name'] ?? payload['tool'] ?? 'tool').toString();
        final input = payload['input'] ?? payload['arguments'] ?? payload['args'];
        s = '$name\n${feedJsonPretty(input is Map ? input : payload)}';
        break;
      case 'tool_result':
        final content = payload['content'];
        if (content is String && content.isNotEmpty) {
          s = content;
        } else if (content is Map || content is List) {
          s = feedJsonPretty(content);
        } else {
          s = (payload['text'] ?? feedJsonPretty(payload)).toString();
        }
        break;
      case 'system':
        // System rows usually carry a one-liner; otherwise fall back
        // to the full payload so audit-trail entries copy with their
        // structured fields intact.
        final t = (payload['text'] ?? payload['summary'] ?? '').toString();
        s = t.isNotEmpty ? t : feedJsonPretty(payload);
        break;
      default:
        final t = (payload['text'] ?? '').toString();
        s = t.isNotEmpty ? t : feedJsonPretty(payload);
    }
    return s.isEmpty ? feedJsonPretty(event) : s;
  }

  Widget _body(
    BuildContext ctx,
    String kind,
    String producer,
    Map<String, dynamic> payload,
  ) {
    switch (kind) {
      case 'lifecycle':
        return _lifecycleBody(ctx, payload);
      case 'session.init':
        return _sessionInitBody(ctx, payload);
      case 'text':
      case 'thought':
        return _markdownBody(
          ctx,
          (payload['text'] ?? feedJsonPretty(payload)).toString(),
          isThought: kind == 'thought',
        );
      case 'raw':
        return _rawBody(ctx, payload);
      case 'tool_call':
        return _toolCallBody(ctx, payload);
      case 'tool_call_update':
        return _toolCallUpdateBody(ctx, payload);
      case 'tool_result':
        return _toolResultBody(ctx, payload);
      case 'turn.result':
        return _turnResultBody(ctx, payload);
      case 'completion':
        return _completionBody(ctx, payload);
      case 'error':
        return _errorBody(ctx, payload);
      case 'approval_request':
        return _approvalRequestBody(ctx, payload);
      case 'plan':
        return _planBody(ctx, payload);
      case 'diff':
        return _diffBody(ctx, payload);
      case 'input.text':
        return _inputTextBody(ctx, payload);
      case 'input.cancel':
        return _inputCancelBody(ctx, payload);
      case 'input.approval':
        return _inputApprovalBody(ctx, payload);
      case 'input.attention_reply':
        return _inputAttentionReplyBody(ctx, payload);
      case 'system':
        return _systemBody(ctx, payload);
      default:
        // Any other hub-side kinds — render their text field when present,
        // fall back to pretty JSON otherwise.
        final t = payload['text']?.toString();
        if (t != null && t.isNotEmpty) return _textBody(ctx, t);
        return _textBody(ctx, feedJsonPretty(payload));
    }
  }

  Widget _inputTextBody(BuildContext ctx, Map<String, dynamic> p) {
    // ADR-032: an input.text payload is the message envelope —
    // {from,to,kind,text,cause,thread} at the top level. The body
    // resolves via `text` (legacy `body` kept as a fallback). When the
    // envelope carries a sender / kind, surface them: an A2A message
    // would otherwise render with no visible sender.
    //
    // v1.0.707 polish — `payload.raw == true` marks an
    // engine-control slash command sent without the envelope wrap
    // (e.g. /clear, /compact). For those we suppress the "from /
    // kind" header rows entirely — they'd be misleading (no
    // envelope was attached) and a slash command is self-
    // describing.
    final body = (p['text'] ?? p['body'] ?? '').toString();
    final raw = p['raw'] == true;
    final rows = <Widget>[];
    if (!raw) {
      final from = p['from'];
      final kind = (p['kind'] ?? '').toString();
      final fromLabel = (p['from_label'] ?? '').toString();
      if (from is Map) {
        final role = (from['role'] ?? '').toString();
        final handle = (from['handle'] ?? '').toString();
        final label = envelopeSenderLabel(
          role: role,
          handle: handle,
          fromLabel: fromLabel,
        );
        if (label.isNotEmpty) rows.add(_kv(ctx, 'from', label));
      } else if (fromLabel.isNotEmpty) {
        // Legacy / sparse payload that carries `from_label` without a
        // structured `from` map. Still render the row — the hub-side
        // stamp is the source of truth either way.
        rows.add(_kv(ctx, 'from', fromLabel));
      }
      if (kind.isNotEmpty) rows.add(_kv(ctx, 'kind', kind));
    }
    if (rows.isEmpty) {
      return _mono(ctx, body.isEmpty ? '(empty)' : body);
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        ...rows,
        const SizedBox(height: 4),
        _mono(ctx, body.isEmpty ? '(empty)' : body),
      ],
    );
  }

  Widget _inputCancelBody(BuildContext ctx, Map<String, dynamic> p) {
    final reason = p['reason']?.toString();
    return _mono(
      ctx,
      (reason == null || reason.isEmpty) ? 'cancel' : 'cancel · $reason',
    );
  }

  Widget _inputApprovalBody(BuildContext ctx, Map<String, dynamic> p) {
    final decision = p['decision']?.toString() ?? '?';
    final reqId = p['request_id']?.toString() ?? '';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _kv(ctx, 'decision', decision),
        if (reqId.isNotEmpty) _kv(ctx, 'request_id', reqId),
      ],
    );
  }

  // Renders the principal's reply to a vendor-neutral attention
  // (request_approval / request_select / request_help). The reply is
  // posted by hub /decide as a structured event; the rendered text we
  // build here mirrors `formatAttentionReplyText` in
  // hub/internal/hostrunner/driver_stdio.go — same per-kind shape
  // because the engine sees this exact text as a user turn, and the
  // transcript should match what the agent saw on the wire.
  Widget _inputAttentionReplyBody(BuildContext ctx, Map<String, dynamic> p) {
    final decision = p['decision']?.toString() ?? '?';
    final kind = p['kind']?.toString() ?? '';
    final reqId = p['request_id']?.toString() ?? '';
    final body = p['body']?.toString() ?? '';
    final optionId = p['option_id']?.toString() ?? '';
    final reason = p['reason']?.toString() ?? '';
    final rendered = renderAttentionReplyText(p);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Lead with the literal text the engine receives. Reads like a
        // typed user message — that's the user's mental model when
        // they tap Approve.
        if (rendered.isNotEmpty) _mono(ctx, rendered),
        if (rendered.isNotEmpty) const SizedBox(height: 6),
        _kv(ctx, 'decision', decision),
        if (kind.isNotEmpty) _kv(ctx, 'kind', kind),
        if (optionId.isNotEmpty) _kv(ctx, 'option_id', optionId),
        if (body.isNotEmpty && rendered != body) _kv(ctx, 'reply', body),
        if (reason.isNotEmpty) _kv(ctx, 'reason', reason),
        if (reqId.isNotEmpty) _kv(ctx, 'request_id', reqId),
      ],
    );
  }


  // Compact renderer for non-init `system` frames. claude-code emits
  // these for sub-agent state (task_started / task_updated /
  // task_notification — shape: {subtype, task_id, ...}) and for the
  // occasional engine-level message. Without this case the default
  // branch dumped the full frame JSON, which dominated the transcript
  // every time the agent backgrounded a task. Render a one-liner per
  // frame; fall back to pretty JSON for subtypes we don't model.
  Widget _systemBody(BuildContext ctx, Map<String, dynamic> p) {
    final subtype = (p['subtype'] ?? '').toString();
    final taskId = (p['task_id'] ?? '').toString();
    String? line;
    switch (subtype) {
      case 'task_started':
        // claude usually carries the spawned subagent's name + initial
        // prompt; show whichever is present without pretending a
        // structure we may not have.
        final name = (p['agent'] ?? p['name'] ?? '').toString();
        final desc = (p['description'] ?? p['prompt'] ?? '').toString();
        final head = name.isEmpty ? 'Task started' : 'Task started · $name';
        line = desc.isEmpty ? head : '$head — $desc';
        break;
      case 'task_updated':
        // Surface the patch keys so the user sees *what* changed without
        // dumping the whole envelope (uuid, session_id, parent_uuid).
        final patch = p['patch'];
        if (patch is Map && patch.isNotEmpty) {
          final pairs = patch.entries
              .map((e) => '${e.key}=${e.value}')
              .join(', ');
          line = 'Task updated · $pairs';
        } else {
          line = 'Task updated';
        }
        break;
      case 'task_notification':
        final msg = (p['message'] ?? p['text'] ?? p['notification'] ?? '').toString();
        line = msg.isEmpty ? 'Task notification' : 'Task: $msg';
        break;
    }
    if (line != null) {
      final suffix = taskId.isEmpty ? '' : '  ·  $taskId';
      return _mono(ctx, '$line$suffix');
    }
    // Unknown subtype — keep the legacy JSON dump so nothing is silently
    // hidden, but tag the subtype on top so the user can spot the kind.
    final t = p['text']?.toString();
    if (t != null && t.isNotEmpty) return _textBody(ctx, t);
    return _textBody(ctx, feedJsonPretty(p));
  }

  Widget _lifecycleBody(BuildContext ctx, Map<String, dynamic> p) {
    final phase = p['phase']?.toString() ?? '?';
    final mode = p['mode']?.toString();
    return _mono(
      ctx,
      mode == null ? phase : '$phase · mode=$mode',
    );
  }

  Widget _sessionInitBody(BuildContext ctx, Map<String, dynamic> p) {
    final sid = p['session_id']?.toString() ?? '?';
    final model = p['model']?.toString() ?? '';
    final toolsRaw = p['tools'];
    final tools = toolsRaw is List ? toolsRaw.length : 0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _kv(ctx, 'session', sid),
        if (model.isNotEmpty) _kv(ctx, 'model', model),
        if (tools > 0) _kv(ctx, 'tools', '$tools'),
      ],
    );
  }

  Widget _toolCallBody(BuildContext ctx, Map<String, dynamic> p) {
    final name = p['name']?.toString() ?? '?';
    final id = p['id']?.toString() ?? '';
    final input = p['input'];
    // AskUserQuestion is the only tool whose answer the user has to
    // produce — claude-code emits a tool_call and waits for a
    // tool_result that holds the picked option. Render it inline
    // here instead of falling through to the generic tool_call card,
    // so the user doesn't have to copy-paste the question or watch
    // the agent timeout. Falls back to the standard card if the
    // payload is missing the expected `questions[]` shape.
    if (name == 'AskUserQuestion' &&
        id.isNotEmpty &&
        input is Map &&
        input['questions'] is List) {
      return _AskUserQuestionCard(
        key: ValueKey('ask-uq-$id'),
        agentId: agentId,
        toolUseId: id,
        input: input.cast<String, dynamic>(),
        priorAnswer: id.isNotEmpty ? toolResults[id] : null,
      );
    }
    // Fold the latest tool_call_update so a single card shows the end
    // state (status + optional content preview) without a second row.
    final update = id.isNotEmpty ? toolUpdates[id] : null;
    // Pair with the matching tool_result by tool_use_id (W-UI-2). When
    // present, the call has resolved — derive a terminal status from
    // is_error so the card reads "completed" / "failed" without needing
    // a tool_call_update from drivers that don't emit them.
    final resultEvent = id.isNotEmpty ? toolResults[id] : null;
    final resultPayload = resultEvent != null && resultEvent['payload'] is Map
        ? (resultEvent['payload'] as Map).cast<String, dynamic>()
        : null;
    final hasResult = resultPayload != null;
    final resultIsError = resultPayload?['is_error'] == true;
    final updateStatus = (update?['status'] ?? p['status'] ?? '').toString();
    final status = updateStatus.isNotEmpty
        ? updateStatus
        : (hasResult ? (resultIsError ? 'failed' : 'completed') : 'pending');
    // ACP tool_call_update.content is a list of content blocks; pull the
    // first text block for a compact preview. Larger outputs land in
    // tool_result anyway so this is just for at-a-glance progress.
    String? preview;
    final content = update?['content'];
    if (content is List) {
      for (final b in content) {
        if (b is Map && b['type'] == 'content') {
          final inner = b['content'];
          if (inner is Map && inner['type'] == 'text') {
            preview = inner['text']?.toString();
            break;
          }
        }
      }
    }
    return _FoldableToolCall(
      // Stable identity so toggling fold state survives card rebuilds
      // when new events stream in or the parent setState fires. Without
      // a key the widget would replay its initial _expanded value on
      // every rebuild.
      key: id.isNotEmpty ? ValueKey('tool-fold-$id') : null,
      name: name,
      status: status,
      toolId: id,
      input: input,
      preview: preview,
      resultPayload: resultPayload,
      resultIsError: resultIsError,
    );
  }

  // Verbose-only renderer for ACP tool_call_update wire frames. Folds
  // its data into the parent tool_call card by default; this card is
  // for the rare case the user toggled debug visibility to inspect
  // intermediate states (e.g. confirming the request_approval gate
  // returned its attention payload).
  Widget _toolCallUpdateBody(BuildContext ctx, Map<String, dynamic> p) {
    final id = p['toolCallId']?.toString() ?? '';
    final status = p['status']?.toString() ?? '';
    final title = p['title']?.toString() ?? '';
    String? preview;
    final content = p['content'];
    if (content is List) {
      for (final b in content) {
        if (b is Map && b['type'] == 'content') {
          final inner = b['content'];
          if (inner is Map && inner['type'] == 'text') {
            preview = inner['text']?.toString();
            break;
          }
        }
      }
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (title.isNotEmpty) _kv(ctx, 'tool', title),
        if (status.isNotEmpty) _kv(ctx, 'status', status),
        if (id.isNotEmpty) _kv(ctx, 'tool_call_id', id),
        if (preview != null && preview.isNotEmpty) _mono(ctx, preview),
      ],
    );
  }

  // Verbose-only renderer for turn.result wire frames. Telemetry
  // strip already aggregates these on every turn — the card is for
  // forensic visibility (e.g. seeing stopReason=cancelled when a new
  // attention_reply prompt cancelled the in-flight one).
  Widget _turnResultBody(BuildContext ctx, Map<String, dynamic> p) {
    final status = p['status']?.toString() ?? '';
    final reason = p['stop_reason']?.toString() ?? '';
    final input = p['input_tokens'];
    final output = p['output_tokens'];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (status.isNotEmpty) _kv(ctx, 'status', status),
        if (reason.isNotEmpty) _kv(ctx, 'stop_reason', reason),
        if (input is num) _kv(ctx, 'input_tokens', input.toString()),
        if (output is num) _kv(ctx, 'output_tokens', output.toString()),
      ],
    );
  }

  Widget _toolResultBody(BuildContext ctx, Map<String, dynamic> p) {
    final id = p['tool_use_id']?.toString() ?? '';
    final name = id.isNotEmpty ? toolNames[id] : null;
    final isError = p['is_error'] == true;
    final content = p['content'];
    final text = content is String ? content : feedJsonPretty(content);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (name != null) _kv(ctx, 'tool', name),
        if (id.isNotEmpty) _kv(ctx, 'tool_use_id', id),
        if (isError) _kv(ctx, 'is_error', 'true'),
        CollapsibleMono(
          text: text,
          color: isError ? DesignColors.error : null,
        ),
      ],
    );
  }

  Widget _completionBody(BuildContext ctx, Map<String, dynamic> p) {
    final sub = p['subtype']?.toString();
    final dur = p['duration_ms'];
    final res = p['result']?.toString();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (sub != null && sub.isNotEmpty) _kv(ctx, 'subtype', sub),
        if (dur is num) _kv(ctx, 'duration', _fmtDuration(dur.toInt())),
        if (res != null && res.isNotEmpty) _mono(ctx, res),
      ],
    );
  }

  // duration_ms comes through as a raw integer; "42357" is cognitive
  // load when "42s" reads at a glance. Anything over a minute shows
  // m+s; anything over an hour shows h+m.
  String _fmtDuration(int ms) {
    if (ms < 1000) return '${ms}ms';
    final s = ms ~/ 1000;
    if (s < 60) {
      final tenths = (ms % 1000) ~/ 100;
      return tenths == 0 ? '${s}s' : '$s.${tenths}s';
    }
    if (s < 3600) return '${s ~/ 60}m ${s % 60}s';
    final h = s ~/ 3600;
    final m = (s % 3600) ~/ 60;
    return '${h}h ${m}m';
  }

  Widget _errorBody(BuildContext ctx, Map<String, dynamic> p) {
    final msg = (p['error'] ?? p['message'] ?? feedJsonPretty(p)).toString();
    return _mono(ctx, msg, color: DesignColors.error);
  }

  // ACP plan update: { sessionUpdate: "plan", entries: [{content, priority,
  // status}] }. Render as a compact checklist so the operator can see what
  // the agent is tracking without drilling into raw JSON.
  Widget _planBody(BuildContext ctx, Map<String, dynamic> p) {
    final entriesRaw = p['entries'];
    if (entriesRaw is! List || entriesRaw.isEmpty) {
      return _mono(ctx, feedJsonPretty(p));
    }
    final rows = <Widget>[];
    for (final e in entriesRaw) {
      if (e is! Map) continue;
      final status = (e['status'] ?? '').toString();
      final content = (e['content'] ?? '').toString();
      rows.add(Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(_planStatusIcon(status),
                size: 14, color: _planStatusColor(status)),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                content,
                style: TextStyle(
                  fontSize: 13,
                  decoration: status == 'completed'
                      ? TextDecoration.lineThrough
                      : null,
                  color: status == 'completed'
                      ? DesignColors.textMuted
                      : null,
                ),
              ),
            ),
          ],
        ),
      ));
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: rows,
    );
  }

  static IconData _planStatusIcon(String status) {
    switch (status) {
      case 'completed':
        return Icons.check_circle_outline;
      case 'in_progress':
        return Icons.radio_button_checked;
      case 'pending':
      default:
        return Icons.radio_button_unchecked;
    }
  }

  // ACP diff update: { sessionUpdate: "diff", path, oldText, newText }.
  // Shows the file path + a +N/-N summary + a collapsible unified-diff
  // preview (plain line-by-line comparison; not a real LCS diff).
  Widget _diffBody(BuildContext ctx, Map<String, dynamic> p) {
    final path = (p['path'] ?? '').toString();
    final oldText = (p['oldText'] ?? p['old_text'] ?? '').toString();
    final newText = (p['newText'] ?? p['new_text'] ?? '').toString();
    final oldLines = oldText.isEmpty ? <String>[] : oldText.split('\n');
    final newLines = newText.isEmpty ? <String>[] : newText.split('\n');
    int adds = 0;
    int dels = 0;
    final rows = <_DiffLine>[];
    final maxLen = math.max(oldLines.length, newLines.length);
    for (var i = 0; i < maxLen; i++) {
      final o = i < oldLines.length ? oldLines[i] : null;
      final n = i < newLines.length ? newLines[i] : null;
      if (o == n) {
        rows.add(_DiffLine(kind: _DiffKind.context, text: o ?? ''));
      } else {
        if (o != null) {
          rows.add(_DiffLine(kind: _DiffKind.delete, text: o));
          dels++;
        }
        if (n != null) {
          rows.add(_DiffLine(kind: _DiffKind.insert, text: n));
          adds++;
        }
      }
    }
    final summary = adds > 0 || dels > 0 ? '+$adds / -$dels' : '0 changes';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (path.isNotEmpty) _kv(ctx, 'path', path),
        _kv(ctx, 'change', summary),
        if (rows.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: _DiffView(lines: rows),
          ),
      ],
    );
  }

  static Color _planStatusColor(String status) {
    switch (status) {
      case 'completed':
        return DesignColors.success;
      case 'in_progress':
        return DesignColors.primary;
      case 'pending':
      default:
        return DesignColors.textMuted;
    }
  }

  Widget _approvalRequestBody(BuildContext ctx, Map<String, dynamic> p) {
    final requestId = p['request_id']?.toString() ?? '';
    final params = (p['params'] is Map)
        ? (p['params'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};
    final priorDecision = resolvedApprovals[requestId];
    return _ApprovalCard(
      agentId: agentId,
      requestId: requestId,
      params: params,
      priorDecision: priorDecision,
    );
  }

  Widget _textBody(BuildContext ctx, String s) => _mono(ctx, s);

  // `raw` covers three shapes from driver_acp.go:
  //   {"text": "..."}                         — scanner/unmarshal failure
  //   {"method": "x", "params": ...}          — unknown JSON-RPC notification
  //   {"sessionUpdate": "x", ...}             — unhandled session/update kind
  // Show the identifying field at the top so an unknown frame is legible
  // at a glance; hide the rest behind CollapsibleMono.
  Widget _rawBody(BuildContext ctx, Map<String, dynamic> p) {
    final text = p['text']?.toString();
    if (text != null && text.isNotEmpty && p.length == 1) {
      return _mono(ctx, text);
    }
    final method = p['method']?.toString();
    final sessionUpdate = p['sessionUpdate']?.toString();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (method != null && method.isNotEmpty) _kv(ctx, 'method', method),
        if (sessionUpdate != null && sessionUpdate.isNotEmpty)
          _kv(ctx, 'update', sessionUpdate),
        CollapsibleMono(text: feedJsonPretty(p)),
      ],
    );
  }

  // Agents (Claude Code especially) emit markdown heavily — bullet lists,
  // fenced code blocks, headers. Rendering as plain mono text buries the
  // structure; rendering with a tight style sheet keeps the card compact
  // while still reading like the agent's terminal output.
  Widget _markdownBody(BuildContext ctx, String s, {bool isThought = false}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final textColor = isThought
        ? (isDark ? DesignColors.textMuted : DesignColors.textMutedLight)
        : (isDark ? DesignColors.textPrimary : DesignColors.textPrimaryLight);
    final codeBg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final codeBorder = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final base = GoogleFonts.spaceGrotesk(
      fontSize: 13,
      height: 1.35,
      color: textColor,
      fontStyle: isThought ? FontStyle.italic : FontStyle.normal,
    );
    final codeStyle = GoogleFonts.jetBrainsMono(
      fontSize: 11,
      height: 1.35,
      color: textColor,
    );
    return MarkdownBody(
      data: normalizeMultilineMath(s),
      selectable: true,
      shrinkWrap: true,
      // Tap on `[text](href)` opens the URL in the system browser.
      // Underline + primary color come from styleSheet.a below; we
      // intentionally don't register a custom 'a' element builder,
      // because flutter_markdown appends the builder's widget *after*
      // the default styled inline span — registering one renders the
      // visible label twice (once colored-underlined, once tappable).
      onTapLink: (text, href, title) => openMarkdownLink(ctx, href),
      builders: {
        'code': HighlightedCodeBuilder(isDark: isDark),
        // KaTeX-style LaTeX math. Two flavors of the same builder so
        // the markdown parser can route inline ($...$) and display
        // ($$...$$) at different vertical sizes/alignment.
        'math': MathBuilder(isDark: isDark, display: false),
        'mathblock': MathBuilder(isDark: isDark, display: true),
      },
      // Custom inline syntaxes only — no BlockSyntax. The preprocessor
      // (normalizeMultilineMath) collapses well-formed multi-line
      // $$...$$ and \[...\] regions into single-line $$...$$ before
      // we get here; unbalanced delimiters fall through to plain text.
      // Order matters: $$...$$ must be tried before $...$ or the
      // parser will eat the leading $$ as two empty $$s; same for
      // \[...\] vs \(...\).
      extensionSet: md.ExtensionSet(
        md.ExtensionSet.gitHubFlavored.blockSyntaxes,
        [
          MathBlockInlineSyntax(),
          MathInlineSyntax(),
          LatexBracketDisplayInlineSyntax(),
          LatexBracketInlineSyntax(),
          ...md.ExtensionSet.gitHubFlavored.inlineSyntaxes,
        ],
      ),
      // Keep paragraph and block spacing tight so cards don't balloon.
      styleSheet: MarkdownStyleSheet(
        p: base,
        a: base.copyWith(
          color: DesignColors.primary,
          decoration: TextDecoration.underline,
          decorationColor: DesignColors.primary.withValues(alpha: 0.4),
        ),
        strong: base.copyWith(fontWeight: FontWeight.w700),
        em: base.copyWith(fontStyle: FontStyle.italic),
        code: codeStyle,
        codeblockPadding: const EdgeInsets.all(8),
        codeblockDecoration: BoxDecoration(
          color: codeBg,
          borderRadius: BorderRadius.circular(4),
          border: Border.all(color: codeBorder),
        ),
        h1: base.copyWith(fontSize: 16, fontWeight: FontWeight.w700),
        h2: base.copyWith(fontSize: 15, fontWeight: FontWeight.w700),
        h3: base.copyWith(fontSize: 14, fontWeight: FontWeight.w700),
        h4: base.copyWith(fontSize: 13, fontWeight: FontWeight.w700),
        blockquote: base.copyWith(
          color: isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight,
        ),
        blockquoteDecoration: BoxDecoration(
          border: Border(
            left: BorderSide(color: codeBorder, width: 3),
          ),
        ),
        blockquotePadding: const EdgeInsets.only(left: 8),
        listBullet: base,
        tableHead: base.copyWith(fontWeight: FontWeight.w700),
        tableBody: base,
        pPadding: const EdgeInsets.only(bottom: 2),
      ),
    );
  }

  // Tool-name → glyph map for the tool_call card header strip. Keeps the
  // transcript scannable: a wall of identical "tool_call" labels reads
  // like noise; an icon per tool ("Bash → terminal", "Edit → pencil",
  // "Read → eye") makes each card immediately identifiable. Unknown
  // names fall through to a generic build glyph.
  static IconData toolIconFor(String name) {
    switch (name) {
      case 'Bash':
      case 'BashOutput':
        return Icons.terminal;
      case 'KillBash':
        return Icons.cancel_outlined;
      case 'Edit':
      case 'MultiEdit':
        return Icons.edit_outlined;
      case 'Write':
        return Icons.note_add_outlined;
      case 'Read':
        return Icons.description_outlined;
      case 'NotebookEdit':
      case 'NotebookRead':
        return Icons.menu_book_outlined;
      case 'Glob':
        return Icons.folder_open_outlined;
      case 'Grep':
        return Icons.search;
      case 'WebFetch':
        return Icons.public;
      case 'WebSearch':
        return Icons.travel_explore;
      case 'Task':
        return Icons.alt_route;
      case 'TodoWrite':
        return Icons.checklist;
      case 'AskUserQuestion':
        return Icons.help_outline;
      case 'ExitPlanMode':
        return Icons.flag_outlined;
      case 'SlashCommand':
        return Icons.terminal_outlined;
    }
    if (name.startsWith('mcp__termipod__')) {
      return Icons.hub_outlined; // hub-side MCP tool
    }
    if (name.startsWith('mcp__')) {
      return Icons.api;
    }
    // Authority-surface tools (projects.list, agents.spawn, schedules.run, …)
    // arrive un-namespaced when the steward calls them through the in-process
    // MCP. Pick a glyph that signals "hub authority" so they're distinct from
    // engine-local tools above.
    if (name.contains('.')) return Icons.hub_outlined;
    return Icons.build_circle_outlined;
  }

  Widget _kv(BuildContext ctx, String k, String v, {Color? valueColor}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: RichText(
        text: TextSpan(
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            color: isDark
                ? DesignColors.textSecondary
                : DesignColors.textSecondaryLight,
          ),
          children: [
            TextSpan(
              text: '$k: ',
              style: TextStyle(
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
            TextSpan(
              text: v,
              style: valueColor == null ? null : TextStyle(color: valueColor),
            ),
          ],
        ),
      ),
    );
  }

  Widget _mono(BuildContext ctx, String s, {Color? color}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    return SelectableText(
      s,
      style: GoogleFonts.jetBrainsMono(
        fontSize: 11,
        color: color ??
            (isDark
                ? DesignColors.textPrimary
                : DesignColors.textPrimaryLight),
      ),
    );
  }

  static Color _accentFor(String kind, String producer) {
    switch (kind) {
      case 'text':
      case 'thought':
        return DesignColors.primary;
      case 'tool_call':
        return DesignColors.terminalBlue;
      case 'tool_result':
        return DesignColors.terminalCyan;
      case 'completion':
        return DesignColors.success;
      case 'error':
        return DesignColors.error;
      case 'lifecycle':
        return DesignColors.warning;
      case 'session.init':
        return DesignColors.secondary;
      case 'approval_request':
        return DesignColors.warning;
      case 'plan':
        return DesignColors.secondary;
      case 'diff':
        return DesignColors.terminalCyan;
      default:
        return producer == 'user'
            ? DesignColors.terminalYellow
            : DesignColors.textMuted;
    }
  }
}

class _AgentEventCardState extends State<AgentEventCard> {
  // Per-card collapse toggle. Default-expanded for every kind so the
  // existing transcript shape is preserved on first render; the user
  // chooses what to fold. Mounted state lives on the State, so the
  // sliver's keyed widgets keep collapsed rows collapsed across
  // scroll-and-back.
  //
  // v1.0.706 polish — orphan tool_result cards (no matching parent
  // tool_call in scope, so they aren't already folded INTO the
  // parent card) default to collapsed. They're noisy by nature
  // (long Bash output, file dumps) and the user is usually scanning
  // for text turns. Failed results auto-expand so the error is
  // visible without an extra tap.
  late bool _collapsed = _defaultCollapsedForKind();

  bool _defaultCollapsedForKind() {
    final kind = (widget.event['kind'] ?? '').toString();
    if (kind != 'tool_result') return false;
    final p = widget.event['payload'];
    if (p is Map && p['is_error'] == true) return false; // keep errors visible
    return true;
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (widget.event['kind'] ?? '').toString();
    final producer = (widget.event['producer'] ?? 'agent').toString();
    final payload = (widget.event['payload'] is Map)
        ? (widget.event['payload'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};

    final accent = AgentEventCard._accentFor(kind, producer);
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;

    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      padding: const EdgeInsets.fromLTRB(10, 8, 10, 10),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _CardHeader(
            kind: kind,
            producer: producer,
            accent: accent,
            ts: widget.event['ts']?.toString(),
            copyText: AgentEventCard._copyTextFor(kind, payload, widget.event),
            collapsed: _collapsed,
            onToggleCollapsed: () =>
                setState(() => _collapsed = !_collapsed),
          ),
          const SizedBox(height: 6),
          if (_collapsed)
            _collapsedPreview(context, kind, payload)
          else
            widget._body(context, kind, producer, payload),
        ],
      ),
    );
  }

  // Single-line preview rendered in place of the body when collapsed.
  // Uses the same source string as the copy affordance so what the user
  // sees in the preview is what they'd get on copy — no surprise.
  Widget _collapsedPreview(
    BuildContext ctx,
    String kind,
    Map<String, dynamic> payload,
  ) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final raw = AgentEventCard._copyTextFor(kind, payload, widget.event);
    final firstLine = () {
      final nl = raw.indexOf('\n');
      return nl == -1 ? raw : raw.substring(0, nl);
    }();
    final more = raw.length > firstLine.length;
    final text = firstLine.isEmpty ? '(empty)' : firstLine;
    return Text(
      more ? '$text  …' : text,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      style: GoogleFonts.jetBrainsMono(
        fontSize: 11,
        color: muted,
      ),
    );
  }
}


class _CardHeader extends StatelessWidget {
  final String kind;
  final String producer;
  final Color accent;
  final String? ts;
  // Pre-computed clipboard text for this card. Empty disables the
  // copy affordance entirely (e.g. internal placeholders we don't
  // want operators dumping into bug reports).
  final String copyText;
  // Per-card collapse state, hoisted from AgentEventCardState. The
  // chevron rotates and tapping the header (anywhere in the row, not
  // just the chevron) toggles. Both are nullable so the header can
  // still be used by a non-collapsible owner if a future caller
  // wants the same visual without the affordance.
  final bool? collapsed;
  final VoidCallback? onToggleCollapsed;
  const _CardHeader({
    required this.kind,
    required this.producer,
    required this.accent,
    required this.ts,
    this.copyText = '',
    this.collapsed,
    this.onToggleCollapsed,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final hasToggle = onToggleCollapsed != null;
    final row = Row(
      children: [
        Container(
          width: 6,
          height: 6,
          decoration: BoxDecoration(
            color: accent,
            borderRadius: BorderRadius.circular(3),
          ),
        ),
        const SizedBox(width: 6),
        Text(
          kind,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w700,
            color: accent,
          ),
        ),
        const SizedBox(width: 6),
        Text(
          producer,
          style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
        ),
        const Spacer(),
        if (ts != null)
          Text(
            _formatTs(ts!),
            style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
          ),
        if (copyText.isNotEmpty) ...[
          const SizedBox(width: 4),
          // Compact copy affordance — small enough to not crowd the
          // header row, large enough to hit on mobile. Tapping copies
          // the pre-computed text and surfaces a SnackBar receipt so
          // the principal knows the action took.
          InkResponse(
            radius: 14,
            onTap: () => _copy(context),
            child: Padding(
              padding: const EdgeInsets.all(2),
              child: Icon(
                Icons.copy_outlined,
                size: 14,
                color: muted,
              ),
            ),
          ),
        ],
        if (hasToggle) ...[
          const SizedBox(width: 4),
          InkResponse(
            radius: 14,
            onTap: onToggleCollapsed,
            child: Padding(
              padding: const EdgeInsets.all(2),
              child: Icon(
                (collapsed ?? false)
                    ? Icons.unfold_more
                    : Icons.unfold_less,
                size: 14,
                color: muted,
              ),
            ),
          ),
        ],
      ],
    );
    if (!hasToggle) return row;
    // Make the whole header row a tap target so users don't have to aim
    // for the chevron — much friendlier on mobile thumbs. The copy and
    // chevron InkResponses above sit on top of this and stop propagation
    // by virtue of their own onTap callbacks.
    return InkWell(
      onTap: onToggleCollapsed,
      borderRadius: BorderRadius.circular(4),
      child: row,
    );
  }

  Future<void> _copy(BuildContext context) async {
    await Clipboard.setData(ClipboardData(text: copyText));
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'Copied ${kind.isEmpty ? "tile" : kind}',
            style: GoogleFonts.jetBrainsMono(fontSize: 12),
          ),
          duration: const Duration(seconds: 1),
          behavior: SnackBarBehavior.floating,
        ),
      );
    }
  }

  static String _formatTs(String iso) {
    final dt = DateTime.tryParse(iso);
    if (dt == null) return iso;
    final local = dt.toLocal();
    String two(int n) => n < 10 ? '0$n' : '$n';
    return '${two(local.hour)}:${two(local.minute)}:${two(local.second)}';
  }
}

/// Interactive approval card rendered for agent_events of kind
/// `approval_request`. Buttons come from the payload's options list;
/// tapping posts an input.approval back to the hub, which the
/// InputRouter then forwards to ACPDriver.Input → JSON-RPC response.
/// Once answered, the card collapses to a decision chip so reopening
/// the feed doesn't show the buttons again.
class _ApprovalCard extends ConsumerStatefulWidget {
  final String? agentId;
  final String requestId;
  final Map<String, dynamic> params;
  final String? priorDecision;
  const _ApprovalCard({
    required this.agentId,
    required this.requestId,
    required this.params,
    required this.priorDecision,
  });

  @override
  ConsumerState<_ApprovalCard> createState() => _ApprovalCardState();
}

class _ApprovalCardState extends ConsumerState<_ApprovalCard> {
  bool _sending = false;
  String? _error;
  String? _localDecision;

  String? get _effectiveDecision => _localDecision ?? widget.priorDecision;

  Future<void> _send(String decision, {String? optionId}) async {
    final agentId = widget.agentId;
    if (agentId == null || widget.requestId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.postAgentInput(
        agentId,
        kind: 'approval',
        requestId: widget.requestId,
        decision: decision,
        optionId: optionId,
      );
      if (!mounted) return;
      setState(() {
        _sending = false;
        _localDecision = decision;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed (${e.status})';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final toolCall = widget.params['toolCall'];
    String? toolSummary;
    if (toolCall is Map) {
      final name = toolCall['name']?.toString();
      if (name != null && name.isNotEmpty) toolSummary = name;
    }
    // Options may arrive as a list of {optionId, name} maps. Fall back to
    // a hard-coded allow/deny pair so the card still works with agents
    // that skip the options block.
    final rawOptions = widget.params['options'];
    final options = <_ApprovalOption>[];
    if (rawOptions is List) {
      for (final o in rawOptions) {
        if (o is Map) {
          final id = o['optionId']?.toString() ?? o['id']?.toString() ?? '';
          final label = o['name']?.toString() ?? o['label']?.toString() ?? id;
          if (id.isNotEmpty) {
            options.add(_ApprovalOption(id: id, label: label));
          }
        }
      }
    }
    if (options.isEmpty) {
      options.addAll(const [
        _ApprovalOption(id: 'allow', label: 'Allow'),
        _ApprovalOption(id: 'deny', label: 'Deny'),
      ]);
    }

    final decided = _effectiveDecision;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (toolSummary != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 6),
            child: RichText(
              text: TextSpan(
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textSecondary
                      : DesignColors.textSecondaryLight,
                ),
                children: [
                  TextSpan(
                      text: 'tool: ',
                      style: TextStyle(color: muted)),
                  TextSpan(text: toolSummary),
                ],
              ),
            ),
          ),
        if (widget.params.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: CollapsibleMono(
              text: feedJsonPretty(widget.params),
            ),
          ),
        if (decided != null)
          _DecisionChip(decision: decided)
        else
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              for (final o in options)
                FilledButton(
                  onPressed: _sending ? null : () => _send(o.id, optionId: o.id),
                  style: FilledButton.styleFrom(
                    backgroundColor: o.id == 'allow'
                        ? DesignColors.success
                        : (o.id == 'deny'
                            ? DesignColors.error
                            : DesignColors.primary),
                    padding: const EdgeInsets.symmetric(
                        horizontal: 12, vertical: 6),
                    minimumSize: const Size(0, 32),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  child: Text(
                    o.label,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
              OutlinedButton(
                onPressed: _sending ? null : () => _send('cancel'),
                style: OutlinedButton.styleFrom(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 12, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(
                  'Cancel',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    color: muted,
                  ),
                ),
              ),
            ],
          ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ),
      ],
    );
  }
}

class _ApprovalOption {
  final String id;
  final String label;
  const _ApprovalOption({required this.id, required this.label});
}

class _DecisionChip extends StatelessWidget {
  final String decision;
  const _DecisionChip({required this.decision});

  @override
  Widget build(BuildContext context) {
    final color = switch (decision) {
      'allow' => DesignColors.success,
      'deny' => DesignColors.error,
      'cancel' => DesignColors.textMuted,
      _ => DesignColors.primary,
    };
    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.15),
          borderRadius: BorderRadius.circular(4),
          border: Border.all(color: color.withValues(alpha: 0.5)),
        ),
        child: Text(
          'decided: $decision',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: color,
          ),
        ),
      ),
    );
  }
}

/// Inline interactive card for `AskUserQuestion` tool calls. The
/// claude-code agent emits a tool_call whose input carries a list of
/// `questions[].options[]`; we render the question + options as
/// buttons here so the user can answer in-flow instead of waiting for
/// the agent to time out (which it does noisily — the user reported
/// "looks like the question prompt was canceled" after a missed
/// reply). Tap → POST input.answer with the chosen option as the
/// body; the hostrunner's stdio driver wraps it in a tool_result with
/// the matching tool_use_id and ships it back to claude-code on
/// stdin.
///
/// Multi-question payloads are technically allowed by the SDK but
/// rare in practice — we render the first question and treat the
/// rest as fallback (their bodies appear in a small JSON dump). We
/// can iterate on multi-question UX once a real example shows up.
class _AskUserQuestionCard extends ConsumerStatefulWidget {
  final String? agentId;
  final String toolUseId;
  final Map<String, dynamic> input;
  final Map<String, dynamic>? priorAnswer;
  const _AskUserQuestionCard({
    super.key,
    required this.agentId,
    required this.toolUseId,
    required this.input,
    required this.priorAnswer,
  });

  @override
  ConsumerState<_AskUserQuestionCard> createState() =>
      _AskUserQuestionCardState();
}

class _AskUserQuestionCardState extends ConsumerState<_AskUserQuestionCard> {
  bool _sending = false;
  String? _error;
  String? _localAnswer;

  String? get _effectiveAnswer {
    if (_localAnswer != null) return _localAnswer;
    final prior = widget.priorAnswer;
    if (prior == null) return null;
    final payload = prior['payload'];
    if (payload is Map) {
      final c = payload['content'];
      if (c is String && c.isNotEmpty) return c;
    }
    return null;
  }

  Future<void> _send(String label) async {
    final agentId = widget.agentId;
    if (agentId == null || agentId.isEmpty || widget.toolUseId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() => _error = 'Not connected');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await client.postAgentInput(
        agentId,
        kind: 'answer',
        requestId: widget.toolUseId,
        body: label,
      );
      if (!mounted) return;
      setState(() {
        _sending = false;
        _localAnswer = label;
      });
    } on HubApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed (${e.status})';
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = 'Send failed: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final questions = widget.input['questions'];
    Map<String, dynamic>? primary;
    if (questions is List && questions.isNotEmpty) {
      final q = questions.first;
      if (q is Map) primary = q.cast<String, dynamic>();
    }
    if (primary == null) {
      // Defensive fallback: payload didn't match the expected shape.
      // Render the raw input so nothing is silently hidden.
      return CollapsibleMono(text: feedJsonPretty(widget.input));
    }
    final header = (primary['header'] ?? '').toString();
    final question = (primary['question'] ?? '').toString();
    final rawOptions = primary['options'];
    final options = <_AskOption>[];
    if (rawOptions is List) {
      for (final o in rawOptions) {
        if (o is Map) {
          final label = (o['label'] ?? '').toString();
          if (label.isEmpty) continue;
          options.add(_AskOption(
            label: label,
            description: (o['description'] ?? '').toString(),
          ));
        }
      }
    }
    final answered = _effectiveAnswer;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (header.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Text(
              header,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                color: muted,
                letterSpacing: 0.4,
              ),
            ),
          ),
        if (question.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Text(
              question,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        if (answered != null)
          Align(
            alignment: Alignment.centerLeft,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                color: DesignColors.success.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
                border: Border.all(
                    color: DesignColors.success.withValues(alpha: 0.5)),
              ),
              child: Text(
                'answered: $answered',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.success,
                ),
              ),
            ),
          )
        else if (options.isEmpty)
          Text(
            '(no options provided)',
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
          )
        else
          Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (final o in options)
                Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: OutlinedButton(
                    onPressed: _sending ? null : () => _send(o.label),
                    style: OutlinedButton.styleFrom(
                      alignment: Alignment.centerLeft,
                      padding: const EdgeInsets.symmetric(
                          horizontal: 12, vertical: 8),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(
                          o.label,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        if (o.description.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.only(top: 2),
                            child: Text(
                              o.description,
                              style: GoogleFonts.spaceGrotesk(
                                fontSize: 11,
                                color: muted,
                              ),
                            ),
                          ),
                      ],
                    ),
                  ),
                ),
            ],
          ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              _error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error),
            ),
          ),
      ],
    );
  }
}

class _AskOption {
  final String label;
  final String description;
  const _AskOption({required this.label, required this.description});
}

/// Tool-call card body with a manual fold control. The body is
/// expanded by default — this matches the prior behavior so the user
/// doesn't lose at-a-glance context — but the user can tap the
/// chevron in the name row to collapse the card down to just the
/// tool name + status pill. Useful for noisy multi-step calls where
/// the input or result body is mostly screen-filling JSON.
class _FoldableToolCall extends StatefulWidget {
  final String name;
  final String status;
  final String toolId;
  final Object? input;
  final String? preview;
  final Map<String, dynamic>? resultPayload;
  final bool resultIsError;
  const _FoldableToolCall({
    super.key,
    required this.name,
    required this.status,
    required this.toolId,
    required this.input,
    required this.preview,
    required this.resultPayload,
    required this.resultIsError,
  });

  @override
  State<_FoldableToolCall> createState() => _FoldableToolCallState();
}

class _FoldableToolCallState extends State<_FoldableToolCall> {
  // Collapsed by default — tool-call args + result preview eat the
  // whole transcript otherwise, and the user is usually scanning for
  // text turns, not tool internals. Auto-expand for failed calls so
  // an error stays visible without an extra tap.
  late bool _expanded = widget.resultIsError;

  @override
  void didUpdateWidget(covariant _FoldableToolCall old) {
    super.didUpdateWidget(old);
    // If a result lands later and it's an error, pop the card open
    // so the user notices. Don't fight a manual collapse — only
    // auto-expand on the *transition* into error state.
    if (widget.resultIsError && !old.resultIsError && !_expanded) {
      _expanded = true;
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final hasResult = widget.resultPayload != null;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 4),
          child: Row(
            children: [
              Icon(AgentEventCard.toolIconFor(widget.name), size: 14, color: muted),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  widget.name,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: fg,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (widget.status.isNotEmpty) _StatusPill(status: widget.status),
              const SizedBox(width: 4),
              InkWell(
                onTap: () => setState(() => _expanded = !_expanded),
                borderRadius: BorderRadius.circular(12),
                child: Padding(
                  padding: const EdgeInsets.all(4),
                  child: Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 16,
                    color: muted,
                  ),
                ),
              ),
            ],
          ),
        ),
        if (_expanded) ...[
          if (widget.toolId.isNotEmpty)
            _ToolKvLine(label: 'id', value: widget.toolId),
          if (widget.input != null)
            CollapsibleMono(text: feedJsonPretty(widget.input)),
          if (widget.preview != null && widget.preview!.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: CollapsibleMono(text: widget.preview!),
            ),
          if (hasResult)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: _ToolResultInline(
                payload: widget.resultPayload!,
                isError: widget.resultIsError,
              ),
            ),
        ],
      ],
    );
  }
}

/// A single label:value line for the foldable tool-call header. Mirrors
/// the parent card's `_kv` formatting without depending on its private
/// instance method.
class _ToolKvLine extends StatelessWidget {
  final String label;
  final String value;
  const _ToolKvLine({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 1),
      child: RichText(
        text: TextSpan(
          children: [
            TextSpan(
              text: '$label: ',
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
            ),
            TextSpan(
              text: value,
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: fg),
            ),
          ],
        ),
      ),
    );
  }
}


enum _DiffKind { context, insert, delete }

class _DiffLine {
  final _DiffKind kind;
  final String text;
  const _DiffLine({required this.kind, required this.text});
}

/// Color-coded diff view for the `diff` event-card body. Each line
/// renders with a green / red / neutral background — the green-on-add /
/// red-on-delete convention matches what every code review tool uses,
/// so the operator doesn't have to read prefixes (+/-) to parse the
/// change. A line-number gutter on the left reinforces ordering for
/// long diffs.
class _DiffView extends StatelessWidget {
  final List<_DiffLine> lines;
  const _DiffView({required this.lines});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final mono = GoogleFonts.jetBrainsMono(
      fontSize: 11,
      height: 1.35,
      color: isDark
          ? DesignColors.textPrimary
          : DesignColors.textPrimaryLight,
    );
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final addBg = (isDark
            ? DesignColors.success
            : DesignColors.success)
        .withValues(alpha: isDark ? 0.18 : 0.14);
    final delBg = DesignColors.error.withValues(alpha: isDark ? 0.18 : 0.12);
    final ctxBg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;

    final children = <Widget>[];
    for (var i = 0; i < lines.length; i++) {
      final l = lines[i];
      final bg = switch (l.kind) {
        _DiffKind.insert => addBg,
        _DiffKind.delete => delBg,
        _DiffKind.context => ctxBg,
      };
      final marker = switch (l.kind) {
        _DiffKind.insert => '+',
        _DiffKind.delete => '-',
        _DiffKind.context => ' ',
      };
      final markerColor = switch (l.kind) {
        _DiffKind.insert => DesignColors.success,
        _DiffKind.delete => DesignColors.error,
        _DiffKind.context => mutedColor,
      };
      children.add(Container(
        color: bg,
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 24,
              child: Text(
                '${i + 1}',
                textAlign: TextAlign.right,
                style: mono.copyWith(color: mutedColor),
              ),
            ),
            const SizedBox(width: 6),
            SizedBox(
              width: 12,
              child: Text(
                marker,
                style: mono.copyWith(
                  color: markerColor,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            Expanded(
              child: Text(
                l.text.isEmpty ? ' ' : l.text,
                style: mono,
                softWrap: true,
              ),
            ),
          ],
        ),
      ));
    }
    return Container(
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: border),
      ),
      clipBehavior: Clip.hardEdge,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: children,
      ),
    );
  }
}

/// Compact status pill for the tool_call card header.
/// pending/in_progress/completed/failed each get their own accent so
/// the user can scan a long transcript without reading every label.
class _StatusPill extends StatelessWidget {
  final String status;
  const _StatusPill({required this.status});

  @override
  Widget build(BuildContext context) {
    final color = switch (status) {
      'failed' => DesignColors.error,
      'completed' => DesignColors.success,
      'in_progress' => DesignColors.terminalCyan,
      'pending' => DesignColors.warning,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        status,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

/// Inline tool_result block rendered inside a tool_call card. Reuses
/// the same collapsible-mono content rendering as the standalone
/// tool_result card, but framed with a left-rail accent so the lineage
/// from input → output reads at a glance.
///
/// v1.0.706 polish — the result body itself is folded behind a
/// "result · N lines" header by default; tapping the header expands.
/// Errors auto-expand so the diagnostic is visible without an extra
/// tap. Same fold contract as `_AgentEventCardState` for orphan
/// tool_result cards.
class _ToolResultInline extends StatefulWidget {
  final Map<String, dynamic> payload;
  final bool isError;
  const _ToolResultInline({required this.payload, required this.isError});

  @override
  State<_ToolResultInline> createState() => _ToolResultInlineState();
}

class _ToolResultInlineState extends State<_ToolResultInline> {
  // Default folded for non-error results. Errors auto-expand so the
  // diagnostic stays visible without a tap.
  late bool _expanded = widget.isError;

  @override
  void didUpdateWidget(covariant _ToolResultInline old) {
    super.didUpdateWidget(old);
    // If the result flips to error after first render (e.g. a
    // tool_call update streams in), auto-expand. Same as
    // _FoldableToolCallState pattern.
    if (widget.isError && !old.isError && !_expanded) {
      _expanded = true;
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final accent =
        widget.isError ? DesignColors.error : DesignColors.success;
    final content = widget.payload['content'];
    final text = content is String ? content : feedJsonPretty(content);
    // Pre-compute the line count so the header can advertise "N
    // lines" before any expansion. Cheap — content payloads max at
    // ~16KB on the wire in practice; the split is O(N) once.
    final lineCount = text.isEmpty ? 0 : '\n'.allMatches(text).length + 1;
    return Container(
      decoration: BoxDecoration(
        border: Border(left: BorderSide(color: accent, width: 2)),
      ),
      padding: const EdgeInsets.fromLTRB(8, 4, 0, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Header row also acts as the fold control. InkWell so the
          // tap target is the entire strip, not just the chevron.
          InkWell(
            onTap: () => setState(() => _expanded = !_expanded),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 2),
              child: Row(
                children: [
                  Icon(
                    widget.isError
                        ? Icons.error_outline
                        : Icons.check_circle_outline,
                    size: 12,
                    color: accent,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    widget.isError ? 'result · error' : 'result',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      color: mutedColor,
                      letterSpacing: 0.5,
                    ),
                  ),
                  if (lineCount > 0) ...[
                    const SizedBox(width: 6),
                    Text(
                      '· ${lineCount} line${lineCount == 1 ? '' : 's'}',
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: mutedColor,
                      ),
                    ),
                  ],
                  const Spacer(),
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 14,
                    color: mutedColor,
                  ),
                ],
              ),
            ),
          ),
          if (_expanded) ...[
            const SizedBox(height: 4),
            CollapsibleMono(
              text: text,
              color: widget.isError ? DesignColors.error : null,
            ),
          ],
        ],
      ),
    );
  }
}


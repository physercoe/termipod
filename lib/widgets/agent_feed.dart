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
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:markdown/markdown.dart' as md;

import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';
import 'agent_compose.dart';

/// True when the event payload carries the `replay: true` flag the M1
/// driver stamps on session/update notifications streamed inside a
/// `session/load` window (ADR-021 W1.2). Used by the feed's ingest
/// filter (W1.3) to drop frames whose content already appears in the
/// cached transcript so the user doesn't see every prior turn twice
/// after a resume.
@visibleForTesting
bool agentEventIsReplay(Map<String, dynamic> evt) {
  final p = evt['payload'];
  if (p is! Map) return false;
  return p['replay'] == true;
}

/// Computes a content-stable dedupe key for an agent_event. The key
/// must be the same for a freshly-streamed replay frame and the
/// originally-streamed live frame, so we can identify equivalence
/// across agent_id and seq (those differ between the dead agent that
/// produced the original event and the resumed agent re-emitting it
/// during session/load replay). Returns null for events whose shape
/// has no stable identity — those are passed through (better duplicate
/// than dropped).
///
/// Keying by kind:
///   text / thought    → kind + length-prefixed text body. Length
///                       prefix prevents prefix-collision (turn 1's
///                       "hello" colliding with turn 2's "hello world"
///                       once both have grown).
///   tool_call         → kind + tool_call_id (agent-stable across
///                       restart for the same logical call).
///   tool_call_update  → kind + tool_call_id + status (status carries
///                       the lifecycle position so multiple updates
///                       per call don't collapse into one).
///   approval_request  → kind + request_id.
@visibleForTesting
String? agentEventReplayKey(Map<String, dynamic> evt) {
  final kind = (evt['kind'] ?? '').toString();
  final raw = evt['payload'];
  if (raw is! Map) return null;
  final payload = raw.cast<String, dynamic>();
  switch (kind) {
    case 'text':
    case 'thought':
      final text = (payload['text'] ?? '').toString();
      if (text.isEmpty) return null;
      return '$kind:${text.length}:$text';
    case 'tool_call':
      final id = (payload['id'] ?? payload['toolCallId'] ?? '').toString();
      if (id.isEmpty) return null;
      return '$kind:$id';
    case 'tool_call_update':
      final id = (payload['toolCallId'] ?? payload['id'] ?? '').toString();
      final status = (payload['status'] ?? '').toString();
      if (id.isEmpty) return null;
      return '$kind:$id:$status';
    case 'approval_request':
      final id = (payload['request_id'] ?? '').toString();
      if (id.isEmpty) return null;
      return '$kind:$id';
  }
  return null;
}

/// ADR-021 W2.5 — extract the latest mode + model state advertised by
/// the agent from a list of agent_events (newest-last). Walks events
/// in reverse for the most recent `current_mode_update` /
/// `current_model_update` system notifications (gemini ACP shape) and
/// returns a `(currentMode, availableModes, currentModel,
/// availableModels)` tuple as a plain map so test fixtures don't have
/// to construct private types.
///
/// Returns null when neither a mode nor a model has been advertised —
/// the strip widget hides itself in that case.
@visibleForTesting
Map<String, dynamic>? modeModelStateFromEvents(List<Map<String, dynamic>> events) {
  String? currentMode;
  List<Map<String, dynamic>>? availableModes;
  String? currentModel;
  List<Map<String, dynamic>>? availableModels;
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    final kind = (e['kind'] ?? '').toString();
    if (kind != 'system') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final body = p.cast<String, dynamic>();
    if (currentMode == null && body['currentModeId'] is String) {
      currentMode = body['currentModeId'] as String;
      if (body['availableModes'] is List) {
        availableModes = [
          for (final m in (body['availableModes'] as List))
            if (m is Map) m.cast<String, dynamic>(),
        ];
      }
    }
    if (currentModel == null && body['currentModelId'] is String) {
      currentModel = body['currentModelId'] as String;
      if (body['availableModels'] is List) {
        availableModels = [
          for (final m in (body['availableModels'] as List))
            if (m is Map) m.cast<String, dynamic>(),
        ];
      }
    }
    if (currentMode != null && currentModel != null) break;
  }
  if (currentMode == null && currentModel == null) return null;
  return <String, dynamic>{
    'currentMode': currentMode,
    'availableModes': availableModes ?? const <Map<String, dynamic>>[],
    'currentModel': currentModel,
    'availableModels': availableModels ?? const <Map<String, dynamic>>[],
  };
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
  const AgentFeed({
    super.key,
    required this.agentId,
    this.sessionId,
    this.padding = const EdgeInsets.all(12),
    this.onSessionInit,
    this.onModeModelChanged,
    this.initialSeq,
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

  @override
  void initState() {
    super.initState();
    _bootstrap();
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
    _reconnectTimer?.cancel();
    _bannerGraceTimer?.cancel();
    _sub?.cancel();
    _scroll.dispose();
    super.dispose();
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
    if (_latestSessionInitPayload() != null) return;
    if (widget.onSessionInit == null) return;
    try {
      // Pull the agent's tail across ALL sessions; scan back from
      // newest for the most recent session.init. Page size is small
      // because session.init is rare and usually within the last few
      // hundred events.
      final any = await client.listAgentEvents(
        widget.agentId,
        tail: true,
        limit: 200,
        // No sessionId — that's the whole point of the fallback.
      );
      if (!mounted) return;
      for (final e in any.reversed) {
        if ((e['kind'] ?? '').toString() != 'session.init') continue;
        final p = e['payload'];
        if (p is! Map) continue;
        final payload = p.cast<String, dynamic>();
        final sid = (payload['session_id'] ?? '').toString();
        if (sid == _lastReportedInitSid) return;
        _lastReportedInitSid = sid;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) widget.onSessionInit?.call(payload);
        });
        return;
      }
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
      // pagination over ts can produce overlap on equal-ts rows).
      final ascending = <Map<String, dynamic>>[];
      for (final e in older.reversed) {
        final id = (e['id'] ?? '').toString();
        if (id.isNotEmpty && !_ids.add(id)) continue;
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
    _events
      ..clear()
      ..addAll(ascending);
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
      if (agentEventIsReplay(evt) &&
          replayKey != null &&
          _replayKeys.contains(replayKey)) {
        return;
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
    final shouldShowBanner = isError && !_isIdleDropSignature(reason);
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

  /// Recognizes the dart:io / network-stack signatures that match a
  /// passive idle close rather than a real connectivity failure. The
  /// SSE stream sits idle between turns; many proxies / Android
  /// dozers / carrier NATs reap idle TCP after ~60s, which surfaces as
  /// an `HttpException: Connection closed ...` (or a peer-reset
  /// SocketException) on the dart side. The reconnect timer recovers
  /// transparently — the banner adds noise without giving the user
  /// anything to do, so we suppress it for these cases. Match by
  /// substring rather than exact text: dart:io's wording has shifted
  /// between SDK versions, and the messages are not part of any
  /// stable contract.
  static bool _isIdleDropSignature(String reason) {
    final lc = reason.toLowerCase();
    return lc.contains('connection closed') ||
        lc.contains('connection reset') ||
        lc.contains('connection abort') ||
        lc.contains('connection terminated') ||
        lc.contains('http2streamlimit') ||
        lc.contains('stream closed') ||
        lc.contains('before full body received');
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
    final initForCompose = _latestSessionInitPayload();
    // Lift session.init to the parent (AppBar) once per change so a
    // session header doesn't take up a transcript row on mobile. The
    // payload identity is stable between events, so we compare
    // session_id to avoid spamming setState when the parent only cares
    // about new connections, not every event.
    if (initForCompose != null) {
      final sid = (initForCompose['session_id'] ?? '').toString();
      if (sid != _lastReportedInitSid) {
        _lastReportedInitSid = sid;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) widget.onSessionInit?.call(initForCompose);
        });
      }
    }
    final composeSlash = _stringList(initForCompose?['slash_commands']);
    final composeMentions = <String>[
      ..._stringList(initForCompose?['agents']),
      ..._stringList(initForCompose?['tools']),
      ..._stringList(initForCompose?['skills']),
    ];
    // Agent-busy signal for the composer's cancel-on-send overlay:
    // the latest event's kind tells us whether the current turn has
    // wrapped. turn.result / completion / lifecycle:exited are the
    // terminal markers; anything else means "the agent is still
    // producing output for the in-flight turn". Composer only renders
    // the cancel button when the user has already typed something —
    // so this flag matters only in the predictive-input scenario.
    final isAgentBusy = _isAgentBusy();
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
    final modelTotals = <String, _ModelTokens>{};
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
    _ModelTokens? cumulativeUsage;
    String cumulativeBucketKey = 'agent';
    // Latest known context-window stats (codex's
    // thread/tokenUsage/updated carries both modelContextWindow and the
    // cumulative total). The window can change mid-session if codex
    // hot-swaps models, so we always track the most recent values.
    int? latestContextWindow;
    int? latestContextUsed;
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
                entry.key.toString(), _ModelTokens.empty);
            tot.add(v.cast<String, dynamic>());
          }
        }
      } else if (kind == 'rate_limit') {
        latestRateLimit = p.cast<String, dynamic>();
      } else if (kind == 'usage' && _isCumulativeUsage(p)) {
        // Cumulative session totals (codex shape). The latest
        // notification supersedes; we don't sum. Claude's per-
        // message usage events lack the `cumulative` marker and
        // are ignored here — the authoritative claude source is
        // turn.result.by_model handled above.
        final t = _ModelTokens.empty();
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
        // Context-window snapshot rides on the same event. Total
        // tokens (input+output+reasoning, all cumulative) is what
        // codex itself uses in its TUI statusline — it's the value
        // the model sees its context filled with on the next turn.
        final cw = (p['context_window'] as num?)?.toInt() ?? 0;
        final used = (p['total_tokens'] as num?)?.toInt() ?? 0;
        if (cw > 0) latestContextWindow = cw;
        if (used > 0) latestContextUsed = used;
      }
    }
    // If no by_model rows arrived (codex's turn/completed doesn't
    // ship them), surface the cumulative usage as a single bucket.
    // The bucket key is shown in the tile's tooltip so we tag it
    // with the engine name rather than leaving it blank.
    if (modelTotals.isEmpty && cumulativeUsage != null) {
      modelTotals[cumulativeBucketKey] = cumulativeUsage;
    }
    // Claude path for context window: the codex `usage` event already
    // populated latestContextWindow / latestContextUsed when present.
    // For claude (which carries the data per-model on turn.result and
    // does not emit cumulative `usage` events), pick the dominant
    // model from modelTotals — the one with the most output, since
    // sub-agents like Haiku produce trivial output relative to the
    // main agent. Use that model's contextWindow as capacity and its
    // latest input + cache_read + cache_create as "used" (matches
    // what claude's TUI statusline shows for the most recent message).
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
        // Latest-turn "used" = the model's input + cache hits for the
        // last turn it ran. cacheCreate is included because writes
        // also occupy context for the rest of the session.
        final used = t.latestInput + t.latestCacheRead + t.latestCacheCreate;
        if (used > 0) latestContextUsed = used;
      }
    }
    final hasTelemetry = turnCount > 0 ||
        modelTotals.isNotEmpty ||
        latestRateLimit != null ||
        latestContextWindow != null;
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
        if (!_isHiddenInFeed(e, toolNames)) e,
    ];
    final visible = _collapseStreamingPartials(filtered);
    // Count the verbose-gated events so the toggle can advertise its
    // value — "Show debug (12)" carries more signal than a bare button.
    int hiddenForVerbose = 0;
    if (!_verbose) {
      for (final e in _events) {
        final kind = (e['kind'] ?? '').toString();
        if (_isVerboseOnly(kind, e['payload'])) hiddenForVerbose++;
      }
    }
    // ADR-021 W2.5 — mode/model picker is now hung off the parent's
    // AppBar via [onModeModelChanged] (lifted out of the body so it
    // doesn't burn a row of vertical space above every transcript).
    // The callback fires from the post-build microtask below to avoid
    // a setState-during-build on the parent.
    _maybeFireModeModelChanged();
    return Column(
      children: [
        // session.init is rendered in the parent AppBar via the
        // onSessionInit callback. We intentionally don't render
        // _SessionHeader inline anymore — the info is fixed for the
        // session and cost a full transcript row on mobile.
        if (hasTelemetry)
          _TelemetryStrip(
            totalCostUsd: totalCostUsd,
            turnCount: turnCount,
            modelTotals: modelTotals,
            rateLimit: latestRateLimit,
            contextWindow: latestContextWindow,
            contextUsed: latestContextUsed,
          ),
        if (_staleSince != null) _OfflineBanner(staleSince: _staleSince!),
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
                    child: _NewEventsPill(
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
                  child: _VerboseToggleChip(
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

  // Returns true when the current turn hasn't ended yet — the agent is
  // streaming text, awaiting a tool result, or hasn't emitted a
  // turn.result. Drives the composer's cancel-on-send overlay so the
  // user can interrupt a running turn to send their next prompt
  // (the predictive-input case the user spelled out).
  //
  // Scan from the tail backwards looking for the latest "real" event —
  // skip producer='user' input echoes since those don't reflect agent
  // state. A terminal kind (turn.result / completion / a lifecycle
  // exit) means idle; anything else from the agent side means busy.
  // No events at all → idle.
  bool _isAgentBusy() {
    for (final e in _events.reversed) {
      final producer = (e['producer'] ?? '').toString();
      if (producer == 'user') continue; // user inputs don't move the state
      final kind = (e['kind'] ?? '').toString();
      if (kind == 'turn.result' || kind == 'completion') return false;
      // session.init is a one-shot handshake event — it lands once
      // per resume/start and means "ready, waiting for input." If
      // it's the most recent agent event (no turn-active signal
      // after it), the agent is idle, not busy.
      if (kind == 'session.init') return false;
      if (kind == 'lifecycle') {
        final p = e['payload'];
        final phase = p is Map ? (p['phase'] ?? '').toString() : '';
        if (phase == 'exited' || phase == 'stopped') return false;
        // 'started' and other lifecycle phases are ambiguous; keep
        // scanning so a recent text/tool_call wins the decision.
        continue;
      }
      // 'system' covers a grab-bag of telemetry frames (status
      // updates, server-startup pings) that don't, on their own,
      // mean a turn is in progress. Skip and keep scanning so a
      // real text/tool_call signal can win.
      if (kind == 'system') continue;
      // Any other agent-produced kind — text streaming, thought,
      // tool_call mid-flight, plan, raw, etc. — means the turn is
      // still in motion.
      return true;
    }
    return false;
  }

  // Find the most recent session.init payload, if any. Used by the
  // composer for picker data — duplicated lookup with the build-time
  // sessionInit lookup, but this one needs to run before the visible
  // list is computed so we can pre-build the AgentCompose.
  Map<String, dynamic>? _latestSessionInitPayload() {
    for (final e in _events.reversed) {
      if ((e['kind'] ?? '').toString() == 'session.init') {
        final p = e['payload'];
        if (p is Map) return p.cast<String, dynamic>();
      }
    }
    return null;
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
    final modelIds = d.availableModels
        .map((m) => m['id']?.toString() ?? '')
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

  List<String> _stringList(Object? v) {
    if (v is! List) return const [];
    return [for (final e in v) e.toString()];
  }

  // Folded-into-parent kinds drop out of the visible list. tool_result is
  // only hidden when the matching tool_call is in scope (toolNames has its
  // id) — a stray result with no parent call still renders so we never
  // silently lose data.
  //
  // Telemetry-only kinds (usage, rate_limit, turn.result) live in the
  // strip above the feed; rendering them as cards too would duplicate
  // the signal.
  //
  // Verbose-gated kinds (W1.B):
  //   lifecycle      — started/stopped frames; the agent's status pill
  //                    on the steward badge already conveys this.
  //   completion     — deprecated alias for turn.result; already covered
  //                    by the telemetry strip.
  //   raw            — thinking blocks + unrecognized frames; debug-only.
  //   system         — non-init system frames; init is in the header.
  // input.* events are NOT hidden by default — the compose box clears
  // after send, so the user needs to see their own message echoed back
  // in the transcript or the chat reads as one-sided.
  // All verbose-gated kinds are revealed when [_verbose] is true.
  bool _isHiddenInFeed(
    Map<String, dynamic> e,
    Map<String, String> toolNames,
  ) {
    final kind = (e['kind'] ?? '').toString();
    if (kind == 'tool_call_update' ||
        kind == 'session.init' ||
        kind == 'usage' ||
        kind == 'rate_limit' ||
        kind == 'turn.result') {
      return true;
    }
    if (kind == 'tool_call') {
      // Hide MCP "gate" tool_calls — the ones whose effect is to open
      // an attention_item that mobile already renders as an inline
      // card. Showing both surfaces (the tool_call card + the
      // attention card) double-counts the same event.
      //
      // Three gates today, all under mcp__termipod__:
      //   - permission_prompt — claude-code's --permission-prompt-tool
      //     contract. Rendered as the inline approval card.
      //   - request_select — multi-choice. Rendered as the inline
      //     SELECT card.
      //   - request_approval — generic ask-for-human-yes/no. Rendered
      //     as an attention item on the Me page (no inline card, but
      //     the tool_call card is still noisy).
      // Bare names also accepted (no `mcp__<server>__` prefix) so
      // alternate engines that surface the same tool names hide too.
      final p = e['payload'];
      if (p is Map) {
        final name = (p['name'] ?? '').toString();
        const gates = {
          'permission_prompt',
          'request_select',
          // Back-compat: an agent spawned with a stale prompt template
          // may still call request_decision; the server aliases to
          // request_select but the tool_call event keeps the old name.
          // Hide both so the duplicate-card fix covers either spelling.
          'request_decision',
          'request_approval',
        };
        if (gates.contains(name)) return true;
        for (final g in gates) {
          if (name.endsWith('__$g')) return true;
        }
      }
      return false;
    }
    if (kind == 'tool_result') {
      final p = e['payload'];
      if (p is Map) {
        final id = p['tool_use_id']?.toString() ?? '';
        if (id.isNotEmpty && toolNames.containsKey(id)) return true;
      }
      return false;
    }
    if (!_verbose && _isVerboseOnly(kind, e['payload'])) return true;
    return false;
  }

  static const _kVerboseOnlyKinds = <String>{
    'lifecycle',
    'completion',
    'raw',
    'system',
  };

  bool _isVerboseOnly(String kind, Object? payload) {
    if (!_kVerboseOnlyKinds.contains(kind)) return false;
    // `system` is a generic envelope. Init lands in the header already
    // (handled above). Don't suppress the rest just because the family
    // is verbose-gated — fall through to render text payloads, etc. so
    // a real system message isn't silently dropped.
    if (kind == 'system' && payload is Map) {
      final sub = (payload['subtype'] ?? '').toString();
      if (sub.isNotEmpty && sub != 'init') return false;
    }
    return true;
  }

  // True when a `usage` payload carries cumulative session totals
  // (codex's thread/tokenUsage/updated). Accepts either a real bool or
  // the string "true" — the frame-profile evaluator only emits strings,
  // so the wire format is `"true"`, but a future evaluator extension
  // (or a different engine's profile) could emit a JSON bool. Either
  // way means "the latest event replaces, don't sum."
  bool _isCumulativeUsage(Map p) {
    final v = p['cumulative'];
    if (v is bool) return v;
    if (v is String) return v.toLowerCase() == 'true';
    return false;
  }

  // Codex emits item/agentMessage/delta as a stream of small chunks
  // while a turn is generating. The driver throttles + buffers them
  // into `kind=text, partial: true` events that share a message_id;
  // each carries the full accumulated text so far (not a delta). The
  // final item/completed produces a normal `kind=text` event with the
  // same message_id and no partial flag.
  //
  // Mobile collapse: walk events in order. The first partial for a
  // message_id opens a chain — its index in the rendered list is
  // remembered. Subsequent text events (partial OR final) for the
  // same message_id replace the chain entry instead of appending. A
  // text event with no partial flag and no preceding partial chain
  // (claude's case) appends normally — we only redirect events whose
  // message_id is already a known chain root, so claude's per-block
  // text events with the same message_id keep stacking the way they
  // do today.
  static List<Map<String, dynamic>> _collapseStreamingPartials(
      List<Map<String, dynamic>> events) {
    final out = <Map<String, dynamic>>[];
    final chainIdx = <String, int>{};
    for (final e in events) {
      final kind = (e['kind'] ?? '').toString();
      if (kind != 'text') {
        out.add(e);
        continue;
      }
      final p = e['payload'];
      String? mid;
      bool isPartial = false;
      if (p is Map) {
        final m = (p['message_id'] ?? '').toString();
        if (m.isNotEmpty) mid = m;
        final pv = p['partial'];
        isPartial = (pv == true || pv == 'true');
      }
      if (mid == null) {
        out.add(e);
        continue;
      }
      final existing = chainIdx[mid];
      if (existing != null) {
        // We're in a streaming chain for this message_id — every
        // subsequent text event (partial or final) replaces the entry.
        out[existing] = e;
      } else if (isPartial) {
        // First partial for this message_id opens a chain.
        chainIdx[mid] = out.length;
        out.add(e);
      } else {
        // Regular text event with no preceding partial — claude's
        // shape; append without opening a chain.
        out.add(e);
      }
    }
    return out;
  }
}

/// "Offline · last updated 2m ago" strip shown above the transcript
/// when the bootstrap fetch fell back to the snapshot cache. Cleared
/// the moment a live SSE event arrives — same trigger as `_error`,
/// because either a fresh fetch or the first stream push proves the
/// hub is reachable again.
class _OfflineBanner extends StatelessWidget {
  final DateTime staleSince;
  const _OfflineBanner({required this.staleSince});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        color: DesignColors.warning.withValues(alpha: 0.08),
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Row(
        children: [
          Icon(Icons.cloud_off_outlined, size: 14, color: muted),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              'Offline — showing cached transcript (last updated '
              '${_relative(staleSince)})',
              style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
            ),
          ),
        ],
      ),
    );
  }

  static String _relative(DateTime ts) {
    final diff = DateTime.now().difference(ts);
    if (diff.inMinutes < 1) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    return '${diff.inDays}d ago';
  }
}

/// One-line bar above the event list: shows the verbose toggle and,
/// when off, how many events are currently hidden by it. Mirrors the
/// "show raw" toggle in claude-code's terminal (Ctrl+O) — by default
/// the transcript reads as a chat surface; flip to see the debug
/// stream when something looks wrong.
/// Floating chip in the feed's top-right corner. Toggles _verbose so
/// debug-fidelity events (lifecycle, raw, system) appear as cards.
/// Replaces the prior full-row toggle bar that ate vertical space on
/// every chat surface even when nothing was hidden. Tooltip carries
/// the explanatory copy so the chip stays icon+count-only.
class _VerboseToggleChip extends StatelessWidget {
  final bool verbose;
  final int hiddenCount;
  final VoidCallback onToggle;
  const _VerboseToggleChip({
    required this.verbose,
    required this.hiddenCount,
    required this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final label = verbose
        ? 'on'
        : (hiddenCount > 0 ? '$hiddenCount' : '');
    return Tooltip(
      message: verbose
          ? 'Hide debug events (lifecycle, raw, system)'
          : 'Show debug events (lifecycle, raw, system)'
              '${hiddenCount > 0 ? ' — $hiddenCount currently hidden' : ''}',
      child: Material(
        color: bg.withValues(alpha: 0.92),
        elevation: 1,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(14),
          side: BorderSide(color: border),
        ),
        child: InkWell(
          onTap: onToggle,
          borderRadius: BorderRadius.circular(14),
          child: Padding(
            padding: const EdgeInsets.symmetric(
                horizontal: 8, vertical: 4),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  verbose
                      ? Icons.visibility
                      : Icons.visibility_off_outlined,
                  size: 14,
                  color: muted,
                ),
                if (label.isNotEmpty) ...[
                  const SizedBox(width: 4),
                  Text(
                    label,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      color: muted,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
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

    final toolName = (widget.payload['tool_name'] ?? 'tool').toString();
    final input = widget.payload['input'];
    final inputText = input == null
        ? ''
        : (input is String ? input : AgentEventCard._jsonPretty(input));
    final tierColor = switch (_tier) {
      'strategic' => DesignColors.error,
      'significant' => DesignColors.warning,
      _ => DesignColors.primary,
    };
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
                  'Approve $toolName?',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          if (inputText.isNotEmpty) ...[
            const SizedBox(height: 6),
            _CollapsibleMono(text: inputText),
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
                child: const Text('Deny'),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: _sending ? null : () => _decide('approve'),
                style: FilledButton.styleFrom(
                  backgroundColor: _isStrategic
                      ? DesignColors.error
                      : DesignColors.success,
                  foregroundColor: Colors.white,
                  padding: const EdgeInsets.symmetric(
                      horizontal: 14, vertical: 6),
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Text(_isStrategic ? 'Approve (strategic)' : 'Approve'),
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

class _NewEventsPill extends StatelessWidget {
  // Number of events that arrived while scrolled away from tail. 0
  // when the user is just reading history with no new traffic — the
  // pill still renders as a plain jump-to-tail control so they can
  // snap back without scrolling manually.
  final int count;
  // Current scroll position as a 0..100 percent so the pill doubles
  // as a position indicator. Helpful in long sessions where "where am
  // I?" is non-obvious from row count alone.
  final int scrollPercent;
  final VoidCallback onTap;
  const _NewEventsPill({
    required this.count,
    required this.scrollPercent,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final label = count > 0 ? '$count new · $scrollPercent%' : '$scrollPercent%';
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(16),
        child: Container(
          padding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: DesignColors.primary,
            borderRadius: BorderRadius.circular(16),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.2),
                blurRadius: 6,
                offset: const Offset(0, 2),
              ),
            ],
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                label,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: Colors.white,
                ),
              ),
              const SizedBox(width: 4),
              const Icon(Icons.arrow_downward,
                  size: 14, color: Colors.white),
            ],
          ),
        ),
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
        s = '$name\n${_jsonPretty(input is Map ? input : payload)}';
        break;
      case 'tool_result':
        final content = payload['content'];
        if (content is String && content.isNotEmpty) {
          s = content;
        } else if (content is Map || content is List) {
          s = _jsonPretty(content);
        } else {
          s = (payload['text'] ?? _jsonPretty(payload)).toString();
        }
        break;
      case 'system':
        // System rows usually carry a one-liner; otherwise fall back
        // to the full payload so audit-trail entries copy with their
        // structured fields intact.
        final t = (payload['text'] ?? payload['summary'] ?? '').toString();
        s = t.isNotEmpty ? t : _jsonPretty(payload);
        break;
      default:
        final t = (payload['text'] ?? '').toString();
        s = t.isNotEmpty ? t : _jsonPretty(payload);
    }
    return s.isEmpty ? _jsonPretty(event) : s;
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
          (payload['text'] ?? _jsonPretty(payload)).toString(),
          isThought: kind == 'thought',
        );
      case 'raw':
        return _rawBody(ctx, payload);
      case 'tool_call':
        return _toolCallBody(ctx, payload);
      case 'tool_result':
        return _toolResultBody(ctx, payload);
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
      case 'system':
        return _systemBody(ctx, payload);
      default:
        // Any other hub-side kinds — render their text field when present,
        // fall back to pretty JSON otherwise.
        final t = payload['text']?.toString();
        if (t != null && t.isNotEmpty) return _textBody(ctx, t);
        return _textBody(ctx, _jsonPretty(payload));
    }
  }

  Widget _inputTextBody(BuildContext ctx, Map<String, dynamic> p) {
    // InputRouter strips the "input." prefix before dispatch; the
    // persisted event still has a body field matching AgentCompose's
    // postAgentInput payload.
    final body = (p['body'] ?? p['text'] ?? '').toString();
    return _mono(ctx, body.isEmpty ? '(empty)' : body);
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
    return _textBody(ctx, _jsonPretty(p));
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

  Widget _toolResultBody(BuildContext ctx, Map<String, dynamic> p) {
    final id = p['tool_use_id']?.toString() ?? '';
    final name = id.isNotEmpty ? toolNames[id] : null;
    final isError = p['is_error'] == true;
    final content = p['content'];
    final text = content is String ? content : _jsonPretty(content);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (name != null) _kv(ctx, 'tool', name),
        if (id.isNotEmpty) _kv(ctx, 'tool_use_id', id),
        if (isError) _kv(ctx, 'is_error', 'true'),
        _CollapsibleMono(
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
    final msg = (p['error'] ?? p['message'] ?? _jsonPretty(p)).toString();
    return _mono(ctx, msg, color: DesignColors.error);
  }

  // ACP plan update: { sessionUpdate: "plan", entries: [{content, priority,
  // status}] }. Render as a compact checklist so the operator can see what
  // the agent is tracking without drilling into raw JSON.
  Widget _planBody(BuildContext ctx, Map<String, dynamic> p) {
    final entriesRaw = p['entries'];
    if (entriesRaw is! List || entriesRaw.isEmpty) {
      return _mono(ctx, _jsonPretty(p));
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
        _CollapsibleMono(text: _jsonPretty(p)),
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
      data: _normalizeMultilineMath(s),
      selectable: true,
      shrinkWrap: true,
      // Tap on `[text](href)` opens the URL in the system browser.
      // Underline + primary color come from styleSheet.a below; we
      // intentionally don't register a custom 'a' element builder,
      // because flutter_markdown appends the builder's widget *after*
      // the default styled inline span — registering one renders the
      // visible label twice (once colored-underlined, once tappable).
      onTapLink: (text, href, title) => _openLink(ctx, href),
      builders: {
        'code': _HighlightedCodeBuilder(isDark: isDark),
        // KaTeX-style LaTeX math. Two flavors of the same builder so
        // the markdown parser can route inline ($...$) and display
        // ($$...$$) at different vertical sizes/alignment.
        'math': _MathBuilder(isDark: isDark, display: false),
        'mathblock': _MathBuilder(isDark: isDark, display: true),
      },
      // Custom inline syntaxes only — no BlockSyntax. The preprocessor
      // (_normalizeMultilineMath) collapses well-formed multi-line
      // $$...$$ and \[...\] regions into single-line $$...$$ before
      // we get here; unbalanced delimiters fall through to plain text.
      // Order matters: $$...$$ must be tried before $...$ or the
      // parser will eat the leading $$ as two empty $$s; same for
      // \[...\] vs \(...\).
      extensionSet: md.ExtensionSet(
        md.ExtensionSet.gitHubFlavored.blockSyntaxes,
        [
          _MathBlockInlineSyntax(),
          _MathInlineSyntax(),
          _LatexBracketDisplayInlineSyntax(),
          _LatexBracketInlineSyntax(),
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

  static String _jsonPretty(Object? v) {
    try {
      return const JsonEncoder.withIndent('  ').convert(v);
    } catch (_) {
      return v?.toString() ?? '';
    }
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
  bool _collapsed = false;

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

/// MarkdownElementBuilder that swaps `<pre><code class="language-X">` blocks
/// out for a syntax-highlighted view. Inline `<code>` (no class attribute)
/// returns null so flutter_markdown falls back to its own monochrome
/// styleSheet rendering — we only want the heavy treatment on fenced
/// blocks where the language is declared. Fenced blocks without a
/// language (just ``` ```) get a plaintext highlight (no colors), which
/// still picks up the themed background + padding so the block visually
/// stands out from prose.
class _HighlightedCodeBuilder extends MarkdownElementBuilder {
  final bool isDark;
  _HighlightedCodeBuilder({required this.isDark});

  @override
  Widget? visitElementAfter(md.Element element, TextStyle? preferredStyle) {
    final classAttr = element.attributes['class'] ?? '';
    // Inline `<code>` has no class — let the base styleSheet handle it.
    // Fenced blocks always get a class even with no language ("language-").
    if (!classAttr.startsWith('language-')) return null;
    var language = classAttr.substring('language-'.length).trim();
    // flutter_highlight expects a known id or 'plaintext'; an unknown id
    // will raise. Map common aliases and fall back to plaintext for
    // anything we don't recognize.
    language = _normalizeLanguage(language);
    final theme = isDark ? atomOneDarkTheme : atomOneLightTheme;
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(4),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      clipBehavior: Clip.hardEdge,
      child: HighlightView(
        element.textContent,
        language: language,
        theme: theme,
        padding: const EdgeInsets.all(8),
        textStyle: GoogleFonts.jetBrainsMono(fontSize: 11, height: 1.35),
      ),
    );
  }

  // highlight.js language ids we keep as-is (the package ships these).
  // Aliases a user might type in a fence get rerouted here so we don't
  // throw "no language with that id" at runtime. Unknowns drop to
  // plaintext (still themed/padded, just not colored).
  static String _normalizeLanguage(String raw) {
    if (raw.isEmpty) return 'plaintext';
    final l = raw.toLowerCase();
    const aliases = {
      'sh': 'bash',
      'shell': 'bash',
      'zsh': 'bash',
      'console': 'bash',
      'js': 'javascript',
      'ts': 'typescript',
      'jsx': 'javascript',
      'tsx': 'typescript',
      'py': 'python',
      'rb': 'ruby',
      'rs': 'rust',
      'kt': 'kotlin',
      'cs': 'cs',
      'h': 'cpp',
      'hpp': 'cpp',
      'cc': 'cpp',
      'cxx': 'cpp',
      'c++': 'cpp',
      'objc': 'objectivec',
      'yml': 'yaml',
      'md': 'markdown',
      'tex': 'latex',
      'plain': 'plaintext',
      'text': 'plaintext',
    };
    return aliases[l] ?? l;
  }
}

// LaTeX math support. Two delimiter conventions, both common in LLM
// output:
//
//   1. arXiv/Pandoc dollar style:   $...$  (inline)   $$...$$ (display)
//   2. LaTeX bracket style:         \(...\) (inline)  \[...\] (display)
//
// All three single-line variants are inline syntaxes; only \[...\]
// also has a block flavor since LLMs frequently emit it as
//
//   \[
//   <expr possibly with \\ row breaks>
//   \]
//
// — and inline syntaxes can't span newlines.

// _openLink launches a markdown URL through the system browser. We
// don't try to validate or whitelist schemes — that's the OS's job
// and operators have legitimate uses for ssh:, mailto:, etc. A
// SnackBar surfaces any launch failure so a broken href doesn't
// silently swallow the tap.
Future<void> _openLink(BuildContext ctx, String? href) async {
  if (href == null || href.isEmpty) return;
  final uri = Uri.tryParse(href);
  if (uri == null) return;
  try {
    final ok = await launchUrl(uri, mode: LaunchMode.externalApplication);
    if (!ok && ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(
        SnackBar(content: Text('Could not open $href')),
      );
    }
  } catch (e) {
    if (ctx.mounted) {
      ScaffoldMessenger.of(ctx).showSnackBar(
        SnackBar(content: Text('Open failed: $e')),
      );
    }
  }
}

// _MathBlockInlineSyntax: matches $$...$$ (single-line). Listed BEFORE
// the inline $...$ rule so the parser can claim both delimiters
// before the $-rule eats the leading pair.
class _MathBlockInlineSyntax extends md.InlineSyntax {
  _MathBlockInlineSyntax() : super(r'\$\$([^\$\n]+?)\$\$');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('mathblock', tex));
    return true;
  }
}

// _MathInlineSyntax: matches $...$ on a single line, requiring
// non-$ content. Avoids triggering on bare $5 / $20 currency
// references (those would need a closing $ to match).
class _MathInlineSyntax extends md.InlineSyntax {
  _MathInlineSyntax() : super(r'\$([^\$\n]+?)\$');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('math', tex));
    return true;
  }
}

// _LatexBracketDisplayInlineSyntax: matches single-line \[ ... \].
// Multi-line \[...\] is collapsed to $$...$$ by _normalizeMultilineMath
// before this syntax sees the input.
class _LatexBracketDisplayInlineSyntax extends md.InlineSyntax {
  _LatexBracketDisplayInlineSyntax() : super(r'\\\[([^\n]+?)\\\]');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('mathblock', tex));
    return true;
  }
}

// _LatexBracketInlineSyntax: matches \( ... \) on a single line.
class _LatexBracketInlineSyntax extends md.InlineSyntax {
  _LatexBracketInlineSyntax() : super(r'\\\(([^\n]+?)\\\)');
  @override
  bool onMatch(md.InlineParser parser, Match match) {
    final tex = match[1] ?? '';
    parser.addNode(md.Element.text('math', tex));
    return true;
  }
}

// _normalizeMultilineMath collapses well-formed multi-line math regions
// into single-line forms the inline syntaxes already handle.
//
//   $$\n<expr>\n$$           →  $$<expr-flattened>$$
//   \[\n<expr>\n\]           →  $$<expr-flattened>$$
//
// Internal newlines become spaces — TeX treats both identically, so
// this preserves matrix `\\` row breaks and other structural commands.
//
// Why preprocess instead of registering a BlockSyntax: a greedy block
// parser silently swallows everything after a stray `$$` when no close
// follows (codex transcripts contain shell `$$`, prompt strings, etc.)
// — the visible symptom is "transcript renders blank." This function
// only rewrites a region when it finds a matching close; unbalanced
// delimiters fall through unchanged and render as plain text.
//
// Fenced code blocks (``` … ```) are skipped so command examples stay
// untouched; non-greedy matching keeps two adjacent regions distinct.
String _normalizeMultilineMath(String input) {
  if (input.isEmpty) return input;
  // Split on fenced code blocks; even-indexed slices are body text we
  // rewrite, odd-indexed slices are code we leave verbatim. The
  // fence itself goes back into the odd slice.
  final fence = RegExp(r'(```[\s\S]*?```|~~~[\s\S]*?~~~)', multiLine: true);
  final parts = <String>[];
  int cursor = 0;
  for (final m in fence.allMatches(input)) {
    parts.add(input.substring(cursor, m.start));
    parts.add(m.group(0)!);
    cursor = m.end;
  }
  parts.add(input.substring(cursor));

  // Patterns are non-greedy; the (?=\s*\n|$) anchors require the
  // delimiters to sit on their own line so we don't claim inline
  // sequences the inline syntaxes already handle.
  final dollarBlock = RegExp(
    r'(^|\n)\$\$\s*\n([\s\S]+?)\n\s*\$\$(?=\s*\n|\s*$)',
    multiLine: true,
  );
  final bracketBlock = RegExp(
    r'(^|\n)\\\[\s*\n([\s\S]+?)\n\s*\\\](?=\s*\n|\s*$)',
    multiLine: true,
  );

  String flatten(String body) =>
      body.replaceAll(RegExp(r'\s*\n\s*'), ' ').trim();

  for (var i = 0; i < parts.length; i += 2) {
    var s = parts[i];
    s = s.replaceAllMapped(dollarBlock, (m) {
      return '${m.group(1)}\$\$${flatten(m.group(2)!)}\$\$';
    });
    s = s.replaceAllMapped(bracketBlock, (m) {
      return '${m.group(1)}\$\$${flatten(m.group(2)!)}\$\$';
    });
    parts[i] = s;
  }
  return parts.join();
}

// _MathBuilder renders a flutter_math_fork Math.tex widget for the
// element's text. `display` toggles inline (uses MathStyle.text) vs
// block (uses MathStyle.display, larger and centered).
//
// Errors fall back to the raw TeX wrapped in $...$ as inline mono —
// keeps malformed math visible rather than silently dropped, so the
// principal can spot LLM-generated typos.
class _MathBuilder extends MarkdownElementBuilder {
  final bool isDark;
  final bool display;
  _MathBuilder({required this.isDark, required this.display});

  @override
  Widget? visitElementAfter(md.Element element, TextStyle? preferredStyle) {
    final tex = element.textContent;
    final color = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final base = GoogleFonts.spaceGrotesk(
      fontSize: display ? 15 : 13,
      color: color,
    );
    final widget = Math.tex(
      tex,
      textStyle: base,
      mathStyle: display ? MathStyle.display : MathStyle.text,
      onErrorFallback: (e) => Text(
        '\$$tex\$',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: DesignColors.error,
        ),
      ),
    );
    if (!display) return widget;
    // Display-math: center on its own line with a touch of vertical
    // breathing room so the bigger glyphs don't run into surrounding
    // paragraphs.
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Center(child: widget),
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
            child: _CollapsibleMono(
              text: AgentEventCard._jsonPretty(widget.params),
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
      return _CollapsibleMono(text: AgentEventCard._jsonPretty(widget.input));
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

/// Mono text that collapses past _kCollapseLines with a toggle. Long
/// tool_call inputs and tool_result outputs would otherwise dominate the
/// feed — a single grep result can push everything else off-screen.
class _CollapsibleMono extends StatefulWidget {
  final String text;
  final Color? color;
  const _CollapsibleMono({required this.text, this.color});

  @override
  State<_CollapsibleMono> createState() => _CollapsibleMonoState();
}

const int _kCollapseLines = 12;

class _CollapsibleMonoState extends State<_CollapsibleMono> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final lines = widget.text.split('\n');
    final overflow = lines.length > _kCollapseLines;
    final shown = (overflow && !_expanded)
        ? lines.take(_kCollapseLines).join('\n')
        : widget.text;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SelectableText(
          shown,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            color: widget.color ??
                (isDark
                    ? DesignColors.textPrimary
                    : DesignColors.textPrimaryLight),
          ),
        ),
        if (overflow)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton(
              onPressed: () => setState(() => _expanded = !_expanded),
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                minimumSize: const Size(0, 24),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                foregroundColor: muted,
              ),
              child: Text(
                _expanded
                    ? 'Collapse'
                    : 'Show all (${lines.length} lines)',
                style: GoogleFonts.jetBrainsMono(fontSize: 10),
              ),
            ),
          ),
      ],
    );
  }
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
            _CollapsibleMono(text: AgentEventCard._jsonPretty(widget.input)),
          if (widget.preview != null && widget.preview!.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: _CollapsibleMono(text: widget.preview!),
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

/// Open the session.init details bottom sheet for [payload]. Public so
/// SessionChatScreen can wire its AppBar chip to the same drawer the
/// inline header used to use. [agentKind] surfaces the engine
/// (claude-code, codex, ...) which session.init doesn't carry.
void showSessionDetailsSheet(
  BuildContext context,
  Map<String, dynamic> payload, {
  String? agentKind,
}) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (ctx) =>
        _SessionDetailsSheet(payload: payload, agentKind: agentKind),
  );
}

/// Compact AppBar chip rendering engine + model + permission mode +
/// tools/mcp counts from a session.init payload. Tap → details sheet.
/// Replaces the inline _SessionHeader so the transcript doesn't burn a
/// row on a fixed-shape header.
///
/// [agentKind] is the agent's runtime (claude-code, codex, ...) from
/// the agents table. session.init carries the model (LLM weights) but
/// not the engine that's hosting it; surfacing both lets the operator
/// tell at a glance "this is claude-code running opus 4.7" rather
/// than guessing from the model string.
class SessionInitChip extends StatelessWidget {
  final Map<String, dynamic> payload;
  final String? agentKind;
  const SessionInitChip({
    super.key,
    required this.payload,
    this.agentKind,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final model = payload['model']?.toString() ?? '';
    final permMode = payload['permission_mode']?.toString() ?? '';
    final tools = _SessionHeader._toList(payload['tools']);
    final mcpServers = _SessionHeader._toMapList(payload['mcp_servers']);
    return InkWell(
      onTap: () => showSessionDetailsSheet(context, payload,
          agentKind: agentKind),
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (agentKind != null && agentKind!.isNotEmpty) ...[
              _Pill(
                label: _shortKind(agentKind!),
                color: DesignColors.primary,
              ),
              const SizedBox(width: 4),
            ],
            if (model.isNotEmpty)
              _Pill(
                label: _shortModel(model),
                color: DesignColors.secondary,
              ),
            if (permMode.isNotEmpty) ...[
              const SizedBox(width: 4),
              _Pill(
                label: permMode,
                color: _SessionHeader._permModeColor(permMode),
              ),
            ],
            if (tools.isNotEmpty) ...[
              const SizedBox(width: 4),
              _Pill(label: '${tools.length}t', color: mutedColor),
            ],
            if (mcpServers.isNotEmpty) ...[
              const SizedBox(width: 4),
              _Pill(
                label: '${mcpServers.length}mcp',
                color:
                    _SessionHeader._mcpAggregateColor(mcpServers, mutedColor),
              ),
            ],
            const SizedBox(width: 2),
            Icon(Icons.expand_more, size: 14, color: mutedColor),
          ],
        ),
      ),
    );
  }

  // Trim the long claude/codex model strings (e.g.
  // "claude-opus-4-7-20260101") down to the family + version so the
  // pill stays readable in the AppBar. Unknown shapes pass through.
  static String _shortModel(String raw) {
    if (raw.startsWith('claude-')) {
      final parts = raw.split('-');
      if (parts.length >= 4) return '${parts[1]} ${parts[2]}.${parts[3]}';
    }
    return raw;
  }

  // Engine names ship as "claude-code" / "codex" / etc. The pill is
  // narrow, so drop the "-code" suffix where it adds no signal.
  static String _shortKind(String raw) {
    if (raw == 'claude-code') return 'claude';
    return raw;
  }
}

/// Sticky header rendered above the agent feed when a session.init event
/// is present. Compact by default — tap to open a bottom-sheet drawer
/// with the rich session metadata (model, tools, mcp servers, slash
/// commands, agents, skills, cwd, version, permission mode).
///
/// Now unused inline (lifted into the SessionChatScreen AppBar via
/// [SessionInitChip]); kept around because the per-pill helpers
/// (_permModeColor, _mcpAggregateColor) are reused by the chip.
class _SessionHeader extends StatelessWidget {
  final Map<String, dynamic> payload;
  const _SessionHeader({required this.payload});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final model = payload['model']?.toString() ?? '';
    final permMode = payload['permission_mode']?.toString() ?? '';
    final tools = _toList(payload['tools']);
    final mcpServers = _toMapList(payload['mcp_servers']);
    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: () => _openDrawer(context),
        child: Container(
          decoration: BoxDecoration(
            color: bg,
            border: Border(bottom: BorderSide(color: border)),
          ),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Row(
            children: [
              Icon(Icons.memory, size: 14, color: DesignColors.secondary),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  model.isEmpty ? 'session' : model,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: isDark
                        ? DesignColors.textPrimary
                        : DesignColors.textPrimaryLight,
                  ),
                ),
              ),
              if (permMode.isNotEmpty) ...[
                const SizedBox(width: 6),
                _Pill(label: permMode, color: _permModeColor(permMode)),
              ],
              if (tools.isNotEmpty) ...[
                const SizedBox(width: 6),
                _Pill(label: '${tools.length} tools', color: mutedColor),
              ],
              if (mcpServers.isNotEmpty) ...[
                const SizedBox(width: 6),
                _Pill(
                  label: '${mcpServers.length} mcp',
                  color: _mcpAggregateColor(mcpServers, mutedColor),
                ),
              ],
              const SizedBox(width: 4),
              Icon(Icons.expand_more, size: 16, color: mutedColor),
            ],
          ),
        ),
      ),
    );
  }

  void _openDrawer(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (ctx) => _SessionDetailsSheet(payload: payload),
    );
  }

  static Color _permModeColor(String mode) {
    // bypassPermissions / acceptEdits / default / plan: only "default"
    // and "plan" are restrictive; the others let the agent edit/run
    // without prompting and deserve an amber pill so the operator notices.
    switch (mode) {
      case 'default':
      case 'plan':
        return DesignColors.success;
      case 'acceptEdits':
        return DesignColors.warning;
      case 'bypassPermissions':
        return DesignColors.error;
      default:
        return DesignColors.textMuted;
    }
  }

  // Aggregate color for the mcp pill: red if any server is in error,
  // amber if any needs auth, green if all connected, muted otherwise.
  static Color _mcpAggregateColor(
      List<Map<String, dynamic>> servers, Color fallback) {
    var hasError = false;
    var hasNeedsAuth = false;
    var allConnected = true;
    for (final s in servers) {
      final status = (s['status'] ?? '').toString().toLowerCase();
      if (status == 'failed' || status == 'error') {
        hasError = true;
      } else if (status == 'needs-auth' || status == 'pending-auth') {
        hasNeedsAuth = true;
      } else if (status != 'connected' && status != 'ok') {
        allConnected = false;
      }
    }
    if (hasError) return DesignColors.error;
    if (hasNeedsAuth) return DesignColors.warning;
    if (allConnected) return DesignColors.success;
    return fallback;
  }

  static List<String> _toList(Object? v) {
    if (v is! List) return const [];
    return [for (final e in v) e.toString()];
  }

  static List<Map<String, dynamic>> _toMapList(Object? v) {
    if (v is! List) return const [];
    return [
      for (final e in v)
        if (e is Map) e.cast<String, dynamic>(),
    ];
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

class _Pill extends StatelessWidget {
  final String label;
  final Color color;
  const _Pill({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

/// Bottom-sheet drawer shown when the operator taps the session header.
/// Sectioned view of every field session.init exposes; sections absent
/// from the payload (e.g. plugins on a stripped-down driver) just don't
/// render, so the drawer adapts to whatever the driver surfaces.
class _SessionDetailsSheet extends StatelessWidget {
  final Map<String, dynamic> payload;
  final String? agentKind;
  const _SessionDetailsSheet({required this.payload, this.agentKind});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final children = <Widget>[];

    void section(String title, Widget body) {
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
        child: Text(
          title,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w700,
            color: mutedColor,
            letterSpacing: 0.5,
          ),
        ),
      ));
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
        child: body,
      ));
    }

    final model = payload['model']?.toString() ?? '';
    final version = payload['version']?.toString() ?? '';
    final permMode = payload['permission_mode']?.toString() ?? '';
    final outputStyle = payload['output_style']?.toString() ?? '';
    final cwd = payload['cwd']?.toString() ?? '';
    final sessionId = payload['session_id']?.toString() ?? '';

    final modelLine = [
      if (model.isNotEmpty) model,
      if (version.isNotEmpty) 'v$version',
    ].join(' · ');
    final hasAgentSection = (agentKind != null && agentKind!.isNotEmpty) ||
        modelLine.isNotEmpty ||
        permMode.isNotEmpty ||
        outputStyle.isNotEmpty;
    if (hasAgentSection) {
      section(
        'AGENT',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (agentKind != null && agentKind!.isNotEmpty)
              _kvLine(context, 'engine', agentKind!),
            if (modelLine.isNotEmpty) _kvLine(context, 'model', modelLine),
            if (permMode.isNotEmpty)
              _kvLine(context, 'permission', permMode,
                  valueColor: _SessionHeader._permModeColor(permMode)),
            if (outputStyle.isNotEmpty) _kvLine(context, 'style', outputStyle),
            if (sessionId.isNotEmpty) _kvLine(context, 'session', sessionId),
          ],
        ),
      );
    }

    if (cwd.isNotEmpty) {
      section('WORKDIR', _kvLine(context, 'cwd', cwd));
    }

    final tools = _SessionHeader._toList(payload['tools']);
    if (tools.isNotEmpty) {
      section('TOOLS · ${tools.length}', _ChipWrap(items: tools));
    }

    final mcp = _SessionHeader._toMapList(payload['mcp_servers']);
    if (mcp.isNotEmpty) {
      section(
        'MCP SERVERS · ${mcp.length}',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final s in mcp) _McpRow(server: s),
          ],
        ),
      );
    }

    final slash = _SessionHeader._toList(payload['slash_commands']);
    if (slash.isNotEmpty) {
      section('SLASH · ${slash.length}', _ChipWrap(items: slash));
    }

    final agents = _SessionHeader._toList(payload['agents']);
    if (agents.isNotEmpty) {
      section('AGENTS · ${agents.length}', _ChipWrap(items: agents));
    }

    final skills = _SessionHeader._toList(payload['skills']);
    if (skills.isNotEmpty) {
      section('SKILLS · ${skills.length}', _ChipWrap(items: skills));
    }

    final plugins = _SessionHeader._toList(payload['plugins']);
    if (plugins.isNotEmpty) {
      section('PLUGINS · ${plugins.length}', _ChipWrap(items: plugins));
    }

    return SafeArea(
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.of(context).size.height * 0.85,
        ),
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: children,
          ),
        ),
      ),
    );
  }

  Widget _kvLine(BuildContext ctx, String k, String v, {Color? valueColor}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: RichText(
        text: TextSpan(
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: isDark
                ? DesignColors.textPrimary
                : DesignColors.textPrimaryLight,
          ),
          children: [
            TextSpan(text: '$k: ', style: TextStyle(color: muted)),
            TextSpan(
              text: v,
              style: valueColor == null ? null : TextStyle(color: valueColor),
            ),
          ],
        ),
      ),
    );
  }
}

class _ChipWrap extends StatelessWidget {
  final List<String> items;
  const _ChipWrap({required this.items});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final fg = isDark
        ? DesignColors.textSecondary
        : DesignColors.textSecondaryLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: [
        for (final it in items)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: border),
            ),
            child: Text(
              it,
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: fg),
            ),
          ),
      ],
    );
  }
}

class _McpRow extends StatelessWidget {
  final Map<String, dynamic> server;
  const _McpRow({required this.server});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final name = (server['name'] ?? '?').toString();
    final status = (server['status'] ?? '').toString();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          Expanded(
            child: Text(
              name,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, color: fg),
            ),
          ),
          if (status.isNotEmpty)
            _Pill(label: status, color: _mcpStatusColor(status)),
        ],
      ),
    );
  }

  static Color _mcpStatusColor(String status) {
    switch (status.toLowerCase()) {
      case 'connected':
      case 'ok':
        return DesignColors.success;
      case 'needs-auth':
      case 'pending-auth':
        return DesignColors.warning;
      case 'failed':
      case 'error':
        return DesignColors.error;
      default:
        return DesignColors.textMuted;
    }
  }
}

/// Compact telemetry strip rendered between the session header and the
/// feed. Three signals: cumulative cost (summed turn.result.cost_usd),
/// most-recent turn token usage, and rate-limit window progress. The
/// strip is only mounted when at least one of these has data so the
/// chrome doesn't sit empty before the first turn completes.
///
/// Tap → bottom-sheet with a per-model breakdown when by_model lands;
/// for now keeps the strip compact and tap-inert (the data fits in one
/// row at typical phone widths).
/// Aggregated token totals for one model across all turn.result frames.
/// Mutable so the build-time aggregation loop can fold each frame in
/// without rebuilding the map every event.
class _ModelTokens {
  int input = 0;
  int output = 0;
  int cacheRead = 0;
  int cacheCreate = 0;
  double costUsd = 0.0;
  // Static per-model capacity carried by claude's modelUsage and
  // codex's tokenUsage.modelContextWindow. Driver normalizes both
  // into `context_window` on the wire. 0 = unknown/not reported.
  int contextWindow = 0;
  int maxOutputTokens = 0;
  // Latest per-call input + cache totals (NOT cumulative), used to
  // estimate current context-window utilization. claude's `result`
  // frame ships these as cumulative within the run, so this matches
  // what claude itself shows in its TUI: "what was loaded for the
  // most recent message."
  int latestInput = 0;
  int latestCacheRead = 0;
  int latestCacheCreate = 0;

  static _ModelTokens empty() => _ModelTokens();

  void add(Map<String, dynamic> v) {
    final i = (v['input'] as num?)?.toInt() ?? 0;
    final o = (v['output'] as num?)?.toInt() ?? 0;
    final cr = (v['cache_read'] as num?)?.toInt() ?? 0;
    final cc = (v['cache_create'] as num?)?.toInt() ?? 0;
    final c = (v['cost_usd'] as num?)?.toDouble() ?? 0.0;
    input += i;
    output += o;
    cacheRead += cr;
    cacheCreate += cc;
    costUsd += c;
    // Static metadata — overwrite (not sum). The driver carries
    // these per-model on every turn.result; the latest non-zero
    // wins so a model swap mid-session updates the capacity.
    final cw = (v['context_window'] as num?)?.toInt() ?? 0;
    if (cw > 0) contextWindow = cw;
    final mo = (v['max_output_tokens'] as num?)?.toInt() ?? 0;
    if (mo > 0) maxOutputTokens = mo;
    // Latest-turn snapshot — overwrites each call so a single
    // backward walk (or sequential add()) leaves the trailing
    // values intact.
    latestInput = i;
    latestCacheRead = cr;
    latestCacheCreate = cc;
  }

  // Total billable input = fresh input + cache writes (cache reads are
  // billed at a 10% rate at most providers, so callers can show them
  // separately rather than rolling them into the headline number).
  int get billableInput => input + cacheCreate;
}

class _TelemetryStrip extends StatelessWidget {
  final double totalCostUsd;
  final int turnCount;
  final Map<String, _ModelTokens> modelTotals;
  final Map<String, dynamic>? rateLimit;
  // Context window: total capacity and current used. Codex sources
  // the pair from `thread/tokenUsage/updated` (`modelContextWindow` +
  // cumulative `total_tokens`); claude sources it from the dominant
  // model in `result.modelUsage` — `contextWindow` for capacity and
  // the latest turn's `inputTokens + cacheReadInputTokens +
  // cacheCreationInputTokens` for "used" (matches what claude's TUI
  // statusline shows for the most recent message). The tile
  // suppresses itself when capacity is null/zero.
  final int? contextWindow;
  final int? contextUsed;
  const _TelemetryStrip({
    required this.totalCostUsd,
    required this.turnCount,
    required this.modelTotals,
    required this.rateLimit,
    this.contextWindow,
    this.contextUsed,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final tiles = <Widget>[];
    // Cost tile is hidden when totalCostUsd is exactly zero — codex's
    // turn/completed notification doesn't carry cost, so a codex
    // session would otherwise render `$0.0000 · N turns`, which reads
    // as "we ran for free" rather than "we don't know what it cost."
    if (turnCount > 0 && totalCostUsd > 0) {
      tiles.add(_TelemetryTile(
        icon: Icons.payments_outlined,
        label: '\$${totalCostUsd.toStringAsFixed(4)}',
        sub: '$turnCount turn${turnCount == 1 ? '' : 's'}',
        color: DesignColors.success,
        fg: fg,
        muted: mutedColor,
        tooltip:
            'Cumulative cost across $turnCount completed turn${turnCount == 1 ? '' : 's'}.',
      ));
    }
    if (modelTotals.isNotEmpty) {
      // Aggregate across all models — this is what the user actually
      // pays for. Headline shows ↑ billable_in / ↓ out; the cache_read
      // total goes in the sub line because it's billed at a fraction of
      // the input rate and conflating them inflates the number.
      var totalBillableIn = 0;
      var totalOut = 0;
      var totalCacheRead = 0;
      modelTotals.forEach((_, t) {
        totalBillableIn += t.billableInput;
        totalOut += t.output;
        totalCacheRead += t.cacheRead;
      });
      final tooltip = StringBuffer()
        ..write('Session-wide token usage across ')
        ..write(modelTotals.length)
        ..write(modelTotals.length == 1 ? ' model' : ' models')
        ..write(':\n');
      modelTotals.forEach((name, t) {
        tooltip
          ..write('• ')
          ..write(_shortModelName(name))
          ..write(': ↑ ')
          ..write(t.billableInput)
          ..write(' (in ')
          ..write(t.input)
          ..write(' + cache_create ')
          ..write(t.cacheCreate)
          ..write(') → ↓ ')
          ..write(t.output)
          ..write('  ·  cache_read ')
          ..write(t.cacheRead)
          ..write('\n');
      });
      tooltip.write(
          '↑ = billable input (fresh + cache writes). ↓ = output. '
          'cache_read is billed at a fraction of input cost so it sits in the sub-line.');
      // Single combined arrow icon keeps the tile narrow; the up/down
      // arrows in the headline carry the directional read.
      tiles.add(_TelemetryTile(
        icon: Icons.swap_vert,
        label:
            '↑${_fmtTokens(totalBillableIn)}  ↓${_fmtTokens(totalOut)}',
        sub: totalCacheRead > 0
            ? 'cache ${_fmtTokens(totalCacheRead)}'
            : '${modelTotals.length} model${modelTotals.length == 1 ? '' : 's'}',
        color: DesignColors.terminalCyan,
        fg: fg,
        muted: mutedColor,
        tooltip: tooltip.toString(),
      ));
    }
    // Context-window tile: used / total + percent. Mirrors what
    // codex's TUI statusline shows so a session running in the
    // background can be checked at a glance without re-attaching to
    // the terminal. Color tracks fill: green < 70%, amber 70-90%,
    // red > 90% — past 90% the next big response will spill, which
    // is the threshold to summarize/compact.
    final cw = contextWindow;
    final cu = contextUsed;
    if (cw != null && cw > 0) {
      final used = cu ?? 0;
      final pct = (used / cw).clamp(0.0, 1.0);
      final pctStr = '${(pct * 100).toStringAsFixed(0)}%';
      final color = pct >= 0.9
          ? DesignColors.error
          : pct >= 0.7
              ? Colors.orange
              : DesignColors.success;
      tiles.add(_TelemetryTile(
        icon: Icons.donut_large,
        label: '${_fmtTokens(used)}/${_fmtTokens(cw)}',
        sub: pctStr,
        color: color,
        fg: fg,
        muted: mutedColor,
        tooltip:
            'Context window utilization: $used / $cw tokens ($pctStr).\n'
            'Past ~90% the next response will spill — a good moment to '
            'summarize or branch a fresh thread.',
      ));
    }
    final rl = rateLimit;
    if (rl != null) {
      final win = (rl['window'] ?? '').toString();
      final status = (rl['status'] ?? '').toString();
      final resetsAtRaw = (rl['resets_at'] ?? '').toString();
      final resetIn = _resetIn(resetsAtRaw);
      // If we have nothing useful to show — no window label, no parseable
      // reset, no status — suppress the tile entirely. Previous default
      // ("rate / window") was confusing and looked like a stuck UI.
      final hasUsefulContent =
          win.isNotEmpty || resetIn != null || status.isNotEmpty;
      if (hasUsefulContent) {
        final color = _rateLimitColor(status, resetIn);
        final label = win.isNotEmpty ? _humanWindow(win) : 'rate';
        final sub = resetIn != null
            ? 'resets ${_fmtCountdown(resetIn)}'
            : (status.isNotEmpty ? status : '—');
        tiles.add(_TelemetryTile(
          icon: Icons.av_timer,
          label: label,
          sub: sub,
          color: color,
          fg: fg,
          muted: mutedColor,
          tooltip:
              'Rate-limit window'
              '${win.isEmpty ? '' : ' ($win)'}'
              '. Claude tracks usage in two rolling windows (5h and weekly); '
              'the label names which one this status applies to.'
              '${status.isEmpty ? '' : '\nStatus: $status.'}'
              '${resetIn == null ? '' : '\nResets in ${_fmtCountdown(resetIn).replaceFirst('in ', '')}.'}',
        ));
      }
    }
    if (tiles.isEmpty) return const SizedBox.shrink();
    return Container(
      decoration: BoxDecoration(
        color: bg,
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Row(
        children: [
          for (var i = 0; i < tiles.length; i++) ...[
            Expanded(child: tiles[i]),
            if (i < tiles.length - 1)
              Container(
                width: 1,
                height: 24,
                color: border,
                margin: const EdgeInsets.symmetric(horizontal: 8),
              ),
          ],
        ],
      ),
    );
  }

  // Trim claude/codex model strings down for the tooltip per-model
  // breakdown ("claude-opus-4-7-20260101" → "opus 4.7"). Mirrors the
  // AppBar SessionInitChip's shortener; kept local to the strip so
  // the two callers stay decoupled.
  static String _shortModelName(String raw) {
    if (raw.startsWith('claude-')) {
      final parts = raw.split('-');
      if (parts.length >= 4) return '${parts[1]} ${parts[2]}.${parts[3]}';
    }
    return raw;
  }

  static String _fmtTokens(int? n) {
    if (n == null) return '—';
    if (n < 1000) return '$n';
    if (n < 1000000) {
      final k = n / 1000.0;
      return k >= 10
          ? '${k.toStringAsFixed(0)}k'
          : '${k.toStringAsFixed(1)}k';
    }
    final m = n / 1000000.0;
    return '${m.toStringAsFixed(1)}M';
  }

  // Parse the reset-at timestamp and return time-until as a Duration.
  // Accepts either an ISO-8601 string (Anthropic stream-json's typical
  // shape) or a numeric Unix epoch (claude has emitted both depending
  // on version, and the numeric form has been seen in seconds, ms, µs,
  // and ns across SDK versions — different libs pick whichever unit the
  // upstream HTTP header uses verbatim). Returns null if the timestamp
  // is empty, unparseable, or resolves to something nonsensically far
  // in the future (which previously rendered as "resets in 1540333567h"
  // when a µs-precision value got read as ms).
  static Duration? _resetIn(String raw) {
    if (raw.isEmpty) return null;
    DateTime? ts = DateTime.tryParse(raw);
    if (ts == null) {
      // Numeric epoch fallback. Pick the unit by magnitude — for any
      // reset within ~50 years of now, the magnitude buckets don't
      // overlap, so the heuristic is unambiguous:
      //   < 1e11  ⇒ seconds  (year 2286 in seconds)
      //   < 1e14  ⇒ ms       (year 2286 in ms)
      //   < 1e17  ⇒ µs       (year 2286 in µs)
      //   else    ⇒ ns
      var n = int.tryParse(raw);
      n ??= double.tryParse(raw)?.toInt();
      if (n != null && n > 0) {
        int ms;
        if (n < 100000000000) {
          ms = n * 1000;
        } else if (n < 100000000000000) {
          ms = n;
        } else if (n < 100000000000000000) {
          ms = n ~/ 1000;
        } else {
          ms = n ~/ 1000000;
        }
        ts = DateTime.fromMillisecondsSinceEpoch(ms, isUtc: true);
      }
    }
    if (ts == null) return null;
    final diff = ts.difference(DateTime.now().toUtc());
    if (diff.isNegative) return Duration.zero;
    // Sanity bound: rate-limit windows reset within hours, never weeks.
    // A diff this far out means we still misinterpreted the unit (or
    // upstream sent garbage); show nothing rather than render a number
    // the user can't make sense of.
    if (diff.inDays > 7) return null;
    return diff;
  }

  static String _fmtCountdown(Duration d) {
    if (d.inMinutes < 1) return 'now';
    if (d.inHours < 1) return 'in ${d.inMinutes}m';
    final m = d.inMinutes % 60;
    return m == 0 ? 'in ${d.inHours}h' : 'in ${d.inHours}h ${m}m';
  }

  // Status drives color when present; otherwise fall back to time-pressure
  // heuristic so a "warn" status near the reset doesn't read green.
  // `allowed` is what Anthropic ships in the wild today
  // (rate_limit_event.status="allowed" — see hub-runner driver_stdio.go);
  // alias it to the green case so the most-common steady-state reads
  // OK rather than a muted gray.
  static Color _rateLimitColor(String status, Duration? resetIn) {
    switch (status.toLowerCase()) {
      case 'limited':
      case 'exceeded':
      case 'denied':
        return DesignColors.error;
      case 'warn':
      case 'warning':
        return DesignColors.warning;
      case 'ok':
      case 'available':
      case 'allowed':
        return DesignColors.success;
    }
    if (resetIn != null && resetIn.inMinutes <= 5) {
      return DesignColors.warning;
    }
    return DesignColors.textMuted;
  }
}

class _TelemetryTile extends StatelessWidget {
  final IconData icon;
  final String label;
  final String sub;
  final Color color;
  final Color fg;
  final Color muted;
  final String? tooltip;
  const _TelemetryTile({
    required this.icon,
    required this.label,
    required this.sub,
    required this.color,
    required this.fg,
    required this.muted,
    this.tooltip,
  });

  @override
  Widget build(BuildContext context) {
    final row = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 14, color: color),
        const SizedBox(width: 6),
        Flexible(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                label,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  color: fg,
                ),
              ),
              Text(
                sub,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 9,
                  color: muted,
                ),
              ),
            ],
          ),
        ),
      ],
    );
    final t = tooltip;
    if (t == null || t.isEmpty) return row;
    return Tooltip(
      message: t,
      waitDuration: const Duration(milliseconds: 250),
      preferBelow: true,
      child: row,
    );
  }
}

// Map raw rate-limit window strings (whatever claude emits — `5_hour`,
// `5h`, `five_hour`, `weekly`, `week`, `session`, etc.) to a short human
// label. Unknown values pass through verbatim so we never hide signal.
//
// Anthropic's stream-json `rate_limit_event.rateLimitType` ships
// english-spelled forms ("five_hour", "one_hour") today; the
// underscore-numeric variants ("5_hour") show up on older clients.
// Matching both keeps the strip readable across versions.
String _humanWindow(String raw) {
  switch (raw.toLowerCase()) {
    case '5h':
    case '5_hour':
    case '5_hours':
    case 'five_hour':
    case 'five_hours':
    case 'session':
      return '5h';
    case '1h':
    case '1_hour':
    case '1_hours':
    case 'one_hour':
    case 'one_hours':
      return '1h';
    case 'weekly':
    case 'week':
    case '7d':
    case 'weekly_opus':
      return 'weekly';
  }
  return raw;
}

/// Inline tool_result block rendered inside a tool_call card. Reuses
/// the same collapsible-mono content rendering as the standalone
/// tool_result card, but framed with a left-rail accent so the lineage
/// from input → output reads at a glance.
class _ToolResultInline extends StatelessWidget {
  final Map<String, dynamic> payload;
  final bool isError;
  const _ToolResultInline({required this.payload, required this.isError});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final accent =
        isError ? DesignColors.error : DesignColors.success;
    final content = payload['content'];
    final text = content is String ? content : AgentEventCard._jsonPretty(content);
    return Container(
      decoration: BoxDecoration(
        border: Border(left: BorderSide(color: accent, width: 2)),
      ),
      padding: const EdgeInsets.fromLTRB(8, 4, 0, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Icon(
                isError ? Icons.error_outline : Icons.check_circle_outline,
                size: 12,
                color: accent,
              ),
              const SizedBox(width: 4),
              Text(
                isError ? 'result · error' : 'result',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  color: mutedColor,
                  letterSpacing: 0.5,
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          _CollapsibleMono(
            text: text,
            color: isError ? DesignColors.error : null,
          ),
        ],
      ),
    );
  }
}

/// ADR-021 W2.5 — captured mode + model state advertised by the agent
/// plus the bound picker callbacks. Lifted out of [AgentFeed] so the
/// SessionChatScreen AppBar can host the picker icon — without it the
/// chip strip cost a row of vertical space above every transcript even
/// for engines that never re-advertise mode/model after handshake.
///
/// `currentMode` / `currentModel` are nullable because some engines
/// only advertise one of the two (e.g. claude exposes model but not a
/// runtime "mode" concept). availableModes / availableModels each list
/// `{id, name, description?}` maps mirroring the ACP shape.
class ModeModelPickerData {
  final String? currentMode;
  final List<Map<String, dynamic>> availableModes;
  final String? currentModel;
  final List<Map<String, dynamic>> availableModels;
  final Future<void> Function(String modeId) onPickMode;
  final Future<void> Function(String modelId) onPickModel;
  const ModeModelPickerData({
    required this.currentMode,
    required this.availableModes,
    required this.currentModel,
    required this.availableModels,
    required this.onPickMode,
    required this.onPickModel,
  });
  bool get hasMode => currentMode != null && availableModes.isNotEmpty;
  bool get hasModel => currentModel != null && availableModels.isNotEmpty;
  bool get hasAny => hasMode || hasModel;

  // Friendly label for the AppBar icon's tooltip / chip subtitle.
  // Falls back to the id when no `name` is advertised.
  String? _labelFor(String? currentId, List<Map<String, dynamic>> options) {
    if (currentId == null) return null;
    for (final o in options) {
      if (o['id']?.toString() == currentId) {
        final name = (o['name'] ?? '').toString();
        return name.isNotEmpty ? name : currentId;
      }
    }
    return currentId;
  }

  String? get currentModeLabel => _labelFor(currentMode, availableModes);
  String? get currentModelLabel => _labelFor(currentModel, availableModels);
}

/// Opens a single bottom-sheet listing both mode and model options
/// (whichever the agent advertises) so the SessionChatScreen AppBar
/// icon collapses to one tap. Each section header is suppressed when
/// that capability is absent. Selecting a row pops the sheet and fires
/// the matching `onPick*` callback — caller's responsibility to surface
/// any error via SnackBar.
Future<void> showModeModelPickerSheet(
  BuildContext context,
  ModeModelPickerData data,
) {
  return showModalBottomSheet<void>(
    context: context,
    showDragHandle: true,
    builder: (sheetCtx) {
      return SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (data.hasMode) ...[
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
                child: Text(
                  'Mode',
                  style: Theme.of(sheetCtx).textTheme.titleSmall,
                ),
              ),
              for (final opt in data.availableModes)
                ListTile(
                  leading: const Icon(Icons.tune, size: 18),
                  title: Text((opt['name'] ?? opt['id'] ?? '').toString()),
                  subtitle: opt['description'] != null
                      ? Text((opt['description']).toString())
                      : null,
                  trailing: (opt['id']?.toString() == data.currentMode)
                      ? const Icon(Icons.check, size: 18)
                      : null,
                  onTap: () {
                    final id = opt['id']?.toString() ?? '';
                    Navigator.of(sheetCtx).pop();
                    if (id.isNotEmpty && id != data.currentMode) {
                      data.onPickMode(id);
                    }
                  },
                ),
            ],
            if (data.hasMode && data.hasModel) const Divider(height: 1),
            if (data.hasModel) ...[
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
                child: Text(
                  'Model',
                  style: Theme.of(sheetCtx).textTheme.titleSmall,
                ),
              ),
              for (final opt in data.availableModels)
                ListTile(
                  leading: const Icon(Icons.psychology_alt, size: 18),
                  title: Text((opt['name'] ?? opt['id'] ?? '').toString()),
                  subtitle: opt['description'] != null
                      ? Text((opt['description']).toString())
                      : null,
                  trailing: (opt['id']?.toString() == data.currentModel)
                      ? const Icon(Icons.check, size: 18)
                      : null,
                  onTap: () {
                    final id = opt['id']?.toString() ?? '';
                    Navigator.of(sheetCtx).pop();
                    if (id.isNotEmpty && id != data.currentModel) {
                      data.onPickModel(id);
                    }
                  },
                ),
            ],
            const SizedBox(height: 4),
          ],
        ),
      );
    },
  );
}

// AgentFeed — mobile renderer for the hub's agent_events stream
// (blueprint P2.1). Subscribes to SSE, backfills any seq the user
// missed, and lays each event out as a typed card (text, tool_call,
// tool_result, completion, lifecycle, …). Unknown kinds fall through
// to a raw JSON card so the transcript is never silently dropped.
import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../services/hub/hub_client.dart';
import '../theme/design_colors.dart';
import 'agent_compose.dart';

/// Renders a live, scrollable feed of agent_events for [agentId]. Keeps
/// its own seq cursor so reconnects don't replay the whole history. The
/// first frame is the in-DB backfill fetched via listAgentEvents; after
/// that, new frames arrive through streamAgentEvents.
class AgentFeed extends ConsumerStatefulWidget {
  final String agentId;
  final EdgeInsetsGeometry padding;
  const AgentFeed({
    super.key,
    required this.agentId,
    this.padding = const EdgeInsets.all(12),
  });

  @override
  ConsumerState<AgentFeed> createState() => _AgentFeedState();
}

class _AgentFeedState extends ConsumerState<AgentFeed> {
  final List<Map<String, dynamic>> _events = [];
  int _maxSeq = 0;
  String? _error;
  bool _loading = true;
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
  void _onScroll() {
    if (!_scroll.hasClients) return;
    final atBottom = _scroll.position.pixels >=
        _scroll.position.maxScrollExtent - 40;
    if (_followTail != atBottom) {
      setState(() {
        _followTail = atBottom;
        // Returning to the tail clears the pending-event counter; the
        // pill disappears on the same frame.
        if (atBottom) _newWhileAway = 0;
      });
    }
  }

  void _jumpToLatest() {
    _scrollToTail();
    setState(() {
      _followTail = true;
      _newWhileAway = 0;
    });
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
    try {
      final backfill = await client.listAgentEvents(widget.agentId, limit: 500);
      if (!mounted) return;
      _events
        ..clear()
        ..addAll(backfill);
      _maxSeq = _events.fold<int>(0, (m, e) {
        final seq = (e['seq'] as num?)?.toInt() ?? 0;
        return seq > m ? seq : m;
      });
      setState(() => _loading = false);
      _subscribe(client);
      // Give the first-frame layout a tick, then pin to the tail.
      WidgetsBinding.instance.addPostFrameCallback((_) => _scrollToTail());
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

  void _subscribe(HubClient client) {
    _reconnectTimer?.cancel();
    _sub?.cancel();
    _sub = client
        .streamAgentEvents(widget.agentId, sinceSeq: _maxSeq)
        .listen((evt) {
      if (!mounted) return;
      final seq = (evt['seq'] as num?)?.toInt() ?? 0;
      // The hub replays from `since` inclusive on some endpoints; guard
      // against dupes deterministically by seq.
      if (seq > 0 && seq <= _maxSeq) return;
      // First successful delivery after a drop clears the banner and the
      // backoff counter so the next drop starts over at 1s.
      final clearedError = _error != null;
      setState(() {
        _events.add(evt);
        if (seq > _maxSeq) _maxSeq = seq;
        if (clearedError) _error = null;
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
      _scheduleReconnect(client, reason: '$e');
    }, onDone: () {
      _scheduleReconnect(client, reason: 'stream closed');
    });
  }

  void _scheduleReconnect(HubClient client, {required String reason}) {
    if (!mounted) return;
    // Cap at 16s — fast enough that a recovered hub is picked up quickly,
    // slow enough that a genuinely-down hub doesn't get hammered.
    final delaySecs = math.min(16, 1 << _reconnectAttempt);
    _reconnectAttempt += 1;
    // Schedule the banner grace-period instead of showing immediately.
    // A successful resubscribe within [_bannerGrace] cancels this timer
    // and the user never sees the drop. Repeated drops within the same
    // window leave the original timer in place so the user sees one
    // banner, not flicker.
    if (_bannerGraceTimer == null || !_bannerGraceTimer!.isActive) {
      _bannerGraceTimer = Timer(_bannerGrace, () {
        if (!mounted) return;
        setState(() => _error =
            'Stream dropped ($reason) · retrying in ${delaySecs}s');
      });
    }
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(Duration(seconds: delaySecs), () {
      if (!mounted) return;
      _subscribe(client);
    });
  }

  void _scrollToTail() {
    if (!_scroll.hasClients) return;
    _scroll.jumpTo(_scroll.position.maxScrollExtent);
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
    final composeSlash = _stringList(initForCompose?['slash_commands']);
    final composeMentions = <String>[
      ..._stringList(initForCompose?['agents']),
      ..._stringList(initForCompose?['tools']),
      ..._stringList(initForCompose?['skills']),
    ];
    final compose = AgentCompose(
      agentId: widget.agentId,
      slashCommands: composeSlash,
      mentions: composeMentions,
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
    // Sticky header pulls from the latest session.init (a steward
    // that reconnects can produce more than one). The composer above
    // already plucked the same payload — kept distinct so a future
    // header-only optimization stays local.
    final sessionInit = initForCompose;
    // Telemetry strip inputs (W-UI-3): cumulative cost from all
    // turn.result events, latest per-message usage block, latest
    // rate_limit. We walk forward so latest-wins reflects ordering;
    // cost is summed because each turn contributes once.
    double totalCostUsd = 0.0;
    Map<String, dynamic>? latestUsage;
    Map<String, dynamic>? latestRateLimit;
    int turnCount = 0;
    for (final e in _events) {
      final kind = (e['kind'] ?? '').toString();
      final p = e['payload'];
      if (p is! Map) continue;
      if (kind == 'turn.result') {
        turnCount += 1;
        final c = p['cost_usd'];
        if (c is num) totalCostUsd += c.toDouble();
      } else if (kind == 'usage') {
        latestUsage = p.cast<String, dynamic>();
      } else if (kind == 'rate_limit') {
        latestRateLimit = p.cast<String, dynamic>();
      }
    }
    final hasTelemetry = turnCount > 0 ||
        latestUsage != null ||
        latestRateLimit != null;
    // Build the visible event list: drop folded-in kinds.
    //   tool_call_update — folded into parent tool_call card.
    //   tool_result      — paired with parent tool_call by tool_use_id;
    //                      orphaned results (no matching call) still render
    //                      so a one-off tool_result isn't silently swallowed.
    //   session.init     — surfaced in the sticky header above.
    //   debug-only kinds — gated by _verbose toggle (W1.B).
    final visible = <Map<String, dynamic>>[
      for (final e in _events)
        if (!_isHiddenInFeed(e, toolNames)) e,
    ];
    // Count the verbose-gated events so the toggle can advertise its
    // value — "Show debug (12)" carries more signal than a bare button.
    int hiddenForVerbose = 0;
    if (!_verbose) {
      for (final e in _events) {
        final kind = (e['kind'] ?? '').toString();
        if (_isVerboseOnly(kind, e['payload'])) hiddenForVerbose++;
      }
    }
    return Column(
      children: [
        if (sessionInit != null) _SessionHeader(payload: sessionInit),
        if (hasTelemetry)
          _TelemetryStrip(
            totalCostUsd: totalCostUsd,
            turnCount: turnCount,
            usage: latestUsage,
            rateLimit: latestRateLimit,
          ),
        if (_verbose || hiddenForVerbose > 0)
          _VerboseToggleBar(
            verbose: _verbose,
            hiddenCount: hiddenForVerbose,
            onToggle: () => setState(() => _verbose = !_verbose),
          ),
        Expanded(
          child: Stack(
            children: [
              ListView.separated(
                controller: _scroll,
                padding: widget.padding,
                itemCount: visible.length,
                separatorBuilder: (_, _) => const SizedBox(height: 8),
                itemBuilder: (ctx, i) => AgentEventCard(
                  event: visible[i],
                  toolNames: toolNames,
                  toolUpdates: toolUpdates,
                  toolResults: toolResults,
                  resolvedApprovals: resolvedApprovals,
                  agentId: widget.agentId,
                ),
              ),
              // Inline approval cards (W1.A): when the agent has called
              // permission_prompt for a tier ≥ significant tool, the hub
              // posts an open attention_item. Pin it to the bottom of the
              // event list so the user sees it in context with the latest
              // turn — the agent is paused waiting for a decision, and
              // hiding the card behind a tab would invert the urgency.
              const Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: _PendingPermissionPrompts(),
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
              if (!_followTail && _newWhileAway > 0)
                Positioned(
                  left: 0,
                  right: 0,
                  bottom: 12,
                  child: Center(
                    child: _NewEventsPill(
                      count: _newWhileAway,
                      onTap: _jumpToLatest,
                    ),
                  ),
                ),
            ],
          ),
        ),
        compose,
      ],
    );
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
  //   input.*        — user-typed input echo; the user's own message is
  //                    already on screen via the compose box, no need to
  //                    show it twice as a structured event card.
  // All of these are revealed when [_verbose] is true.
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
    'input.text',
    'input.cancel',
    'input.approval',
    'input.attach',
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
}

/// One-line bar above the event list: shows the verbose toggle and,
/// when off, how many events are currently hidden by it. Mirrors the
/// "show raw" toggle in claude-code's terminal (Ctrl+O) — by default
/// the transcript reads as a chat surface; flip to see the debug
/// stream when something looks wrong.
class _VerboseToggleBar extends StatelessWidget {
  final bool verbose;
  final int hiddenCount;
  final VoidCallback onToggle;
  const _VerboseToggleBar({
    required this.verbose,
    required this.hiddenCount,
    required this.onToggle,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final label = verbose
        ? 'Hide debug events'
        : (hiddenCount > 0
            ? 'Show debug ($hiddenCount hidden)'
            : 'Show debug');
    return Container(
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Row(
        children: [
          Icon(
            verbose ? Icons.visibility : Icons.visibility_off_outlined,
            size: 14,
            color: muted,
          ),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              verbose
                  ? 'Showing all events (lifecycle, raw, system, input echoes)'
                  : 'Hiding lifecycle, raw, system, input echoes',
              style: GoogleFonts.jetBrainsMono(fontSize: 10, color: muted),
            ),
          ),
          TextButton(
            onPressed: onToggle,
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              minimumSize: const Size(0, 28),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              foregroundColor: muted,
            ),
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
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

class _NewEventsPill extends StatelessWidget {
  final int count;
  final VoidCallback onTap;
  const _NewEventsPill({required this.count, required this.onTap});

  @override
  Widget build(BuildContext context) {
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
                '$count new',
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
class AgentEventCard extends StatelessWidget {
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
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final kind = (event['kind'] ?? '').toString();
    final producer = (event['producer'] ?? 'agent').toString();
    final payload = (event['payload'] is Map)
        ? (event['payload'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};

    final accent = _accentFor(kind, producer);
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
              ts: event['ts']?.toString()),
          const SizedBox(height: 6),
          _body(context, kind, producer, payload),
        ],
      ),
    );
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
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _kv(ctx, 'tool', name),
        if (id.isNotEmpty) _kv(ctx, 'id', id),
        _kv(ctx, 'status', status, valueColor: _statusColor(status)),
        if (input != null) _CollapsibleMono(text: _jsonPretty(input)),
        if (preview != null && preview.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: _CollapsibleMono(text: preview),
          ),
        if (hasResult)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: _ToolResultInline(
              payload: resultPayload,
              isError: resultIsError,
            ),
          ),
      ],
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
    final buf = StringBuffer();
    int adds = 0;
    int dels = 0;
    // Naive line-aligned diff: walk both lists in parallel. Adequate for
    // the preview — the hub stores the authoritative texts, the operator
    // can pull them on a real screen later.
    final maxLen = math.max(oldLines.length, newLines.length);
    for (var i = 0; i < maxLen; i++) {
      final o = i < oldLines.length ? oldLines[i] : null;
      final n = i < newLines.length ? newLines[i] : null;
      if (o == n) {
        buf.writeln('  ${o ?? ''}');
      } else {
        if (o != null) {
          buf.writeln('- $o');
          dels++;
        }
        if (n != null) {
          buf.writeln('+ $n');
          adds++;
        }
      }
    }
    final summary = '+$adds / -$dels';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (path.isNotEmpty) _kv(ctx, 'path', path),
        _kv(ctx, 'change', summary),
        if (buf.isNotEmpty) _CollapsibleMono(text: buf.toString().trimRight()),
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
      data: s,
      selectable: true,
      shrinkWrap: true,
      // Keep paragraph and block spacing tight so cards don't balloon.
      styleSheet: MarkdownStyleSheet(
        p: base,
        a: base.copyWith(color: DesignColors.primary),
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

  // Known ACP tool_call statuses + the synthetic "pending" we apply when
  // the tool_result hasn't arrived yet. Colored so the feed is scannable
  // without reading every status string.
  Color? _statusColor(String s) {
    switch (s) {
      case 'failed':
        return DesignColors.error;
      case 'completed':
        return DesignColors.success;
      case 'in_progress':
        return DesignColors.terminalCyan;
      case 'pending':
        return DesignColors.warning;
      default:
        return null;
    }
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

class _CardHeader extends StatelessWidget {
  final String kind;
  final String producer;
  final Color accent;
  final String? ts;
  const _CardHeader(
      {required this.kind,
      required this.producer,
      required this.accent,
      required this.ts});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Row(
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
      ],
    );
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

/// Sticky header rendered above the agent feed when a session.init event
/// is present. Compact by default — tap to open a bottom-sheet drawer
/// with the rich session metadata (model, tools, mcp servers, slash
/// commands, agents, skills, cwd, version, permission mode).
///
/// Built from typed `session.init` payload, not claude JSON. Other
/// drivers can populate the same fields and inherit this UI for free;
/// fields they don't surface stay absent rather than showing as blanks.
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
  const _SessionDetailsSheet({required this.payload});

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
    if (modelLine.isNotEmpty || permMode.isNotEmpty || outputStyle.isNotEmpty) {
      section(
        'AGENT',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
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
class _TelemetryStrip extends StatelessWidget {
  final double totalCostUsd;
  final int turnCount;
  final Map<String, dynamic>? usage;
  final Map<String, dynamic>? rateLimit;
  const _TelemetryStrip({
    required this.totalCostUsd,
    required this.turnCount,
    required this.usage,
    required this.rateLimit,
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
    if (turnCount > 0) {
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
    final u = usage;
    if (u != null) {
      final inTok = (u['input_tokens'] as num?)?.toInt();
      final outTok = (u['output_tokens'] as num?)?.toInt();
      final cacheRead = (u['cache_read'] as num?)?.toInt();
      final headline = '${_fmtTokens(inTok)} → ${_fmtTokens(outTok)}';
      final sub = cacheRead != null && cacheRead > 0
          ? 'cache ${_fmtTokens(cacheRead)}'
          : 'in → out tokens';
      tiles.add(_TelemetryTile(
        icon: Icons.bolt_outlined,
        label: headline,
        sub: sub,
        color: DesignColors.terminalCyan,
        fg: fg,
        muted: mutedColor,
        tooltip:
            'Last assistant turn: ${inTok ?? 0} input tokens → ${outTok ?? 0} output tokens'
            '${cacheRead != null && cacheRead > 0 ? ' (prompt cache: $cacheRead)' : ''}.\n'
            'Suffix is k = thousand, M = million.',
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
  // shape) or a numeric Unix epoch in seconds (also seen in the wild —
  // claude has emitted both depending on version). Returns null if the
  // timestamp is empty or unparseable so the strip falls back to status.
  static Duration? _resetIn(String raw) {
    if (raw.isEmpty) return null;
    DateTime? ts = DateTime.tryParse(raw);
    if (ts == null) {
      // Numeric epoch fallback. Heuristic: <1e12 ⇒ seconds, ≥1e12 ⇒ ms.
      final secondsOrMs = int.tryParse(raw);
      if (secondsOrMs != null && secondsOrMs > 0) {
        ts = secondsOrMs < 1000000000000
            ? DateTime.fromMillisecondsSinceEpoch(secondsOrMs * 1000, isUtc: true)
            : DateTime.fromMillisecondsSinceEpoch(secondsOrMs, isUtc: true);
      }
    }
    if (ts == null) return null;
    final diff = ts.difference(DateTime.now().toUtc());
    return diff.isNegative ? Duration.zero : diff;
  }

  static String _fmtCountdown(Duration d) {
    if (d.inMinutes < 1) return 'now';
    if (d.inHours < 1) return 'in ${d.inMinutes}m';
    final m = d.inMinutes % 60;
    return m == 0 ? 'in ${d.inHours}h' : 'in ${d.inHours}h ${m}m';
  }

  // Status drives color when present; otherwise fall back to time-pressure
  // heuristic so a "warn" status near the reset doesn't read green.
  static Color _rateLimitColor(String status, Duration? resetIn) {
    switch (status.toLowerCase()) {
      case 'limited':
      case 'exceeded':
        return DesignColors.error;
      case 'warn':
      case 'warning':
        return DesignColors.warning;
      case 'ok':
      case 'available':
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
// `5h`, `weekly`, `week`, `session`, etc.) to a short human label.
// Unknown values pass through verbatim so we never hide signal.
String _humanWindow(String raw) {
  switch (raw.toLowerCase()) {
    case '5h':
    case '5_hour':
    case '5_hours':
    case 'session':
      return '5h';
    case 'weekly':
    case 'week':
    case '7d':
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

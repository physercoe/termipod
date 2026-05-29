// AgentFeed telemetry strip — the compact cost / token / rate-limit row
// rendered between the session header and the transcript.
//
// Cluster wedge of the agent_feed split (docs/plans/agent-feed-split.md,
// W3). Reads its inputs (reducer outputs + the per-model ModelTokens
// aggregation) as constructor args from the container, which stays the
// authority on the event list. Telemetry-only helpers (_TelemetryTile,
// _fmtTokens, _humanWindow, …) stay private here — only `TelemetryStrip`
// is referenced cross-library (by the container), so only it is public.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';
import 'feed_reducer.dart';
import 'feed_render.dart';

/// Compact telemetry strip rendered between the session header and the
/// feed. Three signals: cumulative cost (summed turn.result.cost_usd),
/// most-recent turn token usage, and rate-limit window progress. The
/// strip is only mounted when at least one of these has data so the
/// chrome doesn't sit empty before the first turn completes.
///
/// Tap → bottom-sheet with a per-model breakdown when by_model lands;
/// for now keeps the strip compact and tap-inert (the data fits in one
/// row at typical phone widths).
class TelemetryStrip extends StatelessWidget {
  final double totalCostUsd;
  final int turnCount;
  final Map<String, ModelTokens> modelTotals;
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
  // ADR-036 D8 chip 1 (W4-a) — process-cumulative USD from the latest
  // claude-code status_line frame's cost.total_cost_usd. Null when no
  // statusLine frame has carried a cost block. Resets to 0 on every
  // process respawn; preserved across /clear and /model swaps within
  // the same process.
  final double? processCostUsd;
  // ADR-036 D8 chip 2 (W4-c) — session-cumulative USD imputed by the
  // hub from sum of agent_events.usage × pricing table. Null when the
  // sessionCost endpoint hasn't responded yet OR when no priced model
  // appeared in the session (degrade-blank per D9). Polled on a 15s
  // cadence in the parent state.
  final double? sessionCostUsdImputed;
  // Full sessionCost endpoint response — drives the W4-c tooltip
  // breakdown (per-model USD/tokens, snapshot_date, missing models).
  // Null when sessionCostUsdImputed is null.
  final Map<String, dynamic>? sessionCostDetail;
  // ADR-036 W5 — rate_limits block from the latest status_line frame,
  // verbatim from the wire: `{five_hour:{used_percentage,resets_at},
  // seven_day:{used_percentage,resets_at}}`. Either sub-block may be
  // absent; the renderer self-gates per window. Null when no
  // status_line has carried a rate_limits block yet.
  final Map<String, dynamic>? rateLimitsFromStatus;
  // ADR-036 W6 — 200K hard-cap alarm. True iff claude has flagged
  // that the next API call's prompt will exceed the plan's hard
  // cap. Renders a red leading tile prompting `/clear`. False (or
  // null upstream) suppresses the tile entirely.
  final bool exceeds200kAlarm;
  const TelemetryStrip({
    required this.totalCostUsd,
    required this.turnCount,
    required this.modelTotals,
    required this.rateLimit,
    this.contextWindow,
    this.contextUsed,
    this.processCostUsd,
    this.sessionCostUsdImputed,
    this.sessionCostDetail,
    this.rateLimitsFromStatus,
    this.exceeds200kAlarm = false,
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
    // ADR-036 W6 — 200K hard-cap alarm. Leading position (left-most)
    // so it lands first in a scan, and red so it earns the attention
    // it deserves. claude's status_line sets `exceeds_200k_tokens`
    // when the next API call's prompt will breach the plan's hard
    // cap; the user's recourse is /clear (rotate to a fresh session
    // within the same process). Tile self-gates on false.
    if (exceeds200kAlarm) {
      tiles.add(_TelemetryTile(
        icon: Icons.warning_amber_rounded,
        label: '200K cap',
        sub: 'consider /clear',
        color: DesignColors.error,
        fg: fg,
        muted: mutedColor,
        tooltip: 'The next API call\'s prompt will exceed the plan\'s '
            '200K-token hard cap (claude-code flag '
            '`exceeds_200k_tokens`). Use /clear to rotate to a fresh '
            'session within this same process — the cost meter '
            'preserves but the conversation context resets, '
            'unblocking the next turn.',
      ));
    }
    // ADR-036 v1.0.706 polish — ONE combined cost tile (was three:
    // turn.result-sourced, statusLine-sourced, hub-imputed). The
    // headline shows the most-live number available; the long-press
    // tooltip carries the other two as cross-check references, plus
    // per-model breakdown when the hub-side detail is loaded.
    //
    // On-device smoke showed process vs session diverging by 10–30%
    // WITHIN a single turn — different measurement scopes (statusLine
    // is per-process, /cost is hub-imputed pricing × usage), both
    // legitimate but confusing rendered side-by-side. The director
    // wants ONE "what does this turn cost me" number; cross-check is
    // a long-press concern, not a glance concern.
    //
    // Headline precedence (most-live first):
    //   1. processCostUsd   — statusLine cost.total_cost_usd, refreshes
    //                         ~every 10s; resets on respawn, preserved
    //                         across /clear (claude-code M4 only)
    //   2. totalCostUsd     — sum of turn.result.cost_usd events;
    //                         the legacy claude / codex path
    //   3. (none — render the "N turns" forward-progress cue)
    //
    // Sub-line is ALWAYS `N turn[s]` per the user's ask — turn count is
    // the second-most-asked-for number after dollars.
    //
    // Engine matrix:
    //   - claude-code M4 → processCostUsd present, totalCostUsd usually
    //                      also present; headline = process
    //   - claude-code M2 → no statusLine; totalCostUsd from turn.result
    //   - codex         → no statusLine, no turn.result cost; turns-only
    //   - antigravity   → same as codex (gemini usageMetadata in-memory)
    final headlineCost = processCostUsd ?? (totalCostUsd > 0 ? totalCostUsd : null);
    if (headlineCost != null || turnCount > 0) {
      final tooltip = StringBuffer();
      if (headlineCost == null) {
        // turns-only fallback (codex / agy)
        tooltip.write('Completed turns this session. Cost not reported '
            'by this engine.');
      } else {
        // Lead with the headline source so the reader knows which
        // number they're looking at. "process" is the most-live scope;
        // "turn-aggregated" is the legacy summed-across-turn-results
        // scope.
        final headlineLabel = processCostUsd != null
            ? 'process (live statusLine)'
            : 'turn-aggregated (sum of turn.result events)';
        tooltip
          ..write('\$')
          ..write(headlineCost.toStringAsFixed(4))
          ..write(' — ')
          ..write(headlineLabel)
          ..write(' across ')
          ..write(turnCount)
          ..write(' turn')
          ..write(turnCount == 1 ? '' : 's')
          ..write('.');
        // Cross-check: any non-headline scope we ALSO have a value for
        // gets surfaced as a reference. The 10–30% divergence the user
        // observed is real — different scopes, different timings —
        // and the tooltip is the right place to explain it without
        // eating glance budget.
        final cross = <String>[];
        if (processCostUsd != null && totalCostUsd > 0 &&
            processCostUsd != headlineCost) {
          cross.add(
              '\$${totalCostUsd.toStringAsFixed(4)} turn-aggregated '
              '(sum of turn.result events; usually trails process by '
              'one frame)');
        }
        if (sessionCostUsdImputed != null &&
            sessionCostUsdImputed != headlineCost) {
          cross.add(
              '\$${sessionCostUsdImputed!.toStringAsFixed(4)} session-imputed '
              '(hub-side: usage events × pricing table; '
              'preserved across resumes)');
        }
        if (cross.isNotEmpty) {
          tooltip.write('\n\nCross-check:');
          for (final c in cross) {
            tooltip..write('\n• ')..write(c);
          }
        }
        // Per-model breakdown ships only when the hub-side detail
        // GET /sessions/{id}/cost has resolved (~15s poll cadence).
        // Reuse the existing composer so the breakdown shape stays in
        // one place; we splice JUST the table portion since the lead
        // header is already written above.
        if (sessionCostDetail != null) {
          final tail = buildSessionCostTooltipFromDetail(
            sessionCostUsdImputed ?? 0,
            sessionCostDetail,
            pair: false,
          );
          // The composer's first line is "$X session — imputed…" which
          // we don't want here (the cross-check above already surfaced
          // the session number); strip everything up to the first
          // double-newline so we keep only the per-model + rate-snapshot
          // sections.
          final breakIdx = tail.indexOf('\n\n');
          if (breakIdx > 0 && breakIdx + 2 < tail.length) {
            tooltip..write('\n\n')..write(tail.substring(breakIdx + 2));
          }
        }
        tooltip.write('\n\nSubscription users aren\'t billed this — '
            'it\'s an estimate against the public API rate sheet.');
      }
      tiles.add(_TelemetryTile(
        // The bolt icon (was on the process tile) tells the user "live".
        // When we're showing turns-only, fall back to autorenew so the
        // tile reads as a progress cue rather than a stale price.
        icon: headlineCost != null
            ? Icons.bolt_outlined
            : Icons.autorenew_outlined,
        label: headlineCost != null
            ? '\$${headlineCost.toStringAsFixed(4)}'
            : '$turnCount',
        sub: headlineCost != null
            ? '$turnCount turn${turnCount == 1 ? '' : 's'}'
            : (turnCount == 1 ? 'turn' : 'turns'),
        color: DesignColors.success,
        fg: fg,
        muted: mutedColor,
        tooltip: tooltip.toString(),
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
    // ADR-036 W5 — rate-limits chip pair from status_line. One tile
    // per window (5h rolling + 7d rolling). Distinct from the legacy
    // single-window rate_limit chip above which serves claude's
    // stream-json rate_limit_event (single window at a time, fires
    // only when a limit hits); status_line.rate_limits ships both
    // windows on every refresh, so we render them ambient.
    final rlStatus = rateLimitsFromStatus;
    if (rlStatus != null) {
      for (final entry in <List<dynamic>>[
        ['five_hour', '5h', '5-hour rolling'],
        ['seven_day', '7d', '7-day rolling'],
      ]) {
        final wireKey = entry[0] as String;
        final shortLabel = entry[1] as String;
        final longLabel = entry[2] as String;
        final w = rlStatus[wireKey];
        if (w is! Map) continue; // self-gate: window absent on this frame
        final pct = (w['used_percentage'] as num?);
        final resetsAt = (w['resets_at'] as num?)?.toInt();
        if (pct == null && resetsAt == null) continue;
        final tier = rateLimitAlarmTier(pct);
        final pctLabel = pct == null
            ? '?'
            : '${pct.toStringAsFixed(0)}%';
        final resetsLabel = formatRateLimitResetsAt(resetsAt);
        // v1.0.704 polish — the sub-line is the compact countdown
        // (e.g. "3h43m"); the tooltip carries the absolute wall-clock
        // ("Mon 03:00") so a long-press on mobile reveals it without
        // eating sub-line width on every render. Empty absolute string
        // (same defensive-inputs as the compact formatter) collapses
        // the line cleanly.
        final resetsAbs = formatRateLimitResetsAtAbsolute(resetsAt);
        tiles.add(_TelemetryTile(
          icon: Icons.timelapse,
          label: '$shortLabel  $pctLabel',
          sub: resetsLabel.isEmpty ? longLabel : resetsLabel,
          color: tier.color,
          fg: fg,
          muted: mutedColor,
          tooltip: 'Anthropic plan limit — $longLabel window'
              '${pct == null ? '' : '\nUsed: $pctLabel'
                  '${tier.severity == 'green' ? '' : ' (${tier.severity} — '
                      '${tier.severity == 'amber' ? '≥80%, slow down' : '≥95%, throttling imminent'})'}'}'
              '${resetsLabel.isEmpty ? '' : '\nResets in: $resetsLabel'}'
              '${resetsAbs.isEmpty ? '' : ' ($resetsAbs)'}'
              '\n\nSource: claude statusLine `rate_limits.$wireKey`; '
              'rolling, not aligned to a clock midnight. Rendered in '
              'device-local time per ADR-036 D7.',
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

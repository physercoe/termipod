// AgentFeed shared render primitives.
//
// Layer 1 of the agent_feed split (docs/plans/agent-feed-split.md, W1).
// Only the genuinely CROSS-CLUSTER render primitives live here — the
// pieces drawn by more than one of the feed's card clusters (or by the
// container plus the telemetry strip). Single-cluster helpers (`_kv` /
// `_mono` on the event card, `_StatusPill` on tool calls, the telemetry
// formatters) deliberately stay with their cluster and migrate in that
// cluster's own wedge — moving them here early would be premature
// generalization (the very thing the monolith-refactor warns against).
//
//   feedJsonPretty  — event_card, approval, interaction, tool_renderers
//   CollapsibleMono — all five card clusters
//   ModelTokens     — the container's per-model aggregation + telemetry
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';

/// Pretty-print a JSON-able value with a 2-space indent; falls back to
/// `toString()` on any value the encoder can't handle. Lifted from
/// `AgentEventCard._jsonPretty` in W1 — shared across the feed's card
/// clusters (event_card, approval, interaction, tool_renderers).
String feedJsonPretty(Object? v) {
  try {
    return const JsonEncoder.withIndent('  ').convert(v);
  } catch (_) {
    return v?.toString() ?? '';
  }
}

/// Mono text that collapses past _kCollapseLines with a toggle. Long
/// tool_call inputs and tool_result outputs would otherwise dominate the
/// feed — a single grep result can push everything else off-screen.
class CollapsibleMono extends StatefulWidget {
  final String text;
  final Color? color;
  const CollapsibleMono({required this.text, this.color});

  @override
  State<CollapsibleMono> createState() => _CollapsibleMonoState();
}

const int _kCollapseLines = 12;

class _CollapsibleMonoState extends State<CollapsibleMono> {
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

/// Aggregated token totals for one model across all turn.result frames.
/// Mutable so the build-time aggregation loop can fold each frame in
/// without rebuilding the map every event.
class ModelTokens {
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

  static ModelTokens empty() => ModelTokens();

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

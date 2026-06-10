// AgentFeed tool-call renderers — the foldable tool_call card and its
// inline tool_result body.
//
// Cluster wedge of the agent_feed split (docs/plans/agent-feed-split.md,
// W4). Only `FoldableToolCall` is referenced cross-library (by the event
// card), so it alone is public; the result-inline body, the kv line, the
// status pill, and the tool-icon map are tool-call-only and stay private.
// `_toolIconFor` was a static on `AgentEventCard` whose sole caller lived
// here — it moves in rather than forcing a back-import of the container.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import 'feed_render.dart';

// Tool-name → glyph map for the tool_call card header strip. Keeps the
// transcript scannable: a wall of identical "tool_call" labels reads
// like noise; an icon per tool ("Bash → terminal", "Edit → pencil",
// "Read → eye") makes each card immediately identifiable. Unknown
// names fall through to a generic build glyph.
IconData _toolIconFor(String name) {
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

/// Tool-call card body with a manual fold control. The body is
/// expanded by default — this matches the prior behavior so the user
/// doesn't lose at-a-glance context — but the user can tap the
/// chevron in the name row to collapse the card down to just the
/// tool name + status pill. Useful for noisy multi-step calls where
/// the input or result body is mostly screen-filling JSON.
class FoldableToolCall extends StatefulWidget {
  final String name;
  final String status;
  final String toolId;
  final Object? input;
  final String? preview;
  final Map<String, dynamic>? resultPayload;
  final bool resultIsError;
  const FoldableToolCall({
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
  State<FoldableToolCall> createState() => _FoldableToolCallState();
}

class _FoldableToolCallState extends State<FoldableToolCall> {
  // Collapsed by default for EVERY call — tool-call args + result preview
  // eat the whole transcript otherwise, and the user is usually scanning
  // for text turns, not tool internals. Failed calls collapse too: a failed
  // tool can carry a huge body (e.g. an `attach` that errored still holds
  // its base64 content), so auto-expanding it on error blew up the card.
  // The `failed` status pill in the header (rendered outside the expand
  // block) already signals the error at a glance — the user taps to read
  // the details.
  bool _expanded = false;

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
              Icon(_toolIconFor(widget.name), size: 14, color: muted),
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
      padding: const EdgeInsets.symmetric(vertical: Spacing.s2),
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
      padding: const EdgeInsets.symmetric(horizontal: Spacing.s8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        status,
        style: GoogleFonts.jetBrainsMono(
          fontSize: FontSizes.label,
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
                      fontSize: FontSizes.label,
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
                        fontSize: FontSizes.label,
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

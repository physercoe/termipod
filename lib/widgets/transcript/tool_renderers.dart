// AgentFeed tool-call renderers — the foldable tool_call card, its
// inline tool_result body, and the P1 tool-call GROUP card.
//
// Cluster wedge of the agent_feed split (docs/plans/agent-feed-split.md,
// W4). `FoldableToolCall` and `ToolCallGroupCard` are referenced
// cross-library (by the event card / the live feed), so they alone are
// public; the result-inline body, the kv line, the status pill, and the
// tool-icon map stay private. `_toolIconFor` was a static on
// `AgentEventCard` whose sole caller lived here — it moves in rather
// than forcing a back-import of the container.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../app_chip.dart';
import 'feed_reducer.dart';
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
              if (widget.status.isNotEmpty) _statusPill(widget.status),
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
        if (_expanded)
          _ToolCallDetail(
            toolId: widget.toolId,
            input: widget.input,
            preview: widget.preview,
            resultPayload: widget.resultPayload,
            resultIsError: widget.resultIsError,
          ),
      ],
    );
  }
}

/// The expanded body of a tool_call — id, input, streaming preview,
/// inline result. Extracted from [_FoldableToolCallState] so the P1
/// tool-group rows (agent-transcript-redesign §6 P1) expand into the
/// SAME detail the standalone card shows; one body, two fold controls.
class _ToolCallDetail extends StatelessWidget {
  final String toolId;
  final Object? input;
  final String? preview;
  final Map<String, dynamic>? resultPayload;
  final bool resultIsError;
  const _ToolCallDetail({
    required this.toolId,
    required this.input,
    required this.preview,
    required this.resultPayload,
    required this.resultIsError,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (toolId.isNotEmpty) _ToolKvLine(label: 'id', value: toolId),
        if (input != null) CollapsibleMono(text: feedJsonPretty(input)),
        if (preview != null && preview!.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: CollapsibleMono(text: preview!),
          ),
        if (resultPayload != null)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: _ToolResultInline(
              payload: resultPayload!,
              isError: resultIsError,
            ),
          ),
      ],
    );
  }
}

/// The P1 tool-call GROUP card (agent-transcript-redesign §6 P1,
/// decision §7.3 — kimi-web `ToolGroup.vue` behavior as written): one
/// card for a run of ≥2 consecutive tool calls. Header
/// `● N tool calls · <state>` with aggregate state **running > error >
/// done** (the pure math lives in feed_reducer.dart —
/// [toolCallGroupState]); rows are icon + localized verb + key argument
/// + diffstat + status glyph, with per-row lazy detail (tap a row to
/// expand the SAME [_ToolCallDetail] body the standalone card shows).
///
/// Groups are EXPANDED BY DEFAULT and NEVER auto-collapse — the header
/// tap is a user opt-in collapse, remembered per group instance (the
/// live feed keys the card by its run's anchor seq, so this State
/// survives rebuilds as new events stream in). Error rows auto-expand
/// their detail and are counted in the header (`· N failed`).
class ToolCallGroupCard extends StatefulWidget {
  final ToolCallGroup group;
  // The SAME FoldMaps lineage the standalone cards render from — a
  // call's status/glyph resolves identically in and out of a group.
  final Map<String, Map<String, dynamic>> toolUpdates;
  final Map<String, Map<String, dynamic>> toolResults;
  const ToolCallGroupCard({
    super.key,
    required this.group,
    required this.toolUpdates,
    required this.toolResults,
  });

  @override
  State<ToolCallGroupCard> createState() => _ToolCallGroupCardState();
}

class _ToolCallGroupCardState extends State<ToolCallGroupCard> {
  // User opt-in collapse (see the class doc). Default EXPANDED — the
  // group's whole point is glanceable rows without a first tap.
  bool _collapsed = false;

  // Per-row expansion overrides keyed by tool id (or a positional
  // fallback for id-less calls). A missing entry means "default":
  // error rows expanded, everything else one line. An explicit entry
  // always wins, so a user collapse sticks even if the row later
  // flips to error — and a row that errors after being fine expands
  // on its own (the default recomputes from the new state).
  final Map<String, bool> _rowExpanded = {};

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final bg = isDark
        ? DesignColors.surfaceDark
        : DesignColors.surfaceLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    final l10n = AppLocalizations.of(context)!;

    final state =
        toolCallGroupState(widget.group, widget.toolResults, widget.toolUpdates);
    final errorCount = toolCallGroupErrorCount(
        widget.group, widget.toolResults, widget.toolUpdates);
    final stateColor = switch (state) {
      ToolGroupState.running => DesignColors.terminalCyan,
      ToolGroupState.error => DesignColors.error,
      ToolGroupState.done => DesignColors.success,
    };
    final stateLabel = switch (state) {
      ToolGroupState.running => l10n.toolGroupStateRunning,
      ToolGroupState.error => l10n.toolGroupFailedCount(errorCount),
      ToolGroupState.done => l10n.toolGroupStateDone,
    };

    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      padding:
          const EdgeInsets.fromLTRB(Spacing.s8, 8, Spacing.s8, Spacing.s8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Header — the only fold control for the whole group. InkWell
          // so the tap target is the entire strip (kimi-web pins scroll
          // on toggle; our ListView keeps position by key instead).
          InkWell(
            onTap: () => setState(() => _collapsed = !_collapsed),
            child: Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Row(
                children: [
                  Container(
                    width: 6,
                    height: 6,
                    decoration: BoxDecoration(
                      color: stateColor,
                      shape: BoxShape.circle,
                    ),
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      l10n.toolGroupHeader(widget.group.events.length),
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 12,
                        fontWeight: FontWeight.w700,
                        color: fg,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  Text(
                    '· $stateLabel',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: FontSizes.label,
                      fontWeight: FontWeight.w700,
                      color: stateColor,
                    ),
                  ),
                  // Errors must surface in the header even while the
                  // aggregate reads running (a sibling call is still in
                  // flight) — otherwise a mid-group failure hides until
                  // the turn wraps.
                  if (state != ToolGroupState.error && errorCount > 0)
                    Padding(
                      padding: const EdgeInsets.only(left: 6),
                      child: Text(
                        '· ${l10n.toolGroupFailedCount(errorCount)}',
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: FontSizes.label,
                          fontWeight: FontWeight.w700,
                          color: DesignColors.error,
                        ),
                      ),
                    ),
                  const SizedBox(width: 4),
                  Icon(
                    _collapsed ? Icons.expand_more : Icons.expand_less,
                    size: 16,
                    color: muted,
                  ),
                ],
              ),
            ),
          ),
          if (!_collapsed) ...[
            for (var i = 0; i < widget.group.events.length; i++)
              _buildRow(context, i, muted: muted, fg: fg, border: border),
          ],
        ],
      ),
    );
  }

  Widget _buildRow(
    BuildContext context,
    int index, {
    required Color muted,
    required Color fg,
    required Color border,
  }) {
    final l10n = AppLocalizations.of(context)!;
    final e = widget.group.events[index];
    final p = e['payload'] is Map
        ? (e['payload'] as Map).cast<String, dynamic>()
        : <String, dynamic>{};
    final name = (p['name'] ?? p['tool'] ?? '?').toString();
    final id = (p['id'] ?? '').toString();
    final input = p['input'];
    final update = id.isNotEmpty ? widget.toolUpdates[id] : null;
    final resEvent = id.isNotEmpty ? widget.toolResults[id] : null;
    final resultPayload = resEvent != null && resEvent['payload'] is Map
        ? (resEvent['payload'] as Map).cast<String, dynamic>()
        : null;
    final resultIsError = resultPayload?['is_error'] == true;
    final rowState =
        toolCallRowState(e, widget.toolResults, widget.toolUpdates);
    final keyArg = toolCallKeyArg(name, input);
    final diffstat = toolCallDiffstat(name, p);
    final rowKey = id.isNotEmpty ? id : 'row-$index';
    final expanded =
        _rowExpanded[rowKey] ?? (rowState == ToolGroupState.error);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (index > 0) Divider(height: 1, thickness: 1, color: border),
        InkWell(
          onTap: () =>
              setState(() => _rowExpanded[rowKey] = !expanded),
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 5),
            child: Row(
              children: [
                Icon(_toolIconFor(name), size: 14, color: muted),
                const SizedBox(width: 6),
                Text(
                  toolVerbFor(l10n, name),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: fg,
                  ),
                ),
                if (keyArg.isNotEmpty) ...[
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      keyArg,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: FontSizes.label,
                        color: muted,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ] else
                  const Spacer(),
                if (diffstat != null) ...[
                  const SizedBox(width: 6),
                  Text(
                    diffstat,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: FontSizes.label,
                      color: muted,
                    ),
                  ),
                ],
                const SizedBox(width: 6),
                _rowGlyph(rowState),
              ],
            ),
          ),
        ),
        if (expanded)
          Padding(
            padding: const EdgeInsets.only(left: 20, bottom: 4),
            child: _ToolCallDetail(
              toolId: id,
              input: input,
              preview: toolCallUpdatePreview(update),
              resultPayload: resultPayload,
              resultIsError: resultIsError,
            ),
          ),
      ],
    );
  }

  // The row's status glyph — one glanceable shape per row state (the
  // standalone card's pill would blow the row to two lines): a live
  // spinner while running, check / error glyphs once resolved. Colors
  // mirror `_statusPill`.
  Widget _rowGlyph(ToolGroupState state) {
    switch (state) {
      case ToolGroupState.error:
        return Icon(Icons.error_outline, size: 14, color: DesignColors.error);
      case ToolGroupState.done:
        return Icon(Icons.check_circle_outline,
            size: 14, color: DesignColors.success);
      case ToolGroupState.running:
        return SizedBox(
          width: 12,
          height: 12,
          child: CircularProgressIndicator(
            strokeWidth: 1.5,
            color: DesignColors.terminalCyan,
          ),
        );
    }
  }
}

/// Localized one-word verb for a tool row (kimi-web ToolRow shape) —
/// the group row reads "Edit  lib/foo.dart" instead of repeating the
/// raw engine name. Unknown / engine-specific / MCP tools fall back to
/// their raw name, which is already self-describing. kimi's ACP titles
/// reuse the claude-style names where they overlap (`TodoList` is the
/// kimi spelling of claude's `TodoWrite`).
String toolVerbFor(AppLocalizations l10n, String name) {
  switch (name) {
    case 'Bash':
      return l10n.toolVerbBash;
    case 'Read':
      return l10n.toolVerbRead;
    case 'Edit':
    case 'MultiEdit':
    case 'NotebookEdit':
      return l10n.toolVerbEdit;
    case 'Write':
      return l10n.toolVerbWrite;
    case 'Glob':
      return l10n.toolVerbGlob;
    case 'Grep':
      return l10n.toolVerbGrep;
    case 'WebFetch':
      return l10n.toolVerbWebFetch;
    case 'WebSearch':
      return l10n.toolVerbWebSearch;
    case 'Task':
      return l10n.toolVerbTask;
    case 'TodoWrite':
    case 'TodoList':
      return l10n.toolVerbTodo;
  }
  return name;
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
/// Tool-status tag rendered via the shared [AppStatusChip] (ADR-047 D-7).
Widget _statusPill(String status) {
  final color = switch (status) {
    'failed' => DesignColors.error,
    'completed' => DesignColors.success,
    'in_progress' => DesignColors.terminalCyan,
    'pending' => DesignColors.warning,
    _ => DesignColors.textMuted,
  };
  return AppStatusChip(label: status, color: color);
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

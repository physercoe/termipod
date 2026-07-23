// P2 state dock UI (docs/plans/agent-transcript-redesign.md §6 P2, decision
// §7.5) — the ambient session-state chips above the composer plus the modal
// bottom sheet they open.
//
// kimi-web `ChatDock.vue` model: the chips are STATE VISIBILITY, not feed
// filtering — tapping one never touches the transcript lens; it opens a
// detail list. Decision §7.5 picks the bottom sheet over a dedicated tab on
// mobile ("bottom sheet first; promote to a tab only if the checklist
// becomes multi-section").
//
// The model is a pure derivation ([SessionActivity], session_activity.dart)
// computed by the host from the FULL event list — this file only renders it:
//   * [SessionActivityStrip] — the chip strip (self-hides when no chip is
//     visible, so the host can place it unconditionally).
//   * [showSessionActivitySheet] / [SessionActivitySheet] — the modal sheet
//     with the Tasks / Sub-agents / Todos segmented switcher.
//
// All rows (tasks, sub-agents, todos) share ONE status-glyph style —
// kimi-web's StatusGlyph rule — reusing the P1 group-card glyph shapes
// (running spinner / error / done) plus a hollow glyph for a pending todo.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../l10n/app_localizations.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../app_chip.dart';
import 'session_activity.dart';

/// The chip strip above the composer. Renders the visible chips per the
/// locked visibility rules ([SessionActivity.showTasks] et al.) and nothing
/// at all when none apply — the host places it unconditionally above
/// `AgentCompose`.
class SessionActivityStrip extends StatelessWidget {
  final SessionActivity activity;
  const SessionActivityStrip({super.key, required this.activity});

  // One ambient chip: the shared [AppStatusChip] visual (ADR-047 D-7 —
  // composed, NOT a new private *Chip widget class, per the design-token
  // ratchet) made tappable. Running-state chips tint terminalCyan (the
  // same "in flight" color the P1 group card uses); the todos chip tints
  // slate.
  Widget _chip({
    required String label,
    required IconData icon,
    required Color color,
    required VoidCallback onTap,
  }) {
    return InkWell(
      onTap: onTap,
      borderRadius: Radii.xsBorder,
      child: AppStatusChip(label: label, icon: icon, color: color),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (activity.isEmpty) return const SizedBox.shrink();
    final l10n = AppLocalizations.of(context)!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        Spacing.s8,
        Spacing.s4,
        Spacing.s8,
        Spacing.s4,
      ),
      child: Row(
        children: [
          if (activity.showTasks) ...[
            _chip(
              label: l10n.sessionDockTasksChip(activity.shellRunning),
              icon: Icons.terminal,
              color: DesignColors.terminalCyan,
              onTap: () => showSessionActivitySheet(
                context,
                activity,
                SessionDockKind.tasks,
              ),
            ),
            const SizedBox(width: Spacing.s8),
          ],
          if (activity.showSubagents) ...[
            _chip(
              label: l10n.sessionDockSubagentsChip(activity.subagentRunning),
              icon: Icons.alt_route,
              color: DesignColors.terminalCyan,
              onTap: () => showSessionActivitySheet(
                context,
                activity,
                SessionDockKind.subagents,
              ),
            ),
            const SizedBox(width: Spacing.s8),
          ],
          if (activity.showTodos)
            _chip(
              label: l10n.sessionDockTodosChip(
                activity.todos!.done,
                activity.todos!.total,
              ),
              icon: Icons.checklist,
              // Todos are a neutral rollup (done/total), not a "running"
              // signal — slate instead of the running-state cyan.
              color: DesignColors.slate,
              onTap: () => showSessionActivitySheet(
                context,
                activity,
                SessionDockKind.todos,
              ),
            ),
        ],
      ),
    );
  }
}

/// Open the state-dock bottom sheet on [initialKind]. [activity] is a
/// snapshot of the derivation at tap time — the sheet is a transient detail
/// view, not a live mirror; reopening it reads the newest state.
void showSessionActivitySheet(
  BuildContext context,
  SessionActivity activity,
  SessionDockKind initialKind,
) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (ctx) =>
        SessionActivitySheet(activity: activity, initialKind: initialKind),
  );
}

/// The modal detail sheet: a segmented switcher (Tasks / Sub-agents /
/// Todos — the shared [AppChoiceChip] segment idiom) over the selected
/// kind's list. Opening it does NOT filter the transcript (no chip = no
/// filter; lens behavior unchanged).
class SessionActivitySheet extends StatefulWidget {
  final SessionActivity activity;
  final SessionDockKind initialKind;
  const SessionActivitySheet({
    super.key,
    required this.activity,
    required this.initialKind,
  });

  @override
  State<SessionActivitySheet> createState() => _SessionActivitySheetState();
}

class _SessionActivitySheetState extends State<SessionActivitySheet> {
  late SessionDockKind _kind = widget.initialKind;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: Spacing.s16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                AppChoiceChip(
                  label: l10n.sessionDockKindTasks,
                  selected: _kind == SessionDockKind.tasks,
                  onTap: () => setState(() => _kind = SessionDockKind.tasks),
                ),
                const SizedBox(width: Spacing.s8),
                AppChoiceChip(
                  label: l10n.sessionDockKindSubagents,
                  selected: _kind == SessionDockKind.subagents,
                  onTap: () =>
                      setState(() => _kind = SessionDockKind.subagents),
                ),
                const SizedBox(width: Spacing.s8),
                AppChoiceChip(
                  label: l10n.sessionDockKindTodos,
                  selected: _kind == SessionDockKind.todos,
                  onTap: () => setState(() => _kind = SessionDockKind.todos),
                ),
              ],
            ),
            const SizedBox(height: Spacing.s12),
            // shrinkWrap inside Flexible: the sheet hugs a short list but
            // scrolls once rows would exceed the modal's max height.
            Flexible(child: _buildKindList(context)),
            const SizedBox(height: Spacing.s8),
          ],
        ),
      ),
    );
  }

  Widget _buildKindList(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    switch (_kind) {
      case SessionDockKind.tasks:
        final calls = widget.activity.shellCalls;
        if (calls.isEmpty) return _emptyNote(l10n.sessionDockTasksEmpty, muted);
        return ListView(
          shrinkWrap: true,
          children: [
            for (final c in calls) _TaskRow(call: c, icon: Icons.terminal),
          ],
        );
      case SessionDockKind.subagents:
        final calls = widget.activity.subagentCalls;
        if (calls.isEmpty) {
          return _emptyNote(l10n.sessionDockSubagentsEmpty, muted);
        }
        return ListView(
          shrinkWrap: true,
          children: [
            for (final c in calls) _TaskRow(call: c, icon: Icons.alt_route),
          ],
        );
      case SessionDockKind.todos:
        final items = widget.activity.todos?.items ?? const <SessionTodoItem>[];
        if (items.isEmpty) return _emptyNote(l10n.sessionDockTodosEmpty, muted);
        return ListView(
          shrinkWrap: true,
          children: [for (final t in items) _TodoRow(item: t)],
        );
    }
  }

  Widget _emptyNote(String text, Color muted) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: Spacing.s16),
      child: Text(
        text,
        textAlign: TextAlign.center,
        style: GoogleFonts.spaceGrotesk(
          fontSize: FontSizes.caption,
          color: muted,
        ),
      ),
    );
  }
}

/// The ONE status-glyph style every dock row shares (kimi-web StatusGlyph
/// rule): the same shapes the P1 group-card rows use — a live spinner while
/// running, check / error glyphs once resolved — plus a hollow glyph for a
/// pending todo (the one state a tool call never has but a todo does).
class SessionStatusGlyph extends StatelessWidget {
  /// Tool-call state, or null for a pending (not-yet-started) todo.
  final SessionTaskStatus? status;
  const SessionStatusGlyph({super.key, required this.status});

  @override
  Widget build(BuildContext context) {
    switch (status) {
      case SessionTaskStatus.error:
        return const Icon(
          Icons.error_outline,
          size: IconSizes.sm,
          color: DesignColors.error,
        );
      case SessionTaskStatus.done:
        return const Icon(
          Icons.check_circle_outline,
          size: IconSizes.sm,
          color: DesignColors.success,
        );
      case SessionTaskStatus.running:
        return const SizedBox(
          width: 12,
          height: 12,
          child: CircularProgressIndicator(
            strokeWidth: 1.5,
            color: DesignColors.terminalCyan,
          ),
        );
      case null:
        final isDark = Theme.of(context).brightness == Brightness.dark;
        return Icon(
          Icons.radio_button_unchecked,
          size: IconSizes.sm,
          color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
        );
    }
  }
}

/// A Tasks / Sub-agents sheet row: icon + raw tool name + key argument +
/// status glyph — the P1 group-card row shape (kimi-web `ToolRow.vue`).
class _TaskRow extends StatelessWidget {
  final SessionTaskCall call;
  final IconData icon;
  const _TaskRow({required this.call, required this.icon});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: Spacing.s4),
      child: Row(
        children: [
          Icon(icon, size: IconSizes.sm, color: muted),
          const SizedBox(width: Spacing.s8),
          Text(
            call.name,
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.caption,
              fontWeight: FontWeight.w700,
              color: fg,
            ),
          ),
          if (call.keyArg.isNotEmpty) ...[
            const SizedBox(width: Spacing.s8),
            Expanded(
              child: Text(
                call.keyArg,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: FontSizes.label,
                  color: muted,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ] else
            const Spacer(),
          const SizedBox(width: Spacing.s8),
          SessionStatusGlyph(status: call.status),
        ],
      ),
    );
  }
}

/// A Todos sheet row: shared status glyph + content. Completed = strike-
/// through + faint, in_progress = medium weight — the same treatment the
/// in-transcript plan card gives its checklist (event_card.dart `_planBody`),
/// so a todo reads identically in the transcript and the dock.
class _TodoRow extends StatelessWidget {
  final SessionTodoItem item;
  const _TodoRow({required this.item});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final completed = item.isCompleted;
    // Map the todo status onto the shared glyph: pending → hollow (null),
    // in_progress → the running spinner, completed → the done check.
    final glyphStatus = completed
        ? SessionTaskStatus.done
        : item.status == 'in_progress'
        ? SessionTaskStatus.running
        : null;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: Spacing.s4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionStatusGlyph(status: glyphStatus),
          const SizedBox(width: Spacing.s8),
          Expanded(
            child: Text(
              item.content,
              style: TextStyle(
                fontSize: FontSizes.bodySmall,
                fontWeight: item.status == 'in_progress'
                    ? FontWeight.w500
                    : FontWeight.w400,
                decoration: completed ? TextDecoration.lineThrough : null,
                color: completed ? muted : fg,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

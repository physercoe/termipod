import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../providers/vocab_provider.dart';
import '../../services/vocab/vocab_axis.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';
import '../../theme/task_priority_style.dart';
import '../../widgets/hub_offline_banner.dart';
import '../sessions/sessions_screen.dart' show SessionChatScreen;
import 'overview_widgets/workspace_overview.dart' show formatRelative;
import 'plan_viewer_screen.dart';
import 'task_edit_sheet.dart';

/// Full-screen task detail. Shows title, body, status, and a status picker
/// at the top. Edits patch the task and refetch before rendering so stale
/// state doesn't linger on the screen.
class TaskDetailScreen extends ConsumerStatefulWidget {
  final String projectId;
  final String taskId;
  final Map<String, dynamic>? initial;
  const TaskDetailScreen({
    super.key,
    required this.projectId,
    required this.taskId,
    this.initial,
  });

  @override
  ConsumerState<TaskDetailScreen> createState() => _TaskDetailScreenState();
}

class _TaskDetailScreenState extends ConsumerState<TaskDetailScreen> {
  Map<String, dynamic>? _task;
  String? _error;
  bool _loading = true;
  DateTime? _staleSince;
  // ADR-029 W9: audit rows scoped to this task. Best-effort —
  // network failure leaves _audit at its last-known value so the
  // timeline never erases itself on a transient blip.
  List<Map<String, dynamic>> _audit = const [];

  @override
  void initState() {
    super.initState();
    if (widget.initial != null) {
      _task = widget.initial;
      _loading = false;
    }
    _refresh();
  }

  Future<void> _refresh() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final cached =
          await client.getTaskCached(widget.projectId, widget.taskId);
      if (!mounted) return;
      setState(() {
        _task = cached.body;
        _staleSince = cached.staleSince;
        _loading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
    // W9: load audit history filtered to this task. Best-effort —
    // failure leaves `_audit` at its previous value rather than
    // erasing the timeline on a transient blip. Server-side audit
    // endpoint takes only `action` / `since` / `project_id` /
    // `limit`; filter by target_id client-side.
    try {
      final cached = await client.listAuditEventsCached(
        projectId: widget.projectId,
        limit: 200,
      );
      if (!mounted) return;
      final rows = cached.body
          .where((r) =>
              (r['target_kind'] ?? '').toString() == 'task' &&
              (r['target_id'] ?? '').toString() == widget.taskId)
          .toList();
      setState(() => _audit = rows);
    } catch (_) {
      // swallow — _audit stays where it was.
    }
  }

  Future<void> _openEditSheet() async {
    final task = _task;
    if (task == null) return;
    final updated = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => TaskEditSheet(
        projectId: widget.projectId,
        task: task,
      ),
    );
    if (updated != null && mounted) {
      setState(() => _task = updated);
    }
  }

  Future<void> _setStatus(String s) async {
    final l10n = AppLocalizations.of(context)!;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final row = await client.patchTask(
        widget.projectId,
        widget.taskId,
        status: s,
      );
      if (!mounted) return;
      setState(() => _task = row);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.statusChangeFailedError('$e'))),
        );
      }
    }
  }

  Future<void> _setPriority(TaskPriority p) async {
    final l10n = AppLocalizations.of(context)!;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final row = await client.patchTask(
        widget.projectId,
        widget.taskId,
        priority: p.wire,
      );
      if (!mounted) return;
      setState(() => _task = row);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(l10n.priorityChangeFailedError('$e'))),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final vocab = ref.watch(vocabularyProvider);
    final taskTerm = vocab.term(VocabAxis.entityTask);
    return Scaffold(
      appBar: AppBar(
        title: Text(taskTerm.title),
        actions: [
          IconButton(
            icon: const Icon(Icons.edit_outlined),
            tooltip: l10n.taskDetailEditTooltip(taskTerm.lower),
            onPressed: _task == null ? null : _openEditSheet,
          ),
        ],
      ),
      body: Column(
        children: [
          HubOfflineBanner(staleSince: _staleSince, onRetry: _refresh),
          Expanded(
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : _error != null
                    ? Center(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Text(_error!,
                              style: GoogleFonts.jetBrainsMono(
                                  color: DesignColors.error, fontSize: 12)),
                        ),
                      )
                    : _body(),
          ),
        ],
      ),
    );
  }

  Widget _body() {
    final task = _task ?? const <String, dynamic>{};
    final title = (task['title'] ?? '').toString();
    final body = (task['body_md'] ?? '').toString();
    final status = (task['status'] ?? 'todo').toString();
    final source = (task['source'] ?? 'ad_hoc').toString();
    final planId = (task['plan_id'] ?? '').toString();
    final planStepId = (task['plan_step_id'] ?? '').toString();
    final priority = parseTaskPriority(task['priority']);
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return RefreshIndicator(
      onRefresh: _refresh,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              TaskPriorityDot(priority: priority, size: 10),
              const SizedBox(width: 10),
              Expanded(
                child: Text(title,
                    style: GoogleFonts.spaceGrotesk(
                        fontSize: 20, fontWeight: FontWeight.w700)),
              ),
            ],
          ),
          const SizedBox(height: 12),
          // Compact state row: status + priority side-by-side as
          // popup pickers. Replaces the two stacked ChoiceChip rows
          // that ate ~140px of vertical space for fields users
          // typically read but rarely change.
          _StateRow(
            status: status,
            priority: priority,
            onStatus: _setStatus,
            onPriority: _setPriority,
            isDark: isDark,
          ),
          const SizedBox(height: 12),
          // Unified attribution card: collapses the prior triplet
          // (Source / Attribution / Worker session) into one bordered
          // card that answers who-and-when in 2–4 icon rows. The
          // worker-session Open button rides on the assignee row.
          _AttributionCard(
            task: task,
            source: source,
            planId: planId,
            planStepId: planStepId,
            projectId: widget.projectId,
            isDark: isDark,
          ),
          const SizedBox(height: 16),
          Container(
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight,
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
              ),
            ),
            child: _TaskBody(body: body),
          ),
          const SizedBox(height: 16),
          // W9 timeline: every audit row for this task in reverse-
          // chrono order. Includes the W2.9 task.notify lineage via
          // task.status rows + any task.update / task.create entries.
          _TaskAuditTimeline(rows: _audit, isDark: isDark),
        ],
      ),
    );
  }
}

/// Compact state row: status + priority as side-by-side popup pickers.
/// Replaces the prior stacked ChoiceChip Wraps. Each picker shows the
/// selected value with a dropdown arrow; tapping opens a small menu.
/// Saves ~140px vertical; reads as a header rather than a control panel.
class _StateRow extends StatelessWidget {
  final String status;
  final TaskPriority priority;
  final ValueChanged<String> onStatus;
  final ValueChanged<TaskPriority> onPriority;
  final bool isDark;
  const _StateRow({
    required this.status,
    required this.priority,
    required this.onStatus,
    required this.onPriority,
    required this.isDark,
  });

  static const _statuses = [
    'todo',
    'in_progress',
    'blocked',
    'in_review',
    'done',
    'cancelled',
  ];

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(child: _statusPicker(context)),
        const SizedBox(width: 8),
        Expanded(child: _priorityPicker(context)),
      ],
    );
  }

  Widget _statusPicker(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return PopupMenuButton<String>(
      tooltip: l10n.changeStatusTooltip,
      itemBuilder: (_) => [
        for (final s in _statuses)
          PopupMenuItem<String>(
            value: s,
            child: Text(
              taskStatusLabel(l10n, s),
              style: GoogleFonts.spaceGrotesk(
                fontWeight:
                    s == status ? FontWeight.w700 : FontWeight.w400,
              ),
            ),
          ),
      ],
      onSelected: onStatus,
      child: _pickerChip(label: l10n.taskDetailStatusLabel, value: taskStatusLabel(l10n, status)),
    );
  }

  Widget _priorityPicker(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return PopupMenuButton<TaskPriority>(
      tooltip: l10n.changePriorityTooltip,
      itemBuilder: (_) => [
        for (final p in TaskPriority.values)
          PopupMenuItem<TaskPriority>(
            value: p,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TaskPriorityDot(priority: p, size: 10),
                const SizedBox(width: 8),
                Text(
                  p.localizedLabel(l10n),
                  style: GoogleFonts.spaceGrotesk(
                    fontWeight:
                        p == priority ? FontWeight.w700 : FontWeight.w400,
                  ),
                ),
              ],
            ),
          ),
      ],
      onSelected: onPriority,
      child: _pickerChip(
        label: l10n.taskDetailPriorityLabel,
        value: priority.localizedLabel(l10n),
        leading: TaskPriorityDot(priority: priority, size: 8),
      ),
    );
  }

  Widget _pickerChip({
    required String label,
    required String value,
    Widget? leading,
  }) {
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: Spacing.s8),
      decoration: BoxDecoration(
        color: isDark
            ? DesignColors.surfaceDark
            : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      child: Row(
        children: [
          Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: FontSizes.label,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.5,
              color: muted,
            ),
          ),
          const SizedBox(width: 10),
          if (leading != null) ...[leading, const SizedBox(width: 6)],
          Expanded(
            child: Text(
              value,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          Icon(Icons.arrow_drop_down, size: 18, color: muted),
        ],
      ),
    );
  }
}

/// Unified attribution card. Collapses what used to be three separate
/// bordered sections (_SourceSection, _TaskAttributionBlock,
/// _LinkedWorkSection) into one card whose rows answer who-and-when:
///
/// - Assignee row: `@worker · running` plus the worker-session Open
///   button on the right (the Open is unique to the assignee, so it
///   belongs on that row, not in its own card).
/// - Provenance row: `assigned by @steward` for spawn/ad-hoc tasks
///   with an assigner; `Generated by plan step …` (tappable, opens
///   PlanViewer) for plan-derived tasks; `Created manually` only when
///   neither applies — this fixes the v1.0.614 mislabel where every
///   spawn-created task read "Created manually" because hub's `source`
///   field is plan-vs-ad_hoc, not spawn-vs-human.
/// - Time row: started / done / cancelled (relative).
/// - Optional result summary panel below a divider when populated.
///
/// Each row is individually optional so pre-ADR-029 tasks render
/// cleanly (or hide the whole card when nothing applies).
class _AttributionCard extends ConsumerWidget {
  final Map<String, dynamic> task;
  final String source;
  final String planId;
  final String planStepId;
  final String projectId;
  final bool isDark;
  const _AttributionCard({
    required this.task,
    required this.source,
    required this.planId,
    required this.planStepId,
    required this.projectId,
    required this.isDark,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final assigneeID = (task['assignee_id'] ?? '').toString();
    final assigneeHandle = (task['assignee_handle'] ?? '').toString();
    final assigneeStatus = (task['assignee_status'] ?? '').toString();
    final assignerHandle = (task['assigner_handle'] ?? '').toString();
    final startedRaw = (task['started_at'] ?? '').toString();
    final completedRaw = (task['completed_at'] ?? '').toString();
    final updatedRaw = (task['updated_at'] ?? '').toString();
    final status = (task['status'] ?? '').toString();
    final summary = (task['result_summary'] ?? '').toString();
    // ADR-034 D-6: terminal_reason is the additive close-classification
    // on a closed task (completed / failed / killed / timed_out /
    // superseded) — shown alongside, not replacing, the status.
    final terminalReason = (task['terminal_reason'] ?? '').toString();
    final started = DateTime.tryParse(startedRaw);
    final completed = DateTime.tryParse(completedRaw);
    final cancelled = status == 'cancelled'
        ? (completed ?? DateTime.tryParse(updatedRaw))
        : null;
    final isPlan = source == 'plan' && planStepId.isNotEmpty;

    // Resolve linked session for the Open button (rides on the
    // assignee row). The provider read is no-op when assigneeID is
    // empty, which avoids spurious rebuilds for unassigned tasks.
    String sessionId = '';
    String sessionTitle = '';
    if (assigneeID.isNotEmpty) {
      final sessionsState = ref.watch(sessionsProvider).value;
      final allSessions = <Map<String, dynamic>>[
        ...?sessionsState?.active,
        ...?sessionsState?.previous,
      ];
      final session = allSessions.firstWhere(
        (s) => (s['current_agent_id'] ?? '').toString() == assigneeID,
        orElse: () => const <String, dynamic>{},
      );
      final sid = (session['id'] ?? '').toString();
      if (sid.isNotEmpty) {
        sessionId = sid;
        sessionTitle = (session['title'] ?? '').toString().isNotEmpty
            ? (session['title']).toString()
            : '@${_stripAt(assigneeHandle)}';
      }
    }

    // Hide the entire card when there's nothing useful to show.
    // Pre-ADR-029 tasks land here.
    if (assigneeHandle.isEmpty &&
        assignerHandle.isEmpty &&
        !isPlan &&
        started == null &&
        completed == null &&
        cancelled == null &&
        summary.isEmpty) {
      return const SizedBox.shrink();
    }

    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: isDark
            ? DesignColors.surfaceDark
            : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: isDark
              ? DesignColors.borderDark
              : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (assigneeHandle.isNotEmpty)
            _assigneeRow(
              context,
              l10n: l10n,
              handle: assigneeHandle,
              workerStatus: assigneeStatus,
              muted: muted,
              sessionId: sessionId,
              sessionTitle: sessionTitle,
              assigneeID: assigneeID,
            ),
          _provenanceRow(
            context,
            l10n: l10n,
            isPlan: isPlan,
            assignerHandle: assignerHandle,
            muted: muted,
          ),
          if (started != null || completed != null || cancelled != null)
            _iconRow(
              icon: Icons.schedule,
              child: Text(
                _timeLine(l10n, started, completed, cancelled, status),
                style: GoogleFonts.spaceGrotesk(fontSize: 12),
              ),
              muted: muted,
            ),
          if (terminalReason.isNotEmpty)
            _iconRow(
              icon: Icons.flag_outlined,
              child: Text(
                l10n.taskDetailClosedReason(terminalReason),
                style: GoogleFonts.spaceGrotesk(fontSize: 12),
              ),
              muted: muted,
            ),
          if (isPlan) ...[
            const SizedBox(height: 6),
            Padding(
              padding: const EdgeInsets.only(left: 22),
              child: Text(
                l10n.taskDetailCloseWarning,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  color: muted,
                ),
              ),
            ),
          ],
          if (summary.isNotEmpty) ...[
            const SizedBox(height: 8),
            const Divider(height: 1),
            const SizedBox(height: 8),
            Text(
              l10n.taskDetailResultSummary,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: muted,
                letterSpacing: 0.4,
              ),
            ),
            const SizedBox(height: 4),
            SelectableText(
              summary,
              style: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
            ),
          ],
        ],
      ),
    );
  }

  Widget _assigneeRow(
    BuildContext context, {
    required AppLocalizations l10n,
    required String handle,
    required String workerStatus,
    required Color muted,
    required String sessionId,
    required String sessionTitle,
    required String assigneeID,
  }) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Icon(Icons.person_outline, size: 14, color: muted),
          const SizedBox(width: 8),
          Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: _statusColor(workerStatus, muted),
            ),
          ),
          const SizedBox(width: 6),
          Flexible(
            child: Text(
              '@${_stripAt(handle)}',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (workerStatus.isNotEmpty) ...[
            const SizedBox(width: 6),
            Text(
              '· $workerStatus',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: muted,
              ),
            ),
          ],
          const Spacer(),
          if (sessionId.isNotEmpty)
            TextButton.icon(
              icon: const Icon(Icons.forum_outlined, size: 14),
              label: Text(l10n.taskDetailOpenLabel),
              style: TextButton.styleFrom(
                visualDensity: VisualDensity.compact,
                padding: const EdgeInsets.symmetric(horizontal: 8),
                minimumSize: const Size(0, 28),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              onPressed: () {
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => SessionChatScreen(
                      sessionId: sessionId,
                      agentId: assigneeID,
                      title: sessionTitle,
                    ),
                  ),
                );
              },
            )
          else if (assigneeID.isNotEmpty)
            Text(
              l10n.taskDetailNoLiveSession,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontStyle: FontStyle.italic,
                color: muted,
              ),
            ),
        ],
      ),
    );
  }

  Widget _provenanceRow(
    BuildContext context, {
    required AppLocalizations l10n,
    required bool isPlan,
    required String assignerHandle,
    required Color muted,
  }) {
    if (isPlan) {
      return InkWell(
        borderRadius: BorderRadius.circular(4),
        onTap: planId.isEmpty
            ? null
            : () => Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => PlanViewerScreen(
                      planId: planId,
                      projectId: projectId,
                    ),
                  ),
                ),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 2),
          child: Row(
            children: [
              Icon(Icons.playlist_play_outlined,
                  size: 14, color: DesignColors.primary),
              const SizedBox(width: 8),
              Expanded(
                child: Text.rich(
                  TextSpan(
                    style: GoogleFonts.spaceGrotesk(fontSize: 12),
                    children: [
                      TextSpan(text: l10n.taskDetailGeneratedByPlanStep(planStepId)),
                    ],
                  ),
                ),
              ),
              if (planId.isNotEmpty)
                Icon(Icons.chevron_right, size: 16, color: muted),
            ],
          ),
        ),
      );
    }
    if (assignerHandle.isNotEmpty) {
      return _iconRow(
        icon: Icons.swap_horiz,
        child: Text(
          l10n.taskDetailAssignedBy(_stripAt(assignerHandle)),
          style: GoogleFonts.spaceGrotesk(fontSize: 12),
        ),
        muted: muted,
      );
    }
    return _iconRow(
      icon: Icons.person_outline,
      child: Text(
        l10n.taskDetailCreatedManually,
        style: GoogleFonts.spaceGrotesk(fontSize: 12),
      ),
      muted: muted,
    );
  }

  Widget _iconRow({
    required IconData icon,
    required Widget child,
    required Color muted,
  }) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Icon(icon, size: 14, color: muted),
          const SizedBox(width: 8),
          Expanded(child: child),
        ],
      ),
    );
  }

  String _timeLine(AppLocalizations l10n, DateTime? started,
      DateTime? completed, DateTime? cancelled, String status) {
    if (cancelled != null) {
      return l10n.taskDetailCancelledAgo(formatRelative(cancelled));
    }
    if (completed != null && status == 'done') {
      final donePart = l10n.taskDetailDoneAgo(formatRelative(completed));
      if (started != null) {
        return '$donePart · ${l10n.taskDetailStartedAgo(formatRelative(started))}';
      }
      return donePart;
    }
    if (started != null) {
      return l10n.taskDetailStartedAgo(formatRelative(started));
    }
    return '';
  }

  Color _statusColor(String s, Color muted) {
    switch (s) {
      case 'running':
        return DesignColors.success;
      case 'idle':
        return DesignColors.terminalCyan;
      case 'paused':
        return DesignColors.warning;
      case 'crashed':
      case 'failed':
        return DesignColors.error;
      case 'terminated':
      default:
        return muted;
    }
  }

  String _stripAt(String h) => h.startsWith('@') ? h.substring(1) : h;
}

/// ADR-029 W9 audit timeline. Renders one row per audit_events entry
/// targeting this task, reverse-chronological. Common actions:
///   - task.create — "created via {source}"
///   - task.status — "{from} → {to}"
///   - task.update — "updated: {fields}"
///   - task.delete — "deleted" (rare; usually the row is gone too)
/// Pre-ADR-029-Phase-1 tasks may have empty audit; we show a hint
/// rather than an empty section so the surface stays explicable.
class _TaskAuditTimeline extends StatelessWidget {
  final List<Map<String, dynamic>> rows;
  final bool isDark;
  const _TaskAuditTimeline({required this.rows, required this.isDark});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          l10n.taskDetailActivityHeading,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w600,
            color: muted,
            letterSpacing: 0.4,
          ),
        ),
        const SizedBox(height: 6),
        if (rows.isEmpty)
          Text(
            l10n.taskDetailNoAuditRows,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 12,
              color: muted,
              fontStyle: FontStyle.italic,
            ),
          )
        else
          for (final r in rows) _AuditRow(row: r, isDark: isDark),
      ],
    );
  }
}

class _AuditRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final bool isDark;
  const _AuditRow({required this.row, required this.isDark});

  @override
  Widget build(BuildContext context) {
    final action = (row['action'] ?? '').toString();
    final actorHandle = (row['actor_handle'] ?? '').toString();
    final summary = (row['summary'] ?? '').toString();
    final tsRaw = (row['ts'] ?? '').toString();
    final ts = DateTime.tryParse(tsRaw);
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 8,
            height: 8,
            margin: const EdgeInsets.only(top: Spacing.s4),
            decoration: BoxDecoration(
              color: _actionColor(action, muted),
              shape: BoxShape.circle,
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(
                      action,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (actorHandle.isNotEmpty) ...[
                      const SizedBox(width: 6),
                      Text(
                        '· @${actorHandle.startsWith("@") ? actorHandle.substring(1) : actorHandle}',
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 11,
                          color: muted,
                        ),
                      ),
                    ],
                    const Spacer(),
                    if (ts != null)
                      Text(
                        '${formatRelative(ts)} ago',
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: FontSizes.label,
                          color: muted,
                        ),
                      ),
                  ],
                ),
                if (summary.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(
                    summary,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      color: muted,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  Color _actionColor(String action, Color muted) {
    if (action == 'task.create') return DesignColors.success;
    if (action == 'task.status') return DesignColors.primary;
    if (action == 'task.update') return DesignColors.terminalCyan;
    if (action == 'task.delete') return DesignColors.error;
    return muted;
  }
}

/// Renders a task body either as markdown (when it contains common
/// markdown markers) or as mono-spaced plain text. Mirrors the
/// heuristic already used by the Documents viewer so behaviour is
/// consistent across task/doc bodies.
class _TaskBody extends StatelessWidget {
  final String body;
  const _TaskBody({required this.body});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    if (body.isEmpty) {
      return Text(
        l10n.taskDetailNoBody,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 13,
          height: 1.4,
          color: DesignColors.textMuted,
        ),
      );
    }
    final looksMd =
        RegExp(r'(^|\n)(#|- |\* |\d+\. |```|> )').hasMatch(body);
    if (looksMd) {
      return MarkdownBody(
        data: body,
        selectable: true,
        styleSheet: MarkdownStyleSheet(
          p: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
          code: GoogleFonts.jetBrainsMono(fontSize: 12),
          h1: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
          h2: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
          h3: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
        ),
      );
    }
    return SelectableText(
      body,
      style: GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.4),
    );
  }
}


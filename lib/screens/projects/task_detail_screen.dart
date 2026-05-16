import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../theme/design_colors.dart';
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

  static const _statuses = [
    'todo',
    'in_progress',
    'blocked',
    'done',
    'cancelled',
  ];

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
          SnackBar(content: Text('Status change failed: $e')),
        );
      }
    }
  }

  Future<void> _setPriority(TaskPriority p) async {
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
          SnackBar(content: Text('Priority change failed: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Task'),
        actions: [
          IconButton(
            icon: const Icon(Icons.edit_outlined),
            tooltip: 'Edit task',
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
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final s in _statuses)
                ChoiceChip(
                  label: Text(s),
                  selected: status == s,
                  onSelected: (sel) {
                    if (sel) _setStatus(s);
                  },
                ),
            ],
          ),
          const SizedBox(height: 10),
          _PriorityLabel(isDark: isDark),
          const SizedBox(height: 6),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final p in TaskPriority.values)
                ChoiceChip(
                  avatar: TaskPriorityDot(priority: p, size: 12),
                  label: Text(p.label),
                  selected: priority == p,
                  onSelected: (sel) {
                    if (sel) _setPriority(p);
                  },
                ),
            ],
          ),
          const SizedBox(height: 16),
          _SourceSection(
            source: source,
            planId: planId,
            planStepId: planStepId,
            projectId: widget.projectId,
          ),
          const SizedBox(height: 16),
          // ADR-029 W9 attribution block: assignee + assigner + time
          // + result_summary surfaced together so the detail screen
          // answers "who, when, what happened" before the body.
          _TaskAttributionBlock(task: task, isDark: isDark),
          const SizedBox(height: 16),
          // W9 linked-work pane: jumps to the worker's session chat.
          // Hub denormalizes assignee_id; we resolve session_id from
          // the local sessions provider.
          _LinkedWorkSection(task: task, isDark: isDark),
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

/// ADR-029 W9: stacked metadata block for the task detail header.
/// Shows the assignee chip with status pip, assigner attribution,
/// the lifecycle timestamp (started / done / cancelled), and the
/// worker-supplied result summary when present. Each piece is
/// individually optional so pre-ADR-029 tasks still render cleanly.
class _TaskAttributionBlock extends StatelessWidget {
  final Map<String, dynamic> task;
  final bool isDark;
  const _TaskAttributionBlock({required this.task, required this.isDark});

  @override
  Widget build(BuildContext context) {
    final assigneeHandle =
        (task['assignee_handle'] ?? '').toString();
    final assigneeStatus =
        (task['assignee_status'] ?? '').toString();
    final assignerHandle =
        (task['assigner_handle'] ?? '').toString();
    final startedRaw = (task['started_at'] ?? '').toString();
    final completedRaw = (task['completed_at'] ?? '').toString();
    final updatedRaw = (task['updated_at'] ?? '').toString();
    final status = (task['status'] ?? '').toString();
    final summary = (task['result_summary'] ?? '').toString();
    final started = DateTime.tryParse(startedRaw);
    final completed = DateTime.tryParse(completedRaw);
    final cancelled = status == 'cancelled'
        ? (completed ?? DateTime.tryParse(updatedRaw))
        : null;
    if (assigneeHandle.isEmpty &&
        assignerHandle.isEmpty &&
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
            _row(
              context,
              icon: Icons.person_outline,
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Container(
                    width: 8,
                    height: 8,
                    decoration: BoxDecoration(
                      shape: BoxShape.circle,
                      color: _statusColor(assigneeStatus, muted),
                    ),
                  ),
                  const SizedBox(width: 6),
                  Text(
                    '@${_stripAt(assigneeHandle)}',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (assigneeStatus.isNotEmpty) ...[
                    const SizedBox(width: 6),
                    Text(
                      '· $assigneeStatus',
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 11,
                        color: muted,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          if (assignerHandle.isNotEmpty)
            _row(
              context,
              icon: Icons.swap_horiz,
              child: Text(
                'assigned by @${_stripAt(assignerHandle)}',
                style: GoogleFonts.spaceGrotesk(fontSize: 12),
              ),
            ),
          if (started != null || completed != null || cancelled != null)
            _row(
              context,
              icon: Icons.schedule,
              child: Text(
                _timeLine(started, completed, cancelled, status),
                style: GoogleFonts.spaceGrotesk(fontSize: 12),
              ),
            ),
          if (summary.isNotEmpty) ...[
            const SizedBox(height: 8),
            const Divider(height: 1),
            const SizedBox(height: 8),
            Text(
              'Result summary',
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

  Widget _row(BuildContext context,
      {required IconData icon, required Widget child}) {
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
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

  String _timeLine(DateTime? started, DateTime? completed,
      DateTime? cancelled, String status) {
    if (cancelled != null) {
      return 'cancelled ${formatRelative(cancelled)} ago';
    }
    if (completed != null && status == 'done') {
      return 'done ${formatRelative(completed)} ago'
          '${started != null ? " · started ${formatRelative(started)} ago" : ""}';
    }
    if (started != null) {
      return 'started ${formatRelative(started)} ago';
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

/// ADR-029 W9 linked-work pane. The assignee_id from the hub
/// identifies the agent currently doing this task; we look up its
/// session via the global sessions provider and provide a one-tap
/// "Open worker session" affordance. Plan-bound tasks without an
/// assignee fall back to a hint pointing at the plan viewer.
class _LinkedWorkSection extends ConsumerWidget {
  final Map<String, dynamic> task;
  final bool isDark;
  const _LinkedWorkSection({required this.task, required this.isDark});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final assigneeID = (task['assignee_id'] ?? '').toString();
    final assigneeHandle = (task['assignee_handle'] ?? '').toString();
    if (assigneeID.isEmpty) {
      return const SizedBox.shrink();
    }
    final sessionsState = ref.watch(sessionsProvider).value;
    final allSessions = <Map<String, dynamic>>[
      ...?sessionsState?.active,
      ...?sessionsState?.previous,
    ];
    final session = allSessions.firstWhere(
      (s) => (s['current_agent_id'] ?? '').toString() == assigneeID,
      orElse: () => const <String, dynamic>{},
    );
    final sessionId = (session['id'] ?? '').toString();
    final sessionTitle =
        (session['title'] ?? '').toString().isNotEmpty
            ? (session['title']).toString()
            : '@${_stripAt(assigneeHandle)}';
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
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
          Icon(Icons.forum_outlined, size: 16, color: muted),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              sessionId.isEmpty
                  ? 'Worker @${_stripAt(assigneeHandle)} has no live session.'
                  : 'Worker session: $sessionTitle',
              style: GoogleFonts.spaceGrotesk(fontSize: 12),
            ),
          ),
          if (sessionId.isNotEmpty)
            TextButton(
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
              child: const Text('Open'),
            ),
        ],
      ),
    );
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
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'ACTIVITY',
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
            'No audit rows yet for this task.',
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
            margin: const EdgeInsets.only(top: 5),
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
                          fontSize: 10,
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
    if (body.isEmpty) {
      return Text(
        '(no body)',
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

/// Small "PRIORITY" caption above the priority chip row. Matches the
/// styling convention used by [_SourceSection]'s "SOURCE" header so the
/// two section breaks line up visually.
class _PriorityLabel extends StatelessWidget {
  final bool isDark;
  const _PriorityLabel({required this.isDark});

  @override
  Widget build(BuildContext context) {
    return Text(
      'PRIORITY',
      style: GoogleFonts.jetBrainsMono(
        fontSize: 10,
        fontWeight: FontWeight.w700,
        letterSpacing: 0.5,
        color: isDark ? DesignColors.textMuted : DesignColors.textMutedLight,
      ),
    );
  }
}

/// Renders the task's origin: ad-hoc tasks get a muted "Created manually"
/// line, plan-materialized ones show the owning plan and a tap target that
/// opens the plan viewer. Matches the W2 wedge: tasks are the work atom,
/// plans generate them, and the link must be visible in the detail view.
class _SourceSection extends StatelessWidget {
  final String source;
  final String planId;
  final String planStepId;
  final String projectId;
  const _SourceSection({
    required this.source,
    required this.planId,
    required this.planStepId,
    required this.projectId,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final isPlan = source == 'plan' && planStepId.isNotEmpty;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color:
            isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color:
              isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'SOURCE',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.5,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
          const SizedBox(height: 6),
          if (!isPlan)
            Row(
              children: [
                Icon(
                  Icons.person_outline,
                  size: 16,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
                const SizedBox(width: 6),
                Text(
                  'Created manually',
                  style: GoogleFonts.spaceGrotesk(fontSize: 13),
                ),
              ],
            )
          else
            InkWell(
              onTap: planId.isEmpty
                  ? null
                  : () => Navigator.of(context).push(MaterialPageRoute(
                        builder: (_) => PlanViewerScreen(
                          planId: planId,
                          projectId: projectId,
                        ),
                      )),
              borderRadius: BorderRadius.circular(6),
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                child: Row(
                  children: [
                    const Icon(
                      Icons.playlist_play_outlined,
                      size: 16,
                      color: DesignColors.primary,
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text.rich(
                        TextSpan(
                          style: GoogleFonts.spaceGrotesk(fontSize: 13),
                          children: [
                            const TextSpan(text: 'Generated by plan step '),
                            TextSpan(
                              text: planStepId,
                              style: GoogleFonts.jetBrainsMono(
                                fontSize: 12,
                                color: DesignColors.primary,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                    if (planId.isNotEmpty)
                      const Icon(Icons.chevron_right, size: 18),
                  ],
                ),
              ),
            ),
          if (isPlan) ...[
            const SizedBox(height: 4),
            Text(
              'Closing this task manually does not complete the plan step; the executor owns that transition.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

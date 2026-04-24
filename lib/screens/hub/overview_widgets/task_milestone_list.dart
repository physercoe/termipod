import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../../../theme/task_priority_style.dart';
import '../task_detail_screen.dart';
import 'registry.dart';

/// `task_milestone_list` hero — default when a template doesn't opt in
/// to anything flashier. Lists open (non-done) tasks grouped by priority
/// desc, with a rolled-up "Show all" escape hatch when the list would
/// blow past the cap.
class TaskMilestoneListHero extends ConsumerStatefulWidget {
  final OverviewContext ctx;
  const TaskMilestoneListHero({super.key, required this.ctx});

  @override
  ConsumerState<TaskMilestoneListHero> createState() =>
      _TaskMilestoneListHeroState();
}

class _TaskMilestoneListHeroState
    extends ConsumerState<TaskMilestoneListHero> {
  static const int _cap = 10;

  List<Map<String, dynamic>>? _rows;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    final projectId = widget.ctx.projectId;
    if (client == null || projectId.isEmpty) {
      if (mounted) setState(() => _loading = false);
      return;
    }
    try {
      final cached = await client.listTasksCached(projectId);
      if (!mounted) return;
      setState(() {
        _rows = cached.body;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 24),
        child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
      );
    }
    final all = _rows ?? const <Map<String, dynamic>>[];
    final open = all
        .where((r) => (r['status'] ?? '').toString() != 'done')
        .toList();
    if (open.isEmpty) {
      return _EmptyCard(
        message: all.isEmpty
            ? 'No tasks yet. Create one from the Tasks tab.'
            : 'All tasks are done — nothing open.',
      );
    }

    // Stable sort by priority desc then updated_at desc. Fall back to
    // created_at when updated_at is missing so older backends still work.
    open.sort((a, b) {
      final pa = parseTaskPriority(a['priority']).rank;
      final pb = parseTaskPriority(b['priority']).rank;
      if (pa != pb) return pb.compareTo(pa);
      final ua = (a['updated_at'] ?? a['created_at'] ?? '').toString();
      final ub = (b['updated_at'] ?? b['created_at'] ?? '').toString();
      return ub.compareTo(ua);
    });

    final capped = open.take(_cap).toList();
    final overflow = open.length - capped.length;

    // Group by priority for section headers.
    final sections = <TaskPriority, List<Map<String, dynamic>>>{};
    for (final r in capped) {
      final p = parseTaskPriority(r['priority']);
      sections.putIfAbsent(p, () => []).add(r);
    }
    const order = [
      TaskPriority.urgent,
      TaskPriority.high,
      TaskPriority.med,
      TaskPriority.low,
    ];

    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (final p in order) ...[
          if (sections[p] != null) ...[
            Padding(
              padding: const EdgeInsets.only(top: 4, bottom: 4),
              child: Row(
                children: [
                  TaskPriorityDot(priority: p, size: 7),
                  const SizedBox(width: 6),
                  Text(
                    p.label,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.5,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    ),
                  ),
                ],
              ),
            ),
            for (final row in sections[p]!) _TaskRow(row: row),
          ],
        ],
        if (overflow > 0)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              '+ $overflow more — open Tasks tab',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: DesignColors.textMuted,
                fontStyle: FontStyle.italic,
              ),
            ),
          ),
      ],
    );
  }
}

class _TaskRow extends StatelessWidget {
  final Map<String, dynamic> row;
  const _TaskRow({required this.row});

  @override
  Widget build(BuildContext context) {
    final title = (row['title'] ?? '(untitled)').toString();
    final status = (row['status'] ?? 'todo').toString();
    final priority = parseTaskPriority(row['priority']);
    final projectId = (row['project_id'] ?? '').toString();
    final taskId = (row['id'] ?? '').toString();
    return InkWell(
      onTap: () {
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => TaskDetailScreen(
            projectId: projectId,
            taskId: taskId,
            initial: row,
          ),
        ));
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Row(
          children: [
            TaskPriorityDot(priority: priority, size: 8),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            const SizedBox(width: 8),
            Text(
              status,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: DesignColors.textMuted,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmptyCard extends StatelessWidget {
  final String message;
  const _EmptyCard({required this.message});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 16),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Text(
        message,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 12,
          color: DesignColors.textMuted,
        ),
      ),
    );
  }
}

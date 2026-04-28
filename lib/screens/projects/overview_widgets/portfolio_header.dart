import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../providers/hub_provider.dart';
import '../../../services/hub/open_steward_session.dart';
import '../../../theme/design_colors.dart';
import '../../../theme/task_priority_style.dart';
import '../reviews_screen.dart';
import 'registry.dart';

/// Always-present "portfolio" banner above the pluggable hero region
/// (A+B chassis, IA §6.2). Domain-agnostic — every goal-kind project
/// carries these fields regardless of template.
///
/// Renders a compact card with: goal (1-line expandable), status chip,
/// budget used/cap, steward status chip, attention count (tap →
/// ReviewsScreen), task progress bar, and a priority breakdown legend
/// showing only non-zero priorities to keep the chrome light.
class PortfolioHeader extends ConsumerStatefulWidget {
  final OverviewContext ctx;
  const PortfolioHeader({super.key, required this.ctx});

  @override
  ConsumerState<PortfolioHeader> createState() => _PortfolioHeaderState();
}

class _PortfolioHeaderState extends ConsumerState<PortfolioHeader> {
  bool _goalExpanded = false;
  int _total = 0;
  int _closed = 0;
  final Map<TaskPriority, int> _byPriority = {
    TaskPriority.urgent: 0,
    TaskPriority.high: 0,
    TaskPriority.med: 0,
    TaskPriority.low: 0,
  };
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    _loadTasks();
  }

  Future<void> _loadTasks() async {
    final client = ref.read(hubProvider.notifier).client;
    final projectId = widget.ctx.projectId;
    if (client == null || projectId.isEmpty) {
      if (mounted) setState(() => _loaded = true);
      return;
    }
    try {
      final cached = await client.listTasksCached(projectId);
      final rows = cached.body;
      final byP = {
        TaskPriority.urgent: 0,
        TaskPriority.high: 0,
        TaskPriority.med: 0,
        TaskPriority.low: 0,
      };
      var closed = 0;
      for (final r in rows) {
        if ((r['status'] ?? '').toString() == 'done') closed++;
        final p = parseTaskPriority(r['priority']);
        byP[p] = (byP[p] ?? 0) + 1;
      }
      if (!mounted) return;
      setState(() {
        _total = rows.length;
        _closed = closed;
        _byPriority
          ..clear()
          ..addAll(byP);
        _loaded = true;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loaded = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final project = widget.ctx.project;
    final goal = (project['goal'] ?? '').toString();
    final status = (project['status'] ?? '').toString();
    final budgetCents = project['budget_cents'];
    final stewardAgentId = (project['steward_agent_id'] ?? '').toString();

    // Count open attention for this project off the already-loaded list
    // so the banner doesn't trigger an extra round-trip.
    final attention = ref.watch(hubProvider).value?.attention ?? const [];
    final openAttention = attention
        .where((a) => (a['project_id'] ?? '').toString() == widget.ctx.projectId)
        .length;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (goal.isNotEmpty) ...[
            InkWell(
              onTap: () => setState(() => _goalExpanded = !_goalExpanded),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Icon(Icons.flag_outlined,
                      size: 14, color: DesignColors.primary),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      goal,
                      maxLines: _goalExpanded ? null : 1,
                      overflow:
                          _goalExpanded ? null : TextOverflow.ellipsis,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        height: 1.35,
                      ),
                    ),
                  ),
                  const SizedBox(width: 4),
                  Icon(
                    _goalExpanded
                        ? Icons.expand_less
                        : Icons.expand_more,
                    size: 16,
                    color: DesignColors.textMuted,
                  ),
                ],
              ),
            ),
            const SizedBox(height: 8),
          ],
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _StatusChip(label: status.isEmpty ? 'active' : status),
              _StewardChip(
                stewardAgentId: stewardAgentId,
                projectId: widget.ctx.projectId,
              ),
              if (budgetCents is int)
                _BudgetChip(usedCents: 0, capCents: budgetCents),
              if (openAttention > 0)
                _AttentionPill(
                  count: openAttention,
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) =>
                          ReviewsScreen(projectId: widget.ctx.projectId),
                    ),
                  ),
                ),
            ],
          ),
          if (_loaded && _total > 0) ...[
            const SizedBox(height: 10),
            _TaskProgressRow(
              total: _total,
              closed: _closed,
              isDark: isDark,
            ),
            const SizedBox(height: 6),
            _PriorityBreakdown(byPriority: _byPriority),
          ],
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  final String label;
  const _StatusChip({required this.label});

  @override
  Widget build(BuildContext context) {
    final color = switch (label) {
      'active' => DesignColors.terminalGreen,
      'archived' => DesignColors.textMuted,
      _ => DesignColors.primary,
    };
    return _Chip(label: label, color: color);
  }
}

class _StewardChip extends ConsumerWidget {
  final String stewardAgentId;
  final String projectId;
  const _StewardChip({
    required this.stewardAgentId,
    required this.projectId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // With no live steward status feed on the project payload yet, we
    // surface a coarse "configured" vs "not configured" tri-state. Full
    // running/idle/error states land when the steward config API ships
    // (see docs/ux-steward-audit.md).
    final configured = stewardAgentId.isNotEmpty;
    return _Chip(
      label: configured ? 'steward: configured' : 'steward: not-configured',
      color: configured ? DesignColors.terminalCyan : DesignColors.textMuted,
      icon: Icons.smart_toy_outlined,
      // Per ADR-009 D7: tapping the project's steward chip opens a
      // session scoped to this project. If a project-scoped session
      // already exists, openStewardSession routes there; otherwise it
      // falls back to the steward's default session for now (Phase 2
      // adds creation of new project-scoped sessions).
      onTap: configured && projectId.isNotEmpty
          ? () => openStewardSession(
                context,
                ref,
                scopeKind: 'project',
                scopeId: projectId,
              )
          : null,
    );
  }
}

class _BudgetChip extends StatelessWidget {
  final int usedCents;
  final int capCents;
  const _BudgetChip({required this.usedCents, required this.capCents});

  @override
  Widget build(BuildContext context) {
    final used = (usedCents / 100).toStringAsFixed(0);
    final cap = (capCents / 100).toStringAsFixed(0);
    return _Chip(
      label: 'budget: \$$used / \$$cap',
      color: DesignColors.warning,
      icon: Icons.attach_money,
    );
  }
}

class _AttentionPill extends StatelessWidget {
  final int count;
  final VoidCallback onTap;
  const _AttentionPill({required this.count, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: _Chip(
        label: count == 1 ? '1 review' : '$count reviews',
        color: DesignColors.warning,
        icon: Icons.flag_outlined,
      ),
    );
  }
}

class _Chip extends StatelessWidget {
  final String label;
  final Color color;
  final IconData? icon;
  final VoidCallback? onTap;
  const _Chip({
    required this.label,
    required this.color,
    this.icon,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final body = Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (icon != null) ...[
            Icon(icon, size: 11, color: color),
            const SizedBox(width: 3),
          ],
          Text(
            label,
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              fontWeight: FontWeight.w700,
              color: color,
            ),
          ),
        ],
      ),
    );
    if (onTap == null) return body;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: body,
    );
  }
}

class _TaskProgressRow extends StatelessWidget {
  final int total;
  final int closed;
  final bool isDark;
  const _TaskProgressRow({
    required this.total,
    required this.closed,
    required this.isDark,
  });

  @override
  Widget build(BuildContext context) {
    final ratio = total == 0 ? 0.0 : closed / total;
    return Row(
      children: [
        const Icon(Icons.check_circle_outline,
            size: 14, color: DesignColors.primary),
        const SizedBox(width: 6),
        Text(
          'Tasks',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: ClipRRect(
            borderRadius: BorderRadius.circular(3),
            child: LinearProgressIndicator(
              value: ratio,
              minHeight: 4,
              backgroundColor: isDark
                  ? DesignColors.borderDark
                  : DesignColors.borderLight,
              valueColor:
                  const AlwaysStoppedAnimation(DesignColors.primary),
            ),
          ),
        ),
        const SizedBox(width: 8),
        Text(
          '$closed / $total',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w600,
            color: isDark
                ? DesignColors.textSecondary
                : DesignColors.textSecondaryLight,
          ),
        ),
      ],
    );
  }
}

class _PriorityBreakdown extends StatelessWidget {
  final Map<TaskPriority, int> byPriority;
  const _PriorityBreakdown({required this.byPriority});

  @override
  Widget build(BuildContext context) {
    // Iteration order mirrors TaskPriority.rank desc so urgent is leftmost.
    const order = [
      TaskPriority.urgent,
      TaskPriority.high,
      TaskPriority.med,
      TaskPriority.low,
    ];
    final nonZero = order.where((p) => (byPriority[p] ?? 0) > 0).toList();
    if (nonZero.isEmpty) return const SizedBox.shrink();
    return Wrap(
      spacing: 10,
      runSpacing: 4,
      children: [
        for (final p in nonZero)
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              TaskPriorityDot(priority: p, size: 7),
              const SizedBox(width: 4),
              Text(
                '${p.label} ${byPriority[p]}',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.textMuted,
                ),
              ),
            ],
          ),
      ],
    );
  }
}

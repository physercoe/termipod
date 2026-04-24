import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../providers/hub_provider.dart';
import '../../../theme/design_colors.dart';
import '../project_create_sheet.dart';
import '../project_detail_screen.dart';
import 'registry.dart';

/// `children_status` hero (W5). Renders the current project's direct
/// sub-projects as a compact list: name · status chip · open-tasks count
/// · attention badge. Templates opt in by declaring
/// `overview_widget: children_status` in their YAML.
///
/// Data source: the already-loaded `hubProvider.projects` list, partitioned
/// client-side on `parent_project_id` (no new endpoint needed — see W5 brief).
/// Attention counts come from the same roll-up the Projects tab uses.
class ChildrenStatusHero extends ConsumerWidget {
  final OverviewContext ctx;
  const ChildrenStatusHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider).value;
    final all = hub?.projects ?? const <Map<String, dynamic>>[];
    final parentId = ctx.projectId;
    final parentKind = (ctx.project['kind'] ?? 'goal').toString();
    final children = [
      for (final p in all)
        if ((p['parent_project_id'] ?? '').toString() == parentId &&
            parentId.isNotEmpty)
          p,
    ];

    if (children.isEmpty) {
      return _EmptyCard(
        parentId: parentId,
        parentKind: parentKind,
        parentName: (ctx.project['name'] ?? '').toString(),
      );
    }

    final attention = hub?.attention ?? const [];
    final openByProject = <String, int>{};
    for (final a in attention) {
      final pid = (a['project_id'] ?? '').toString();
      if (pid.isEmpty) continue;
      openByProject[pid] = (openByProject[pid] ?? 0) + 1;
    }

    // Stable sort: attention-first (Blueprint A1), then active before
    // archived, then name.
    children.sort((a, b) {
      final attA = openByProject[(a['id'] ?? '').toString()] ?? 0;
      final attB = openByProject[(b['id'] ?? '').toString()] ?? 0;
      if (attA != attB) return attB.compareTo(attA);
      final sA = (a['status'] ?? '').toString() == 'archived' ? 1 : 0;
      final sB = (b['status'] ?? '').toString() == 'archived' ? 1 : 0;
      if (sA != sB) return sA.compareTo(sB);
      return (a['name'] ?? '').toString().compareTo(
          (b['name'] ?? '').toString());
    });

    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 6),
            child: Row(
              children: [
                const Icon(Icons.account_tree_outlined,
                    size: 14, color: DesignColors.primary),
                const SizedBox(width: 6),
                Text(
                  _headerLabel(children.length, parentKind),
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          for (var i = 0; i < children.length; i++) ...[
            _ChildRow(
              project: children[i],
              openAttention:
                  openByProject[(children[i]['id'] ?? '').toString()] ?? 0,
            ),
            if (i < children.length - 1) const Divider(height: 1),
          ],
        ],
      ),
    );
  }

  static String _headerLabel(int count, String parentKind) {
    final singular = parentKind == 'standing' ? 'sub-Workspace' : 'sub-project';
    final plural = parentKind == 'standing' ? 'sub-Workspaces' : 'sub-projects';
    return count == 1 ? '1 $singular' : '$count $plural';
  }
}

class _ChildRow extends StatelessWidget {
  final Map<String, dynamic> project;
  final int openAttention;
  const _ChildRow({required this.project, required this.openAttention});

  @override
  Widget build(BuildContext context) {
    final name = (project['name'] ?? '?').toString();
    final status = (project['status'] ?? '').toString();
    return InkWell(
      onTap: () {
        Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => ProjectDetailScreen(project: project),
        ));
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          children: [
            Expanded(
              child: Text(
                name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            const SizedBox(width: 8),
            _StatusChip(status: status),
            if (openAttention > 0) ...[
              const SizedBox(width: 6),
              _AttentionBadge(count: openAttention),
            ],
            const SizedBox(width: 4),
            const Icon(Icons.chevron_right,
                size: 16, color: DesignColors.textMuted),
          ],
        ),
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  final String status;
  const _StatusChip({required this.status});

  @override
  Widget build(BuildContext context) {
    final label = status.isEmpty ? 'active' : status;
    final color = switch (label) {
      'active' => DesignColors.terminalGreen,
      'archived' => DesignColors.textMuted,
      _ => DesignColors.primary,
    };
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

class _AttentionBadge extends StatelessWidget {
  final int count;
  const _AttentionBadge({required this.count});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: DesignColors.warning.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: DesignColors.warning.withValues(alpha: 0.5)),
      ),
      child: Text(
        '$count open',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: DesignColors.warning,
        ),
      ),
    );
  }
}

class _EmptyCard extends ConsumerWidget {
  final String parentId;
  final String parentKind;
  final String parentName;
  const _EmptyCard({
    required this.parentId,
    required this.parentKind,
    required this.parentName,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final label = parentKind == 'standing'
        ? 'This Workspace has no sub-Workspaces.'
        : 'This project has no sub-projects.';
    final buttonLabel = parentKind == 'standing'
        ? 'New sub-Workspace'
        : 'New sub-project';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Row(
        children: [
          const Icon(Icons.account_tree_outlined,
              size: 16, color: DesignColors.textMuted),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                color: DesignColors.textMuted,
              ),
            ),
          ),
          const SizedBox(width: 8),
          OutlinedButton.icon(
            icon: const Icon(Icons.add, size: 14),
            label: Text(
              buttonLabel,
              style: GoogleFonts.spaceGrotesk(fontSize: 12),
            ),
            style: OutlinedButton.styleFrom(
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              minimumSize: const Size(0, 32),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
            onPressed: () => _create(context, ref),
          ),
        ],
      ),
    );
  }

  Future<void> _create(BuildContext context, WidgetRef ref) async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => ProjectCreateSheet(
        initialKind: parentKind == 'standing' ? 'standing' : 'goal',
        parentProjectId: parentId,
        parentProjectName: parentName,
      ),
    );
    if (created == true) {
      await ref.read(hubProvider.notifier).refreshAll();
    }
  }
}

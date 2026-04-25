import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/activity_provider.dart';
import '../../providers/hub_provider.dart';
import '../../providers/urgent_tasks_provider.dart';
import '../../services/hub/open_team_channel.dart';
import '../../theme/design_colors.dart';
import '../../theme/task_priority_style.dart';
import '../../widgets/activity_digest_card.dart';
import '../../widgets/steward_badge.dart';
import '../../widgets/team_switcher.dart';
import '../projects/project_detail_screen.dart';
import '../projects/search_screen.dart';
import '../projects/task_detail_screen.dart';

/// Me tab — Tier-0 default landing per `docs/ia-redesign.md` §6.1.
///
/// Sections, top-down:
///   - My Work — horizontal strip of recent projects (tap → ProjectDetail).
///   - Attention — open attention items assigned to or relevant to me,
///     filterable by kind.
///
/// Wedge 5 adds the "Since you were last here" digest at the bottom,
/// mirroring the Activity tab's top-of-feed digest card (identical data;
/// Activity is the firehose, Me is the summary).
///
/// Attention sources by filter chip:
///   - Approvals — kind ∈ {approval_request, decision, template_proposal}.
///   - Agents    — kind='idle' / 'agent_error'.
///   - Messages  — every other attention kind.
class MeScreen extends ConsumerWidget {
  const MeScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hub = ref.watch(hubProvider);
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    final hubState = hub.value ?? const HubState();
    final items = _buildItems(hubState.attention);
    final projects = _recentProjects(hubState.projects);
    final filter = ref.watch(_filterProvider);
    final filtered = items.where(filter.matches).toList();
    final audit = ref.watch(recentAuditProvider);

    return Scaffold(
      floatingActionButton: hubState.configured
          ? FloatingActionButton.extended(
              onPressed: () => openHubMetaChannel(context, ref),
              icon: const Icon(Icons.smart_toy_outlined),
              label: const Text('Direct'),
            )
          : null,
      body: RefreshIndicator(
        onRefresh: () async {
          if (hubState.configured) {
            await ref.read(hubProvider.notifier).refreshAll();
          }
        },
        child: CustomScrollView(
          slivers: [
            SliverAppBar(
              floating: true,
              pinned: true,
              elevation: 0,
              backgroundColor: isDark
                  ? DesignColors.backgroundDark.withValues(alpha: 0.95)
                  : DesignColors.backgroundLight.withValues(alpha: 0.95),
              title: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ClipRRect(
                    borderRadius: BorderRadius.circular(8),
                    child: Image.asset(
                      'assets/icon/icon.png',
                      width: 32,
                      height: 32,
                    ),
                  ),
                  const SizedBox(width: 10),
                  Text(
                    l10n.tabMe,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 24,
                      fontWeight: FontWeight.w700,
                      color: colorScheme.onSurface,
                    ),
                  ),
                ],
              ),
              actions: [
                const TeamSwitcher(),
                IconButton(
                  icon: const Icon(Icons.search),
                  tooltip: 'Search events',
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const SearchScreen(),
                    ),
                  ),
                ),
              ],
            ),
            if (projects.isNotEmpty)
              SliverToBoxAdapter(
                child: _MyWorkStrip(projects: projects),
              ),
            SliverToBoxAdapter(
              child: _SectionLabel(
                text: l10n.meAttentionSection,
              ),
            ),
            SliverToBoxAdapter(
              child: _FilterBar(
                counts: _countsByFilter(items),
                selected: filter,
                onChanged: (f) => ref.read(_filterProvider.notifier).set(f),
              ),
            ),
            if (hubState.loading && items.isEmpty)
              const SliverToBoxAdapter(
                child: SizedBox(
                  height: 240,
                  child: Center(child: CircularProgressIndicator()),
                ),
              )
            else if (filtered.isEmpty)
              SliverToBoxAdapter(
                child: SizedBox(
                  height: 240,
                  child: _EmptyState(filter: filter),
                ),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                sliver: SliverList.separated(
                  itemCount: filtered.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 10),
                  itemBuilder: (_, i) => _MeCard(item: filtered[i]),
                ),
              ),
            const SliverToBoxAdapter(child: _UrgentTasksSection()),
            SliverToBoxAdapter(
              child: _SectionLabel(text: l10n.meDigestSection),
            ),
            SliverToBoxAdapter(
              child: ActivityDigestCard(events: audit.value ?? const []),
            ),
            const SliverToBoxAdapter(child: SizedBox(height: 100)),
          ],
        ),
      ),
    );
  }

  List<_MeItem> _buildItems(List<Map<String, dynamic>> attention) {
    final out = <_MeItem>[];
    for (final a in attention) {
      out.add(_MeItem.attention(a));
    }
    out.sort((a, b) => b.ts.compareTo(a.ts));
    return out;
  }

  /// Most-recent projects for the "My Work" strip. Cap at 6 so the strip
  /// fits one horizontal flick; full list still reachable from the Projects
  /// tab.
  List<Map<String, dynamic>> _recentProjects(
      List<Map<String, dynamic>> projects) {
    if (projects.isEmpty) return const [];
    final sorted = [...projects];
    sorted.sort((a, b) {
      final ta = (a['updated_at'] ?? a['created_at'] ?? '').toString();
      final tb = (b['updated_at'] ?? b['created_at'] ?? '').toString();
      return tb.compareTo(ta);
    });
    return sorted.take(6).toList();
  }

  Map<_Filter, int> _countsByFilter(List<_MeItem> items) {
    final counts = {for (final f in _Filter.values) f: 0};
    for (final item in items) {
      counts[_Filter.all] = (counts[_Filter.all] ?? 0) + 1;
      final f = item.filter;
      counts[f] = (counts[f] ?? 0) + 1;
    }
    return counts;
  }
}

enum _Filter { all, approvals, agents, messages }

extension _FilterX on _Filter {
  String get label {
    switch (this) {
      case _Filter.all:
        return 'All';
      case _Filter.approvals:
        return 'Approvals';
      case _Filter.agents:
        return 'Agents';
      case _Filter.messages:
        return 'Messages';
    }
  }

  bool matches(_MeItem item) {
    if (this == _Filter.all) return true;
    return item.filter == this;
  }
}

class _FilterNotifier extends Notifier<_Filter> {
  @override
  _Filter build() => _Filter.all;
  void set(_Filter f) => state = f;
}

final _filterProvider =
    NotifierProvider<_FilterNotifier, _Filter>(_FilterNotifier.new);

class _MeItem {
  final String id;
  final _Filter filter;
  final String kind;
  final String title;
  final String? subtitle;
  final DateTime ts;
  final String? severity;
  final String? actor;
  final Map<String, dynamic>? attention;

  const _MeItem({
    required this.id,
    required this.filter,
    required this.kind,
    required this.title,
    required this.ts,
    this.subtitle,
    this.severity,
    this.actor,
    this.attention,
  });

  factory _MeItem.attention(Map<String, dynamic> a) {
    final kind = (a['kind'] ?? 'attention').toString();
    final filter = _filterForAttention(kind);
    // actor_kind + actor_handle are the authoritative columns stamped by
    // the hub (migration 0016). An agent-raised attention gets actor for
    // badge rendering; system/user rows stay unbadged.
    final actorKind = (a['actor_kind'] ?? '').toString();
    final actorHandle = (a['actor_handle'] ?? '').toString();
    final actor = (actorKind == 'agent' && actorHandle.isNotEmpty)
        ? actorHandle
        : null;
    return _MeItem(
      id: (a['id'] ?? '').toString(),
      filter: filter,
      kind: kind,
      title: (a['summary'] ?? '').toString(),
      subtitle: (a['scope_kind'] ?? '').toString(),
      ts: _parseTs(a['created_at']?.toString()),
      severity: a['severity']?.toString(),
      actor: actor,
      attention: a,
    );
  }

  static _Filter _filterForAttention(String kind) {
    switch (kind) {
      case 'approval_request':
      case 'decision':
      case 'template_proposal':
        return _Filter.approvals;
      case 'idle':
      case 'agent_error':
        return _Filter.agents;
      default:
        return _Filter.messages;
    }
  }

  static DateTime _parseTs(String? raw) {
    if (raw == null || raw.isEmpty) return DateTime.now();
    return DateTime.tryParse(raw) ?? DateTime.now();
  }
}

class _FilterBar extends StatelessWidget {
  final Map<_Filter, int> counts;
  final _Filter selected;
  final ValueChanged<_Filter> onChanged;

  const _FilterBar({
    required this.counts,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Row(
        children: [
          for (final f in _Filter.values) ...[
            _FilterChip(
              label: f.label,
              count: counts[f] ?? 0,
              selected: selected == f,
              onTap: () => onChanged(f),
            ),
            const SizedBox(width: 8),
          ],
        ],
      ),
    );
  }
}

class _FilterChip extends StatelessWidget {
  final String label;
  final int count;
  final bool selected;
  final VoidCallback onTap;

  const _FilterChip({
    required this.label,
    required this.count,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = selected
        ? DesignColors.primary
        : (isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight);
    final fg = selected
        ? Colors.white
        : (isDark
            ? DesignColors.textSecondary
            : DesignColors.textSecondaryLight);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: selected
                ? DesignColors.primary
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              label,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: fg,
              ),
            ),
            if (count > 0) ...[
              const SizedBox(width: 6),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: selected
                      ? Colors.white.withValues(alpha: 0.2)
                      : DesignColors.primary.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Text(
                  '$count',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                    color: selected ? Colors.white : DesignColors.primary,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  final _Filter filter;
  const _EmptyState({required this.filter});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final msg = filter == _Filter.all
        ? 'Nothing in the inbox'
        : 'Nothing here';
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.inbox_outlined, size: 64, color: muted),
          const SizedBox(height: 16),
          Text(
            msg,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 16,
              fontWeight: FontWeight.w600,
              color: muted,
            ),
          ),
        ],
      ),
    );
  }
}

class _MeCard extends ConsumerWidget {
  final _MeItem item;
  const _MeCard({required this.item});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;
    final accent = _accentColor(item);

    return InkWell(
      onTap: () => _primaryAction(context, ref),
      borderRadius: BorderRadius.circular(12),
      child: Container(
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: accent.withValues(alpha: 0.4)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                _KindChip(text: _kindLabel(item.kind), color: accent),
                if (item.severity != null && item.severity!.isNotEmpty) ...[
                  const SizedBox(width: 6),
                  _KindChip(
                    text: item.severity!,
                    color: _severityColor(item.severity!),
                  ),
                ],
                if (item.actor != null && StewardBadge.matches(item.actor!))
                  const StewardBadge(),
                const Spacer(),
                Text(
                  _shortTs(item.ts),
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Text(
              item.title,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 14,
                fontWeight: FontWeight.w600,
                color: colorScheme.onSurface,
              ),
            ),
            if (item.subtitle != null && item.subtitle!.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                item.subtitle!,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                ),
              ),
            ],
            if (item.filter == _Filter.approvals) ...[
              const SizedBox(height: 10),
              _ApprovalActions(id: item.id),
            ],
          ],
        ),
      ),
    );
  }

  void _primaryAction(BuildContext context, WidgetRef ref) {
    // For attention items the inline Approve/Reject/Resolve buttons do the
    // real work; tapping the card is a no-op for now. A future patch can
    // push a full-screen detail view here.
  }

  Color _accentColor(_MeItem item) {
    switch (item.filter) {
      case _Filter.approvals:
        return DesignColors.primary;
      case _Filter.agents:
        return Colors.orange;
      case _Filter.messages:
        return DesignColors.primary;
      case _Filter.all:
        return DesignColors.primary;
    }
  }

  Color _severityColor(String s) {
    switch (s) {
      case 'critical':
        return DesignColors.error;
      case 'major':
        return Colors.orange;
      case 'minor':
      default:
        return DesignColors.primary;
    }
  }

  String _kindLabel(String kind) {
    if (kind.isEmpty) return 'event';
    return kind.replaceAll('_', ' ');
  }

  String _shortTs(DateTime ts) {
    final diff = DateTime.now().difference(ts);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }
}

class _ApprovalActions extends ConsumerWidget {
  final String id;
  const _ApprovalActions({required this.id});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Row(
      children: [
        OutlinedButton.icon(
          icon: const Icon(Icons.check, size: 16),
          label: const Text('Approve'),
          onPressed: () => _decide(context, ref, 'approve'),
        ),
        const SizedBox(width: 8),
        OutlinedButton.icon(
          icon: const Icon(Icons.close, size: 16),
          label: const Text('Reject'),
          style: OutlinedButton.styleFrom(
            foregroundColor: DesignColors.error,
          ),
          onPressed: () => _decide(context, ref, 'reject'),
        ),
      ],
    );
  }

  Future<void> _decide(
    BuildContext context,
    WidgetRef ref,
    String decision,
  ) async {
    try {
      await ref
          .read(hubProvider.notifier)
          .decide(id, decision, by: '@mobile');
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Decision recorded: $decision')),
        );
      }
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Decide failed: $e')),
        );
      }
    }
  }
}

/// W3 affordance: surfaces the count of open `priority=urgent` tasks
/// across the team's projects, with one-tap entry into the top offenders.
/// Hidden when the count is zero so the Me tab stays quiet in the
/// common case.
class _UrgentTasksSection extends ConsumerWidget {
  const _UrgentTasksSection();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final summary =
        ref.watch(urgentTasksProvider).value ?? UrgentTasksSummary.empty;
    if (summary.count == 0) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    final urgentColor = taskPriorityColor(TaskPriority.urgent);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const _SectionLabel(text: 'Urgent'),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 6, 16, 4),
          child: Container(
            decoration: BoxDecoration(
              color: bg,
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: urgentColor.withValues(alpha: 0.4)),
            ),
            child: Column(
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(14, 12, 14, 6),
                  child: Row(
                    children: [
                      Container(
                        width: 10,
                        height: 10,
                        decoration: BoxDecoration(
                          color: urgentColor,
                          shape: BoxShape.circle,
                        ),
                      ),
                      const SizedBox(width: 10),
                      Expanded(
                        child: Text(
                          '${summary.count} urgent '
                          '${summary.count == 1 ? "task" : "tasks"}',
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 14,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
                for (final row in summary.top)
                  InkWell(
                    onTap: () => Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => TaskDetailScreen(
                        projectId: row.projectId,
                        taskId: row.taskId,
                      ),
                    )),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(
                          horizontal: 14, vertical: 8),
                      child: Row(
                        children: [
                          Container(
                            width: 6,
                            height: 6,
                            decoration: BoxDecoration(
                              color: urgentColor,
                              shape: BoxShape.circle,
                            ),
                          ),
                          const SizedBox(width: 10),
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  row.title,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: GoogleFonts.spaceGrotesk(
                                    fontSize: 13,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                                const SizedBox(height: 2),
                                Text(
                                  '${row.projectName} · ${row.status}',
                                  style: GoogleFonts.jetBrainsMono(
                                    fontSize: 10,
                                    color: muted,
                                  ),
                                ),
                              ],
                            ),
                          ),
                          Icon(Icons.chevron_right, size: 16, color: muted),
                        ],
                      ),
                    ),
                  ),
                Container(height: 1, color: border),
                const SizedBox(height: 4),
              ],
            ),
          ),
        ),
      ],
    );
  }
}

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel({required this.text});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 10, 20, 2),
      child: Text(
        text.toUpperCase(),
        style: GoogleFonts.spaceGrotesk(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          color: muted,
          letterSpacing: 0.8,
        ),
      ),
    );
  }
}

class _MyWorkStrip extends StatelessWidget {
  final List<Map<String, dynamic>> projects;
  const _MyWorkStrip({required this.projects});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _SectionLabel(text: l10n.meMyWorkSection),
        SizedBox(
          height: 76,
          child: ListView.separated(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            itemCount: projects.length,
            separatorBuilder: (_, __) => const SizedBox(width: 8),
            itemBuilder: (ctx, i) {
              final p = projects[i];
              final name = p['name']?.toString() ?? '?';
              final status = p['status']?.toString() ?? '';
              return InkWell(
                onTap: () => Navigator.of(ctx).push(MaterialPageRoute(
                  builder: (_) => ProjectDetailScreen(project: p),
                )),
                borderRadius: BorderRadius.circular(10),
                child: Container(
                  width: 160,
                  padding:
                      const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                  decoration: BoxDecoration(
                    color: bg,
                    borderRadius: BorderRadius.circular(10),
                    border: Border.all(color: border),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisAlignment: MainAxisAlignment.spaceBetween,
                    children: [
                      Text(
                        name,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 13,
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      if (status.isNotEmpty)
                        Text(
                          status,
                          style: GoogleFonts.jetBrainsMono(
                            fontSize: 10,
                            color: muted,
                          ),
                        ),
                    ],
                  ),
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}

/// Pinned CTA to direct the steward from Me. Closes the asymmetry where
class _KindChip extends StatelessWidget {
  final String text;
  final Color color;
  const _KindChip({required this.text, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        text,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
          letterSpacing: 0.3,
        ),
      ),
    );
  }
}

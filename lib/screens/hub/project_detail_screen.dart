import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../theme/task_priority_style.dart';
import '../../widgets/hub_offline_banner.dart';
import '../../widgets/sweep_scatter.dart';
import '../../widgets/team_switcher.dart';
import 'archived_agents_screen.dart';
import 'artifacts_screen.dart';
import 'blobs_section.dart';
import 'docs_section.dart';
import 'documents_screen.dart';
import 'overview_widgets/portfolio_header.dart';
import 'overview_widgets/registry.dart';
import 'plans_screen.dart';
import 'project_channel_create_sheet.dart';
import 'project_channel_screen.dart';
import 'project_create_sheet.dart';
import 'project_edit_sheet.dart';
import 'project_task_create_sheet.dart';
import 'reviews_screen.dart';
import 'runs_screen.dart';
import 'schedules_screen.dart';
import 'task_detail_screen.dart';

/// Linear-style project detail aligned to `docs/ia-redesign.md` §6.2.
/// Horizontal PageView over the IA-canonical sub-surfaces — the pill bar
/// and the PageView are bound to the same index so either tapping a pill
/// or swiping the body flips both.
///
/// Tabs:
///   0 Overview  — goal, status, metadata, shortcut tiles into Runs /
///                 Reviews / Documents / Schedules / Plans / Blobs, and
///                 the archive action. Replaces the old "Info" tab.
///   1 Agents    — agents scoped to this project; archive filter via
///                 the AppBar action.
///   2 Channel   — channel list + per-channel composer; FAB creates.
///   3 Tasks     — Kanban over this project's tasks; FAB creates.
///   4 Files     — read-only tree of the project's docs_root filesystem.
///                 Distinct from the Overview Documents shortcut, which
///                 opens the DB `documents` entity (authored memos,
///                 drafts, reports, reviews — blueprint §6.7).
///
/// Retired from the previous 7-tab shape:
///   - Activity: team-wide feed lives on the Activity top-level tab,
///               filtered to this project.
///   - Blobs:    moved to an Overview shortcut tile (device-local cache).
///   - Info:     rolled into Overview.
class ProjectDetailScreen extends ConsumerStatefulWidget {
  final Map<String, dynamic> project;
  const ProjectDetailScreen({super.key, required this.project});

  @override
  ConsumerState<ProjectDetailScreen> createState() =>
      _ProjectDetailScreenState();
}

class _ProjectDetailScreenState extends ConsumerState<ProjectDetailScreen> {
  final _pager = PageController();
  int _index = 0;
  late Map<String, dynamic> _project;

  static const _labels = [
    'Overview',
    'Agents',
    'Channel',
    'Tasks',
    'Files',
  ];

  @override
  void initState() {
    super.initState();
    _project = Map<String, dynamic>.from(widget.project);
  }

  @override
  void dispose() {
    _pager.dispose();
    super.dispose();
  }

  void _jump(int i) {
    setState(() => _index = i);
    _pager.animateToPage(
      i,
      duration: const Duration(milliseconds: 220),
      curve: Curves.easeOut,
    );
  }

  Future<void> _edit() async {
    final updated = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => ProjectEditSheet(project: _project),
    );
    if (updated == null || !mounted) return;
    setState(() => _project = Map<String, dynamic>.from(updated));
    await ref.read(hubProvider.notifier).refreshAll();
  }

  Future<void> _createSubProject() async {
    final kind = (_project['kind'] ?? 'goal').toString();
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => ProjectCreateSheet(
        initialKind: kind == 'standing' ? 'standing' : 'goal',
        parentProjectId: (_project['id'] ?? '').toString(),
        parentProjectName: (_project['name'] ?? '').toString(),
      ),
    );
    if (created == true && mounted) {
      await ref.read(hubProvider.notifier).refreshAll();
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final kind = (_project['kind'] ?? 'goal').toString();
    final isWorkspace = kind == 'standing';
    final name = (_project['name'] ??
            (isWorkspace ? l10n.kindWorkspace : l10n.kindProject))
        .toString();
    final projectId = (_project['id'] ?? '').toString();
    final parentId = (_project['parent_project_id'] ?? '').toString();
    // Depth-1 child → can't parent another level. The action is still in
    // the overflow menu so it's discoverable, but disabled with a hint.
    final atMaxDepth = parentId.isNotEmpty;
    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Flexible(
              child: Text(
                name,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 18, fontWeight: FontWeight.w700),
              ),
            ),
            const SizedBox(width: 8),
            ProjectKindChip(kind: kind),
          ],
        ),
        actions: [
          const TeamSwitcher(),
          IconButton(
            icon: const Icon(Icons.edit_outlined),
            tooltip: isWorkspace
                ? l10n.workspaceDetailEditTooltip
                : l10n.projectDetailEditTooltip,
            onPressed: _edit,
          ),
          PopupMenuButton<String>(
            tooltip: 'More actions',
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              if (v == 'new_sub' && !atMaxDepth) {
                _createSubProject();
              } else if (v == 'new_sub' && atMaxDepth) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text(
                      'Max sub-project depth is 2 — this project is already nested.',
                    ),
                    duration: Duration(seconds: 3),
                  ),
                );
              }
            },
            itemBuilder: (ctx) => [
              PopupMenuItem(
                value: 'new_sub',
                enabled: !atMaxDepth,
                child: Row(
                  children: [
                    const Icon(Icons.account_tree_outlined, size: 16),
                    const SizedBox(width: 8),
                    Text(isWorkspace
                        ? 'New sub-Workspace'
                        : 'New sub-project'),
                  ],
                ),
              ),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          if (parentId.isNotEmpty)
            _ParentBreadcrumb(parentProjectId: parentId),
          _PillBar(
            labels: _labels,
            selected: _index,
            onChanged: _jump,
          ),
          Expanded(
            child: PageView(
              controller: _pager,
              onPageChanged: (i) => setState(() => _index = i),
              children: [
                _OverviewView(project: _project),
                _AgentsView(projectId: projectId),
                _ChannelsView(projectId: projectId),
                _TasksView(projectId: projectId),
                DocsSection(projectId: projectId),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _PillBar extends StatelessWidget {
  final List<String> labels;
  final int selected;
  final ValueChanged<int> onChanged;
  const _PillBar({
    required this.labels,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Row(
          children: [
            for (var i = 0; i < labels.length; i++) ...[
              _Pill(
                label: labels[i],
                selected: selected == i,
                onTap: () => onChanged(i),
              ),
              const SizedBox(width: 8),
            ],
          ],
        ),
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _Pill({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary
              : (isDark
                  ? DesignColors.surfaceDark
                  : DesignColors.surfaceLight),
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: selected
                ? DesignColors.primary
                : (isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight),
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 12,
            fontWeight: FontWeight.w600,
            color: selected
                ? Colors.white
                : (isDark
                    ? DesignColors.textSecondary
                    : DesignColors.textSecondaryLight),
          ),
        ),
      ),
    );
  }
}

// Activity retired from project detail tabs per IA §6.2 — the top-level
// Activity tab covers the team feed and filters by project. The helper
// view below is parked behind an ignore so future demos can reinstate
// it in Overview as a digest card without resurrecting the queries.

// ignore: unused_element
class _ActivityView extends ConsumerStatefulWidget {
  final String projectId;
  const _ActivityView({required this.projectId});

  @override
  ConsumerState<_ActivityView> createState() => _ActivityViewState();
}

class _ActivityViewState extends ConsumerState<_ActivityView> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _events = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final channels = await client.listChannels(widget.projectId);
      final all = <Map<String, dynamic>>[];
      for (final c in channels) {
        final id = (c['id'] ?? '').toString();
        if (id.isEmpty) continue;
        final evts = await client.listProjectChannelEvents(
          widget.projectId,
          id,
          limit: 20,
        );
        for (final e in evts) {
          e['channel_name'] = c['name'];
          all.add(e);
        }
      }
      all.sort((a, b) {
        final at = (a['received_ts'] ?? a['ts'] ?? '').toString();
        final bt = (b['received_ts'] ?? b['ts'] ?? '').toString();
        return bt.compareTo(at);
      });
      if (!mounted) return;
      setState(() {
        _events = all;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error)),
        ),
      );
    }
    if (_events.isEmpty) {
      return const _Placeholder(text: 'No activity yet');
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
        itemCount: _events.length,
        separatorBuilder: (_, __) => const SizedBox(height: 8),
        itemBuilder: (_, i) => _ActivityRow(evt: _events[i]),
      ),
    );
  }
}

class _ActivityRow extends StatelessWidget {
  final Map<String, dynamic> evt;
  const _ActivityRow({required this.evt});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final channel = (evt['channel_name'] ?? '').toString();
    final from = (evt['from_id'] ?? '').toString();
    final ts = (evt['ts'] ?? evt['received_ts'] ?? '').toString();
    final preview = _preview((evt['parts'] as List?) ?? const []);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      decoration: BoxDecoration(
        color:
            isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
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
          Row(
            children: [
              if (channel.isNotEmpty)
                Text('#$channel',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      color: DesignColors.primary,
                    )),
              if (from.isNotEmpty) ...[
                const SizedBox(width: 6),
                Text(from,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    )),
              ],
              const Spacer(),
              Text(_shortTs(ts),
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 9,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight)),
            ],
          ),
          const SizedBox(height: 4),
          Text(preview,
              style:
                  GoogleFonts.spaceGrotesk(fontSize: 13, height: 1.3)),
        ],
      ),
    );
  }

  String _preview(List<dynamic> parts) {
    for (final raw in parts) {
      if (raw is! Map) continue;
      if (raw['kind'] == 'text' && raw['text'] is String) {
        final t = (raw['text'] as String).trim();
        if (t.isNotEmpty) return t;
      }
    }
    return '(no text)';
  }

  String _shortTs(String raw) {
    if (raw.isEmpty) return '';
    final t = DateTime.tryParse(raw);
    if (t == null) return raw;
    final diff = DateTime.now().difference(t);
    if (diff.inMinutes < 1) return 'now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m';
    if (diff.inHours < 24) return '${diff.inHours}h';
    if (diff.inDays < 7) return '${diff.inDays}d';
    return '${(diff.inDays / 7).floor()}w';
  }
}

// ---- Channels ----

class _ChannelsView extends ConsumerStatefulWidget {
  final String projectId;
  const _ChannelsView({required this.projectId});

  @override
  ConsumerState<_ChannelsView> createState() => _ChannelsViewState();
}

class _ChannelsViewState extends ConsumerState<_ChannelsView> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _channels = const [];
  DateTime? _staleSince;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final cached = await client.listChannelsCached(widget.projectId);
      if (!mounted) return;
      setState(() {
        _channels = cached.body;
        _staleSince = cached.staleSince;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _create() async {
    final created = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      builder: (_) =>
          ProjectChannelCreateSheet(projectId: widget.projectId),
    );
    if (created == null || !mounted) return;
    setState(() => _channels = [..._channels, created]);
  }

  void _open(Map<String, dynamic> row) {
    final id = (row['id'] ?? '').toString();
    final name = (row['name'] ?? id).toString();
    if (id.isEmpty) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ProjectChannelScreen(
          projectId: widget.projectId,
          channelId: id,
          channelName: name,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error)),
        ),
      );
    }
    final list = _channels.isEmpty
        ? const _Placeholder(text: 'No channels yet — tap + to create')
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
              itemCount: _channels.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) => _ChannelRow(
                row: _channels[i],
                onTap: () => _open(_channels[i]),
              ),
            ),
          );
    final body = Column(
      children: [
        HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
        Expanded(child: list),
      ],
    );
    return Stack(
      children: [
        body,
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.extended(
            heroTag: 'project-channel-fab-${widget.projectId}',
            onPressed: _create,
            icon: const Icon(Icons.add),
            label: const Text('Channel'),
          ),
        ),
      ],
    );
  }
}

class _ChannelRow extends StatelessWidget {
  final Map<String, dynamic> row;
  final VoidCallback onTap;
  const _ChannelRow({required this.row, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final name = (row['name'] ?? '').toString();
    final id = (row['id'] ?? '').toString();
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(10),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: isDark
              ? DesignColors.surfaceDark
              : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          children: [
            const Icon(Icons.tag, size: 18, color: DesignColors.primary),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    name.isEmpty ? '(unnamed)' : name,
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (id.isNotEmpty)
                    Text(
                      id,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 10,
                        color: isDark
                            ? DesignColors.textMuted
                            : DesignColors.textMutedLight,
                      ),
                    ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, size: 18),
          ],
        ),
      ),
    );
  }
}

// ---- Tasks ----

class _TasksView extends ConsumerStatefulWidget {
  final String projectId;
  const _TasksView({required this.projectId});

  @override
  ConsumerState<_TasksView> createState() => _TasksViewState();
}

class _TasksViewState extends ConsumerState<_TasksView> {
  bool _loading = true;
  String? _error;
  List<Map<String, dynamic>> _tasks = const [];
  String? _statusFilter;
  TaskPriority? _priorityFilter;
  DateTime? _staleSince;

  // Kept in sync with task_detail_screen's lifecycle list so chips and
  // detail-view transitions agree.
  static const _statusFilters = <String?>[
    null,
    'todo',
    'in_progress',
    'blocked',
    'done',
  ];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final cached = await client.listTasksCached(
        widget.projectId,
        status: _statusFilter,
        priority: _priorityFilter?.wire,
      );
      if (!mounted) return;
      setState(() {
        _tasks = cached.body;
        _staleSince = cached.staleSince;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _openCreate() async {
    final created = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      builder: (_) => ProjectTaskCreateSheet(projectId: widget.projectId),
    );
    if (created == true) await _load();
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              style: GoogleFonts.jetBrainsMono(
                  fontSize: 11, color: DesignColors.error)),
        ),
      );
    }
    final list = _tasks.isEmpty
        ? _Placeholder(
            text: _statusFilter == null
                ? 'No tasks yet — tap + to create'
                : 'No $_statusFilter tasks.',
          )
        : RefreshIndicator(
            onRefresh: _load,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
              itemCount: _tasks.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) => _TaskTile(
                task: _tasks[i],
                projectId: widget.projectId,
                onChanged: _load,
              ),
            ),
          );
    return Stack(
      children: [
        Positioned.fill(
          child: Column(
            children: [
              _TaskStatusBar(
                statuses: _statusFilters,
                selected: _statusFilter,
                onChanged: (v) {
                  if (_statusFilter == v) return;
                  setState(() => _statusFilter = v);
                  _load();
                },
              ),
              _TaskPriorityBar(
                selected: _priorityFilter,
                onChanged: (v) {
                  if (_priorityFilter == v) return;
                  setState(() => _priorityFilter = v);
                  _load();
                },
              ),
              HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
              Expanded(child: list),
            ],
          ),
        ),
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.small(
            heroTag: 'project-tasks-fab',
            onPressed: _openCreate,
            child: const Icon(Icons.add),
          ),
        ),
      ],
    );
  }
}

class _TaskStatusBar extends StatelessWidget {
  final List<String?> statuses;
  final String? selected;
  final ValueChanged<String?> onChanged;
  const _TaskStatusBar({
    required this.statuses,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: Row(
        children: [
          for (final s in statuses) ...[
            _TaskFilterPill(
              label: s ?? 'all',
              selected: s == selected,
              onTap: () => onChanged(s),
            ),
            const SizedBox(width: 6),
          ],
        ],
      ),
    );
  }
}

/// Horizontal priority filter beneath the status bar. Null = "any".
/// Matches the look of `_TaskStatusBar` so the two rows read as a
/// single compound filter and the extra visual weight is honest about
/// being optional.
class _TaskPriorityBar extends StatelessWidget {
  final TaskPriority? selected;
  final ValueChanged<TaskPriority?> onChanged;
  const _TaskPriorityBar({
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    const options = <TaskPriority?>[
      null,
      TaskPriority.urgent,
      TaskPriority.high,
      TaskPriority.med,
      TaskPriority.low,
    ];
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
      child: Row(
        children: [
          for (final p in options) ...[
            _TaskFilterPill(
              label: p?.label ?? 'any priority',
              selected: p == selected,
              onTap: () => onChanged(p),
              leadingDot: p == null ? null : taskPriorityColor(p),
            ),
            const SizedBox(width: 6),
          ],
        ],
      ),
    );
  }
}

class _TaskFilterPill extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  final Color? leadingDot;
  const _TaskFilterPill({
    required this.label,
    required this.selected,
    required this.onTap,
    this.leadingDot,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(12),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        decoration: BoxDecoration(
          color: selected
              ? DesignColors.primary.withValues(alpha: 0.2)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(
            color:
                selected ? DesignColors.primary : DesignColors.borderDark,
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (leadingDot != null) ...[
              Container(
                width: 6,
                height: 6,
                decoration: BoxDecoration(
                  color: leadingDot,
                  shape: BoxShape.circle,
                ),
              ),
              const SizedBox(width: 5),
            ],
            Text(
              label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
                color:
                    selected ? DesignColors.primary : DesignColors.textMuted,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _TaskTile extends StatelessWidget {
  final Map<String, dynamic> task;
  final String projectId;
  final VoidCallback onChanged;
  const _TaskTile({
    required this.task,
    required this.projectId,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final title = (task['title'] ?? '?').toString();
    final status = (task['status'] ?? '').toString();
    final preview = _previewLine((task['body_md'] ?? '').toString());
    final fromPlan = (task['source'] ?? 'ad_hoc').toString() == 'plan';
    final priority = parseTaskPriority(task['priority']);
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () async {
        await Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => TaskDetailScreen(
            projectId: projectId,
            taskId: (task['id'] ?? '').toString(),
            initial: task,
          ),
        ));
        onChanged();
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color:
              isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: isDark
                ? DesignColors.borderDark
                : DesignColors.borderLight,
          ),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: _StatusDot(status: status),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Tooltip(
                        message: 'Priority: ${priority.label}',
                        child: TaskPriorityDot(priority: priority),
                      ),
                      const SizedBox(width: 8),
                      Flexible(
                        child: Text(title,
                            style: GoogleFonts.spaceGrotesk(
                                fontSize: 13,
                                fontWeight: FontWeight.w600)),
                      ),
                      if (fromPlan) ...[
                        const SizedBox(width: 6),
                        Tooltip(
                          message: 'Generated by a plan step',
                          child: Icon(
                            Icons.playlist_play_outlined,
                            size: 13,
                            color: isDark
                                ? DesignColors.textMuted
                                : DesignColors.textMutedLight,
                          ),
                        ),
                      ],
                    ],
                  ),
                  if (preview.isNotEmpty) ...[
                    const SizedBox(height: 2),
                    Text(
                      preview,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
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
            ),
            const SizedBox(width: 8),
            Padding(
              padding: const EdgeInsets.only(top: 1),
              child: Text(status,
                  style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight)),
            ),
          ],
        ),
      ),
    );
  }

  /// First non-empty line of the body with common markdown leaders
  /// stripped — enough to give scannable context without importing a
  /// full markdown renderer into a tile.
  String _previewLine(String body) {
    if (body.isEmpty) return '';
    for (final raw in const LineSplitter().convert(body)) {
      final line = raw.trim();
      if (line.isEmpty) continue;
      final stripped = line
          .replaceFirst(RegExp(r'^#{1,6}\s+'), '')
          .replaceFirst(RegExp(r'^[-*+]\s+'), '')
          .replaceFirst(RegExp(r'^\d+\.\s+'), '')
          .replaceFirst(RegExp(r'^>\s+'), '');
      if (stripped.isNotEmpty) return stripped;
    }
    return '';
  }
}

class _StatusDot extends StatelessWidget {
  final String status;
  const _StatusDot({required this.status});
  @override
  Widget build(BuildContext context) {
    Color c;
    switch (status) {
      case 'done':
        c = Colors.green;
        break;
      case 'in_progress':
        c = Colors.orange;
        break;
      case 'blocked':
        c = DesignColors.error;
        break;
      default:
        c = DesignColors.primary;
    }
    return Container(
      width: 10,
      height: 10,
      decoration: BoxDecoration(
        color: c,
        shape: BoxShape.circle,
      ),
    );
  }
}

// ---- Agents (filtered to this project) ----
// Per IA line 444 agents live *inside* project detail, not as a
// sibling tab under Projects. The archive action on the top-right
// replaces the old HubScreen-level _AgentsTab archive button (Gap #6).

class _AgentsView extends ConsumerWidget {
  final String projectId;
  const _AgentsView({required this.projectId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final hubState = ref.watch(hubProvider).value;
    final all = hubState?.agents ?? const [];
    final rows = all
        .where((a) => (a['project_id'] ?? '').toString() == projectId)
        .toList();
    final kind = (hubState?.projects ?? const <Map<String, dynamic>>[])
        .firstWhere(
          (p) => (p['id'] ?? '').toString() == projectId,
          orElse: () => const <String, dynamic>{},
        )['kind']
        ?.toString() ??
        'goal';
    final isWorkspace = kind == 'standing';
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final body = rows.isEmpty
        ? _Placeholder(
            text: isWorkspace
                ? l10n.workspaceNoAgents
                : l10n.projectNoAgents)
        : ListView.separated(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
            itemCount: rows.length,
            separatorBuilder: (_, __) => const SizedBox(height: 8),
            itemBuilder: (_, i) {
              final a = rows[i];
              return Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
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
                    const Icon(Icons.smart_toy_outlined,
                        size: 18, color: DesignColors.primary),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Text(
                        (a['handle'] ?? a['id'] ?? '?').toString(),
                        style: GoogleFonts.spaceGrotesk(
                            fontSize: 13, fontWeight: FontWeight.w600),
                      ),
                    ),
                    Text((a['status'] ?? '').toString(),
                        style: GoogleFonts.jetBrainsMono(
                            fontSize: 10,
                            color: isDark
                                ? DesignColors.textMuted
                                : DesignColors.textMutedLight)),
                  ],
                ),
              );
            },
          );
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 8, 8),
          child: Row(
            children: [
              const Spacer(),
              IconButton(
                tooltip: 'Archived agents',
                icon: const Icon(Icons.inventory_2_outlined),
                onPressed: () => Navigator.of(context).push(MaterialPageRoute(
                  builder: (_) => const ArchivedAgentsScreen(),
                )),
              ),
            ],
          ),
        ),
        Expanded(child: body),
      ],
    );
  }
}

// ---- Overview ----
// A+B hybrid chassis (IA §6.2 / W4). For goal-kind projects:
//   A) a fixed, domain-agnostic portfolio header (goal, status, budget,
//      steward, attention, task progress + priority breakdown), then
//   B) a pluggable hero region whose widget kind is declared by the
//      bound template (`overview_widget`: task_milestone_list,
//      sweep_compare, recent_artifacts, children_status).
// Below the hero live the shortcut tiles into heavier sub-surfaces
// (Runs / Reviews / Documents / Schedules / Plans / Blobs) and the
// metadata rows / archive action.
// Standing-kind ("Workspace") still renders the pre-W4 layout; W6 will
// redesign that surface.

class _OverviewView extends ConsumerWidget {
  final Map<String, dynamic> project;
  const _OverviewView({required this.project});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final projectId = (project['id'] ?? '').toString();
    final kind = (project['kind'] ?? 'goal').toString();
    final kindLabel = kind == 'standing' ? l10n.kindWorkspace : l10n.kindProject;
    // Count open attention for this project off the already-loaded list.
    final attention = ref.watch(hubProvider).value?.attention ?? const [];
    final openAttention = attention
        .where((a) => (a['project_id'] ?? '').toString() == projectId)
        .length;
    final rows = <MapEntry<String, String>>[
      MapEntry('Name', (project['name'] ?? '').toString()),
      MapEntry('Kind', kindLabel),
      MapEntry('Status', (project['status'] ?? '').toString()),
      if ((project['goal'] ?? '').toString().isNotEmpty)
        MapEntry('Goal', (project['goal'] ?? '').toString()),
      if ((project['template_id'] ?? '').toString().isNotEmpty)
        MapEntry('Steward template', (project['template_id'] ?? '').toString()),
      if ((project['on_create_template_id'] ?? '').toString().isNotEmpty)
        MapEntry('On-create template',
            (project['on_create_template_id'] ?? '').toString()),
      MapEntry('ID', (project['id'] ?? '').toString()),
      MapEntry('Docs root', (project['docs_root'] ?? '').toString()),
      MapEntry('Created', (project['created_at'] ?? '').toString()),
    ];
    final isGoal = kind != 'standing';
    final overviewWidget = (project['overview_widget'] ?? '').toString();
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
      children: [
        if (projectId.isNotEmpty) ...[
          if (openAttention > 0) ...[
            _AttentionBanner(
              count: openAttention,
              onTap: () => Navigator.of(context).push(MaterialPageRoute(
                builder: (_) => ReviewsScreen(projectId: projectId),
              )),
            ),
            const SizedBox(height: 12),
          ],
          if (isGoal) ...[
            // A+B chassis (IA §6.2 / W4): portfolio header is always on,
            // the hero below is whatever the template declared. The old
            // SweepScatter + TaskProgressCounter are subsumed —
            // sweep_compare is the hero for sweep-kind templates, and
            // task progress lives on the portfolio header for everyone.
            PortfolioHeader(ctx: OverviewContext(project: project)),
            const SizedBox(height: 12),
            buildOverviewWidget(
              overviewWidget,
              OverviewContext(project: project),
            ),
            const SizedBox(height: 12),
          ] else ...[
            // Workspace Overview is W6's scope; keep the existing chart
            // + counter intact here so the standing-kind layout is not
            // disturbed by W4.
            SweepScatter(projectId: projectId),
            const SizedBox(height: 12),
            _TaskProgressCounter(projectId: projectId),
            const SizedBox(height: 12),
          ],
          _ShortcutTile(
            icon: Icons.science_outlined,
            label: 'Experiments',
            sub: 'ML training/eval runs (blueprint §6.5)',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => RunsScreen(projectId: projectId),
            )),
          ),
          _ShortcutTile(
            icon: Icons.rate_review_outlined,
            label: 'Reviews',
            sub: kind == 'standing'
                ? 'Pending human decisions on this workspace'
                : 'Pending human decisions on this project',
            trailing: openAttention > 0
                ? _AttentionBadgeSmall(count: openAttention)
                : null,
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => ReviewsScreen(projectId: projectId),
            )),
          ),
          _ShortcutTile(
            icon: Icons.output_outlined,
            label: 'Outputs',
            sub: 'Artifacts runs produce · checkpoints, curves, reports',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => ArtifactsScreen(projectId: projectId),
            )),
          ),
          _ShortcutTile(
            icon: Icons.article_outlined,
            label: 'Documents',
            sub: 'Authored writeups · memos, drafts, reports',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => DocumentsScreen(projectId: projectId),
            )),
          ),
          _ShortcutTile(
            icon: Icons.schedule_outlined,
            label: 'Schedules',
            sub: 'Recurring firings across the team',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => const SchedulesScreen(),
            )),
          ),
          _ShortcutTile(
            icon: Icons.playlist_play_outlined,
            label: 'Plans',
            sub: 'Plan templates the steward executes',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => const PlansScreen(),
            )),
          ),
          _ShortcutTile(
            icon: Icons.perm_media_outlined,
            label: 'Assets',
            sub: 'Browse media from channels · standalone uploads',
            onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => const _BlobsScreen(),
            )),
          ),
          const SizedBox(height: 16),
          const Divider(height: 1),
          const SizedBox(height: 16),
        ],
        for (final r in rows)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(r.key,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.5,
                      color: isDark
                          ? DesignColors.textMuted
                          : DesignColors.textMutedLight,
                    )),
                const SizedBox(height: 2),
                Text(r.value.isEmpty ? '—' : r.value,
                    style: GoogleFonts.spaceGrotesk(fontSize: 13)),
              ],
            ),
          ),
        const SizedBox(height: 16),
        OutlinedButton.icon(
          icon: const Icon(Icons.archive_outlined),
          label: Text(kind == 'standing'
              ? l10n.workspaceArchiveAction
              : l10n.projectArchiveAction),
          style: OutlinedButton.styleFrom(foregroundColor: DesignColors.error),
          onPressed: () => _archive(context, ref),
        ),
      ],
    );
  }

  Future<void> _archive(BuildContext context, WidgetRef ref) async {
    final l10n = AppLocalizations.of(context)!;
    final kind = (project['kind'] ?? 'goal').toString();
    final isWorkspace = kind == 'standing';
    final name = (project['name'] ?? '').toString();
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(isWorkspace
            ? l10n.workspaceArchiveTitle
            : l10n.projectArchiveTitle),
        content: Text(
          isWorkspace
              ? l10n.workspaceArchiveConfirm(name)
              : l10n.projectArchiveConfirm(name),
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Archive'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      await client.archiveProject((project['id'] ?? '').toString());
      await ref.read(hubProvider.notifier).refreshAll();
      if (context.mounted) Navigator.of(context).pop();
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Archive failed: $e')),
        );
      }
    }
  }
}

/// Compact navigation tile surfaced at the top of the Info tab. Routes
/// to a screen scoped to this project via its `projectId` param.
class _ShortcutTile extends StatelessWidget {
  final IconData icon;
  final String label;
  final String sub;
  final Widget? trailing;
  final VoidCallback onTap;
  const _ShortcutTile({
    required this.icon,
    required this.label,
    required this.sub,
    required this.onTap,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Icon(icon, color: DesignColors.primary),
      title: Text(
        label,
        style: GoogleFonts.spaceGrotesk(
          fontSize: 14,
          fontWeight: FontWeight.w700,
        ),
      ),
      subtitle: Text(
        sub,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: DesignColors.textMuted,
        ),
      ),
      trailing: trailing != null
          ? Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                trailing!,
                const SizedBox(width: 4),
                const Icon(Icons.chevron_right, size: 20),
              ],
            )
          : const Icon(Icons.chevron_right, size: 20),
      onTap: onTap,
    );
  }
}

/// Wide banner rendered at the top of Overview when there is pending
/// attention on this project. Taps through to the Reviews queue (the
/// usual source of attention items per blueprint §6.8). A separate small
/// badge on the Reviews shortcut mirrors the count so the affordance
/// stays visible after scrolling past the banner.
class _AttentionBanner extends StatelessWidget {
  final int count;
  final VoidCallback onTap;
  const _AttentionBanner({required this.count, required this.onTap});

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(10),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: DesignColors.warning.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: DesignColors.warning.withValues(alpha: 0.55)),
        ),
        child: Row(
          children: [
            const Icon(Icons.flag_outlined,
                size: 18, color: DesignColors.warning),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                count == 1
                    ? '1 open attention item'
                    : '$count open attention items',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                  color: DesignColors.warning,
                ),
              ),
            ),
            const Icon(Icons.chevron_right,
                size: 18, color: DesignColors.warning),
          ],
        ),
      ),
    );
  }
}

class _AttentionBadgeSmall extends StatelessWidget {
  final int count;
  const _AttentionBadgeSmall({required this.count});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
      decoration: BoxDecoration(
        color: DesignColors.warning.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(9),
        border: Border.all(color: DesignColors.warning.withValues(alpha: 0.6)),
      ),
      child: Text(
        '$count',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: DesignColors.warning,
        ),
      ),
    );
  }
}

class ProjectKindChip extends StatelessWidget {
  final String kind;
  const ProjectKindChip({required this.kind});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final color = switch (kind) {
      'goal' => DesignColors.terminalCyan,
      'standing' => DesignColors.warning,
      _ => DesignColors.textMuted,
    };
    // Schema `kind` stays goal/standing; UI surfaces Project/Workspace
    // per IA §6.2 since the mental models differ.
    final label = switch (kind) {
      'goal' => l10n.kindProject,
      'standing' => l10n.kindWorkspace,
      _ => kind,
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

/// Thin breadcrumb shown on sub-project detail (W5, IA §6.2). Looks up
/// the parent row on the already-loaded `hubProvider.projects` list so
/// tapping it pops to the parent's detail without an extra round-trip.
/// Styled like a standard back-to-parent affordance: chevron-left, parent
/// name, muted but tappable.
class _ParentBreadcrumb extends ConsumerWidget {
  final String parentProjectId;
  const _ParentBreadcrumb({required this.parentProjectId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final all = ref.watch(hubProvider).value?.projects ??
        const <Map<String, dynamic>>[];
    Map<String, dynamic>? parent;
    for (final p in all) {
      if ((p['id'] ?? '').toString() == parentProjectId) {
        parent = p;
        break;
      }
    }
    final parentName = (parent?['name'] ?? 'parent').toString();
    final parentKind = (parent?['kind'] ?? 'goal').toString();
    final kindLabel = parentKind == 'standing' ? 'Workspace' : 'Project';
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Material(
      color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
      child: InkWell(
        onTap: parent == null
            ? null
            : () {
                // Pop to the parent's detail. If we were pushed from the
                // parent, a simple pop is ideal; otherwise push a fresh
                // parent detail so deep links still resolve.
                final nav = Navigator.of(context);
                if (nav.canPop()) {
                  nav.pop();
                } else {
                  nav.pushReplacement(MaterialPageRoute(
                    builder: (_) => ProjectDetailScreen(project: parent!),
                  ));
                }
              },
        child: Padding(
          padding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          child: Row(
            children: [
              const Icon(Icons.chevron_left,
                  size: 16, color: DesignColors.textMuted),
              const SizedBox(width: 4),
              Text(
                kindLabel,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 0.5,
                  color: DesignColors.textMuted,
                ),
              ),
              const SizedBox(width: 6),
              Flexible(
                child: Text(
                  parentName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Placeholder extends StatelessWidget {
  final String text;
  const _Placeholder({required this.text});
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(text,
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            )),
      ),
    );
  }
}

/// Scaffold wrapper for the device-local blob cache, surfaced from the
/// Overview tab's Blobs shortcut. The underlying [BlobsSection] is a
/// body-only widget so we wrap it here to give it an AppBar when opened
/// standalone.
class _BlobsScreen extends StatelessWidget {
  const _BlobsScreen();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Assets',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      body: const BlobsSection(),
    );
  }
}

/// Overview counter showing closed_tasks / total_tasks. Placed above the
/// shortcut tiles so the user can see "I've burned down 3/12" without
/// switching to the Tasks tab. W4 redesigns the Overview chassis; this is
/// intentionally a small card that the redesign can keep or move.
class _TaskProgressCounter extends ConsumerStatefulWidget {
  final String projectId;
  const _TaskProgressCounter({required this.projectId});

  @override
  ConsumerState<_TaskProgressCounter> createState() =>
      _TaskProgressCounterState();
}

class _TaskProgressCounterState extends ConsumerState<_TaskProgressCounter> {
  int _total = 0;
  int _closed = 0;
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final cached = await client.listTasksCached(widget.projectId);
      final rows = cached.body;
      var closed = 0;
      for (final r in rows) {
        if ((r['status'] ?? '').toString() == 'done') closed++;
      }
      if (!mounted) return;
      setState(() {
        _total = rows.length;
        _closed = closed;
        _loaded = true;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loaded = true);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (!_loaded || _total == 0) {
      return const SizedBox.shrink();
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final ratio = _total == 0 ? 0.0 : _closed / _total;
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
      child: Row(
        children: [
          const Icon(Icons.check_circle_outline,
              size: 18, color: DesignColors.primary),
          const SizedBox(width: 8),
          Text(
            'Tasks',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: ratio,
                minHeight: 6,
                backgroundColor: isDark
                    ? DesignColors.borderDark
                    : DesignColors.borderLight,
                valueColor:
                    const AlwaysStoppedAnimation(DesignColors.primary),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Text(
            '$_closed / $_total',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 12,
              fontWeight: FontWeight.w600,
              color: isDark
                  ? DesignColors.textSecondary
                  : DesignColors.textSecondaryLight,
            ),
          ),
        ],
      ),
    );
  }
}

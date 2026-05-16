import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../providers/sessions_provider.dart';
import '../../services/id_format.dart';
import '../sessions/sessions_screen.dart' show SessionChatScreen;
import '../../theme/design_colors.dart';
import '../../theme/task_priority_style.dart';
import '../../widgets/activity_snippet.dart'
    show
        activityIconForAction,
        activityColorForAction,
        activityActionLabel,
        shortRelativeTs;
import '../../widgets/hub_offline_banner.dart';
import '../../widgets/insights_panel.dart';
import '../../widgets/phase_badge.dart';
import '../../widgets/shortcut_tile_strip.dart';
import '../../widgets/team_switcher.dart';
import '../../widgets/spawn_project_steward_sheet.dart';
import '../../widgets/template_yaml_sheet.dart';
import 'phase_summary_screen.dart';
import 'archived_agents_screen.dart';
import 'docs_section.dart';
import 'projects_screen.dart' show openAgentDetail;
import 'overview_widgets/portfolio_header.dart';
import 'overview_widgets/registry.dart';
import 'overview_widgets/workspace_overview.dart';
import 'project_create_sheet.dart';
import 'project_edit_sheet.dart';
import 'project_task_create_sheet.dart';
import 'reviews_screen.dart';
import 'spawn_agent_sheet.dart';
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

  /// Tab to land on when the screen opens. Indexes into the pill order
  /// locked by IA §6.2: 0=Overview, 1=Activity, 2=Agents, 3=Tasks,
  /// 4=Files. Out-of-range values clamp to 0. Used by the URI router
  /// to anchor `termipod://project/<pid>/{activity|agents|tasks|files}`.
  final int initialTab;

  const ProjectDetailScreen({
    super.key,
    required this.project,
    this.initialTab = 0,
  });

  @override
  ConsumerState<ProjectDetailScreen> createState() =>
      _ProjectDetailScreenState();
}

class _ProjectDetailScreenState extends ConsumerState<ProjectDetailScreen> {
  late final PageController _pager;
  late int _index;
  late Map<String, dynamic> _project;

  /// Lifecycle phase fields (W1). Pulled out as locals so the build
  /// method can stay readable; kept in sync from `_project` whenever the
  /// underlying map changes.
  String get _phase => (_project['phase'] ?? '').toString();
  List<String> get _phases => (_project['phases'] as List?)
          ?.map((e) => e.toString())
          .toList() ??
      const [];

  void _openPhaseSummary(String phase) {
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => PhaseSummaryScreen(
          projectId: (_project['id'] ?? '').toString(),
          projectName: (_project['name'] ?? '').toString(),
          phase: phase,
          isCurrent: phase == _phase,
        ),
      ),
    );
  }

  // Pill order locked by IA §6.2 (W2 — Channel demoted to AppBar).
  static const _labels = [
    'Overview',
    'Activity',
    'Agents',
    'Tasks',
    'Files',
  ];

  @override
  void initState() {
    super.initState();
    _project = Map<String, dynamic>.from(widget.project);
    _index = widget.initialTab.clamp(0, _labels.length - 1);
    _pager = PageController(initialPage: _index);
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
            if (_phases.isNotEmpty) ...[
              const SizedBox(width: 8),
              Flexible(
                child: PhaseBadge(
                  phases: _phases,
                  currentPhase: _phase,
                  onTap: _openPhaseSummary,
                  dense: true,
                ),
              ),
            ],
          ],
        ),
        actions: [
          const TeamSwitcher(),
          // Edit + View template YAML moved into the overflow menu so
          // the title row has room for the kind chip + (long) project
          // name on narrow phones. The overflow now collects all the
          // single-project actions — keeps `more_vert` honest about
          // what it offers.
          PopupMenuButton<String>(
            tooltip: 'More actions',
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              switch (v) {
                case 'edit':
                  _edit();
                case 'template_yaml':
                  TemplateYamlSheet.show(
                    context,
                    (_project['template_id'] ?? '').toString(),
                  );
                case 'new_sub':
                  if (atMaxDepth) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                        content: Text(
                          'Max sub-project depth is 2 — this project '
                          'is already nested.',
                        ),
                        duration: Duration(seconds: 3),
                      ),
                    );
                  } else {
                    _createSubProject();
                  }
              }
            },
            itemBuilder: (ctx) {
              final hasTemplate =
                  (_project['template_id'] ?? '').toString().isNotEmpty;
              return [
                PopupMenuItem(
                  value: 'edit',
                  child: Row(
                    children: [
                      const Icon(Icons.edit_outlined, size: 16),
                      const SizedBox(width: 8),
                      Text(isWorkspace
                          ? l10n.workspaceDetailEditTooltip
                          : l10n.projectDetailEditTooltip),
                    ],
                  ),
                ),
                if (hasTemplate)
                  const PopupMenuItem(
                    value: 'template_yaml',
                    child: Row(
                      children: [
                        Icon(Icons.info_outline, size: 16),
                        SizedBox(width: 8),
                        Text('View template YAML'),
                      ],
                    ),
                  ),
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
              ];
            },
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
                _OverviewView(
                  project: _project,
                  onProjectChanged: (updated) {
                    if (!mounted) return;
                    setState(() => _project = updated);
                  },
                ),
                _ActivityView(projectId: projectId),
                _AgentsView(projectId: projectId),
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

// W2 (IA §6.2): Activity is a first-class pill again, this time wired
// to `audit_events` instead of channel feeds. The hub's `project_id`
// query filter pulls both target_kind='project' rows (W1's phase audit
// kinds, project.create/update/archive) and any meta_json carrying this
// project_id (agent.spawn / run.create / document.create / review.* /
// attention.decide / artifact.create / session.*). Channel posts are
// reachable via the Discussion tile (TileSlug.discussion) — add it to
// the current phase via the per-project tile editor (v1.0.484 W6).
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
      final cached = await client.listAuditEventsCached(
        projectId: widget.projectId,
        limit: 100,
      );
      if (!mounted) return;
      setState(() {
        _events = cached.body;
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
    return Column(
      children: [
        HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
        Expanded(
          child: _events.isEmpty
              ? const _Placeholder(text: 'No activity yet')
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView.separated(
                    padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
                    itemCount: _events.length,
                    separatorBuilder: (_, __) => const SizedBox(height: 8),
                    itemBuilder: (_, i) => _ActivityRow(evt: _events[i]),
                  ),
                ),
        ),
      ],
    );
  }
}

class _ActivityRow extends StatelessWidget {
  final Map<String, dynamic> evt;
  const _ActivityRow({required this.evt});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final action = (evt['action'] ?? '').toString();
    final summary = (evt['summary'] ?? '').toString();
    final ts = (evt['ts'] ?? '').toString();
    final actorHandle = (evt['actor_handle'] ?? '').toString();
    final actorKind = (evt['actor_kind'] ?? '').toString();
    final actor = actorHandle.isNotEmpty
        ? '@$actorHandle'
        : (actorKind.isNotEmpty ? actorKind : 'system');
    final icon = activityIconForAction(action);
    final color = activityColorForAction(action);
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
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 1),
            child: Icon(icon, size: 16, color: color),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  summary.isEmpty ? action : summary,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 13,
                    height: 1.35,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 3),
                Text(
                  '$actor · ${activityActionLabel(action)}',
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
          const SizedBox(width: 10),
          Text(
            shortRelativeTs(ts),
            style: GoogleFonts.jetBrainsMono(
              fontSize: 10,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
          ),
        ],
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
    final list = _buildTaskList();
    return Stack(
      children: [
        Positioned.fill(
          child: Column(
            children: [
              _TaskFilterBar(
                statuses: _statusFilters,
                selectedStatus: _statusFilter,
                onStatusChanged: (v) {
                  if (_statusFilter == v) return;
                  setState(() => _statusFilter = v);
                  _load();
                },
                selectedPriority: _priorityFilter,
                onPriorityChanged: (v) {
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

  Widget _buildTaskList() {
    if (_tasks.isEmpty) {
      return _Placeholder(
        text: _statusFilter == null
            ? 'No tasks yet — tap + to create'
            : 'No $_statusFilter tasks.',
      );
    }
    // Status-filtered view: flat list — status is implicit, so no
    // section headers and no per-row status label.
    if (_statusFilter != null) {
      return RefreshIndicator(
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
    }
    // No status filter — group by status (Linear/Asana mobile pattern).
    // Each section header carries the status; per-row label is dropped.
    const order = ['todo', 'in_progress', 'blocked', 'done'];
    final byStatus = <String, List<Map<String, dynamic>>>{};
    for (final t in _tasks) {
      final s = (t['status'] ?? 'todo').toString();
      byStatus.putIfAbsent(s, () => []).add(t);
    }
    final children = <Widget>[];
    for (final st in order) {
      final group = byStatus[st];
      if (group == null || group.isEmpty) continue;
      children.add(_StatusSectionHeader(status: st, count: group.length));
      for (var i = 0; i < group.length; i++) {
        children.add(_TaskTile(
          task: group[i],
          projectId: widget.projectId,
          onChanged: _load,
        ));
        if (i < group.length - 1) {
          children.add(const SizedBox(height: 8));
        }
      }
      children.add(const SizedBox(height: 16));
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
        children: children,
      ),
    );
  }
}

/// Single-row filter bar — status pills scroll horizontally at left,
/// priority filter is a compact icon popup at right. Replaces the
/// earlier two-row layout (status row + priority row) which read as
/// twice the chrome for what users experience as one filter
/// operation. Pattern lifted from Linear / Asana mobile.
class _TaskFilterBar extends StatelessWidget {
  final List<String?> statuses;
  final String? selectedStatus;
  final ValueChanged<String?> onStatusChanged;
  final TaskPriority? selectedPriority;
  final ValueChanged<TaskPriority?> onPriorityChanged;
  const _TaskFilterBar({
    required this.statuses,
    required this.selectedStatus,
    required this.onStatusChanged,
    required this.selectedPriority,
    required this.onPriorityChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 8, 8),
      child: Row(
        children: [
          Expanded(
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: Row(
                children: [
                  for (final s in statuses) ...[
                    _TaskFilterPill(
                      label: s ?? 'all',
                      selected: s == selectedStatus,
                      onTap: () => onStatusChanged(s),
                    ),
                    const SizedBox(width: 6),
                  ],
                ],
              ),
            ),
          ),
          _PriorityFilterButton(
            selected: selectedPriority,
            onChanged: onPriorityChanged,
          ),
        ],
      ),
    );
  }
}

/// PopupMenu-based priority filter. Active priority colors the icon
/// so the user can read the current filter at a glance without
/// opening the menu. Compresses the original 5-pill priority row
/// into a single 20pt icon.
///
/// Wire values: `'any'` plus the four [TaskPriority] wires. We avoid
/// `PopupMenuButton<TaskPriority?>` because Flutter's popup conflates
/// a `null` selection with cancellation — `onSelected` never fires for
/// an item whose `value` is `null`, so "Any priority" was unreachable
/// after a non-null pick (v1.0.508 fix).
class _PriorityFilterButton extends StatelessWidget {
  final TaskPriority? selected;
  final ValueChanged<TaskPriority?> onChanged;
  const _PriorityFilterButton({
    required this.selected,
    required this.onChanged,
  });

  static const String _anyValue = 'any';

  @override
  Widget build(BuildContext context) {
    final tint = selected == null
        ? DesignColors.textMuted
        : taskPriorityColor(selected!);
    return PopupMenuButton<String>(
      tooltip: selected == null
          ? 'Filter by priority'
          : 'Priority: ${selected!.label}',
      onSelected: (v) =>
          onChanged(v == _anyValue ? null : _parsePriorityWire(v)),
      icon: Icon(Icons.filter_list, size: 20, color: tint),
      itemBuilder: (_) => [
        CheckedPopupMenuItem<String>(
          value: _anyValue,
          checked: selected == null,
          child: const Text('Any priority'),
        ),
        for (final p in TaskPriority.values)
          CheckedPopupMenuItem<String>(
            value: p.wire,
            checked: selected == p,
            child: Row(
              children: [
                Container(
                  width: 8,
                  height: 8,
                  decoration: BoxDecoration(
                    color: taskPriorityColor(p),
                    shape: BoxShape.circle,
                  ),
                ),
                const SizedBox(width: 8),
                Text(p.label),
              ],
            ),
          ),
      ],
    );
  }

  static TaskPriority _parsePriorityWire(String wire) {
    for (final p in TaskPriority.values) {
      if (p.wire == wire) return p;
    }
    return TaskPriority.med;
  }
}

/// Section divider rendered between status groups when the status
/// filter is null. The header IS the status — drops the redundant
/// per-row status label from `_TaskTile`.
class _StatusSectionHeader extends StatelessWidget {
  final String status;
  final int count;
  const _StatusSectionHeader({required this.status, required this.count});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 12, 4, 8),
      child: Row(
        children: [
          Text(
            status.toUpperCase().replaceAll('_', ' '),
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.8,
              color: muted,
            ),
          ),
          const SizedBox(width: 8),
          Text(
            '$count',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              color: muted,
            ),
          ),
        ],
      ),
    );
  }
}

class _TaskFilterPill extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _TaskFilterPill({
    required this.label,
    required this.selected,
    required this.onTap,
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
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected ? DesignColors.primary : DesignColors.textMuted,
          ),
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
    final preview = _previewLine((task['body_md'] ?? '').toString());
    final fromPlan = (task['source'] ?? 'ad_hoc').toString() == 'plan';
    final priority = parseTaskPriority(task['priority']);
    // Per-row redesign (v1.0.499 issue 4): drop the leading status
    // dot AND the trailing status text. Status is conveyed by the
    // section header (no-filter view) or the active filter pill
    // (filtered view). The only color cue per row is the priority
    // dot, which is genuinely per-row signal.
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
            Tooltip(
              message: 'Priority: ${priority.label}',
              child: Padding(
                padding: const EdgeInsets.only(top: 4),
                child: TaskPriorityDot(priority: priority),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
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

// ---- Agents (filtered to this project) ----
// Per IA line 444 agents live *inside* project detail, not as a
// sibling tab under Projects. The archive action on the top-right
// replaces the old tab-level _AgentsTab archive button (Gap #6).

class _AgentsView extends ConsumerWidget {
  final String projectId;
  const _AgentsView({required this.projectId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context)!;
    final hubState = ref.watch(hubProvider).value;
    final all = hubState?.agents ?? const [];
    final hosts = hubState?.hosts ?? const [];
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
    // ADR-025 W7: the empty state for a goal project gets a steward
    // CTA. Workspaces (standing projects) follow the same pattern but
    // the framing copy reads differently; we share the CTA either way
    // since `ensureProjectSteward` is project-kind-agnostic.
    // Pull-to-refresh re-fetches the hub fan-out so agent status (and
    // any pending project_steward materializations) update without
    // bouncing out of the project. The empty-state branch wraps the CTA
    // in a ListView so the gesture is reachable even when no agents
    // exist yet — RefreshIndicator needs a scrollable child.
    Future<void> onRefresh() =>
        ref.read(hubProvider.notifier).refreshAll();
    final body = rows.isEmpty
        ? RefreshIndicator(
            onRefresh: onRefresh,
            child: ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                _AgentsEmptyStateCta(
                  projectId: projectId,
                  isWorkspace: isWorkspace,
                  hasHost: hosts.isNotEmpty,
                  placeholderText: isWorkspace
                      ? l10n.workspaceNoAgents
                      : l10n.projectNoAgents,
                ),
              ],
            ),
          )
        : RefreshIndicator(
            onRefresh: onRefresh,
            child: ListView.separated(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 24),
              physics: const AlwaysScrollableScrollPhysics(),
              itemCount: rows.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) {
              final a = rows[i];
              return InkWell(
                onTap: () => openAgentDetail(context, a),
                borderRadius: BorderRadius.circular(8),
                child: Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 12, vertical: 10),
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
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              (a['handle'] ?? a['id'] ?? '?').toString(),
                              style: GoogleFonts.spaceGrotesk(
                                  fontSize: 13, fontWeight: FontWeight.w600),
                            ),
                            GestureDetector(
                              onLongPress: () => copyIdToClipboard(
                                  context, (a['id'] ?? '').toString()),
                              child: Text(
                                formatId(idKindFor('agent'),
                                    (a['id'] ?? '').toString()),
                                style: GoogleFonts.jetBrainsMono(
                                  fontSize: 10,
                                  color: isDark
                                      ? DesignColors.textMuted
                                      : DesignColors.textMutedLight,
                                ),
                              ),
                            ),
                          ],
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
                ),
              );
            },
          ),
        );
    return Stack(
      children: [
        Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 8, 8),
              child: Row(
                children: [
                  const Spacer(),
                  IconButton(
                    tooltip: 'Archived agents',
                    icon: const Icon(Icons.inventory_2_outlined),
                    onPressed: () => Navigator.of(context)
                        .push(MaterialPageRoute(
                      builder: (_) => const ArchivedAgentsScreen(),
                    )),
                  ),
                ],
              ),
            ),
            Expanded(child: body),
          ],
        ),
        if (hosts.isNotEmpty)
          Positioned(
            right: 16,
            bottom: 16,
            // ADR-025 W10: default tap path now drafts an intent
            // message to the project's steward instead of spawning a
            // worker directly. Long-press preserves the legacy
            // direct-spawn flow as the Advanced bypass.
            child: GestureDetector(
              onLongPress: () => showSpawnAgentSheet(
                context,
                hosts: hosts,
                projectId: projectId,
              ),
              child: FloatingActionButton.extended(
                heroTag: 'ask_project_steward_$projectId',
                onPressed: () => _askProjectSteward(
                  context,
                  ref,
                  projectId,
                ),
                icon: const Icon(Icons.forum_outlined),
                label: const Text('Ask steward'),
                tooltip:
                    'Ask the project steward (long-press for Advanced direct spawn)',
              ),
            ),
          ),
      ],
    );
  }
}

// ADR-025 W10 — Spawn-FAB intent rerouting. Resolves the project's
// live steward, finds its session, and opens SessionChatScreen so
// the director can dictate the spawn request as a normal chat turn.
// Falls through to the W7 host-picker sheet when no steward exists
// yet (director must consent to the steward before delegating
// worker spawns to it).
Future<void> _askProjectSteward(
  BuildContext context,
  WidgetRef ref,
  String projectId,
) async {
  final hub = ref.read(hubProvider).value;
  Map<String, dynamic>? stewardAgent;
  for (final a in hub?.agents ?? const <Map<String, dynamic>>[]) {
    if ((a['project_id'] ?? '').toString() != projectId) continue;
    if (!((a['kind'] ?? '').toString().startsWith('steward.'))) continue;
    final status = (a['status'] ?? '').toString();
    if (status == 'terminated' ||
        status == 'crashed' ||
        status == 'failed') {
      continue;
    }
    if ((a['archived_at'] ?? '').toString().isNotEmpty) continue;
    stewardAgent = a;
    break;
  }
  if (stewardAgent == null) {
    // No live steward yet — kick the host-picker sheet so the
    // director can materialize one. Same flow as the empty-state
    // CTA on this tab.
    await showSpawnProjectStewardSheet(context, projectId: projectId);
    return;
  }
  final stewardID = (stewardAgent['id'] ?? '').toString();
  final sessions = ref.read(sessionsProvider).value;
  final allSessions = <Map<String, dynamic>>[
    ...?sessions?.active,
    ...?sessions?.previous,
  ];
  Map<String, dynamic>? stewardSession;
  for (final s in allSessions) {
    if ((s['current_agent_id'] ?? '').toString() == stewardID) {
      stewardSession = s;
      break;
    }
  }
  if (stewardSession == null) {
    // Steward without a session is the multi-steward UX gap that
    // pre-dates auto_open_session. Drop into the agent detail
    // sheet so the operator can resolve manually.
    openAgentDetail(context, stewardAgent);
    return;
  }
  if (!context.mounted) return;
  Navigator.of(context).push(
    MaterialPageRoute(
      builder: (_) => SessionChatScreen(
        sessionId: (stewardSession?['id'] ?? '').toString(),
        agentId: stewardID,
        title: (stewardSession?['title'] ?? 'Project steward').toString(),
      ),
    ),
  );
}

// ADR-025 W7 — empty-state CTA on the project Agents tab. When no
// agent is bound to this project, we surface a "Spawn steward" button
// rather than the bare placeholder text: ADR-025 D1 says every
// engaged project has exactly one steward, materialized lazily on
// first engagement, and this is the principal's consent gate.
class _AgentsEmptyStateCta extends ConsumerWidget {
  final String projectId;
  final bool isWorkspace;
  final bool hasHost;
  final String placeholderText;
  const _AgentsEmptyStateCta({
    required this.projectId,
    required this.isWorkspace,
    required this.hasHost,
    required this.placeholderText,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.smart_toy_outlined,
              size: 36,
              color: isDark
                  ? DesignColors.textMuted
                  : DesignColors.textMutedLight,
            ),
            const SizedBox(height: 8),
            Text(
              placeholderText,
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
            const SizedBox(height: 16),
            FilledButton.icon(
              onPressed: !hasHost
                  ? null
                  : () async {
                      final agentId =
                          await showSpawnProjectStewardSheet(
                        context,
                        projectId: projectId,
                      );
                      if (agentId != null &&
                          agentId.isNotEmpty &&
                          context.mounted) {
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text(
                                'Project steward spawned ($agentId).'),
                          ),
                        );
                      }
                    },
              icon: const Icon(Icons.bolt_outlined),
              label: const Text('Spawn project steward'),
            ),
            if (!hasHost) ...[
              const SizedBox(height: 8),
              Text(
                'Register a host-runner first.',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 11,
                  color: Colors.redAccent,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

// ---- Overview ----
// Two chassis live here, one per kind:
//   - goal ("Project"):   W4 A+B — fixed PortfolioHeader + a pluggable
//                         hero declared by template.overview_widget
//                         (task_milestone_list / recent_artifacts /
//                         children_status / experiment_dash / …).
//   - standing ("Workspace"): W6 — WorkspaceHeader (cadence + last
//                         firing) + RecentFiringsList hero. No task
//                         progress % and no close state, since
//                         workspaces never complete.
// Both branches share the shortcut tiles into heavier sub-surfaces
// (Runs / Reviews / Documents / Schedules / Plans / Blobs) plus the
// metadata rows and archive action.

class _OverviewView extends ConsumerWidget {
  final Map<String, dynamic> project;
  /// Plumbed down to [ShortcutTileStrip] so the Customize sheet can
  /// hand back the freshly-saved project body. Without this hook the
  /// PATCH succeeds but the parent screen keeps its stale `_project`
  /// snapshot and the strip never re-renders.
  final ValueChanged<Map<String, dynamic>>? onProjectChanged;
  const _OverviewView({
    required this.project,
    this.onProjectChanged,
  });

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
        MapEntry('Project template', (project['template_id'] ?? '').toString()),
      if ((project['on_create_template_id'] ?? '').toString().isNotEmpty)
        MapEntry('On-create template',
            (project['on_create_template_id'] ?? '').toString()),
      MapEntry('ID', (project['id'] ?? '').toString()),
      MapEntry('Docs root', (project['docs_root'] ?? '').toString()),
      MapEntry('Created', (project['created_at'] ?? '').toString()),
    ];
    final isGoal = kind != 'standing';
    final overviewWidget = (project['overview_widget'] ?? '').toString();
    return RefreshIndicator(
      onRefresh: () => _refresh(context, ref, projectId),
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
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
            // the hero below is whatever the template declared.
            PortfolioHeader(ctx: OverviewContext(project: project)),
            const SizedBox(height: 12),
            buildOverviewWidget(
              overviewWidget,
              OverviewContext(project: project),
            ),
            const SizedBox(height: 12),
          ] else ...[
            // Workspace chassis (W6): cadence-first header + recent
            // firings list. No task progress %, since workspaces don't
            // close. See overview_widgets/workspace_overview.dart.
            buildWorkspaceOverview(OverviewContext(project: project)),
            const SizedBox(height: 12),
          ],
          // W4 (IA §6.2 / template-yaml-schema §11): replace the prior
          // 7-hard-coded-tile strip with the template-declared,
          // phase-filtered set. Reviews removed — orange attention
          // banner above already serves it (acceptance gap #4).
          ShortcutTileStrip(
            projectId: projectId,
            projectName: (project['name'] ?? '').toString(),
            templateId: (project['template_id'] ?? '').toString(),
            phase: (project['phase'] ?? '').toString(),
            phaseTileOverrides:
                parsePhaseTilesMap(project['phase_tile_overrides']),
            phaseTilesTemplate:
                parsePhaseTilesMap(project['phase_tiles_template']),
            overviewWidgetOverrides:
                parsePhaseStringMap(project['overview_widget_overrides']),
            overviewWidgetTemplate:
                parsePhaseStringMap(project['overview_widget_template']),
            currentOverviewWidget:
                (project['overview_widget'] ?? '').toString(),
            onProjectChanged: onProjectChanged,
          ),
          const SizedBox(height: 16),
          // Insights — Tier-1 metric tiles (ADR-022 D3 / insights-phase-1
          // W2). Renders silently when the project has no event volume,
          // so legacy / lifecycle-disabled projects don't pay UI cost.
          InsightsPanel(scope: InsightsScope.project(projectId)),
          const SizedBox(height: 12),
          const Divider(height: 1),
          const SizedBox(height: 16),
        ],
        Theme(
          data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
          child: ExpansionTile(
            tilePadding: EdgeInsets.zero,
            childrenPadding: const EdgeInsets.only(top: 8, bottom: 4),
            title: Text(
              'Details',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 0.5,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
            children: [
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
              const SizedBox(height: 8),
              OutlinedButton.icon(
                icon: const Icon(Icons.archive_outlined),
                label: Text(kind == 'standing'
                    ? l10n.workspaceArchiveAction
                    : l10n.projectArchiveAction),
                style: OutlinedButton.styleFrom(
                    foregroundColor: DesignColors.error),
                onPressed: () => _archive(context, ref),
              ),
            ],
          ),
        ),
      ],
      ),
    );
  }

  Future<void> _refresh(
    BuildContext context,
    WidgetRef ref,
    String projectId,
  ) async {
    if (projectId.isEmpty) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final fresh = await client.getProject(projectId);
      onProjectChanged?.call(fresh);
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Refresh failed: $e')),
      );
    }
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
                // Always land on the actual parent, regardless of the
                // route the user took to get here. `Navigator.pop` would
                // only be correct when the child was pushed directly from
                // the parent — if the user came in via deep-link, search,
                // or the global Projects list, popping would drop them
                // somewhere unrelated (the IA-1 regression the user
                // reported against v1.0.217).
                Navigator.of(context).pushReplacement(MaterialPageRoute(
                  builder: (_) => ProjectDetailScreen(project: parent!),
                ));
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


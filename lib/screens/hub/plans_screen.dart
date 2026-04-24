import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/entity_names.dart';
import '../../theme/design_colors.dart';
import 'plan_create_sheet.dart';
import 'plan_viewer_screen.dart';

/// Read-only list of team plans (blueprint §6.2, P2.4). Plans are the
/// shallow phase/step scaffolds that agents or schedulers drive — the
/// viewer screen handles the per-plan detail; this screen is just the
/// index. Rows come from `GET /v1/teams/{team}/plans`.
class PlansScreen extends ConsumerStatefulWidget {
  const PlansScreen({super.key});

  @override
  ConsumerState<PlansScreen> createState() => _PlansScreenState();
}

// Filter chips for the status row. `null` means "all statuses". The
// server accepts any subset of the lifecycle values; we don't hard-code
// the full list — uncommon ones still show under "all" so nothing is
// hidden accidentally. `_activeFilter` is a synthetic entry: the hub
// doesn't know about it, so we fetch everything and filter client-side
// to the non-terminal lifecycle states.
const _activeFilter = 'active';
const _activeStatuses = <String>{
  'draft',
  'ready',
  'running',
  'pending',
  'proposed',
};
const _statusFilters = <String?>[
  _activeFilter,
  null,
  'draft',
  'ready',
  'running',
  'completed',
  'failed',
  'cancelled',
];

class _PlansScreenState extends ConsumerState<PlansScreen> {
  List<Map<String, dynamic>>? _rows;
  List<Map<String, dynamic>>? _projects;
  bool _loading = true;
  String? _error;
  // Default to `_activeFilter` so the screen opens on in-flight work
  // rather than a firehose of completed + failed history.
  String? _statusFilter = _activeFilter;
  String? _projectFilter; // null = all

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      // Server-side filter keeps the wire small. Project list is loaded
      // once so the filter sheet can render names instead of bare ids.
      // Synthetic `active` filter is client-side — fetch without a status
      // constraint, then cull to the non-terminal lifecycle set.
      final serverStatus =
          _statusFilter == _activeFilter ? null : _statusFilter;
      final plansFuture = client.listPlansCached(
        projectId: _projectFilter,
        status: serverStatus,
      );
      final projectsFuture =
          _projects == null ? client.listProjectsCached() : null;
      final plansResp = await plansFuture;
      var rows = plansResp.body;
      if (projectsFuture != null) {
        _projects = (await projectsFuture).body;
      }
      if (_statusFilter == _activeFilter) {
        rows = rows
            .where((r) =>
                _activeStatuses.contains((r['status'] ?? '').toString()))
            .toList();
      }
      rows.sort((a, b) => (b['created_at'] ?? '')
          .toString()
          .compareTo((a['created_at'] ?? '').toString()));
      if (!mounted) return;
      setState(() {
        _rows = rows;
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

  Future<void> _pickProject() async {
    final projects = _projects ?? const [];
    final picked = await showModalBottomSheet<_ProjectPick>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => _ProjectFilterSheet(
        projects: projects,
        selectedId: _projectFilter,
      ),
    );
    if (picked == null) return;
    setState(() => _projectFilter = picked.clear ? null : picked.id);
    _load();
  }

  String _projectFilterLabel() {
    final id = _projectFilter;
    if (id == null) return 'All projects';
    for (final p in _projects ?? const <Map<String, dynamic>>[]) {
      if ((p['id'] ?? '').toString() == id) {
        return (p['name'] ?? id).toString();
      }
    }
    return id;
  }

  Future<void> _createPlan() async {
    final plan = await showModalBottomSheet<Map<String, dynamic>>(
      context: context,
      isScrollControlled: true,
      backgroundColor: Colors.transparent,
      builder: (_) => const PlanCreateSheet(),
    );
    if (!mounted || plan == null) return;
    final planId = (plan['id'] ?? '').toString();
    final projectId = (plan['project_id'] ?? '').toString();
    await _load();
    if (!mounted || planId.isEmpty) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => PlanViewerScreen(
        planId: planId,
        projectId: projectId,
      ),
    ));
    if (mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Plans',
          style: GoogleFonts.spaceGrotesk(
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _loading ? null : _load,
          ),
          PopupMenuButton<String>(
            tooltip: 'More',
            icon: const Icon(Icons.more_vert),
            onSelected: (v) {
              if (v == 'new') _createPlan();
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'new',
                child: ListTile(
                  dense: true,
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(Icons.add, size: 20),
                  title: Text('New plan'),
                ),
              ),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          _FilterBar(
            statusFilter: _statusFilter,
            projectLabel: _projectFilterLabel(),
            projectIsActive: _projectFilter != null,
            onStatusSelected: (s) {
              if (_statusFilter == s) return;
              setState(() => _statusFilter = s);
              _load();
            },
            onProjectTap: _pickProject,
          ),
          Expanded(child: _body()),
        ],
      ),
    );
  }

  Widget _body() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Padding(
        padding: const EdgeInsets.all(24),
        child: Text(_error!,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 12, color: DesignColors.error)),
      );
    }
    final rows = _rows ?? const [];
    if (rows.isEmpty) {
      final filtered = (_statusFilter != null || _projectFilter != null);
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            filtered
                ? 'No plans match the current filters.'
                : 'No plans yet — plans appear here once a steward or '
                    'template emits a phased scaffold.',
            textAlign: TextAlign.center,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 13, color: DesignColors.textMuted),
          ),
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: rows.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (ctx, i) {
          final p = rows[i];
          final projects = _projects ??
              ref.watch(hubProvider).value?.projects ??
              const [];
          return _PlanRow(plan: p, projects: projects);
        },
      ),
    );
  }
}

class _PlanRow extends StatelessWidget {
  final Map<String, dynamic> plan;
  final List<Map<String, dynamic>> projects;
  const _PlanRow({required this.plan, required this.projects});

  @override
  Widget build(BuildContext context) {
    final id = (plan['id'] ?? '').toString();
    final projectId = (plan['project_id'] ?? '').toString();
    final projectName = projectId.isEmpty
        ? '(no project)'
        : projectNameFor(projectId, projects);
    final version = (plan['version'] ?? 1).toString();
    final status = (plan['status'] ?? '').toString();
    final template = (plan['template_id'] ?? '').toString();
    final created = (plan['created_at'] ?? '').toString();
    return ListTile(
      title: Row(
        children: [
          PlanStatusChip(status: status),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              projectName,
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.spaceGrotesk(
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          Text(
            'v$version',
            style: GoogleFonts.jetBrainsMono(
              fontSize: 11,
              color: DesignColors.textMuted,
            ),
          ),
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 2),
        child: Text(
          [
            if (template.isNotEmpty) template,
            if (created.isNotEmpty) created,
          ].join(' · '),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
        ),
      ),
      trailing: const Icon(Icons.chevron_right, size: 20),
      onTap: () => Navigator.of(context).push(MaterialPageRoute(
        builder: (_) => PlanViewerScreen(
          planId: id,
          projectId: projectId,
        ),
      )),
    );
  }
}

/// Colored status pill shared between the list and viewer screens.
/// Status values come from blueprint §6.2 plan lifecycle.
class PlanStatusChip extends StatelessWidget {
  final String status;
  const PlanStatusChip({super.key, required this.status});

  @override
  Widget build(BuildContext context) {
    final s = status.toLowerCase();
    final color = switch (s) {
      'running' => DesignColors.terminalBlue,
      'done' || 'completed' || 'succeeded' => DesignColors.success,
      'failed' || 'error' => DesignColors.error,
      'paused' || 'blocked' => DesignColors.warning,
      'cancelled' || 'skipped' => DesignColors.textMuted,
      'ready' => DesignColors.terminalCyan,
      'draft' || 'proposed' || 'pending' => DesignColors.textMuted,
      _ => DesignColors.textMuted,
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        s.isEmpty ? '?' : s,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

class _ProjectPick {
  final String? id;
  final bool clear;
  const _ProjectPick({this.id, this.clear = false});
}

class _FilterBar extends StatelessWidget {
  final String? statusFilter;
  final String projectLabel;
  final bool projectIsActive;
  final ValueChanged<String?> onStatusSelected;
  final VoidCallback onProjectTap;
  const _FilterBar({
    required this.statusFilter,
    required this.projectLabel,
    required this.projectIsActive,
    required this.onStatusSelected,
    required this.onProjectTap,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: border)),
      ),
      padding: const EdgeInsets.fromLTRB(8, 6, 8, 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Row(
              children: [
                for (final s in _statusFilters) ...[
                  _StatusChip(
                    label: s == null ? 'all' : s,
                    selected: statusFilter == s,
                    onTap: () => onStatusSelected(s),
                  ),
                  const SizedBox(width: 6),
                ],
              ],
            ),
          ),
          const SizedBox(height: 6),
          InkWell(
            onTap: onProjectTap,
            borderRadius: BorderRadius.circular(6),
            child: Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
              decoration: BoxDecoration(
                border: Border.all(
                  color: projectIsActive
                      ? DesignColors.primary
                      : border,
                ),
                borderRadius: BorderRadius.circular(6),
              ),
              child: Row(
                children: [
                  Icon(
                    projectIsActive
                        ? Icons.filter_alt
                        : Icons.filter_alt_outlined,
                    size: 14,
                    color: projectIsActive
                        ? DesignColors.primary
                        : DesignColors.textMuted,
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      projectLabel,
                      overflow: TextOverflow.ellipsis,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                        color: projectIsActive
                            ? DesignColors.primary
                            : DesignColors.textMuted,
                      ),
                    ),
                  ),
                  const Icon(Icons.arrow_drop_down,
                      size: 16, color: DesignColors.textMuted),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  final String label;
  final bool selected;
  final VoidCallback onTap;
  const _StatusChip({
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
            color: selected
                ? DesignColors.primary
                : DesignColors.borderDark,
          ),
        ),
        child: Text(
          label,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
            color: selected
                ? DesignColors.primary
                : DesignColors.textMuted,
          ),
        ),
      ),
    );
  }
}

class _ProjectFilterSheet extends StatelessWidget {
  final List<Map<String, dynamic>> projects;
  final String? selectedId;
  const _ProjectFilterSheet({
    required this.projects,
    required this.selectedId,
  });

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (_, scroll) => Container(
        decoration: const BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
        ),
        padding: const EdgeInsets.fromLTRB(8, 12, 8, 16),
        child: ListView.separated(
          controller: scroll,
          itemCount: projects.length + 1,
          separatorBuilder: (_, __) => const Divider(height: 1),
          itemBuilder: (_, i) {
            if (i == 0) {
              return ListTile(
                leading: const Icon(Icons.clear, size: 18),
                title: Text(
                  'All projects',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                selected: selectedId == null,
                onTap: () => Navigator.of(context)
                    .pop(const _ProjectPick(clear: true)),
              );
            }
            final p = projects[i - 1];
            final id = (p['id'] ?? '').toString();
            final name = (p['name'] ?? id).toString();
            final kind = (p['kind'] ?? '').toString();
            return ListTile(
              title: Text(
                name,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                [if (kind.isNotEmpty) kind, id].join(' · '),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10,
                  color: DesignColors.textMuted,
                ),
              ),
              selected: selectedId == id,
              onTap: () =>
                  Navigator.of(context).pop(_ProjectPick(id: id)),
            );
          },
        ),
      ),
    );
  }
}

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../l10n/app_localizations.dart';
import '../providers/hub_provider.dart';
import '../providers/insights_provider.dart';
import '../screens/insights/insights_screen.dart';
import '../services/id_format.dart';
import '../theme/design_colors.dart';
import '../theme/tokens.dart';
import 'activity_digest_card.dart';
import 'hub_offline_banner.dart';

/// Shared activity feed — the team's `audit_events` mutation trail, rendered
/// chronologically with data-driven filters (action prefix · actor ·
/// project), free-text search, and tappable rows that open a detail sheet.
///
/// One widget, two hosts: the team-wide Activity tab (AuditScreen) embeds
/// it with no [projectId]; the project detail Activity tab passes the
/// project id so the feed loads only that project's events. When scoped to a
/// project the project filter axis collapses on its own (one project =
/// nothing to pick), leaving action-prefix + actor.
///
/// It carries its own compact toolbar (search · clear · insights · refresh)
/// so it drops cleanly into a `TabBarView` that has no AppBar of its own.
class ActivityFeed extends ConsumerStatefulWidget {
  /// When non-null, load only this project's audit events (server-side
  /// `project_id` filter) and scope the Insights pivot to the project.
  final String? projectId;
  const ActivityFeed({super.key, this.projectId});

  @override
  ConsumerState<ActivityFeed> createState() => _ActivityFeedState();
}

class _ActivityFeedState extends ConsumerState<ActivityFeed> {
  // Filters derived from loaded rows; null == "no filter on this axis".
  // Chip sets are data-driven so they reflect what's actually in the feed
  // rather than a hardcoded enumeration that drifts as new action kinds land.
  String? _prefix;
  String? _actor;
  String? _projectId;
  String _query = '';
  bool _searchVisible = false;
  final _searchCtrl = TextEditingController();
  List<Map<String, dynamic>> _allRows = const [];
  bool _loading = false;
  String? _error;
  DateTime? _staleSince;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _searchCtrl.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    // HubNotifier.build() is async (reads prefs + opens caches). Await it
    // before reading .client or a cold-start screen sees null and falsely
    // reports "Hub not configured".
    await ref.read(hubProvider.future);
    if (!mounted) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'Hub not configured.';
      });
      return;
    }
    try {
      final cached = await client.listAuditEventsCached(
        projectId: widget.projectId,
        limit: 500,
      );
      if (mounted) {
        setState(() {
          _allRows = cached.body;
          _staleSince = cached.staleSince;
          _loading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _loading = false;
          _error = '$e';
        });
      }
    }
  }

  /// Group actions by their `<verb>` prefix (`agent.spawn` → `agent`) and
  /// count. Sorted by count desc; ties broken alphabetically.
  List<MapEntry<String, int>> get _prefixCounts =>
      _countBy(_allRows, (r) {
        final action = (r['action'] ?? '').toString();
        if (action.isEmpty) return null;
        final dot = action.indexOf('.');
        return dot > 0 ? action.substring(0, dot) : action;
      });

  /// Distinct actor handles (human + agent). Events without a handle roll
  /// into a `(kind)` bucket so background jobs can still be isolated.
  List<MapEntry<String, int>> get _actorCounts =>
      _countBy(_allRows, (r) {
        final h = (r['actor_handle'] ?? '').toString();
        if (h.isNotEmpty) return h;
        final kind = (r['actor_kind'] ?? '').toString();
        return kind.isEmpty ? '(unknown)' : '($kind)';
      });

  /// Distinct project ids across loaded rows. Suppressed when the feed is
  /// already pinned to one project (nothing to choose between).
  List<MapEntry<String, int>> get _projectCounts =>
      widget.projectId != null
          ? const []
          : _countBy(_allRows, _rowProjectId);

  List<Map<String, dynamic>> get _filteredRows {
    final q = _query.trim().toLowerCase();
    return _allRows.where((r) {
      final action = (r['action'] ?? '').toString();
      if (_prefix != null &&
          !(action.startsWith('$_prefix.') || action == _prefix)) {
        return false;
      }
      if (_actor != null) {
        final h = (r['actor_handle'] ?? '').toString();
        final kind = (r['actor_kind'] ?? '').toString();
        final label =
            h.isNotEmpty ? h : (kind.isEmpty ? '(unknown)' : '($kind)');
        if (label != _actor) return false;
      }
      if (_projectId != null && _rowProjectId(r) != _projectId) return false;
      if (q.isNotEmpty) {
        final hay = '${(r['summary'] ?? '')} ${r['action'] ?? ''} '
                '${r['actor_handle'] ?? ''} ${r['target_id'] ?? ''}'
            .toLowerCase();
        if (!hay.contains(q)) return false;
      }
      return true;
    }).toList();
  }

  /// Map project ids to names so the chip row reads "demo-research" instead
  /// of "prj_abc123".
  Map<String, String> _projectNameMap() {
    final projects = ref.read(hubProvider).value?.projects ?? const [];
    return {
      for (final p in projects)
        if ((p['id'] ?? '').toString().isNotEmpty)
          (p['id'] ?? '').toString(): (p['name'] ?? p['id']).toString(),
    };
  }

  /// Map project ids to their `kind` so row summaries can substitute the
  /// right noun (`project` vs. `workspace`) for standing-kind projects.
  Map<String, String> _projectKindMap() {
    final projects = ref.read(hubProvider).value?.projects ?? const [];
    return {
      for (final p in projects)
        if ((p['id'] ?? '').toString().isNotEmpty)
          (p['id'] ?? '').toString(): (p['kind'] ?? 'goal').toString(),
    };
  }

  bool get _hasActiveFilter =>
      _prefix != null ||
      _actor != null ||
      _projectId != null ||
      _query.trim().isNotEmpty;

  void _clearFilters() {
    setState(() {
      _prefix = null;
      _actor = null;
      _projectId = null;
      _query = '';
      _searchCtrl.clear();
    });
  }

  // Map the current filter state onto an [InsightsScope] and push Insights.
  // A project — either the feed's pinned project or a selected project
  // filter — wins; otherwise the team scope.
  void _openInsights() {
    final cfg = ref.read(hubProvider).value?.config;
    if (cfg == null) return;
    final pinned = widget.projectId;
    final selected = _projectId;
    final String? pid = (pinned != null && pinned.isNotEmpty)
        ? pinned
        : (selected != null && selected.isNotEmpty ? selected : null);
    final InsightsScope scope =
        pid != null ? InsightsScope.project(pid) : InsightsScope.team(cfg.teamId);
    Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => InsightsScreen(scope: scope),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final projectNames = _projectNameMap();
    return Column(
      children: [
        _toolbar(),
        HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
        if (_searchVisible)
          _SearchField(
            l10n: l10n,
            controller: _searchCtrl,
            onChanged: (v) => setState(() => _query = v),
          ),
        ActivityDigestCard(events: _filteredRows),
        _UnifiedFilterChips(
          l10n: l10n,
          prefixes: _prefixCounts,
          actors: _actorCounts,
          projects: _projectCounts,
          projectNames: projectNames,
          totalCount: _allRows.length,
          selectedPrefix: _prefix,
          selectedActor: _actor,
          selectedProjectId: _projectId,
          onPrefix: (v) => setState(() => _prefix = v),
          onActor: (v) => setState(() => _actor = v),
          onProject: (v) => setState(() => _projectId = v),
        ),
        const Divider(height: 1),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _load,
            child: _buildBody(),
          ),
        ),
      ],
    );
  }

  Widget _toolbar() {
    final l10n = AppLocalizations.of(context)!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 2, 4, 0),
      child: Row(
        children: [
          const Spacer(),
          IconButton(
            visualDensity: VisualDensity.compact,
            iconSize: 20,
            tooltip: _searchVisible ? l10n.activityHideSearchTooltip : l10n.activitySearchTooltip,
            icon: Icon(_searchVisible ? Icons.search_off : Icons.search),
            onPressed: () => setState(() {
              _searchVisible = !_searchVisible;
              if (!_searchVisible) {
                _query = '';
                _searchCtrl.clear();
              }
            }),
          ),
          if (_hasActiveFilter)
            IconButton(
              visualDensity: VisualDensity.compact,
              iconSize: 20,
              tooltip: l10n.activityClearFiltersTooltip,
              icon: const Icon(Icons.filter_alt_off),
              onPressed: _clearFilters,
            ),
          IconButton(
            visualDensity: VisualDensity.compact,
            iconSize: 20,
            tooltip: l10n.activityInsightsTooltip,
            icon: const Icon(Icons.insights_outlined),
            onPressed: _openInsights,
          ),
          IconButton(
            visualDensity: VisualDensity.compact,
            iconSize: 20,
            tooltip: l10n.buttonRefresh,
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
    );
  }

  Widget _buildBody() {
    final l10n = AppLocalizations.of(context)!;
    final rows = _filteredRows;
    final kindMap = _projectKindMap();
    if (_loading && _allRows.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Text(
            _error! == 'Hub not configured.'
                ? l10n.activityHubNotConfigured
                : _error!,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        ],
      );
    }
    if (rows.isEmpty) {
      return ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Center(
            child: Text(
              _hasActiveFilter
                  ? l10n.activityNoMatchingEvents
                  : l10n.activityNoActivityYet,
            ),
          ),
        ],
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 4),
      itemCount: rows.length,
      separatorBuilder: (_, __) => const Divider(height: 1),
      itemBuilder: (_, i) => _AuditRow(
        l10n: l10n,
        data: rows[i],
        projectKindMap: kindMap,
      ),
    );
  }
}

/// Pull a project id from an audit row: `target_id` when `target_kind` is
/// 'project', else `meta.project_id` which most downstream mutations carry.
String? _rowProjectId(Map<String, dynamic> r) {
  if ((r['target_kind'] ?? '').toString() == 'project') {
    final id = (r['target_id'] ?? '').toString();
    if (id.isNotEmpty) return id;
  }
  final meta = r['meta'];
  if (meta is Map) {
    final pid = (meta['project_id'] ?? '').toString();
    if (pid.isNotEmpty) return pid;
  }
  return null;
}

List<MapEntry<String, int>> _countBy(
  List<Map<String, dynamic>> rows,
  String? Function(Map<String, dynamic>) key,
) {
  final counts = <String, int>{};
  for (final r in rows) {
    final k = key(r);
    if (k == null || k.isEmpty) continue;
    counts[k] = (counts[k] ?? 0) + 1;
  }
  final entries = counts.entries.toList()
    ..sort((a, b) {
      final byCount = b.value.compareTo(a.value);
      return byCount != 0 ? byCount : a.key.compareTo(b.key);
    });
  return entries;
}

/// Single-row horizontal chip strip folding the filter axes (action prefix,
/// actor, project) into one scrollable list. A chip's axis is read from its
/// visual prefix: bare (`agent`) = action prefix, `@foo` = actor, `#demo` =
/// project. Axis dividers keep the strip structured.
class _UnifiedFilterChips extends StatelessWidget {
  final AppLocalizations l10n;
  final List<MapEntry<String, int>> prefixes;
  final List<MapEntry<String, int>> actors;
  final List<MapEntry<String, int>> projects;
  final Map<String, String> projectNames;
  final int totalCount;
  final String? selectedPrefix;
  final String? selectedActor;
  final String? selectedProjectId;
  final ValueChanged<String?> onPrefix;
  final ValueChanged<String?> onActor;
  final ValueChanged<String?> onProject;

  const _UnifiedFilterChips({
    required this.l10n,
    required this.prefixes,
    required this.actors,
    required this.projects,
    required this.projectNames,
    required this.totalCount,
    required this.selectedPrefix,
    required this.selectedActor,
    required this.selectedProjectId,
    required this.onPrefix,
    required this.onActor,
    required this.onProject,
  });

  @override
  Widget build(BuildContext context) {
    final showActors = actors.length >= 2;
    final showProjects = projects.length >= 2;
    final children = <Widget>[
      ChoiceChip(
        label: Text(l10n.activityAllChip(totalCount)),
        selected: selectedPrefix == null &&
            selectedActor == null &&
            selectedProjectId == null,
        onSelected: (_) {
          onPrefix(null);
          onActor(null);
          onProject(null);
        },
      ),
      for (final entry in prefixes)
        ChoiceChip(
          label: Text('${entry.key} (${entry.value})'),
          selected: selectedPrefix == entry.key,
          onSelected: (_) =>
              onPrefix(selectedPrefix == entry.key ? null : entry.key),
        ),
      if (showActors) const _AxisDivider(),
      if (showActors)
        for (final entry in actors)
          ChoiceChip(
            label: Text('@${entry.key} (${entry.value})'),
            selected: selectedActor == entry.key,
            onSelected: (_) =>
                onActor(selectedActor == entry.key ? null : entry.key),
          ),
      if (showProjects) const _AxisDivider(),
      if (showProjects)
        for (final entry in projects)
          ChoiceChip(
            label: Text(
                '#${projectNames[entry.key] ?? entry.key} (${entry.value})'),
            selected: selectedProjectId == entry.key,
            onSelected: (_) => onProject(
                selectedProjectId == entry.key ? null : entry.key),
          ),
    ];
    return SizedBox(
      height: 44,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: Spacing.s8),
        itemCount: children.length,
        separatorBuilder: (_, __) => const SizedBox(width: 6),
        itemBuilder: (_, i) => children[i],
      ),
    );
  }
}

class _AxisDivider extends StatelessWidget {
  const _AxisDivider();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 2, vertical: Spacing.s8),
      child: VerticalDivider(
        width: 12,
        thickness: 1,
        color: (isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight)
            .withValues(alpha: 0.3),
      ),
    );
  }
}

/// Inline search field shown above the digest when the toolbar search toggle
/// is on. Stays inline (not a sheet) so it composes with the chip rows —
/// text, prefix, actor and project filters all AND together.
class _SearchField extends StatelessWidget {
  final AppLocalizations l10n;
  final TextEditingController controller;
  final ValueChanged<String> onChanged;
  const _SearchField({required this.l10n, required this.controller, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
      child: TextField(
        controller: controller,
        onChanged: onChanged,
        autofocus: true,
        style: const TextStyle(fontSize: 13),
        decoration: InputDecoration(
          hintText: l10n.activitySearchHint,
          prefixIcon: const Icon(Icons.search, size: 18),
          isDense: true,
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: Spacing.s8),
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
          ),
        ),
      ),
    );
  }
}

class _AuditRow extends StatelessWidget {
  final AppLocalizations l10n;
  final Map<String, dynamic> data;
  final Map<String, String> projectKindMap;
  const _AuditRow({required this.l10n, required this.data, this.projectKindMap = const {}});

  @override
  Widget build(BuildContext context) {
    final action = (data['action'] ?? '').toString();
    final summary = _rewriteNoun((data['summary'] ?? '').toString());
    final ts = (data['ts'] ?? '').toString();
    final actorHandle = (data['actor_handle'] ?? '').toString();
    final actorKind = (data['actor_kind'] ?? '').toString();
    final icon = _iconForAction(action);
    final color = _colorForAction(context, action);
    final actorLabel = actorHandle.isNotEmpty
        ? '@$actorHandle'
        : (actorKind.isNotEmpty ? actorKind : l10n.activitySystemActor);

    return ListTile(
      leading: Icon(icon, color: color),
      title: Text(
        summary,
        style: const TextStyle(fontWeight: FontWeight.w600),
      ),
      subtitle: Text(
        '$actorLabel  ·  $action',
        style: TextStyle(
          color: Theme.of(context).colorScheme.onSurfaceVariant,
          fontSize: 12,
        ),
      ),
      trailing: Text(
        _shortTime(ts),
        style: TextStyle(
          fontFamily: 'HackGenConsole',
          fontSize: 11,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
      onTap: () => _showDetail(context),
    );
  }

  void _showDetail(BuildContext context) {
    final l10n = this.l10n;
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (_) => DraggableScrollableSheet(
        expand: false,
        initialChildSize: 0.55,
        minChildSize: 0.3,
        maxChildSize: 0.9,
        builder: (_, controller) => SingleChildScrollView(
          controller: controller,
          padding: const EdgeInsets.all(16),
          child: _DetailView(l10n: l10n, data: data),
        ),
      ),
    );
  }

  /// Swap "project" → "workspace" in a pre-formatted summary when the
  /// subject is a standing-kind project. Falls back to the original
  /// whenever the kind is unknown — purely vocabulary-level.
  String _rewriteNoun(String summary) {
    if (summary.isEmpty) return summary;
    final pid = _rowProjectId(data);
    if (pid == null) return summary;
    final kind = projectKindMap[pid];
    if (kind != 'standing') return summary;
    return summary
        .replaceAll('Project', 'Workspace')
        .replaceAll('project', 'workspace');
  }

  static IconData _iconForAction(String action) {
    if (action.startsWith('agent.spawn')) return Icons.rocket_launch_outlined;
    if (action.startsWith('agent.terminate')) return Icons.power_settings_new;
    if (action.startsWith('attention.')) return Icons.flag_outlined;
    if (action.startsWith('schedule.')) return Icons.schedule;
    if (action.startsWith('host.')) return Icons.dns_outlined;
    return Icons.history;
  }

  static Color _colorForAction(BuildContext context, String action) {
    final scheme = Theme.of(context).colorScheme;
    if (action == 'agent.terminate' ||
        action == 'schedule.delete' ||
        action == 'host.delete') {
      return scheme.error;
    }
    if (action == 'agent.spawn' || action == 'schedule.create') {
      return DesignColors.primary;
    }
    return scheme.onSurfaceVariant;
  }

  static String _shortTime(String ts) {
    // Accepts "2026-04-21T10:33:33.123Z"; show "MM-DD HH:MM".
    if (ts.length < 16) return ts;
    return '${ts.substring(5, 10)} ${ts.substring(11, 16)}';
  }
}

class _DetailView extends StatelessWidget {
  final AppLocalizations l10n;
  final Map<String, dynamic> data;
  const _DetailView({required this.l10n, required this.data});

  @override
  Widget build(BuildContext context) {
    final meta = data['meta'];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          (data['summary'] ?? '').toString(),
          style: GoogleFonts.spaceGrotesk(
              fontSize: 16, fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 12),
        _kv(context, l10n.activityDetailAction, (data['action'] ?? '').toString()),
        _kv(context, l10n.activityDetailActor, [
          (data['actor_handle'] ?? '').toString(),
          (data['actor_kind'] ?? '').toString(),
        ].where((s) => s.isNotEmpty).join('  ·  ')),
        _TargetKv(
          l10n: l10n,
          targetKind: (data['target_kind'] ?? '').toString(),
          targetId: (data['target_id'] ?? '').toString(),
        ),
        _kv(context, l10n.activityDetailTime, (data['ts'] ?? '').toString()),
        if (meta is Map && meta.isNotEmpty) ...[
          const SizedBox(height: 12),
          Text(
            l10n.activityDetailMetadata,
            style: GoogleFonts.spaceGrotesk(
                fontSize: 13, fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 4),
          for (final e in meta.entries)
            _kv(context, e.key.toString(), '${e.value}'),
        ],
      ],
    );
  }

  Widget _kv(BuildContext context, String k, String v) {
    if (v.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              k,
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              v,
              style: const TextStyle(fontSize: 13),
            ),
          ),
        ],
      ),
    );
  }
}

/// Target row in the detail panel — the kind plus a type-prefixed short form
/// of the id, long-press to copy the full ULID.
class _TargetKv extends StatelessWidget {
  final AppLocalizations l10n;
  final String targetKind;
  final String targetId;
  const _TargetKv({required this.l10n, required this.targetKind, required this.targetId});

  @override
  Widget build(BuildContext context) {
    if (targetKind.isEmpty && targetId.isEmpty) {
      return const SizedBox.shrink();
    }
    final short =
        targetId.isEmpty ? '' : formatId(idKindFor(targetKind), targetId);
    final muted = Theme.of(context).colorScheme.onSurfaceVariant;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(
              l10n.activityDetailTarget,
              style: TextStyle(fontSize: 12, color: muted),
            ),
          ),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    if (targetKind.isNotEmpty) ...[
                      Text(targetKind, style: const TextStyle(fontSize: 13)),
                      const SizedBox(width: 8),
                    ],
                    if (short.isNotEmpty)
                      GestureDetector(
                        onLongPress: () =>
                            copyIdToClipboard(context, targetId),
                        child: Text(
                          short,
                          style: TextStyle(
                            fontSize: 12,
                            color: muted,
                            fontFamily: 'monospace',
                          ),
                        ),
                      ),
                  ],
                ),
                if (targetId.isNotEmpty)
                  SelectableText(
                    targetId,
                    style: TextStyle(
                      fontSize: 11,
                      color: muted,
                      fontFamily: 'monospace',
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

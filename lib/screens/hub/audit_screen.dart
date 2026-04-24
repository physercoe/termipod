import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../widgets/activity_digest_card.dart';
import '../../widgets/hub_offline_banner.dart';
import '../../widgets/team_switcher.dart';

/// Activity tab body per `docs/ia-redesign.md` §6.3 — the team's mutation
/// feed backed by `audit_events`. Chronological, filterable; a digest card
/// at the top summarises the last 24h and is mirrored on the Me tab.
/// Rows come from `GET /v1/teams/{team}/audit` newest-first; the server
/// caps the response at 500 rows.
class AuditScreen extends ConsumerStatefulWidget {
  const AuditScreen({super.key});

  @override
  ConsumerState<AuditScreen> createState() => _AuditScreenState();
}

class _AuditScreenState extends ConsumerState<AuditScreen> {
  // Filters derived from loaded rows; null == "no filter on this axis".
  // Chip sets are data-driven (see `_prefixCounts` / `_actorCounts` /
  // `_projectCounts`) so they reflect what's actually in the feed rather
  // than a hardcoded enumeration that drifts as new action kinds land on
  // the backend.
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
    // HubNotifier.build() is async (reads prefs + opens caches). In initState
    // we must await it before reading .client, or a cold-start screen sees
    // null and falsely reports "Hub not configured".
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
      final cached = await client.listAuditEventsCached(limit: 500);
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

  /// Distinct actor handles (human + agent) across loaded rows. Events
  /// without a handle are rolled into '(system)' so you can still isolate
  /// background jobs.
  List<MapEntry<String, int>> get _actorCounts =>
      _countBy(_allRows, (r) {
        final h = (r['actor_handle'] ?? '').toString();
        if (h.isNotEmpty) return h;
        final kind = (r['actor_kind'] ?? '').toString();
        return kind.isEmpty ? '(unknown)' : '($kind)';
      });

  /// Pull a project id from each row. Uses `target_id` when `target_kind`
  /// is 'project', otherwise falls back to `meta.project_id` which most
  /// downstream mutations (run.create, plan.create, channel.create…)
  /// populate.
  List<MapEntry<String, int>> get _projectCounts =>
      _countBy(_allRows, _rowProjectId);

  static String? _rowProjectId(Map<String, dynamic> r) {
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

  static List<MapEntry<String, int>> _countBy(
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

  /// Map project ids to their names using the already-loaded projects list
  /// so the chip row reads "demo-research" instead of "proj_abc123".
  Map<String, String> _projectNameMap() {
    final projects = ref.read(hubProvider).value?.projects ?? const [];
    return {
      for (final p in projects)
        if ((p['id'] ?? '').toString().isNotEmpty)
          (p['id'] ?? '').toString(): (p['name'] ?? p['id']).toString(),
    };
  }

  /// Map project ids to their `kind` so row summaries can substitute the
  /// right noun (`project` vs. `workspace`) when the event subject is a
  /// standing-kind project per blueprint §6.1 + IA §6.2.
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

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final projectNames = _projectNameMap();
    final actorCounts = _actorCounts;
    final projectCounts = _projectCounts;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.tabActivity,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          const TeamSwitcher(),
          IconButton(
            tooltip: _searchVisible ? 'Hide search' : 'Search',
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
              tooltip: 'Clear filters',
              icon: const Icon(Icons.filter_alt_off),
              onPressed: _clearFilters,
            ),
          IconButton(
            tooltip: 'Refresh',
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: Column(
        children: [
          HubOfflineBanner(staleSince: _staleSince, onRetry: _load),
          if (_searchVisible) _SearchField(
            controller: _searchCtrl,
            onChanged: (v) => setState(() => _query = v),
          ),
          ActivityDigestCard(events: _filteredRows),
          _UnifiedFilterChips(
            prefixes: _prefixCounts,
            actors: actorCounts,
            projects: projectCounts,
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
      ),
    );
  }

  Widget _buildBody() {
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
            _error!,
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
                  ? 'No events match the current filters.'
                  : 'No audit events yet.',
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
        data: rows[i],
        projectKindMap: kindMap,
      ),
    );
  }
}

/// Single-row horizontal chip strip that folds the three filter axes
/// (action prefix, actor, project) into one scrollable list. A chip's
/// axis is read from its visual prefix: bare (`agent`) is an action
/// prefix, `@foo` is an actor, `#demo` is a project. An axis-internal
/// divider separates the groups so the strip still reads as structured.
class _UnifiedFilterChips extends StatelessWidget {
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
        label: Text('All ($totalCount)'),
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
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
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
      padding: const EdgeInsets.symmetric(horizontal: 2, vertical: 6),
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

/// Inline search field shown above the digest card when the AppBar search
/// toggle is on. We stay inline rather than pushing to a sheet so the
/// field can live alongside the chip rows — all three axes (text, kind,
/// actor, project) compose with AND semantics.
class _SearchField extends StatelessWidget {
  final TextEditingController controller;
  final ValueChanged<String> onChanged;
  const _SearchField({required this.controller, required this.onChanged});

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
          hintText: 'Search summary / action / target…',
          prefixIcon: const Icon(Icons.search, size: 18),
          isDense: true,
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
          ),
        ),
      ),
    );
  }
}


class _AuditRow extends StatelessWidget {
  final Map<String, dynamic> data;
  final Map<String, String> projectKindMap;
  const _AuditRow({required this.data, this.projectKindMap = const {}});

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
        : (actorKind.isNotEmpty ? actorKind : 'system');

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
          child: _DetailView(data: data),
        ),
      ),
    );
  }

  /// Swap the noun "project"/"Project" for "workspace"/"Workspace" in a
  /// row's pre-formatted summary when the subject is a standing-kind
  /// project. Falls back to the original string whenever the kind is
  /// unknown (e.g. events predating the map, or rows with no project
  /// binding) — blueprint §6.1 treats the schema as unchanged, so this
  /// is purely vocabulary-level.
  String _rewriteNoun(String summary) {
    if (summary.isEmpty) return summary;
    final pid = _AuditScreenState._rowProjectId(data);
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
  final Map<String, dynamic> data;
  const _DetailView({required this.data});

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
        _kv(context, 'Action', (data['action'] ?? '').toString()),
        _kv(context, 'Actor', [
          (data['actor_handle'] ?? '').toString(),
          (data['actor_kind'] ?? '').toString(),
        ].where((s) => s.isNotEmpty).join('  ·  ')),
        _kv(context, 'Target', [
          (data['target_kind'] ?? '').toString(),
          (data['target_id'] ?? '').toString(),
        ].where((s) => s.isNotEmpty).join(' ')),
        _kv(context, 'Time', (data['ts'] ?? '').toString()),
        if (meta is Map && meta.isNotEmpty) ...[
          const SizedBox(height: 12),
          Text(
            'Metadata',
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

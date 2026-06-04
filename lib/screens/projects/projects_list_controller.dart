import '../../providers/project_filter_provider.dart';

/// Orchestration seam for the Projects list (WS2 of
/// docs/plans/internal-techdebt-cleanup.md). The list shaping — parsing the
/// per-project insight rollup, filtering/sorting by the AppBar filter,
/// partitioning goals vs workspaces, and flattening sub-projects under their
/// parent — is pure over the hub's `Map<String, dynamic>` rows, so it lives
/// here as plain functions/classes that can be unit-tested without a widget
/// harness. The `_ProjectsTab` widget keeps view composition only.

/// Per-project Insights row condensed for the projects list. Sourced
/// from `/v1/insights?team_id=X`'s `by_project[]`. The projects list
/// pulls this in parallel with the hub project list so each row can
/// render the 3-line card without a per-project round-trip.
class ProjectInsight {
  final String currentPhase;

  /// 1-based position of `currentPhase` inside the project's template
  /// phase list (0 when unknown). Plus the total phase count. Drives
  /// the `N/M` suffix on the project-list phase pill (v1.0.513).
  final int phaseIndex;
  final int phasesTotal;
  final double progress;
  final int openCriteria;
  final int openAttention;
  final String lastActivity;
  const ProjectInsight({
    required this.currentPhase,
    required this.phaseIndex,
    required this.phasesTotal,
    required this.progress,
    required this.openCriteria,
    required this.openAttention,
    required this.lastActivity,
  });
}

/// Fold the insights `by_project[]` list (as the hub returns it) into a
/// project_id-keyed [ProjectInsight] map. Tolerates a null/non-list input
/// (provider loading/errored) by returning an empty map, so the projects
/// list falls through to the 2-line tile without a freeze on first paint.
Map<String, ProjectInsight> foldProjectInsights(Object? byProjectRaw) {
  if (byProjectRaw is! List) return const {};
  final out = <String, ProjectInsight>{};
  for (final e in byProjectRaw) {
    if (e is! Map) continue;
    final m = e.cast<String, dynamic>();
    final id = (m['project_id'] ?? '').toString();
    if (id.isEmpty) continue;
    out[id] = ProjectInsight(
      currentPhase: (m['current_phase'] ?? '').toString(),
      phaseIndex: _asInt(m['phase_index']),
      phasesTotal: _asInt(m['phases_total']),
      progress: _asDouble(m['progress']),
      openCriteria: _asInt(m['open_criteria']),
      openAttention: _asInt(m['open_attention']),
      lastActivity: (m['last_activity'] ?? '').toString(),
    );
  }
  return out;
}

/// A project row in the flattened list. Depth 0 = top-level row; depth 1 =
/// sub-project row rendered with the indent + left-rail treatment. Max depth
/// is 2 (server-enforced, clamped client-side in [flattenProjectsWithChildren]).
class ProjectNode {
  final Map<String, dynamic> project;
  final int depth;
  final int childCount;
  const ProjectNode({
    required this.project,
    required this.depth,
    required this.childCount,
  });
}

/// Apply the AppBar filter (status / needs-me / sort) to [items]. Pure: it
/// never mutates the caller's list. `needs_me` reads from [openByProject] +
/// each insight's `openCriteria`; the sort reads insight `lastActivity` with
/// a `created_at` fallback for rows Insights hasn't aggregated yet.
List<Map<String, dynamic>> applyProjectFilter(
  List<Map<String, dynamic>> items,
  ProjectListFilter filter,
  Map<String, int> openByProject,
  Map<String, ProjectInsight> byProject,
) {
  var rows = items;
  switch (filter.status) {
    case ProjectStatusFilter.active:
      rows = rows
          .where((p) => (p['status'] ?? '').toString() != 'archived')
          .toList();
    case ProjectStatusFilter.archived:
      rows = rows
          .where((p) => (p['status'] ?? '').toString() == 'archived')
          .toList();
    case ProjectStatusFilter.all:
      // No-op — include both. Local var avoids mutating the caller list.
      rows = [...rows];
  }
  if (filter.needsMeOnly) {
    rows = rows.where((p) {
      final pid = (p['id'] ?? '').toString();
      final att = openByProject[pid] ?? 0;
      final ac = byProject[pid]?.openCriteria ?? 0;
      return att > 0 || ac > 0;
    }).toList();
  }
  switch (filter.sort) {
    case ProjectSortMode.recentActivity:
      rows.sort((a, b) {
        // Prefer insights last_activity; fall back to created_at so
        // workspaces and not-yet-aggregated projects still sort sanely.
        String key(Map<String, dynamic> p) {
          final pid = (p['id'] ?? '').toString();
          final la = byProject[pid]?.lastActivity ?? '';
          if (la.isNotEmpty) return la;
          return (p['created_at'] ?? '').toString();
        }

        return key(b).compareTo(key(a));
      });
    case ProjectSortMode.name:
      rows.sort((a, b) {
        final na = (a['name'] ?? '').toString().toLowerCase();
        final nb = (b['name'] ?? '').toString().toLowerCase();
        return na.compareTo(nb);
      });
    case ProjectSortMode.createdDesc:
      rows.sort((a, b) {
        final ca = (a['created_at'] ?? '').toString();
        final cb = (b['created_at'] ?? '').toString();
        return cb.compareTo(ca);
      });
  }
  return rows;
}

/// Partition projects on `kind` per blueprint §6.1: goal vs. standing
/// (workspace). The schema is one table; the mobile IA splits them into two
/// named sections since the mental models differ (bounded outcome vs. ongoing
/// container). A missing/empty kind defaults to goal.
({List<Map<String, dynamic>> goals, List<Map<String, dynamic>> standings})
    partitionProjectsByKind(List<Map<String, dynamic>> items) {
  final goals = <Map<String, dynamic>>[];
  final standings = <Map<String, dynamic>>[];
  for (final p in items) {
    final kind = (p['kind'] ?? 'goal').toString();
    if (kind == 'standing') {
      standings.add(p);
    } else {
      goals.add(p);
    }
  }
  return (goals: goals, standings: standings);
}

/// Flatten a section's projects with their direct children inlined right under
/// each parent, in the order the list came in. Children whose parent isn't in
/// this section are rendered as orphan parents at depth 0 so archived-parent
/// drift doesn't hide rows (W5 edge case). Depth is capped at 1 client-side.
List<ProjectNode> flattenProjectsWithChildren(
  List<Map<String, dynamic>> rows,
) {
  final byId = <String, Map<String, dynamic>>{};
  for (final p in rows) {
    final id = (p['id'] ?? '').toString();
    if (id.isNotEmpty) byId[id] = p;
  }
  final childrenByParent = <String, List<Map<String, dynamic>>>{};
  final tops = <Map<String, dynamic>>[];
  for (final p in rows) {
    final parent = (p['parent_project_id'] ?? '').toString();
    if (parent.isNotEmpty && byId.containsKey(parent)) {
      childrenByParent.putIfAbsent(parent, () => []).add(p);
    } else {
      tops.add(p);
    }
  }
  final out = <ProjectNode>[];
  for (final parent in tops) {
    final pid = (parent['id'] ?? '').toString();
    final kids = childrenByParent[pid] ?? const <Map<String, dynamic>>[];
    out.add(ProjectNode(
      project: parent,
      depth: 0,
      childCount: kids.length,
    ));
    for (final child in kids) {
      out.add(ProjectNode(project: child, depth: 1, childCount: 0));
    }
  }
  return out;
}

int _asInt(dynamic v) {
  if (v is int) return v;
  if (v is num) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

double _asDouble(dynamic v) {
  if (v is double) return v;
  if (v is num) return v.toDouble();
  if (v is String) return double.tryParse(v) ?? 0;
  return 0;
}

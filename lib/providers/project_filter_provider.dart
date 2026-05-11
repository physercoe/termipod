import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Filter / sort state for the Projects list AppBar affordance.
///
/// Defaults match the frequent case: show active projects only, sort
/// by most-recent activity. Non-default state surfaces a dot on the
/// AppBar icon so the user notices when the list is filtered.
///
/// Persisted in SharedPreferences under `projects_list_filter_v1` so a
/// power-user setup ("only show me what needs me") survives across
/// app restarts.

enum ProjectStatusFilter {
  /// Hide archived; the common case.
  active,
  /// All — both active and archived intermixed.
  all,
  /// Only archived — for digging up old projects.
  archived,
}

enum ProjectSortMode {
  /// Most-recent last_activity (from insights `by_project[]`); falls
  /// back to created_at for projects without insights data.
  recentActivity,
  /// Name A-Z (case-insensitive).
  name,
  /// created_at descending (newest first).
  createdDesc,
}

class ProjectListFilter {
  final ProjectStatusFilter status;
  final bool needsMeOnly;
  final ProjectSortMode sort;

  const ProjectListFilter({
    this.status = ProjectStatusFilter.active,
    this.needsMeOnly = false,
    this.sort = ProjectSortMode.recentActivity,
  });

  static const defaults = ProjectListFilter();

  bool get isDefault =>
      status == defaults.status &&
      needsMeOnly == defaults.needsMeOnly &&
      sort == defaults.sort;

  ProjectListFilter copyWith({
    ProjectStatusFilter? status,
    bool? needsMeOnly,
    ProjectSortMode? sort,
  }) {
    return ProjectListFilter(
      status: status ?? this.status,
      needsMeOnly: needsMeOnly ?? this.needsMeOnly,
      sort: sort ?? this.sort,
    );
  }

  Map<String, dynamic> toJson() => {
        'status': status.name,
        'needs_me_only': needsMeOnly,
        'sort': sort.name,
      };

  static ProjectListFilter fromJson(Map<String, dynamic> m) {
    return ProjectListFilter(
      status: ProjectStatusFilter.values.firstWhere(
        (s) => s.name == m['status'],
        orElse: () => defaults.status,
      ),
      needsMeOnly: m['needs_me_only'] == true,
      sort: ProjectSortMode.values.firstWhere(
        (s) => s.name == m['sort'],
        orElse: () => defaults.sort,
      ),
    );
  }
}

const _prefsKey = 'projects_list_filter_v1';

class ProjectListFilterNotifier extends Notifier<ProjectListFilter> {
  @override
  ProjectListFilter build() {
    _load();
    return ProjectListFilter.defaults;
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_prefsKey);
      if (raw == null) return;
      // Lightweight JSON round-trip — three fields, no nested shape, so
      // we don't pull dart:convert here; instead the notifier stores the
      // raw String back and lets fromJson handle it.
      final decoded = _decode(raw);
      if (decoded != null) state = decoded;
    } catch (_) {
      // Swallow: defaults are fine on prefs failure.
    }
  }

  Future<void> set(ProjectListFilter next) async {
    state = next;
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_prefsKey, _encode(next));
    } catch (_) {
      // Best-effort persistence; UI state already updated.
    }
  }

  Future<void> reset() => set(ProjectListFilter.defaults);
}

/// Hand-rolled tiny encoder so we don't pull dart:convert into the
/// provider just for three fields. Stable order; whitespace-free.
String _encode(ProjectListFilter f) =>
    'status=${f.status.name}|needs_me=${f.needsMeOnly}|sort=${f.sort.name}';

ProjectListFilter? _decode(String raw) {
  final parts = raw.split('|');
  String? status;
  String? needsMe;
  String? sort;
  for (final p in parts) {
    final i = p.indexOf('=');
    if (i <= 0) continue;
    final k = p.substring(0, i);
    final v = p.substring(i + 1);
    if (k == 'status') status = v;
    if (k == 'needs_me') needsMe = v;
    if (k == 'sort') sort = v;
  }
  if (status == null && needsMe == null && sort == null) return null;
  return ProjectListFilter.fromJson({
    'status': status,
    'needs_me_only': needsMe == 'true',
    'sort': sort,
  });
}

final projectFilterProvider =
    NotifierProvider<ProjectListFilterNotifier, ProjectListFilter>(
  ProjectListFilterNotifier.new,
);

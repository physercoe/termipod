import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/project_filter_provider.dart';
import 'package:termipod/screens/projects/projects_list_controller.dart';

// WS2 (docs/plans/internal-techdebt-cleanup.md): the Projects-list shaping is
// extracted into a non-widget seam so it can be unit-tested directly. These
// pin the insight fold, the filter/sort, the goal/standing partition, and the
// sub-project flatten the _ProjectsTab used to do inline.

Map<String, dynamic> proj(
  String id, {
  String name = '',
  String status = 'active',
  String kind = 'goal',
  String created = '',
  String parent = '',
}) =>
    {
      'id': id,
      'name': name,
      'status': status,
      'kind': kind,
      'created_at': created,
      'parent_project_id': parent,
    };

ProjectInsight insight({int openCriteria = 0, String lastActivity = ''}) =>
    ProjectInsight(
      currentPhase: '',
      phaseIndex: 0,
      phasesTotal: 0,
      progress: 0,
      openCriteria: openCriteria,
      openAttention: 0,
      lastActivity: lastActivity,
    );

void main() {
  group('foldProjectInsights', () {
    test('non-list input → empty map', () {
      expect(foldProjectInsights(null), isEmpty);
      expect(foldProjectInsights('nope'), isEmpty);
    });

    test('keys by project_id, skips rows without an id, parses numbers', () {
      final folded = foldProjectInsights([
        {
          'project_id': 'p1',
          'current_phase': 'build',
          'phase_index': 2,
          'phases_total': 5,
          'progress': 0.4,
          'open_criteria': 3,
          'open_attention': 1,
          'last_activity': '2026-06-01T00:00:00Z',
        },
        {'current_phase': 'noid'}, // no project_id → skipped
      ]);
      expect(folded.keys, ['p1']);
      final p1 = folded['p1']!;
      expect(p1.currentPhase, 'build');
      expect(p1.phaseIndex, 2);
      expect(p1.phasesTotal, 5);
      expect(p1.progress, 0.4);
      expect(p1.openCriteria, 3);
      expect(p1.lastActivity, '2026-06-01T00:00:00Z');
    });

    test('tolerates string-encoded numbers', () {
      final folded = foldProjectInsights([
        {'project_id': 'p1', 'phase_index': '4', 'progress': '0.25'},
      ]);
      expect(folded['p1']!.phaseIndex, 4);
      expect(folded['p1']!.progress, 0.25);
    });
  });

  group('applyProjectFilter', () {
    final items = [
      proj('a', name: 'Zeta', status: 'active', created: '2026-01-01'),
      proj('b', name: 'alpha', status: 'archived', created: '2026-03-01'),
      proj('c', name: 'Mu', status: 'active', created: '2026-02-01'),
    ];

    test('status active hides archived; archived shows only archived', () {
      // (default sort reorders, so compare membership, not position.)
      const active = ProjectListFilter(status: ProjectStatusFilter.active);
      const arch = ProjectListFilter(status: ProjectStatusFilter.archived);
      expect(applyProjectFilter(items, active, const {}, const {})
          .map((p) => p['id']).toSet(), {'a', 'c'});
      expect(applyProjectFilter(items, arch, const {}, const {})
          .map((p) => p['id']).toSet(), {'b'});
    });

    test('status all keeps both and does not mutate the caller list', () {
      const all = ProjectListFilter(
          status: ProjectStatusFilter.all, sort: ProjectSortMode.createdDesc);
      final out = applyProjectFilter(items, all, const {}, const {});
      expect(out.length, 3);
      expect(items.map((p) => p['id']), ['a', 'b', 'c']); // unchanged
    });

    test('needsMeOnly keeps projects with open attention OR open criteria', () {
      const f = ProjectListFilter(
          status: ProjectStatusFilter.all, needsMeOnly: true);
      final out = applyProjectFilter(
        items,
        f,
        const {'a': 1}, // a: open attention
        {'b': insight(openCriteria: 2)}, // b: open criteria
      );
      expect(out.map((p) => p['id']).toSet(), {'a', 'b'});
    });

    test('sort name is case-insensitive A-Z', () {
      const f = ProjectListFilter(
          status: ProjectStatusFilter.all, sort: ProjectSortMode.name);
      expect(applyProjectFilter(items, f, const {}, const {})
          .map((p) => p['name']), ['alpha', 'Mu', 'Zeta']);
    });

    test('sort createdDesc is newest first', () {
      const f = ProjectListFilter(
          status: ProjectStatusFilter.all, sort: ProjectSortMode.createdDesc);
      expect(applyProjectFilter(items, f, const {}, const {})
          .map((p) => p['id']), ['b', 'c', 'a']);
    });

    test('sort recentActivity prefers insight lastActivity, falls back to '
        'created_at', () {
      const f = ProjectListFilter(
          status: ProjectStatusFilter.all, sort: ProjectSortMode.recentActivity);
      // a gets a fresh insight activity; b/c fall back to created_at.
      final out = applyProjectFilter(
        items,
        f,
        const {},
        {'a': insight(lastActivity: '2026-12-31T00:00:00Z')},
      );
      // a (insight 2026-12) > b (created 2026-03) > c (created 2026-02).
      expect(out.map((p) => p['id']), ['a', 'b', 'c']);
    });
  });

  group('partitionProjectsByKind', () {
    test('splits standing into workspaces, everything else into goals', () {
      final (:goals, :standings) = partitionProjectsByKind([
        proj('g1', kind: 'goal'),
        proj('w1', kind: 'standing'),
        proj('g2', kind: ''), // empty → goal
      ]);
      expect(goals.map((p) => p['id']), ['g1', 'g2']);
      expect(standings.map((p) => p['id']), ['w1']);
    });
  });

  group('flattenProjectsWithChildren', () {
    test('inlines children under their parent with depth + childCount', () {
      final rows = [
        proj('parent'),
        proj('child1', parent: 'parent'),
        proj('child2', parent: 'parent'),
        proj('solo'),
      ];
      final nodes = flattenProjectsWithChildren(rows);
      expect(nodes.map((n) => '${n.project['id']}@${n.depth}'),
          ['parent@0', 'child1@1', 'child2@1', 'solo@0']);
      expect(nodes.first.childCount, 2);
    });

    test('orphan child (parent not in section) renders at depth 0', () {
      final rows = [proj('orphan', parent: 'archived-elsewhere')];
      final nodes = flattenProjectsWithChildren(rows);
      expect(nodes.single.project['id'], 'orphan');
      expect(nodes.single.depth, 0);
    });
  });
}

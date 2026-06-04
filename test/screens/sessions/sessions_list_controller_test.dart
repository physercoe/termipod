import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/sessions_provider.dart';
import 'package:termipod/screens/sessions/sessions_list_controller.dart';

// WS2 (docs/plans/internal-techdebt-cleanup.md): the Sessions-list bucketing
// is extracted into a non-widget seam so it can be unit-tested directly.
// These pin the grouping the SessionsScreen used to assemble inline —
// steward grouping, current-vs-previous split, worker exclusion, the
// orphan/detached bucket, and the steward categorisation.

Map<String, dynamic> agent(
  String id,
  String handle,
  String kind,
  String status, {
  String projectId = '',
}) =>
    {
      'id': id,
      'handle': handle,
      'kind': kind,
      'status': status,
      'project_id': projectId,
    };

Map<String, dynamic> session(String id, String agentId, String status) =>
    {'id': id, 'current_agent_id': agentId, 'status': status};

StewardGroup group(String id, String handle, {String projectId = ''}) =>
    StewardGroup(
      agent: {'id': id, 'handle': handle, 'project_id': projectId},
      current: null,
      previous: const [],
    );

void main() {
  group('categorizeStewardGroup', () {
    test('general steward handle → general', () {
      expect(categorizeStewardGroup(group('s1', '@steward')),
          StewardCategory.general);
    });
    test('project-bound by @steward.<pid8> handle → project', () {
      expect(categorizeStewardGroup(group('s1', '@steward.abc12345')),
          StewardCategory.project);
    });
    test('project-bound by non-empty project_id → project', () {
      expect(
          categorizeStewardGroup(
              group('s1', 'research-steward', projectId: 'p1')),
          StewardCategory.project);
    });
    test('unbound domain steward → domain', () {
      expect(categorizeStewardGroup(group('s1', 'research-steward')),
          StewardCategory.domain);
    });
    test('empty agentId → detached', () {
      expect(categorizeStewardGroup(group('', 'Detached sessions')),
          StewardCategory.detached);
    });
  });

  group('groupSessionsBySteward', () {
    test('buckets current/previous, excludes workers, collects orphans', () {
      final agents = [
        agent('s-gen', '@steward', 'steward.v1', 'running'),
        agent('s-dom', 'research-steward', 'steward.research', 'running'),
        // worker (non-steward, project-bound) — its session is excluded.
        agent('w1', 'worker-1', 'claude-code', 'running', projectId: 'p1'),
      ];
      final sessions = SessionsState(
        active: [
          session('ses1', 's-gen', 'active'),
          session('wses', 'w1', 'active'), // worker → excluded
          session('orph', 'ghost', 'active'), // no live steward → detached
        ],
        previous: [
          session('ses0', 's-gen', 'archived'),
        ],
      );

      final groups = groupSessionsBySteward(agents, sessions);

      // s-gen (has current) sorts before s-dom (session-less); detached last.
      expect(groups.length, 3);
      expect(groups[0].agentId, 's-gen');
      expect(groups[0].current?['id'], 'ses1');
      expect(groups[0].previous.map((s) => s['id']), ['ses0']);

      expect(groups[1].agentId, 's-dom');
      expect(groups[1].current, isNull);
      expect(groups[1].previous, isEmpty);

      final detached = groups.last;
      expect(categorizeStewardGroup(detached), StewardCategory.detached);
      expect(detached.current, isNull);
      expect(detached.previous.map((s) => s['id']), ['orph']);
      // orphan active session is rendered as paused (engine is gone).
      expect(detached.previous.single['status'], 'paused');

      // the worker session never surfaces in any group.
      final allIds = groups
          .expand((g) => [
                if (g.current != null) g.current!['id'],
                ...g.previous.map((s) => s['id']),
              ])
          .toSet();
      expect(allIds.contains('wses'), isFalse);
    });

    test('terminated steward is excluded; its session falls to detached', () {
      final agents = [
        agent('s-dead', 'infra-steward', 'steward.infra', 'terminated'),
      ];
      final sessions = SessionsState(
        active: [session('s', 's-dead', 'active')],
      );
      final groups = groupSessionsBySteward(agents, sessions);
      // no live steward group; the session is an orphan → one detached group.
      expect(groups.length, 1);
      expect(categorizeStewardGroup(groups.single), StewardCategory.detached);
      expect(groups.single.previous.single['id'], 's');
    });

    test('empty inputs → no groups', () {
      expect(groupSessionsBySteward(const [], const SessionsState()), isEmpty);
    });
  });
}

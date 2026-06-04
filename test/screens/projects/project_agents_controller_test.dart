import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/sessions_provider.dart';
import 'package:termipod/screens/projects/project_agents_controller.dart';

// WS2 (docs/plans/internal-techdebt-cleanup.md): the project Agents view's
// live+stopped row reconciliation is extracted into a non-widget seam so it
// can be unit-tested directly. These tests pin the merge the widget used to
// assemble inline — including the v1.0.799 stop-vs-archive fact (a Stop stays
// visible as resumable; an Archive drops to history).

Map<String, dynamic> agent(String id, String projectId, String status) =>
    {'id': id, 'project_id': projectId, 'status': status};

Map<String, dynamic> sess(String agentId, String status) =>
    {'current_agent_id': agentId, 'status': status};

void main() {
  const proj = 'proj_1';

  group('projectLiveAgentRows', () {
    test('keeps only rows whose project_id matches', () {
      final all = [
        agent('a1', proj, 'running'),
        agent('a2', 'other', 'running'),
        agent('a3', proj, 'running'),
      ];
      final live = projectLiveAgentRows(all, proj);
      expect(live.map((a) => a['id']), ['a1', 'a3']);
    });

    test('empty roster → empty', () {
      expect(projectLiveAgentRows(const [], proj), isEmpty);
    });
  });

  group('projectStoppedAgentRows', () {
    test('terminated + paused session → included (resumable)', () {
      final state = SessionsState(active: [sess('t1', 'paused')]);
      final stopped = projectStoppedAgentRows(
        terminated: [agent('t1', proj, 'terminated')],
        liveIds: const {},
        sessions: state,
      );
      expect(stopped.map((a) => a['id']), ['t1']);
    });

    test('terminated + archived session → excluded (permanent history)', () {
      final state = SessionsState(previous: [sess('t1', 'archived')]);
      final stopped = projectStoppedAgentRows(
        terminated: [agent('t1', proj, 'terminated')],
        liveIds: const {},
        sessions: state,
      );
      expect(stopped, isEmpty);
    });

    test('terminated + no matching session → excluded (unknown, not resumable)',
        () {
      final stopped = projectStoppedAgentRows(
        terminated: [agent('t1', proj, 'terminated')],
        liveIds: const {},
        sessions: const SessionsState(),
      );
      expect(stopped, isEmpty);
    });

    test('dedup: a terminated id already live is dropped', () {
      final state = SessionsState(active: [sess('t1', 'paused')]);
      final stopped = projectStoppedAgentRows(
        terminated: [agent('t1', proj, 'terminated')],
        liveIds: const {'t1'},
        sessions: state,
      );
      expect(stopped, isEmpty);
    });
  });

  group('projectAgentRows (full merge)', () {
    test('live first, then stopped-resumable, deduped', () {
      final all = [
        agent('live1', proj, 'running'),
        agent('other', 'proj_2', 'running'),
      ];
      final terminated = [
        agent('stopped1', proj, 'terminated'), // paused → resumable, kept
        agent('archived1', proj, 'terminated'), // archived → dropped
        agent('live1', proj, 'terminated'), // dup of a live id → dropped
      ];
      final sessions = SessionsState(
        active: [sess('live1', 'active'), sess('stopped1', 'paused')],
        previous: [sess('archived1', 'archived')],
      );
      final rows = projectAgentRows(
        all: all,
        terminated: terminated,
        sessions: sessions,
        projectId: proj,
      );
      // other-project agent excluded; live before stopped; archived + dup gone.
      expect(rows.map((a) => a['id']), ['live1', 'stopped1']);
    });

    test('no terminated agents → just the live rows', () {
      final all = [agent('live1', proj, 'running')];
      final rows = projectAgentRows(
        all: all,
        terminated: const [],
        sessions: const SessionsState(),
        projectId: proj,
      );
      expect(rows.map((a) => a['id']), ['live1']);
    });
  });
}

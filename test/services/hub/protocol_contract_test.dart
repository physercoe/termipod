import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/sessions_provider.dart';
import 'package:termipod/services/hub/agent_status.dart';

// WS3 (docs/plans/internal-techdebt-cleanup.md): pin the hub→app JSON
// contract for the two highest-churn shapes — sessions and agents — at parse
// time, so a renamed/dropped hub field fails in CI instead of rendering a
// blank card on a device. The app reads these as Map<String, dynamic> by
// design (no DTOs); the fixtures + the pure resolvers are the safety net.
//
// Fixtures mirror the hub structs:
//   test/fixtures/sessions_list.json → sessionOut (handlers_sessions.go)
//   test/fixtures/agents_list.json   → agentOut   (handlers_agents.go)

List<Map<String, dynamic>> _loadList(String path) {
  final raw = jsonDecode(File(path).readAsStringSync()) as List;
  return raw.map((e) => (e as Map).cast<String, dynamic>()).toList();
}

void main() {
  late List<Map<String, dynamic>> sessions;
  late List<Map<String, dynamic>> agents;

  setUpAll(() {
    sessions = _loadList('test/fixtures/sessions_list.json');
    agents = _loadList('test/fixtures/agents_list.json');
  });

  group('session JSON contract', () {
    test('load-bearing fields are present under their hub key names', () {
      // These are the keys the mobile surfaces actually read; if the hub
      // renames one this assertion catches it before a screen does.
      for (final s in sessions) {
        expect(s.containsKey('id'), isTrue, reason: 'session.id');
        expect(s.containsKey('status'), isTrue, reason: 'session.status');
        expect(s.containsKey('current_agent_id'), isTrue,
            reason: 'session.current_agent_id (drives resumability)');
        expect(s.containsKey('scope_kind'), isTrue, reason: 'session.scope_kind');
        expect(s.containsKey('scope_id'), isTrue, reason: 'session.scope_id');
      }
    });

    test('bucketSessions splits live/paused into active, archived into previous',
        () {
      final state = bucketSessions(sessions);
      final activeIds =
          state.active.map((s) => (s['id'] ?? '').toString()).toSet();
      final previousIds =
          state.previous.map((s) => (s['id'] ?? '').toString()).toSet();
      // active session + paused (stopped, resumable) session are "live".
      expect(activeIds, containsAll(<String>{'ses_live01', 'ses_stopped1'}));
      // archived session falls through to previous.
      expect(previousIds, contains('ses_archived1'));
      expect(activeIds, isNot(contains('ses_archived1')));
    });

    test('status vocabulary is the ADR-009 set (no legacy strings in fixture)',
        () {
      const adr009 = {'active', 'paused', 'archived', 'deleted'};
      for (final s in sessions) {
        expect(adr009, contains((s['status'] ?? '').toString()),
            reason: 'fixture should track the current vocab');
      }
    });
  });

  group('agent JSON contract', () {
    test('load-bearing fields are present under their hub key names', () {
      for (final a in agents) {
        expect(a.containsKey('id'), isTrue, reason: 'agent.id');
        expect(a.containsKey('status'), isTrue, reason: 'agent.status');
        expect(a.containsKey('project_id'), isTrue, reason: 'agent.project_id');
      }
    });
  });

  group('stop-vs-archive resolution over the fixtures (v1.0.799)', () {
    // The end-to-end resolution the lifecycle UI depends on: an agent's row
    // status alone can't tell Stop from Archive (both 'terminated'); the
    // session it fronts (matched by current_agent_id) is the discriminator.
    late SessionsState state;
    setUpAll(() => state = bucketSessions(sessions));

    String labelFor(String agentId) {
      final agent = agents.firstWhere((a) => a['id'] == agentId);
      final status = (agent['status'] ?? '').toString();
      final resumable =
          agentResumability(sessionStatusForAgent(state, agentId));
      return agentStatusLabelResumable(status, resumable);
    }

    test('paused session → "stopped" (resumable)', () {
      expect(sessionStatusForAgent(state, 'agt_stopped1'), 'paused');
      expect(agentResumability(sessionStatusForAgent(state, 'agt_stopped1')),
          AgentResumability.resumable);
      expect(labelFor('agt_stopped1'), 'stopped');
    });

    test('archived session → "archived" (permanent)', () {
      expect(sessionStatusForAgent(state, 'agt_archived1'), 'archived');
      expect(agentResumability(sessionStatusForAgent(state, 'agt_archived1')),
          AgentResumability.permanent);
      expect(labelFor('agt_archived1'), 'archived');
    });

    test('live agent resolves its active session, not a terminal label', () {
      expect(sessionStatusForAgent(state, 'agt_live01'), 'active');
      // running agent keeps its own label; resumability is "unknown" (not a
      // terminal row) and never coerces a live agent to stopped/archived.
      expect(labelFor('agt_live01'), isNot('stopped'));
      expect(labelFor('agt_live01'), isNot('archived'));
    });

    test('no matching session → empty status (cold snapshot tolerated)', () {
      expect(sessionStatusForAgent(state, 'agt_does_not_exist'), '');
      expect(agentResumability(''), AgentResumability.unknown);
    });
  });
}

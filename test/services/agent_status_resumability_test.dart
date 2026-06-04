import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/hub/agent_status.dart';
import 'package:termipod/providers/sessions_provider.dart';

// Stop and Archive BOTH leave agents.status = 'terminated' (hub
// handlers_agents.go); the fact that distinguishes a resumable run from a
// permanent one lives on the SESSION (Stop → paused, Archive → archived).
// These tests pin the mobile-side resolution so the "what does ended mean,
// paused or archived?" ambiguity can't quietly come back.
void main() {
  group('agentResumability', () {
    test('paused session → resumable', () {
      expect(agentResumability('paused'), AgentResumability.resumable);
      // The legacy 'interrupted' alias was retired in W1.3 — it now reads as
      // unknown (the conservative fallback) like any unrecognised status.
      expect(agentResumability('interrupted'), AgentResumability.unknown);
    });

    test('archived / deleted session → permanent', () {
      expect(agentResumability('archived'), AgentResumability.permanent);
      expect(agentResumability('deleted'), AgentResumability.permanent);
    });

    test('active / empty / unknown session → unknown', () {
      expect(agentResumability('active'), AgentResumability.unknown);
      expect(agentResumability(''), AgentResumability.unknown);
      expect(agentResumability('whatever'), AgentResumability.unknown);
    });
  });

  group('agentStatusLabelResumable', () {
    test('terminated + resumable → "stopped"', () {
      expect(
        agentStatusLabelResumable('terminated', AgentResumability.resumable),
        'stopped',
      );
    });

    test('terminated + permanent / unknown → "archived" (glossary word)', () {
      expect(
        agentStatusLabelResumable('terminated', AgentResumability.permanent),
        'archived',
      );
      expect(
        agentStatusLabelResumable('terminated', AgentResumability.unknown),
        'archived',
      );
    });

    test('non-terminated statuses defer to agentStatusLabel', () {
      // Resumability must not bleed into live/other states.
      expect(
        agentStatusLabelResumable('running', AgentResumability.resumable),
        'running',
      );
      expect(
        agentStatusLabelResumable('failed', AgentResumability.resumable),
        'failed',
      );
      expect(
        agentStatusLabelResumable('archived', AgentResumability.unknown),
        'archived',
      );
    });
  });

  group('sessionStatusForAgent', () {
    Map<String, dynamic> sess(String agentId, String status) =>
        {'current_agent_id': agentId, 'status': status};

    test('matches the session an agent fronts, across both buckets', () {
      final state = SessionsState(
        active: [sess('a-live', 'active'), sess('a-stopped', 'paused')],
        previous: [sess('a-ended', 'archived')],
      );
      expect(sessionStatusForAgent(state, 'a-stopped'), 'paused');
      expect(sessionStatusForAgent(state, 'a-ended'), 'archived');
      expect(sessionStatusForAgent(state, 'a-live'), 'active');
    });

    test('no match or null state → empty string', () {
      expect(sessionStatusForAgent(null, 'x'), '');
      expect(
        sessionStatusForAgent(const SessionsState(), 'x'),
        '',
      );
      expect(
        sessionStatusForAgent(
          SessionsState(active: [sess('other', 'paused')]),
          'x',
        ),
        '',
      );
    });

    test('empty agentId never matches', () {
      final state = SessionsState(active: [sess('', 'paused')]);
      expect(sessionStatusForAgent(state, ''), '');
    });

    test('end-to-end: stopped vs archived agent resolves to the right label',
        () {
      final state = SessionsState(
        active: [sess('stopped-agent', 'paused')],
        previous: [sess('archived-agent', 'archived')],
      );
      expect(
        agentStatusLabelResumable(
          'terminated',
          agentResumability(sessionStatusForAgent(state, 'stopped-agent')),
        ),
        'stopped',
      );
      expect(
        agentStatusLabelResumable(
          'terminated',
          agentResumability(sessionStatusForAgent(state, 'archived-agent')),
        ),
        'archived',
      );
    });
  });
}

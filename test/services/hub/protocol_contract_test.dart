import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/sessions_provider.dart';
import 'package:termipod/services/hub/agent_status.dart';

// WS3 (docs/plans/internal-techdebt-cleanup.md): pin the hub→app JSON
// contract for the three highest-churn shapes — sessions, agents, and the
// session digest + turn index — at parse time, so a renamed/dropped hub field
// fails in CI instead of rendering a blank card on a device. The app reads
// these as Map<String, dynamic> by design (no DTOs); the fixtures + the pure
// resolvers are the safety net.
//
// Fixtures mirror the hub structs:
//   test/fixtures/sessions_list.json  → sessionOut  (handlers_sessions.go)
//   test/fixtures/agents_list.json    → agentOut    (handlers_agents.go)
//   test/fixtures/session_digest.json → digestJSON  (handlers_agent_digest.go)
//   test/fixtures/agent_turns.json    → turnJSON    (handlers_agent_turns.go)

List<Map<String, dynamic>> _loadList(String path) {
  final raw = jsonDecode(File(path).readAsStringSync()) as List;
  return raw.map((e) => (e as Map).cast<String, dynamic>()).toList();
}

Map<String, dynamic> _loadMap(String path) =>
    (jsonDecode(File(path).readAsStringSync()) as Map).cast<String, dynamic>();

void main() {
  late List<Map<String, dynamic>> sessions;
  late List<Map<String, dynamic>> agents;
  late Map<String, dynamic> digest;
  late Map<String, dynamic> turnsBody;

  setUpAll(() {
    sessions = _loadList('test/fixtures/sessions_list.json');
    agents = _loadList('test/fixtures/agents_list.json');
    digest = _loadMap('test/fixtures/session_digest.json');
    turnsBody = _loadMap('test/fixtures/agent_turns.json');
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

  group('session digest JSON contract (ADR-038 §5)', () {
    test('headline fields RunReportCard reads are present', () {
      // run_report_card.dart pulls each of these by key; a rename here would
      // blank the report card on a device.
      for (final key in <String>[
        'event_count',
        'turn_count',
        'error_count',
        'tool_total',
        'tool_failed',
        'cost_usd',
        'duration_ms',
        'active_ms',
        'outcome',
        'last_ts',
        'by_model',
        'errors',
        'latency',
      ]) {
        expect(digest.containsKey(key), isTrue, reason: 'digest.$key');
      }
      // session rollup swaps agent_id for session_id + agent_ids.
      expect(digest.containsKey('session_id'), isTrue);
      expect(digest['agent_ids'], isA<List<dynamic>>());
    });

    test('by_model aggregates carry in/out token keys', () {
      // _modelRow reads m['in'] / m['out']; the digest agg uses those keys
      // (not tokens_in/tokens_out — that is the /v1/insights shape).
      final byModel = digest['by_model'] as Map<String, dynamic>;
      expect(byModel, isNotEmpty);
      for (final agg in byModel.values) {
        final m = (agg as Map).cast<String, dynamic>();
        expect(m.containsKey('in'), isTrue, reason: 'by_model.*.in');
        expect(m.containsKey('out'), isTrue, reason: 'by_model.*.out');
      }
    });

    test('error classes keep sample arrays aligned 1:1 (940d20a)', () {
      // The Errors lens jumps via (ts, seq); sample_ts must stay aligned with
      // sample_seqs or a jump lands on the wrong event. Pin that alignment.
      final errors = digest['errors'] as Map<String, dynamic>;
      expect(errors, isNotEmpty);
      for (final v in errors.values) {
        final cls = (v as Map).cast<String, dynamic>();
        final seqs = cls['sample_seqs'] as List;
        final tss = cls['sample_ts'] as List;
        final labels = cls['sample_labels'] as List;
        expect(cls['count'], isA<int>(), reason: 'errors.*.count');
        expect(tss.length, seqs.length, reason: 'sample_ts ⟷ sample_seqs');
        expect(labels.length, seqs.length,
            reason: 'sample_labels ⟷ sample_seqs');
      }
    });

    test('first-error-seq traversal (RunReportCard) finds the min sample', () {
      // Mirror run_report_card._firstErrorSeq: scan every class, take the
      // smallest first-sample seq. Proves the nested errors map is navigable.
      final errors = digest['errors'] as Map<String, dynamic>;
      int? best;
      for (final v in errors.values) {
        final seqs = (v as Map)['sample_seqs'];
        if (seqs is List && seqs.isNotEmpty) {
          final s = seqs.first as int;
          if (best == null || s < best) best = s;
        }
      }
      expect(best, 212); // tool:Bash 212 < error:rate_limit 301 < tool:Bash 418
    });

    test('latency exposes percentile + histogram fields', () {
      final latency = digest['latency'] as Map<String, dynamic>;
      for (final key in <String>['p50_ms', 'p95_ms', 'bounds', 'counts']) {
        expect(latency.containsKey(key), isTrue, reason: 'latency.$key');
      }
      // counts has len(bounds)+1 buckets (the trailing overflow bucket).
      final bounds = latency['bounds'] as List;
      final counts = latency['counts'] as List;
      expect(counts.length, bounds.length + 1);
    });
  });

  group('agent_turns JSON contract (turn index)', () {
    test('listing wraps turns[] under the session keys', () {
      expect(turnsBody.containsKey('turns'), isTrue);
      expect(turnsBody['turns'], isA<List<dynamic>>());
      expect(turnsBody.containsKey('agent_ids'), isTrue);
    });

    test('every turn carries start_seq — the loader window anchor', () {
      // The mobile loader resets the All-view window around start_seq; without
      // it, jump-to-turn cannot land.
      final turns = (turnsBody['turns'] as List).cast<Map>();
      expect(turns, isNotEmpty);
      for (final t in turns) {
        final row = t.cast<String, dynamic>();
        for (final key in <String>[
          'turn_id',
          'idx',
          'start_seq',
          'start_ts',
          'status',
          'open',
        ]) {
          expect(row.containsKey(key), isTrue, reason: 'turn.$key');
        }
      }
    });

    test('open turn is flagged open with a zero end_seq', () {
      final turns = (turnsBody['turns'] as List).cast<Map>();
      final open = turns
          .map((t) => t.cast<String, dynamic>())
          .firstWhere((t) => t['open'] == true);
      // scanTurns sets open = (end_seq == 0); a live in-progress turn.
      expect(open['end_seq'], 0);
      expect(open['status'], 'in_progress');
    });
  });
}

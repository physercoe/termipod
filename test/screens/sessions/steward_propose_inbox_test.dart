import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/sessions/widgets/steward_propose_inbox.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W19 — steward-side propose inbox.
//
// Covers:
//   * stewardProposeInboxRows predicate — the 4-clause filter that
//     scopes the global attention list to "rows THIS steward should
//     act on" (kind=propose, assigned_tier=project-steward,
//     status=open, project_id matches the steward's project)
//   * StewardProposeInboxPill widget — self-gating visibility based
//     on agentKind + projectId + row count
//
// The Screen variant (StewardProposeInboxScreen) is exercised via
// the pill's Navigator.push integration; its body is just a ListView
// over the same predicate.

Map<String, dynamic> _row({
  String kind = 'propose',
  String assignedTier = 'project-steward',
  String status = 'open',
  String projectId = 'proj-research',
  String changeKind = 'task.set_status',
}) {
  return {
    'id': 'att-${DateTime.now().microsecondsSinceEpoch}',
    'kind': kind,
    'change_kind': changeKind,
    'assigned_tier': assignedTier,
    'status': status,
    'project_id': projectId,
    'change_spec': {'status': 'done'},
    'target_ref': {'task_id': 'task-x', 'project_id': projectId},
    'summary': 'Propose task close-out',
  };
}

Widget _wrap(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(appBar: AppBar(actions: [child]), body: const SizedBox()),
    ),
  );
}

void main() {
  group('stewardProposeInboxRows — predicate', () {
    test('returns empty list when no attention rows exist', () {
      expect(stewardProposeInboxRows(const [], 'proj-x'), isEmpty);
    });

    test('matches the canonical 4-clause filter', () {
      final items = [
        _row(), // matches
        _row(kind: 'approval_request'), // wrong kind
        _row(assignedTier: 'principal'), // wrong tier
        _row(status: 'resolved'), // wrong status
        _row(projectId: 'proj-other'), // wrong project
      ];
      final out = stewardProposeInboxRows(items, 'proj-research');
      expect(out.length, 1);
    });

    test('returns multiple matches preserving original order', () {
      final items = [
        _row(changeKind: 'task.set_status'),
        _row(changeKind: 'deliverable.set_state'),
        _row(changeKind: 'phase.advance'),
      ];
      final out = stewardProposeInboxRows(items, 'proj-research');
      expect(out.length, 3);
      expect(out[0]['change_kind'], 'task.set_status');
      expect(out[1]['change_kind'], 'deliverable.set_state');
      expect(out[2]['change_kind'], 'phase.advance');
    });

    test('empty projectId narrows to zero (defensive — no row matches)', () {
      final items = [_row(projectId: 'proj-x')];
      expect(stewardProposeInboxRows(items, ''), isEmpty);
    });

    test('legacy row without project_id field never matches', () {
      // Pre-ADR-030 rows lack project_id; defaulting to '' (via
      // ?? '') means the predicate rejects them. Defensive — the
      // steward inbox is intentionally narrow.
      final items = [
        {
          'id': 'legacy',
          'kind': 'propose',
          'assigned_tier': 'project-steward',
          'status': 'open',
          // no project_id
        },
      ];
      expect(stewardProposeInboxRows(items, 'proj-research'), isEmpty);
    });
  });

  group('StewardProposeInboxPill — visibility gating', () {
    testWidgets('hidden when agentKind is not a steward kind', (tester) async {
      await tester.pumpWidget(_wrap(
        const StewardProposeInboxPill(
          agentKind: 'coder.v1', // worker, not steward
          projectId: 'proj-x',
        ),
      ));
      await tester.pumpAndSettle();

      expect(find.byIcon(Icons.inbox_outlined), findsNothing);
    });

    testWidgets('hidden when projectId is empty', (tester) async {
      await tester.pumpWidget(_wrap(
        const StewardProposeInboxPill(
          agentKind: 'steward.research.v1',
          projectId: '', // team-scoped general steward
        ),
      ));
      await tester.pumpAndSettle();

      expect(find.byIcon(Icons.inbox_outlined), findsNothing);
    });

    testWidgets('hidden when there are no matching rows', (tester) async {
      // ProviderScope without any hubProvider override → hub state is
      // null → no matching rows → pill stays hidden.
      await tester.pumpWidget(_wrap(
        const StewardProposeInboxPill(
          agentKind: 'steward.research.v1',
          projectId: 'proj-research',
        ),
      ));
      await tester.pumpAndSettle();

      expect(find.byIcon(Icons.inbox_outlined), findsNothing);
    });
  });
}

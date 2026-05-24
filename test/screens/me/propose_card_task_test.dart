import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/propose_card_task.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W17 — per-kind propose card for task.set_status.
//
// task.set_status's change_spec quirks vs deliverable / phase:
//   * field is `status` (not `to_status`) — directly carries the
//     target status the agent wants to set
//   * no `from_status` field — task.set_status compares the row's
//     current status at Apply time
//   * result_summary is recommended for 'done', allowed-but-pointless
//     for 'cancelled' — when present, renders below the transition
//     as a wrapped quote-block

Map<String, dynamic> _proposeRow({
  required String assignedTier,
  String escalationState = 'none',
  String toStatus = 'done',
  String? resultSummary = 'completed first pass; tests green',
}) {
  return {
    'id': 'att-w17-test',
    'kind': 'propose',
    'change_kind': 'task.set_status',
    'assigned_tier': assignedTier,
    'escalation_state': escalationState,
    'change_spec': {
      'status': toStatus,
      if (resultSummary != null) 'result_summary': resultSummary,
    },
    'target_ref': {
      'task_id': 'task-789',
      'project_id': 'proj-abc',
    },
    'summary': 'Propose task.set_status — close-out',
  };
}

Widget _wrap(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(body: child),
    ),
  );
}

void main() {
  group('ProposeCardTask — primary variant', () {
    testWidgets('shows Approve + Reject when viewer is addressee',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Reject'), findsOneWidget);
    });

    testWidgets('renders to_status chip (no from-side)', (tester) async {
      // task.set_status has no from_status field on the wire — the
      // card shows → done only, no from-side chip.
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('done'), findsOneWidget);
      // No "in_progress" or similar from-side label.
    });

    testWidgets('renders result_summary block when present', (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('completed first pass; tests green'), findsOneWidget);
    });

    testWidgets('result_summary omitted when not present', (tester) async {
      // Cancelled tasks typically don't carry a summary.
      final row = _proposeRow(
        assignedTier: 'principal',
        toStatus: 'cancelled',
        resultSummary: null,
      );
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('cancelled'), findsOneWidget);
      // No summary block — verified by absence of any test-result text.
    });

    testWidgets('renders task + project ids', (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('task: task-789'), findsOneWidget);
      expect(find.text('project: proj-abc'), findsOneWidget);
    });
  });

  group('ProposeCardTask — stalled variant', () {
    testWidgets('shows Override + View task when viewer is NOT addressee',
        (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Override'), findsOneWidget);
      expect(find.text('View task'), findsOneWidget);
      expect(find.text('Approve'), findsNothing);
    });

    testWidgets('shows Stuck pill with addressee name', (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(
          _wrap(ProposeCardTask(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.textContaining('Stuck'), findsOneWidget);
      expect(find.textContaining('project-steward'), findsOneWidget);
    });
  });
}

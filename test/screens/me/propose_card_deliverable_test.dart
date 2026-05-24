import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/propose_card_deliverable.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W15 — per-kind propose card for deliverable.set_state.
//
// Renders one of two variants based on isAddresseeOfPropose:
//   * primary — viewer IS addressee: Approve / Reject
//   * stalled — viewer is NOT addressee but escalation_state surfaced
//     the row to them: Override / View deliverable + top "Stuck" pill
//
// Tests below assert the visible affordances per variant. Decide flow
// itself is not exercised here (covered by hub-side decide handler
// tests); the card is responsible only for routing the action to the
// correct backend call shape.

Map<String, dynamic> _proposeRow({
  required String assignedTier,
  String escalationState = 'none',
  String fromState = 'draft',
  String toState = 'ratified',
}) {
  return {
    'id': 'att-w15-test',
    'kind': 'propose',
    'change_kind': 'deliverable.set_state',
    'assigned_tier': assignedTier,
    'escalation_state': escalationState,
    'change_spec': {
      'from_state': fromState,
      'to_state': toState,
    },
    'target_ref': {
      'deliverable_id': 'del-abc-123',
    },
    'summary': 'Propose deliverable.set_state — initial draft reviewed',
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
  group('ProposeCardDeliverable — primary variant', () {
    testWidgets('shows Approve + Reject when viewer is addressee',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Reject'), findsOneWidget);
      expect(find.text('Override'), findsNothing);
    });

    testWidgets('renders state-transition chips (draft → ratified)',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.text('draft'), findsOneWidget);
      expect(find.text('ratified'), findsOneWidget);
    });

    testWidgets('renders summary + deliverable id', (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(
          find.text('Propose deliverable.set_state — initial draft reviewed'),
          findsOneWidget);
      expect(find.text('deliverable: del-abc-123'), findsOneWidget);
    });

    testWidgets('no stalled pill when escalation_state == none',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.textContaining('Stuck'), findsNothing);
    });
  });

  group('ProposeCardDeliverable — stalled variant', () {
    testWidgets('shows Override + View deliverable when viewer is NOT addressee',
        (tester) async {
      // Row addressed to project-steward; viewer is principal; row has
      // escalated to principal via the loop sweep.
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.text('Override'), findsOneWidget);
      expect(find.text('View deliverable'), findsOneWidget);
      expect(find.text('Approve'), findsNothing);
    });

    testWidgets('shows Stuck pill with addressee name', (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.textContaining('Stuck'), findsOneWidget);
      expect(find.textContaining('project-steward'), findsOneWidget);
    });

    testWidgets('body block unchanged in stalled variant', (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      // State transition + summary + deliverable id all still rendered.
      expect(find.text('draft'), findsOneWidget);
      expect(find.text('ratified'), findsOneWidget);
      expect(find.text('deliverable: del-abc-123'), findsOneWidget);
    });
  });

  group('ProposeCardDeliverable — variant edge cases', () {
    testWidgets('addressed-to-addressee but stalled → primary variant',
        (tester) async {
      // Defensive: if escalation_state walked to principal but the
      // row is ALREADY addressed to principal, the addressee predicate
      // wins. (Unusual state — typically the row's assigned_tier stays
      // unchanged through the walk per D-7 Option 2′ "decision stays".)
      final row = _proposeRow(
        assignedTier: 'principal',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Override'), findsNothing);
    });

    testWidgets('legacy row (no assigned_tier) → primary variant',
        (tester) async {
      // Pre-ADR-030 rows lack assigned_tier — render primary so the
      // viewer can still act on them.
      final row = _proposeRow(assignedTier: '');
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.text('Approve'), findsOneWidget);
    });

    testWidgets('change_spec parses from raw JSON string too', (tester) async {
      // hub_client decodes change_spec as a Map most of the time, but
      // defensive parsing handles a JSON-string carrier (e.g. when the
      // wire was stringified upstream).
      final row = {
        'id': 'att-1',
        'kind': 'propose',
        'change_kind': 'deliverable.set_state',
        'assigned_tier': 'principal',
        'escalation_state': 'none',
        'change_spec': '{"from_state":"in_review","to_state":"ratified"}',
        'target_ref': '{"deliverable_id":"del-xyz"}',
        'summary': 'Propose deliverable.set_state',
      };
      await tester.pumpWidget(_wrap(
        ProposeCardDeliverable(attention: row, myTier: 'principal'),
      ));
      await tester.pumpAndSettle();

      expect(find.text('in_review'), findsOneWidget);
      expect(find.text('ratified'), findsOneWidget);
      expect(find.text('deliverable: del-xyz'), findsOneWidget);
    });
  });
}

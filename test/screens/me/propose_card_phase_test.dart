import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/propose_card_phase.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W16 — per-kind propose card for phase.advance.
//
// Sibling to propose_card_deliverable_test.dart — same variant logic,
// different field shape (from_phase / to_phase instead of from_state /
// to_state; project_id instead of deliverable_id). Tests the
// kind-specific rendering quirks (the omitted from_phase case is
// unique to phase.advance — its optimistic-concurrency check is
// optional).

Map<String, dynamic> _proposeRow({
  required String assignedTier,
  String escalationState = 'none',
  String? fromPhase = 'discovery',
  String toPhase = 'design',
}) {
  return {
    'id': 'att-w16-test',
    'kind': 'propose',
    'change_kind': 'phase.advance',
    'assigned_tier': assignedTier,
    'escalation_state': escalationState,
    'change_spec': {
      if (fromPhase != null) 'from_phase': fromPhase,
      'to_phase': toPhase,
    },
    'target_ref': {'project_id': 'proj-abc-123'},
    'summary': 'Propose phase.advance — discovery criteria all met',
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
  group('ProposeCardPhase — primary variant', () {
    testWidgets('shows Approve + Reject when viewer is addressee',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardPhase(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Reject'), findsOneWidget);
      expect(find.text('Override'), findsNothing);
    });

    testWidgets('renders phase-transition chips (discovery → design)',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardPhase(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('discovery'), findsOneWidget);
      expect(find.text('design'), findsOneWidget);
    });

    testWidgets('renders project id', (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardPhase(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('project: proj-abc-123'), findsOneWidget);
    });
  });

  group('ProposeCardPhase — stalled variant', () {
    testWidgets('shows Override + View project when viewer is NOT addressee',
        (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(
          _wrap(ProposeCardPhase(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Override'), findsOneWidget);
      expect(find.text('View project'), findsOneWidget);
      expect(find.text('Approve'), findsNothing);
    });

    testWidgets('shows Stuck pill with addressee name', (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(
          _wrap(ProposeCardPhase(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.textContaining('Stuck'), findsOneWidget);
      expect(find.textContaining('project-steward'), findsOneWidget);
    });
  });

  group('ProposeCardPhase — from_phase omitted (forced advance)', () {
    testWidgets('renders → to_phase without from-side chip', (tester) async {
      // phase.advance accepts a forced advance when from_phase is omitted
      // (the optimistic-concurrency check is opt-in). The card renders
      // the arrow + to_phase chip; no from-side chip appears.
      final row = _proposeRow(assignedTier: 'principal', fromPhase: null);
      await tester.pumpWidget(
          _wrap(ProposeCardPhase(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('design'), findsOneWidget);
      // 'discovery' (default fromPhase) is omitted via fromPhase: null →
      // the from-side chip should not render.
      expect(find.text('discovery'), findsNothing);
    });
  });
}

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/propose_card_project_create.dart';

import '../../helpers/test_helpers.dart';

// WS4 / ADR-046 — per-kind propose card for project.create. change_spec
// carries the full inline project spec; the director reviews it before
// approving (approval materializes the project).

const _spec = '''
phases:
  - alpha
  - beta
phase_specs:
  alpha:
    criteria:
      - id: a
        kind: text
        body: {text: x}
''';

Map<String, dynamic> _proposeRow({
  required String assignedTier,
  String escalationState = 'none',
  String name = 'data-migration',
  String goal = 'Migrate the dataset',
  String steward = 'agents.steward.code-migration',
  String configYaml = _spec,
}) {
  return {
    'id': 'att-ws4-test',
    'kind': 'propose',
    'change_kind': 'project.create',
    'assigned_tier': assignedTier,
    'escalation_state': escalationState,
    'change_spec': {
      'name': name,
      'goal': goal,
      'kind': 'goal',
      'on_create_template_id': steward,
      'config_yaml': configYaml,
    },
    'summary': 'Propose project.create — $name',
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
  group('ProposeCardProjectCreate — primary variant', () {
    testWidgets('shows Approve + Reject when viewer is addressee',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardProjectCreate(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Approve'), findsOneWidget);
      expect(find.text('Reject'), findsOneWidget);
    });

    testWidgets('renders name, goal, steward, and a phase count',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardProjectCreate(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('data-migration'), findsOneWidget);
      expect(find.text('Migrate the dataset'), findsOneWidget);
      expect(find.text('steward: agents.steward.code-migration'),
          findsOneWidget);
      expect(find.text('2 phases'), findsOneWidget);
    });

    testWidgets('View spec opens the full config_yaml in a sheet',
        (tester) async {
      final row = _proposeRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardProjectCreate(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('View spec'), findsOneWidget);
      await tester.tap(find.text('View spec'));
      await tester.pumpAndSettle();

      // The sheet header + the raw spec body are shown for review.
      expect(find.textContaining('Spec — data-migration'), findsOneWidget);
      expect(find.textContaining('phase_specs'), findsOneWidget);
    });
  });

  group('ProposeCardProjectCreate — stalled variant', () {
    testWidgets('shows Override + View spec when viewer is NOT addressee',
        (tester) async {
      final row = _proposeRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(
          _wrap(ProposeCardProjectCreate(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Override'), findsOneWidget);
      expect(find.text('View spec'), findsOneWidget);
      expect(find.text('Approve'), findsNothing);
    });
  });
}
